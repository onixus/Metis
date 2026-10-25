package delivery

import (
	"context"
	"errors"
	"testing"
)

func TestDL04_ExhaustedSimulationDoesNotReturnFabricatedCompletion(t *testing.T) {
	iterations, err := simulate(context.Background(), 1, []int{0}, 1, 42)
	if !errors.Is(err, ErrNoThroughput) || iterations != nil {
		t.Fatalf("expected no completion estimate, got %v, %v", iterations, err)
	}
}
