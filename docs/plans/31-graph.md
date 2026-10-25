# Итерация 31 — граф, UI и приёмка контрактов

Дата: 2026-10-25. Исполнитель: агент Codex graph_ui; человеческое ревью pending.
План до кода: [31.md](31.md). Существующие изменения других итераций сохранены.

## Реализовано

- PG-03/05: форма продуктовой/фичевой связи без drag, с парным выбором endpoints; смешанное соединение фичи и продукта явно отклоняется вместо молчаливого сохранения продуктовой связи. Сервер остаётся источником проверки циклов и полномочий.
- PG-09: product deep link `?product=`, синхронизация выбора/сброса с URL, совместные фильтры типа связи/продукта/lifecycle и дополнительный статус контракта; пустой результат и ошибки загрузки раскрытых фич не маскируются под пустой список.
- PG-09: фичевые handles используются только после загрузки обоих endpoints, `updateNodeInternals` обновляет их геометрию после раскрытия/загрузки. Вспомогательная загрузка фич формы не увеличивает закрытые узлы.
- AD-02: источники connections и удаление доступны по существующей ActionWriteGraph (admin/cpo/service либо PM private на потребителя); read-only роль не видит создание связи. Рёбра контракта не удаляются вручную; меняются через редактор контракта.
- PG-08: `useShiftDate` инвалидирует проекции всех продуктов, включая downstream strategic/features/graph-features, и контракты.
- В GraphPage подключён ContractsPanel основного агента. Его PG-04 create/edit flow проверен через браузер и readback реального API.

## Проверки

`npm run typecheck`, `npm run lint`, `node --test src/lib/graph.test.ts` — PASS (4 теста с ID: фильтры без dangling edges, парные endpoints, готовность handles, deny graph write gate).
`node --check web/scripts/acceptance-graph31.cjs`, целевой `git diff --check` — PASS.

Финальный browser acceptance — PASS на замороженном latest API/dist loopback-стенде `http://127.0.0.1:18079`, синтетические данные, ephemeral memory storage. Скрипт: `web/scripts/acceptance-graph31.cjs`.

1. PG-09: product URL filter и сброс через React Router.
2. PG-03: продуктовая связь через доступную форму сохранена и прочитана API.
3. PG-03/05: фичевая связь через форму; обратная связь отклонена HTTP409 с видимым путём цикла.
4. PG-04: контракт создан с фичами обеих сторон/версией/владельцем/матрицей; затем изменены статус/критичность/версия, readback проверил значения и два generated ребра с новой критичностью.
5. PG-04: для POST и PUT `page.route` отправил mutation в реальный API через `route.fetch`, удержал только ответ браузеру; во время ожидания input, добавление пары версий и отмена недоступны. Затем исходный ответ возвращён и проверен API readback. Это регрессия потери правок при незавершённом сохранении.
6. PG-09: desktop1440/mobile390, светлая/тёмная темы через реальный переключатель, full/focused graph и фильтр статуса контракта; горизонтального переполнения документа нет.
7. AD-02: marketing read-only роль не видит создания связи/контракта. `pageerror`: 0.

Исходные финальные evidence: `/tmp/metis31-browser-pr-final/graph31/results.json`, `graph-{light,dark}.png`, `graph-mobile-{light,dark}.png`, `graph-mobile-focused-{light,dark}.png`, `graph-contract-filter.png`, `contract-pending-{create,edit}.png`. Визуально просмотрены `graph-mobile-focused-light.png` и `contract-pending-edit.png`: поля/контролы читаемы, матрица на mobile имеет внутреннюю прокрутку, pending элементы disabled. Полный граф десяти продуктов требует zoom/pan либо product filter на узком экране.

Chromium в обычном sandbox завершался SIGABRT/EPERM; локальная browser приёмка выполнена через одобренный require_escalated. Предварительные неуспешные попытки были проблемами готовности стенда/селекторов/ожидания React Router; финальный прогон PASS. Внешняя сеть, реальные данные, публикация, merge и deployment не использовались.
