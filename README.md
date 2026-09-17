# Метида (Metis)

Внутренняя платформа портфельного управления продуктами: граф портфеля, сигналы спроса, приоритизация, roadmap, обязательства, SSDLC и сертификация, экономика, решения. ТЗ — [docs/spec.md](docs/spec.md), инструкция агента-разработчика — [AGENTS.md](AGENTS.md).

Состояние: **этап 1 (ядро и delivery)** — граф портфеля, сигналы, приоритизация, roadmap, проекция трекера, аудит, RBAC/ABAC, API, фронтенд. Сценарий приёмки 7.7 автоматизирован в `tests/e2e`.

## Быстрый старт

Требования: Go 1.27+, Node 22+, Docker (для стенда).

```bash
make ci                 # gofmt, vet, golangci-lint, тесты с -race, сборка
go test ./tests/e2e/    # сценарий приёмки этапа 1 в процессе (память + мок Jira)
```

Стенд (PostgreSQL, Keycloak, мок Jira, api, worker):

```bash
docker compose -f deploy/compose/docker-compose.yml up --build
```

API: `http://localhost:8081/api/v1`, спецификация `http://localhost:8081/api/openapi.yaml`, метрики `/metrics`, `/healthz`, `/readyz`.

Фронтенд:

```bash
cd web && npm ci && npm run dev
```

## Конфигурация (окружение)

| Переменная | Назначение |
| --- | --- |
| `METIS_STORAGE` | `postgres` (по умолчанию) или `memory` |
| `METIS_DATABASE_URL` | строка подключения PostgreSQL |
| `METIS_MIGRATE` | применить миграции при старте |
| `METIS_SEED` | загрузить референсные портфели |
| `METIS_AUTH_MODE` | `oidc` (по умолчанию) или `hmac` (только стенд/e2e) |
| `METIS_OIDC_ISSUER`, `METIS_OIDC_CLIENT_ID` | параметры IdP |
| `METIS_HMAC_SECRET`, `METIS_HMAC_ISSUER` | секрет и издатель токенов стенда |
| `METIS_JIRA_BASE_URL`, `METIS_JIRA_TOKEN` | адаптер Jira; пусто — выключен |
| `METIS_WEBHOOK_TOKEN` | секрет входящего webhook трекера (`POST /api/v1/webhooks/jira`) |
| `METIS_CRM_DIR` | каталог CSV-выгрузок CRM |
| `METIS_OTEL_EXPORTER` | `none`, `stdout`, `otlp` |

Секреты передаются только через окружение или Secret Kubernetes (см. `deploy/helm/metis`).

## Структура

См. [AGENTS.md](AGENTS.md#структура-репозитория). Открытые вопросы — [docs/questions.md](docs/questions.md), решения — [docs/adr](docs/adr), планы итераций — [docs/plans](docs/plans).

## Лицензия

Apache-2.0 (см. LICENSE). Лицензии зависимостей проверяются в CI (NF-L02).
