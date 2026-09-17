// Package audit — журнал аудита (AD-04, NF-S05): только INSERT, сцепка хешей,
// проверка целостности и выгрузка в SIEM в формате CEF.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// Action — тип аудируемого действия.
type Action string

const (
	ActionViewFinance     Action = "finance.view"
	ActionExport          Action = "data.export"
	ActionDateChange      Action = "roadmap.date_change"
	ActionRuleChange      Action = "rule.change"
	ActionAccessChange    Action = "access.change"
	ActionLogin           Action = "auth.login"
	ActionLoginDenied     Action = "auth.denied"
	ActionConnectorChange Action = "connector.change"
	ActionGraphChange     Action = "graph.change"
)

// Entry — запись, которую хочет добавить приложение.
type Entry struct {
	Actor      string
	Action     Action
	ObjectType string
	ObjectID   string
	ProductID  kernel.ID
	Details    map[string]any
}

// Record — сохранённая запись с хешами.
type Record struct {
	Seq        int64          `json:"seq"`
	At         time.Time      `json:"at"`
	Actor      string         `json:"actor"`
	Action     Action         `json:"action"`
	ObjectType string         `json:"object_type"`
	ObjectID   string         `json:"object_id"`
	ProductID  kernel.ID      `json:"product_id"`
	Details    map[string]any `json:"details"`
	PrevHash   string         `json:"-"`
	Hash       string         `json:"-"`
}

// Store — хранилище записей. Реализации не имеют UPDATE и DELETE (инвариант 8).
type Store interface {
	// Last возвращает последнюю запись; kernel.ErrNotFound, если журнал пуст.
	Last(ctx context.Context) (Record, error)
	// Insert добавляет запись с заданными Seq, PrevHash и Hash.
	Insert(ctx context.Context, r Record) error
	// Walk перебирает записи по возрастанию Seq.
	Walk(ctx context.Context, fn func(Record) error) error
}

// Logger добавляет записи в журнал.
type Logger struct {
	store Store
	clock kernel.Clock
}

// NewLogger создаёт Logger.
func NewLogger(store Store, clock kernel.Clock) *Logger {
	return &Logger{store: store, clock: clock}
}

// GenesisHash — предыдущий хеш первой записи.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Append добавляет запись, сцепляя её с предыдущей.
func (l *Logger) Append(ctx context.Context, e Entry) (Record, error) {
	if e.Actor == "" || e.Action == "" {
		return Record{}, kernel.Invalid("audit", "actor и action обязательны")
	}
	prev, err := l.store.Last(ctx)
	seq := int64(1)
	prevHash := GenesisHash
	switch {
	case err == nil:
		seq = prev.Seq + 1
		prevHash = prev.Hash
	case kernel.IsNotFound(err):
	default:
		return Record{}, fmt.Errorf("audit last: %w", err)
	}
	r := Record{
		Seq: seq, At: l.clock.Now().UTC(), Actor: e.Actor, Action: e.Action,
		ObjectType: e.ObjectType, ObjectID: e.ObjectID, ProductID: e.ProductID,
		Details: e.Details, PrevHash: prevHash,
	}
	h, err := ComputeHash(r)
	if err != nil {
		return Record{}, err
	}
	r.Hash = h
	if err := l.store.Insert(ctx, r); err != nil {
		return Record{}, fmt.Errorf("audit insert: %w", err)
	}
	return r, nil
}

// ComputeHash считает SHA-256(prev_hash || canonical_json(record)).
func ComputeHash(r Record) (string, error) {
	body, err := kernel.CanonicalJSON(r)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(r.PrevHash))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyResult — итог проверки целостности.
type VerifyResult struct {
	Checked   int64
	OK        bool
	BrokenSeq int64 // первая нарушенная запись, 0 если всё цело
	Reason    string
}

// Verify проходит журнал и проверяет сцепку хешей.
func Verify(ctx context.Context, store Store) (VerifyResult, error) {
	res := VerifyResult{OK: true}
	prevHash := GenesisHash
	var expectSeq int64 = 1
	err := store.Walk(ctx, func(r Record) error {
		if !res.OK {
			return nil
		}
		res.Checked++
		if r.Seq != expectSeq {
			res.OK, res.BrokenSeq, res.Reason = false, r.Seq, fmt.Sprintf("пропуск последовательности: ожидался %d", expectSeq)
			return nil
		}
		if r.PrevHash != prevHash {
			res.OK, res.BrokenSeq, res.Reason = false, r.Seq, "prev_hash не совпадает с хешем предыдущей записи"
			return nil
		}
		h, err := ComputeHash(r)
		if err != nil {
			return err
		}
		if h != r.Hash {
			res.OK, res.BrokenSeq, res.Reason = false, r.Seq, "хеш записи не совпадает с содержимым"
			return nil
		}
		prevHash = r.Hash
		expectSeq++
		return nil
	})
	if err != nil {
		return VerifyResult{}, fmt.Errorf("audit verify: %w", err)
	}
	return res, nil
}
