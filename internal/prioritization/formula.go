package prioritization

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
)

// Ограничения вычислителя (защита от злоупотреблений: произвольный код невозможен по построению,
// лимиты защищают от глубокой рекурсии и больших выражений).
const (
	MaxFormulaLength = 1024
	MaxFormulaDepth  = 32
)

// Formula — разобранное арифметическое выражение. Поддерживаются числа, + - * /, скобки,
// унарный минус, идентификаторы переменных и функции min/max, ifgt/ifge/ifeq.
// Условные функции вычисляют только выбранную ветку. Циклов, присваиваний,
// доступа к окружению нет.
type Formula struct {
	src  string
	root node
	vars []string
}

// ParseFormula разбирает выражение. Ошибки — kernel.ErrValidation.
func ParseFormula(src string) (*Formula, error) {
	if strings.TrimSpace(src) == "" {
		return nil, kernel.Invalid("formula", "пустое выражение")
	}
	if len(src) > MaxFormulaLength {
		return nil, kernel.Invalid("formula", fmt.Sprintf("длина выражения превышает %d символов", MaxFormulaLength))
	}
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, seen: map[string]struct{}{}}
	root, err := p.expr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, kernel.Invalid("formula", fmt.Sprintf("неожиданный символ %q в позиции %d", p.peek().text, p.peek().pos))
	}
	return &Formula{src: src, root: root, vars: p.vars}, nil
}

// Source — исходный текст.
func (f *Formula) Source() string { return f.src }

// Variables — переменные в порядке первого появления.
func (f *Formula) Variables() []string { return append([]string(nil), f.vars...) }

// Eval вычисляет выражение. Отсутствующая переменная и деление на ноль — kernel.ErrValidation.
func (f *Formula) Eval(vars map[string]decimal.Decimal) (decimal.Decimal, error) {
	return f.root.eval(vars)
}

// ---- лексер ----

type tokKind int

const (
	tokEOF tokKind = iota
	tokNumber
	tokIdent
	tokOp // + - * / ( ) ,
)

type token struct {
	kind tokKind
	text string
	pos  int
}

func tokenize(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isDigit(c) || (c == '.' && i+1 < len(src) && isDigit(src[i+1])):
			start := i
			dot := false
			for i < len(src) && (isDigit(src[i]) || src[i] == '.') {
				if src[i] == '.' {
					if dot {
						return nil, kernel.Invalid("formula", fmt.Sprintf("некорректное число в позиции %d", start))
					}
					dot = true
				}
				i++
			}
			toks = append(toks, token{tokNumber, src[start:i], start})
		case isIdentStart(c):
			start := i
			for i < len(src) && isIdentChar(src[i]) {
				i++
			}
			toks = append(toks, token{tokIdent, src[start:i], start})
		case strings.IndexByte("+-*/(),", c) >= 0:
			toks = append(toks, token{tokOp, string(c), i})
			i++
		default:
			return nil, kernel.Invalid("formula", fmt.Sprintf("недопустимый символ %q в позиции %d", c, i))
		}
	}
	return append(toks, token{tokEOF, "", len(src)}), nil
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIdentChar(c byte) bool  { return isIdentStart(c) || isDigit(c) }

// ---- парсер (рекурсивный спуск) ----

type parser struct {
	toks  []token
	i     int
	depth int
	vars  []string
	seen  map[string]struct{}
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }

func (p *parser) isOp(s string) bool { return p.peek().kind == tokOp && p.peek().text == s }

func (p *parser) enter() error {
	p.depth++
	if p.depth > MaxFormulaDepth {
		return kernel.Invalid("formula", fmt.Sprintf("глубина вложенности превышает %d", MaxFormulaDepth))
	}
	return nil
}

func (p *parser) leave() { p.depth-- }

func (p *parser) expr() (node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	left, err := p.term()
	if err != nil {
		return nil, err
	}
	for p.isOp("+") || p.isOp("-") {
		op := p.next().text
		right, err := p.term()
		if err != nil {
			return nil, err
		}
		left = &binary{op: op[0], l: left, r: right}
	}
	return left, nil
}

func (p *parser) term() (node, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for p.isOp("*") || p.isOp("/") {
		op := p.next().text
		right, err := p.unary()
		if err != nil {
			return nil, err
		}
		left = &binary{op: op[0], l: left, r: right}
	}
	return left, nil
}

func (p *parser) unary() (node, error) {
	if p.isOp("-") {
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer p.leave()
		p.next()
		operand, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &neg{operand}, nil
	}
	return p.primary()
}

