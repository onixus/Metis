# PostgreSQL DoD — итерация 26

Дата: 2026-10-25. Проверена изолированная ветка Roadmap/обязательств. PostgreSQL **16.14**, UTF8, `en_US.UTF-8`, loopback127.0.0.1:55446. Новый временный кластер `/private/tmp/metis-roadmap26-pg/data-unicode`; другие БД и данные не изменялись. Роль runtime `metis_app` — NOSUPERUSER/NOCREATEDB/NOCREATEROLE. Данные синтетические.

## Проверки

- **PASS** полный `CGO_ENABLED=1 go test -race -count=1 -tags integration ./...` с `METIS_TEST_DATABASE_URL=postgres://metis@127.0.0.1:55446/metis_test?sslmode=disable`. Лог `/private/tmp/metis-roadmap26-pg/integration-unicode.log`.
- **PASS** отдельный полный `go test -race -count=1 -tags integration ./internal/app` с `METIS_RUNTIME_TEST_DATABASE_URL` на отдельной `metis_runtime_test`. Проверено настоящее подключение runtime под непривилегированной ролью. Лог `/private/tmp/metis-roadmap26-pg/runtime-unicode.log`.
- **PASS** новая регрессия `TestCT03_NFS05_PostgresRiskActionPersistsAndRollsBackWithAudit`: настоящий HTTP-сдвиг → отдельный worker → риск; отказ вставки аудита после создания action/outbox вызывает HTTP500 и откат action/outbox/audit; повтор успешен, другая реплика видит историю и владельца, чужой PM получает403, audit hash-chain цела. Проверка audit привязана к ID риска, повторный запуск не зависит от старых данных.
- **PASS** новая миграция `commitments/00003_risk_actions.sql` и PG-тест неизменяемости истории. Проверены сохранение через новый репозиторий и UPDATE/DELETE-отказы, отказ нулевого scope/чужого продукта.
- **PASS** обратная совместимость API: собран настоящий предыдущий `origin/main` **8e03064e474b052f7743396bcabc4baf2dbf9168** из отдельного `git archive`; старый бинарник запущен как `metis_app` на отдельной БД после новых миграций. 7 HTTP-assertions: создание продукта, фичи, обязательства, релиза; чтение обязательств/релизов/алертов. [Результат](26-acceptance/postgres-compatibility.json). Старый процесс остановлен, временные HMAC-ключ/токен не сохранялись.

## Исправленная среда и границы

Первый прогон с Darwin `locale=C` и UTF8 менял ранжирование русского FTS: `lower('Экспорт')` не давал ожидаемую Unicode-классификацию. Причина устранена корректной локалью нового кластера; код/ожидания теста не ослаблялись, skips не добавлялись. На новом кластере полный SG04/integration PASS.

Локальный пакет PostgreSQL не содержит pgvector. В текущих миграциях нет vector-колонок или зависимости от расширения: используются FTS/tsvector (вопрос24). Поэтому локально выполнены все интеграционные тесты реальной БД, а установка `CREATE EXTENSION vector` подтверждается отдельно GitHub CI на Ubuntu/PostgreSQL16. Это не проверка production, внешнего OIDC, реальных адаптеров или совместной обработки новых риск-событий старым worker.

NF-L01: тест и evidence подготовлены агентом Codex; человеческое ревью pending.
