# Метида (Metis)

Платформа управления портфелем B2B-продуктов информационной безопасности: спрос клиентов → фичи и приоритеты → roadmap и обязательства → релизы и SSDLC. ТЗ — [docs/spec.md](docs/spec.md), инструкция агента-разработчика — [AGENTS.md](AGENTS.md).

Состояние: **пилот этапов 1–3; актуализировано 25.10.2026 по main `fb65dd1` (PR #60)**. Граф портфеля, спрос, приоритизация, roadmap, discovery, обязательства, compliance и решения дополнены экономикой: импорт CSV/XLSX, версии и закрытие периодов, распределение затрат и выручки, P&L продукта/портфеля, команда × продукт, сценарии и объяснение формул до источника. Финансовый доступ требует отдельного уровня и роли, чтения и экспорт аудируются.

Подключены адаптеры Jira/Confluence Data Center, коробочного Bitrix24 и настраиваемого OData 1С (ЗУП и учётная база). CRM и 1С используются только для чтения; записи в Jira/Confluence выполняет outbox. Контрактные тесты не заменяют настройку и приёмку на реальных системах заказчика. Полная промышленная готовность и весь этап 3 не заявляются.

[Реализация и открытые задачи](docs/reviews/2026-10-25-implementation-status.md) · [Запуск пилота](docs/pilot-runbook.md) · [Экономика](docs/economics.md) · [Подключение адаптеров](docs/integrations.md) · [1С и файлы](docs/integrations-1c.md) · [Последняя функциональная итерация](docs/plans/31.md).

Портфель доступен без финансовых прав: продукты, хабы, зависимости и иерархия Capability → Feature → Requirement. Граф позволяет создавать связи и редактировать двусторонние контракты; изменение зависимостей атомарно пересчитывает ограничения дат. Решения трассируются до исходных сигналов в обе стороны. Интерфейс использует APEX Design System с тёмной и светлой темами.

При `METIS_STORAGE=postgres` конструктор финансовых показателей сохраняет определения, импорты и историю в схеме `modeling`, общей для API и worker. В «Экономике» явное объединение выбранного месяца переключает книгу и конструктор на единый источник с архивом обеих историй; автоматического объединения всех периодов нет. Формулы поддерживают `worklogs()`, `crm_revenue()`, `headcount()` через scoped-порты и сохраняемые снимки. Ограничения закрытых периодов и бизнес-отката — в [руководстве экономики](docs/economics.md).

Roadmap включает жизненный цикл релизов, EOL, launch tier/date и календарь. Риски обязательств имеют владельца, подтверждение, ручную эскалацию и историю; доставка внешних уведомлений не реализована. Delivery показывает объяснимый прогноз P50/P85/P95 со свежестью данных. Режим `memory` предназначен для тестов и демонстрации.

## Быстрый старт

Требования: Go 1.27.3+, Node 24+, npm 11+, Docker Compose (для стенда).

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
| `METIS_SEED` | загрузить референсные портфели acceptance-сценариев |
| `METIS_SEED_APEX` | загрузить канонический портфель APEX; не меняет acceptance fixtures |
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
| `METIS_FORMULA_BOOK_PRODUCT_ID` | книга прямых formula-источников, если месяц ещё не связан объединением |
| `METIS_WORKLOG_TEAMS_FILE` | соответствие непрозрачного ключа автора Jira команде |
| `METIS_SECURITY_DIR` | каталог манифестов пайплайна безопасности для автосбора доказательств (CM-09) |
| `METIS_FINANCE_DIR`, `METIS_FINANCE_TEMPLATE`, `METIS_FINANCE_INTERVAL` | загрузка книг XLSX по расписанию: каталог, название шаблона импорта, интервал (по умолчанию `1h`) |
| `METIS_LICENSE_KEY`, `METIS_LICENSE_PUBKEY` | лицензионный ключ поставки и публичный ключ поставщика в base64 (AD-06) |
| `METIS_OTEL_EXPORTER` | `none`, `stdout`, `otlp` |
| `METIS_WEB_DIR` | каталог собранной SPA; в Docker-образе уже установлен |
| `METIS_DELIVERY_SYNC_INTERVAL` | период сверки трекера, по умолчанию `1h` |
| `METIS_RENEWAL_INTERVAL` | период проверки продлений сертификатов, по умолчанию `24h` |

Секреты передаются только через окружение или Secret Kubernetes (см. `deploy/helm/metis`).

Пилот сериализует операции PostgreSQL для согласованности графа и outbox между процессами ([ADR-0006](docs/adr/0006-pilot-transaction-boundary.md)). Промышленные SLO и масштабирование ещё не подтверждены. Работа без Atlassian поддержана ядром. Выбранные системы подключаются через инфраструктурные фабрики и нейтральные порты.
## APEX product templates

APEX-шаблоны загружаются отдельным флагом `METIS_SEED_APEX=true`. Он намеренно
отделён от `METIS_SEED=true`, который сохраняет стабильный набор данных
acceptance-сценариев. Так добавление продуктовых шаблонов не меняет семантику
старых e2e fixtures и не засоряет их outbox.

APEX seed создаёт Gateway, Shapoclyack, Lariska, Ferrum, BSDM-Proxy, Oko-Ra,
Pulse и Asmodeus. Для каждого продукта создаются рассчитанный delivery baseline,
четыре ключевых workstream-фичи, effort/confidence и плановые даты.

Методика и текущие цифры: [docs/apex-product-templates.md](docs/apex-product-templates.md).

## Структура

См. [AGENTS.md](AGENTS.md#структура-репозитория). Открытые вопросы — [docs/questions.md](docs/questions.md), решения — [docs/adr](docs/adr), планы итераций — [docs/plans](docs/plans).

## Лицензия

Apache-2.0 (см. LICENSE). Лицензии зависимостей проверяются в CI (NF-L02).
