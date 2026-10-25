package economics_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type platformPort struct {
	calls     int
	lastKind  economics.PlatformSourceKind
	lastSlice economics.Slice
	lastScope authz.Scope
	value     economics.PlatformValue
	err       error
	change    bool
}

func (p *platformPort) ReadFormulaSource(ctx context.Context, sc authz.Scope, kind economics.PlatformSourceKind, sl economics.Slice) (economics.PlatformValue, error) {
	if err := ctx.Err(); err != nil {
		return economics.PlatformValue{}, err
	}
	p.calls++
	p.lastKind, p.lastSlice, p.lastScope = kind, sl, sc
	if p.err != nil {
		return economics.PlatformValue{}, p.err
	}
	value := p.value
	if p.change {
		value.Value = value.Value.Add(decimal.NewFromInt(int64(p.calls)))
	}
	return value, nil
}
func platformValue(value int64, currency string) economics.PlatformValue {
	return economics.PlatformValue{Value: decimal.NewFromInt(value), Currency: currency, SnapshotID: "synthetic-snapshot-v1", AsOf: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), Provenance: []economics.PlatformProvenance{{Source: "synthetic", RecordID: "aggregate", Version: "v1"}}}
}
func declareSourceMetric(t *testing.T, f *fixture, key, expression string) {
	t.Helper()
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: key, Name: key, Expression: expression}); err != nil {
		t.Fatal(err)
	}
}

func TestEC09_EC10_DirectSourcesMemoExecutedLineageAndAffectedGraph(t *testing.T) {
	f := newFixture(t)
	port := &platformPort{value: platformValue(3600, "")}
	f.svc.WithPlatformSources(port)
	declareSourceMetric(t, f, "work", `worklogs()`)
	declareSourceMetric(t, f, "nested_sources", `metric("work") + worklogs() + (true ? worklogs() : headcount())`)
	explanation, err := f.svc.Explain(f.ctx, f.fin, "nested_sources", economics.Slice{ProductID: f.edr, Period: jan()})
	if err != nil || !explanation.Value.Equal(decimal.NewFromInt(10800)) {
		t.Fatalf("source calculation: %+v %v", explanation, err)
	}
	if port.calls != 1 || port.lastKind != economics.SourceWorklogs || port.lastSlice.ProductID != f.edr || port.lastScope.Subject() != f.fin.Subject() {
		t.Fatalf("snapshot/scope lost: %+v", port)
	}
	if len(explanation.Inputs) != 3 || len(explanation.Inputs[0].Inputs) != 1 {
		t.Fatalf("executed lineage: %+v", explanation)
	}
	observed := explanation.Inputs[0].Inputs[0]
	if observed.Kind != formula.RefSource || len(observed.Sources) != 1 || observed.Sources[0].SnapshotID != "synthetic-snapshot-v1" || observed.Sources[0].Period != "2026-01" || observed.Sources[0].CapturedValue != "3600" {
		t.Fatalf("source provenance missing: %+v", observed)
	}
	affected, err := f.svc.Affected(f.ctx, f.fin, formula.Ref{Kind: formula.RefSource, Key: "worklogs"})
	if err != nil || len(affected) != 2 || affected[0] != "nested_sources" || affected[1] != "work" {
		t.Fatalf("source dependency graph: %v %v", affected, err)
	}
}

