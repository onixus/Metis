# Synthetic fixtures only; the isolated stand must be newly seeded.
import json,os,urllib.request
from pathlib import Path
base=os.environ.get('METIS_ACCEPTANCE_URL','http://127.0.0.1:18057')+'/api/v1';root=Path(os.environ['METIS_ACCEPTANCE_TEMP']);tokens=json.loads((root/'tokens.json').read_text())
with urllib.request.urlopen(urllib.request.Request(base+'/products',headers={'Authorization':'Bearer '+tokens['admin']})) as response: products=json.load(response)
ids={p['key']:p['id'] for p in products}
def api(method,path,body=None,role='admin'):
 data=None if body is None else json.dumps(body).encode();r=urllib.request.Request(base+path,data=data,method=method,headers={'Authorization':'Bearer '+tokens[role],'Content-Type':'application/json'})
 try:
  with urllib.request.urlopen(r) as res:return json.load(res)
 except urllib.error.HTTPError as e:raise RuntimeError(f'{method} {path}: {e.code} '+e.read().decode())
soar=ids['soar'];edr=ids['edr'];features=api('GET',f'/products/{soar}/features');feat=features[0]['id']
signal=api('POST',f'/products/{soar}/signals',{'source':'manual','text':'SYN-PRIVATE-DS56: экспорт событий для синтетического пилота','account_id':'SYN-ACCOUNT','deal_id':'SYN-DEAL','segment':'SYN-B2B','deal_amount':{'amount':1200000001,'currency':'RUB'},'account_arr':{'amount':29,'currency':'RUB'},'blocks_deal':True})
api('POST',f"/signals/{signal['id']}/link",{'feature_id':feat})
api('POST',f'/products/{soar}/signals',{'source':'manual','text':'Синтетический сигнал в очереди разбора','deal_amount':{'amount':100000,'currency':'RUB'}})
model=api('POST','/scoring-models',{'product_id':soar,'name':'DS56 RICE','type':'rice'})
api('PUT',f"/scoring-models/{model['id']}/features/{feat}/inputs",{'values':{'reach':'10','impact':'2','confidence':'0.8','effort':'4'}})
hyp=api('GET',f'/products/{edr}/hypotheses')[0]
interview=api('POST',f'/products/{edr}/interviews',{'date':'2026-10-23','account_id':'SYN-ACCOUNT','segment':'SYN-B2B','participants':['Синтетический собеседник'],'notes':'Синтетическое интервью для приёмки дизайн-системы','hypothesis_ids':[hyp['id']]})
insight=api('POST',f'/products/{edr}/insights',{'text':'Синтетический инсайт: оператору требуется экспорт','confidence':'high','interview_id':interview['id'],'hypothesis_ids':[hyp['id']]})
api('POST',f'/products/{edr}/evidence',{'source':'manual','date':'2026-10-23','trust':'high','verification':'verified','sha256':'ab'*32,'hypothesis_id':hyp['id'],'insight_id':insight['id']})
for audience,title in [('internal','SYN-PRIVATE-ROADMAP'),('sales_safe','Экспорт событий для пилота')]:
 api('POST',f'/products/{soar}/roadmap/items',{'title':title,'feature_id':feat,'bucket':'next','audience':audience,'start_date':'2026-11-01','end_date':'2026-12-01'})
decision=api('POST','/decisions',{'title':'Синтетическое портфельное решение DS56','context':'Согласовать экспорт событий для пилота','options':[{'key':'a','title':'Общий формат'},{'key':'b','title':'Отдельный формат'}],'chosen_key':'a','rationale':'Единая схема','links':[{'kind':'signal','id':signal['id']},{'kind':'feature','id':feat}]})
ids.update(feature=feat,signal=signal['id'],hypothesis=hyp['id'],insight=insight['id'],decision=decision['id']);(root/'ids.json').write_text(json.dumps(ids));print('Synthetic populated acceptance fixtures ready: signal/score/roadmap/discovery/decision; real API used.')
