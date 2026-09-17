package knowledgedocs_test

import (
	"strings"
	"testing"

	"github.com/onixus/metis/internal/knowledgedocs"
)

func TestDA01_ADRRenderedFromTemplateWithLinks(t *testing.T) {
	out := knowledgedocs.RenderADR(knowledgedocs.ADRInput{
		ID: "dec-1", Title: "Коннектор EDR <v2>", Status: "accepted", Author: "pm-soar",
		Context:        "Сделки блокируются\nбез коннектора",
		Options:        []knowledgedocs.ADROption{{Key: "A", Title: "Свой коннектор"}, {Key: "B", Title: "Партнёрский", Description: "лицензия"}},
		ChosenKey:      "A",
		Rationale:      "Контроль сроков",
		ExpectedEffect: "+3 сделки",
		ReviewDate:     "2027-03-01",
		Snapshot:       map[string]string{"feature_value": "1200000 RUB", "date": "2026-12-01"},
		Links:          []knowledgedocs.LinkRow{{Kind: "feature", ID: "f-1", Title: "Коннектор"}, {Kind: "hypothesis", ID: "h-1"}},
	})
	for _, want := range []string{
		"<h1>Коннектор EDR &lt;v2&gt;</h1>", "<h2>Контекст</h2>", "Сделки блокируются<br/>без коннектора",
		"<strong>A</strong>: Свой коннектор <strong>(выбран)</strong>", "Партнёрский — лицензия",
		"Выбран вариант A: Свой коннектор", "<h2>Ожидаемый эффект</h2>", "2027-03-01",
		"<th>date</th><td>2026-12-01</td>", "<th>feature_value</th>",
		"<td>feature</td><td>f-1</td><td>Коннектор</td>", "<td>hypothesis</td><td>h-1</td><td></td>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("нет фрагмента %q в:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<v2>") {
		t.Fatal("текст не экранирован")
	}
}

func TestDS05_BriefRenderedWithLinks(t *testing.T) {
	out := knowledgedocs.RenderBrief(knowledgedocs.BriefInput{
		Title:      "Brief: изоляция хоста",
		Hypotheses: []string{"Клиенты хотят <изоляцию>"},
		Insights:   []string{"3 из 5 интервью"},
		Evidence:   nil,
		Links:      map[string]string{"hypothesis": "h-1", "feature": "f-2", "signal": "s-3"},
	})
	for _, want := range []string{
		"<h1>Brief: изоляция хоста</h1>", "<li>Клиенты хотят &lt;изоляцию&gt;</li>", "<li>3 из 5 интервью</li>",
		"<h2>Evidence</h2>\n<p>Нет.</p>", "<td>feature</td><td>f-2</td>", "<td>hypothesis</td><td>h-1</td>", "<td>signal</td><td>s-3</td>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("нет фрагмента %q в:\n%s", want, out)
		}
	}
	// Порядок связей детерминирован (по виду), рендер стабилен.
	if strings.Index(out, "<td>feature</td>") > strings.Index(out, "<td>hypothesis</td>") {
		t.Fatal("связи не отсортированы")
	}
	if out != knowledgedocs.RenderBrief(knowledgedocs.BriefInput{Title: "Brief: изоляция хоста", Hypotheses: []string{"Клиенты хотят <изоляцию>"}, Insights: []string{"3 из 5 интервью"}, Links: map[string]string{"signal": "s-3", "feature": "f-2", "hypothesis": "h-1"}}) {
		t.Fatal("рендер недетерминирован")
	}
}

func TestDS05_EmptyLinksRendered(t *testing.T) {
	out := knowledgedocs.RenderBrief(knowledgedocs.BriefInput{Title: "x"})
	if !strings.Contains(out, "<p>Связей нет.</p>") {
		t.Fatalf("пустые связи: %s", out)
	}
}
