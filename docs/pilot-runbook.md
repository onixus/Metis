# Эксплуатация пилота Метиды

Итерация 12, 2026-09-22. Цель — воспроизводимый пилот с PostgreSQL, отдельным worker и браузерным интерфейсом на одном origin. Коммерческая приёмка, восстановление из резервной копии, SLO и масштабирование требуют отдельных испытаний.

## Стенд, проверенный в этой итерации

Локальный Compose project `metis-pilot-iteration12` запущен на `http://127.0.0.1:18083`; порты PostgreSQL/Jira mock/Confluence mock — 15434/18090/18091. Это отдельный синтетический стенд, не затрагивающий другие локальные проекты. Пока он работает, получить токен для входа можно из корня репозитория:

```sh
docker compose -p metis-pilot-iteration12 -f deploy/compose/docker-compose.yml exec api /dev-token -subject pilot-cpo -roles cpo
```

Для обновления именно этого стенда сохраняйте его имя, volume и порты:

```sh
METIS_API_PORT=18083 METIS_POSTGRES_PORT=15434 METIS_JIRA_MOCK_PORT=18090 METIS_CONFLUENCE_MOCK_PORT=18091 \
  docker compose -p metis-pilot-iteration12 -f deploy/compose/docker-compose.yml up --build -d
```

Далее описан обычный запуск нового стенда с портом 8082 по умолчанию.

## Сборка и запуск стенда

Нужны Docker Compose v2 и доступные локально образы из `deploy/Dockerfile` и `deploy/compose/docker-compose.yml`. При изолированной установке заранее загрузите базовые образы и зависимости в разрешённые реестры. В runtime API и браузер не используют CDN.

```sh
docker compose -f deploy/compose/docker-compose.yml config --quiet
docker compose -f deploy/compose/docker-compose.yml up --build -d
docker compose -f deploy/compose/docker-compose.yml ps -a
curl --fail http://localhost:8082/readyz
```

Приложение открывается на `http://localhost:8082`, API — `/api/v1`, спецификация — `/api/openapi.yaml`. `METIS_API_PORT` меняет опубликованный порт; внутри контейнера API слушает 8081. PostgreSQL и моки также публикуются только на loopback; их порты меняются через `METIS_POSTGRES_PORT`, `METIS_JIRA_MOCK_PORT`, `METIS_CONFLUENCE_MOCK_PORT`.

Сначала PostgreSQL создаёт роль `metis_app`. Одноразовый сервис `migrate` применяет embedded-миграции с owner-подключением. После его успешного завершения API и worker подключаются как `metis_app` с `METIS_MIGRATE=false`. Роль приложения не владеет схемами и не получает UPDATE/DELETE на журналы аудита и доказательств. Заданные в Compose `metis-dev-only` — синтетические учётные данные локального стенда; для внешнего пилота нужны отдельные секреты и TLS.

Статический frontend включён в образ API. `METIS_WEB_DIR=/web` задаётся образом; отдельные nginx и Node в runtime не нужны. `/runtime-config.js` содержит только режим входа и публичные OIDC-параметры и не кешируется. Секреты API и БД в браузер не передаются. Старые deep links (`/products/.../roadmap`, `/callback`) открываются после обновления страницы; отсутствующие API-маршруты и assets не подменяются HTML.

## Вход в локальный HMAC-стенд

По умолчанию Compose работает в HMAC-режиме. Выпустите токен синтетического пользователя на один час и вставьте его на странице входа:

```sh
docker compose -f deploy/compose/docker-compose.yml exec api /dev-token -subject pilot-cpo -roles cpo
```

Для проверки ограничения PM одним продуктом:

```sh
docker compose -f deploy/compose/docker-compose.yml exec api /dev-token -subject pilot-pm-edr -roles pm -products edr
```

CLI использует серверный секрет из окружения контейнера, отказывается работать без `METIS_AUTH_MODE=hmac`, требует явного пользователя и ограничивает срок токена диапазоном 1 минута — 8 часов. Токен выводится только в терминал оператора; его нельзя добавлять в issue, логи приложения или файлы репозитория. Роль `cpo` выбрана для демонстрации сквозного процесса; для проверки ABAC используйте PM и presale.

## Вход через OIDC

Поставляемая SPA берёт режим входа и issuer из конфигурации API. Изменение IdP не требует пересборки JavaScript. Для внешнего IdP задайте `METIS_AUTH_MODE=oidc`, `METIS_OIDC_ISSUER`, `METIS_OIDC_CLIENT_ID`; issuer должен быть одним и тем же URL, доступным API и браузеру. Публичный клиент использует Authorization Code + PKCE S256; access token должен иметь audience, совпадающий с `METIS_OIDC_CLIENT_ID`, и клеймы `roles`, `products`.

Для Keycloak из Compose:

1. Добавьте на машине оператора DNS/hosts-запись `127.0.0.1 idp.metis.test`. Compose уже задаёт такой DNS alias для контейнеров. Общий issuer — `http://idp.metis.test:8080/realms/metis`.
2. Запустите IdP и дождитесь discovery endpoint:

   ```sh
   docker compose -f deploy/compose/docker-compose.yml --profile oidc up -d keycloak
   curl --fail http://idp.metis.test:8080/realms/metis/.well-known/openid-configuration
   ```

3. После готовности IdP запустите приложение:

   ```sh
   METIS_AUTH_MODE=oidc docker compose -f deploy/compose/docker-compose.yml --profile oidc up --build -d
   ```

4. Откройте `http://localhost:8082`. Синтетические пользователи realm: `cpo`, `pm-edr`, `pm-vm`, `pm-soar`, `presale`, `admin-metis`, пароль стенда `metis-dev-only`.

