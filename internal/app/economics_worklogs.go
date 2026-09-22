package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

type worklogTeams struct {
	AuthorTeam map[string]string `json:"author_team"`
}

func loadWorklogTeams(ctx context.Context, path string) (worklogTeams, error) {
	if err := ctx.Err(); err != nil {
		return worklogTeams{}, err
	}
	if path == "" {
		return worklogTeams{}, kernel.Invalid("worklog_teams", "worklog team mapping is not configured")
	}
	raw, err := readFinanceFile(path, 1<<20)
	if err != nil {
		return worklogTeams{}, fmt.Errorf("%w: cannot read worklog team mapping", kernel.ErrUnavailable)
	}
	var cfg worklogTeams
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return worklogTeams{}, kernel.Invalid("worklog_teams", "invalid team mapping JSON")
	}
	if len(cfg.AuthorTeam) == 0 || len(cfg.AuthorTeam) > 10000 {
		return worklogTeams{}, kernel.Invalid("worklog_teams", "team mapping must contain 1..10000 opaque authors")
	}
	for author, team := range cfg.AuthorTeam {
		if author == "" || len(author) > 256 || strings.TrimSpace(team) == "" || len(team) > 128 {
			return worklogTeams{}, kernel.Invalid("worklog_teams", "invalid author or team key")
		}
	}
	return cfg, nil
}

// ApplyFinanceWorklogs replaces allocation rules from an authoritative period snapshot.
// It stores only team totals and product shares, never tracker authors or individual salaries.
func (a *App) ApplyFinanceWorklogs(ctx context.Context, sc authz.Scope, book kernel.ID, period string, expectedVersion int, recalculate bool) (economics.Snapshot, error) {
	if err := sc.Require(authz.ActionWriteFinance, book); err != nil {
		return economics.Snapshot{}, err
	}
	if a.Tracker == nil || a.Delivery == nil {
		return economics.Snapshot{}, fmt.Errorf("%w: delivery tracker is disabled", kernel.ErrUnavailable)
	}
	start, err := time.Parse("2006-01", period)
	if err != nil || len(period) != 7 {
		return economics.Snapshot{}, kernel.Invalid("period", "expected YYYY-MM")
	}
	snapshot, err := a.Economics.Snapshot(ctx, sc, book, period, 0)
	if err != nil {
		return economics.Snapshot{}, err
	}
	if snapshot.Version != expectedVersion || (snapshot.Closed && !recalculate) {
		return economics.Snapshot{}, kernel.ErrConflict
	}
	cfg, err := loadWorklogTeams(ctx, a.Cfg.WorklogTeamsFile)
	if err != nil {
		return economics.Snapshot{}, err
	}
	issueProducts, err := a.financeWorklogIssues(ctx, sc, snapshot)
	if err != nil {
		return economics.Snapshot{}, err
	}
	issues := make([]string, 0, len(issueProducts))
	for key := range issueProducts {
		issues = append(issues, key)
	}
	sort.Strings(issues)
	if len(issues) == 0 {
		return economics.Snapshot{}, kernel.Invalid("worklogs", "book products have no mapped delivery issues")
	}
	logs, err := a.Tracker.Worklogs(ctx, issues, start)
	if err != nil {
		return economics.Snapshot{}, fmt.Errorf("%w: cannot read complete tracker worklogs", kernel.ErrUnavailable)
	}
	if len(logs) > 100000 {
		return economics.Snapshot{}, kernel.Invalid("worklogs", "worklog limit exceeded")
	}
	shares, err := worklogAllocations(logs, issueProducts, cfg.AuthorTeam, start, start.AddDate(0, 1, 0))
	if err != nil {
		return economics.Snapshot{}, err
	}
	changed := 0
	for i := range snapshot.Rows {
		row := &snapshot.Rows[i]
		if row.Category == economics.Revenue || row.TeamID == "" {
			continue
		}
		allocation, ok := shares[row.TeamID]
		if !ok {
			return economics.Snapshot{}, kernel.Invalid("worklogs", "a financial team has no worklogs in the selected period")
		}
		row.Allocations = allocation
		row.AllocationSource = "worklogs"
		changed++
	}
	if changed == 0 {
		return economics.Snapshot{}, kernel.Invalid("worklogs", "book contains no team cost rows")
	}
	return a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: book, Period: period, Currency: snapshot.Currency, ExpectedVersion: expectedVersion, Recalculate: recalculate,
		Source: snapshot.Source, SourceHash: snapshot.SourceHash, Rows: snapshot.Rows, Fields: snapshot.Fields})
}

