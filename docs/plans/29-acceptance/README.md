# Итерация29 — проверка исправлений ревью

Изоляция перед merge: исходная финансовая итерация27 перенумерована в29, чтобы сохранить уже принятые планы main. JSON/PNG и хеши исторических доказательств сохранены побайтово; пути/номера внутри raw JSON относятся к исходной проверке и не удостоверяют финальный merge SHA. Финальная проверка изолированной ветки фиксируется отдельно в [плане30](../30.md).

2026-10-25: все три воспроизведённых дефекта устранены. [Результаты/хеши логов и финальных исходников](gates.json). Данные синтетические, PostgreSQL16.14 UTF8 на loopback, внешние источники в регрессиях — контролируемые тестовые реализации публичных портов, способные изменять ответ между чтениями.

## Регрессии

- `TestEC09_EC13_FormulaSlicesShareCompleteCRMInput`: total=sum(products), один полный read в обоих порядках срезов; новая операция перечитывает источник.
- `TestEC09_EC13_FormulaSlicesShareWorklogUniverseAndBookVersion`: общий worklog universe и зафиксированная книга/headcount; `TestEC09_EC13_NFS05_PostgresFormulaSlicesShareRawCapture` повторяет CRM-согласованность с реальными persisted source snapshots.
- `formula_capture_test.go`: concurrent single-read, Scope/provider/session isolation, clone isolation, ошибки/отмена,32MiB retention budget и отсутствие partial value на отказе.
- `TestEC08_EC09_EC11_EC13_CurrentCommonFormulaOwnsBookUpdates`: старый revenue/payroll заменён общей безопасной формулой; payroll0 Save/Report/Scenario/Project/Reconciliation проходит; original pinned версия сохраняет старую DSL; postbinding pinned inputs используют общий registry/lineage; ошибка candidate не меняет книгу.
- `TestEC09_EC11_EC13_NFS05_PostgresCurrentFormulasValidateCandidateAndPinnedInputs`: реальный PostgreSQL cutoff/pinned inputs, отказ current candidate и rollback audit.
- `feature_canonical_scope_test.go`: до/после binding восстановлены успешные вызовы FeatureEconomics/BranchCosts; zero/aggregate-only/foreign-product scope отвергаются, точные суммы проверены с явно настроенными migrated custom keys. Автоматическое отображение FeatureID/Branch metadata на стандартные keys в это исправление не входит.

## Общий gate

На свежих отдельных БД задать `METIS_TEST_DATABASE_URL`, `METIS_RUNTIME_TEST_DATABASE_URL`; выполнить `CGO_ENABLED=1 go test -race -count=1 -tags integration ./...`. Полный прогон PASS, включая новые PG-регрессии и HTTP/e2e. Full golangci-lint0/vet/build CGO_ENABLED0/gofmt/diff-check, Go licenses, gitleaks — PASS. `make generate` воспроизводит43 files побайтово. HTTP/OpenAPI/UI контрактов и зависимостей итерация29 не меняет; новый browser прогон не выполнялся, браузерные доказательства26 относятся к предыдущей версии backend.

Независимый агент просмотрел source cache и candidate/cutoff изменения без подтверждённых замечаний. Человеческое ревью pending. Удалённый CI/обновлённая vulnerability DB govulncheck не запускались из-за AGENTS registry-only сети; merge/deployment не выполнялись. Временный PostgreSQL после проверки остановлен.