Realm содержит audience mapper и разрешает redirect URI `http://localhost:8082/callback` и `http://localhost:5173/callback`. При другом порте/домене добавьте конкретные callback и logout URI в IdP. HTTP и пароль стенда допустимы только в локальной демонстрации; внешний пилот использует HTTPS. До первого старта OIDC API IdP должен быть доступен для discovery; ошибка discovery завершает запуск API.

## Проверка сквозного сценария

1. Войти как CPO, выбрать EDR или создать свой синтетический продукт.
2. Принять B2B-сигнал спроса, создать/выбрать фичу и связать сигнал с ней.
3. Ввести оценку, включить фичу в roadmap и изменить её дату с причиной.
4. Проверить затронутые контракты/обязательства и историю дат; дождаться работы отдельного worker.
5. Проверить compliance-трек, доказательства и стоимость подтверждения; изменить настройки с разрешённой ролью.
6. Перезапустить API/worker и убедиться, что данные и настройки сохранились.
7. Выйти, войти как PM другого продукта или presale и убедиться, что закрытые данные предыдущей сессии не видны.

Ядро работает без Atlassian. Для такого испытания отключите оба адаптера одинаково у API и worker:

```sh
METIS_JIRA_BASE_URL= METIS_CONFLUENCE_BASE_URL= \
  docker compose -f deploy/compose/docker-compose.yml up -d api worker
```

Пустой URL отключает адаптер. Запрос публикации ADR без базы знаний отклоняется API; отсутствие адаптера не является успешным выполнением внешней команды. Синхронизацию трекера и проверку продлений worker выполняет с интервалами `METIS_DELIVERY_SYNC_INTERVAL` (в Compose по умолчанию `5m`) и `METIS_RENEWAL_INTERVAL` (`24h`). Интервал сверки стенда меньше порога устаревания проекции `15m`; при собственных настройках сохраняйте это соотношение. Значение по умолчанию самого бинарника и Helm — `1h`, для такого развёртывания задайте интервал явно.

## Локальный запуск без сборки образов

```sh
cd web
npm ci
npm run build
cd ..
export METIS_AUTH_MODE=hmac
export METIS_HMAC_SECRET="$(openssl rand -hex 32)"
METIS_STORAGE=memory METIS_SEED=true METIS_WEB_DIR=web/dist METIS_HTTP_ADDR=:8082 go run ./cmd/api
```

Во втором терминале нужен тот же секрет из доверенного окружения. Команда `go run ./cmd/dev-token -subject pilot-cpo -roles cpo` выдаёт токен. Память подходит для демонстрации: данные теряются при остановке. Для разработки Vite сохраняет `.env`-конфигурацию, например `VITE_API_PROXY_TARGET=http://localhost:8082 npm run dev` в `web`.

## Kubernetes и миграции

По умолчанию Helm запускает по одной реплике API и worker. `config.migrateOnStart=false`: секрет `metis-secrets.databaseUrl` содержит подключение **metis_app**, не владельца схем. Для этой роли администратор БД до миграций создаёт LOGIN, задаёт пароль в хранилище секретов и выдаёт CONNECT к БД. Миграции выдают права схем/таблиц, если роль `metis_app` уже существует.

До `helm upgrade --install` примените миграции отдельно. Образ `registry.local/metis/migrate:<version>` строится из `deploy/Dockerfile` с `--target migrate`. Пример одноразового Job, запускаемого оператором после создания Secret `metis-migration` с owner URL:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: metis-migrate-v010
spec:
  backoffLimit: 1
  template:
    spec:
      restartPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        seccompProfile: {type: RuntimeDefault}
      containers:
        - name: migrate
          image: registry.local/metis/migrate:0.1.0
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities: {drop: [ALL]}
          env:
            - name: METIS_DATABASE_URL
              valueFrom:
                secretKeyRef: {name: metis-migration, key: databaseUrl}
```

Дождитесь успешного завершения Job, затем обновляйте приложение. Job не является Helm hook: порядок создания секретов/роли/БД контролируется оператором. Owner Secret не передаётся API или worker. API startup probe допускает до 5 минут на запуск; ошибки отсутствующих схем исправляются миграцией, не отключением readiness.

```sh
helm lint deploy/helm/metis
helm template metis deploy/helm/metis > /tmp/metis-rendered.yaml
helm upgrade --install metis deploy/helm/metis -f pilot-values.yaml
```

В `pilot-values.yaml` задайте внутренний registry/tag, OIDC issuer/client, Secret, ingress hostname и TLS. Для OTLP требуется доступный коллектор и его настройки; по умолчанию exporter выключен. Увеличение числа реплик и обновление без простоя допускаются после проверки согласованности графа, миграций и обработки событий под конкурентной нагрузкой.

## Диагностика и границы проверки

```sh
docker compose -f deploy/compose/docker-compose.yml logs --tail=100 migrate api worker
curl --fail http://localhost:8082/healthz
curl --fail http://localhost:8082/readyz
curl --fail http://localhost:8082/metrics
```

Liveness показывает доступность процесса; readiness API проверяет доступность БД. Это не проверка состояния всех коннекторов или гарантированного SLA очереди. Ошибки событий и DLQ проверяются отдельно. При обновлении выполняйте `up --build`, проверяйте завершение `migrate` и сохранность существующего `pgdata`; `down -v` удаляет данные и не используется для обычного обновления.

Автоматические in-process acceptance и PostgreSQL integration не заменяют браузерную приёмку, реальный OIDC и запуск каждого целевого образа. Доказательства выполненных в итерации проверок и оставшиеся ограничения приведены в обзоре готовности продукта.
