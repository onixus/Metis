package economics

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	book "github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/prioritization"
)

func (s *Service) migratedDefinitions(a HistoryArchive) ([]Field, []Metric, error) {
	fields := map[string]Field{}
	metrics := map[string]Metric{}
	// Standard ledger inputs use the model's configured canonical aliases.
	standard := map[string]string{book.Revenue: s.cfg.RevenueField, book.Payroll: s.cfg.PayrollField, book.DirectCost: s.cfg.DirectCostField, book.Marketing: s.cfg.MarketingField, book.HubCost: book.HubCost, book.CertificationCost: book.CertificationCost, book.MaintenanceCost: book.MaintenanceCost, "direct_total": "direct_total", "direct_profit": "direct_profit", "loaded_profit": "loaded_profit"}
	standard["bundle_revenue"] = s.cfg.BundleRevenueField
	for _, snap := range a.BookVersions {
		activeFields := map[string]string{}
		activeMetrics := map[string]string{}
		for _, f := range snap.Fields {
			if f.Source == "calculated" {
				activeMetrics[f.Key] = a.MetricKeys[f.Key]
			} else {
				activeFields[f.Key] = a.FieldKeys[f.Key]
			}
		}

		for raw, key := range standard {
			f := fields[key]
			if f.ID == kernel.NilID {
				f = Field{ID: kernel.NewID(), Key: key, Versions: []FieldVersion{{Version: 1, Name: raw, Type: FieldMoney, Currency: snap.Currency, Source: SourceImport, EffectiveFrom: a.Period.Start(), Actor: snap.CreatedBy, At: snap.CreatedAt}}}
				fields[key] = f
			}
		}
		for _, old := range snap.Fields {
			if old.Name == "" {
				old.Name = old.Key
			}
			typ := FieldType(old.Type)
			if typ == "catalog" {
				typ = FieldReference
			}
			if old.Source != "calculated" {
				key := a.FieldKeys[old.Key]
				f := fields[key]
				if f.ID == kernel.NilID {
					f = Field{ID: kernel.NewID(), Key: key}
				}
				v := FieldVersion{Version: len(f.Versions) + 1, Name: old.Name, Type: typ, Currency: snap.Currency, Source: FieldSource(old.Source), EffectiveFrom: a.Period.Start(), Actor: snap.CreatedBy, At: snap.CreatedAt}
				if typ != FieldMoney {
					v.Currency = ""
				}
				if len(f.Versions) == 0 || !sameFieldDefinition(f.Latest(), v) || f.Latest().Name != v.Name {
					f.Versions = append(f.Versions, v)
				}
				fields[key] = f
				continue
			}
			fieldKey := a.MetricKeys[old.Key]
			field := fields[fieldKey]
			if field.ID == kernel.NilID {
				field = Field{ID: kernel.NewID(), Key: fieldKey}
			}
			fv := FieldVersion{Version: len(field.Versions) + 1, Name: old.Name, Type: typ, Source: SourceCalculated, EffectiveFrom: a.Period.Start(), Actor: snap.CreatedBy, At: snap.CreatedAt}
			if typ == FieldMoney {
				fv.Currency = snap.Currency
			}
			if len(field.Versions) == 0 || !sameFieldDefinition(field.Latest(), fv) || field.Latest().Name != fv.Name {
				field.Versions = append(field.Versions, fv)
			}
			fields[fieldKey] = field
			expression, err := translateBookFormula(old.Formula, activeFields, activeMetrics, standard)
			if err != nil {
				return nil, nil, err
			}
			if old.Type == "money" {
				expression = "round_minor(" + expression + ")"
			}
			prog, err := s.engine.Compile(expression)
			if err != nil {
				return nil, nil, fmt.Errorf("translate book metric %s: %w", old.Key, err)
			}
			key := a.MetricKeys[old.Key]
			m := metrics[key]
			if m.ID == kernel.NilID {
				m = Metric{ID: kernel.NewID(), Key: key}
			}
			v := MetricVersion{Version: len(m.Versions) + 1, Name: old.Name, Expression: expression, ResultType: typ, EffectiveFrom: a.Period.Start(), Currency: snap.Currency, Refs: prog.Refs(), Actor: snap.CreatedBy, At: snap.CreatedAt}
			if old.Type != "money" {
				v.Currency = ""
			}
			if len(m.Versions) == 0 || m.Latest().Expression != expression || m.Latest().Name != v.Name {
				m.Versions = append(m.Versions, v)
			}
			metrics[key] = m
		}
	}
	outF := []Field{}
	outM := []Metric{}
	for _, f := range fields {
		outF = append(outF, f)
	}
	for _, m := range metrics {
		outM = append(outM, m)
	}
	return outF, outM, nil
}