func TestEC09_NFS02_DirectSourcesFailClosedAndNeverWidenCaller(t *testing.T) {
	for _, failure := range []error{kernel.ErrUnavailable, kernel.ErrForbidden, context.Canceled} {
		f := newFixture(t)
		port := &platformPort{err: failure}
		f.svc.WithPlatformSources(port)
		declareSourceMetric(t, f, "failure_source", `headcount()`)
		if _, err := f.svc.Value(f.ctx, f.fin, "failure_source", economics.Slice{ProductID: f.edr, Period: jan()}); !errors.Is(err, failure) {
			t.Fatalf("source failure lost: %v", err)
		}
	}
	f := newFixture(t)
	port := &platformPort{value: platformValue(1, "")}
	f.svc.WithPlatformSources(port)
	restricted := authz.New(authz.Params{Subject: "synthetic-limited", Roles: []authz.Role{authz.RoleFinance}, Finance: authz.FinanceFull, Products: map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate}})
	for i, expression := range []string{fmt.Sprintf(`worklogs("product", "%s")`, f.vm), `worklogs("product", "*")`} {
		key := fmt.Sprintf("denied_source_%d", i)
		declareSourceMetric(t, f, key, expression)
		if _, err := f.svc.Value(f.ctx, restricted, key, economics.Slice{ProductID: f.edr, Period: jan()}); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("foreign source permitted: %v", err)
		}
	}
	if port.calls != 0 {
		t.Fatal("unauthorized source read before scope check")
	}
	declareSourceMetric(t, f, "full_source", `headcount()`)
	if _, err := f.svc.Value(f.ctx, aggregatesScope(), "full_source", economics.Slice{ProductID: f.edr, Period: jan()}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("aggregate scope widened: %v", err)
	}
	if _, err := f.svc.Value(f.ctx, f.fin, "full_source", economics.Slice{ProductID: f.edr}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("month-less source allowed: %v", err)
	}
}

func TestEC09_DirectSourceCompleteZeroCurrencyAndBounds(t *testing.T) {
	f := newFixture(t)
	port := &platformPort{value: platformValue(0, "RUB")}
	f.svc.WithPlatformSources(port)
	declareSourceMetric(t, f, "source_zero", `crm_revenue()`)
	sl := economics.Slice{ProductID: f.edr, Period: jan()}
	if v, err := f.svc.Value(f.ctx, f.fin, "source_zero", sl); err != nil || !v.IsZero() {
		t.Fatalf("complete zero rejected: %v %v", v, err)
	}
	cases := []economics.PlatformValue{platformValue(-1, "RUB"), platformValue(1, "USD"), platformValue(1, ""), platformValue(1, "RUB"), platformValue(1, "RUB"), platformValue(1, "RUB")}
	cases[3].SnapshotID = ""
	cases[4].Provenance = nil
	cases[5].Value = decimal.New(1, 100000000)
	for i, value := range cases {
		port.value = value
		if _, err := f.svc.Value(f.ctx, f.fin, "source_zero", sl); err == nil {
			t.Fatalf("invalid source%d accepted", i)
		}
	}
	port.value = platformValue(9007199254740993, "RUB")
	if v, err := f.svc.Value(f.ctx, f.fin, "source_zero", sl); err != nil || v.String() != "9007199254740993" {
		t.Fatalf("large integer precision: %v %v", v, err)
	}
}

func TestEC09_EC13_SourceScenarioSharesCapturedBaselineAndKeepsFacts(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	port := &platformPort{value: platformValue(100, ""), change: true}
	f.svc.WithPlatformSources(port)
	declareSourceMetric(t, f, "source_scenario", `worklogs()`)
	sn, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic source override", Period: jan(), Products: []kernel.ID{f.edr}, Overrides: []economics.Override{{Source: economics.SourceWorklogs, ProductID: f.edr, Period: jan(), Value: decimal.NewFromInt(7200)}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.svc.RunScenario(f.ctx, f.fin, sn.ID, []string{"source_scenario"})
	if err != nil {
		t.Fatal(err)
	}
	key := "source_scenario@" + f.edr.String()
	if result.BaseMetrics[key] != "101" || result.Metrics[key] != "7200" || port.calls != 1 {
		t.Fatalf("scenario drift/mutation: %+v reads%d", result, port.calls)
	}
	observation := result.Explanations[key].Inputs[0].Sources[0]
	if !observation.Overridden || observation.CapturedValue != "101" || result.Explanations[key].Inputs[0].Value.String() != "7200" {
		t.Fatalf("scenario provenance did not preserve baseline: %+v", observation)
	}
	if !port.value.Value.Equal(decimal.NewFromInt(100)) {
		t.Fatal("scenario mutated provider")
	}
	after, err := f.svc.Value(f.ctx, f.fin, "source_scenario", economics.Slice{ProductID: f.edr, Period: jan()})
	if err != nil || !after.Equal(decimal.NewFromInt(102)) {
		t.Fatalf("source facts overwritten: %v %v", after, err)
	}
}

