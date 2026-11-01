#!/usr/bin/env python3
"""Rebuild reviewed metadata from downloaded official documentation. See docs/model-catalog.md."""
import json,re,calendar,sys
from copy import deepcopy
from pathlib import Path
from html.parser import HTMLParser
ROOT=Path(__file__).resolve().parents[2]
TMP=Path(sys.argv[1])
class Tables(HTMLParser):
 def __init__(self):super().__init__();self.tables=[];self.rows=None;self.cell=None;self.heading=None;self.context='';self.span={};self.col=0
 def handle_starttag(self,t,a):
  d=dict(a)
  if t in ('h2','h3','h4','h5'):self.heading=[]
  if t=='table':self.rows=[];self.span={};self.table_context=self.context
  if t=='tr':self.row=[];self.col=0
  if t in ('td','th'):self.cell=[];self.rowspan=int(d.get('rowspan',1))
 def handle_data(self,d):
  if self.cell is not None:self.cell.append(d)
  if self.heading is not None:self.heading.append(d)
 def handle_endtag(self,t):
  if t in ('h2','h3','h4','h5') and self.heading is not None:self.context=' '.join(''.join(self.heading).split());self.heading=None
  if t in ('td','th') and self.cell is not None:
   while self.col in self.span:
    value,n=self.span[self.col];self.row.append(value)
    if n==1:del self.span[self.col]
    else:self.span[self.col]=(value,n-1)
    self.col+=1
   value=' '.join(' '.join(self.cell).split());self.row.append(value)
   if self.rowspan>1:self.span[self.col]=(value,self.rowspan-1)
   self.col+=1;self.cell=None
  if t=='tr' and self.rows is not None:
   while self.col in self.span:
    value,n=self.span[self.col];self.row.append(value)
    if n==1:del self.span[self.col]
    else:self.span[self.col]=(value,n-1)
    self.col+=1
   self.rows.append(self.row)
  if t=='table' and self.rows is not None:self.tables.append((self.table_context,self.rows));self.rows=None
def tables(name):
 p=Tables();p.feed((TMP/name).read_text());return p.tables
catalog={p:{} for p in ('openai','alibaba','bytedance')}
def entry(p,id):
 return catalog[p].setdefault(id,dict(checked_at='2026-09-13',facts=[],sources=[]))
def fact(m,k,v):
 f=dict(label=k,value=v)
 if f not in m['facts']:m['facts'].append(f)
def source(m,k,u):
 s=dict(title=k,url=u)
 if s not in m['sources']:m['sources'].append(s)
def date(m,id):
 d=re.search(r'-(20\d{2}-\d{2}-\d{2})$',id)
 if d:m['version_date']=d[1]
docs=json.loads((TMP/'openai-docs.json').read_text())
for doc in docs:
 id,t=doc['id'],doc['text'];m=entry('openai',id)
 m['description']=re.sub(r'\[([^]]+)\]\([^)]+\)',r'\1',t.split('\n\n')[2])
 for line in t.splitlines():
  if not line.startswith('- ') or ': ' not in line:continue
  k,v=line[2:].split(': ',1)
  if k=='Current snapshot':m['snapshot']=v;date(m,v)
  elif k not in ('Model ID','Model type'):fact(m,'Provider: '+k,v)
 if id=='gpt-image-2.5-flare':m['description']='Fast, high-quality everyday image generation and editing.'
 if id=='gpt-image-2.5-sunburst':m['description']='Image generation and editing, with a focus on editing precision.'
 source(m,'OpenAI model documentation','https://developers.openai.com/api/docs/models/'+id)
 source(m,'OpenAI pricing','https://developers.openai.com/api/docs/pricing')
 if 'image' in id:source(m,'Image cost calculation','https://developers.openai.com/api/docs/guides/image-generation#calculating-costs')
 if id=='gpt-image-1':m['shutdown_date']='2026-10-23'
 if id in ('gpt-image-1.5','gpt-image-1-mini','chatgpt-image-latest'):m['shutdown_date']='2026-12-01'
 if id in ('gpt-audio','gpt-audio-mini'):m['shutdown_date']='2027-01-20'
 if id in ('tts-1','tts-1-hd','gpt-4o-mini-tts-2025-03-20','gpt-4o-mini-tts-2025-12-15'):m['shutdown_date']='2027-01-06'
 if m.get('shutdown_date'):source(m,'Deprecations','https://developers.openai.com/api/docs/deprecations')
month=year=day=None
for line in (TMP/'openai-changelog.md').read_text().splitlines():
 h=re.match(r'## (\w+), (\d{4})',line)
 if h:month=list(calendar.month_name).index(h[1]);year=int(h[2])
 h=re.match(r'### \w+ (\d+)',line)
 if h:day=int(h[1])
 if year and month and day and re.search(r'\b(released|launching|introducing|launched)\b',line,re.I):
  for id,m in catalog['openai'].items():
   if re.search(r'(?<![\w-])'+re.escape(id)+r'(?![\w.-])',line):
    d=f'{year:04}-{month:02}-{day:02}'
    if d<m.get('release_date','9999'):m['release_date']=d;source(m,'Release announcement','https://developers.openai.com/api/docs/changelog')
