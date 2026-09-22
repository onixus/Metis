# Экономика: 1С ЗУП, база 1С:Предприятия и файлы

Метида читает два независимых источника: ЗУП для агрегированного ФОТ по командам и учётную базу на платформе 1С:Предприятие для выручки/расходов. «1С:Предприятие» обозначает платформу; название прикладной конфигурации, её регистры и публикации необходимо указать при подключении. Адаптер не выбирает их автоматически и не записывает данные в 1С.

Работают ручные CSV/XLSX и чтение OData v3 с явной коллекцией и соответствием полей. Проверены синтетические OData-ответы `d.results`/`__next`, а также `value`/`odata.nextLink`; обращения к реальной базе заказчика в проверку не входили. Контракты ЗУП и учётной базы должны пройти приёмку на согласованных агрегатах.

## Файл и шаблон

Откройте «Экономика → Загрузить данные», выберите книгу и месяц, загрузите файл, выполните предпросмотр и сохраните версию. Пример CSV из интерфейса уже содержит UUID выбранного продукта. Общие синтетические примеры: [CSV](../fixtures/finance/example.csv), [XLSX](../fixtures/finance/example.xlsx), [шаблон](../fixtures/finance/template.json), [контрольный расчёт](../fixtures/finance/README.md).

Пустой `columns` автоматически распознаёт стандартные заголовки. Явный маппинг имеет вид `"product_id": "Продукт"`: слева поле Метиды, справа заголовок файла. Все явно выбранные колонки должны существовать. Обязательны `product_id`, `period`, `category`, `currency` и ровно одно из `amount`/`amount_minor`.

| Поле | Содержание |
| --- | --- |
| `product_id` | UUID продукта Метиды |
| `period` | Месяц `YYYY-MM`, сохранённый текстом; числовые Excel serial dates не угадываются |
| `category` | `revenue`, `payroll`, `direct_cost`, `marketing`, `hub_cost`, `certification_cost`, `maintenance_cost` |
| `amount` | Неотрицательная сумма в основных единицах, десятичная точка, без разделителей тысяч; RUB/USD/EUR/GBP/CNY, ровно представимая в двух минорных знаках |
| `amount_minor` | Альтернатива `amount`: неотрицательное целое `int64` в минорных единицах; доступно для других трёхбуквенных кодов валют |
| `currency` | Три прописные латинские буквы; валюты в одной книге/периоде не смешиваются, конвертации нет |
| `team_id`, `headcount` | Для `payroll` обязательны идентификатор команды и численность ≥1; импортируются агрегаты, не индивидуальные зарплаты |
| `feature_id`, `certification_track_id` | Необязательные UUID существующей фичи/трека соответствующего продукта |
| `branch`, `bundle_id`, `description` | Необязательные измерения и описание |

CSV: UTF-8; разделитель — запятая, точка с запятой или табуляция. `header_row` — физическая строка заголовка, начиная с 1. XLSX: `sheet` выбирает лист по имени, пустое значение — первый лист. Формулы читаются только по сохранённым scalar cached values. Файл с формулой без сохранённого результата нужно пересчитать в доверенном табличном редакторе и сохранить заново.

Любая ошибка строки запрещает фиксацию всего импорта; исходные значения в диагностике не повторяются. Повторная загрузка создаёт новую версию. Закрытый период требует явного пересчёта. Каждая принятая строка хранит имя источника, лист, номер строки и SHA-256 исходных байтов. Отпечаток импорта отдельно учитывает маппинг, чтобы его изменение не было пропущено при автоматической синхронизации.

## Профили источников

`METIS_FINANCE_SOURCES_FILE` указывает на локальный JSON конфигурации, смонтированный в API/worker. `METIS_FINANCE_SYNC_INTERVAL` задаёт интервал положительным Go duration, например `24h`. Учётные данные предоставляет хранилище секретов через переменные окружения, имена которых перечислены в профиле; значения секретов в JSON и репозиторий не помещаются.

В конфигурации ниже все `YOUR_...`, UUID и хосты — явные заглушки. Их нужно заменить после проверки опубликованной модели данных. Названия коллекций не являются именами стандартных регистров ЗУП или иной конфигурации.