func TestEC09_EC13_PinnedBookExternalSupportsSourcesWithoutStoredFactFallback(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	f.svc.WithAuditor(externalAuditor{})
	port := &platformPort{value: platformValue(5, "")}
	f.svc.WithPlatformSources(port)
	declareSourceMetric(t, f, "external_source", `field("revenue") + headcount()`)
	ex, err := f.svc.EvaluateExternal(f.ctx, f.fin, "external_source", economics.Slice{ProductID: f.edr, Period: jan()}, nil, map[string]economics.FieldType{})
	if err != nil || !ex.Value.Equal(decimal.NewFromInt(5)) || port.calls != 1 {
		t.Fatalf("external source/fact isolation: %+v %v", ex, err)
	}
	if len(ex.Inputs) != 2 || len(ex.Inputs[1].Sources) != 1 {
		t.Fatalf("external provenance lost: %+v", ex)
	}
}

func TestEC13_SourceOverrideValidatesTargetAndRejectsPartialAggregates(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	team := kernel.NewID()
	port := &platformPort{value: platformValue(100, "")}
	f.svc.WithPlatformSources(port)
	for _, override := range []economics.Override{
		{FieldKey: "revenue", Source: economics.SourceWorklogs, ProductID: f.edr},
		{Source: "shell", ProductID: f.edr},
		{Source: economics.SourceHeadcount, ProductID: f.edr, Value: decimal.RequireFromString("1.5")},
		{Source: economics.SourceCRMRevenue, ProductID: f.edr, Value: decimal.NewFromInt(-1)},
		{FieldKey: "headcount", ProductID: f.edr, TeamID: team},
	} {
		if _, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Invalid source", Period: jan(), Products: []kernel.ID{f.edr}, Overrides: []economics.Override{override}}); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("invalid source override accepted: %+v %v", override, err)
		}
	}
	declareSourceMetric(t, f, "team_source", fmt.Sprintf(`worklogs("team", "%s")`, team))
	declareSourceMetric(t, f, "all_source", `worklogs("product", "*")`)
	for _, tc := range []struct {
		key      string
		override economics.Override
		wantErr  bool
	}{
		{"team_source", economics.Override{Source: economics.SourceWorklogs, ProductID: f.edr, TeamID: team, Value: decimal.NewFromInt(12)}, false},
		{"team_source", economics.Override{Source: economics.SourceWorklogs, ProductID: f.edr, Value: decimal.NewFromInt(12)}, true},
		{"all_source", economics.Override{Source: economics.SourceWorklogs, ProductID: f.edr, Value: decimal.NewFromInt(12)}, true},
	} {
		sn, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Synthetic", Period: jan(), Products: []kernel.ID{f.edr}, Overrides: []economics.Override{tc.override}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := f.svc.RunScenario(f.ctx, f.fin, sn.ID, []string{tc.key})
		if tc.wantErr {
			if !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("ambiguous source replacement allowed: %v", err)
			}
			continue
		}
		if err != nil || result.Metrics[tc.key+"@"+f.edr.String()] != "12" {
			t.Fatalf("exact team override failed: %+v %v", result, err)
		}
	}
	a := economics.Override{Source: economics.SourceHeadcount, ProductID: f.edr, TeamID: team, Value: decimal.NewFromInt(3)}
	b := economics.Override{Source: economics.SourceHeadcount, ProductID: f.edr, Value: decimal.NewFromInt(4)}
	if _, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "Overlap", Period: jan(), Overrides: []economics.Override{a, b}}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("source overlap allowed: %v", err)
	}
}

func TestEC09_EC10_SourceProvenanceAmplificationBudget(t *testing.T) {
	f := newFixture(t)
	value := platformValue(1, "")
	value.Provenance = make([]economics.PlatformProvenance, 256)
	for i := range value.Provenance {
		value.Provenance[i] = economics.PlatformProvenance{Source: "synthetic", RecordID: fmt.Sprintf("aggregate_%d", i)}
	}
	port := &platformPort{value: value}
	f.svc.WithPlatformSources(port)
	declareSourceMetric(t, f, "source_budget", strings.Repeat("worklogs()+", 69)+"worklogs()")
	if _, err := f.svc.Explain(f.ctx, f.fin, "source_budget", economics.Slice{ProductID: f.edr, Period: jan()}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("provenance amplification accepted: %v", err)
	}
	if port.calls != 1 {
		t.Fatalf("memo lost under rejected trace: %d", port.calls)
	}
}

