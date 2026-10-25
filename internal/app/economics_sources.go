package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/sources"
	sourcepg "github.com/onixus/metis/internal/economics/sources/pgstore"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
	"github.com/shopspring/decimal"
)

type economicsPlatformSources struct {
	app             *App
	captureIdentity *App
	bookSnapshot    *economics.Snapshot
	teamMapping     *worklogTeams
}

func (a *App) buildFormulaSources() {
	var store sources.Store = sources.NewMemStore()
	if a.db != nil {
		store = sourcepg.New(a.db)
	}
	a.FormulaSources = sources.NewService(store, a.Audit, kernel.SystemClock{})
	a.Modeling.WithPlatformSources(economicsPlatformSources{app: a})
}
func (p economicsPlatformSources) ReadFormulaSource(ctx context.Context, sc authz.Scope, kind modeling.PlatformSourceKind, sl modeling.Slice) (modeling.PlatformValue, error) {
	ctx = modeling.WithFormulaSnapshot(ctx)
	if p.app.FormulaSources == nil || p.app.Modeling == nil {
		return modeling.PlatformValue{}, kernel.ErrUnavailable
	}
	if sc.Finance() < authz.FinanceFull {
		return modeling.PlatformValue{}, kernel.ErrForbidden
	}
	if err := sc.Require(authz.ActionReadModelFinance, sl.ProductID); err != nil {
		return modeling.PlatformValue{}, err
	}
	if sl.Period.IsZero() || sl.Item != "" {
		return modeling.PlatformValue{}, kernel.Invalid("slice", "source needs month and does not support article attribution")
	}
	if sl.ProductID == kernel.NilID {
		return p.aggregate(ctx, sc, kind, sl)
	}
	key := sources.Key{ProductID: sl.ProductID, Period: sl.Period.String(), Kind: string(kind), TeamID: sl.TeamID}
	closed, err := p.app.Modeling.PeriodClosed(ctx, sc, sl.Period)
	if err != nil {
		return modeling.PlatformValue{}, err
	}
	if closed {
		snap, err := p.app.FormulaSources.Latest(ctx, sc, key)
		if errors.Is(err, kernel.ErrNotFound) {
			return modeling.PlatformValue{}, fmt.Errorf("%w: closed period has no captured formula source", kernel.ErrUnavailable)
		}
		return platformSnapshot(snap), err
	}
	var in sources.Input
	switch kind {
	case modeling.SourceWorklogs:
		in, err = p.worklogs(ctx, sc, sl)
	case modeling.SourceCRMRevenue:
		in, err = p.crmRevenue(ctx, sc, sl)
	case modeling.SourceHeadcount:
		in, err = p.headcount(ctx, sc, sl)
	default:
		err = kernel.Invalid("source", "unknown platform source")
	}
	if err != nil {
		return modeling.PlatformValue{}, err
	}
	snap, err := p.app.FormulaSources.Capture(ctx, sc, in)
	if err != nil {
		return modeling.PlatformValue{}, err
	}
	return platformSnapshot(snap), nil
}
func platformSnapshot(s sources.Snapshot) modeling.PlatformValue {
	v := modeling.PlatformValue{Value: s.Value, Currency: s.Currency, SnapshotID: s.ID.String(), AsOf: s.AsOf, Provenance: make([]modeling.PlatformProvenance, 0, len(s.Provenance)+1)}
	v.Provenance = append(v.Provenance, modeling.PlatformProvenance{Source: "economics.formula_sources", RecordID: s.ID.String(), Version: s.Revision()})
	for _, p := range s.Provenance {
		v.Provenance = append(v.Provenance, modeling.PlatformProvenance{Source: p.Source, RecordID: p.RecordID, Version: p.Version})
	}
	return v
}
func contentHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("source canonical content: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func sourceInput(sl modeling.Slice, kind modeling.PlatformSourceKind, value int64, currency, origin, hash string) sources.Input {
	return sources.Input{Key: sources.Key{ProductID: sl.ProductID, Period: sl.Period.String(), Kind: string(kind), TeamID: sl.TeamID}, Value: decimal.NewFromInt(value), Currency: currency, AsOf: time.Now().UTC(), Provenance: []sources.Provenance{{Source: origin, RecordID: hash, Version: sl.Period.String()}}}
}
func addSourceInt(a, b int64) (int64, error) {
	if b < 0 || a > math.MaxInt64-b {
		return 0, kernel.Invalid("source", "nonnegative int64 aggregate overflow")
	}
	return a + b, nil
}

