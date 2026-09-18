// Package ports — интерфейсы портов ядра к внешним системам (ТЗ 4.1).
// Домен зависит только от этих интерфейсов; адаптеры лежат в internal/adapters.
package ports

import (
	"context"

	"github.com/onixus/metis/internal/kernel"
)

// Account — проекция аккаунта из CRM. Поля принадлежат CRM и доступны только на чтение.
type Account struct {
	ExternalID string       `json:"external_id"`
	Name       string       `json:"name"`
	Segment    string       `json:"segment"`
	ARR        kernel.Money `json:"arr"` // годовая выручка по аккаунту
}

// DealStage — стадия сделки в CRM. Набор стадий задаётся CRM заказчика; ядро хранит строку.
type DealStage string

// Deal — проекция сделки из CRM. Поля принадлежат CRM и доступны только на чтение.
type Deal struct {
	ExternalID   string       `json:"external_id"`
	AccountID    string       `json:"account_id"`
	Name         string       `json:"name"`
	Stage        DealStage    `json:"stage"`
	Amount       kernel.Money `json:"amount"`
	ProductKey   string       `json:"product_key"` // ключ продукта Метиды, к которому относится сделка
	Regulatory   string       `json:"regulatory"`  // регуляторное требование сделки (ГОСТ, уровень доверия и т.п.)
	Version      string       `json:"version"`     // запрошенная версия продукта
	ExpectedDate kernel.Date  `json:"expected_date"`
	// RequestedFeatures — запросы фич из сделки; каждый порождает сигнал (SG-01).
	RequestedFeatures []string `json:"requested_features"`
	Notes             string   `json:"notes"`
	// BlocksOnFeatures — сделка не закроется без запрошенных фич.
	BlocksOnFeatures bool `json:"blocks_on_features"`
	// Outcome — исход сделки: won, lost или пусто для открытых (DA-04).
	Outcome DealOutcome `json:"outcome"`
	// Reason — причина выигрыша или проигрыша по классификатору CRM (DA-04).
	Reason string `json:"reason"`
	// ClosedDate — дата закрытия сделки.
	ClosedDate kernel.Date `json:"closed_date"`
	// Products — ключи всех продуктов сделки; первый — основной (для attach rate, DA-04).
	Products []string `json:"products"`
	// Features — фичи, вошедшие в выигранную сделку (DA-04).
	Features []string `json:"features"`
}

// DealOutcome — исход сделки (DA-04).
type DealOutcome string

// Исходы сделки.
const (
	DealOpen DealOutcome = ""
	DealWon  DealOutcome = "won"
	DealLost DealOutcome = "lost"
)

// CRM — порт чтения CRM (ТЗ 4.1). Платформа в CRM ничего не пишет.
type CRM interface {
	Accounts(ctx context.Context) ([]Account, error)
	Deals(ctx context.Context) ([]Deal, error)
}
