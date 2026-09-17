// Package seed загружает референсные портфели (ТЗ 2.3, 7.7 п.1) синтетическими данными.
package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
)

// Result — идентификаторы, нужные сценарию приёмки.
type Result struct {
	Products  map[string]kernel.ID // по ключу
	Features  map[string]kernel.ID // по имени
	Contracts map[string]kernel.ID // по имени
}

// ensureProduct возвращает существующий продукт по ключу или создаёт новый.
// Второй результат — true, если продукт создан сейчас: только тогда для него заводятся фичи.
func ensureProduct(ctx context.Context, svc *pg.Service, sc authz.Scope, in pg.ProductInput) (kernel.ID, bool, error) {
	if id, err := svc.ProductIDByKey(ctx, in.Key); err == nil {
		return id, false, nil
	}
	p, err := svc.CreateProduct(ctx, sc, in)
	if err != nil {
		return kernel.NilID, false, fmt.Errorf("seed product %s: %w", in.Key, err)
	}
	return p.ID, true, nil
}

// Security загружает портфель ИБ: Deception, VM, EDR, SOAR (хаб) и три контракта.
// Идемпотентно по каждому продукту: существующие продукты переиспользуются, фичи и контракты
// заводятся только для созданных сейчас продуктов.
func Security(ctx context.Context, svc *pg.Service, sc authz.Scope) (Result, error) {
	res := Result{Products: map[string]kernel.ID{}, Features: map[string]kernel.ID{}, Contracts: map[string]kernel.ID{}}
	created := map[string]bool{}
	products := []pg.ProductInput{
		{Key: "deception", Name: "Deception", Type: pg.ProductTypeSecurity, Owner: "pm-deception", Lifecycle: pg.LifecycleActive},
		{Key: "vm", Name: "VM", Type: pg.ProductTypeSecurity, Owner: "pm-vm", Lifecycle: pg.LifecycleActive, SSDLCCertified: true},
		{Key: "edr", Name: "EDR", Type: pg.ProductTypeSecurity, Owner: "pm-edr", Lifecycle: pg.LifecycleActive, SSDLCCertified: true},
		{Key: "soar", Name: "SOAR", Type: pg.ProductTypeSecurity, Owner: "pm-soar", Lifecycle: pg.LifecycleActive, HubManual: true},
	}
	for _, in := range products {
		id, isNew, err := ensureProduct(ctx, svc, sc, in)
		if err != nil {
			return res, err
		}
		res.Products[in.Key], created[in.Key] = id, isNew
	}
	d := func(y int, m time.Month, day int) kernel.Date { return kernel.DateOf(y, m, day) }
	features := []struct {
		product, name string
		date          kernel.Date
		status        pg.FeatureStatus
	}{
		{"edr", "Response API v2", d(2026, 11, 15), pg.FeaturePlanned},
		{"edr", "Изоляция хоста", d(2026, 10, 1), pg.FeatureInProgress},
		{"soar", "Коннектор EDR v2", d(2026, 12, 1), pg.FeaturePlanned},
		{"soar", "Коннектор VM", d(2026, 11, 1), pg.FeaturePlanned},
		{"soar", "Коннектор Deception", d(2027, 2, 1), pg.FeatureIdea},
		{"soar", "Плейбуки реагирования v3", d(2027, 1, 20), pg.FeaturePlanned},
		{"vm", "Экспорт уязвимостей API", d(2026, 10, 20), pg.FeatureInProgress},
		{"deception", "События ловушек в SIEM", d(2027, 1, 15), pg.FeaturePlanned},
	}
	for _, f := range features {
		if !created[f.product] {
			continue
		}
		ft, err := svc.CreateFeature(ctx, sc, res.Products[f.product], pg.FeatureInput{Name: f.name, Status: f.status, PlannedDate: f.date})
		if err != nil {
			return res, fmt.Errorf("seed feature %s: %w", f.name, err)
		}
		res.Features[f.name] = ft.ID
	}
	contracts := []struct {
		name, provider, consumer, pf, cf string
		crit                             pg.Criticality
	}{
		// Хаб — поставщик: зависимые продукты потребляют коннекторы SOAR (ТЗ 2.3, 2.4).
		{"EDR ↔ SOAR", "soar", "edr", "Коннектор EDR v2", "Response API v2", pg.CritBlocks},
		{"VM ↔ SOAR", "soar", "vm", "Коннектор VM", "Экспорт уязвимостей API", pg.CritAccelerates},
		{"Deception ↔ SOAR", "soar", "deception", "Коннектор Deception", "События ловушек в SIEM", pg.CritDesirable},
	}
	for _, c := range contracts {
		if !created[c.provider] || !created[c.consumer] {
			continue
		}
		ic, err := svc.SaveContract(ctx, sc, kernel.NilID, pg.ContractInput{
			Name: c.name, ProviderProductID: res.Products[c.provider], ConsumerProductID: res.Products[c.consumer],
			ProviderFeatureIDs: []kernel.ID{res.Features[c.pf]}, ConsumerFeatureIDs: []kernel.ID{res.Features[c.cf]},
			InterfaceVersion: "2.0", Owner: "pm-soar", Status: pg.ContractActive, Criticality: c.crit,
			Compatibility: []pg.VersionPair{{ProviderVersion: "5.x", ConsumerVersion: "3.x", Compatible: true}},
		})
		if err != nil {
			return res, fmt.Errorf("seed contract %s: %w", c.name, err)
		}
		res.Contracts[c.name] = ic.ID
	}
	return res, nil
}