text=(ROOT/'plugins/provider/alibaba/alibaba.go').read_text()
part=text[text.index('func mediaModels('):text.index('func configureInputCapabilities')]
ids=set(re.findall(r'"((?:qwen|wan|happyhorse|fun-music|z-image)[a-z0-9.*-]+)"',part))
price_tables=tables('alibaba-pricing.html')
for id in sorted(ids):
 m=entry('alibaba',id);date(m,id)
 m['description']=('Image generation and editing.' if 'image' in id or 't2i' in id or 'i2i' in id else 'Text-to-speech synthesis.' if 'tts' in id else 'Music generation from a description or lyrics.' if 'music' in id else 'Video generation from text and supported references.')
 source(m,'Alibaba model pricing','https://www.alibabacloud.com/help/en/model-studio/model-pricing')
 matched=False
 for context,rows in price_tables:
  if not rows:continue
  headers=rows[0]
  for row in rows[1:]:
   if not row or not row[0] or row[0].split()[0]!=id:continue
   matched=True
   equivalent=re.search(r'equivalent to ([a-z0-9.-]+)',row[0])
   if equivalent:m['snapshot']=equivalent[1];date(m,equivalent[1])
   fields=[]
   for i,v in enumerate(row[1:],1):
    h=headers[i] if i<len(headers) else ''
    if 'quota' in h.lower():continue
    fields.append(h+': '+v)
   fact(m,'Published tariff — '+(context or 'see deployment scope'),'; '.join(fields))
 if not matched:fact(m,'Pricing audit','No matching current public tariff was found for this exact model ID. No price is inferred from a similarly named model.')
for context,rows in tables('alibaba-images.html'):
 if not rows:continue
 for row in rows[1:]:
  if not row or row[0] not in catalog['alibaba']:continue
  m=catalog['alibaba'][row[0]]
  for h,v in zip(rows[0][1:],row[1:]):
   if h.lower() in ('strengths','best for','use cases'):m['description']=v
   else:fact(m,'Provider: '+h,v)
  source(m,'Alibaba image capabilities','https://www.alibabacloud.com/help/en/model-studio/image-model')
byte=(TMP/'byte-content.txt').read_text().replace('\\-','-')
for line in byte.splitlines():
 if not line.startswith('|'):continue
 cells=line.strip('|').split('|');match=re.match(r'((?:dreamina-seedance|seedance|dola-seedream|seedream)[a-z0-9-]+)',cells[0])
 if not match:continue
 id=match[1];m=entry('bytedance',id)
 d=re.search(r'-(\d{6})$',id)
 if d:m['version_date']='20'+d[1][:2]+'-'+d[1][2:4]+'-'+d[1][4:]
 m['description']='Image generation and editing with reference images.' if 'seedream' in id else 'Video generation from text and supported references.'
 if '2-5' in id:m['description']='Long-form video storytelling with multiple reference assets.'
 if '5-0-pro' in id:m['description']='Image generation and editing with visual reasoning.'
 clean=lambda s: ' '.join(re.sub(r'\[([^]]+)\]\([^)]+\)',r'\1',re.sub('<[^>]*>',' ',s)).replace('**','').replace('* ','').replace(chr(96),'').split())
 if 'seedream' in id:
  fact(m,'Published input image tariff (USD)',clean(cells[1]));fact(m,'Published output image tariff (USD)',clean(cells[2]))
 else:
  fact(m,'Published standard tariff (USD / million tokens)',clean(cells[1]))
  fact(m,'Published offline tariff (USD / million tokens)',clean(cells[2]))
  fact(m,'Video billing','Estimated tokens = (input video seconds + output video seconds) × output width × height × FPS / 1024. Video inputs have separate rates and minimum token charges. Actual completion_tokens governs metered usage.')
 source(m,'BytePlus model pricing','https://docs.byteplus.com/en/docs/modelark/1544106')
 source(m,'BytePlus models','https://ai.byteplus.com/en/model')
 if '2-0-mini' in id:fact(m,'Temporary promotion','60% off standard online rates, 480p/720p, August 7–October 7, 2026. Estimates use standard list rates, excluding temporary promotions; see source for exact eligibility and times.')
 if '2-0-fast' in id:fact(m,'Temporary promotion','25% off standard online rates, 480p/720p, August 7–October 7, 2026. Estimates use standard list rates, excluding temporary promotions; see source for exact eligibility and times.')
 if '2-5' in id:fact(m,'Temporary promotion','28% off standard online rates, 1080p only, August 14–September 17, 2026. Estimates use standard list rates, excluding temporary promotions; see source for exact eligibility and times.')
