//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
)

func TestCT03_NFS05_PostgresRiskActionPersistsAndRollsBackWithAudit(t *testing.T) {
	a, reader, owner := runtimeApps(t)
	worker := postgresWorker(t, a.Cfg)
	ctx := context.Background()
	product := runtimeProduct(t, a, "risk-"+kernel.NewID().String(), "risk-fixture")
	response := runtimeRequest(a, "POST", "/products/"+product+"/features", `{"name":"Synthetic obligation feature","planned_date":"2026-10-01"}`, "risk-fixture", []string{"cpo"}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create feature: %d %s", response.Code, response.Body.String())
	}
	var feature gen.Feature
	if err := json.Unmarshal(response.Body.Bytes(), &feature); err != nil {
		t.Fatal(err)
	}
	response = runtimeRequest(a, "POST", "/products/"+product+"/commitments", fmt.Sprintf(`{"kind":"customer","counterparty":"synthetic-account","subject":"Synthetic availability","due_date":"2026-10-15","basis":"Synthetic agreement","owner":"synthetic-owner","feature_id":%q}`, feature.Id), "risk-fixture", []string{"cpo"}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create commitment: %d %s", response.Code, response.Body.String())
	}
	response = runtimeRequest(a, "POST", "/features/"+feature.Id.String()+"/shift-date", `{"planned_date":"2026-11-01","reason":"Synthetic delay"}`, "risk-fixture", []string{"cpo"}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("shift feature: %d %s", response.Code, response.Body.String())
	}
	drainWorker(t, worker)
	response = runtimeRequest(reader, "GET", "/products/"+product+"/commitment-alerts?open=false", "", "risk-reader", []string{"cpo"}, nil)
	var alerts []gen.CommitmentAlert
	if err := json.Unmarshal(response.Body.Bytes(), &alerts); err != nil || response.Code != http.StatusOK || len(alerts) == 0 {
		t.Fatalf("worker alerts: %d %s %v", response.Code, response.Body.String(), err)
	}
	alert := alerts[0]
	path := "/commitment-alerts/" + alert.Id.String() + "/actions"
	// The failed audit occurs after inserting the domain action and its outbox
	// event, so this exercises the same transaction as a real HTTP command.
	_, err := owner.Pool().Exec(ctx, `CREATE FUNCTION audit.reject_risk_runtime_record() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.actor = 'reject-risk-audit' THEN RAISE EXCEPTION 'synthetic risk audit failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER reject_risk_runtime_record BEFORE INSERT ON audit.records FOR EACH ROW EXECUTE FUNCTION audit.reject_risk_runtime_record()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = owner.Pool().Exec(ctx, "DROP TRIGGER IF EXISTS reject_risk_runtime_record ON audit.records; DROP FUNCTION IF EXISTS audit.reject_risk_runtime_record()")
	})
	body := `{"kind":"escalated","recipient":"synthetic-director","note":"Synthetic recovery plan"}`
	response = runtimeRequest(a, "POST", path, body, "reject-risk-audit", []string{"cpo"}, nil)
	if response.Code < 500 {
		t.Fatalf("audit failure returned success: %d %s", response.Code, response.Body.String())
	}
	var count int
	if err := owner.Pool().QueryRow(ctx, "SELECT count(*) FROM commitments.alert_actions WHERE alert_id=$1", alert.Id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed action persisted: %d %v", count, err)
	}
	if err := owner.Pool().QueryRow(ctx, "SELECT count(*) FROM kernel.outbox WHERE actor='reject-risk-audit'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed action outbox persisted: %d %v", count, err)
	}
	if err := owner.Pool().QueryRow(ctx, "SELECT count(*) FROM audit.records WHERE actor='reject-risk-audit' AND object_type='commitment_risk'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed action audit persisted: %d %v", count, err)
	}
	response = runtimeRequest(a, "POST", path, body, "risk-escalator", []string{"cpo"}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("escalate: %d %s", response.Code, response.Body.String())
	}
	response = runtimeRequest(reader, "GET", "/products/"+product+"/commitment-alerts?open=false", "", "risk-reader", []string{"cpo"}, nil)
	if err := json.Unmarshal(response.Body.Bytes(), &alerts); err != nil || len(alerts) == 0 {
		t.Fatalf("read persisted risk: %s %v", response.Body.String(), err)
	}
	if alerts[0].RiskOwner == nil || *alerts[0].RiskOwner != "synthetic-director" || alerts[0].Actions == nil || len(*alerts[0].Actions) != 1 {
		t.Fatalf("replica lost risk history: %+v", alerts[0])
	}
	response = runtimeRequest(reader, "POST", path, body, "foreign-risk-pm", []string{"pm"}, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign risk write: %d %s", response.Code, response.Body.String())
	}
	drainWorker(t, worker)
	verified, err := audit.Verify(ctx, reader.AuditStore)
	if err != nil || !verified.OK {
		t.Fatalf("risk audit chain: %+v %v", verified, err)
	}
	if err := owner.Pool().QueryRow(ctx, "SELECT count(*) FROM audit.records WHERE actor='risk-escalator' AND object_type='commitment_risk' AND object_id=$1", alert.Id.String()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("risk audit missing: %d %v", count, err)
	}
}
