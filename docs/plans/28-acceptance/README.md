# Итерация28 — доказательства приёмки

Изоляция перед merge: исходная финансовая итерация26 перенумерована в28, чтобы сохранить уже принятые планы main. JSON/PNG и хеши исторических доказательств сохранены побайтово; пути/номера внутри raw JSON относятся к исходной проверке и не удостоверяют финальный merge SHA. Финальная проверка изолированной ветки фиксируется отдельно в [плане30](../30.md).

2026-10-25. Реальные PostgreSQL16.14/Go API/Chromium на loopback, синтетические данные и HMAC роли. Ответы API не подменялись. Автор — агенты Codex, человеческое ревью pending.

## Объединение истории

[HTTP результат](unification-acceptance.json): исходные3 book revisions и3 model versions сохранены; CAS/stale digest409;3 отказа доступа; повторное применение, доступ к старому отчёту, живые входы/общие определения, новый manual ввод в книгу без независимых batches и запрет несовместимых определений — PASS.

[Браузерный результат](unification-browser.json): dark/light × desktop1440/mobile390; предпросмотр расхождений P&L40000 minor RUB, явный checkbox, точный digest плана JSON, применение, скачивание архива (2 исходных book versions/1model version), прежний P&L на выбранной книге, отсутствие JS errors/переполнения — PASS. [Desktop dark](unification-dark-1440.png), [mobile light](unification-light-390.png), [финальные source/build хеши](unification-provenance.json). Мобильный снимок фиксирует нижнюю часть мастера со сверкой и подтверждением результата.

Воспроизведение: `npm --prefix web run build`; создать новую PostgreSQL16 UTF8 БД и приватный файл URL, запустить `web/scripts/visual-stand-ds56.py --temp-dir NEW_PRIVATE_DIR --port FREE_LOOPBACK_PORT --database-url-file PRIVATE_FILE --crm-dir SYNTHETIC_CRM_DIR`. Выставить `METIS_ACCEPTANCE_URL`/`METIS_ACCEPTANCE_TEMP`, затем выполнить `python3 web/scripts/acceptance-economics-unification.py` и `node web/scripts/acceptance-economics-unification.cjs`. Для нестандартного Playwright задать `METIS_PLAYWRIGHT_MODULE`/`METIS_CHROMIUM_PATH`. Runner рассчитан на свежие месяцы; `METIS_UNIFICATION_PERIODS` позволяет выбрать два новых месяца для повторной проверки UI. API должен быть перезапущен после замены dist: startup index и assets должны соответствовать одной сборке.

Точный план/архив скачивались и проверялись в приватной временной папке; токены в репозиторий не переносились. Временные API и принадлежащий проверке PostgreSQL после приёмки остановлены.

## Прямые источники и gates

[Приёмка sources](sources.md), [headcount](sources-headcount.json), [CRM](sources-crm_revenue.json), [provenance](sources-provenance.json). Source-сценарии сняты до финального уточнения текстов предупреждений в мастере объединения; соответствующие source UI/engine файлы не менялись. Для каждого прогона сохранён собственный хеш web build. Worklogs в браузере отключён и проверен как503; рабочая агрегация и isolation проверены app/domain/PG тестами.

[Gates и хеши логов](gates.json): полный race/integration всех Go-пакетов на свежих PostgreSQL БД, lint0/vet/build/gofmt, воспроизводимость43 generated files,18 web tests/lint/typecheck/build, Go/web licenses и gitleaks — PASS. Команды/границы в [основном плане](../26.md). Финальная правка после Go gate затрагивала только UI-тексты, browser runner и документы; web lint/typecheck/build и browser повторены.

Удалённый CI, свежая vulnerability DB govulncheck, production/нагрузка, реальные Jira/CRM/OIDC и другие браузеры не проверялись. Сеть по AGENTS разрешена только к реестрам пакетов. Полнота всего раздела экономики не заявляется за пределами выбранных двух направлений.