func (p *parser) primary() (node, error) {
	t := p.next()
	switch t.kind {
	case tokNumber:
		v, err := decimal.NewFromString(t.text)
		if err != nil {
			return nil, kernel.Invalid("formula", fmt.Sprintf("некорректное число %q", t.text))
		}
		return &literal{v}, nil
	case tokIdent:
		if p.isOp("(") {
			return p.call(t.text)
		}
		if _, ok := p.seen[t.text]; !ok {
			p.seen[t.text] = struct{}{}
			p.vars = append(p.vars, t.text)
		}
		return &variable{t.text}, nil
	case tokOp:
		if t.text == "(" {
			inner, err := p.expr()
			if err != nil {
				return nil, err
			}
			if !p.isOp(")") {
				return nil, kernel.Invalid("formula", fmt.Sprintf("ожидалась ')' в позиции %d", p.peek().pos))
			}
			p.next()
			return inner, nil
		}
	}
	return nil, kernel.Invalid("formula", fmt.Sprintf("неожиданный символ %q в позиции %d", t.text, t.pos))
}

func (p *parser) call(name string) (node, error) {
	if name != "min" && name != "max" && name != "ifgt" && name != "ifge" && name != "ifeq" {
		return nil, kernel.Invalid("formula", fmt.Sprintf("неизвестная функция %q; доступны min, max, ifgt, ifge, ifeq", name))
	}
	p.next() // (
	var args []node
	for {
		a, err := p.expr()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if p.isOp(",") {
			p.next()
			continue
		}
		break
	}
	if !p.isOp(")") {
		return nil, kernel.Invalid("formula", fmt.Sprintf("ожидалась ')' в позиции %d", p.peek().pos))
	}
	p.next()
	if (name == "ifgt" || name == "ifge" || name == "ifeq") && len(args) != 4 {
		return nil, kernel.Invalid("formula", "условная функция требует четыре аргумента")
	}
	return &call{name: name, args: args}, nil
}

// ---- дерево ----

type node interface {
	eval(vars map[string]decimal.Decimal) (decimal.Decimal, error)
}

type literal struct{ v decimal.Decimal }

func (n *literal) eval(map[string]decimal.Decimal) (decimal.Decimal, error) { return n.v, nil }

type variable struct{ name string }

func (n *variable) eval(vars map[string]decimal.Decimal) (decimal.Decimal, error) {
	v, ok := vars[n.name]
	if !ok {
		return decimal.Zero, kernel.Invalid("formula", fmt.Sprintf("переменная %q не задана", n.name))
	}
	return v, nil
}

type neg struct{ x node }

func (n *neg) eval(vars map[string]decimal.Decimal) (decimal.Decimal, error) {
	v, err := n.x.eval(vars)
	if err != nil {
		return decimal.Zero, err
	}
	return v.Neg(), nil
}

type binary struct {
	op   byte
	l, r node
}

func (n *binary) eval(vars map[string]decimal.Decimal) (decimal.Decimal, error) {
	l, err := n.l.eval(vars)
	if err != nil {
		return decimal.Zero, err
	}
	r, err := n.r.eval(vars)
	if err != nil {
		return decimal.Zero, err
	}
	switch n.op {
	case '+':
		return l.Add(r), nil
	case '-':
		return l.Sub(r), nil
	case '*':
		return l.Mul(r), nil
	default:
		if r.IsZero() {
			return decimal.Zero, kernel.Invalid("formula", "деление на ноль")
		}
		return l.Div(r), nil
	}
}

type call struct {
	name string
	args []node
}

func (n *call) eval(vars map[string]decimal.Decimal) (decimal.Decimal, error) {
	if n.name == "ifgt" || n.name == "ifge" || n.name == "ifeq" {
		left, err := n.args[0].eval(vars)
		if err != nil {
			return decimal.Zero, err
		}
		right, err := n.args[1].eval(vars)
		if err != nil {
			return decimal.Zero, err
		}
		matches := (n.name == "ifgt" && left.GreaterThan(right)) || (n.name == "ifge" && left.GreaterThanOrEqual(right)) || (n.name == "ifeq" && left.Equal(right))
		if matches {
			return n.args[2].eval(vars)
		}
		return n.args[3].eval(vars)
	}
	acc, err := n.args[0].eval(vars)
	if err != nil {
		return decimal.Zero, err
	}
	for _, a := range n.args[1:] {
		v, err := a.eval(vars)
		if err != nil {
			return decimal.Zero, err
		}
		if (n.name == "min" && v.LessThan(acc)) || (n.name == "max" && v.GreaterThan(acc)) {
			acc = v
		}
	}
	return acc, nil
}
