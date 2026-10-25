# Итерация 28 — объединение исторических контуров экономики

Требования EC-07…EC-13, AD-04, NF-S02/NF-S05. До кода: выбрать единственный канонический источник периода явным preview/apply, сохранить исходные истории и определения, отказать неоднозначностям вместо суммирования независимых денег. Миграция и её audit выполняются одной scoped PostgreSQL-транзакцией, повтор идентичного плана идемпотентен, устаревший digest отклоняется. Чтения старых версий остаются воспроизводимыми. Новые зависимости и изменения применённых миграций не требуются; append-only binding добавляется новой миграцией.

План: публичный экспорт книг → архив обеих историй и conflict preview → canonical binding → единые чтения Facts/PnL с защитой от двойной аллокации → отказ legacy input writes после binding → отказные/race/PG rollback/restart/idempotency тесты. Definitions мигрируются в общий registry только при проверяемом однозначном mapping, исходная DSL сохраняется вместе с provenance.


## Реализация и проверки

Добавлены append-only migration archive/binding в modeling.documents (новая миграция00002), canonicalStore со scoped публичным book-портом, импорт определений/версий в namespace и перевод legacy DSL с сохранением точности, запрет второго источника записи, общий текущий Report и реальная lineage (book+directsource snapshots). Старые закреплённые book versions и исходные explicit model DataVersion сохраняют оригиналы.

Focused race: switch2model+2book revisions, stale/idempotent digest, исходные версии, динамическая видимость нового book fact, source-only scenario без изменения P&L, unbound aggregate access и bound deny — PASS. Реальная PostgreSQL16 regression: audit/outbox failure откатывает и registry, и binding; перезапуск сохраняет canonical20k; div16 даёт6666.6666666666666667; архивы2+2, explicit old version и запрет второй записи — PASS. Полный общий gate выполняет основной агент.

## Финальный статус агента истории

Исходники зафиксированы для общего gate. `go test -race -count=1 ./internal/economics/... ./internal/app` — PASS (на момент стабильного core до завершающей typed manual маршрутизации); текущий focused race всех history/canonical/team/bundle/namespace/reimport сценариев — PASS. **Полный PostgreSQL modeling/pgstore suite с -race -tags integration — PASS (4.543s)** на PostgreSQL16 с изоляцией БД каждого top-level test. Scoped `golangci-lint` economics/app — **0 issues**. `git diff --check` — PASS.

Дополнительные регрессии: rejected batches/error rows в архиве, стабильный digest DataVersion0, Foo/foo+UpperMetric общий edit, полная семантическая история namespace, Source-only и виртуальные сценарные входы не меняют fixed PnL, старый Team.ID и101 минорная единица бандла, Item-разделённые canonical manual rows после повторного импорта. Общий HTTP/browser/full-repository gate выполняет основной агент.

## Последняя авторизованная проверка готовности плана

Архив теперь расходует консервативный32MiB byte budget по каждому snapshot и import batch **до добавления** в общий массив, затем по metadata/maps. Окончательный точный JSON-limit сохраняется и повторяется после назначения реального actor/time перед записью. Нового усечения истории нет. Регрессия128 средних версий подтверждает ранний отказ менее чем после16 чтений; отказ не уменьшает бюджет и не создаёт binding.

Preview также выполняет точный будущий общий отчёт на read-only overlay новых определений до их сохранения: весь набор текущих book metrics × продукты/итог, с теми же Explain limits и сравнением исходных значений. Валидная исходная цепочка глубже6, которая не может раскрываться общим Explain, отклоняется до применения без частично записанных определений. Архив сохраняет даже семантически совпавшие предсуществовавшие namespace definitions, исключения raw source histories больше нет.

`MetricVersion.result_type` сохраняет money/number/percent импортированного показателя, переживает restart и обычную правку формулы; тип/денежная валюта не меняются неявно. Полный PostgreSQL modeling/pgstore race suite после этих изменений — PASS (5.705s), focused history race — PASS (2.885s), scoped economics/app lint —0 issues, diffcheck —PASS. Дальнейшие исходники агента истории заморожены.
