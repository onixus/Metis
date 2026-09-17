package discovery

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/onixus/metis/internal/kernel"
)

// MemIndex — индекс похожести в памяти (SG-04): TF-IDF по токенам и косинусная близость.
// Документы разделены по виду и продукту, поэтому подсказки не пересекают границу продукта.
// Внешних зависимостей нет; float64 допустим — это не деньги (инвариант 6 не затронут).
type MemIndex struct {
	mu   sync.Mutex
	docs map[indexKey]map[kernel.ID]map[string]int // bucket → документ → частоты токенов
}

type indexKey struct {
	kind      string
	productID kernel.ID
}

// NewMemIndex создаёт пустой индекс.
func NewMemIndex() *MemIndex {
	return &MemIndex{docs: map[indexKey]map[kernel.ID]map[string]int{}}
}

// Tokenize разбивает текст на токены: строчные буквы и цифры, длина от двух символов.
func Tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if len([]rune(f)) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

func termFreq(text string) map[string]int {
	tf := map[string]int{}
	for _, t := range Tokenize(text) {
		tf[t]++
	}
	return tf
}

// Upsert добавляет или заменяет документ. Пустой текст удаляет документ из индекса.
func (m *MemIndex) Upsert(_ context.Context, kind string, id, productID kernel.ID, text string) error {
	if kind == "" {
		return kernel.Invalid("kind", "обязателен")
	}
	if id == kernel.NilID || productID == kernel.NilID {
		return kernel.Invalid("id", "идентификаторы документа и продукта обязательны")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := indexKey{kind: kind, productID: productID}
	bucket := m.docs[key]
	if bucket == nil {
		bucket = map[kernel.ID]map[string]int{}
		m.docs[key] = bucket
	}
	tf := termFreq(text)
	if len(tf) == 0 {
		delete(bucket, id)
		return nil
	}
	bucket[id] = tf
	return nil
}

// Similar возвращает до limit документов вида kind продукта productID, ближайших к тексту по
// косинусу TF-IDF-векторов; совпадения с нулевой близостью не возвращаются. limit ≤ 0 — без ограничения.
func (m *MemIndex) Similar(_ context.Context, kind string, productID kernel.ID, text string, limit int) ([]Match, error) {
	query := termFreq(text)
	if len(query) == 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.docs[indexKey{kind: kind, productID: productID}]
	if len(bucket) == 0 {
		return nil, nil
	}
	// Документная частота по корпусу (запрос в корпус не входит).
	df := map[string]int{}
	for _, tf := range bucket {
		for t := range tf {
			df[t]++
		}
	}
	n := float64(len(bucket))
	idf := func(t string) float64 {
		// Сглаженный IDF: термин, встречающийся во всех документах, не обнуляется.
		return math.Log(1+n/float64(1+df[t])) + 1
	}
	qv, qn := vector(query, idf)
	if qn == 0 {
		return nil, nil
	}
	out := make([]Match, 0, len(bucket))
	for id, tf := range bucket {
		dv, dn := vector(tf, idf)
		if dn == 0 {
			continue
		}
		var dot float64
		for t, w := range qv {
			dot += w * dv[t]
		}
		if score := dot / (qn * dn); score > 0 {
			out = append(out, Match{ID: id, Score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// vector строит TF-IDF-вектор и его норму.
func vector(tf map[string]int, idf func(string) float64) (map[string]float64, float64) {
	v := make(map[string]float64, len(tf))
	var sum float64
	for t, c := range tf {
		w := float64(c) * idf(t)
		v[t] = w
		sum += w * w
	}
	return v, math.Sqrt(sum)
}