func (p economicsPlatformSources) crmRevenue(ctx context.Context, sc authz.Scope, sl modeling.Slice) (sources.Input, error) {
	if p.app.CRM == nil {
		return sources.Input{}, fmt.Errorf("%w: CRM source disabled", kernel.ErrUnavailable)
	}
	if sl.TeamID != kernel.NilID {
		return sources.Input{}, kernel.Invalid("team", "CRM revenue has no authoritative team attribution")
	}
	product, err := p.app.Portfolio.Product(ctx, sc, sl.ProductID)
	if err != nil {
		return sources.Input{}, err
	}
	deals, err := p.crmDeals(ctx, sc)
	if err != nil {
		return sources.Input{}, fmt.Errorf("%w: complete CRM projection unavailable", kernel.ErrUnavailable)
	}
	if len(deals) > 100000 {
		return sources.Input{}, kernel.Invalid("crm", "deal snapshot limit exceeded")
	}
	currency := p.app.Modeling.Config().Currency
	selected := []ports.Deal{}
	seen := map[string]string{}
	var total int64
	for _, deal := range deals {
		if err := ctx.Err(); err != nil {
			return sources.Input{}, err
		}
		if deal.Outcome != ports.DealWon {
			continue
		}
		if deal.ExternalID == "" || deal.ClosedDate.IsZero() || deal.ProductKey == "" {
			return sources.Input{}, kernel.Invalid("crm", "won deal requires stable identity, closed date and primary product")
		}
		if deal.ClosedDate.Before(sl.Period.Start()) || deal.ClosedDate.After(sl.Period.End()) {
			continue
		}
		hash, err := contentHash(deal)
		if err != nil {
			return sources.Input{}, err
		}
		if previous, ok := seen[deal.ExternalID]; ok {
			if previous != hash {
				return sources.Input{}, kernel.Invalid("crm", "conflicting duplicate deal")
			}
			continue
		}
		seen[deal.ExternalID] = hash
		if deal.ProductKey != product.Key {
			continue
		}
		if deal.Amount.Currency != currency || deal.Amount.Amount < 0 {
			return sources.Input{}, kernel.Invalid("currency", "won revenue must use model currency and nonnegative minor units")
		}
		total, err = addSourceInt(total, deal.Amount.Amount)
		if err != nil {
			return sources.Input{}, err
		}
		selected = append(selected, deal)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ExternalID < selected[j].ExternalID })
	hash, err := contentHash(selected)
	if err != nil {
		return sources.Input{}, err
	}
	return sourceInput(sl, modeling.SourceCRMRevenue, total, currency, "crm.won_revenue.aggregate", hash), nil
}

