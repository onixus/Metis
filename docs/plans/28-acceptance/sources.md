# Прямые источники формул — финальная браузерная приёмка

2026-10-25: **PASS**, реальный локальный API с PostgreSQL16.14, HMAC finance/full и синтетическими данными. API-ответы не подменялись. Стенд основной сессии обслуживал уже объединённый открытый период2026-09.

Проверены4 комбинации «источник × тема» и desktop1440/mobile390:

- headcount: реальная численность общей книги **8**, сценарная подмена **11**;
- crm_revenue: настоящий CSV-адаптер CRM, контрольная выручка **123456** минорных единиц RUB, сценарий **123459**;
- вставка source-функции в редакторе → сохранение → расчёт и Explain;
- source в сценарном payload взаимоисключает field_key; значения передаются строками;
- baseline/scenario используют один и тот же snapshot ID; исходный captured_value и время сохранены, overridden=true только у сценария;
- worklogs при отключённом Delivery/Jira даёт **503**, не ноль;
- нет JS page errors и горизонтального переполнения документа; таблицы имеют собственную горизонтальную прокрутку. Длинные имена импортированных показателей переносятся.

[Headcount JSON](sources-headcount.json), [CRM JSON](sources-crm_revenue.json), [provenance/хеши](sources-provenance.json), [CRM desktop dark](sources-crm_revenue-dark-1440.png), [headcount mobile light](sources-headcount-light-390.png).

Воспроизведение: собрать web и запустить настоящий API PostgreSQL на loopback с открытым объединённым месяцем и доступными синтетическими источниками. Задать `METIS_ACCEPTANCE_URL`, приватный `METIS_ACCEPTANCE_TEMP` с tokens.json, `METIS_SOURCE_PRODUCT_ID`, `METIS_SOURCE_PERIOD`, `METIS_SOURCE_KIND`, `METIS_SOURCE_EXPECTED_VALUE`; при отключённом источнике — `METIS_EXPECT_UNAVAILABLE_SOURCE`. При нестандартной установке задать `METIS_PLAYWRIGHT_MODULE`/`METIS_CHROMIUM_PATH`. Выполнить `node web/scripts/acceptance-economics-sources.cjs` отдельно для headcount и crm_revenue. Результаты разделены по kind и не перезаписывают друг друга. Токены не выводятся и не копируются в репозиторий.

Это не приёмка настоящего Jira/CRM заказчика, внешнего OIDC, иных браузеров или production. Worklogs в браузере проверен как явно недоступный; полная агрегация worklogs и security/isolation/restart — отдельные app/domain/PG регрессии. Автор — Codex; человеческое ревью pending. Основной стенд не останавливался этим агентом.
