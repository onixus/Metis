# Pull request: этап 1 — ядро и delivery

## Что сделано

Итерации 1–10 плана этапа 1 (`docs/plans/01.md` … `10.md`):

- Каркас: Go-модуль, CI (gofmt, vet, golangci-lint с errcheck/staticcheck/gosec/depguard/forbidigo, тесты `-race`, govulncheck, go-licenses, gitleaks, SBOM CycloneDX, проверка лицензий npm), compose-стенд, Helm-чарт, OpenTelemetry и Prometheus.
- `kernel`, `identityaccess` (OIDC, HMAC для стенда, `authz.Scope` с запретом по умолчанию, RBAC/ABAC), `audit` (append-only, хеш-цепочка, CEF, проверка целостности, триггер и REVOKE в PostgreSQL).
- `portfoliograph`: продукты, иерархия, четыре типа связей, контракты, циклы с путём, роль хаба, rollup производного спроса, распространение сдвигов, стратегический срез.
- `signals` + порт CRM + файловый адаптер; `prioritization` (RICE, WSJF, собственная формула на безопасном вычислителе, денежные метрики, производный спрос); `roadmap` (представления, аудиторные срезы, история дат, обработчик сдвигов); `delivery` + порт DeliveryTracker + адаптер Jira + мок WireMock.
- Outbox и воркер на PostgreSQL (`SKIP LOCKED`, повторы, DLQ), миграции goose, sqlc, PG-хранилища аудита и графа.
- `api/openapi.yaml` (spec-first), `internal/httpapi`, `internal/app`, `cmd/api`, `cmd/worker`, seed двух референсных портфелей, `tests/e2e` — сценарий приёмки 7.7.
- `web`: граф на React Flow, бэклог, roadmap, дашборды продукта, хаба, delivery, админ-проверка аудита.

## Закрытые ID

PG-01…PG-10, SG-01…SG-03, SG-05, PR-01…PR-03, RM-01…RM-03, DL-01…DL-03, AD-01, AD-02, AD-04, AD-05, DA-03 (дашборды продукта, хаба, delivery), NF-S01, NF-S05, NF-P03, NF-P04, NF-R05, NF-R06, NF-S17, NF-O01, NF-O04, NF-M04, NF-M05.

## Как проверить

```bash
make ci
go test -race ./tests/e2e/
cd web && npm ci && npm run lint && npm run typecheck && npm run build
```

С PostgreSQL: `METIS_TEST_DATABASE_URL=postgres://… go test -tags integration ./...`.

## Что не сделано

- PostgreSQL-хранилища для signals, prioritization, roadmap, delivery (вопрос №12): модули на MemStore.
- Интеграционные тесты с PostgreSQL написаны, но в среде разработки не выполнялись (Docker недоступен, вопрос №05); выполняются в CI.
- Compose-стенд и Keycloak не поднимались локально по той же причине.
- Вторые адаптеры под отечественный трекер и wiki (NF-L03) — этап 2 по ТЗ.

## Авторство (NF-L01)

Код написан агентом Claude Fable 5.1 по ТЗ владельца. Проверка и приёмка человеком: _заполняется рецензентом_.
