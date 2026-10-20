(() => {
 'use strict';
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const number=n=>new Intl.NumberFormat('en-US',{maximumFractionDigits:0}).format(n);
 const usd=n=>n==null?'—':new Intl.NumberFormat('en-US',{style:'currency',currency:'USD',minimumFractionDigits:2,maximumFractionDigits:6}).format(n);
 const shortUSD=n=>new Intl.NumberFormat('en-US',{style:'currency',currency:'USD',notation:'compact',maximumFractionDigits:1}).format(n);
 const date=(s,year=true)=>new Intl.DateTimeFormat('en-GB',{day:'numeric',month:'short',...(year?{year:'numeric'}:{}),timeZone:'UTC'}).format(new Date(s+'T00:00:00Z'));
 const period=(p,year=true)=>p.from===p.to?date(p.from,year):date(p.from,year)+' – '+date(p.to,year);
 function mount(parent){
  const root=el('div',null,'report-summary');parent.append(root);
  let serial=0,lastKey='',endpoint='',query=null,data=null,interval='day',chartMetric='spend',modelMode='public',top=5,showZero=false,showUnknown=false,showYear=false;
  const cards=el('section',null,'report-metrics');cards.setAttribute('aria-label','Usage metrics');
  const notice=el('p',null,'report-summary-notice');notice.setAttribute('role','status');notice.hidden=true;
  function section(title){const card=el('section',null,'report-summary-card'),heading=el('div',null,'report-chart-heading'),h=el('h2',title);heading.append(h);card.append(heading);root.append(card);return {card,heading,h};}
  root.append(cards,notice);
  const spend=section('Daily Spend'),spendChart=el('div');spend.card.append(spendChart);
  function choices(heading,label,values,selected,onChange){const group=el('div',null,'report-chart-options');group.setAttribute('role','group');group.setAttribute('aria-label',label);for(const [value,name]of values){const button=el('button',name,'secondary');button.type='button';button.setAttribute('aria-pressed',String(value===selected));button.onclick=()=>{for(const b of group.children)b.setAttribute('aria-pressed',String(b===button));onChange(value);};group.append(button);}heading.append(group);return group;}
  toggle(spend.heading,'Year label','',value=>{showYear=value;if(data)drawSpend();});
  const commercial=typeof systemAdministrator!=='undefined'&&(systemAdministrator||clientManager);
  if(commercial)choices(spend.heading,'Chart metric',[['spend','Spend'],['profit','Profit']],chartMetric,value=>{chartMetric=value;drawSpend();});
  choices(spend.heading,'Spend interval',[['day','Day'],['week','Week'],['month','Month']],interval,value=>{interval=value;spend.h.textContent={day:'Daily Spend',week:'Weekly Spend',month:'Monthly Spend'}[value];if(query)load(endpoint,query,true);});
  const models=section('Top models'),modelChart=el('div');models.card.append(modelChart);
  const modelControls=el('div',null,'report-model-controls');models.heading.append(modelControls);
  function dropdown(label,values,selected,onChange){const field=el('label',label),select=el('select');select.setAttribute('aria-label',label);for(const [value,name]of values)select.append(new Option(name,value));select.value=String(selected);select.onchange=()=>onChange(select.value);field.append(select);modelControls.append(field);}
  dropdown('Show top',[[5,'5'],[10,'10'],[25,'25'],[50,'50']],top,value=>{top=Number(value);drawModels();});
  dropdown('Model names',[['public','Public name'],['provider','Provider model']],modelMode,value=>{modelMode=value;drawModels();});
  FormUI.refresh();
  const providers=section('Spend by provider'),providerContent=el('div',null,'report-provider-layout');providers.card.append(providerContent);
  const switches=el('div',null,'report-chart-switches');providers.heading.append(switches);
  function toggle(parent,text,help,change){const label=el('label',null,'report-chart-switch'),input=el('input');input.type='checkbox';input.setAttribute('aria-label',text);label.append(el('span',text),input);if(help)label.title=help;input.onchange=()=>change(input.checked);parent.append(label);}
  toggle(switches,'Show zero spend','Include providers with no known charges.',value=>{showZero=value;drawProviders();});
  toggle(switches,'Show unknown','Include requests without an identified provider.',value=>{showUnknown=value;drawProviders();});
  const blank=(target,text)=>target.replaceChildren(el('p',text,'report-chart-empty'));
  function tooltip(node,text){node.tabIndex=0;node.setAttribute('aria-label',text);node.append(el('span',text,'report-chart-tooltip'));}
  function drawMetrics(){
   cards.replaceChildren();
   for(const [label,value,cls,detail]of [
    ...(data.profit_usd==null?[]:[['Profit',usd(data.profit_usd),'',`${number(data.pending_profit_requests||0)} requests awaiting verification/cost`]]),
    ['Total requests',number(data.requests),'',null],
    ['Successful requests',number(data.successful),'report-success',null],
    ['Failed requests',number(data.failed),'operation-error',null],
    ['Average cost per request',usd(data.average_cost_usd),'',data.priced_requests<data.requests?'Known costs only':null],
    ['Total tokens',data.token_requests?number(data.tokens):'—','',data.token_requests<data.requests?`Reported for ${number(data.token_requests)} of ${number(data.requests)} requests`:null]
   ]){const card=el('div',null,'report-metric');card.append(el('h3',label),el('strong',value,cls));if(detail)card.append(el('small',detail));cards.append(card);}
   const missing=data.requests-data.priced_requests;notice.hidden=!missing;notice.textContent=missing?`Cost pending for ${number(missing)} requests. Totals include known charges.`:'';
  }
  function drawSpend(){
   if(!data)return;
   spend.h.textContent=({day:'Daily',week:'Weekly',month:'Monthly'}[interval])+' '+(chartMetric==='profit'?'Profit':'Spend');
   spendChart.replaceChildren();const points=data.periods,metric=chartMetric==='profit'?'profit_usd':'spend_usd',max=Math.max(...points.map(p=>p[metric]||0),0),min=Math.min(...points.map(p=>p[metric]||0),0);
   if(!points.length){blank(spendChart,'No requests in this period.');return;}
   const layout=el('div',null,'report-spend-layout'),axis=el('div',null,'report-spend-axis'),scroll=el('div',null,'report-spend-scroll'),bars=el('div',null,'report-spend-bars'+(showYear?' with-year':''));
   for(const part of [1,.75,.5,.25,0])axis.append(el('span',shortUSD(min+(max-min)*part)));
   const scale=max-min||1;
   for(const p of points){const item=el('div',null,'report-spend-item'),track=el('div',null,'report-spend-track'),bar=el('div',null,'report-spend-bar');const amount=p[metric]||0;bar.style.height=(100*Math.abs(amount)/scale)+'%';bar.style.position='absolute';bar.style.bottom=(100*(Math.min(0,amount)-min)/scale)+'%';if(amount<0)bar.style.background='var(--red)';
    const label=period(p,showYear),value=chartMetric==='profit'?`${usd(p.profit_usd||0)} · ${number(p.pending_profit_requests||0)} pending`:p.priced_requests===0&&p.requests>0?'Cost pending':usd(p.spend_usd);
    tooltip(track,`${period(p)}: ${value} · ${number(p.requests)} requests`);track.prepend(bar);item.append(track,el('span',label,'report-spend-date'));bars.append(item);
   }
   scroll.append(bars);layout.append(axis,scroll);spendChart.append(layout);
  }
  function drawModels(){if(!data)return;modelChart.replaceChildren();const rows=(modelMode==='public'?data.public_models:data.provider_models).slice(0,top);if(!rows.length){blank(modelChart,'No requests in this period.');return;}
   const list=el('ol',null,'report-model-bars'),max=Math.max(...rows.map(r=>r.spend_usd),0)||1;
   for(const row of rows){const li=el('li'),name=modelMode==='public'?row.name:row.provider+'/'+row.name,label=el('span',name||'Unknown','report-model-name'),track=el('div',null,'report-model-track'),bar=el('div',null,'report-model-bar');bar.style.width=(100*Math.max(0,row.spend_usd)/max)+'%';label.title=name;const value=row.priced_requests?usd(row.spend_usd):'Pending';tooltip(track,`${name}: ${value} · ${number(row.requests)} requests`);track.prepend(bar);li.append(label,track,el('span',value,'report-chart-value'));list.append(li);}
   modelChart.append(list);
  }
  function drawProviders(){if(!data)return;providerContent.replaceChildren();const rows=data.providers.filter(p=>(showUnknown||p.name)&&(showZero||p.spend_usd>0||p.priced_requests<p.requests));
   if(!rows.length){blank(providerContent,data.providers.length?'No providers match these options.':'No requests in this period.');return;}
   const total=rows.reduce((n,r)=>n+r.spend_usd,0),donut=el('div',null,'report-donut'),segments=[];let at=0;
   const colors=['var(--chart-1,var(--accent))','var(--chart-2)','var(--chart-3)','var(--chart-4)','var(--chart-5)','var(--chart-6)'];
   rows.forEach((row,i)=>{const next=at+(total>0?row.spend_usd/total*100:0);if(next>at)segments.push(`${colors[i%colors.length]} ${at}% ${next}%`);at=next;});
   donut.style.background=segments.length?'conic-gradient('+segments.join(',')+')':'var(--line)';donut.setAttribute('role','img');donut.setAttribute('aria-label',`Spend by provider: ${usd(total)}. Breakdown in the adjacent table.`);donut.append(el('strong',rows.some(r=>r.priced_requests)?usd(total):'—'));
   const wrap=el('div',null,'docs-table-wrap'),table=el('table',null,'docs-table'),head=el('thead'),tr=el('tr'),body=el('tbody');
   for(const text of ['Provider','Spend','Successful','Failed','Tokens']){const th=el('th',text);th.scope='col';tr.append(th);}head.append(tr);
   rows.forEach((row,i)=>{const tr=el('tr'),name=el('td'),dot=el('span',null,'report-provider-dot');dot.style.background=colors[i%colors.length];name.append(dot,document.createTextNode(row.name||'Unknown'));tr.append(name,el('td',row.priced_requests?usd(row.spend_usd):'Pending'),el('td',number(row.successful),'report-success'),el('td',number(row.failed),row.failed?'operation-error':''),el('td',row.token_requests?number(row.tokens):'—'));body.append(tr);});
   table.append(head,body);wrap.append(table);providerContent.append(donut,wrap);
  }
  async function load(nextEndpoint,nextQuery,force=false){
   endpoint=nextEndpoint;query=new URLSearchParams(nextQuery);query.delete('offset');query.delete('limit');query.delete('format');query.set('view','summary');query.set('interval',interval);
   const key=endpoint+query.toString();if(!force&&key===lastKey)return;lastKey=key;const current=++serial;data=null;notice.hidden=false;FormUI.message(notice,'Loading summary…');root.setAttribute('aria-busy','true');
   for(const target of [cards,spendChart,modelChart,providerContent])target.replaceChildren();
   try{const result=await api(key);if(current!==serial||!root.isConnected)return;data=result;FormUI.message(notice,'');drawMetrics();drawSpend();drawModels();drawProviders();}
   catch(error){if(current!==serial||!root.isConnected)return;lastKey='';FormUI.message(notice,'Could not load summary: '+error.message,'error');}
   finally{if(current===serial)root.removeAttribute('aria-busy');}
  }
  return {load};
 }
 window.ReportSummary={mount};
})();
