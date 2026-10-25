# Итерация 31 — проверка Портфеля и графа

2026-10-25. Финальные исходники и хеши логов: [gates.json](gates.json). Работа выполнена основным Codex и тремя агентами, взаимное агентское ревью завершено, человеческое ревью pending. Данные синтетические, реальные сервисы клиентов не использовались.

## Проверки

Полный `CGO_ENABLED=1 go test -race -count=1 -tags integration ./...` PASS на PostgreSQL16.14 с двумя чистыми UTF8/en_US.UTF-8 базами, owner и отдельным LOGIN metis_app для runtime. Включены реальные transaction rollback, concurrent replicas, property tests, PG08 earlier/later и persisted reload. Lint0/vet/build CGO_ENABLED0, frontend lint/typecheck/31 tests/build, Go/frontend licenses и gitleaks PASS. `make generate` PASS, 40 выбранных generated files побайтово не изменились.

Первые подготовительные прогоны выявили неверную конфигурацию окружения: локаль C меняет русский FTS ranking, отсутствие LOGIN metis_app блокирует runtime. Они не считались успешной проверкой. Финальный полный прогон выполнен на новых базах с правильной Unicode локалью и прикладной ролью, без изменения/пропуска тестов поиска. go-licenses запущен с GOROOT активного toolchain (без него установленный бинарник ошибочно относит stdlib к внешним модулям). Ожидаемые warnings об assembly в stdlib/dependencies не являются лицензионными находками.

## Browser acceptance

Реальная статическая production сборка и API memory/HMAC, loopback, adapters Atlassian выключены. Финальный API включает исправление переноса дат в обе стороны. Скрипты `web/scripts/acceptance-portfolio31.cjs` и `acceptance-graph31.cjs`:

- [Portfolio results](portfolio31-results.json): PG01 CPO/PM edit; PG02 capability→feature→requirement с повторным чтением после reload; PG06 тип security и manual hub независимо, PM не меняет hub; PG08 детали затронутых дат; PG10/presale доступный обзор и отказ приватной иерархии.
- [Graph results](graph31-results.json): URL/product filter/reset; продуктовые/фичевые связи через форму; reverse cycle409 и путь; создание/редактирование контракта с фичами, owner/version/status/criticality/матрицей и API readback; readonly controls; light/dark desktop/mobile. Настоящие POST/PUT выполняются через route.fetch, ответ удерживается до проверки блокировки input/add-pair/cancel.

В обоих итоговых сценариях browser pageerrors отсутствуют, горизонтального overflow страницы нет. Скриншоты включены выборочно. При агентском ревью найдено редактирование полей во время записи контракта с последующей потерей ввода; исправлено disabled fieldset и покрыто удержанным ответом. Scoped отчёты: [backend](../31-backend.md), [portfolio](../31-portfolio.md), [graph](../31-graph.md), [review](../31-web.md).

## Воспроизведение

Задать METIS_TEST_DATABASE_URL и METIS_RUNTIME_TEST_DATABASE_URL на разные свежие UTF8 БД с Unicode LC_CTYPE; перед миграциями создать LOGIN metis_app (без superuser), как требует runtime test helper. Запустить полный race/integration. Для generators добавить установленный oapi-codegen в PATH, выполнить `make generate`; для go-licenses задать GOROOT активного Go toolchain.

Для UI: `npm --prefix web run build`; `NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost python3 web/scripts/visual-stand-ds56.py --temp-dir NEW_PRIVATE_PATH --port FREE_PORT`. После readiness задать METIS_ACCEPTANCE_URL, METIS_ACCEPTANCE_TEMP; при необходимости METIS_PLAYWRIGHT_MODULE/METIS_CHROMIUM_PATH; выполнить оба acceptance scripts. Во время browser прогона dist/API не заменять. Временные credentials в evidence не копируются.

Remote CI/обновлённый govulncheck не запускались: AGENTS разрешает сеть только к реестрам пакетов. Внешний OIDC/Atlassian, production, deployment и ёмкость production не проверены. Работа выделена в отдельный worktree от main41acf69; посторонние изменения в исходном checkout сохранены. Подготовлено [PR описание](../../pr/31-portfolio-graph.md); merge/deploy не выполнялись.

Проверки gates.json и browser results повторены на выделенной ветке от актуального main41acf69 перед коммитом. Предыдущая приёмка общего рабочего дерева сохранена отдельно как initial-workspace-gates.json; она не заменяет проверку PR. Локальный номер28 перенумерован в31, существующие документы28 из main сохранены.
