# ADR-0001. Стек этапа 1

Статус: принято · Дата: 2026-09-17

## Контекст

ТЗ (раздел 7.1) задаёт стек. Решение владельца от 17.09.2026: бэкенд на Go. Платформа поставляется заказчикам и включается в реестр (раздел 5.6), поэтому лицензии зависимостей ограничены (NF-L02).

## Решение

Go, chi, pgx v5, sqlc, oapi-codegen, slog, OpenTelemetry, gonum/graph, goose, go-oidc. Фронтенд: React, Vite, TanStack Query, React Flow.

## Зависимости и лицензии

| Зависимость | Назначение | Лицензия |
| --- | --- | --- |
| github.com/go-chi/chi/v5 | HTTP-маршрутизация | MIT |
| github.com/jackc/pgx/v5 | Драйвер PostgreSQL | MIT |
| github.com/oapi-codegen/oapi-codegen/v2 | Генерация серверных интерфейсов из OpenAPI | Apache-2.0 |
| github.com/oapi-codegen/runtime | Рантайм сгенерированного кода | Apache-2.0 |
| github.com/getkin/kin-openapi | Валидация запросов по спецификации | MIT |
| github.com/pressly/goose/v3 | Миграции | MIT |
| gonum.org/v1/gonum | Алгоритмы на графах | BSD-3-Clause |
| github.com/coreos/go-oidc/v3 | OIDC | Apache-2.0 |
| github.com/golang-jwt/jwt/v5 | Разбор JWT в тестах и для аварийного доступа | MIT |
| go.opentelemetry.io/otel (+ sdk, exporters) | Трассировки и метрики | Apache-2.0 |
| github.com/prometheus/client_golang | Экспорт метрик | Apache-2.0 |
| github.com/google/uuid | UUID v7 идентификаторы | BSD-3-Clause |
| github.com/shopspring/decimal | Точные расчёты аллокации | MIT |
| pgtestdb / testcontainers | не используются; интеграционные тесты идут против внешней БД по `METIS_TEST_DATABASE_URL` | — |
| pgregory.net/rapid | Property-based тесты | MIT |

## Последствия

Все перечисленные лицензии входят в разрешённый список. Проверка — go-licenses в CI.