func (a *App) financeWorklogIssues(ctx context.Context, sc authz.Scope, snapshot economics.Snapshot) (map[string]kernel.ID, error) {
	products := map[kernel.ID]bool{snapshot.ProductID: true}
	for _, row := range snapshot.Rows {
		products[row.ProductID] = true
		for _, allocation := range row.Allocations {
			products[allocation.ProductID] = true
		}
	}
	issues := map[string]kernel.ID{}
	for product := range products {
		if err := sc.Require(authz.ActionReadFinance, product); err != nil {
			return nil, err
		}
		mappings, err := a.Delivery.Mappings(ctx, sc, product)
		if err != nil {
			return nil, err
		}
		for _, mapping := range mappings {
			projection, state, err := a.Delivery.FeatureProjection(ctx, sc, mapping.FeatureID)
			if err != nil {
				return nil, fmt.Errorf("%w: delivery projection is incomplete", kernel.ErrUnavailable)
			}
			if state.Stale || projection.SyncedAt.IsZero() {
				return nil, fmt.Errorf("%w: synchronize delivery before computing worklog shares", kernel.ErrUnavailable)
			}
			keys := []string{mapping.EpicKey}
			for _, issue := range projection.Issues {
				keys = append(keys, issue.Key)
			}
			for _, key := range keys {
				if key == "" {
					return nil, kernel.Invalid("worklogs", "empty delivery issue key")
				}
				if previous, ok := issues[key]; ok && previous != product {
					return nil, fmt.Errorf("%w: delivery issue belongs to multiple financial products", kernel.ErrConflict)
				}
				issues[key] = product
			}
		}
	}
	return issues, nil
}

func worklogAllocations(logs []ports.Worklog, issueProducts map[string]kernel.ID, authorTeams map[string]string, start, end time.Time) (map[string][]economics.Allocation, error) {
	teams := map[string]map[kernel.ID]int64{}
	seen := map[string]ports.Worklog{}
	for _, log := range logs {
		if log.Started.Before(start) || !log.Started.Before(end) {
			continue
		}
		product, ok := issueProducts[log.IssueKey]
		if !ok {
			return nil, kernel.Invalid("worklogs", "tracker returned an unmapped issue")
		}
		team, ok := authorTeams[log.Author]
		if !ok {
			return nil, kernel.Invalid("worklogs", "worklogs include an author without an explicit team mapping")
		}
		if log.ExternalID == "" || log.Spent <= 0 {
			return nil, kernel.Invalid("worklogs", "worklog requires a stable ID and positive duration")
		}
		key := log.IssueKey + "/" + log.ExternalID
		if previous, ok := seen[key]; ok {
			if previous != log {
				return nil, fmt.Errorf("%w: conflicting duplicate worklog", kernel.ErrConflict)
			}
			continue
		}
		seen[key] = log
		if teams[team] == nil {
			teams[team] = map[kernel.ID]int64{}
		}
		previous := teams[team][product]
		duration := int64(log.Spent)
		if previous > math.MaxInt64-duration {
			return nil, kernel.Invalid("worklogs", "team duration overflow")
		}
		teams[team][product] = previous + duration
	}
	out := map[string][]economics.Allocation{}
	for team, products := range teams {
		var total int64
		ids := make([]kernel.ID, 0, len(products))
		for product, duration := range products {
			if total > math.MaxInt64-duration {
				return nil, kernel.Invalid("worklogs", "team duration overflow")
			}
			total += duration
			ids = append(ids, product)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
		allocations := make([]economics.Allocation, 0, len(ids))
		remainder := decimal.NewFromInt(1)
		for _, product := range ids {
			share := decimal.NewFromInt(products[product]).DivRound(decimal.NewFromInt(total), 16).Truncate(12)
			allocations = append(allocations, economics.Allocation{ProductID: product, Share: share})
			remainder = remainder.Sub(share)
		}
		// The final 12-decimal residual has a deterministic receiver; money allocation subsequently conserves every minor unit.
		allocations[0].Share = allocations[0].Share.Add(remainder)
		for _, allocation := range allocations {
			if !allocation.Share.GreaterThan(decimal.Zero) {
				return nil, kernel.Invalid("worklogs", "worklog share is below supported precision")
			}
		}
		out[team] = allocations
	}
	return out, nil
}
