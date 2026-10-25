package economics_test

import (
	"testing"
	"time"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/kernel"
)

func TestDA02_EC13_CapacityUsesLinkedPlanAndClassifiesBaselineSeparately(t *testing.T) {
	for _, tt := range []struct {
		name                           string
		planned, due                   kernel.Date
		shift                          int
		status                         string
		known, base, breach, newBreach bool
	}{
		{name: "new breach far from today", planned: kernel.DateOf(2028, 1, 10), due: kernel.DateOf(2028, 1, 15), shift: 6, known: true, breach: true, newBreach: true},
		{name: "exact deadline safe", planned: kernel.DateOf(2028, 1, 10), due: kernel.DateOf(2028, 1, 15), shift: 5, known: true},
		{name: "existing breach", planned: kernel.DateOf(2028, 1, 20), due: kernel.DateOf(2028, 1, 15), shift: 2, known: true, base: true, breach: true},
		{name: "negative shift resolves", planned: kernel.DateOf(2028, 1, 20), due: kernel.DateOf(2028, 1, 15), shift: -5, known: true, base: true},
		{name: "negative shift remains breached", planned: kernel.DateOf(2028, 1, 20), due: kernel.DateOf(2028, 1, 15), shift: -1, known: true, base: true, breach: true},
		{name: "zero shift baseline", planned: kernel.DateOf(2028, 1, 20), due: kernel.DateOf(2028, 1, 15), known: true, base: true, breach: true},
		{name: "missing planned date", due: kernel.DateOf(2026, 2, 12), shift: 30},
		{name: "missing deadline", planned: kernel.DateOf(2028, 1, 20), shift: 30},
		{name: "ambiguous dates", planned: kernel.DateOf(2028, 1, 20), due: kernel.DateOf(2028, 1, 15), status: "ambiguous", shift: 30},
		{name: "completed has no actual date", planned: kernel.DateOf(2028, 1, 20), due: kernel.DateOf(2028, 1, 15), status: "completed", shift: 30},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			// The clock is in 2026; classifications in 2028 must be identical.
			status := tt.status
			if status == "" {
				status = "known"
			}
			f.svc.WithCommitments(commitmentsStub{items: []economics.CommitmentRef{{ID: kernel.NewID(), ProductID: f.edr, DueDate: tt.due, PlannedDate: tt.planned, PlanningStatus: status}}})
			scenario, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: tt.name, Period: jan(), Products: []kernel.ID{f.edr}, CapacityShiftDays: tt.shift})
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.svc.RunScenario(f.ctx, f.fin, scenario.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Commitments) != 1 {
				t.Fatalf("missing impact: %+v", result)
			}
			impact := result.Commitments[0]
			if impact.Known != tt.known || impact.BaselineBreached != tt.base || impact.Breached != tt.breach || impact.NewlyBreached != tt.newBreach {
				t.Fatalf("wrong classification: %+v", impact)
			}
			if tt.known {
				if impact.BaselineDate != tt.planned || impact.ScenarioDate != tt.planned.AddDays(tt.shift) {
					t.Fatalf("wrong dates: %+v", impact)
				}
			} else if !impact.BaselineDate.IsZero() || !impact.ScenarioDate.IsZero() {
				t.Fatalf("fabricated dates: %+v", impact)
			}
			if impact.Reason == "" {
				t.Fatal("missing explanation")
			}
			// Computing a scenario does not modify the input plan.
			if result.Commitments[0].Commitment.PlannedDate != tt.planned {
				t.Fatal("plan mutated")
			}
		})
	}
}

func TestDA02_EC13_CapacityShiftBoundsRejectInvalidDates(t *testing.T) {
	f := newFixture(t)
	for _, shift := range []int{-36501, 36501} {
		if _, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{Name: "too large", Period: economics.PeriodOf(2026, time.January), CapacityShiftDays: shift}); err == nil {
			t.Fatalf("accepted shift %d", shift)
		}
	}
}
