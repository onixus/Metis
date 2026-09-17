# Мок Jira (WireMock)

Синтетические ответы без реальных имён и токенов. Сценарий приёмки 7.7 п.5 (сдвиг эпика «SOAR-42 Коннектор EDR v2» на 21 день):

1. Исходное состояние: `GET /rest/api/2/issue/SOAR-42` → due date `2026-12-01`.
2. e2e переключает сценарий: `PUT /__admin/scenarios/epic-shift/state` с телом `{"state":"Shifted"}` → due date `2026-12-22`.
3. e2e отправляет тело `__files/webhook_epic_shifted.json` на webhook-эндпоинт платформы; плановая сверка `Sync` даёт тот же результат.
