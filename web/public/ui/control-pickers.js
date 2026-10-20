(() => {
  'use strict';
  const controls=new WeakMap(),searches=new WeakMap();let active=null,serial=0;
  const el=(tag,cls,text)=>{const n=document.createElement(tag);if(cls)n.className=cls;if(text!=null)n.textContent=text;return n;};
  const action=(text,fn,label=text)=>{const b=el('button','picker-action',text);b.type='button';b.setAttribute('aria-label',label);b.onclick=fn;return b;};
  function close(restore=false){window.ModelInfo?.close();if(!active)return;const {panel,trigger}=active;active=active.parent;panel.dispose?.();panel.remove();trigger.setAttribute('aria-expanded','false');if(restore&&trigger.isConnected)trigger.focus();}
  function position(){const chain=[];for(let entry=active;entry;entry=entry.parent)chain.unshift(entry);for(const {panel,trigger} of chain){const r=(trigger.closest('.floating-field')||trigger).getBoundingClientRect();if(panel.classList.contains('select-popup'))panel.style.width=Math.min(Math.max(r.width,panel.classList.contains('model-select-popup')?600:200),innerWidth-24)+'px';panel.style.maxHeight=Math.min(panel.classList.contains('model-select-popup')?540:panel.classList.contains('select-popup')?320:innerHeight-24,innerHeight-24)+'px';const h=panel.getBoundingClientRect().height,w=panel.getBoundingClientRect().width;panel.style.left=Math.max(12,Math.min(r.left,innerWidth-w-12))+'px';panel.style.top=(r.bottom+8+h<=innerHeight-12?r.bottom+8:Math.max(12,r.top-h-8))+'px';}}
  // Outside clicks and Escape are handled below. Native auto-dismiss would close
  // the popup before a second trigger click, causing that click to reopen it.
  // Nested filters keep their parent picker open; dismissal unwinds one level.
  function show(trigger,panel){while(active&&!active.panel.contains(trigger))close();panel.classList.add('control-popup');panel.id='control-popup-'+(++serial);panel.setAttribute('popover','manual');(trigger.closest('dialog')||document.body).append(panel);active={trigger,panel,parent:active};trigger.setAttribute('aria-expanded','true');trigger.setAttribute('aria-controls',panel.id);if(panel.showPopover)panel.showPopover();panel.addEventListener('toggle',e=>{if(e.newState==='closed'&&active?.panel===panel)close();});position();}
  function changed(control){control.dispatchEvent(new Event('input',{bubbles:true}));control.dispatchEvent(new Event('change',{bubbles:true}));}
  document.addEventListener('pointerdown',e=>{if(window.ModelInfo?.contains(e.target))return;while(active&&!active.panel.contains(e.target)&&!active.trigger.contains(e.target))close();},true);
  document.addEventListener('keydown',e=>{if(active&&e.key==='Escape'){e.preventDefault();close(true);}});
  window.addEventListener('resize',position);document.addEventListener('scroll',e=>{if(active&&!active.panel.contains(e.target))position();},true);
  const labelFor=control=>control.getAttribute('aria-label')||control.closest('.floating-field')?.querySelector('.floating-label')?.textContent||'Choose value';
  function selectPicker(control,state){
    const {trigger}=state;if(control.disabled)return;if(active?.trigger===trigger){close(true);return;}
    if(control.multiple){multiSelectPicker(control,state);return;}
    if(control.hasAttribute('data-model-picker')){window.ModelPicker.open(control,state,{show,close,position,sync,changed,labelFor});return;}
    if(searches.has(control)){searchPicker(control,state,searches.get(control));return;}
    const panel=el('div','select-popup');panel.setAttribute('role','listbox');panel.setAttribute('aria-label',labelFor(control));panel.tabIndex=-1;
    panel.style.width=Math.min(Math.max(trigger.getBoundingClientRect().width,200),innerWidth-24)+'px';
    const options=[...control.options].filter(o=>!o.hidden&&!(o.parentElement.tagName==='OPTGROUP'&&o.parentElement.hidden));let current=-1;const items=[];
    const pick=i=>{if(!items[i]||items[i].disabled)return;control.value=options[i].value;close(true);sync(control);changed(control);};
    let group='';
    options.forEach((option,i)=>{const name=option.parentElement.tagName==='OPTGROUP'?option.parentElement.label:'';if(name&&name!==group)panel.append(el('div','picker-group',name));group=name;
      const item=el('div','picker-option',option.textContent);if(option.userIdentity){item.replaceChildren(UserUI.identity(option.userIdentity,{label:option.textContent}));item.setAttribute('aria-label',option.textContent);}item.id='picker-option-'+(++serial);item.setAttribute('role','option');item.setAttribute('aria-selected',String(option.selected));item.disabled=option.disabled||option.parentElement.disabled;item.setAttribute('aria-disabled',String(!!item.disabled));item.onclick=()=>pick(i);items.push(item);panel.append(item);if(option.selected&&!item.disabled)current=i;
    });
    function highlight(i){if(i<0||!items[i]||items[i].disabled)return;current=i;items.forEach((item,j)=>item.classList.toggle('highlighted',j===i));panel.setAttribute('aria-activedescendant',items[i].id);items[i].scrollIntoView({block:'nearest'});}
    const enabled=items.map((item,i)=>!item.disabled?i:-1).filter(i=>i>=0);let search='',lastType=0;
    panel.onkeydown=e=>{if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){e.preventDefault();let next=enabled.indexOf(current);next=e.key==='Home'?0:e.key==='End'?enabled.length-1:Math.max(0,Math.min(enabled.length-1,next+(e.key==='ArrowDown'?1:-1)));highlight(enabled[next]);}else if(e.key==='Enter'||e.key===' '){e.preventDefault();pick(current);}else if(e.key==='Tab'){close(true);}else if(e.key.length===1&&!e.ctrlKey&&!e.metaKey){search=Date.now()-lastType>700?e.key:search+e.key;lastType=Date.now();const match=enabled.find(i=>options[i].textContent.trim().toLowerCase().startsWith(search.toLowerCase()));if(match!=null)highlight(match);}};
    show(trigger,panel);panel.focus();highlight(current>=0?current:enabled[0]);
  }
  function searchPicker(control,{trigger},config){
    const panel=el('div','select-popup searchable-select-popup'),label=labelFor(control);
    panel.setAttribute('role','dialog');panel.setAttribute('aria-label',label+' choices');
    const field=el('label','',config.label||'Search'),search=el('input'),status=el('p','picker-search-status'),list=el('div','picker-search-options');
    search.type='search';search.maxLength=200;search.setAttribute('aria-label',config.label||'Search');field.append(search);
    status.setAttribute('role','status');list.setAttribute('role','listbox');list.setAttribute('aria-label',label);list.tabIndex=0;
    panel.append(field,status,list);let entries=[],current=-1,request=0,timer,disposed=false;
    function highlight(index,scroll=true){current=index;entries.forEach((entry,i)=>entry.node.classList.toggle('highlighted',i===index));if(entries[index]){list.setAttribute('aria-activedescendant',entries[index].node.id);if(scroll)entries[index].node.scrollIntoView({block:'nearest'});}else list.removeAttribute('aria-activedescendant');}
    function pick(index){const entry=entries[index];if(!entry||entry.option.disabled||control.disabled)return;const option=entry.option;let selected=[...control.options].find(o=>o.value===option.value);if(!selected){selected=option.cloneNode(true);selected.userIdentity=option.userIdentity;control.append(selected);}control.value=selected.value;close(true);sync(control);changed(control);}
    async function load(){
      const version=++request;entries=[];current=-1;list.replaceChildren();list.removeAttribute('aria-activedescendant');list.setAttribute('aria-busy','true');status.textContent='Searching…';position();
      try{const result=await config.load(search.value.trim());if(disposed||version!==request||!control.isConnected)return;
        for(const option of result.options.filter(o=>o.value&&!o.hidden)){
          const index=entries.length,item=el('div','picker-option',option.textContent);item.id=panel.id+'-option-'+index;item.setAttribute('role','option');item.setAttribute('aria-selected',String(option.value===control.value));item.setAttribute('aria-disabled',String(!!option.disabled));
          if(option.userIdentity){item.replaceChildren(UserUI.identity(option.userIdentity,{label:option.textContent}));item.setAttribute('aria-label',option.textContent);}item.onclick=()=>pick(index);list.append(item);entries.push({node:item,option});
        }
        status.textContent=result.message||(entries.length?'':config.empty||'No matches.');highlight(entries.findIndex(e=>!e.option.disabled&&e.option.value===control.value),false);
      }catch(error){if(disposed||version!==request)return;status.textContent=error.message||'Search failed. Try again.';list.append(action('Retry',load));}
      finally{if(!disposed&&version===request){list.removeAttribute('aria-busy');position();}}
    }
    search.oninput=()=>{clearTimeout(timer);request++;entries=[];current=-1;list.replaceChildren();list.removeAttribute('aria-activedescendant');status.textContent='Searching…';timer=setTimeout(load,200);};
    panel.dispose=()=>{disposed=true;request++;clearTimeout(timer);};
    panel.onkeydown=e=>{
      if(e.key==='Tab'){setTimeout(()=>{if(panel.isConnected&&!window.ControlPickers.contains(document.activeElement))close();},0);return;}
      if(e.target!==search&&e.target!==list)return;
      if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)&&(e.target!==search||e.key.startsWith('Arrow'))){e.preventDefault();const enabled=entries.map((v,i)=>v.option.disabled?-1:i).filter(i=>i>=0);let index=enabled.indexOf(current);index=e.key==='Home'?0:e.key==='End'?enabled.length-1:Math.max(0,Math.min(enabled.length-1,index+(e.key==='ArrowDown'?1:-1)));list.focus();highlight(enabled[index]);}
      else if(e.key==='Enter'||e.key===' '&&e.target===list){e.preventDefault();if(e.target===search){clearTimeout(timer);load();}else pick(current);}
    };
    show(trigger,panel);FormUI.refresh();search.focus();load();
  }
  function multiSelectPicker(control,{trigger}){
    const panel=el('div','select-popup multi-select-popup');panel.setAttribute('role','dialog');panel.setAttribute('aria-label',labelFor(control));
    const switches=[];
    for(const option of [...control.options].filter(o=>!o.hidden)){
      const label=el('label','multi-select-option'),input=el('input');input.type='checkbox';input.setAttribute('role','switch');input.checked=option.selected;
      label.append(input,el('span','',option.textContent));panel.append(label);switches.push({input,option});
      input.onchange=()=>{option.selected=input.checked;sync(control);changed(control);};
    }
    let focused=null;
    const refresh=()=>{if(control.disabled&&panel.contains(document.activeElement))focused=document.activeElement;for(const {input,option} of switches){input.checked=option.selected;input.disabled=control.disabled||option.disabled;}if(!control.disabled&&focused){if(document.activeElement===document.body&&panel.isConnected)focused.focus();focused=null;}};
    control.addEventListener('change',refresh);
    // The observer follows async locks and rollback without closing this popup.
    const observer=new MutationObserver(refresh);observer.observe(control,{attributes:true,subtree:true});
    panel.dispose=()=>{observer.disconnect();control.removeEventListener('change',refresh);};
    panel.onkeydown=e=>{const enabled=switches.map(s=>s.input).filter(i=>!i.disabled),index=enabled.indexOf(document.activeElement);if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){e.preventDefault();const next=e.key==='Home'?0:e.key==='End'?enabled.length-1:Math.max(0,Math.min(enabled.length-1,index+(e.key==='ArrowDown'?1:-1)));enabled[next]?.focus();}else if(e.key==='Tab'&&enabled.length){e.preventDefault();enabled[(index+(e.shiftKey?-1:1)+enabled.length)%enabled.length].focus();}};
    show(trigger,panel);refresh();switches.find(s=>!s.input.disabled)?.input.focus();
  }
  const iso=d=>`${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`;
  function datePicker(control,state){
    const {trigger}=state;if(control.disabled)return;if(active?.trigger===trigger){close(true);return;}
    const monthly=control.type==='month',today=new Date(),parsed=control.value?new Date(control.value+(monthly?'-01T12:00:00':'T12:00:00')):today;let shown=Number.isNaN(parsed.getTime())?today:parsed;
    shown=new Date(shown.getFullYear(),shown.getMonth(),1);const panel=el('div','calendar-popup');panel.setAttribute('role','dialog');panel.setAttribute('aria-label',labelFor(control));
    const valid=value=>(!control.min||value>=control.min)&&(!control.max||value<=control.max);
    const pick=value=>{if(value&&!valid(value))return;control.value=value;close(true);changed(control);};
    function render(focusValue){panel.replaceChildren();const header=el('div','calendar-header');header.append(action('‹',()=>{shown=new Date(shown.getFullYear()-(monthly?1:0),shown.getMonth()-(monthly?0:1),1);render();panel.querySelector('.calendar-header button').focus();},monthly?'Previous year':'Previous month'),el('strong','',monthly?String(shown.getFullYear()):shown.toLocaleDateString(undefined,{month:'long',year:'numeric'})),action('›',()=>{shown=new Date(shown.getFullYear()+(monthly?1:0),shown.getMonth()+(monthly?0:1),1);render();panel.querySelector('.calendar-header button:last-child').focus();},monthly?'Next year':'Next month'));panel.append(header);
      const grid=el('div',monthly?'calendar-months':'calendar-days');
      if(!monthly)for(let i=0;i<7;i++)grid.append(el('span','calendar-weekday',new Date(2026,0,5+i).toLocaleDateString(undefined,{weekday:'short'})));
      const start=new Date(shown.getFullYear(),shown.getMonth(),1-((shown.getDay()+6)%7));
      for(let i=0;i<(monthly?12:42);i++){const d=monthly?new Date(shown.getFullYear(),i,1):new Date(start.getFullYear(),start.getMonth(),start.getDate()+i),value=monthly?iso(d).slice(0,7):iso(d);const b=action(monthly?d.toLocaleDateString(undefined,{month:'short'}):String(d.getDate()),()=>pick(value),d.toLocaleDateString(undefined,monthly?{month:'long',year:'numeric'}:{weekday:'long',year:'numeric',month:'long',day:'numeric'}));b.dataset.date=value;b.disabled=!valid(value);b.classList.toggle('selected',value===control.value);b.classList.toggle('outside',!monthly&&d.getMonth()!==shown.getMonth());if(value===(monthly?iso(today).slice(0,7):iso(today)))b.setAttribute('aria-current','date');b.setAttribute('aria-pressed',String(value===control.value));grid.append(b);}
      panel.append(grid);const footer=el('div','calendar-footer');footer.append(action('Clear',()=>pick('')),action('Today',()=>pick(monthly?iso(today).slice(0,7):iso(today))));footer.lastChild.disabled=!valid(monthly?iso(today).slice(0,7):iso(today));panel.append(footer);position();if(focusValue)panel.querySelector(`[data-date="${focusValue}"]`)?.focus();
    }
    panel.onkeydown=e=>{if(e.key==='Tab'){const buttons=[...panel.querySelectorAll('button:not(:disabled)')],i=buttons.indexOf(document.activeElement);if((e.shiftKey&&i===0)||(!e.shiftKey&&i===buttons.length-1)){e.preventDefault();buttons[e.shiftKey?buttons.length-1:0].focus();}}const value=e.target.dataset.date;if(!value||!['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(e.key))return;e.preventDefault();const d=new Date(value+(monthly?'-01T12:00:00':'T12:00:00')),delta={ArrowLeft:-1,ArrowRight:1,ArrowUp:monthly?-3:-7,ArrowDown:monthly?3:7}[e.key];if(monthly)d.setMonth(d.getMonth()+delta);else d.setDate(d.getDate()+delta);shown=new Date(d.getFullYear(),d.getMonth(),1);render(monthly?iso(d).slice(0,7):iso(d));};
    show(trigger,panel);render();(panel.querySelector('button.selected:not(:disabled)')||panel.querySelector('[aria-current=date]:not(:disabled)')||panel.querySelector('button:not(:disabled)')).focus();
  }
  function sync(control){const state=controls.get(control);if(!state)return;const {trigger}=state;const disabled=control.disabled||!!control.closest('fieldset:disabled');if(trigger.disabled!==disabled)trigger.disabled=disabled;trigger.setAttribute('aria-label',labelFor(control));if(control.hasAttribute('data-model-picker')||control.multiple)trigger.setAttribute('aria-haspopup','dialog');if(control.tagName==='SELECT'&&state.text){const text=control.multiple?`${control.selectedOptions.length} selected`:control.selectedOptions[0]?.textContent||'';const person=control.multiple?null:control.selectedOptions[0]?.userIdentity,key=JSON.stringify([text,person||null]);if(state.identityKey!==key){state.identityKey=key;if(person)state.text.replaceChildren(UserUI.identity(person,{label:text}));else state.text.textContent=text;}control.closest('.floating-field')?.classList.toggle('raised',!!text||document.activeElement===trigger);}if(active?.trigger===trigger&&((!control.multiple&&trigger.disabled)||!control.isConnected))close();}
  function enhance(control,{trigger:customTrigger}={}){const wrap=control.closest('.floating-field');if((!wrap&&!customTrigger)||!(control.tagName==='SELECT'||['date','month'].includes(control.type)))return;if(controls.has(control)){sync(control);return;}
    const select=control.tagName==='SELECT',trigger=customTrigger||el('button',select?'select-trigger':'calendar-trigger');trigger.type='button';trigger.setAttribute('aria-expanded','false');trigger.setAttribute('aria-haspopup',select?'listbox':'dialog');const state={trigger};controls.set(control,state);
    if(select){if(!customTrigger){trigger.setAttribute('role','combobox');state.text=el('span','select-value');trigger.append(state.text,el('span','select-chevron'));wrap.classList.add('custom-select');}control.classList.add('picker-source');control.tabIndex=-1;control.setAttribute('aria-hidden','true');}else{trigger.innerHTML='<svg viewBox="0 0 20 20" fill="none" aria-hidden="true"><rect x="3" y="4" width="14" height="13" rx="2"/><path d="M3 8h14M6 2v4M14 2v4"/></svg>';wrap.classList.add('custom-date');}
    if(!customTrigger)wrap.append(trigger);trigger.onclick=()=>select?selectPicker(control,state):datePicker(control,state);trigger.onkeydown=e=>{if(e.key==='ArrowDown'||e.key==='ArrowUp'){e.preventDefault();select?selectPicker(control,state):datePicker(control,state);}};control.addEventListener('change',()=>sync(control));control.addEventListener('keydown',e=>{if(e.altKey&&e.key==='ArrowDown'){e.preventDefault();datePicker(control,state);}});sync(control);
  }
  window.ControlPickers={enhance,sync,search(control,config){searches.set(control,config);enhance(control);controls.get(control)?.trigger.setAttribute('aria-haspopup','dialog');},contains(node){for(let entry=active;entry;entry=entry.parent)if(entry.panel.contains(node)||entry.trigger.contains(node))return true;return false;}};
  new MutationObserver(()=>{while(active&&!active.trigger.isConnected)close();}).observe(document.body,{childList:true,subtree:true});
})();
