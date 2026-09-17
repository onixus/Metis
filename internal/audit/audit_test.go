package audit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/kernel"
)

func newLogger() (*audit.Logger, *audit.MemStore) {
	st := audit.NewMemStore()
	return audit.NewLogger(st, kernel.FixedClock{T: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}), st
}

func TestAD04_AppendChainsHashes(t *testing.T) {
	l, st := newLogger()
	ctx := context.Background()
	r1, err := l.Append(ctx, audit.Entry{Actor: "cpo", Action: audit.ActionDateChange, ObjectType: "feature", ObjectID: "f1", Details: map[string]any{"reason": "сдвиг эпика"}})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := l.Append(ctx, audit.Entry{Actor: "cpo", Action: audit.ActionExport})
	if err != nil {
		t.Fatal(err)
	}
	if r1.PrevHash != audit.GenesisHash || r2.PrevHash != r1.Hash || r2.Seq != 2 {
		t.Fatal("сцепка нарушена")
	}
	res, err := audit.Verify(ctx, st)
	if err != nil || !res.OK || res.Checked != 2 {
		t.Fatalf("verify: %+v %v", res, err)
	}
}

func TestNFS05_VerifyDetectsTamperedRecord(t *testing.T) {
	l, st := newLogger()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := l.Append(ctx, audit.Entry{Actor: "a", Action: audit.ActionLogin}); err != nil {
			t.Fatal(err)
		}
	}
	st.Tamper(3, func(r *audit.Record) { r.Actor = "intruder" })
	res, err := audit.Verify(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenSeq != 3 {
		t.Fatalf("ожидалось нарушение в записи 3, получено %+v", res)
	}
}

func TestNFS05_VerifyDetectsDeletedRecord(t *testing.T) {
	l, st := newLogger()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := l.Append(ctx, audit.Entry{Actor: "a", Action: audit.ActionLogin}); err != nil {
			t.Fatal(err)
		}
	}
	// Имитация удаления второй записи: третья теперь идёт сразу после первой.
	st.Tamper(2, func(r *audit.Record) { r.Seq = 3 })
	res, err := audit.Verify(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenSeq != 3 {
		t.Fatalf("ожидалось нарушение последовательности, получено %+v", res)
	}
}

func TestAD04_CEFFormat(t *testing.T) {
	l, _ := newLogger()
	r, err := l.Append(context.Background(), audit.Entry{Actor: "u=1", Action: audit.ActionViewFinance, ObjectType: "pnl", ObjectID: "p|1", Details: map[string]any{"k": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	line := audit.CEF(r, "0.1.0")
	if !strings.HasPrefix(line, "CEF:0|Metis|Metis|0.1.0|finance.view|finance.view|5|") {
		t.Fatalf("заголовок: %s", line)
	}
	if !strings.Contains(line, `suser=u\=1`) || !strings.Contains(line, "cs2=p|1") || !strings.Contains(line, "k=v") {
		t.Fatalf("расширение: %s", line)
	}
}
