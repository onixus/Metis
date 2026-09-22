// Package economics — экономика портфеля (ТЗ 3.9, EC-01…EC-13): импорт финансовых данных,
// настраиваемые поля и формулы, аллокация затрат хаба, P&L продукта и портфеля, сценарии.
//
// Деньги хранятся в минорных единицах (инвариант 6); промежуточные расчёты ведутся в decimal
// и округляются до минорной единицы только на выходе. Доступ к данным ограничен уровнем
// authz.FinanceLevel (NF-S02), просмотр и экспорт пишутся в аудит (AD-04).
package economics

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/kernel"
)

// Period — расчётный период (месяц). Финансовые данные приходят помесячно.
type Period struct {
	Year  int        `json:"year"`
	Month time.Month `json:"month"`
}

// ParsePeriod разбирает период в формате YYYY-MM.
func ParsePeriod(s string) (Period, error) {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) != 2 {
		return Period{}, kernel.Invalid("period", "период задаётся как YYYY-MM")
	}
	y, err := strconv.Atoi(parts[0])
	if err != nil || y < 1970 || y > 9999 {
		return Period{}, kernel.Invalid("period", "недопустимый год")
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 1 || m > 12 {
		return Period{}, kernel.Invalid("period", "недопустимый месяц")
	}
	return Period{Year: y, Month: time.Month(m)}, nil
}

// PeriodOf строит период из года и месяца.
func PeriodOf(y int, m time.Month) Period { return Period{Year: y, Month: m} }

// IsZero сообщает, задан ли период.
func (p Period) IsZero() bool { return p.Year == 0 }

// String возвращает YYYY-MM.
func (p Period) String() string {
	if p.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d", p.Year, int(p.Month))
}

// MarshalJSON выдаёт YYYY-MM.
func (p Period) MarshalJSON() ([]byte, error) { return []byte(`"` + p.String() + `"`), nil }

// UnmarshalJSON читает YYYY-MM.
func (p *Period) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*p = Period{}
		return nil
	}
	v, err := ParsePeriod(s)
	if err != nil {
		return err
	}
	*p = v
	return nil
}

// Start — первый день периода.
func (p Period) Start() kernel.Date { return kernel.DateOf(p.Year, p.Month, 1) }

// End — последний день периода.
func (p Period) End() kernel.Date {
	return kernel.DateFromTime(time.Date(p.Year, p.Month+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1))
}

// Before сравнивает периоды.
func (p Period) Before(o Period) bool { return p.Start().Before(o.Start()) }

// FieldType — тип финансового поля (EC-08).
type FieldType string

// Типы полей.
const (
	FieldMoney     FieldType = "money"
	FieldNumber    FieldType = "number"
	FieldPercent   FieldType = "percent"
	FieldDate      FieldType = "date"
	FieldReference FieldType = "reference"
)

func (t FieldType) valid() bool {
	switch t {
	case FieldMoney, FieldNumber, FieldPercent, FieldDate, FieldReference:
		return true
	}
	return false
}

// FieldSource — источник значений поля (EC-08).
type FieldSource string

// Источники значений.
const (
	SourceImport     FieldSource = "import"
	SourceManual     FieldSource = "manual"
	SourceCalculated FieldSource = "calculated"
)

func (s FieldSource) valid() bool {
	return s == SourceImport || s == SourceManual || s == SourceCalculated
}

// FieldVersion — версия описания поля с датой действия (EC-11).
type FieldVersion struct {
	Version       int                 `json:"version"`
	EffectiveFrom kernel.Date         `json:"effective_from"`
	Name          string              `json:"name"`
	Type          FieldType           `json:"type"`
	Currency      string              `json:"currency,omitempty"`
	Dimensions    []formula.Dimension `json:"dimensions"`
	Source        FieldSource         `json:"source"`
	Actor         string              `json:"actor"`
	At            time.Time           `json:"at"`
}

// Field — настраиваемое финансовое поле (EC-08). Ключ неизменен, описание версионируется.
type Field struct {
	ID       kernel.ID      `json:"id"`
	Key      string         `json:"key"`
	Versions []FieldVersion `json:"versions"`
}

// At возвращает версию поля, действующую на дату.
func (f Field) At(d kernel.Date) (FieldVersion, bool) { return versionAt(f.Versions, d) }

// Latest возвращает последнюю версию поля.
func (f Field) Latest() FieldVersion {
	if len(f.Versions) == 0 {
		return FieldVersion{}
	}
	return f.Versions[len(f.Versions)-1]
}

// MetricVersion — версия формулы показателя с датой действия (EC-11).
type MetricVersion struct {
	Version       int           `json:"version"`
	EffectiveFrom kernel.Date   `json:"effective_from"`
	Name          string        `json:"name"`
	Expression    string        `json:"expression"`
	Refs          []formula.Ref `json:"refs"`
	Currency      string        `json:"currency,omitempty"`
	Actor         string        `json:"actor"`
	At            time.Time     `json:"at"`
}

