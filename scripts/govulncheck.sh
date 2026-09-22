#!/usr/bin/env bash
# Запуск govulncheck с явным списком исключений (AGENTS.md: находка допускается только с ADR).
#
# Исключения перечислены в EXCLUDED и обоснованы в ADR-0005 (раздел «Уязвимости зависимостей»)
# и в docs/questions.md. Любая другая вызываемая из кода уязвимость роняет сборку.
set -euo pipefail

EXCLUDED=(
  # GO-2026-6452: паника excelize на специально собранной книге XLSX. Исправления у поставщика
  # библиотеки нет; разбор идёт через адаптер internal/adapters/financexlsx, который
  # перехватывает панику и возвращает ошибку валидации (вопрос №35).
  "GO-2026-6452"
)

report="$(mktemp)"
trap 'rm -f "$report"' EXIT

status=0
go run golang.org/x/vuln/cmd/govulncheck@latest -format json ./... > "$report" || status=$?
if [ "$status" -gt 1 ]; then
  echo "govulncheck завершился с ошибкой ($status)" >&2
  exit "$status"
fi

found=()
while IFS= read -r line; do
  [ -n "$line" ] && found+=("$line")
done < <(python3 "$(dirname "$0")/govulncheck_filter.py" "$report")

unexpected=()
for id in ${found[@]+"${found[@]}"}; do
  skip=""
  for allowed in "${EXCLUDED[@]}"; do
    [ "$id" = "$allowed" ] && skip=1
  done
  [ -z "$skip" ] && unexpected+=("$id")
done

if [ "${#unexpected[@]}" -gt 0 ]; then
  echo "govulncheck: уязвимости вне списка исключений: ${unexpected[*]}" >&2
  exit 1
fi
if [ "${#found[@]}" -gt 0 ]; then
  echo "govulncheck: находки из списка исключений (ADR-0005): ${found[*]}"
fi
echo "govulncheck: новых уязвимостей нет"
