"""Populate the modeling P&L through its real XLSX import API, using synthetic data only."""
import io
import json
import os
from pathlib import Path
import urllib.parse
import urllib.request
from xml.sax.saxutils import escape
import zipfile

root = Path(os.environ['METIS_ACCEPTANCE_TEMP'])
base = os.environ['METIS_ACCEPTANCE_URL'] + '/api/v1'
token = json.loads((root / 'tokens.json').read_text())['finance']
def api(method, route, data=None, content_type='application/json'):
    body = json.dumps(data).encode() if data is not None and content_type == 'application/json' else data
    request = urllib.request.Request(base + route, data=body, method=method,
                                     headers={'Authorization': 'Bearer ' + token, 'Content-Type': content_type})
    with urllib.request.urlopen(request) as response:
        return json.load(response)

teams = {team['key'] for team in api('GET', '/economics/teams')}
for key in ['syn_endpoint', 'syn_vulnerability', 'syn_platform']:
    if key not in teams:
        api('POST', '/economics/teams', {'key': key, 'name': key})
template = next(t for t in api('GET', '/economics/templates') if t['name'] == 'Финансы месяца')
rows = [['Продукт', 'Команда', 'Период', 'Статья', 'revenue', 'payroll', 'direct_costs', 'marketing', 'budget', 'certified_revenue'],
        ['edr', 'syn_endpoint', '2026-09', '', 6000000, 2400000, 200000, 300000, 5000000, 1000000],
        ['vm', 'syn_vulnerability', '2026-09', '', 3000000, 1200000, 100000, 100000, 2500000, 1000000],
        ['soar', 'syn_platform', '2026-09', '', 1000000, 600000, 50000, 10000, 800000, 0]]
xml_rows = []
for index, row in enumerate(rows, 1):
    cells = []
    for column, value in enumerate(row):
        ref = chr(65 + column) + str(index)
        cells.append(f'<c r="{ref}"><v>{value}</v></c>' if isinstance(value, int) else
                     f'<c r="{ref}" t="inlineStr"><is><t>{escape(value)}</t></is></c>')
    xml_rows.append(f'<row r="{index}">' + ''.join(cells) + '</row>')
stream = io.BytesIO()
with zipfile.ZipFile(stream, 'w', zipfile.ZIP_DEFLATED) as book:
    book.writestr('[Content_Types].xml', '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>')
    book.writestr('_rels/.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>')
    book.writestr('xl/workbook.xml', '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Финансы" sheetId="1" r:id="rId1"/></sheets></workbook>')
    book.writestr('xl/_rels/workbook.xml.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>')
    book.writestr('xl/worksheets/sheet1.xml', '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>' + ''.join(xml_rows) + '</sheetData></worksheet>')
query = urllib.parse.urlencode({'templateId': template['id'], 'period': '2026-09', 'fileName': 'synthetic-ds56.xlsx'})
result = api('POST', '/economics/imports?' + query, stream.getvalue(), 'application/octet-stream')
if result['batch'].get('errors'):
    raise RuntimeError('Synthetic finance import failed validation')
print('DS56 modeling P&L populated through real XLSX import API.')