// Metric — расчётный показатель (EC-09). Формула версионируется.
type Metric struct {
	ID       kernel.ID       `json:"id"`
	Key      string          `json:"key"`
	Versions []MetricVersion `json:"versions"`
}

// At возвращает версию формулы, действующую на дату.
func (m Metric) At(d kernel.Date) (MetricVersion, bool) { return versionAt(m.Versions, d) }

// Latest возвращает последнюю версию формулы.
func (m Metric) Latest() MetricVersion {
	if len(m.Versions) == 0 {
		return MetricVersion{}
	}
	return m.Versions[len(m.Versions)-1]
}

type versioned interface{ effective() kernel.Date }

func (v FieldVersion) effective() kernel.Date  { return v.EffectiveFrom }
func (v MetricVersion) effective() kernel.Date { return v.EffectiveFrom }

// versionAt выбирает последнюю версию с датой действия не позже d.
func versionAt[T versioned](versions []T, d kernel.Date) (T, bool) {
	var out T
	found := false
	for _, v := range versions {
		if d.IsZero() || !v.effective().After(d) {
			out, found = v, true
		}
	}
	return out, found
}

// FactRow — строка финансовых данных. Значение денежных полей — в минорных единицах.
type FactRow struct {
	ID        kernel.ID       `json:"id"`
	BatchID   kernel.ID       `json:"batch_id"`
	FieldKey  string          `json:"field_key"`
	ProductID kernel.ID       `json:"product_id,omitempty"`
	TeamID    kernel.ID       `json:"team_id,omitempty"`
	Period    Period          `json:"period"`
	Item      string          `json:"item,omitempty"`
	Value     decimal.Decimal `json:"value"`
	// DataVersion — версия данных периода (EC-07): повторная загрузка создаёт новую.
	DataVersion int `json:"data_version"`
	// Sheet и Row — координаты исходной строки импорта для объяснения значения (EC-10).
	Sheet string `json:"sheet,omitempty"`
	Row   int    `json:"row,omitempty"`
}

// BatchStatus — состояние загрузки.
type BatchStatus string

// Состояния загрузки.
const (
	BatchPreview  BatchStatus = "preview"
	BatchApplied  BatchStatus = "applied"
	BatchRejected BatchStatus = "rejected"
)

// RowError — ошибка разбора строки импорта (EC-07).
type RowError struct {
	Sheet   string `json:"sheet"`
	Row     int    `json:"row"`
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
}

// ImportBatch — загрузка финансовых данных (EC-01, EC-07). История загрузок хранится.
type ImportBatch struct {
	ID          kernel.ID   `json:"id"`
	TemplateID  kernel.ID   `json:"template_id"`
	Period      Period      `json:"period"`
	DataVersion int         `json:"data_version"`
	Status      BatchStatus `json:"status"`
	FileName    string      `json:"file_name"`
	SHA256      string      `json:"sha256"`
	Rows        int         `json:"rows"`
	Errors      []RowError  `json:"errors,omitempty"`
	Actor       string      `json:"actor"`
	At          time.Time   `json:"at"`
	// Scheduled — загрузка выполнена по расписанию, а не вручную (EC-01).
	Scheduled bool `json:"scheduled"`
}

// ColumnMap — привязка колонки листа к полю и измерениям (EC-07).
type ColumnMap struct {
	Column   string `json:"column"`    // буква или заголовок колонки
	FieldKey string `json:"field_key"` // поле платформы
	// MinorUnits — значения денежного поля уже в минорных единицах (копейках);
	// иначе значение умножается на 100 при загрузке (инвариант 6).
	MinorUnits bool `json:"minor_units"`
}

// SheetMap — привязка листа книги к полям.
type SheetMap struct {
	Sheet     string `json:"sheet"`
	HeaderRow int    `json:"header_row"`
	// Колонки измерений: значения берутся из этих колонок для каждой строки.
	ProductColumn string      `json:"product_column,omitempty"`
	TeamColumn    string      `json:"team_column,omitempty"`
	PeriodColumn  string      `json:"period_column,omitempty"`
	ItemColumn    string      `json:"item_column,omitempty"`
	Columns       []ColumnMap `json:"columns"`
}

