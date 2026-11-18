(() => {
 'use strict';
 const configs=new WeakMap(),mounted=new WeakMap(),live=new Set();
 const gear='<path d="m9 3-.7 2.3-2 .9-2.2-.9-2 3.4 1.6 1.8v2.3L2.1 15l2 3.4 2.2-.9 2 .9.7 2.6h4l.7-2.6 2-.9 2.2.9 2-3.4-1.6-2.2v-2.3l1.6-1.8-2-3.4-2.2.9-2-.9L13 3Z"/><circle cx="11" cy="12" r="3"/>';
 const expand='<path d="M9 5H3v6M3 5l6 6M15 5h6v6m0-6-6 6M9 19H3v-6m0 6 6-6M15 19h6v-6m0 6-6-6"/>';
 const collapse='<path d="M3 5l6 6m0-6v6H3M21 5l-6 6m0-6v6h6M3 19l6-6m0 6v-6H3M21 19l-6-6m0 6v-6h6"/>';
 const node=(tag,cls,text)=>{const n=document.createElement(tag);if(cls)n.className=cls;if(text!=null)n.textContent=text;return n;};
 function icon(button,path){if(button.dataset.tableIcon===path)return;button.dataset.tableIcon=path;button.innerHTML='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">'+path+'</svg>';}
 function attach(table,{id,columns,api,legacy=false,defaults,status,minWidths}={}){
  if(!id||!columns||columns.length<=3)return;
  configs.set(table,{id,columns,api,legacy,defaults,status,minWidths});table.dataset.consoleTable=id;
  if(table.isConnected)mount(table);
 }
 function mount(table){
  if(mounted.has(table))return;
  const config=configs.get(table);if(!config)return;
  const {id,columns,legacy}=config,wrap=table.parentElement,header=table.tHead?.rows[0],originalMinWidth=table.style.minWidth;if(!header)return;
  const originalHeaders=[...header.cells];if(originalHeaders.length!==columns.length)return;
  const actions=columns.indexOf('actions'),dataColumns=columns.filter(c=>c!=='actions');
  // The first column names the row (member, organization, key…), so it cannot be hidden. Requests keeps its released report_columns behavior.
  const locked=legacy?'':dataColumns[0];
  const labels=new Map(columns.map((id,i)=>{const copy=originalHeaders[i].cloneNode(true);copy.querySelectorAll('button,svg,.sr-only,select').forEach(n=>n.remove());return [id,copy.textContent.trim()||id.replaceAll('_',' ')];}));
  const cell=actions>=0?originalHeaders[actions]:node('th');cell.classList.add('report-columns-cell','table-controls-cell');cell.dataset.tableUiOwned=actions>=0?'header':'aux';cell.scope='col';if(actions<0)header.append(cell);
  const controls=node('span','table-controls'),choose=node('button','secondary report-columns-button'),wideButton=node('button','secondary report-columns-button');
  choose.type=wideButton.type='button';choose.setAttribute('aria-label',legacy?'Columns':'Columns for '+id.replaceAll('_',' '));choose.title='Choose columns';icon(choose,gear);
  wideButton.title='Expand table to viewport width';wideButton.setAttribute('aria-label','Expand table to viewport width');wideButton.setAttribute('aria-pressed','false');
  controls.append(choose,wideButton);cell.replaceChildren(controls);
  // Backing selects stay outside business forms, including descendant-based
  // custom snapshots. Only the two type=button triggers live in the table.
  const select=node('select');select.multiple=true;select.hidden=true;select.dataset.tableColumns=id;select.dataset.plainControl='';select.setAttribute('aria-label',legacy?'Columns':'Columns for '+id.replaceAll('_',' '));if(legacy)select.id='reportColumns';document.body.append(select);
  for(const column of dataColumns){const option=new Option(labels.get(column),column);option.disabled=column===locked;select.append(option);}
  ControlPickers.enhance(select,{trigger:choose});
  const status=config.status||node('span','table-preference-status');status.setAttribute('role','status');const recover=node('button','secondary table-recover','Show hidden fields');recover.type='button';recover.hidden=true;const feedback=node('div','table-preference-feedback');feedback.dataset.tableUiOwned='aux';feedback.dataset.tableFeedback=id;feedback.append(recover);if(!config.status)feedback.append(status);wrap.after(feedback);
  let saved=[],visible=[],wide=false,busy=false,forced=new Set(),stamp=GatewayProfile.context();
  const defaults=config.defaults||dataColumns;
  function preferences(){
   let pref=legacy?GatewayProfile.reportColumns():GatewayProfile.tableColumns(id);
   if(legacy&&pref?.includes('user'))pref=[...new Set(pref.map(c=>c==='user'?'api_key':c))];
   saved=pref||[];visible=dataColumns.some(c=>saved.includes(c))?dataColumns.filter(c=>c===locked||saved.includes(c)):defaults;
   wide=GatewayProfile.tableWide(id);stamp=GatewayProfile.context();
  }
  function message(text='',error=false){FormUI.message(status,text,error?'error':'');feedback.hidden=recover.hidden&&(!status.textContent||!!config.status);}
  function geometry(){
   wrap.classList.toggle('table-wide-surface',wide);wrap.style.removeProperty('width');wrap.style.removeProperty('margin-left');
   const popup=wrap.closest('.model-info-popup');if(popup){popup.classList.toggle('table-wide-popup',wide);window.ModelInfo?.reposition?.();return;}
   if(!wide||!wrap.getClientRects().length||wrap.closest('details:not([open]),[hidden]'))return;
   const normal=wrap.getBoundingClientRect(),gutter=innerWidth<=600?12:20;let left=gutter;
   for(const sidebar of document.querySelectorAll('.configuration-sidebar,.system-sidebar,.provider-sidebar,.report-donut')){
    const r=sidebar.getBoundingClientRect();if(r.width&&r.height&&r.right<=normal.left&&r.top<normal.bottom&&r.bottom>normal.top)left=Math.max(left,r.right+(parseFloat(getComputedStyle(sidebar.parentElement).columnGap)||20));
   }
   const width=Math.max(0,innerWidth-gutter-left);wrap.style.width=width+'px';wrap.style.marginLeft=(left-normal.left)+'px';
  }
  function apply(){
   if(!table.isConnected)return;if(!table.getClientRects().length||table.closest('details:not([open]),[hidden]'))ControlPickers.dismiss(choose);
   const shown=c=>c==='actions'||visible.includes(c)||forced.has(c);
   let hiddenEditable=false;
   for(const row of table.rows){
    if(row.parentElement.tagName==='THEAD'){
     columns.forEach((column,i)=>{const c=row.cells[i];if(c&&c!==cell){c.dataset.column=column;c.hidden=!shown(column);}});continue;
    }
    if(row.cells.length===1&&row.cells[0].colSpan>1){row.cells[0].colSpan=dataColumns.filter(shown).length+1;continue;}
    columns.forEach((column,i)=>{
     const c=row.cells[i];if(!c)return;c.dataset.column=column;c.hidden=!shown(column);
     if(c.hidden&&c.querySelector('input,select,textarea,button'))hiddenEditable=true;
    });
    if(actions<0&&!row.querySelector('[data-table-tools]')){const c=node('td');c.dataset.tableTools='';c.dataset.tableUiOwned='aux';row.append(c);}
   }
   recover.hidden=!hiddenEditable;feedback.hidden=recover.hidden&&(!status.textContent||!!config.status);
   for(const option of select.options)option.selected=visible.includes(option.value)||forced.has(option.value);
   select.disabled=busy;wideButton.disabled=busy;ControlPickers.sync(select);
   const all=visible.length===dataColumns.length&&!forced.size;table.style.minWidth=config.minWidths?(dataColumns.filter(shown).reduce((sum,c)=>sum+(config.minWidths[c]||130),104))+'px':all?originalMinWidth:Math.max(240,(visible.length+forced.size)*140+104)+'px';
   wideButton.setAttribute('aria-pressed',String(wide));wideButton.setAttribute('aria-label',wide?'Restore normal table width':'Expand table to viewport width');wideButton.title=wideButton.getAttribute('aria-label');icon(wideButton,wide?collapse:expand);geometry();
  }
  async function persist(input){
   const captured=stamp;busy=true;apply();message('Saving table settings…');
   try{await GatewayProfile.save(input,config.api||window.api);if(!table.isConnected||!GatewayProfile.sameContext(captured))return;preferences();forced.clear();message();}
   catch(error){if(table.isConnected&&GatewayProfile.sameContext(captured)){preferences();forced.clear();message(error.message,true);}}
   finally{busy=false;if(table.isConnected){if(!GatewayProfile.sameContext(captured)){preferences();forced.clear();message();}apply();}}
  }
  select.onchange=()=>{
   const next=[...select.options].filter(o=>o.selected||o.disabled).map(o=>o.value);
   if(!next.length){apply();message(legacy?'Keep at least one column visible.':'Keep at least one data column visible.',true);return;}
   visible=next;forced.clear();apply();
   const values=[...saved.filter(c=>!dataColumns.includes(c)),...next];
   persist(legacy?{report_columns:values}:{table_columns:{[id]:next}});
  };
  wideButton.onclick=()=>{if(busy)return;wide=!wide;apply();persist({table_wide:{[id]:wide}});};
  recover.onclick=()=>{dataColumns.forEach(c=>forced.add(c));apply();message('Hidden fields are shown for review. Your saved column selection is unchanged.');};
  function invalid(event){const c=event.target.closest('td');if(c?.hidden){forced.add(c.dataset.column);apply();message('A hidden field needs attention. Correct it before saving.',true);}}
  // Sticky header tools must not cover a keyboard-focused data-header action.
  // Scroll only this horizontal viewport; pointer clicks keep their coordinates.
  function revealHeader(event){
   const target=event.target;if(!header.contains(target)||cell.contains(target)||!target.matches(':focus-visible'))return;
   const r=target.getBoundingClientRect(),bounds=wrap.getBoundingClientRect(),edge=cell.getBoundingClientRect().left;
   if(r.right>edge-4||r.left<bounds.left+4)wrap.scrollLeft+=r.left-bounds.left-Math.max(4,(edge-bounds.left-r.width)/2);
  }
  table.addEventListener('focusin',revealHeader);
  table.addEventListener('invalid',invalid,true);
  const state={table,apply,refresh(){if(!busy||!GatewayProfile.sameContext(stamp)){preferences();forced.clear();apply();}},destroy(){ControlPickers.dismiss(choose);select.remove();feedback.remove();wrap.classList.remove('table-wide-surface');wrap.style.removeProperty('width');wrap.style.removeProperty('margin-left');wrap.closest('.model-info-popup')?.classList.remove('table-wide-popup');table.removeEventListener('invalid',invalid,true);table.removeEventListener('focusin',revealHeader);live.delete(state);mounted.delete(table);}};
  mounted.set(table,state);live.add(state);preferences();apply();
 }
 let scheduled=false;const pending=new Set();
 function schedule(table){if(table)pending.add(table);if(scheduled)return;scheduled=true;queueMicrotask(()=>{scheduled=false;for(const state of [...live])if(!state.table.isConnected)state.destroy();for(const table of pending)if(table.isConnected){mount(table);mounted.get(table)?.apply();}pending.clear();});}
 new MutationObserver(records=>{for(const record of records){if(record.removedNodes?.length)schedule();if(record.type==='attributes'){if(record.target.matches('table[data-console-table]'))schedule(record.target);if(!record.target.matches('td,th'))record.target.querySelectorAll('table[data-console-table]').forEach(schedule);continue;}const table=record.target.closest?.('table[data-console-table]');if(table)schedule(table);for(const n of record.addedNodes)if(n.nodeType===1){if(n.matches('table[data-console-table]'))schedule(n);n.querySelectorAll('table[data-console-table]').forEach(schedule);}}}).observe(document.body,{childList:true,subtree:true,attributes:true,attributeFilter:['hidden','open']});
 window.addEventListener('resize',()=>{for(const state of live)state.apply();});
 window.addEventListener('gateway-profile-changed',()=>{for(const state of live)state.refresh();});
 window.TableUI={attach,refresh:table=>mounted.get(table)?.apply()};
})();
