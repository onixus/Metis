# Итерация 29 — восстановить финансовый Scope EC-05 после объединения

Требования прочитаны: EC-05 (инвестиции/выручка фич и стоимость веток), NF-S01 (доменная авторизация, deny-by-default), NF-S02 (финансовые права/аудит). Исправляется подтверждённая регрессия review итерации28.

План до кода:

1. Перед внутренним roleless ModelCalculationScope сохранить исходный scope в operation context существующим WithCanonicalReadScope, как у других canonical-read путей. Не выдавать дополнительные роли пользователю и не менять публичный контракт.
2. Проверить остальные прямые чтения Facts в pnl.go на аналогичный путь; не расширять область исправления на независимые правила аллокации.
3. Добавить EC05/NFS01/NFS02 регрессии до/после binding: точные суммы с canonical manual facts, finance/full успех, aggregate-only/zero/чужой product отказ после binding. Старый необъединённый aggregate доступ сохраняется.
4. Выполнить scoped race tests и lint. Новых зависимостей, API и миграций нет. Используется существующее архитектурное решение canonical read scope ADR0029; нового ADR не требуется.

Автор: Codex; человеческое ревью pending.

Реализация: сохранён исходный Scope на входе FeatureEconomics/BranchCosts; внутренний вычислительный Scope остаётся без ролей. Каноническая книга авторизует исходного финансового пользователя, в том числе отклоняет aggregate-only после binding. Успех проверен с default Config (нет специальной атрибуции), точные суммы1001/2502/1501 и703 — с явно настроенными на migrated custom input keys Config. Автоматическое отображение FeatureID/Branch metadata на default modeling field keys не добавлялось.

Проверка: полный modeling package `CGO_ENABLED=1 go test -race -count=1 ./internal/economics/modeling` — PASS8.207s (лог `/private/tmp/metis27-scope-tests.log`). Scoped `golangci-lint run ./internal/economics/modeling/...` — PASS,0 issues (лог `/private/tmp/metis27-scope-lint.log`); gofmt и scoped diff-check — PASS. Основной агент завершает общий gate после стабилизации всех трёх исправлений.
