package formula

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/interpreter/functions"
	"github.com/shopspring/decimal"
)

// ErrCompile — формула не компилируется; ErrEval — ошибка вычисления.
var (
	ErrCompile = errors.New("формула не компилируется")
	ErrEval    = errors.New("формула не вычисляется")
)

// Dimension — измерение, по которому берётся агрегат (EC-08).
type Dimension string

// Измерения данных экономики.
const (
	DimProduct Dimension = "product"
	DimTeam    Dimension = "team"
	DimPeriod  Dimension = "period"
	DimItem    Dimension = "item"
)

// Any — значение измерения «все» в агрегатах: sum("revenue", "product", "*").
const Any = "*"

// Valid сообщает, известно ли измерение.
func (d Dimension) Valid() bool {
	switch d {
	case DimProduct, DimTeam, DimPeriod, DimItem:
		return true
	}
	return false
}

// Slice — срез, в котором вычисляется показатель. Значения измерений — строки,
// чтобы движок не зависел от доменных типов экономики.
type Slice struct {
	Product string
	Team    string
	Period  string
	Item    string
}

// Filter — что именно запрашивается у источника данных: срез плюс раскрытие
// одного измерения (Dim пусто — строго срез).
type Filter struct {
	Slice Slice
	Dim   Dimension
	Value string
}

// Source — источник данных для формул. Реализуется модулем economics;
// в сценариях (DA-02, EC-13) подменяет часть значений, не трогая факты.
type Source interface {
	// Values возвращает значения поля, попавшие под фильтр.
	Values(key string, f Filter) ([]decimal.Decimal, error)
	// Metric возвращает значение другого показателя в срезе.
	Metric(key string, s Slice) (decimal.Decimal, error)
}

// RefKind — вид ссылки формулы.
type RefKind string

// Виды ссылок.
const (
	RefField  RefKind = "field"
	RefMetric RefKind = "metric"
)

// Ref — ссылка формулы на поле или другой показатель; основа графа зависимостей (EC-10).
type Ref struct {
	Kind RefKind `json:"kind"`
	Key  string  `json:"key"`
}

// Имена функций формулы.
const (
	fnField  = "field"
	fnMetric = "metric"
	fnSum    = "sum"
	fnAvg    = "avg"
	fnCount  = "count"
	fnMin    = "min"
	fnMax    = "max"
	fnDec    = "d"
)

// aggFuncs — агрегаты; каждый имеет форму из одного аргумента (срез)
// и из трёх (ключ, измерение, значение).
var aggFuncs = map[string]func([]decimal.Decimal) decimal.Decimal{
	fnSum:   aggSum,
	fnAvg:   aggAvg,
	fnCount: func(v []decimal.Decimal) decimal.Decimal { return decimal.NewFromInt(int64(len(v))) },
	fnMin:   aggMin,
	fnMax:   aggMax,
}

// DefaultCostLimit — предел стоимости вычисления одной формулы (EC-09: расчёт ограничен сверху).
const DefaultCostLimit uint64 = 20_000

// Engine компилирует и вычисляет формулы. Потокобезопасен.
type Engine struct {
	env       *cel.Env
	costLimit uint64
}

// New создаёт движок. Среда CEL собирается без макросов и без расширений:
// доступны только арифметика, сравнения, тернарный оператор и функции данных.
func New() (*Engine, error) {
	dec := DecimalType
	opts := []cel.EnvOption{
		cel.ClearMacros(),
		cel.Variable("product", cel.StringType),
		cel.Variable("team", cel.StringType),
		cel.Variable("period", cel.StringType),
		cel.Variable("item", cel.StringType),
		cel.Function(fnField, cel.Overload("field_string", []*cel.Type{cel.StringType}, dec)),
		cel.Function(fnMetric, cel.Overload("metric_string", []*cel.Type{cel.StringType}, dec)),
		cel.Function(fnDec, cel.Overload("d_string", []*cel.Type{cel.StringType}, dec)),
	}
	for name := range aggFuncs {
		opts = append(opts,
			cel.Function(name,
				cel.Overload(name+"_string", []*cel.Type{cel.StringType}, dec),
				cel.Overload(name+"_string_string_string", []*cel.Type{cel.StringType, cel.StringType, cel.StringType}, dec)),
		)
	}
	opts = append(opts, arithmeticOverloads(dec)...)
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, fmt.Errorf("%w: среда: %w", ErrCompile, err)
	}
	return &Engine{env: env, costLimit: DefaultCostLimit}, nil
}

// WithCostLimit задаёт предел стоимости вычисления.
func (e *Engine) WithCostLimit(limit uint64) *Engine {
	c := *e
	c.costLimit = limit
	return &c
}