```json
{
  "book_product_id": "11111111-1111-4111-8111-111111111111",
  "sources": [
    {
      "name": "zup-team-payroll",
      "kind": "onec",
      "username_env": "METIS_ZUP_USERNAME",
      "password_env": "METIS_ZUP_PASSWORD",
      "onec": {
        "base_url": "https://zup.example.invalid/YOUR_BASE/odata/standard.odata",
        "collection": "YOUR_PUBLISHED_TEAM_AGGREGATE",
        "fields": {
          "product_id": "YOUR_PRODUCT_REFERENCE_FIELD",
          "period": "YOUR_MONTH_FIELD",
          "amount": "YOUR_AGGREGATE_PAYROLL_FIELD",
          "team_id": "YOUR_TEAM_FIELD",
          "headcount": "YOUR_TEAM_HEADCOUNT_FIELD"
        },
        "product_ids": {
          "YOUR_EXTERNAL_PRODUCT_REFERENCE": "11111111-1111-4111-8111-111111111111"
        },
        "currency": "RUB",
        "category": "payroll",
        "period_format": "date"
      }
    },
    {
      "name": "business-revenue",
      "kind": "onec",
      "username_env": "METIS_BUSINESS_USERNAME",
      "password_env": "METIS_BUSINESS_PASSWORD",
      "onec": {
        "base_url": "https://business.example.invalid/YOUR_BASE/odata/standard.odata",
        "collection": "YOUR_PUBLISHED_REVENUE_AGGREGATE",
        "fields": {
          "product_id": "YOUR_PRODUCT_REFERENCE_FIELD",
          "period": "YOUR_MONTH_FIELD",
          "amount": "YOUR_AGGREGATE_REVENUE_FIELD"
        },
        "product_ids": {
          "YOUR_EXTERNAL_PRODUCT_REFERENCE": "11111111-1111-4111-8111-111111111111"
        },
        "currency": "RUB",
        "category": "revenue",
        "period_format": "date"
      }
    }
  ]
}
```

Для расходов можно добавить профиль той же учётной базы с отдельной коллекцией и `category: "direct_cost"` либо сопоставить поле `category`, в котором источник уже публикует коды статей Метиды. До согласования конфигурации преобразование счетов, проводок, натуральных знаков регистра и сторно не выполняется. `fields` принимает только явно названные скалярные свойства, без произвольных выражений и навигационных запросов. `product_ids` переводит внешние ссылки продуктов в UUID Метиды; без карты источник обязан возвращать непосредственно эти UUID. Неизвестная ссылка становится ошибкой строки.

`period_format: "month"` ожидает `YYYY-MM`; `"date"` принимает ISO дату или datetime и выделяет месяц исходной даты без сдвига часового пояса. Необязательный `filter` передаёт явно согласованный OData `$filter`. Профили должны публиковать один и тот же набор полных периодов; рекомендуется ограничить регулярную выгрузку текущим открытым месяцем. Пустая/неполная выгрузка и ошибка любого профиля сохраняют прежнюю книгу. Изменение закрытого периода автоматически не фиксируется.

Вместо OData каждый профиль может читать атомарно обновляемый файл:

```json
{
  "name": "zup-team-payroll-file",
  "kind": "file",
  "file": "/run/metis/finance/zup-payroll.xlsx",
  "template": {
    "sheet": "Finance",
    "header_row": 1,
    "delimiter": ",",
    "columns": {}
  }
}
```

Сначала публикуйте временный файл, затем заменяйте целевой файл атомарно. Пути файлов задаёт конфигурация развёртывания; HTTP принимает байты загрузки, а не путь на сервере. При объединении источников исходные хеши строк сохраняются, а книга получает общий отпечаток нормализованных данных. Неизменившаяся выгрузка не создаёт очередную версию.

## Границы импорта и проверки

| Ограничение | Значение |
| --- | --- |
| CSV/XLSX | 8 MiB |
| ZIP | 128 элементов, 16 MiB на элемент, 32 MiB суммарной распаковки, сжатие ≤100× |
| Таблица | 10 000 строк данных, 128 колонок, 4 KiB на ячейку |
| XML | Глубина 32; DTD, дополнительные processing instructions и дублирующиеся атрибуты запрещены |
| OData | 8 MiB на ответ, 32 MiB на снимок, 100 страниц, 10 000 строк |

Макросы, external links, внешние relationships, зашифрованные ZIP и небезопасные имена элементов отклоняются. XML/ZIP не извлекаются на диск. Внешние ссылки и формулы не исполняются. CSV-экспорт экранирует формульные префиксы, включая скрытые ведущими пробелами и управляющими символами.

