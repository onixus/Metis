package sources_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/economics/sources"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type brokenAudit struct{}

func (brokenAudit) Append(context.Context, audit.Entry) (audit.Record, error) {
	return audit.Record{}, kernel.ErrUnavailable
}
func TestEC09_EC11_NFS02_ImmutableSourceRevisionsAndAuditedZero(t *testing.T) {
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("synthetic")
	clock := kernel.FixedClock{T: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)}
	store := sources.NewMemStore()
	svc := sources.NewService(store, audit.NewLogger(audit.NewMemStore(), clock), clock)
	in := sources.Input{Key: sources.Key{ProductID: kernel.NewID(), Period: "2026-10", Kind: "crm_revenue"}, Value: decimal.Zero, Currency: "RUB", AsOf: clock.Now(), Provenance: []sources.Provenance{{Source: "crm.aggregate", RecordID: "content-hash"}}}
	first, err := svc.Capture(ctx, sc, in)
	if err != nil || first.Version != 1 {
		t.Fatalf("capture: %+v %v", first, err)
	}
	in.AsOf = in.AsOf.Add(time.Hour)
	same, err := svc.Capture(ctx, sc, in)
	if err != nil || same.ID != first.ID {
		t.Fatalf("duplicate content: %+v %v", same, err)
	}
	in.Value = decimal.NewFromInt(10)
	second, err := svc.Capture(ctx, sc, in)
	if err != nil || second.Version != 2 {
		t.Fatalf("new revision: %+v %v", second, err)
	}
	history, err := svc.History(ctx, sc, in.Key)
	if err != nil || len(history) != 2 || !history[0].Value.IsZero() {
		t.Fatalf("history rewritten: %+v %v", history, err)
	}
	history[0].Value = decimal.NewFromInt(99)
	again, _ := svc.History(ctx, sc, in.Key)
	if !again[0].Value.IsZero() {
		t.Fatal("caller mutated history")
	}
	for _, denied := range []authz.Scope{{}, authz.New(authz.Params{Subject: "aggregate", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates})} {
		if _, err := svc.Latest(ctx, denied, in.Key); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("source denial: %v", err)
		}
	}
	in.Value = decimal.NewFromInt(20)
	failed := sources.NewService(store, brokenAudit{}, clock)
	if _, err := failed.Capture(ctx, sc, in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("audit bypass: %v", err)
	}
	latest, _ := svc.Latest(ctx, sc, in.Key)
	if latest.Version != 2 {
		t.Fatal("failed audit appended source")
	}
}
func TestEC09_NFS02_PortfolioSourceCannotEscapeByProductKey(t *testing.T) {
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("synthetic")
	id := kernel.NewID()
	clock := kernel.SystemClock{}
	svc := sources.NewService(sources.NewMemStore(), audit.NewLogger(audit.NewMemStore(), clock), clock)
	in := sources.Input{Key: sources.Key{ProductID: id, Period: "2026-10", Kind: "headcount", Portfolio: true}, Value: decimal.NewFromInt(20), AsOf: clock.Now(), Provenance: []sources.Provenance{{Source: "book.aggregate", RecordID: "content-hash"}}}
	if _, err := svc.Capture(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	limited := authz.New(authz.Params{Subject: "one", Roles: []authz.Role{authz.RoleFinance}, Products: map[kernel.ID]authz.Access{id: authz.AccessPrivate}, Finance: authz.FinanceFull})
	if _, err := svc.Latest(ctx, limited, in.Key); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("portfolio leaked: %v", err)
	}
	in.Portfolio = false
	if _, err := svc.Latest(ctx, limited, in.Key); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("namespace collision: %v", err)
	}
}
