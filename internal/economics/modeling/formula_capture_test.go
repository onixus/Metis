package economics

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestEC09_NFS02_FormulaCaptureIsolatedClonedAndConcurrent(t *testing.T) {
	ctx := WithFormulaSnapshot(context.Background())
	sc := identityaccess.FinanceServiceScope("capture-one")
	provider := new(int)
	var calls atomic.Int64
	read := func(context.Context) ([]int, error) { calls.Add(1); return []int{7}, nil }
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			v, err := CaptureFormulaInput(ctx, sc, provider, "complete", read)
			if err != nil || len(v) != 1 || v[0] != 7 {
				t.Errorf("capture %v %v", v, err)
			}
			if len(v) > 0 {
				v[0] = 99
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("read %d times", calls.Load())
	}
	for _, changed := range []struct {
		ctx      context.Context
		scope    authz.Scope
		provider *int
	}{
		{WithFormulaSnapshot(context.Background()), sc, provider},
		{ctx, identityaccess.FinanceServiceScope("capture-two"), provider},
		{ctx, sc, new(int)},
	} {
		if _, err := CaptureFormulaInput(changed.ctx, changed.scope, changed.provider, "complete", read); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("scope/provider/operation reused capture: %d", calls.Load())
	}
	if _, err := CaptureFormulaInput(ctx, authz.Scope{}, provider, "complete", read); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope: %v", err)
	}
}

func TestEC09_NFS02_FormulaCaptureWaitCancellationAndError(t *testing.T) {
	ctx := WithFormulaSnapshot(context.Background())
	sc := identityaccess.FinanceServiceScope("capture-cancel")
	provider := new(int)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var calls atomic.Int64
	read := func(context.Context) (int, error) {
		calls.Add(1)
		close(entered)
		<-release
		return 0, kernel.ErrUnavailable
	}
	go func() { _, err := CaptureFormulaInput(ctx, sc, provider, "blocked", read); done <- err }()
	<-entered
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := CaptureFormulaInput(cancelled, sc, provider, "blocked", read); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled capture: %v", err)
	}
	close(release)
	if err := <-done; !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := CaptureFormulaInput(ctx, sc, provider, "blocked", read); !errors.Is(err, kernel.ErrUnavailable) || calls.Load() != 1 {
		t.Fatalf("failed capture changed: calls %d err %v", calls.Load(), err)
	}
}

func TestEC09_NFS02_FormulaCaptureRetainedBytesBoundAndFailedPayload(t *testing.T) {
	ctx := WithFormulaSnapshot(context.Background())
	sc := identityaccess.FinanceServiceScope("capture-budget")
	provider := new(int)
	session := ctx.Value(formulaSnapshotKey{}).(*formulaSnapshot)
	session.captureBytes = maxFormulaCaptureBytes - 3
	read := func(context.Context) ([]int, error) { return []int{1}, nil }
	if value, err := CaptureFormulaInput(ctx, sc, provider, "boundary", read); err != nil || len(value) != 1 {
		t.Fatalf("boundary %v %v", value, err)
	}
	if session.captureBytes != maxFormulaCaptureBytes {
		t.Fatalf("charged %d", session.captureBytes)
	}
	if value, err := CaptureFormulaInput(ctx, sc, provider, "oversize", read); !errors.Is(err, kernel.ErrValidation) || value != nil {
		t.Fatalf("over budget retained value %v err %v", value, err)
	}
	for key, entry := range session.captures {
		if key.key == "oversize" && entry.value != nil {
			t.Fatal("oversized payload retained")
		}
	}
	failed := WithFormulaSnapshot(context.Background())
	if value, err := CaptureFormulaInput(failed, sc, provider, "failed", func(context.Context) ([]int, error) { return []int{1, 2, 3}, kernel.ErrUnavailable }); !errors.Is(err, kernel.ErrUnavailable) || value != nil {
		t.Fatalf("failed partial value %v err %v", value, err)
	}
	failedSession := failed.Value(formulaSnapshotKey{}).(*formulaSnapshot)
	if failedSession.captureBytes != 0 {
		t.Fatal("failed payload charged")
	}
	for _, entry := range failedSession.captures {
		if entry.value != nil {
			t.Fatal("failed partial payload retained")
		}
	}
}
