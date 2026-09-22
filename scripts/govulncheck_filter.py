"""Фильтр отчёта govulncheck: печатает идентификаторы уязвимостей, вызываемых из кода.

Отчёт читается в формате `govulncheck -format json` — поток JSON-объектов. Находка попадает
в вывод, только если у неё есть трасса вызова из нашего кода: уязвимости в модулях, код
которых не вызывается, сборку не роняют.
"""

import json
import sys


def messages(text: str):
    decoder = json.JSONDecoder()
    index = 0
    while index < len(text):
        while index < len(text) and text[index].isspace():
            index += 1
        if index >= len(text):
            return
        message, index = decoder.raw_decode(text, index)
        yield message


def main(path: str) -> None:
    with open(path, encoding="utf-8") as report:
        text = report.read()
    ids = set()
    for message in messages(text):
        if not isinstance(message, dict):
            continue
        finding = message.get("finding")
        if not finding:
            continue
        trace = finding.get("trace") or []
        if trace and trace[0].get("function"):
            ids.add(finding["osv"])
    print("\n".join(sorted(ids)))


if __name__ == "__main__":
    main(sys.argv[1])