type legacyTranslator struct {
	tokens                    []string
	pos                       int
	fields, metrics, standard map[string]string
}

func translateBookFormula(src string, fields, metrics, standard map[string]string) (string, error) {
	if _, err := prioritization.ParseFormula(src); err != nil {
		return "", err
	}
	tokens := []string{}
	for i := 0; i < len(src); {
		r := rune(src[i])
		if unicode.IsSpace(r) {
			i++
			continue
		}
		start := i
		if (src[i] >= 'a' && src[i] <= 'z') || (src[i] >= 'A' && src[i] <= 'Z') || src[i] == '_' {
			for i < len(src) && ((src[i] >= 'a' && src[i] <= 'z') || (src[i] >= 'A' && src[i] <= 'Z') || (src[i] >= '0' && src[i] <= '9') || src[i] == '_') {
				i++
			}
		} else if (src[i] >= '0' && src[i] <= '9') || src[i] == '.' {
			for i < len(src) && ((src[i] >= '0' && src[i] <= '9') || src[i] == '.') {
				i++
			}
		} else {
			i++
		}
		tokens = append(tokens, src[start:i])
	}
	p := legacyTranslator{tokens: tokens, fields: fields, metrics: metrics, standard: standard}
	out, err := p.expr(0)
	if err != nil {
		return "", err
	}
	if p.pos != len(tokens) {
		return "", kernel.Invalid("formula", "unconsumed legacy formula tokens")
	}
	return out, nil
}
func (p *legacyTranslator) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos]
}
func (p *legacyTranslator) next() string {
	v := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return v
}
func (p *legacyTranslator) expr(min int) (string, error) {
	left, err := p.atom()
	if err != nil {
		return "", err
	}
	for {
		op := p.peek()
		prec := 0
		switch op {
		case "+", "-":
			prec = 1
		case "*", "/":
			prec = 2
		}
		if prec == 0 || prec < min {
			break
		}
		p.next()
		right, err := p.expr(prec + 1)
		if err != nil {
			return "", err
		}
		if op == "/" {
			left = "div16(" + left + "," + right + ")"
		} else {
			left = "(" + left + op + right + ")"
		}
		if len(left) > 8192 {
			return "", kernel.Invalid("formula", "translated formula exceeds limit")
		}
	}
	return left, nil
}
func (p *legacyTranslator) atom() (string, error) {
	t := p.next()
	if t == "" {
		return "", kernel.Invalid("formula", "unexpected end")
	}
	if t == "-" {
		v, err := p.atom()
		return "(-" + v + ")", err
	}
	if t == "(" {
		v, err := p.expr(0)
		if err != nil {
			return "", err
		}
		if p.next() != ")" {
			return "", kernel.Invalid("formula", "missing closing parenthesis")
		}
		return "(" + v + ")", nil
	}
	if t[0] >= '0' && t[0] <= '9' || t[0] == '.' {
		return "d(" + strconv.Quote(t) + ")", nil
	}
	if p.peek() == "(" {
		p.next()
		args := []string{}
		for {
			v, err := p.expr(0)
			if err != nil {
				return "", err
			}
			args = append(args, v)
			if p.peek() != "," {
				break
			}
			p.next()
		}
		if p.next() != ")" {
			return "", kernel.Invalid("formula", "missing call parenthesis")
		}
		if t == "ifgt" || t == "ifge" || t == "ifeq" {
			if len(args) != 4 {
				return "", kernel.Invalid("formula", "conditional arity")
			}
			op := map[string]string{"ifgt": ">", "ifge": ">=", "ifeq": "=="}[t]
			return "(" + args[0] + op + args[1] + "?" + args[2] + ":" + args[3] + ")", nil
		}
		if t != "min" && t != "max" {
			return "", kernel.Invalid("formula", "unsupported legacy function")
		}
		fn := "min_pair"
		if t == "max" {
			fn = "max_pair"
		}
		level := args
		for len(level) > 1 {
			next := []string{}
			for i := 0; i < len(level); i += 2 {
				if i+1 == len(level) {
					next = append(next, level[i])
					continue
				}
				v := fn + "(" + level[i] + "," + level[i+1] + ")"
				if len(v) > 8192 {
					return "", kernel.Invalid("formula", "translated formula exceeds limit")
				}
				next = append(next, v)
			}
			level = next
		}
		v := level[0]

		return v, nil
	}
	if key := p.metrics[t]; key != "" {
		return "metric(" + strconv.Quote(key) + ")", nil
	}
	key := p.fields[t]
	if key == "" {
		key = p.standard[t]
	}
	if key == "" {
		return "", kernel.Invalid("formula", "unknown legacy input "+strings.TrimSpace(t))
	}
	return "field(" + strconv.Quote(key) + ")", nil
}
