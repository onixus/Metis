# Итерация26 — браузерная приёмка выделенной ветки

2026-10-25: **PASS**, 12 групп сценариев dark/light, 43 явные проверки HTTP-статусов, 0 ошибок JavaScript. Прогон выполнен из отдельного worktree, без параллельной экономики: настоящий memory/HMAC API и outbox worker, синтетические данные, адаптеры выключены, ответы не подменяются. Desktop 1440×1000 и mobile 390×844.

Проверены launch tier/date, обязательность даты, фильтры, сохранение и сброс запуска, обновление календаря; запрет обхода readiness 409, очистка EOL через null, выпуск/снятие с поддержки, блокировка ветки; UI отсутствия данных прогноза; сдвиг→outbox→риск→назначение→эскалация с передачей ответственности→план обработки→история→подтверждение; presale/marketing 403 и read-only mobile. Число запросов отличается от итерации25 из-за меньшего количества polling GET, состав обязательных assertions сохранён.

[Матрица](matrix.json), [provenance](provenance.json). Снимки сохранены рядом. Заполненный прогноз с датой расчёта/историей/свежестью проверяется HTTP-тестом, сравнение сроков и отказ делать вывод по устаревшим данным — двумя Node-тестами.

Web gate: lint/typecheck, 14 Node tests, production build/license gate, 2 Python safety tests — PASS.

Воспроизведение: `web/scripts/acceptance-roadmap25.cjs` с новым `visual-stand-ds56.py` из этой ветки; переменные METIS_ACCEPTANCE_URL,METIS_ACCEPTANCE_TEMP,METIS_PLAYWRIGHT_MODULE,METIS_CHROMIUM_PATH. Не пересобирать dist во время проверки; в этом прогоне использован отдельный snapshot. Это Chromium/memory/HMAC, не подтверждение production, внешнего OIDC или реального трекера. PostgreSQL подтверждается отдельным integration/runtime прогоном. Человеческое ревью pending.
