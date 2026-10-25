# Итерация29 — согласованность прямых источников одного расчёта

Требования: EC-09, EC-10, EC-11, EC-13; архитектурные инварианты Scope/публичных портов. Перед кодом перечитаны AGENTS.md, соответствующие строки docs/spec.md и ADR0030/0031.

План: исправить исполнение принятого решения об одном capture на операцию: отдельный session cache полных ответов CRM, книги/headcount, worklogs+полного issue universe и team mapping. Ключ включает исходный Scope, идентичность приложения/провайдера и месяц/universe; разные срезы только проецируют один ответ. Cache не глобальный и не меняет immutable persisted snapshots/закрытые периоды. Ожидание конкурентного capture учитывает отмену, callbacks разных ключей не держат общий mutex и не создают рекурсивный deadlock. Ошибка полного capture не маскируется нулём. Новых зависимостей, API-контрактов и миграций не требуется.

Добавить регрессии на один вызов CRM и равенство total=sum(products), обратный порядок срезов, worklogs/headcount, новую операцию/Scope/provider, concurrent/cancel/error. Проверить focused race tests и lint; основной агент выполняет общий gate. Код/проверки — Codex, человеческое ревью pending.

## Реализация и проверки

`CaptureFormulaInput` хранит типизированные полные входы через session namespace, Scope fingerprint и provider identity. Первая загрузка вне mutex; concurrent readers ждут per-entry channel с отменой, ошибки сохраняются до конца операции, возвращаемые коллекции клонируются. Срезы используют один CRM response и один worklog issue universe/полный response/mapping; все book/headcount срезы используют одну версию книги. У каждой новой операции новая session. Бюджет128 raw capture entries; global cache отсутствует. Обычные scalar Value/Explain и прямой adapter call также создают локальную session, composed caller сохраняет уже созданную.

PASS: `CGO_ENABLED=1 go test -race -count=1 ./internal/app ./internal/economics/modeling`; focused source regressions; реальный PostgreSQL16 `TestEC09_EC13_NFS05_PostgresFormulaSlicesShareRawCapture` с отдельной создаваемой/удаляемой БД; scoped golangci-lint0 issues. Логи: `/private/tmp/metis27-source-{fullrace,pg,lint,regressions}.log`. Новый PG-тест подтверждает один CRM fetch и нулевое total−productA−productB; обычный тест проверяет оба порядка срезов и обновление в следующей операции. Дополнительные регрессии — worklogs один complete fetch, headcount pinned book, concurrent/clone/cancel/error/Scope/provider isolation. API/SQL/generated не менялись.

По независимому review добавлен общий raw retention budget32MiB JSON-представления до клонирования в cache, помимо128 entries. Регрессия проверяет точную границу, отказ следующего capture, отсутствие удержания oversized и partial-error payload.