// Template — шаблон импорта XLSX (EC-07).
type Template struct {
	ID        kernel.ID  `json:"id"`
	Name      string     `json:"name"`
	Sheets    []SheetMap `json:"sheets"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// AllocationBasis — база распределения затрат хаба (EC-02).
type AllocationBasis string

// Базы распределения.
const (
	BasisManual   AllocationBasis = "manual"
	BasisRevenue  AllocationBasis = "revenue"
	BasisWorklogs AllocationBasis = "worklogs"
)

func (b AllocationBasis) valid() bool {
	return b == BasisManual || b == BasisRevenue || b == BasisWorklogs
}

// AllocationRule — правило аллокации затрат хаба на продукты-потребители (EC-02).
// Версионируется датой действия; применённые версии не изменяются.
type AllocationRule struct {
	ID            kernel.ID                     `json:"id"`
	HubProductID  kernel.ID                     `json:"hub_product_id"`
	Basis         AllocationBasis               `json:"basis"`
	Shares        map[kernel.ID]decimal.Decimal `json:"shares,omitempty"`
	Consumers     []kernel.ID                   `json:"consumers,omitempty"`
	Version       int                           `json:"version"`
	EffectiveFrom kernel.Date                   `json:"effective_from"`
	Actor         string                        `json:"actor"`
	At            time.Time                     `json:"at"`
}

// BundleRule — правило атрибуции выручки бандла между продуктами (EC-04).
type BundleRule struct {
	ID            kernel.ID                     `json:"id"`
	BundleKey     string                        `json:"bundle_key"`
	Shares        map[kernel.ID]decimal.Decimal `json:"shares"`
	Version       int                           `json:"version"`
	EffectiveFrom kernel.Date                   `json:"effective_from"`
	Actor         string                        `json:"actor"`
	At            time.Time                     `json:"at"`
}

// TeamShare — доля команды в продукте за период (EC-12). Источник — worklogs или ручной ввод.
type TeamShare struct {
	TeamID    kernel.ID       `json:"team_id"`
	ProductID kernel.ID       `json:"product_id"`
	Period    Period          `json:"period"`
	Share     decimal.Decimal `json:"share"`
	Source    string          `json:"source"` // worklogs | manual
}

// Team — команда (вторая ось детализации, EC-12).
type Team struct {
	ID   kernel.ID `json:"id"`
	Key  string    `json:"key"`
	Name string    `json:"name"`
}

// PnL — отчёт о прибылях и убытках продукта за период (EC-03).
// Прямой вид — без нагрузки хаба; вид с нагрузкой добавляет аллокацию (EC-02).
type PnL struct {
	ProductID     kernel.ID    `json:"product_id"`
	Period        Period       `json:"period"`
	Revenue       kernel.Money `json:"revenue"`
	BundleRevenue kernel.Money `json:"bundle_revenue"`
	DirectCosts   kernel.Money `json:"direct_costs"`
	HubLoad       kernel.Money `json:"hub_load"`
	DirectProfit  kernel.Money `json:"direct_profit"`
	LoadedProfit  kernel.Money `json:"loaded_profit"`
}

// PortfolioPnL — свод по портфелю (EC-03).
type PortfolioPnL struct {
	Period   Period       `json:"period"`
	Products []PnL        `json:"products"`
	Revenue  kernel.Money `json:"revenue"`
	Costs    kernel.Money `json:"costs"`
	Profit   kernel.Money `json:"profit"`
}

// TeamCost — затраты команды за период с разбивкой по продуктам (EC-12).
type TeamCost struct {
	TeamID    kernel.ID                  `json:"team_id"`
	Period    Period                     `json:"period"`
	Total     kernel.Money               `json:"total"`
	ByProduct map[kernel.ID]kernel.Money `json:"by_product"`
}

// Matrix — матрица «команда × продукт» (EC-12).
type Matrix struct {
	Period   Period                                   `json:"period"`
	Teams    []kernel.ID                              `json:"teams"`
	Products []kernel.ID                              `json:"products"`
	Cells    map[kernel.ID]map[kernel.ID]kernel.Money `json:"cells"`
}

// FeatureEconomics — инвестиции в фичу против привязанной выручки (EC-05).
type FeatureEconomics struct {
	FeatureID  kernel.ID    `json:"feature_id"`
	Investment kernel.Money `json:"investment"`
	Revenue    kernel.Money `json:"revenue"`
	Balance    kernel.Money `json:"balance"`
}

// BranchCost — стоимость поддержки ветки версии (EC-05).
type BranchCost struct {
	ProductID kernel.ID    `json:"product_id"`
	Branch    string       `json:"branch"`
	Cost      kernel.Money `json:"cost"`
}

// CertificationEconomics — экономика сертификации (EC-06).
type CertificationEconomics struct {
	ProductID        kernel.ID    `json:"product_id"`
	Period           Period       `json:"period"`
	TrackCost        kernel.Money `json:"track_cost"`
	CertifiedRevenue kernel.Money `json:"certified_revenue"`
	Balance          kernel.Money `json:"balance"`
}

// Explanation — объяснение значения показателя (EC-10): формула, вклад ссылок и исходные строки.
type Explanation struct {
	Key        string          `json:"key"`
	Kind       formula.RefKind `json:"kind"`
	Expression string          `json:"expression,omitempty"`
	Value      decimal.Decimal `json:"value"`
	Inputs     []Explanation   `json:"inputs,omitempty"`
	Rows       []FactRow       `json:"rows,omitempty"`
}

// Названия доменных событий модуля.
const (
	EventBatchApplied   = "economics.batch.applied"
	EventFieldSaved     = "economics.field.saved"
	EventMetricSaved    = "economics.metric.saved"
	EventRuleSaved      = "economics.allocation.saved"
	EventPeriodClosed   = "economics.period.closed"
	EventPeriodRecalced = "economics.period.recalculated"
)
