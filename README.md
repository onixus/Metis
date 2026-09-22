# Метида (Metis)

Платформа управления портфелем B2B-продуктов информационной безопасности: спрос клиентов → фичи и приоритеты → roadmap и обязательства → релизы и SSDLC. ТЗ — [docs/spec.md](docs/spec.md), инструкция агента-разработчика — [AGENTS.md](AGENTS.md).

Состояние: **пилот этапов 1–3, итерация 13**. Граф портфеля, спрос, приоритизация, roadmap, discovery, обязательства, compliance и решения дополнены экономикой: импорт CSV/XLSX, версии и закрытие периодов, распределение затрат и выручки, P&L продукта/портфеля, команда × продукт, сценарии и объяснение формул до источника. Финансовый доступ требует отдельного уровня и роли, чтения и экспорт аудируются.

Подключены адаптеры Jira/Confluence Data Center, коробочного Bitrix24 и настраиваемого OData 1С (ЗУП и учётная база). CRM и 1С используются только для чтения; записи в Jira/Confluence выполняет outbox. Контрактные тесты не заменяют настройку и приёмку на реальных системах заказчика. Полная промышленная готовность и весь этап 3 не заявляются.

[Аудит и дорожная карта](docs/reviews/2026-09-22-product-readiness.md) · [Запуск пилота](docs/pilot-runbook.md) · [Экономика](docs/economics.md) · [Подключение адаптеров](docs/integrations.md) · [1С и файлы](docs/integrations-1c.md) · [План итерации](docs/plans/13.md).

## Быстрый старт

Требования: Go 1.27.1+, Node 24+, npm 11+, Docker Compose (для стенда).

```bash
make ci                 # gofmt, vet, golangci-lint, тесты с -race, сборка
go test ./tests/e2e/    # приёмка пилота и экономики в памяти с моками адаптеров
make web                # lint, typecheck, тесты UI-логики, production build
```

Стенд (PostgreSQL, миграции, моки Jira/Confluence, API со встроенным web, отдельный worker):

```bash
docker compose -f deploy/compose/docker-compose.yml up --build
```

Приложение: `http://localhost:8082`, API: `/api/v1`, спецификация: `/api/openapi.yaml`, метрики `/metrics`, `/healthz`, `/readyz`. Порт меняется через `METIS_API_PORT`. Вход со стендовым токеном и профиль OIDC описаны в [руководстве](docs/pilot-runbook.md). Миграции выполняются отдельным процессом; runtime подключается ограниченной ролью `metis_app`.

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
| `METIS_CONFLUENCE_BASE_URL`, `METIS_CONFLUENCE_TOKEN` | адаптер Confluence (порт KnowledgeBase, страницы ADR); пусто — выключен, ядро работает без него (NF-L03) |
| `METIS_CONFLUENCE_SPACE` | пространство Confluence для страниц ADR (по умолчанию `METIS`) |
| `METIS_WEBHOOK_TOKEN` | секрет входящего webhook трекера (`POST /api/v1/webhooks/jira`) |
| `METIS_CRM_DIR` | каталог CSV-выгрузок CRM |
| `METIS_DELIVERY_PROVIDER`, `METIS_KNOWLEDGE_PROVIDER`, `METIS_CRM_PROVIDER` | явный выбор адаптеров; см. руководство подключения |
| `METIS_BITRIX_BASE_URL`, `METIS_BITRIX_TOKEN`, `METIS_BITRIX_FIELDS_FILE` | коробочный Bitrix24 REST, OAuth токен из secret, соответствие полей |
| `METIS_FINANCE_SOURCES_FILE`, `METIS_FINANCE_SYNC_INTERVAL` | профили ЗУП/учётной базы/файлов и расписание загрузок |
| `METIS_WORKLOG_TEAMS_FILE` | соответствие непрозрачного ключа автора Jira команде |
| `METIS_OTEL_EXPORTER` | `none`, `stdout`, `otlp` |
| `METIS_WEB_DIR` | каталог собранной SPA; в Docker-образе уже установлен |
| `METIS_DELIVERY_SYNC_INTERVAL` | период сверки трекера, по умолчанию `1h` |
| `METIS_RENEWAL_INTERVAL` | период проверки продлений сертификатов, по умолчанию `24h` |

Секреты передаются только через окружение или Secret Kubernetes (см. `deploy/helm/metis`).

Пилот сериализует операции PostgreSQL для согласованности графа и outbox между процессами ([ADR-0006](docs/adr/0006-pilot-transaction-boundary.md)). Промышленные SLO и масштабирование ещё не подтверждены. Работа без Atlassian поддержана ядром. Выбранные системы подключаются через инфраструктурные фабрики и нейтральные порты.

## Структура

См. [AGENTS.md](AGENTS.md#структура-репозитория). Открытые вопросы — [docs/questions.md](docs/questions.md), решения — [docs/adr](docs/adr), планы итераций — [docs/plans](docs/plans).

## Лицензия

Apache-2.0 (см. LICENSE). Лицензии зависимостей проверяются в CI (NF-L02).