for id in ('seedance-1-0-lite-t2v-250428','seedance-1-0-lite-i2v-250428','seedream-5-0-260128'):
 m=entry('bytedance',id);m['description']='Legacy model ID; current public pricing is not confirmed.'
 fact(m,'Pricing audit','This exact ID is absent from the current BytePlus price list. A tariff for another ID is not substituted.')
 source(m,'BytePlus model pricing','https://docs.byteplus.com/en/docs/modelark/1544106')
 d=id[-6:];m['version_date']='20'+d[:2]+'-'+d[2:4]+'-'+d[4:]
for region, rows in tables('alibaba-releases.html'):
 if region not in ('Singapore','China (Beijing)'):continue
 for row in rows:
  if len(row)<4 or not re.fullmatch(r'20\d{2}-\d{2}-\d{2}',row[1]):continue
  modelcol=3 if region=='Singapore' else 2
  for id in row[modelcol].split():
   if id not in catalog['alibaba']:continue
   m=catalog['alibaba'][id]
   m['release_date']=min(m.get('release_date','9999'),row[1])
   source(m,'Alibaba model release history','https://www.alibabacloud.com/help/en/model-studio/newly-released-models')
   if len(row)>modelcol+1:
    description=row[modelcol+1]
    if m['description'] in ('Image generation and editing.','Text-to-speech synthesis.','Music generation from a description or lyrics.','Video generation from text and supported references.'):
     m['description']=description.split('. ')[0]+'.'
    fact(m,'Release notes ('+region+', '+row[1]+')',description)
# Avoid assigning an unconfirmed latest date to aliases that do not point there.
for line in (TMP/'byte-catalog.md').read_text().replace('\\-','-').splitlines():
 if not line.startswith('|['):continue
 cells=line.strip('|').split('|');match=re.match(r'\[([^]]+)\]',cells[0])
 if not match or match[1] not in catalog['bytedance']:continue
 m=catalog['bytedance'][match[1]]
 labels=['Provider: reference workflows','Provider: default max requests/minute (account-specific limits may differ)'] if 'seedream' in match[1] else ['Provider: reference workflows','Provider: output specifications / limits','Provider: default rate limits (account-specific limits may differ)']
 for label,value in zip(labels,cells[1:]):fact(m,label,clean(value))
 source(m,'BytePlus model catalogue','https://docs.byteplus.com/en/docs/modelark/1330310')
 if match[1]=='seedream-5-0-260128':
  m['description']='Image generation and editing; the provider catalogue also lists the Seedream 5.0 Lite ID for this model.'
  m['facts']=[f for f in m['facts'] if f['label']!='Pricing audit']
  fact(m,'Published output image tariff (USD)','0.035 per generated image; confirmed Lite alias in the provider model catalogue.')
  fact(m,'Documented equivalent ID','seedream-5-0-lite-260128')
for code in ['1159178','2172655']:
 month=''
 for line in (TMP/('byte-releases-'+code+'.md')).read_text().replace('\\-','-').splitlines():
  h=re.fullmatch(r'# (20\d{2})(\d{2})',line)
  if h:month=h[1]+'-'+h[2]
  if not month or not line.startswith('|'):continue
  cells=line.strip('|').split('|')
  if len(cells)<5:continue
  id=cells[1].strip().replace('1.0','1-0').removeprefix('bytedance-')
  if len(cells)>5 and re.fullmatch(r'\d{6}',cells[3].strip()):id+='-'+cells[3].strip()
  if id not in catalog['bytedance']:continue
  m=catalog['bytedance'][id];m['release_date']=month
  fact(m,'Release notes',clean(cells[-1]));source(m,'BytePlus release history','https://docs.byteplus.com/en/docs/modelark/'+code)
# Release announcements outside the developer changelog's retained history.
for id,d,url in [
 ('gpt-image-1','2025-04-23','https://openai.com/index/image-generation-api/'),
 ('gpt-4o-mini-tts','2025-03-20','https://openai.com/index/introducing-our-next-generation-audio-models/'),
 ('tts-1','2023-11-06','https://openai.com/index/new-models-and-developer-products-announced-at-devday/'),
 ('tts-1-hd','2023-11-06','https://openai.com/index/new-models-and-developer-products-announced-at-devday/')]:
 m=catalog['openai'][id];m['release_date']=d;source(m,'Release announcement',url)
# Exact, reviewed retirements must not be inherited by a live family alias.
for id,base,shutdown in [('gpt-image-1','gpt-image-1','2026-10-23'),('gpt-audio-mini-2025-10-06','gpt-audio-mini','2026-07-23')]:
 if id not in catalog['openai']:
  catalog['openai'][id]=deepcopy(catalog['openai'][base])
  catalog['openai'][id]['snapshot']=id
  date(catalog['openai'][id],id)
 m=catalog['openai'][id]
 m['shutdown_date']=shutdown
 m['checked_at']='2026-10-24'
 fact(m,'Lifecycle status','Retired by provider')
 source(m,'Deprecations','https://developers.openai.com/api/docs/deprecations')
(ROOT/'internal/modelcatalog/catalog.json').write_text(json.dumps(catalog,indent=2,ensure_ascii=False)+'\n')
print({p:len(v) for p,v in catalog.items()})