// arithmeticOverloads объявляет операции над decimal. Привязок у них нет: стандартная
// библиотека CEL сама вызывает методы traits у левого операнда (см. decimal.go), поэтому
// decimal обязан стоять слева. Литерал-коэффициент пишется справа — «field("fot") * 0.5» —
// или оборачивается в d("0.5").
func arithmeticOverloads(dec *cel.Type) []cel.EnvOption {
	rhs := []*cel.Type{dec, cel.IntType, cel.DoubleType}
	decls := func(op string, result *cel.Type) cel.EnvOption {
		overloads := make([]cel.FunctionOpt, 0, len(rhs))
		for _, r := range rhs {
			overloads = append(overloads, cel.Overload(
				fmt.Sprintf("%s_decimal_%s", strings.Trim(op, "_"), typeName(r)),
				[]*cel.Type{dec, r}, result))
		}
		return cel.Function(op, overloads...)
	}
	return []cel.EnvOption{
		decls("_+_", dec),
		decls("_-_", dec),
		decls("_*_", dec),
		decls("_/_", dec),
		decls("_<_", cel.BoolType),
		decls("_<=_", cel.BoolType),
		decls("_>_", cel.BoolType),
		decls("_>=_", cel.BoolType),
		cel.Function("-_", cel.Overload("neg_decimal", []*cel.Type{dec}, dec)),
	}
}

func typeName(t *cel.Type) string {
	switch t {
	case cel.IntType:
		return "int"
	case cel.DoubleType:
		return "double"
	}
	return "decimal"
}

// Program — скомпилированная формула.
type Program struct {
	engine *Engine
	ast    *cel.Ast
	src    string
	refs   []Ref
}

// Source возвращает исходный текст формулы.
func (p *Program) Source() string { return p.src }

// Refs возвращает ссылки формулы на поля и показатели в устойчивом порядке.
func (p *Program) Refs() []Ref { return append([]Ref(nil), p.refs...) }

// Compile компилирует формулу и собирает её ссылки. Ключи полей и показателей
// обязаны быть строковыми константами: иначе граф зависимостей (EC-10) не построить.
func (e *Engine) Compile(src string) (*Program, error) {
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("%w: пустое выражение", ErrCompile)
	}
	ast, iss := e.env.Compile(src)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompile, iss.Err())
	}
	if !ast.OutputType().IsExactType(DecimalType) {
		return nil, fmt.Errorf("%w: результат формулы должен быть числом, получен %v", ErrCompile, ast.OutputType())
	}
	refs, err := collectRefs(ast)
	if err != nil {
		return nil, err
	}
	return &Program{engine: e, ast: ast, src: src, refs: refs}, nil
}

func collectRefs(ast *cel.Ast) ([]Ref, error) {
	seen := map[Ref]struct{}{}
	var bad error
	celast.PostOrderVisit(ast.NativeRep().Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		if e.Kind() != celast.CallKind {
			return
		}
		call := e.AsCall()
		name := call.FunctionName()
		kind := RefKind("")
		switch name {
		case fnField:
			kind = RefField
		case fnMetric:
			kind = RefMetric
		default:
			if _, ok := aggFuncs[name]; ok {
				kind = RefField
			}
		}
		if kind == "" {
			return
		}
		args := call.Args()
		if len(args) == 0 {
			return
		}
		key, ok := literalString(args[0])
		if !ok {
			bad = fmt.Errorf("%w: ключ в %s(...) должен быть строковой константой", ErrCompile, name)
			return
		}
		if len(args) == 3 {
			dim, ok := literalString(args[1])
			if !ok {
				bad = fmt.Errorf("%w: измерение в %s(...) должно быть строковой константой", ErrCompile, name)
				return
			}
			if !Dimension(dim).Valid() {
				bad = fmt.Errorf("%w: неизвестное измерение %q", ErrCompile, dim)
				return
			}
			if _, ok := literalString(args[2]); !ok {
				bad = fmt.Errorf("%w: значение измерения в %s(...) должно быть строковой константой", ErrCompile, name)
				return
			}
		}
		seen[Ref{Kind: kind, Key: key}] = struct{}{}
	}))
	if bad != nil {
		return nil, bad
	}
	out := make([]Ref, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

func literalString(e celast.Expr) (string, bool) {
	if e.Kind() != celast.LiteralKind {
		return "", false
	}
	s, ok := e.AsLiteral().(types.String)
	if !ok {
		return "", false
	}
	return string(s), true
}

// Eval вычисляет формулу в срезе. Источник данных передаётся на каждое вычисление,
// поэтому один и тот же Program используется и для фактов, и для сценариев.
func (p *Program) Eval(src Source, s Slice) (decimal.Decimal, error) {
	if src == nil {
		return decimal.Zero, fmt.Errorf("%w: источник данных не задан", ErrEval)
	}
	// Привязки функций задаются на уровне программы: источник данных меняется от вызова
	// к вызову (факты или сценарий), а среда и скомпилированный AST переиспользуются.
	// cel.Functions помечен deprecated; замены с той же семантикой в cel-go нет
	// (см. docs/questions.md, вопрос 34).
	prg, err := p.engine.env.Program(p.ast,
		cel.CostLimit(p.engine.costLimit),
		cel.Functions(bindings(src, s)...), //nolint:staticcheck // привязка на уровне программы (вопрос 34)
	)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: план: %w", ErrEval, err)
	}
	out, _, err := prg.Eval(map[string]any{
		"product": s.Product, "team": s.Team, "period": s.Period, "item": s.Item,
	})
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: %w", ErrEval, err)
	}
	d, err := toDecimal(out)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: %w", ErrEval, err)
	}
	return d, nil
}

