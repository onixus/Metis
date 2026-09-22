// Package marketing — маркетинговая аналитика (ТЗ 3.10, DA-04): win/loss по причинам
// и сегментам, фичи в выигранных сделках, attach rate.
//
// Источник данных — порт CRM: сделки и аккаунты принадлежат CRM и доступны только
// на чтение (инвариант 4). Платформа в CRM ничего не пишет.
package marketing

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// Counter — количество сделок и сумма по срезу.
type Counter struct {
	Key    string       `json:"key"`
	Won    int          `json:"won"`
	Lost   int          `json:"lost"`
	Amount kernel.Money `json:"amount"`
}

// FeatureCount — фича и число выигранных сделок, где она заявлена (DA-04).
type FeatureCount struct {
	Feature string       `json:"feature"`
	Deals   int          `json:"deals"`
	Amount  kernel.Money `json:"amount"`
}

// AttachRate — доля выигранных сделок, в которые вошёл продукт (DA-04).
type AttachRate struct {
	ProductKey string `json:"product_key"`
	Deals      int    `json:"deals"`
	// Rate — доля в процентах от всех выигранных сделок, с точностью до сотых.
	Rate string `json:"rate"`
}

// Report — маркетинговый отчёт (DA-04).
type Report struct {
	ProductKey string         `json:"product_key,omitempty"`
	Won        int            `json:"won"`
	Lost       int            `json:"lost"`
	WonAmount  kernel.Money   `json:"won_amount"`
	LostAmount kernel.Money   `json:"lost_amount"`
	ByReason   []Counter      `json:"by_reason"`
	BySegment  []Counter      `json:"by_segment"`
	Features   []FeatureCount `json:"features"`
	Attach     []AttachRate   `json:"attach_rate"`
}

// Filter — отбор сделок для отчёта.
type Filter struct {
	// ProductKey — ключ продукта; пусто — весь портфель.
	ProductKey string
	From, To   kernel.Date
}

// Service — публичный интерфейс модуля маркетинговой аналитики.
type Service struct {
	crm      ports.CRM
	products ProductResolver
}

// NewService создаёт сервис поверх порта CRM.
func NewService(crm ports.CRM) *Service { return &Service{crm: crm} }

// ProductResolver — порт: ключ продукта → идентификатор, для проверки доступа к продукту.
type ProductResolver interface {
	ProductIDByKey(ctx context.Context, key string) (kernel.ID, error)
}

// WithProducts подключает справочник продуктов: отчёт по конкретному продукту требует
// права читать маркетинговые данные этого продукта.
func (s *Service) WithProducts(r ProductResolver) *Service { s.products = r; return s }

// WinLoss собирает отчёт win/loss (DA-04).
func (s *Service) WinLoss(ctx context.Context, sc authz.Scope, f Filter) (Report, error) {
	product := kernel.NilID
	if f.ProductKey != "" && s.products != nil {
		id, err := s.products.ProductIDByKey(ctx, f.ProductKey)
		if err != nil {
			return Report{}, err
		}
		product = id
	}
	if err := sc.Require(authz.ActionReadMarketing, product); err != nil {
		return Report{}, err
	}
	if product == kernel.NilID && !sc.SeesAllProducts() {
		return Report{}, fmt.Errorf("%w: портфельный отчёт доступен при доступе ко всем продуктам", kernel.ErrForbidden)
	}
	if s.crm == nil {
		return Report{}, fmt.Errorf("%w: адаптер CRM не подключён", kernel.ErrUnavailable)
	}
	deals, err := s.crm.Deals(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("сделки: %w", err)
	}
	accounts, err := s.crm.Accounts(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("аккаунты: %w", err)
	}
	segment := make(map[string]string, len(accounts))
	for _, a := range accounts {
		segment[a.ExternalID] = a.Segment
	}

	rep := Report{ProductKey: f.ProductKey}
	byReason := map[string]*Counter{}
	bySegment := map[string]*Counter{}
	features := map[string]*FeatureCount{}
	attach := map[string]int{}
	for _, d := range deals {
		if !matches(d, f) {
			continue
		}
		switch d.Outcome {
		case ports.DealWon:
			rep.Won++
			rep.WonAmount = add(rep.WonAmount, d.Amount)
			for _, p := range d.Products {
				attach[p]++
			}
			for _, feat := range d.Features {
				fc, ok := features[feat]
				if !ok {
					fc = &FeatureCount{Feature: feat}
					features[feat] = fc
				}
				fc.Deals++
				fc.Amount = add(fc.Amount, d.Amount)
			}
		case ports.DealLost:
			rep.Lost++
			rep.LostAmount = add(rep.LostAmount, d.Amount)
		default:
			continue // открытые сделки в win/loss не попадают
		}
		reason := d.Reason
		if reason == "" {
			reason = "без причины"
		}
		count(byReason, reason, d)
		seg := segment[d.AccountID]
		if seg == "" {
			seg = "без сегмента"
		}
		count(bySegment, seg, d)
	}
	rep.ByReason = sorted(byReason)
	rep.BySegment = sorted(bySegment)
	rep.Features = sortedFeatures(features)
	rep.Attach = attachRates(attach, rep.Won)
	return rep, nil
}

func matches(d ports.Deal, f Filter) bool {
	if f.ProductKey != "" {
		found := d.ProductKey == f.ProductKey
		for _, p := range d.Products {
			if p == f.ProductKey {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if !f.From.IsZero() && (d.ClosedDate.IsZero() || d.ClosedDate.Before(f.From)) {
		return false
	}
	if !f.To.IsZero() && (d.ClosedDate.IsZero() || d.ClosedDate.After(f.To)) {
		return false
	}
	return true
}

func count(m map[string]*Counter, key string, d ports.Deal) {
	c, ok := m[key]
	if !ok {
		c = &Counter{Key: key}
		m[key] = c
	}
	if d.Outcome == ports.DealWon {
		c.Won++
	} else {
		c.Lost++
	}
	c.Amount = add(c.Amount, d.Amount)
}

func add(a, b kernel.Money) kernel.Money {
	sum, err := a.Add(b)
	if err != nil {
		// Валюты в выгрузке CRM не совпадают: сумма по срезу не считается, счётчики остаются.
		return a
	}
	return sum
}

func sorted(m map[string]*Counter) []Counter {
	out := make([]Counter, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lost != out[j].Lost {
			return out[i].Lost > out[j].Lost
		}
		if out[i].Won != out[j].Won {
			return out[i].Won > out[j].Won
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func sortedFeatures(m map[string]*FeatureCount) []FeatureCount {
	out := make([]FeatureCount, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Deals != out[j].Deals {
			return out[i].Deals > out[j].Deals
		}
		return out[i].Feature < out[j].Feature
	})
	return out
}

func attachRates(counts map[string]int, won int) []AttachRate {
	out := make([]AttachRate, 0, len(counts))
	for key, n := range counts {
		rate := "0.00"
		if won > 0 {
			rate = decimal.NewFromInt(int64(n)).Mul(decimal.NewFromInt(100)).
				DivRound(decimal.NewFromInt(int64(won)), 2).StringFixed(2)
		}
		out = append(out, AttachRate{ProductKey: key, Deals: n, Rate: rate})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Deals != out[j].Deals {
			return out[i].Deals > out[j].Deals
		}
		return out[i].ProductKey < out[j].ProductKey
	})
	return out
}