func (p economicsPlatformSources) worklogs(ctx context.Context, sc authz.Scope, sl modeling.Slice) (sources.Input, error) {
	if p.app.Tracker == nil || p.app.Delivery == nil {
		return sources.Input{}, fmt.Errorf("%w: worklog source disabled", kernel.ErrUnavailable)
	}
	cfg, err := p.sourceTeams(ctx, sc)
	if err != nil {
		return sources.Input{}, err
	}
	team, err := p.teamKey(ctx, sc, sl.TeamID, teamKeys(cfg.AuthorTeam))
	if err != nil {
		return sources.Input{}, err
	}
	snapshot := economics.Snapshot{SnapshotInfo: economics.SnapshotInfo{ProductID: sl.ProductID}}
	// A configured financial book declares the complete cross-product ownership
	// universe, so one tracker issue cannot be counted in two product slices.
	if strings.TrimSpace(p.app.Cfg.FormulaBookProductID) != "" || sc.SeesAllProducts() {
		candidate, readErr := p.book(ctx, sc, sl.Period.String())
		if readErr == nil {
			snapshot = candidate
		} else if strings.TrimSpace(p.app.Cfg.FormulaBookProductID) != "" || !errors.Is(readErr, kernel.ErrUnavailable) {
			return sources.Input{}, readErr
		}
	}
	capture, err := p.worklogInput(ctx, sc, sl.Period, snapshot)
	if err != nil {
		return sources.Input{}, err
	}
	cfg, issues := capture.Teams, capture.Issues
	keys := make([]string, 0, len(issues))
	for key, product := range issues {
		if product == sl.ProductID {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return sources.Input{}, fmt.Errorf("%w: product has no authoritative mapped worklog coverage", kernel.ErrUnavailable)
	}
	sort.Strings(keys)
	start, end := sl.Period.Start().Time(), sl.Period.End().AddDays(1).Time()
	logs := capture.Logs
	seen := map[string]ports.Worklog{}
	selected := []ports.Worklog{}
	var seconds int64
	for _, log := range logs {
		if log.Started.Before(start) || !log.Started.Before(end) {
			continue
		}
		owner, mappedIssue := issues[log.IssueKey]
		if !mappedIssue {
			return sources.Input{}, kernel.Invalid("worklogs", "worklog issue not mapped")
		}
		if owner != sl.ProductID {
			continue
		}
		if log.ExternalID == "" || log.Spent <= 0 || log.Spent%time.Second != 0 {
			return sources.Input{}, kernel.Invalid("worklogs", "invalid issue identity or duration")
		}
		mapped, ok := cfg.AuthorTeam[log.Author]
		if !ok {
			return sources.Input{}, kernel.Invalid("worklogs", "author has no declared team")
		}
		key := log.IssueKey + "/" + log.ExternalID
		if prev, ok := seen[key]; ok {
			if prev != log {
				return sources.Input{}, kernel.Invalid("worklogs", "conflicting duplicate worklog")
			}
			continue
		}
		seen[key] = log
		if team != "" && team != mapped {
			continue
		}
		seconds, err = addSourceInt(seconds, int64(log.Spent/time.Second))
		if err != nil {
			return sources.Input{}, err
		}
		selected = append(selected, log)
	}
	sort.Slice(selected, func(i, j int) bool {
		a, b := selected[i], selected[j]
		if a.IssueKey != b.IssueKey {
			return a.IssueKey < b.IssueKey
		}
		return a.ExternalID < b.ExternalID
	})
	hash, err := contentHash(struct {
		Logs   []ports.Worklog
		Teams  map[string]string
		Issues []string
	}{selected, cfg.AuthorTeam, keys})
	if err != nil {
		return sources.Input{}, err
	}
	return sourceInput(sl, modeling.SourceWorklogs, seconds, "", "delivery.worklogs.aggregate", hash), nil
}
func (p economicsPlatformSources) sourceTeams(ctx context.Context, sc authz.Scope) (worklogTeams, error) {
	if p.teamMapping != nil {
		return kernel.CloneValue(*p.teamMapping), nil
	}
	return modeling.CaptureFormulaInput(ctx, sc, p.captureProvider(), "worklog-teams", func(ctx context.Context) (worklogTeams, error) {
		mapping, err := loadWorklogTeams(ctx, p.app.Cfg.WorklogTeamsFile)
		if err != nil {
			if ctx.Err() != nil {
				return worklogTeams{}, ctx.Err()
			}
			return worklogTeams{}, fmt.Errorf("%w: authoritative worklog team mapping is unavailable", kernel.ErrUnavailable)
		}
		return mapping, nil
	})
}

func teamKeys(in map[string]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, key := range in {
		if !seen[key] {
			out = append(out, key)
			seen[key] = true
		}
	}
	sort.Strings(out)
	return out
}
func (p economicsPlatformSources) teamKey(ctx context.Context, sc authz.Scope, id kernel.ID, candidates []string) (string, error) {
	if id == kernel.NilID {
		return "", nil
	}
	for _, key := range candidates {
		hash, err := bookTeamID(key)
		if err != nil {
			return "", err
		}
		if hash == id {
			return key, nil
		}
	}
	if sc.SeesAllProducts() {
		teams, err := p.app.Modeling.Teams(ctx, sc)
		if err != nil {
			return "", err
		}
		for _, team := range teams {
			if team.ID == id {
				for _, candidate := range candidates {
					if candidate == team.Key {
						return candidate, nil
					}
				}
			}
		}
	}
	return "", kernel.Invalid("team", "team is not explicitly mapped to this source")
}

func (p economicsPlatformSources) bookOwner(ctx context.Context, sc authz.Scope, period modeling.Period) (kernel.ID, error) {
	raw := strings.TrimSpace(p.app.Cfg.FormulaBookProductID)
	if raw != "" {
		id, err := kernel.ParseID(raw)
		if err != nil {
			return kernel.NilID, kernel.Invalid("formula_book_product_id", "invalid configured book UUID")
		}
		return id, nil
	}
	id, err := p.app.Modeling.CanonicalBookOwner(ctx, sc, period)
	if errors.Is(err, kernel.ErrNotFound) || errors.Is(err, kernel.ErrUnavailable) {
		return kernel.NilID, fmt.Errorf("%w: configure a formula book or unify this period", kernel.ErrUnavailable)
	}
	return id, err
}
func (p economicsPlatformSources) book(ctx context.Context, sc authz.Scope, period string) (economics.Snapshot, error) {
	if p.bookSnapshot != nil {
		if p.bookSnapshot.Period != period {
			return economics.Snapshot{}, kernel.Invalid("period", "captured book period mismatch")
		}
		if err := economics.RequireSnapshot(sc, *p.bookSnapshot, authz.ActionReadFinance); err != nil {
			return economics.Snapshot{}, err
		}
		return kernel.CloneValue(*p.bookSnapshot), nil
	}

	return modeling.CaptureFormulaInput(ctx, sc, p.captureProvider(), "book:"+period, func(ctx context.Context) (economics.Snapshot, error) {
		parsed, err := modeling.ParsePeriod(period)
		if err != nil {
			return economics.Snapshot{}, err
		}
		id, err := p.bookOwner(ctx, sc, parsed)
		if err != nil {
			return economics.Snapshot{}, err
		}
		return p.app.Economics.Snapshot(ctx, sc, id, period, 0)
	})
}
func (p economicsPlatformSources) headcount(ctx context.Context, sc authz.Scope, sl modeling.Slice) (sources.Input, error) {
	snapshot, err := p.book(ctx, sc, sl.Period.String())
	if err != nil {
		return sources.Input{}, err
	}
	return p.headcountSnapshot(ctx, sc, sl, snapshot)
}
func (p economicsPlatformSources) headcountSnapshot(ctx context.Context, sc authz.Scope, sl modeling.Slice, snapshot economics.Snapshot) (sources.Input, error) {
	var err error

	keys := []string{}
	for _, r := range snapshot.Rows {
		if r.Category == economics.Payroll && r.TeamID != "" {
			keys = append(keys, r.TeamID)
		}
	}
	team, err := p.teamKey(ctx, sc, sl.TeamID, keys)
	if err != nil {
		return sources.Input{}, err
	}
	counts := map[string]int{}
	for _, row := range snapshot.Rows {
		if row.Category != economics.Payroll {
			continue
		}
		if row.TeamID == "" || row.Headcount <= 0 {
			return sources.Input{}, kernel.Invalid("headcount", "payroll requires declared positive team headcount")
		}
		if old, ok := counts[row.TeamID]; ok && old != row.Headcount {
			return sources.Input{}, kernel.Invalid("headcount", "conflicting headcount for shared team")
		}
		counts[row.TeamID] = row.Headcount
	}
	participating := map[string]bool{}
	for _, row := range snapshot.Rows {
		if row.Category != economics.Payroll || (team != "" && row.TeamID != team) {
			continue
		}
		matches := sl.ProductID == kernel.NilID || row.ProductID == sl.ProductID
		if len(row.Allocations) > 0 && sl.ProductID != kernel.NilID {
			matches = false
			for _, a := range row.Allocations {
				if a.ProductID == sl.ProductID && a.Share.IsPositive() {
					matches = true
				}
			}
		}
		if matches {
			participating[row.TeamID] = true
		}
	}
	if len(participating) == 0 {
		return sources.Input{}, fmt.Errorf("%w: no authoritative payroll headcount for slice", kernel.ErrUnavailable)
	}
	var total int64
	for team := range participating {
		total, err = addSourceInt(total, int64(counts[team]))
		if err != nil {
			return sources.Input{}, err
		}
	}
	hash, err := contentHash(struct {
		Book    kernel.ID
		Version int
		Teams   map[string]bool
		Counts  map[string]int
	}{snapshot.ID, snapshot.Version, participating, counts})
	if err != nil {
		return sources.Input{}, err
	}
	out := sourceInput(sl, modeling.SourceHeadcount, total, "", "finance.payroll.headcount.aggregate", hash)
	out.Provenance = append(out.Provenance, sources.Provenance{Source: "economics.book", RecordID: snapshot.ID.String(), Version: fmt.Sprint(snapshot.Version)})
	return out, nil
}

// captureProvider is stable across private readers used for portfolio projections.
func (p economicsPlatformSources) captureProvider() *App {
	if p.captureIdentity != nil {
		return p.captureIdentity
	}
	return p.app
}

func (p economicsPlatformSources) crmDeals(ctx context.Context, sc authz.Scope) ([]ports.Deal, error) {
	return modeling.CaptureFormulaInput(ctx, sc, p.captureProvider(), "crm-deals", func(ctx context.Context) ([]ports.Deal, error) {
		deals, err := p.app.CRM.Deals(ctx)
		if err != nil {
			return nil, err
		}
		if len(deals) > 100000 {
			return nil, kernel.Invalid("crm", "deal snapshot limit exceeded")
		}
		return deals, nil
	})
}

type formulaWorklogInput struct {
	Logs   []ports.Worklog
	Issues map[string]kernel.ID
	Teams  worklogTeams
}

func (p economicsPlatformSources) worklogInput(ctx context.Context, sc authz.Scope, period modeling.Period, snapshot economics.Snapshot) (formulaWorklogInput, error) {
	key := "worklogs:" + period.String() + ":" + snapshot.ProductID.String() + ":" + snapshot.ID.String()
	return modeling.CaptureFormulaInput(ctx, sc, p.captureProvider(), key, func(ctx context.Context) (formulaWorklogInput, error) {
		mapping, err := p.sourceTeams(ctx, sc)
		if err != nil {
			return formulaWorklogInput{}, err
		}
		issues, err := p.app.financeWorklogIssues(ctx, sc, snapshot)
		if err != nil {
			return formulaWorklogInput{}, err
		}
		if len(issues) == 0 {
			return formulaWorklogInput{}, fmt.Errorf("%w: no authoritative mapped worklog coverage", kernel.ErrUnavailable)
		}
		keys := make([]string, 0, len(issues))
		for key := range issues {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		logs, err := p.app.Tracker.Worklogs(ctx, keys, period.Start().Time())
		if err != nil {
			return formulaWorklogInput{}, fmt.Errorf("%w: complete worklogs unavailable", kernel.ErrUnavailable)
		}
		if len(logs) > 100000 {
			return formulaWorklogInput{}, kernel.Invalid("worklogs", "worklog snapshot limit exceeded")
		}
		for _, log := range logs {
			if _, ok := issues[log.IssueKey]; !ok {
				return formulaWorklogInput{}, kernel.Invalid("worklogs", "worklog issue not mapped")
			}
		}
		return formulaWorklogInput{Logs: logs, Issues: issues, Teams: mapping}, nil
	})
}

func (p economicsPlatformSources) aggregate(ctx context.Context, sc authz.Scope, kind modeling.PlatformSourceKind, sl modeling.Slice) (modeling.PlatformValue, error) {
	if !sc.SeesAllProducts() {
		return modeling.PlatformValue{}, kernel.ErrForbidden
	}
	anchor, err := p.bookOwner(ctx, sc, sl.Period)
	if err != nil {
		return modeling.PlatformValue{}, err
	}
	key := sources.Key{ProductID: anchor, Period: sl.Period.String(), Kind: string(kind), TeamID: sl.TeamID, Portfolio: true}
	closed, err := p.app.Modeling.PeriodClosed(ctx, sc, sl.Period)
	if err != nil {
		return modeling.PlatformValue{}, err
	}
	if closed {
		snapshot, err := p.app.FormulaSources.Latest(ctx, sc, key)
		if errors.Is(err, kernel.ErrNotFound) {
			return modeling.PlatformValue{}, fmt.Errorf("%w: closed period has no captured portfolio source", kernel.ErrUnavailable)
		}
		return platformSnapshot(snapshot), err
	}
	snapshot, err := p.book(ctx, sc, sl.Period.String())
	if err != nil {
		return modeling.PlatformValue{}, err
	}
	if kind == modeling.SourceHeadcount {
		in, err := p.headcountSnapshot(ctx, sc, sl, snapshot)
		if err != nil {
			return modeling.PlatformValue{}, err
		}
		in.Key = key
		in.Provenance = append(in.Provenance, sources.Provenance{Source: "portfolio.headcount", RecordID: "deduplicated-teams", Version: sl.Period.String()})
		v, err := p.app.FormulaSources.Capture(ctx, sc, in)
		return platformSnapshot(v), err
	}
	products := map[kernel.ID]bool{}
	for _, row := range snapshot.Rows {
		products[row.ProductID] = true
		for _, a := range row.Allocations {
			products[a.ProductID] = true
		}
	}
	if len(products) == 0 {
		return modeling.PlatformValue{}, kernel.ErrUnavailable
	}
	ids := make([]kernel.ID, 0, len(products))
	for id := range products {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	if len(ids) > 128 {
		return modeling.PlatformValue{}, kernel.Invalid("products", "source product aggregate limit exceeded")
	}
	reader := economicsPlatformSources{app: p.app, captureIdentity: p.captureProvider(), bookSnapshot: &snapshot, teamMapping: p.teamMapping}
	input := sources.Input{Key: key, Value: decimal.Zero, AsOf: time.Now().UTC(), Provenance: []sources.Provenance{}}
	for _, id := range ids {
		sub := sl
		sub.ProductID = id
		v, err := reader.ReadFormulaSource(ctx, sc, kind, sub)
		if err != nil {
			return modeling.PlatformValue{}, err
		}
		input.Value = input.Value.Add(v.Value)
		if input.Value.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
			return modeling.PlatformValue{}, kernel.Invalid("value", "source aggregate exceeds int64")
		}
		input.Currency = v.Currency
		input.Provenance = append(input.Provenance, sources.Provenance{Source: "economics.formula_sources", RecordID: v.SnapshotID, Version: sl.Period.String()})
	}
	captured, err := p.app.FormulaSources.Capture(ctx, sc, input)
	if err != nil {
		return modeling.PlatformValue{}, err
	}
	return platformSnapshot(captured), nil
}
