# Итерация31 — backend портфеля и графа

Реализованы PG-02/04/05/07/08/10:

- GET возможностей продукта и требований фичи с приватным scope; OpenAPI изменён первым, сервер/client/schema перегенерированы `make generate`.
- Capability чужого продукта отклоняется при UpdateFeature.
- Контракт и принадлежащие ему рёбра заменяются атомарно (MemStore lock/PostgreSQL transaction). Candidate cycle check исключает устаревшие рёбра, ручные зависимости сохраняются. Смена сторон, дубли фич и неизвестный status отклоняются; прямое удаление contract-owned edge возвращает Conflict. ADR0034.
- При изменении коэффициентов пересчитывается максимальная критичность параллельных рёбер; settings и contract slices возвращаются копиями.
- Сдвиг дат в обе стороны рассчитывается от поставщиков к потребителям из актуальных PlannedDate всего DAG. Учитываются upstream constraints самого источника, промежуточные и другие supplier dates; устаревший ImpliedDate не используется как вход, устранённые задержки/предупреждения снимаются. Регрессия раннего переноса проверяет persisted reload.
- StrategicSlice возвращает готовность контрактов, вычисленную из плановых и implied dates.

Проверки: `GOCACHE=/private/tmp/metis31-go-cache CGO_ENABLED=1 go test -race ./internal/portfoliograph/... ./internal/httpapi/...` PASS. Реальные HTTP GET private/strategic/none и PG10 ready_date проверены. Targeted golangci-lint PASS (0 issues), `git diff --check` PASS. `make generate` PASS.

Добавлен integration TestPG04_PGContractReplacementAtomicRollback: FK-ошибка после обновления контракта/удаления прежних рёбер должна откатить обе операции; успешная замена и отказ hidden endpoint. Результат PostgreSQL integration будет записан после запуска временной БД основным агентом.

Реализовано агентом Codex, человеческое ревью pending. Чужие незакоммиченные изменения сохранены; миграций и новых зависимостей нет.

После дополнительного PG08 review targeted race portfoliograph/httpapi повторён — PASS.

Финальный parent gate: полный Go race/integration на свежих PostgreSQL16.14 UTF8/en_US.UTF-8 БД с отдельным runtime LOGIN metis_app — PASS, включая TestPG04_PGContractReplacementAtomicRollback и все runtime/e2e. Финальные lint/vet/build и generation PASS, evidence в 31-acceptance.
