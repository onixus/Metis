# APEX product templates for Metis

Дата baseline: **2026-09-22**.

Этот набор связывает Metis с каноническим реестром APEX, не смешивая его с
демонстрационными портфелями EDR/SOAR/VM и Infrastructure. При
`METIS_SEED=true` Metis создаёт восемь участников APEX и четыре ключевых
workstream-фичи для каждого.

## Как считаются цифры

Это плановая оценка, а не утверждение о production readiness.

Вес статуса:

| status | weight |
|---|---:|
| `done` | 100 |
| `in_progress` | 60 |
| `planned` | 20 |
| `discovery` | 10 |
| `idea` | 0 |

**Completion %** = среднее весов workstream-фич продукта.

**Remaining effort** = сумма относительных engineering points всех workstream,
которые ещё не `done`.

**Confidence %** = средневзвешенная по effort уверенность для незавершённых
workstream.

Engineering points нужны для сравнительного планирования между продуктами. Это
не человеко-дни и не обещание даты. При появлении бенчей, релизов, закрытых
issues или новых roadmap-решений baseline следует пересчитать.

## Текущий расчёт

| Product | Repo | Completion | Remaining effort | Confidence |
|---|---|---:|---:|---:|
| APEX Gateway | `onixus/unified-platform` | 60% | 47 pts | 73% |
| Shapoclyack | `onixus/Shapoclyack` | 70% | 21 pts | 75% |
| Lariska | `onixus/Lariska` | 40% | 55 pts | 69% |
| Ferrum | `onixus/Ferrum` | 40% | 42 pts | 74% |
| BSDM-Proxy | `onixus/bsdm-proxy` | 50% | 34 pts | 81% |
| Oko-Ra | `onixus/Oko-Ra` | 40% | 76 pts | 66% |
| Pulse | `onixus/GenDec` | 60% | 29 pts | 85% |
| Asmodeus | `onixus/Asmodeus` | 40% | 55 pts | 79% |

## Основание baseline

Шаблоны опираются на уже существующие границы и планы репозиториев:

- **APEX Gateway**: Architecture Contract v1 уже существует и проверяется CI;
  Gateway/Web Console реализованы, но README всё ещё фиксирует demo-auth и
  in-memory control state. Cross-repository conformance обозначен следующим
  шагом контракта.
- **Shapoclyack**: продукт уже имеет EASM/CAASM/RBVM ядро; оставшаяся оценка
  сосредоточена на APEX-boundary и воспроизводимом performance baseline.
- **Lariska**: канонический контракт закрепляет владение endpoint inventory и
  local agent state; дальнейшие workstream покрывают telemetry, event boundary
  и fleet hardening.
- **Ferrum**: admission/controller/agent MVP реализован, а ROADMAP/issues явно
  оставляют first tagged release, cosign/SBOM, kind/k3d e2e и BPF LSM.
- **BSDM-Proxy**: ядро HTTP/HTTPS proxy существует; текущий архитектурный долг
  связан с ростом функций вокруг одного request path, вынесением policy и
  изоляцией Kafka/ClickHouse analytics от hot path.
- **Oko-Ra**: контракт APEX задаёт world model, scenario graph и causal/risk
  projections как authoritative state; оценки оставшейся работы сфокусированы
  на ingestion/provenance и измеримой калибровке.
- **Pulse (GenDec)**: scanner core существует; текущий план включает
  refactoring точности/скорости, before/after benchmarks и APEX scan-observation
  boundary.
- **Asmodeus**: продукт ведётся как synthetic BAS/resilience engine; шаблон
  выделяет safety boundaries, Blue Team feedback metrics и APEX exercise-event
  contract.

## Что появляется в UI Metis

У каждого продукта создаётся capability вида:

`Portfolio baseline: <completion>% complete · <remaining> pts remaining · <confidence>% confidence`

Под ним создаются четыре workstream-фичи со статусами и плановыми датами.
Для каждой фичи requirement хранит effort, confidence и текстовое основание
оценки. Так baseline виден непосредственно из portfolio graph и не требует
отдельной таблицы, которая через две недели неизбежно стала бы ещё одним
источником истины.

Источник шаблонов и формул: `internal/seed/apex.go`.