// Infrastructure загружает инфраструктурный портфель: Desktop, Server, LDAP, Backup, Виртуализация → Платформа управления (хаб).
func Infrastructure(ctx context.Context, svc *pg.Service, sc authz.Scope) (Result, error) {
	res := Result{Products: map[string]kernel.ID{}, Features: map[string]kernel.ID{}, Contracts: map[string]kernel.ID{}}
	created := map[string]bool{}
	products := []pg.ProductInput{
		{Key: "mgmt", Name: "Платформа управления ПО и конфигурациями", Type: pg.ProductTypePlatform, Owner: "pm-mgmt", HubManual: true},
		{Key: "desktop", Name: "Desktop", Type: pg.ProductTypeInfrastructure, Owner: "pm-desktop"},
		{Key: "server", Name: "Server", Type: pg.ProductTypeInfrastructure, Owner: "pm-server", SSDLCCertified: true},
		{Key: "ldap", Name: "LDAP", Type: pg.ProductTypeInfrastructure, Owner: "pm-ldap"},
		{Key: "backup", Name: "Backup", Type: pg.ProductTypeInfrastructure, Owner: "pm-backup"},
		{Key: "virt", Name: "Виртуализация", Type: pg.ProductTypeInfrastructure, Owner: "pm-virt"},
	}
	for _, in := range products {
		in.Lifecycle = pg.LifecycleActive
		id, isNew, err := ensureProduct(ctx, svc, sc, in)
		if err != nil {
			return res, err
		}
		res.Products[in.Key], created[in.Key] = id, isNew
	}
	if !created["mgmt"] {
		return res, nil
	}
	agent, err := svc.CreateFeature(ctx, sc, res.Products["mgmt"], pg.FeatureInput{Name: "Агент управления v4", Status: pg.FeaturePlanned, PlannedDate: kernel.DateOf(2026, 12, 15)})
	if err != nil {
		return res, err
	}
	res.Features["Агент управления v4"] = agent.ID
	for _, key := range []string{"desktop", "server", "ldap", "backup", "virt"} {
		if !created[key] {
			continue
		}
		f, err := svc.CreateFeature(ctx, sc, res.Products[key], pg.FeatureInput{Name: "Поддержка агента v4", Status: pg.FeaturePlanned, PlannedDate: kernel.DateOf(2027, 1, 31)})
		if err != nil {
			return res, err
		}
		res.Features[key+"/Поддержка агента v4"] = f.ID
		if _, err := svc.CreateLink(ctx, sc, pg.LinkInput{Type: pg.LinkBundled, FromFeatureID: f.ID, ToFeatureID: agent.ID, Criticality: pg.CritBlocks}); err != nil {
			return res, fmt.Errorf("seed link %s: %w", key, err)
		}
		if _, err := svc.CreateLink(ctx, sc, pg.LinkInput{Type: pg.LinkSharedComponent, FromProductID: res.Products[key], ToProductID: res.Products["mgmt"], Criticality: pg.CritAccelerates}); err != nil {
			return res, fmt.Errorf("seed shared link %s: %w", key, err)
		}
	}
	return res, nil
}
