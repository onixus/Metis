package crmfile_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onixus/metis/internal/adapters/crmfile"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

const fixtures = "../../../fixtures/crm"

// TestCRMPort_ContractOnFixtures — контрактный тест порта CRM на синтетических выгрузках.
func TestCRMPort_ContractOnFixtures(t *testing.T) {
	var crm ports.CRM = crmfile.New(fixtures)
	ctx := context.Background()

	accounts, err := crm.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 4 {
		t.Fatalf("аккаунтов: %d", len(accounts))
	}
	if accounts[0].ExternalID != "acc-001" || accounts[0].ARR != kernel.RUB(24_000_000_00) || accounts[0].Segment != "enterprise" {
		t.Fatalf("acc-001: %+v", accounts[0])
	}
	if accounts[1].ARR.Amount != 8_500_000_50 {
		t.Fatalf("копейки: %+v", accounts[1].ARR)
	}
	// Недоверенный ввод: формулы остаются строками.
	if accounts[2].Name != "=SUM(A1:A9)" || accounts[3].Name != "+Плюс Технологии" {
		t.Fatalf("формулы должны читаться как текст: %q %q", accounts[2].Name, accounts[3].Name)
	}

	deals, err := crm.Deals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(deals) != 4 {
		t.Fatalf("сделок: %d", len(deals))
	}
	d := deals[0]
	if d.ExternalID != "deal-001" || d.AccountID != "acc-001" || d.Amount != kernel.RUB(12_000_000_00) || d.ProductKey != "edr" ||
		d.Stage != "negotiation" || d.Regulatory != "ГОСТ Р 56939" || d.Version != "3.0" || !d.BlocksOnFeatures ||
		d.ExpectedDate.String() != "2026-12-15" || len(d.RequestedFeatures) != 1 || d.RequestedFeatures[0] != "Response API v2" {
		t.Fatalf("deal-001: %+v", d)
	}
	if len(deals[1].RequestedFeatures) != 2 || deals[1].RequestedFeatures[1] != "Экспорт в SIEM" {
		t.Fatalf("список фич: %+v", deals[1].RequestedFeatures)
	}
	if deals[2].Notes != "@import os" {
		t.Fatalf("notes как текст: %q", deals[2].Notes)
	}
	if !deals[3].Amount.IsZero() || !deals[3].ExpectedDate.IsZero() || deals[3].RequestedFeatures[0] != "-Коннектор" {
		t.Fatalf("пустые поля: %+v", deals[3])
	}
}

func TestCRMPort_FileSizeLimit(t *testing.T) {
	a := crmfile.New(fixtures, crmfile.WithMaxFileSize(10))
	if _, err := a.Accounts(context.Background()); !errors.Is(err, crmfile.ErrFileTooLarge) {
		t.Fatalf("ожидался ErrFileTooLarge, получено %v", err)
	}
}

func TestCRMPort_MissingFileAndBadData(t *testing.T) {
	dir := t.TempDir()
	a := crmfile.New(dir)
	if _, err := a.Deals(context.Background()); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("нет файла: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deals.csv"), []byte("id,account_id,product_key,amount\nd1,a1,edr,abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Deals(context.Background()); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("плохая сумма: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deals.csv"), []byte("id,name\nd1,x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Deals(context.Background()); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("нет обязательной колонки: %v", err)
	}
}
