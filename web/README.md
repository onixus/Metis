# Метида — веб-интерфейс

SPA на Vite + React 19 + TypeScript. Данные — через клиент, сгенерированный из `api/openapi.yaml`
(`npm run generate` → `src/api/schema.d.ts`). Загрузка данных — TanStack Query, граф — React Flow.
Язык интерфейса — русский, все строки в `src/i18n/ru.ts` (NF-M05).

## Запуск

```sh
cd web
npm ci
cp .env.example .env      # при необходимости поправить значения
npm run dev               # http://localhost:5173, /api проксируется на бэкенд
```

Dev-сервер проксирует `/api` на `http://localhost:8081` (переопределяется `VITE_API_PROXY_TARGET`).
В продакшене статику из `dist/` нужно раздавать с того же origin, что и `/api/v1`, либо настроить
обратный прокси.

Проверки: `npm run lint && npm run typecheck && npm run build`.

## Режимы аутентификации (`VITE_AUTH_MODE`)

- `token` — стенд и e2e. На странице `/login` вставляется Bearer-токен; он хранится в `sessionStorage`
  текущей вкладки и подставляется в заголовок `Authorization`. В журналы токен не попадает.
- `oidc` — вход через провайдера (Keycloak на стенде): Authorization Code + PKCE через `oidc-client-ts`.
  Нужны `VITE_OIDC_ISSUER`, `VITE_OIDC_CLIENT_ID`; redirect URI — `<origin>/callback`,
  post-logout — `<origin>/login`. Сессия хранится в `sessionStorage`.

Права и аудитория (`internal` / `sales_safe`) берутся из `/me`; интерфейс их не задаёт, а только показывает.

## Маршруты

| Путь | Экран |
| --- | --- |
| `/` | Продукты с ролью хаба и уровнем доступа |
| `/graph` | Граф портфеля (PG-09): фильтры по типу связи, продукту, статусу |
| `/products/:id` | Продуктовый дашборд: стратегический срез; в приватном контуре — бэклог, сдвиг дат, очередь сигналов |
| `/products/:id/roadmap` | Roadmap: таймлайн / now-next-later / по релизам, изменение дат с причиной, история |
| `/hub` | Дашборд хаба: контракты, топ фич по производному спросу, затронутые фичи зависимых продуктов |
| `/delivery` | Delivery: план/факт по фичам, затронутые сдвигом; место под метрики спринтов |
| `/admin` | Проверка целостности аудита (роли `admin`/`compliance`) |

## Структура `src/`

- `api/` — `client.ts` (openapi-fetch, Bearer, problem+json → сообщения), `hooks.ts` (TanStack Query), `types.ts`, `schema.d.ts` (генерируется)
- `auth/` — провайдеры `token` и `oidc`, `AuthContext`
- `i18n/ru.ts` — строки интерфейса
- `components/` — каркас и общие элементы
- `pages/` — экраны по маршрутам
- `lib/format.ts` — даты, деньги (минорные единицы), разница дней
