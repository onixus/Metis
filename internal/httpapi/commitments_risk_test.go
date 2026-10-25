package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestCT03_HTTPRiskHandlingAndRefusal(t *testing.T) {
	ctx := context.Background()
	store := commitments.NewMemStore()
	sc := cpoScope()
	product, foreign := kernel.NewID(), kernel.NewID()
	svc := commitments.NewService(store, nopPub{}, kernel.SystemClock{})
	c, err := svc.Create(ctx, sc, product, commitments.Input{Kind: commitments.KindCustomer, Counterparty: "synthetic-account", Subject: "Synthetic delivery", DueDate: kernel.DateOf(2026, 12, 1), Basis: "Synthetic agreement", Owner: "synthetic-owner"})
	if err != nil {
		t.Fatal(err)
	}
	a := commitments.Alert{ID: kernel.NewID(), ProductID: product, CommitmentID: c.ID, Kind: commitments.AlertRoadmapShift, RaisedAt: time.Now().UTC()}
	if err := store.AppendAlert(ctx, sc, a); err != nil {
		t.Fatal(err)
	}
	auditStore := audit.NewMemStore()
	auditLog := audit.NewLogger(auditStore, kernel.SystemClock{})
	request := func(srv *Server, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/commitment-alerts/"+a.ID.String()+"/actions", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		srv.Handler().ServeHTTP(response, req)
		return response
	}
	allowed := NewServer(Deps{Commitments: svc, Audit: auditLog, Instrument: withScope(sc)})
	response := request(allowed, `{"kind":"escalated","recipient":"synthetic-director","note":"Need a recovery plan"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("handling %d %s", response.Code, response.Body.String())
	}
	var got gen.CommitmentAlert
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RiskOwner == nil || *got.RiskOwner != "synthetic-director" || got.Actions == nil || len(*got.Actions) != 1 {
		t.Fatalf("missing owner/history: %+v", got)
	}
	denied := NewServer(Deps{Commitments: svc, Audit: auditLog, Instrument: withScope(pmScope("foreign-pm", foreign))})
	if response := request(denied, `{"kind":"noted","note":"forbidden"}`); response.Code != http.StatusForbidden {
		t.Fatalf("foreign action %d %s", response.Code, response.Body.String())
	}
	if response := request(allowed, `{"kind":"escalated","note":"missing recipient"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid action %d %s", response.Code, response.Body.String())
	}
	verified, err := audit.Verify(ctx, auditStore)
	if err != nil || !verified.OK || verified.Checked != 1 {
		t.Fatalf("audit chain: %+v %v", verified, err)
	}
}

// interleavedRiskStore deterministically schedules another writer's committed
// action between this request's INSERT and its read of the resulting history.
type interleavedRiskStore struct {
	commitments.Store
	other commitments.AlertAction
}

func (s *interleavedRiskStore) AppendAlertAction(ctx context.Context, sc authz.Scope, action commitments.AlertAction) error {
	if err := s.Store.AppendAlertAction(ctx, sc, action); err != nil {
		return err
	}
	return s.Store.AppendAlertAction(ctx, sc, s.other)
}

func TestCT03_HTTPAuditIdentifiesOwnActionWhenAnotherWriterAppends(t *testing.T) {
	ctx, sc := context.Background(), cpoScope()
	base := commitments.NewMemStore()
	svc := commitments.NewService(base, nopPub{}, kernel.SystemClock{})
	product := kernel.NewID()
	c, err := svc.Create(ctx, sc, product, commitments.Input{Kind: commitments.KindCustomer, Counterparty: "synthetic-account", Subject: "Synthetic delivery", DueDate: kernel.DateOf(2026, 12, 1), Basis: "Synthetic agreement", Owner: "synthetic-owner"})
	if err != nil {
		t.Fatal(err)
	}
	alert := commitments.Alert{ID: kernel.NewID(), ProductID: product, CommitmentID: c.ID, Kind: commitments.AlertRoadmapShift, RaisedAt: time.Now().UTC()}
	if err := base.AppendAlert(ctx, sc, alert); err != nil {
		t.Fatal(err)
	}
	other := commitments.AlertAction{ID: kernel.NewID(), AlertID: alert.ID, ProductID: product, Kind: "escalated", Recipient: "second-owner", Note: "Another concurrent request", Actor: "second-writer", CreatedAt: time.Now().UTC()}
	svc = commitments.NewService(&interleavedRiskStore{Store: base, other: other}, nopPub{}, kernel.SystemClock{})
	auditStore := audit.NewMemStore()
	srv := NewServer(Deps{Commitments: svc, Audit: audit.NewLogger(auditStore, kernel.SystemClock{}), Instrument: withScope(sc)})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/commitment-alerts/"+alert.ID.String()+"/actions", bytes.NewBufferString(`{"kind":"assigned","recipient":"first-owner","note":"This request"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("handling %d %s", response.Code, response.Body.String())
	}
	history, err := base.AlertActions(ctx, sc, alert.ID)
	if err != nil || len(history) != 2 || history[1].ID != other.ID {
		t.Fatalf("fixture did not interleave another writer: %+v %v", history, err)
	}
	record, err := auditStore.Last(ctx, identityaccess.ServiceScope("test-journal"))
	if err != nil {
		t.Fatal(err)
	}
	handling, ok := record.Details["handling"].(commitments.AlertAction)
	if !ok || handling.ID != history[0].ID || handling.Actor != record.Actor || handling.Actor != "cpo" || handling.Kind != "assigned" || handling.Recipient != "first-owner" {
		t.Fatalf("audit attributed another writer's action: record=%+v handling=%+v", record, handling)
	}
	verified, err := audit.Verify(ctx, auditStore)
	if err != nil || !verified.OK || verified.Checked != 1 {
		t.Fatalf("audit chain: %+v %v", verified, err)
	}
}
