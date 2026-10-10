//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/modeling/pgstore"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/ports"
	"github.com/shopspring/decimal"
)

func database(t *testing.T) *pgdb.DB {
	t.Helper()
	url := os.Getenv("METIS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("METIS_TEST_DATABASE_URL missing: docs/questions.md #05")
	}
	d, err := pgdb.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	if err := migrate.Up(context.Background(), d.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	return d
}
func service(t *testing.T, d *pgdb.DB, p kernel.Publisher) *economics.Service {
	t.Helper()
	s, err := economics.NewService(pgstore.New(d), p, kernel.SystemClock{}, economics.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEC08_EC09_EC10_EC11_PostgresHistoryAndConcurrentRevision(t *testing.T) {
	d := database(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("test")
	s := service(t, d, nil)
	key := "field_" + strings.ReplaceAll(kernel.NewID().String(), "-", "_")
	in := economics.FieldInput{Key: key, Name: "Synthetic", Type: economics.FieldNumber, Source: economics.SourceManual, EffectiveFrom: kernel.DateOf(2026, 1, 1)}
	first, err := s.SaveField(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Name = "Synthetic v2"
	in.EffectiveFrom = kernel.DateOf(2026, 2, 1)
	if _, err := s.SaveField(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	reader := pgstore.New(database(t))
	got, err := reader.Field(ctx, sc, key)
	if err != nil || len(got.Versions) != 2 || got.Versions[0].Name != first.Versions[0].Name {
		t.Fatalf("history: %+v %v", got, err)
	}
	broken := kernel.CloneValue(got)
	broken.Versions[0].Name = "rewrite"
	broken.Versions = append(broken.Versions, economics.FieldVersion{Version: 3})
	if err := reader.SaveField(ctx, sc, broken); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("rewrote history: %v", err)
	}
	next := kernel.CloneValue(got)
	next.Versions = append(next.Versions, economics.FieldVersion{Version: 3, Name: "Synthetic v3"})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, st := range []*pgstore.Store{pgstore.New(d), reader} {
		wg.Add(1)
		go func(st *pgstore.Store) { defer wg.Done(); results <- st.SaveField(ctx, sc, next) }(st)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, kernel.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent revision: %d/%d", success, conflict)
	}
	metricKey := "metric_" + strings.ReplaceAll(kernel.NewID().String(), "-", "_")
	if _, err := s.SaveMetric(ctx, sc, economics.MetricInput{Key: metricKey, Name: "Synthetic metric", Expression: `field("` + key + `") * 2`}); err != nil {
		t.Fatal(err)
	}
	readService := service(t, database(t), nil)
	if _, err := readService.SaveMetric(ctx, sc, economics.MetricInput{Key: metricKey, Name: "Cycle", Expression: `metric("` + metricKey + `")`}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("cycle after restart: %v", err)
	}
	metrics, err := reader.Metrics(ctx, sc)
	if err != nil || len(metrics) == 0 {
		t.Fatalf("metrics: %v", err)
	}
}

func TestEC07_EC12_PostgresAllObjectsAndFactVersions(t *testing.T) {
	d := database(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("test")
	store := pgstore.New(d)
	reader := pgstore.New(database(t))
	period := economics.PeriodOf(2090, time.Month(1+time.Now().UnixNano()%12))
	product := kernel.NewID()
	team := economics.Team{ID: kernel.NewID(), Key: kernel.NewID().String(), Name: "Synthetic"}
	tpl := economics.Template{ID: kernel.NewID(), Name: "Synthetic"}
	scenario := economics.Scenario{ID: kernel.NewID(), Name: "Synthetic", Products: []kernel.ID{product}}
	allocation := economics.AllocationRule{ID: kernel.NewID(), HubProductID: product, Version: 1}
	bundle := economics.BundleRule{ID: kernel.NewID(), BundleKey: kernel.NewID().String(), Version: 1, Shares: map[kernel.ID]decimal.Decimal{product: decimal.NewFromInt(1)}}
	for _, err := range []error{store.SaveTemplate(ctx, sc, tpl), store.SaveTeam(ctx, sc, team), store.SaveScenario(ctx, sc, scenario), store.SaveAllocationRule(ctx, sc, allocation), store.SaveBundleRule(ctx, sc, bundle), store.SaveTeamShares(ctx, sc, []economics.TeamShare{{TeamID: team.ID, ProductID: product, Period: period, Share: decimal.NewFromInt(1)}}), store.ClosePeriod(ctx, sc, period, "synthetic")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reader.Template(ctx, sc, tpl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Scenario(ctx, sc, scenario.ID); err != nil {
		t.Fatal(err)
	}
	for _, check := range []func() error{
		func() error {
			v, e := reader.Teams(ctx, sc)
			if len(v) == 0 {
				t.Error("teams lost")
			}
			return e
		},
		func() error {
			v, e := reader.TeamShares(ctx, sc, period)
			if len(v) == 0 {
				t.Error("shares lost")
			}
			return e
		},
		func() error {
			v, e := reader.AllocationRules(ctx, sc)
			if len(v) == 0 {
				t.Error("allocation lost")
			}
			return e
		},
		func() error {
			v, e := reader.BundleRules(ctx, sc)
			if len(v) == 0 {
				t.Error("bundle lost")
			}
			return e
		},
		func() error {
			v, e := reader.ClosedPeriods(ctx, sc)
			if len(v) == 0 {
				t.Error("closed lost")
			}
			return e
		}} {
		if err := check(); err != nil {
			t.Fatal(err)
		}
	}
	// Use a unique year/month derived from the DB's current batch history.
	batches, err := store.Batches(ctx, sc, period)
	if err != nil {
		t.Fatal(err)
	}
	version := 1
	for _, b := range batches {
		if b.DataVersion >= version {
			version = b.DataVersion + 1
		}
	}
	var first economics.ImportBatch
	for i := 0; i < 2; i++ {
		b := economics.ImportBatch{ID: kernel.NewID(), Period: period, DataVersion: version + i, Status: economics.BatchApplied}
		if i == 0 {
			first = b
		}
		if err := store.SaveBatch(ctx, sc, b); err != nil {
			t.Fatal(err)
		}
		row := economics.FactRow{ID: kernel.NewID(), BatchID: b.ID, ProductID: product, TeamID: team.ID, Period: period, DataVersion: b.DataVersion, Value: decimal.NewFromInt(int64(100 + i))}
		if err := store.AppendFacts(ctx, sc, []economics.FactRow{row}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendFacts(ctx, sc, []economics.FactRow{row}); !errors.Is(err, kernel.ErrConflict) {
			t.Fatalf("fact overwritten: %v", err)
		}
	}
	if err := store.SaveBatch(ctx, sc, first); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("batch rewritten: %v", err)
	}
	for _, v := range []int{version, version + 1} {
		rows, err := reader.Facts(ctx, sc, economics.FactFilter{Product: &product, DataVersion: &v})
		if err != nil || len(rows) != 1 || !rows[0].Value.Equal(decimal.NewFromInt(int64(100+v-version))) {
			t.Fatalf("version %d: %+v %v", v, rows, err)
		}
	}
	rows, err := reader.Facts(ctx, sc, economics.FactFilter{Product: &product})
	if err != nil || len(rows) != 1 || rows[0].DataVersion != version+1 {
		t.Fatalf("latest: %+v %v", rows, err)
	}
}

type readerStub struct{ field string }

func (r readerStub) Read(_ context.Context, _ io.Reader, _ ports.FinanceMapping) (ports.FinanceReadResult, error) {
	return ports.FinanceReadResult{Cells: []ports.FinanceCell{{FieldKey: r.field, Period: "2091-01", Value: decimal.NewFromInt(10)}}}, nil
}

type failPublisher struct{}

func (failPublisher) Publish(context.Context, ...kernel.Event) error { return kernel.ErrUnavailable }

type failAuditor struct{}

func (failAuditor) FinanceAccess(context.Context, string, string, string, kernel.ID, map[string]any) error {
	return kernel.ErrUnavailable
}

func TestEC07_EC11_PostgresImportRollbackAndManualRollback(t *testing.T) {
	d := database(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("test")
	store := pgstore.New(d)
	s := service(t, d, nil)
	key := "import_" + strings.ReplaceAll(kernel.NewID().String(), "-", "_")
	if _, err := s.SaveField(ctx, sc, economics.FieldInput{Key: key, Name: "Synthetic", Type: economics.FieldNumber, Source: economics.SourceImport}); err != nil {
		t.Fatal(err)
	}
	tpl, err := s.SaveTemplate(ctx, sc, economics.TemplateInput{Name: "Synthetic", Sheets: []economics.SheetMap{{Sheet: "Sheet1", Columns: []economics.ColumnMap{{Column: "A", FieldKey: key}}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.WithImport(readerStub{key}, nil)
	before, err := store.Batches(ctx, sc, economics.Period{})
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"publisher", "audit"} {
		broken := service(t, d, nil).WithImport(readerStub{key}, nil)
		if failure == "publisher" {
			broken = service(t, d, failPublisher{}).WithImport(readerStub{key}, nil)
		} else {
			broken.WithAuditor(failAuditor{})
		}
		if _, err := broken.ApplyImport(ctx, sc, economics.ImportInput{TemplateID: tpl.ID, Data: []byte("synthetic")}); !errors.Is(err, kernel.ErrUnavailable) {
			t.Fatalf("%s failure: %v", failure, err)
		}
		after, err := store.Batches(ctx, sc, economics.Period{})
		if err != nil || len(before) != len(after) {
			t.Fatalf("partial batches: %v %v", after, err)
		}
		rows, err := store.Facts(ctx, sc, economics.FactFilter{FieldKey: key})
		if err != nil || len(rows) != 0 {
			t.Fatalf("partial facts: %+v %v", rows, err)
		}
	}
	result, err := s.ApplyImport(ctx, sc, economics.ImportInput{TemplateID: tpl.ID, Data: []byte("synthetic")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pgstore.New(database(t)).Batch(ctx, sc, result.Batch.ID); err != nil {
		t.Fatal(err)
	}
	manualKey := "manual_" + strings.ReplaceAll(kernel.NewID().String(), "-", "_")
	if _, err := s.SaveField(ctx, sc, economics.FieldInput{Key: manualKey, Name: "Synthetic", Type: economics.FieldNumber, Source: economics.SourceManual}); err != nil {
		t.Fatal(err)
	}
	s.WithAuditor(failAuditor{})
	if _, err := s.SetManualValue(ctx, sc, manualKey, economics.Slice{Period: economics.PeriodOf(2092, 1)}, decimal.NewFromInt(1)); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatal(err)
	}
	rows, err := store.Facts(ctx, sc, economics.FactFilter{FieldKey: manualKey})
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial manual value: %v %v", rows, err)
	}
}

func TestNFS02_NFS16_AD02_PostgresScopeBeforeIOAndProductIsolation(t *testing.T) {
	ctx := context.Background()
	s := pgstore.New(nil)
	for _, sc := range []authz.Scope{{}, authz.New(authz.Params{Subject: "no-finance", AllProducts: authz.AccessPrivate})} {
		if _, err := s.Fields(ctx, sc); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatal(err)
		}
		if _, err := s.Facts(ctx, sc, economics.FactFilter{}); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatal(err)
		}
		if err := s.SaveTeam(ctx, sc, economics.Team{}); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatal(err)
		}
		if err := s.Transact(ctx, sc, func(context.Context) error { t.Fatal("unauthorized transaction"); return nil }); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatal(err)
		}
	}
	d := database(t)
	store := pgstore.New(d)
	admin := identityaccess.FinanceServiceScope("test")
	own, other := kernel.NewID(), kernel.NewID()
	period := economics.PeriodOf(2093, 1)
	history, err := store.Batches(ctx, admin, period)
	if err != nil {
		t.Fatal(err)
	}
	version := len(history) + 1
	batch := economics.ImportBatch{ID: kernel.NewID(), Period: period, DataVersion: version, Status: economics.BatchApplied}
	if err := store.SaveBatch(ctx, admin, batch); err != nil {
		t.Fatal(err)
	}
	rows := []economics.FactRow{{ID: kernel.NewID(), BatchID: batch.ID, ProductID: own, Period: period, DataVersion: version}, {ID: kernel.NewID(), BatchID: batch.ID, ProductID: other, Period: period, DataVersion: version}}
	if err := store.AppendFacts(ctx, admin, rows); err != nil {
		t.Fatal(err)
	}
	narrow := authz.New(authz.Params{Subject: "narrow", Products: map[kernel.ID]authz.Access{own: authz.AccessPrivate}, Finance: authz.FinanceFull})
	got, err := store.Facts(ctx, narrow, economics.FactFilter{Period: &period})
	if err != nil || len(got) != 1 || got[0].ProductID != own {
		t.Fatalf("foreign facts: %+v %v", got, err)
	}
	if _, err := store.Facts(ctx, narrow, economics.FactFilter{Product: &other}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatal(err)
	}
	aggregate := authz.New(authz.Params{Subject: "aggregate", AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates})
	if _, err := store.Facts(ctx, aggregate, economics.FactFilter{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := store.Batches(ctx, narrow, period); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatal(err)
	}
	if err := store.SaveScenario(ctx, narrow, economics.Scenario{ID: kernel.NewID()}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatal(err)
	}
}
