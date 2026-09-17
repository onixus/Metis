// Package decisions — Decision Records (DA-01): контекст, снимок данных, варианты, выбор, ожидаемый эффект,
// дата ревизии и связи с гипотезами, фичами, сигналами, обязательствами, релизами и треками.
// Текст ADR живёт в базе знаний (ТЗ 4.3): страница создаётся из шаблона MADR через порт ports.KnowledgeBase
// обработчиком outbox (инвариант 5); платформа хранит PageID. Публичный интерфейс — этот пакет.
package decisions

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/knowledgedocs"
)

// Status — статус решения.
type Status string

const (
	StatusProposed   Status = "proposed"
	StatusAccepted   Status = "accepted"
	StatusSuperseded Status = "superseded"
	StatusRejected   Status = "rejected"
)

// ValidStatus сообщает, известен ли статус.
func ValidStatus(s Status) bool {
	switch s {
	case StatusProposed, StatusAccepted, StatusSuperseded, StatusRejected:
		return true
	}
	return false
}

// LinkKind — вид сущности, с которой связано решение (DS-04: трассировка до решения).
type LinkKind string

const (
	LinkHypothesis LinkKind = "hypothesis"
	LinkFeature    LinkKind = "feature"
	LinkSignal     LinkKind = "signal"
	LinkCommitment LinkKind = "commitment"
	LinkRelease    LinkKind = "release"
	LinkTrack      LinkKind = "track"
)

// ValidLinkKind сообщает, известен ли вид связи.
func ValidLinkKind(k LinkKind) bool {
	switch k {
	case LinkHypothesis, LinkFeature, LinkSignal, LinkCommitment, LinkRelease, LinkTrack:
		return true
	}
	return false
}

// Option — рассмотренный вариант решения.
type Option struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// Link — связь решения с сущностью платформы.
type Link struct {
	Kind LinkKind  `json:"kind"`
	ID   kernel.ID `json:"id"`
}

// DecisionRecord — зафиксированное решение (ТЗ 2.1, DA-01).
// ProductID = kernel.NilID — портфельное решение: запись по ActionWriteDecisions для NilID (cpo/admin),
// чтение — роли cpo/admin; иначе права проверяются по продукту.
type DecisionRecord struct {
	ID        kernel.ID `json:"id"`
	ProductID kernel.ID `json:"product_id"`
	Title     string    `json:"title"`
	Context   string    `json:"context"`
	// Snapshot — снимок данных на момент решения (ценность фич, даты, ARR); заполняет вызывающий.
	Snapshot       map[string]any `json:"snapshot,omitempty"`
	Options        []Option       `json:"options"`
	ChosenKey      string         `json:"chosen_key,omitempty"`
	Rationale      string         `json:"rationale,omitempty"`
	ExpectedEffect string         `json:"expected_effect,omitempty"`
	ReviewDate     kernel.Date    `json:"review_date"` // дата ревизии без времени (инвариант 7)
	Status         Status         `json:"status"`
	SupersededBy   kernel.ID      `json:"superseded_by,omitempty"`
	Links          []Link         `json:"links,omitempty"`
	// PageID — страница ADR в базе знаний; заполняется обработчиком outbox после создания страницы.
	PageID    string    `json:"page_id,omitempty"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DecisionRef — краткая ссылка на решение для трассировки discovery (DS-04, порт DecisionLinks).
type DecisionRef struct {
	ID    kernel.ID `json:"id"`
	Title string    `json:"title"`
}

// Названия доменных событий.
const (
	EventRecordSaved    = "decisions.record.saved"
	EventRecordAccepted = "decisions.record.accepted"
	EventPageRequested  = "decisions.page.requested"
	EventPageCreated    = "decisions.page.created"
)

// Метки и ключи page properties страницы ADR.
const (
	LabelMetis         = "metis"
	LabelADR           = "adr"
	PropDecisionID     = "metis_decision_id"
	PropDecisionStatus = "metis_decision_status"
	PropProductID      = "metis_product_id"
	PropLinkPrefix     = "metis_link_" // metis_link_<kind> = идентификаторы через запятую
	adrTitlePrefix     = "ADR: "
)

// RenderADR формирует страницу ADR по шаблону MADR со связями и снимком данных (критерий готовности этапа 2).
func RenderADR(rec DecisionRecord) string {
	in := knowledgedocs.ADRInput{
		ID:             rec.ID.String(),
		Title:          rec.Title,
		Status:         string(rec.Status),
		Context:        rec.Context,
		ChosenKey:      rec.ChosenKey,
		Rationale:      rec.Rationale,
		ExpectedEffect: rec.ExpectedEffect,
		Author:         rec.Author,
		Snapshot:       snapshotStrings(rec.Snapshot),
	}
	if !rec.ReviewDate.IsZero() {
		in.ReviewDate = rec.ReviewDate.String()
	}
	for _, o := range rec.Options {
		in.Options = append(in.Options, knowledgedocs.ADROption{Key: o.Key, Title: o.Title, Description: o.Description})
	}
	for _, l := range rec.Links {
		in.Links = append(in.Links, knowledgedocs.LinkRow{Kind: string(l.Kind), ID: l.ID.String()})
	}
	if rec.SupersededBy != kernel.NilID {
		in.Links = append(in.Links, knowledgedocs.LinkRow{Kind: "superseded_by", ID: rec.SupersededBy.String()})
	}
	return knowledgedocs.RenderADR(in)
}

// PageProperties — page properties страницы ADR: идентификатор, статус, продукт и связи по видам.
func PageProperties(rec DecisionRecord) map[string]string {
	props := map[string]string{
		PropDecisionID:     rec.ID.String(),
		PropDecisionStatus: string(rec.Status),
	}
	if rec.ProductID != kernel.NilID {
		props[PropProductID] = rec.ProductID.String()
	}
	byKind := map[LinkKind][]string{}
	for _, l := range rec.Links {
		byKind[l.Kind] = append(byKind[l.Kind], l.ID.String())
	}
	for k, ids := range byKind {
		sort.Strings(ids)
		s := ""
		for i, id := range ids {
			if i > 0 {
				s += ","
			}
			s += id
		}
		props[PropLinkPrefix+string(k)] = s
	}
	return props
}

// snapshotStrings приводит снимок к строкам: строки как есть, прочее — JSON.
func snapshotStrings(m map[string]any) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case string:
			out[k] = t
		case fmt.Stringer:
			out[k] = t.String()
		default:
			raw, err := json.Marshal(v)
			if err != nil {
				out[k] = fmt.Sprint(v)
				continue
			}
			out[k] = string(raw)
		}
	}
	return out
}
