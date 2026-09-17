# Мок Confluence (WireMock)

Синтетические ответы Confluence Data Center REST без реальных имён и токенов. Покрывает порт
`KnowledgeBase` (`internal/adapters/confluence`): чтение страницы с метками и свойствами,
поиск по CQL, создание страницы ADR/discovery brief из шаблона, метки и page properties.

| Запрос | Ответ |
| --- | --- |
| `GET /rest/api/content/1001` | `__files/page_1001.json` — страница ADR-0001 |
| `GET /rest/api/content/1001/property` | `__files/page_1001_properties.json` |
| `GET /rest/api/content/1001/property/metis_decision_id` | существующее свойство (версия 1) |
| `GET /rest/api/content/{id}/property/{key}` (прочие) | 404 — свойства нет, адаптер создаёт его POST |
| `PUT /rest/api/content/{id}/property/{key}` | обновлённое свойство |
| `POST /rest/api/content/{id}/property` | `__files/property_created.json` |
| `POST /rest/api/content/{id}/label` | `__files/labels_added.json` |
| `GET /rest/api/content/search?cql=…label = "adr"…` | `__files/search_adr.json` — две страницы ADR |
| `POST /rest/api/content` | `__files/create_page.json` — созданная страница 2001 |

Сценарий приёмки этапа 2: платформа публикует `decisions.page.requested`, обработчик outbox
создаёт страницу (`POST /rest/api/content`), добавляет метки `metis`, `adr` и свойства
`metis_decision_id`, `metis_link_*`; `PageID` решения становится `2001`.
