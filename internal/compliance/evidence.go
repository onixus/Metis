package compliance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/onixus/metis/internal/kernel"
)

// EvidenceStore — журнал доказательств. Реализации не имеют UPDATE и DELETE (инвариант 8).
type EvidenceStore interface {
	// Last возвращает последнюю запись; kernel.ErrNotFound, если журнал пуст.
	Last(ctx context.Context) (EvidenceItem, error)
	// Insert добавляет запись с заданными Seq, PrevHash и Hash.
	Insert(ctx context.Context, e EvidenceItem) error
	// Walk перебирает записи по возрастанию Seq.
	Walk(ctx context.Context, fn func(EvidenceItem) error) error
}

// EvidenceGenesisHash — предыдущий хеш первой записи журнала.
const EvidenceGenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// ComputeEvidenceHash считает SHA-256(prev_hash || canonical_json(record)).
func ComputeEvidenceHash(e EvidenceItem) (string, error) {
	body, err := kernel.CanonicalJSON(e)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(e.PrevHash))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ValidSHA256 проверяет, что строка — hex-представление SHA-256 (64 символа).
func ValidSHA256(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// appendEvidence сцепляет запись с предыдущей и добавляет её в журнал.
func appendEvidence(ctx context.Context, store EvidenceStore, e EvidenceItem) (EvidenceItem, error) {
	prev, err := store.Last(ctx)
	seq := int64(1)
	prevHash := EvidenceGenesisHash
	switch {
	case err == nil:
		seq = prev.Seq + 1
		prevHash = prev.Hash
	case kernel.IsNotFound(err):
	default:
		return EvidenceItem{}, fmt.Errorf("evidence last: %w", err)
	}
	e.Seq, e.PrevHash = seq, prevHash
	h, err := ComputeEvidenceHash(e)
	if err != nil {
		return EvidenceItem{}, err
	}
	e.Hash = h
	if err := store.Insert(ctx, e); err != nil {
		return EvidenceItem{}, fmt.Errorf("evidence insert: %w", err)
	}
	return e, nil
}

// VerifyResult — итог проверки целостности журнала доказательств.
type VerifyResult struct {
	Checked   int64
	OK        bool
	BrokenSeq int64 // первая нарушенная запись, 0 если всё цело
	Reason    string
}

// VerifyEvidenceLog проходит журнал и проверяет сцепку хешей.
func VerifyEvidenceLog(ctx context.Context, store EvidenceStore) (VerifyResult, error) {
	res := VerifyResult{OK: true}
	prevHash := EvidenceGenesisHash
	var expectSeq int64 = 1
	err := store.Walk(ctx, func(e EvidenceItem) error {
		if !res.OK {
			return nil
		}
		res.Checked++
		if e.Seq != expectSeq {
			res.OK, res.BrokenSeq, res.Reason = false, e.Seq, fmt.Sprintf("пропуск последовательности: ожидался %d", expectSeq)
			return nil
		}
		if e.PrevHash != prevHash {
			res.OK, res.BrokenSeq, res.Reason = false, e.Seq, "prev_hash не совпадает с хешем предыдущей записи"
			return nil
		}
		h, err := ComputeEvidenceHash(e)
		if err != nil {
			return err
		}
		if h != e.Hash {
			res.OK, res.BrokenSeq, res.Reason = false, e.Seq, "хеш записи не совпадает с содержимым"
			return nil
		}
		prevHash = e.Hash
		expectSeq++
		return nil
	})
	if err != nil {
		return VerifyResult{}, fmt.Errorf("evidence verify: %w", err)
	}
	return res, nil
}

// EvidenceMemStore — журнал в памяти для тестов и стендов без БД.
type EvidenceMemStore struct {
	mu   sync.RWMutex
	recs []EvidenceItem
}

// NewEvidenceMemStore создаёт пустой журнал.
func NewEvidenceMemStore() *EvidenceMemStore { return &EvidenceMemStore{} }

// Last — последняя запись.
func (m *EvidenceMemStore) Last(context.Context) (EvidenceItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.recs) == 0 {
		return EvidenceItem{}, kernel.ErrNotFound
	}
	return m.recs[len(m.recs)-1], nil
}

// Insert добавляет запись.
func (m *EvidenceMemStore) Insert(_ context.Context, e EvidenceItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.recs) > 0 && m.recs[len(m.recs)-1].Seq >= e.Seq {
		return kernel.ErrConflict
	}
	m.recs = append(m.recs, e)
	return nil
}

// Walk перебирает записи.
func (m *EvidenceMemStore) Walk(_ context.Context, fn func(EvidenceItem) error) error {
	m.mu.RLock()
	snapshot := append([]EvidenceItem(nil), m.recs...)
	m.mu.RUnlock()
	for _, e := range snapshot {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// Tamper подменяет содержимое записи (только для тестов проверки целостности).
func (m *EvidenceMemStore) Tamper(seq int64, mutate func(*EvidenceItem)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.recs {
		if m.recs[i].Seq == seq {
			mutate(&m.recs[i])
		}
	}
}