func TestEC09_NFS02_CRMCurrencyCannotBeRelabeledThroughNestedOrCachedMetric(t *testing.T) {
	f := newFixture(t)
	port := &platformPort{value: platformValue(12345, "RUB")}
	f.svc.WithPlatformSources(port)
	declareSourceMetric(t, f, "crm_currency_child", `crm_revenue()`)
	for _, expression := range []string{`crm_revenue()`, `metric("crm_currency_child")`} {
		if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "crm_wrong_currency", Name: "Synthetic", Currency: "USD", Expression: expression}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Value(f.ctx, f.fin, "crm_wrong_currency", economics.Slice{ProductID: f.edr, Period: jan()}); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("RUB source relabeled USD: %v", err)
		}
	}
	if port.calls != 0 {
		t.Fatal("read incompatible source before currency refusal")
	}
	declareSourceMetric(t, f, "crm_compare_currency", `metric("crm_currency_child")`)
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "crm_compare_currency", Name: "Synthetic USD", Currency: "USD", Expression: `metric("crm_currency_child")`}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.CompareVersions(f.ctx, f.fin, "crm_compare_currency", economics.Slice{ProductID: f.edr, Period: jan()}, 1, 2); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("memo laundered currency: %v", err)
	}
	if port.calls != 1 {
		t.Fatalf("source reread in comparison: %d", port.calls)
	}
}

func TestEC09_EC10_NFS02_FormulaSnapshotContextPinsComposedCallsAndIsolatesScopeService(t *testing.T) {
	f := newFixture(t)
	port := &platformPort{value: platformValue(100, ""), change: true}
	f.svc.WithPlatformSources(port).WithAuditor(externalAuditor{})
	declareSourceMetric(t, f, "shared_context_source", `headcount()`)
	ctx := economics.WithFormulaSnapshot(f.ctx)
	sl := economics.Slice{ProductID: f.edr, Period: jan()}
	for i := 0; i < 2; i++ {
		value, err := f.svc.EvaluateExternal(ctx, f.fin, "shared_context_source", sl, nil, nil)
		if err != nil || value.Value.String() != "101" {
			t.Fatalf("external clone snapshot drift: %+v %v", value, err)
		}
	}
	if port.calls != 1 {
		t.Fatalf("composed report read source%d times", port.calls)
	}
	limited := authz.New(authz.Params{Subject: f.fin.Subject(), Roles: []authz.Role{authz.RoleFinance}, Finance: authz.FinanceFull, Products: map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate}})
	value, err := f.svc.Value(ctx, limited, "shared_context_source", sl)
	if err != nil || value.String() != "102" || port.calls != 2 {
		t.Fatalf("permission scope reused another snapshot: %v %v calls%d", value, err, port.calls)
	}
	other := newFixture(t)
	otherPort := &platformPort{value: platformValue(7, "")}
	other.svc.WithPlatformSources(otherPort)
	declareSourceMetric(t, other, "shared_context_source", `headcount()`)
	value, err = other.svc.Value(ctx, other.fin, "shared_context_source", sl)
	if err != nil || value.String() != "7" || otherPort.calls != 1 {
		t.Fatalf("another service reused source snapshot: %v %v", value, err)
	}
}

func TestEC09_NFS05_ComposedSnapshotPinsConcurrentCalls(t *testing.T) {
	f := newFixture(t)
	port := &platformPort{value: platformValue(100, ""), change: true}
	f.svc.WithPlatformSources(port)
	declareSourceMetric(t, f, "concurrent_source", `headcount()`)
	ctx := economics.WithFormulaSnapshot(f.ctx)
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			value, err := f.svc.Value(ctx, f.fin, "concurrent_source", economics.Slice{ProductID: f.edr, Period: jan()})
			if err == nil && value.String() != "101" {
				err = fmt.Errorf("source drift: %s", value)
			}
			results <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if port.calls != 1 {
		t.Fatalf("concurrent source recaptured: %d", port.calls)
	}
}
