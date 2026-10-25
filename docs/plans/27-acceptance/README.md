# Итерация 27 — регрессии P2 PR #58

2026-10-25: **PASS**. Два сфокусированных браузерных сценария исправлений плюс прежние 12 групп приёмки в обеих темах. Изолированный memory/HMAC API с синтетическими данными; production и внешние интеграции не затрагивались.

## Исправления

- RM-04/05: открытый редактор сохраняет несохранённое имя после внешнего выпуска релиза и обновления списка через сохранение notes. Select показывает `released`; текстовый save опускает status, возвращает 200 и сохраняет released. Все запросы этого сценария идут в настоящий API, без подмены.
- DL-04: управляемые часы запускают настоящий 15-секундный polling React Query. Устаревание текущего снимка убирает обе положительные оценки срока без лишнего расчёта. Новый `last_success_at` автоматически запрашивает прогноз; пока ответ не получен, старые даты не подтверждают срок. При повторном открытии Delivery кешированный свежий прогноз также не подтверждает сроки до завершения запроса текущего overview. После подтверждения совпадающего снимка сравнение возобновляется.

**Граница сфокусированного forecast-теста:** Playwright подменяет только два read-only GET: `/products/{id}/delivery` и `/features/{id}/forecast`. Это явно заданные projection fixtures для проверки UI/cache/timers, не подтверждение математической модели или реального tracker. Identity, products, features и commitments используют настоящий API. Подмена не применяется к остальному приложению или сценарию релиза.

[Сфокусированная матрица](regressions-matrix.json), [лог](regressions.log). Оба сценария PASS, 0 JS errors; projection GET: 4 overview и 3 forecast.

## Приёмка без подмен

Прежний `acceptance-roadmap25.cjs` повторён против того же настоящего API **без подмены ответов**: 12 групп dark/light, 45 проверок HTTP-статусов, 0 JS errors. Roadmap/calendar/launch preservation, release lifecycle/EOL, настоящий outbox→risk→actions→ack, отказы presale/marketing и mobile read-only сохранены.

[Матрица](unmocked-matrix.json), [лог](unmocked.log), [provenance и хеши](provenance.json). Четыре выбранных снимка находятся рядом. Web lint/typecheck, 20 Node tests, production build/licenses и 2 Python safety tests — PASS.

## Воспроизведение

Сначала production build, затем новый `visual-stand-ds56.py`. Указать `METIS_ACCEPTANCE_URL`, `METIS_ACCEPTANCE_TEMP`, операторские `METIS_PLAYWRIGHT_MODULE` и `METIS_CHROMIUM_PATH`; запустить `node web/scripts/review-regressions58.cjs` и `node web/scripts/acceptance-roadmap25.cjs`. Новых зависимостей нет. Во время теста не пересобирать dist; этот прогон использовал отдельную копию assets. Стенд после проверки остановлен, временные токены удалены. Проверка человеком pending; итоговый exact-head CI фиксируется в PR отдельно.
