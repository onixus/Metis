package jira

import (
	"strings"
	"unicode"
)

// Лимиты длины строк из Jira (в рунах). Содержимое недоверенное (NF-S17).
const (
	MaxKey         = 64
	MaxSummary     = 512
	MaxDescription = 8192
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
