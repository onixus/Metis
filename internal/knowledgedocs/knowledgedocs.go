// Package knowledgedocs — рендеры документов для базы знаний (ТЗ 4.3): ADR (DA-01) и discovery brief (DS-05).
// Чистые функции без зависимостей: на входе данные платформы, на выходе storage-разметка
// (XHTML-подобный формат Confluence Data Center). Весь текст экранируется html.EscapeString;
// разметка не интерпретирует пользовательский ввод.
package knowledgedocs

import (
	"html"
	"sort"
	"strings"
)

// LinkRow — строка таблицы связей: вид сущности платформы, её идентификатор и подпись.
type LinkRow struct {
	Kind  string
	ID    string
	Title string
}

// ADROption — вариант решения в шаблоне MADR.
type ADROption struct {
	Key         string
	Title       string
	Description string
}

// ADRInput — данные Decision Record для шаблона MADR (DA-01).
type ADRInput struct {
	ID             string
	Title          string
	Status         string
	Context        string
	Options        []ADROption
	ChosenKey      string
	Rationale      string
	ExpectedEffect string
	ReviewDate     string // дата ревизии в виде YYYY-MM-DD
	Snapshot       map[string]string
	Links          []LinkRow
	Author         string
}

// BriefInput — данные discovery brief (DS-05).
type BriefInput struct {
	Title      string
	Hypotheses []string
	Insights   []string
	Evidence   []string
	// Links — связи «вид → идентификатор» (гипотеза, фича, сигнал, решение), выводятся таблицей.
	Links map[string]string
}

// RenderADR формирует страницу ADR по шаблону MADR (контекст, варианты, решение, последствия)
// с таблицей связей и снимком данных на момент решения.
func RenderADR(in ADRInput) string {
	var b strings.Builder
	b.WriteString("<h1>" + esc(in.Title) + "</h1>\n")
	writeMeta(&b, [][2]string{
		{"Идентификатор", in.ID},
		{"Статус", in.Status},
		{"Автор", in.Author},
		{"Дата ревизии", in.ReviewDate},
	})
	b.WriteString("<h2>Контекст</h2>\n<p>" + para(in.Context) + "</p>\n")
	b.WriteString("<h2>Рассмотренные варианты</h2>\n")
	if len(in.Options) == 0 {
		b.WriteString("<p>Варианты не указаны.</p>\n")
	} else {
		b.WriteString("<ul>\n")
		for _, o := range in.Options {
			mark := ""
			if o.Key == in.ChosenKey {
				mark = " <strong>(выбран)</strong>"
			}
			b.WriteString("<li><strong>" + esc(o.Key) + "</strong>: " + esc(o.Title) + mark)
			if o.Description != "" {
				b.WriteString(" — " + esc(o.Description))
			}
			b.WriteString("</li>\n")
		}
		b.WriteString("</ul>\n")
	}
	b.WriteString("<h2>Решение</h2>\n<p>" + para(chosenTitle(in)) + "</p>\n<p>" + para(in.Rationale) + "</p>\n")
	b.WriteString("<h2>Ожидаемый эффект</h2>\n<p>" + para(in.ExpectedEffect) + "</p>\n")
	if len(in.Snapshot) > 0 {
		b.WriteString("<h2>Снимок данных на момент решения</h2>\n")
		writeMeta(&b, sortedPairs(in.Snapshot))
	}
	b.WriteString("<h2>Связи</h2>\n")
	writeLinks(&b, in.Links)
	return b.String()
}

// RenderBrief формирует страницу discovery brief: гипотезы, инсайты, evidence и таблица связей (DS-05).
func RenderBrief(in BriefInput) string {
	var b strings.Builder
	b.WriteString("<h1>" + esc(in.Title) + "</h1>\n")
	writeList(&b, "Гипотезы", in.Hypotheses)
	writeList(&b, "Инсайты", in.Insights)
	writeList(&b, "Evidence", in.Evidence)
	b.WriteString("<h2>Связи</h2>\n")
	rows := make([]LinkRow, 0, len(in.Links))
	for _, kv := range sortedPairs(in.Links) {
		rows = append(rows, LinkRow{Kind: kv[0], ID: kv[1]})
	}
	writeLinks(&b, rows)
	return b.String()
}

func chosenTitle(in ADRInput) string {
	for _, o := range in.Options {
		if o.Key == in.ChosenKey {
			return "Выбран вариант " + o.Key + ": " + o.Title
		}
	}
	if in.ChosenKey != "" {
		return "Выбран вариант " + in.ChosenKey
	}
	return "Вариант не выбран."
}

func writeList(b *strings.Builder, title string, items []string) {
	b.WriteString("<h2>" + esc(title) + "</h2>\n")
	if len(items) == 0 {
		b.WriteString("<p>Нет.</p>\n")
		return
	}
	b.WriteString("<ul>\n")
	for _, it := range items {
		b.WriteString("<li>" + esc(it) + "</li>\n")
	}
	b.WriteString("</ul>\n")
}

func writeMeta(b *strings.Builder, rows [][2]string) {
	b.WriteString("<table><tbody>\n")
	for _, r := range rows {
		b.WriteString("<tr><th>" + esc(r[0]) + "</th><td>" + esc(r[1]) + "</td></tr>\n")
	}
	b.WriteString("</tbody></table>\n")
}

func writeLinks(b *strings.Builder, rows []LinkRow) {
	if len(rows) == 0 {
		b.WriteString("<p>Связей нет.</p>\n")
		return
	}
	b.WriteString("<table><thead><tr><th>Вид</th><th>Идентификатор</th><th>Название</th></tr></thead><tbody>\n")
	for _, r := range rows {
		b.WriteString("<tr><td>" + esc(r.Kind) + "</td><td>" + esc(r.ID) + "</td><td>" + esc(r.Title) + "</td></tr>\n")
	}
	b.WriteString("</tbody></table>\n")
}

func sortedPairs(m map[string]string) [][2]string {
	out := make([][2]string, 0, len(m))
	for k, v := range m {
		out = append(out, [2]string{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func esc(s string) string { return html.EscapeString(s) }

// para экранирует текст и переводит переводы строк в <br/>.
func para(s string) string {
	return strings.ReplaceAll(esc(strings.TrimSpace(s)), "\n", "<br/>")
}
