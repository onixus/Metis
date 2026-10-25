//go:build integration

package app

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/sources"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/ports"
)

func isolateSourceRuntimeDatabase(t *testing.T) {
	t.Helper()
	raw := os.Getenv("METIS_RUNTIME_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("METIS_RUNTIME_TEST_DATABASE_URL missing: docs/questions.md #05")
	}
	ctx := context.Background()
	owner, err := pgdb.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	name := "metis_source_close_" + strings.ReplaceAll(kernel.NewID().String(), "-", "")
	if _, err := owner.Pool().Exec(ctx, "CREATE DATABASE "+name); err != nil {
		owner.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	t.Setenv("METIS_RUNTIME_TEST_DATABASE_URL", parsed.String())
	t.Cleanup(func() {
		_, err := owner.Pool().Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
		owner.Close()
	})
}

func TestEC09_EC11_NFS05_PostgresCloseCapturesSourcesAndRollsBackIncompleteCapture(t *testing.T) {
	isolateSourceRuntimeDatabase(t)
	for index, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "unavailable"}[fail], func(t *testing.T) {
			a, _, _ := runtimeApps(t)
			ctx := context.Background()
			sc := identityaccess.FinanceServiceScope("synthetic-close-source")
			setup := identityaccess.ServiceScope("synthetic-setup")
			period := modeling.PeriodOf(2496, time.Month(index+1))
			product, err := a.Portfolio.CreateProduct(ctx, setup, portfoliograph.ProductInput{Key: "source-close-" + strings.ReplaceAll(kernel.NewID().String(), "-", ""), Name: "Synthetic source closure", Type: portfoliograph.ProductTypeSecurity})
			if err != nil {
				t.Fatal(err)
			}
			fields := []economics.Field{{Key: "a_revenue", Name: "A", Type: "money", Source: "calculated", Formula: "revenue/2"}}
			if fail {
				fields = append(fields, economics.Field{Key: "z_missing", Name: "Z", Type: "number", Source: "calculated", Formula: "revenue+1"})
			}
			var book economics.Snapshot
			if err := a.runOperation(ctx, func(ctx context.Context) error {
				var err error
				book, err = a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: product.ID, Period: period.String(), Currency: "RUB", Rows: []economics.Row{{ProductID: product.ID, Category: economics.Revenue, Amount: kernel.RUB(200)}}, Fields: fields})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			in := modeling.HistoryUnificationInput{BookProductID: product.ID, Period: period, ExpectedBookVersion: 1}
			var preview modeling.HistoryUnificationPreview
			if err := a.runOperation(ctx, func(ctx context.Context) error {
				var err error
				preview, err = a.Modeling.PreviewUnification(ctx, sc, in)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			in.Digest = preview.Digest
			if err := a.runOperation(ctx, func(ctx context.Context) error { _, err := a.Modeling.ApplyUnification(ctx, sc, in); return err }); err != nil {
				t.Fatal(err)
			}
			if err := a.runOperation(ctx, func(ctx context.Context) error {
				_, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: preview.DefinitionMappings["a_revenue"], Name: "Direct CRM", Expression: "crm_revenue()", Currency: "RUB", EffectiveFrom: period.Start()})
				if err != nil {
					return err
				}
				if fail {
					_, err = a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: preview.DefinitionMappings["z_missing"], Name: "Unavailable worklogs", Expression: "worklogs()", EffectiveFrom: period.Start()})
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			crm := &formulaCRM{deals: []ports.Deal{{ExternalID: "synthetic-closed-deal", ProductKey: product.Key, Outcome: ports.DealWon, ClosedDate: period.Start(), Amount: kernel.RUB(550)}}}
			a.CRM = crm
			var closed economics.Snapshot
			err = a.runOperation(ctx, func(ctx context.Context) error {
				var err error
				closed, err = a.Economics.Close(ctx, sc, product.ID, period.String(), book.Version)
				return err
			})
			if fail {
				if !errors.Is(err, kernel.ErrUnavailable) {
					t.Fatalf("close accepted unavailable source: %v", err)
				}
				original, err := a.Economics.Snapshot(ctx, sc, product.ID, period.String(), 0)
				if err != nil || original.Closed || original.Version != 1 {
					t.Fatalf("partial close: %+v %v", original, err)
				}
				for _, portfolio := range []bool{false, true} {
					_, err := a.FormulaSources.Latest(ctx, sc, sources.Key{ProductID: product.ID, Period: period.String(), Kind: "crm_revenue", Portfolio: portfolio})
					if !errors.Is(err, kernel.ErrNotFound) {
						t.Fatalf("partial source capture: portfolio=%v err=%v", portfolio, err)
					}
				}
				return
			}
			if err != nil || !closed.Closed || closed.Version != 2 || crm.calls == 0 {
				t.Fatalf("close source preflight: %+v %v calls%d", closed, err, crm.calls)
			}
			calls := crm.calls
			crm.err = kernel.ErrUnavailable
			var report economics.Report
			if err := a.runOperation(ctx, func(ctx context.Context) error {
				var err error
				report, err = a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: product.ID, Period: period.String(), Version: closed.Version})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if report.Total.Metrics["a_revenue"] != "550" || len(report.Products) != 1 || report.Products[0].Metrics["a_revenue"] != "550" || crm.calls != calls {
				t.Fatalf("closed report did not retain captured source: %+v calls%d/%d", report, crm.calls, calls)
			}
		})
	}
}
