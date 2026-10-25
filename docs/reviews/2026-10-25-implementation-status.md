# Реализация и открытые задачи Metis на 25.10.2026

База проверки: main `fb65dd14c7f14ceddaf7be02c089604087438489` после PR #60. Сверены GitHub PR, issues, CI последних head, текущий код, ID-тесты и сохранённые acceptance. Это актуализация документации; новые испытания приложения и production не выполнялись. [Аудит 22.09](2026-09-22-product-readiness.md) сохранён как исторический.

## Что вошло в main

| Область | Реализовано | Основание |
| --- | --- | --- |
| Доступ | Обязательный Scope хранилищ, запрет нулевых прав и подмены владельца | [PR #53](https://github.com/onixus/Metis/pull/53), #18 закрыта |
| Discovery | Двусторонняя цепочка сигнал → инсайт → гипотеза → фича → решение; ограниченный обход без раскрытия скрытых узлов | [PR #55](https://github.com/onixus/Metis/pull/55), #25 закрыта |
| UI | APEX Design System, две темы, локальные шрифты, доступность и адаптивность | [PR #57](https://github.com/onixus/Metis/pull/57), #56 закрыта |
| Сохранность экономики | PostgreSQL modeling для API/worker; append-only история; определения, аудит и outbox в транзакции | [PR #54](https://github.com/onixus/Metis/pull/54), #26 закрыта |
| Roadmap и Delivery | Жизненный цикл релиза/EOL, launch calendar, владелец и история риска, ручная эскалация, прогноз с источником/свежестью/интервалом | [PR #58](https://github.com/onixus/Metis/pull/58), [планы26/27](../plans/27.md) |
| Общая экономика | Явное объединение месяца по digest/CAS с архивом двух историй; общие формулы/числовой manual ввод; source snapshots worklogs/CRM/headcount | [PR #59](https://github.com/onixus/Metis/pull/59), [план30](../plans/30.md), [руководство](../economics.md) |
| Портфель и граф | Обзор без finance, создание иерархии, связи без drag, двусторонний редактор контрактов; атомарные рёбра/даты, защита contract_id; win/loss по marketing scope | [PR #60](https://github.com/onixus/Metis/pull/60), [план31](../plans/31.md) |

## Состояние задач после сверки

Сохранены исходные требования и зависимости issues. Отметка критерия опирается на реализацию и указанные проверки, а не на наличие метода. Полностью подтверждённые задачи закрыты; эпики остаются открытыми.

| Issue | Статус | Выполнено и остаток |
| --- | --- | --- |
| [#30](https://github.com/onixus/Metis/issues/30) | Закрыта | Редактор обеих сторон, цикл с путём, max готовности, compatibility в релизе, разграничение приватных данных. PG04/05/08, RM05 и ABAC-тесты; browser31 |
| [#33](https://github.com/onixus/Metis/issues/33) | Закрыта | Seed-воспроизводимый прогноз; отказ без истории; stale/pending не подтверждают срок; источник, UTC-время, риск и интервал. DL04 Go/HTTP и UI-регрессии PR58 |
| [#34](https://github.com/onixus/Metis/issues/34) | Закрыта | Канонические входы и P&L через публичные порты, архив/версии/Explain, closed-period guards и исходный finance Scope. Объединение явное; независимые деньги не складываются |
| [#41](https://github.com/onixus/Metis/issues/41) | Закрыта | Readiness gate, certified-only fixes/неизменяемость ветки, notes/compatibility/EOL, причины дат, sales-safe и браузерный сценарий |
| [#23](https://github.com/onixus/Metis/issues/23) | Частично | Создание/чтение иерархии и scope реализованы; удаление/архивирование и отсутствие висячих ссылок ещё не приняты |
| [#39](https://github.com/onixus/Metis/issues/39) | Частично | Пересчёт дат в обе стороны и после замены зависимости, rollback/reload/concurrency покрыты; согласованный объём и SLO большого графа, полный согласованный срез обязательств требуют приёмки |
| [#32](https://github.com/onixus/Metis/issues/32) | Частично | Владелец, ручная эскалация/подтверждение/история и retry одного source event есть; единый риск разных причин, SLA и надёжный внешний канал не реализованы |
| [#48](https://github.com/onixus/Metis/issues/48) | Частично | Tier/date, фильтры и sales-safe есть; автоматическая связь переноса релиза с маркетинговой датой/историей и readiness запуска остаётся |
| [#42](https://github.com/onixus/Metis/issues/42) | Частично | Предпросмотр diff/сумм, строгая сверка строк и сохранение правил/manual, CAS/closed-period guards. Повтор импорта создаёт новую версию: дедупликация денег не означает идемпотентность самой операции. Корпоративная финансовая сверка ещё не принята |
| [#43](https://github.com/onixus/Metis/issues/43) | Частично | UI полей/формул/дат действия, Explain, версии и cycle/cost guards есть; нечисловой ручной ввод, полный визуальный граф и приёмка всего EC10 остаются |
| [#49](https://github.com/onixus/Metis/issues/49) | Частично | Один движок/source snapshot, fact не меняется, связанные даты и unknown; capacity задаётся днями. Причинная capacity→duration, автоматические FeatureID/Branch mappings и полная экономика треков требуют завершения |
| [#22](https://github.com/onixus/Metis/issues/22) | Частично | Сохранены browser matrices и отдельные PostgreSQL/worker gates; единая автоматическая OIDC login/logout/role/restart приёмка с отчётом CI ещё не выполнена |
| [#31](https://github.com/onixus/Metis/issues/31) | Открыта | Зависимость #25 завершена; публикация brief и актуальный статус ADR/wiki с восстановлением неопределённого исхода ещё требуют реализации |
| [#52](https://github.com/onixus/Metis/issues/52) | Частично | Win/loss доступен независимо от finance по marketing/portfolio scope; полный CRM drill-down, freshness/полнота, attach rate и повтор импорта ещё требуют сквозной приёмки |

## CI и границы доказательств

Проверены завершённые runs для последних head влитых PR:

| PR | Проверенный head | CI |
| --- | --- | --- |
| #58 | `b3e696b6a75849282d37ddb2473c21a50d61d1a6` | [40197320308](https://github.com/onixus/Metis/actions/runs/40197320308) — success |
| #59 | `2090b86b0bb5e35743ff447b4d5b97acdfbf246f` | [40205176411](https://github.com/onixus/Metis/actions/runs/40205176411) — success |
| #60 | `75b6c4a1a93f6759c7528869d30f70f6c65ecbbd` | [40208541963](https://github.com/onixus/Metis/actions/runs/40208541963) — success |

Во всех трёх runs `go`, `web`, `generated`, `secrets` завершились успешно. Проверены шаги Go race/integration, PostgreSQL16/pgvector, отдельная runtime БД и worker/metis_app, govulncheck, licenses, SBOM, build; web lint/typecheck/tests/build, генерация и Gitleaks. Это CI указанных head; отдельный новый прогон merge-коммита main здесь не заявляется.

Браузерные доказательства: [roadmap27](../plans/27-acceptance/README.md), [экономика30](../plans/30-acceptance/README.md), [портфель31](../plans/31-acceptance/README.md). Исторические JSON/PNG не менялись. Browser31 artifacts относятся к первому коммиту PR60; последующие исправления подтверждены новыми regression tests и CI последнего head. Focused forecast и finance role-matrix используют явно обозначенные подмены read-only проекций; общие сценарии используют реальный API. Режимы memory/HMAC и PG описаны в соответствующих acceptance.

Не подтверждены: реальный корпоративный OIDC/адаптеры, другие браузеры, production deployment, нагрузочные SLO, backup/PITR/restore, offline/upgrade и business rollback на старый бинарник без binding. Малые команды консервативно требуют finance/full; настраиваемый порог численности не заявляется. Человеческое ревью/приёмка pending; реализация, документы и агентское ревью — Codex.

## Следующая работа

P0 Scope и PostgreSQL modeling завершены. Далее — остаток #23/#39, #32/#48 и #42/#43/#49, приёмка интеграций #28/#50 и цепочка evidence/baseline #27/#35/#44. До поставки остаются #19/#20/#21/#22. Дашборды #45, приоритизация #46 и маркетинг #52 сохраняют свой объём; реализация win/loss не завершает эпик аналитики. Отечественные tracker/wiki #36/#37 и AI #29/#38/#47/#51 требуют отдельных решений и итераций. Общий [эпик #8](https://github.com/onixus/Metis/issues/8) и эпики направлений открыты.