OData использует только GET. Авторизация Basic передаётся по HTTPS; сервисная учётка должна иметь чтение только согласованных опубликованных агрегатов. Редиректы не выполняются, ссылка на следующую страницу обязана оставаться на исходных origin и коллекции. Тело ошибок источника, URL и учётные данные не включаются в диагностические сообщения. Финансовые данные разрешены роли `finance` с уровнем `full` и доступом к каждому участвующему продукту; выдача прав выполняется отдельно от настройки коннектора.

Проверка подключения: сопоставить публикуемые поля с JSON-профилем; сравнить preview с контрольными суммами ЗУП/учётной базы; проверить общую книгу, командный ФОТ и lineage; подтвердить неизменность повторной выгрузки, отказ на неизвестном продукте и сохранение книги при отказе одного источника. Тестовые HTTP-серверы подтверждают контракт и защитные границы, но не подменяют эту сверку на выбранной конфигурации.

## Передача конфигурации в Helm и Compose

Helm подключает существующий ConfigMap с файлами профилей в `/etc/metis` и, при необходимости, существующий PVC с выгрузками в `/data`. Оба тома монтируются только для чтения у API и worker. Подразумеваемая структура PVC: `crm/accounts.csv`, `crm/deals.csv`, `finance/...`; файлы должны быть доступны пользователю контейнера UID 65532.

```yaml
config:
  crmProvider: bitrix24
  bitrixBaseURL: https://crm.example.invalid
  bitrixFieldsFile: /etc/metis/bitrix-fields.json
  financeSourcesFile: /etc/metis/finance-sources.json
  financeSyncInterval: 24h
  # Только если используется соответствующий маппинг:
  # jiraFieldsFile: /etc/metis/jira-fields.json
  # worklogTeamsFile: /etc/metis/worklog-teams.json
integrationFiles:
  profileConfigMap: metis-connector-profiles
  dataExistingClaim: metis-finance-exports
secrets:
  existingSecret: metis-secrets
  envFromSecrets:
    - metis-finance-credentials
```

`metis-secrets` содержит ссылочные ключи `databaseUrl`, при включённых адаптерах — `jiraToken`, `webhookToken`, `confluenceToken`, `bitrixToken`. Дополнительный `metis-finance-credentials` должен содержать ключи с точными именами переменных из `username_env`/`password_env` JSON-профилей. Chart не создаёт эти Secret и не записывает их значения в values. ConfigMap также предоставляется развёртыванием. Для CRM-файлов выберите `crmProvider: csv`, задайте `crmDir: /data/crm`; при CRM API том с CSV не нужен.

В Compose конфигурация и расписания общие у API и worker; worker получает тот же каталог CSV, что и API. По умолчанию используются только синтетические CRM-фикстуры и существующие Jira/Confluence mock-сервисы; Bitrix и финансовый опрос отключены. Для подключения задайте параметры окружения развёртывания:

```dotenv
METIS_CONNECTOR_CONFIG_DIR=/absolute/deployment/connector-config
METIS_CONNECTOR_ENV_FILE=/absolute/deployment/secrets/finance.env
METIS_FINANCE_DATA_DIR=/absolute/deployment/finance-exports
METIS_FINANCE_SOURCES_FILE=/etc/metis/finance-sources.json
METIS_FINANCE_SYNC_INTERVAL=24h
METIS_CRM_PROVIDER=bitrix24
METIS_BITRIX_BASE_URL=https://crm.example.invalid
METIS_BITRIX_FIELDS_FILE=/etc/metis/bitrix-fields.json
```

`METIS_CONNECTOR_CONFIG_DIR` монтируется в `/etc/metis`, `METIS_FINANCE_DATA_DIR` — в `/data/finance`, оба только для чтения и в обоих процессах. `METIS_CONNECTOR_ENV_FILE` передаёт произвольные имена секретных переменных из профилей 1С. Bitrix/Jira/Confluence токены передаются отдельными переменными `METIS_BITRIX_TOKEN`, `METIS_JIRA_TOKEN`, `METIS_CONFLUENCE_TOKEN` из окружения хранилища секретов; для них явные Compose environment-поля имеют приоритет над env-файлом. Не сохраняйте рабочие секреты рядом с репозиторием и не публикуйте развёрнутый вывод `docker compose config` с реальными значениями.

Непустые пути и включённые provider требуют существующих корректных профилей. После изменения маппинга или учётных данных нужно перезапустить API и worker: параметры считываются при старте. Готовность шаблонов проверена `helm lint`/`helm template` и `docker compose config`; эти проверки не означают подключения к реальным 1С/Bitrix или проверки Kubernetes-кластера.