func bindings(src Source, s Slice) []*functions.Overload {
	valuesOf := func(key string, f Filter) (ref.Val, []decimal.Decimal) {
		vals, err := src.Values(key, f)
		if err != nil {
			return types.NewErr("поле %q: %v", key, err), nil
		}
		return nil, vals
	}
	out := []*functions.Overload{
		{
			Operator: "field_string",
			Unary: func(v ref.Val) ref.Val {
				key, ok := v.(types.String)
				if !ok {
					return types.NewErr("field: ключ должен быть строкой")
				}
				errVal, vals := valuesOf(string(key), Filter{Slice: s})
				if errVal != nil {
					return errVal
				}
				return Dec(aggSum(vals))
			},
		},
		{
			Operator: "metric_string",
			Unary: func(v ref.Val) ref.Val {
				key, ok := v.(types.String)
				if !ok {
					return types.NewErr("metric: ключ должен быть строкой")
				}
				d, err := src.Metric(string(key), s)
				if err != nil {
					return types.NewErr("показатель %q: %v", string(key), err)
				}
				return Dec(d)
			},
		},
		{
			Operator: "d_string",
			Unary: func(v ref.Val) ref.Val {
				d, err := toDecimal(v)
				if err != nil {
					return types.NewErr("%v", err)
				}
				return Dec(d)
			},
		},
	}
	for name, agg := range aggFuncs {
		out = append(out,
			&functions.Overload{
				Operator: name + "_string",
				Unary: func(v ref.Val) ref.Val {
					key, ok := v.(types.String)
					if !ok {
						return types.NewErr("%s: ключ должен быть строкой", name)
					}
					errVal, vals := valuesOf(string(key), Filter{Slice: s})
					if errVal != nil {
						return errVal
					}
					return Dec(agg(vals))
				},
			},
			&functions.Overload{
				Operator: name + "_string_string_string",
				Function: func(args ...ref.Val) ref.Val {
					if len(args) != 3 {
						return types.NewErr("%s: ожидаются ключ, измерение и значение", name)
					}
					key, ok1 := args[0].(types.String)
					dim, ok2 := args[1].(types.String)
					val, ok3 := args[2].(types.String)
					if !ok1 || !ok2 || !ok3 {
						return types.NewErr("%s: аргументы должны быть строками", name)
					}
					errVal, vals := valuesOf(string(key), Filter{Slice: s, Dim: Dimension(dim), Value: string(val)})
					if errVal != nil {
						return errVal
					}
					return Dec(agg(vals))
				},
			},
		)
	}
	return out
}

func aggSum(v []decimal.Decimal) decimal.Decimal {
	out := decimal.Zero
	for _, d := range v {
		out = out.Add(d)
	}
	return out
}

func aggAvg(v []decimal.Decimal) decimal.Decimal {
	if len(v) == 0 {
		return decimal.Zero
	}
	return aggSum(v).DivRound(decimal.NewFromInt(int64(len(v))), 10)
}

func aggMin(v []decimal.Decimal) decimal.Decimal {
	if len(v) == 0 {
		return decimal.Zero
	}
	out := v[0]
	for _, d := range v[1:] {
		if d.LessThan(out) {
			out = d
		}
	}
	return out
}

func aggMax(v []decimal.Decimal) decimal.Decimal {
	if len(v) == 0 {
		return decimal.Zero
	}
	out := v[0]
	for _, d := range v[1:] {
		if d.GreaterThan(out) {
			out = d
		}
	}
	return out
}

// String возвращает вид ссылки строкой.
func (k RefKind) String() string { return string(k) }
