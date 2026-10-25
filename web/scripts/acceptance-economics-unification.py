"""Real API acceptance: two immutable histories become one current accounting source."""
import json
import os
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request

root = Path(os.environ['METIS_ACCEPTANCE_TEMP'])
base = os.environ['METIS_ACCEPTANCE_URL'] + '/api/v1'
tokens = json.loads((root / 'tokens.json').read_text())
period = '2026-09'

def api(method, route, body=None, role='finance', expected=200):
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(base + route, data=data, method=method, headers={'Authorization': 'Bearer ' + tokens[role], 'Content-Type': 'application/json'})
    try:
        response = urllib.request.urlopen(request, timeout=60)
    except urllib.error.HTTPError as error:
        response = error
    raw = response.read()
    if response.status != expected:
        raise AssertionError(f'{method} {route}: expected {expected}, got {response.status}: {raw[:1200]!r}')
    return json.loads(raw) if raw else None

products = api('GET', '/products')
product = next(p for p in products if p['key'] == 'edr')['id']
path = f'/products/{product}/economics/{period}'
# New independent model input receives two immutable data versions.
api('POST', '/economics/fields', {'key': 'history26', 'name': 'Synthetic legacy input', 'type': 'number', 'source': 'manual', 'effective_from': '2026-09-01'})
for value in ['101', '202']:
    api('POST', '/economics/fields/history26/manual-value', {'product_id': product, 'period': period, 'value': value}, expected=201)
old = api('GET', path)
old_report = api('GET', path + '/report?version=' + str(old['version']))
fields = old['fields'] + [{'key': 'history_extra26', 'name': 'Historical manual input', 'type': 'number', 'source': 'manual'}, {'key': 'history_metric26', 'name': 'Shared historical result', 'type': 'money', 'source': 'calculated', 'formula': 'direct_profit/3'}]
api('PUT', path + '/configuration', {'expected_version': old['version'], 'fields': fields, 'rows': [{'row_id': old['rows'][0]['id'], 'allocations': old['rows'][0]['allocations'], 'values': {'history_extra26': '3.125'}}]})
current = api('GET', path)
batches = api('GET', '/economics/imports?' + urllib.parse.urlencode({'period': period}))
model_version = max([b['data_version'] for b in batches if b['status'] == 'applied'] + [0])
plan_input = {'expected_book_version': current['version'], 'expected_model_version': model_version}
preview = api('POST', path + '/unification/preview', plan_input)
assert preview['book_versions'] >= 2 and preview['model_versions'] >= 2
assert 'history26' in preview['excluded_model_fields']
assert preview['previous_pnl'] and preview['canonical_pnl']
for role in ['finance-aggregates', 'finance-none', 'admin']:
    api('POST', path + '/unification/preview', plan_input, role=role, expected=403)
# A fresh book version makes the confirmed old plan stale.
api('PUT', path + '/configuration', {'expected_version': current['version'], 'fields': fields, 'rows': []})
api('POST', path + '/unification/apply', dict(plan_input, digest=preview['digest']), expected=409)
current = api('GET', path)
plan_input['expected_book_version'] = current['version']
preview = api('POST', path + '/unification/preview', plan_input)
confirmed = dict(plan_input, digest=preview['digest'])
applied = api('POST', path + '/unification/apply', confirmed)
assert applied['applied']
assert api('POST', path + '/unification/apply', confirmed)['applied']
status = api('GET', path + '/unification/status')
assert status['active']
archive = api('GET', path + '/unification/archive')
assert len(archive['book_versions']) >= 3 and len(archive['model_versions']) >= 2
assert archive['book_versions'][0]['id'] == old['id']
assert any(f['field_key'] == 'history26' and f['value'] == '101' for v in archive['model_versions'] for f in v['facts'])
report = api('GET', path + '/report')
pnl = api('GET', '/economics/pnl/products/' + product + '?' + urllib.parse.urlencode({'period': period}))
selected = next(p for p in report['products'] if p['product_id'] == product)
assert pnl['revenue']['amount'] == selected['revenue'] and pnl['loaded_profit']['amount'] == selected['loaded_profit']
assert api('GET', path + '/report?version=' + str(old['version']))['total']['metrics'] == old_report['total']['metrics']
# Current definitions are shared: change only the common constructor.
metric_key = preview['definition_mappings']['history_metric26']
api('POST', '/economics/metrics', {'key': metric_key, 'name': 'Shared current result', 'expression': 'round_minor(div16(field("revenue"),d("2")))', 'currency': 'RUB', 'effective_from': '2026-09-01'})
changed = api('GET', path + '/report')
assert changed['total']['metrics']['history_metric26'] == str(changed['total']['revenue'] // 2)
assert changed['total']['lineage']['history_metric26']['source_snapshots']
shared = api('GET', path + '/metrics/' + metric_key + '?' + urllib.parse.urlencode({'version': changed['version']}))
assert shared['metric']['value'] == changed['total']['metrics']['history_metric26']
# Published common manual fields now write into the canonical book, never independent batches.
manual = api('POST', '/economics/fields/history26/manual-value', {'product_id': product, 'period': period, 'value': '303'}, expected=201)
assert manual['value'] == '303' and manual['book_product_id'] == product
current = api('GET', path)
assert current['version'] == manual['data_version']
assert len(api('GET', '/economics/imports?' + urllib.parse.urlencode({'period': period}))) == len(batches)
fields = current['fields']
changed = api('GET', path + '/report')
api('PUT', path + '/configuration', {'expected_version': changed['version'], 'fields': fields + [{'key': 'divergence26', 'name': 'Forbidden second definition', 'type': 'number', 'source': 'manual'}], 'rows': []}, expected=400)
# One canonical book input edit is visible immediately through the old model API.
field_key = preview['definition_mappings']['history_extra26']
api('POST', '/economics/fields/' + field_key + '/manual-value', {'product_id': product, 'period': period, 'value': '9.75'}, expected=201)
api('POST', '/economics/metrics', {'key': 'current_input26', 'name': 'Live canonical input', 'expression': f'field("{field_key}")', 'effective_from': '2026-09-01'})
value = api('GET', '/economics/metrics/current_input26/explain?' + urllib.parse.urlencode({'period': period, 'productId': product}))
assert value['value'] == '9.75'
result = {'product_id': product, 'period': period, 'book_versions_archived': len(archive['book_versions']), 'model_versions_archived': len(archive['model_versions']), 'source_authority': 'book', 'stale_plan': 409, 'scope_denials': 3, 'replay': 'PASS', 'archive': 'PASS', 'old_report': 'PASS', 'common_definitions': 'PASS', 'live_book_inputs': 'PASS', 'canonical_manual_writes': 'PASS', 'divergent_definitions_rejected': 'PASS'}
(root / 'unification-acceptance.json').write_text(json.dumps(result, ensure_ascii=False, indent=2))
print(json.dumps(result, ensure_ascii=False))
