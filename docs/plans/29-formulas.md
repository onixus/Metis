# Итерация29 — текущие общие формулы без архивной DSL

Требования EC-08/09/10/11/13, NF-S01/NF-S05. Прочитаны docs/spec.md и ADR0029. Исправление найденного review дефекта.

План до кода:
1. Отделить inputs/standard P&L расчёт от исполнения custom DSL, сохраняя полную структурную проверку полей, строк, денег, аллокаций и budget.
2. Для bound текущих отчётов/проекций применять inputs-only режим; для архивных original версий, ставших historical, оставлять исходную DSL; postbinding версии всегда используют общий registry даже при pinned inputs (ADR0033). Текущие показатели берутся только из общего registry.
3. До append новой книги проверять общие формулы на изолированном candidate snapshot без записи; source captures/audit в PostgreSQL остаются в вызывающей транзакции. Ошибка не сохраняет snapshot.
4. Регрессии: old revenue/payroll → общая безопасная формула → payroll0 save/report/scenario/projection успешно; старый pinned report неизменен; невалидная новая общая формула запрещает save; scope/cost/type guards сохранены.
5. Go race тесты экономики/app и lint. Нет новых зависимостей, HTTP-контрактов и миграций.

NF-L01: Codex, человеческое ревью pending.

Итог: ADR0033 и question53 фиксируют границу original/postbinding history. `CommonDefinitionPolicy` выбирает inputs-only по scoped metadata cutoff. Save до append считает общие показатели на private candidate snapshot; Scope не меняется. Reconciliation preview использует тот же режим локального входа.

Регрессии: current save/report/scenario/project/reconciliation, original pinned DSL, пост-binding pinned inputs/lineage и отказ aggregate доступа — PASS; PostgreSQL candidate denominator failure откатывает аудит и не добавляет version, cutoff после PG read подтверждён. Go race economics/app PASS; lint повторён после удаления unused wrapper path.
