package confluence

import (
	"html"
	"strings"
	"unicode"
)

// Лимиты длины строк из Confluence (в рунах). Содержимое недоверенное (NF-S17).
const (
	MaxKey     = 64
	MaxSummary = 512
	MaxBody    = 65536
)

// Sanitize обрезает строку до max рун и удаляет управляющие символы, кроме перевода строки и табуляции.
// Строка не интерпретируется: экранирование при отображении — задача фронтенда.
func Sanitize(s string, max int) string {
	if !strings.ContainsFunc(s, isControl) {
		return truncate(s, max)
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return truncate(b.String(), max)
}

// StripTags удаляет теги storage-разметки и раскрывает HTML-сущности; результат — текст для индекса.
// Разметка не разбирается как документ: любой фрагмент «<…>» считается тегом, блочные теги дают пробел.
func StripTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			b.WriteRune(' ')
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(html.UnescapeString(b.String())), " ")
}

func isControl(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return unicode.IsControl(r) || r == unicode.ReplacementChar
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}
