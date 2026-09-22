package e2e

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/onixus/metis/internal/app"
	"github.com/onixus/metis/tests/e2e/client"
)

func pilotPtr[T any](v T) *T { return &v }

// The pilot must work from an empty installation with both Atlassian adapters
// disabled. This verifies the generated API client and role boundary together.
func TestSG01_SG03_SG05_PG02_PR01_RM01_RM02_PilotFromEmptyPortfolio(t *testing.T) {
	ctx := context.Background()
	a, err := app.Build(ctx, app.Config{Storage: "memory", AuthMode: "hmac", HMACSecret: secret, HMACIssuer: issuer, OTelExport: "none"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()
	e := &env{t: t, app: a, srv: srv}
	cpo := e.client("pilot-cpo", []string{"cpo"}, nil)
	initial := must(cpo.ListProductsWithResponse(ctx))
	if initial.StatusCode() != 200 || initial.JSON200 == nil || len(*initial.JSON200) != 0 {
		t.Fatalf("installation is not empty: %s", initial.Body)
	}
	product := must(cpo.CreateProductWithResponse(ctx, client.CreateProductJSONRequestBody{Key: "pilot-edr", Name: "Синтетический EDR", Type: "security", Owner: pilotPtr("pilot-pm")}))
	if product.StatusCode() != 201 || product.JSON201 == nil {
		t.Fatalf("product: %d %s", product.StatusCode(), product.Body)
	}
	productID := product.JSON201.Id
	pm := e.client("pilot-pm", []string{"pm"}, []string{"pilot-edr"})
	presale := e.client("pilot-presale", []string{"presale"}, nil)
	foreign := e.client("foreign-pm", []string{"pm"}, nil)
	feature := must(pm.CreateFeatureWithResponse(ctx, productID, client.CreateFeatureJSONRequestBody{Name: pilotPtr("Изоляция узла по сигналу SIEM"), Status: pilotPtr(client.FeatureInputStatus("planned"))}))
	if feature.StatusCode() != 201 || feature.JSON201 == nil {
		t.Fatalf("feature: %d %s", feature.StatusCode(), feature.Body)
	}
	featureID := feature.JSON201.Id
	signal := must(pm.IngestSignalWithResponse(ctx, productID, client.IngestSignalJSONRequestBody{Source: "manual", Text: "Синтетический B2B-заказчик требует изоляцию узла через SIEM", AccountId: pilotPtr("synthetic-account"), DealId: pilotPtr("synthetic-deal"), DealAmount: &client.Money{Amount: 125000000, Currency: "RUB"}, BlocksDeal: pilotPtr(true), Segment: pilotPtr("enterprise")}))
	if signal.StatusCode() != 201 || signal.JSON201 == nil {
		t.Fatalf("signal: %d %s", signal.StatusCode(), signal.Body)
	}
	triage := must(pm.TriageSignalWithResponse(ctx, signal.JSON201.Id, client.TriageSignalJSONRequestBody{Status: "in_review"}))
	if triage.StatusCode() != 200 {
		t.Fatalf("triage: %d %s", triage.StatusCode(), triage.Body)
	}
	linked := must(pm.LinkSignalWithResponse(ctx, signal.JSON201.Id, client.LinkSignalJSONRequestBody{FeatureId: &featureID}))
	if linked.StatusCode() != 200 || linked.JSON200 == nil || linked.JSON200.FeatureId == nil || *linked.JSON200.FeatureId != featureID {
		t.Fatalf("link: %d %s", linked.StatusCode(), linked.Body)
	}
	value := must(pm.GetFeatureValueWithResponse(ctx, featureID))
	if value.StatusCode() != 200 || value.JSON200 == nil || value.JSON200.OwnValue.Amount != 125000000 {
		t.Fatalf("B2B demand weight: %d %s", value.StatusCode(), value.Body)
	}
	model := must(pm.CreateScoringModelWithResponse(ctx, client.CreateScoringModelJSONRequestBody{Name: "RICE продукта", Type: "rice", ProductId: &productID}))
	if model.StatusCode() != 201 || model.JSON201 == nil {
		t.Fatalf("model: %d %s", model.StatusCode(), model.Body)
	}
	score := must(pm.SetFeatureScoreInputsWithResponse(ctx, model.JSON201.Id, featureID, client.SetFeatureScoreInputsJSONRequestBody{Values: map[string]string{"reach": "100", "impact": "2", "confidence": "0.8", "effort": "4"}}))
	if score.StatusCode() != 200 || score.JSON200 == nil || score.JSON200.Score != "40" || score.JSON200.Explanation == "" {
		t.Fatalf("score: %d %s", score.StatusCode(), score.Body)
	}
	ranking := must(pm.GetRankingWithResponse(ctx, model.JSON201.Id, productID))
	if ranking.StatusCode() != 200 || ranking.JSON200 == nil || len(*ranking.JSON200) != 1 || (*ranking.JSON200)[0].FeatureId != featureID {
		t.Fatalf("ranking: %d %s", ranking.StatusCode(), ranking.Body)
	}
	publicTitle := "Изоляция узла через SIEM"
	item := must(pm.CreateRoadmapItemWithResponse(ctx, productID, client.CreateRoadmapItemJSONRequestBody{FeatureId: &featureID, Title: &publicTitle, Bucket: pilotPtr(client.RoadmapItemInputBucket("next")), Audience: pilotPtr(client.RoadmapItemInputAudience("sales_safe")), EndDate: &openapi_types.Date{Time: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}}))
	if item.StatusCode() != 201 {
		t.Fatalf("roadmap: %d %s", item.StatusCode(), item.Body)
	}
	internal := must(pm.CreateRoadmapItemWithResponse(ctx, productID, client.CreateRoadmapItemJSONRequestBody{Title: pilotPtr("Внутренний эксперимент"), Bucket: pilotPtr(client.RoadmapItemInputBucket("later")), Audience: pilotPtr(client.RoadmapItemInputAudience("internal"))}))
	if internal.StatusCode() != 201 {
		t.Fatalf("internal roadmap: %d %s", internal.StatusCode(), internal.Body)
	}
	timeline := must(presale.GetRoadmapTimelineWithResponse(ctx, productID))
	if timeline.StatusCode() != 200 || timeline.JSON200 == nil || timeline.JSON200.Items != nil || timeline.JSON200.SalesSafe == nil || len(*timeline.JSON200.SalesSafe) != 1 || (*timeline.JSON200.SalesSafe)[0].Title != publicTitle {
		t.Fatalf("sales-safe: %d %s", timeline.StatusCode(), timeline.Body)
	}
	if denied := must(presale.ListSignalsWithResponse(ctx, productID, nil)); denied.StatusCode() != 403 {
		t.Fatalf("presale saw raw demand: %d %s", denied.StatusCode(), denied.Body)
	}
	if denied := must(foreign.CreateFeatureWithResponse(ctx, productID, client.CreateFeatureJSONRequestBody{Name: pilotPtr("Unauthorized")})); denied.StatusCode() != 403 {
		t.Fatalf("foreign PM modified backlog: %d %s", denied.StatusCode(), denied.Body)
	}
	if denied := must(foreign.SetFeatureScoreInputsWithResponse(ctx, model.JSON201.Id, featureID, client.SetFeatureScoreInputsJSONRequestBody{Values: map[string]string{"reach": "999"}})); denied.StatusCode() != 403 {
		t.Fatalf("foreign PM changed priority: %d %s", denied.StatusCode(), denied.Body)
	}
}
