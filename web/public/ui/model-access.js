(() => {
 let formSerial=0;
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined&&text!==null)n.textContent=text;if(cls)n.className=cls;return n};
 const message=(n,text,error=false)=>{n.textContent=text;n.className=error?'error':'field-explanation'};
 async function provider(parent,provider,api,{current=()=>true}={}) {
  const token=Symbol();parent.modelAccessToken=token;
  const active=()=>current()&&parent.modelAccessToken===token;
  parent.replaceChildren(el('p','Loading model settings…','field-explanation'));
  try {
   const result=await api('/v1/system/model-settings');if(!active())return;
   const models=(result.data||[]).filter(m=>m.provider===provider.id && (provider.models||[]).some(native=>native.id===m.model));
   parent.replaceChildren();parent.classList.add('model-access-list');
   const toolbar=el('div',null,'model-access-toolbar'),search=el('input'),count=el('span',null,'model-access-count');
   search.type='search';search.setAttribute('aria-label','Search provider models');
   toolbar.append(search,count);parent.append(toolbar);
   const scroll=el('div',null,'model-access-scroll'),table=el('table',null,'docs-table model-access-table'),head=el('thead'),header=el('tr'),body=el('tbody');
   table.setAttribute('aria-label','Provider model settings');scroll.tabIndex=0;scroll.setAttribute('role','region');scroll.setAttribute('aria-label','Provider model settings table');
   for(const text of ['Model','Public API name','Available by default','']){const th=el('th',text);th.scope='col';header.append(th)}
   head.append(header);table.append(head,body);scroll.append(table);parent.append(scroll);
   const rows=[];
   for(let model of models) {
    const row=el('tr',null,'model-access-row');row.dataset.model=model.model;
    const identity=el('td'),heading=el('div',null,'model-access-identity');heading.append(el('strong',model.name||model.model));if(model.name&&model.name!==model.model)heading.append(el('small',model.model));identity.append(heading);
    const nameCell=el('td'),name=el('input');name.value=model.public_id;name.required=true;name.maxLength=200;name.pattern=String.raw`[A-Za-z0-9][A-Za-z0-9._:\/\-]{0,199}`;name.autocomplete='off';name.spellcheck=false;name.setAttribute('aria-label','Public API name');
    const status=el('p',null,'model-access-status');status.setAttribute('role','status');nameCell.append(name,status);
    const toggle=el('td',null,'model-access-toggle'),enabled=el('input');enabled.type='checkbox';enabled.checked=model.enabled_by_default;enabled.setAttribute('aria-label','Available by default');toggle.append(enabled);
    const actions=el('td',null,'model-access-actions'),form=el('form'),save=el('button','Save','secondary compact');form.id='model-setting-form-'+(++formSerial);name.setAttribute('form',form.id);enabled.setAttribute('form',form.id);save.type='submit';save.disabled=true;form.append(save);actions.append(form);
    row.append(identity,nameCell,toggle,actions);body.append(row);rows.push({row,model,name});
    FormUI.watch(form,save);
    save.disabled=false;
    const feedback=(text,error=false)=>{status.textContent=text;status.classList.toggle('error',error)};
    row.addEventListener('input',()=>feedback(''));
    form.addEventListener('submit',async e=>{e.preventDefault();if(!form.reportValidity()||save.disabled||save.hidden)return;save.disabled=true;name.disabled=enabled.disabled=true;feedback('');
     try {model=await api('/v1/system/model-settings/'+model.id,{method:'PUT',body:JSON.stringify({public_id:name.value,enabled_by_default:enabled.checked,version:model.version})});if(!active())return;name.value=model.public_id;enabled.checked=model.enabled_by_default;FormUI.saved(form);feedback('');window.dispatchEvent(new Event('model-settings-change'));}
     catch(error){if(active())feedback(error.status===409?'Name already in use or settings changed. Reload this provider and try again.':error.message,true)}
     finally{if(active()){name.disabled=enabled.disabled=false;save.disabled=false;FormUI.update(form)}}
    });
   }
   const empty=el('tr'),emptyCell=el('td',null,'model-access-empty');emptyCell.colSpan=4;empty.append(emptyCell);body.append(empty);
   const filter=()=>{const value=search.value.toLowerCase();let visible=0;for(const {row,model,name}of rows){row.hidden=![model.name,model.model,name.value].some(v=>(v||'').toLowerCase().includes(value));if(!row.hidden)visible++}count.textContent=visible+' / '+models.length+' models';empty.hidden=visible>0;emptyCell.textContent=models.length?'No models match your search.':'Connect the provider to load its models.'};
   search.addEventListener('input',filter);filter();

  }catch(error){if(active())parent.replaceChildren(el('p',error.message,'error'))}
 }
 async function organization(parent,api,{organization,current=()=>true}) {
  const form=el('form',null,'keys-form organization-model-access'),title=el('h2','Additional models'),help=el('p','Enable models for this organization even when they are hidden by default.','field-explanation');
  const rows=el('div',null,'organization-model-grants'),controls=el('div',null,'model-grant-controls'),select=el('select'),add=el('button','Add model','secondary'),status=el('p'),save=el('button','Save','primary compact');
  select.setAttribute('aria-label','Model to enable');add.type='button';save.type='submit';status.setAttribute('role','status');controls.append(select,add);form.append(title,help,rows,controls,status,save);parent.append(form);
  const url='/v1/organizations/'+organization+'/model-access';let version,models=[],selected=new Set(),busy=false;
  function render(){
   rows.replaceChildren();
   for(const id of selected){const model=models.find(m=>m.id===id);if(!model)continue;const row=el('div',null,'model-grant-row'),copy=el('div');copy.append(el('strong',model.public_id),el('small',model.provider+' · '+model.model+(model.enabled_by_default?' · Available by default':'')));const remove=el('button','Remove','secondary');remove.type='button';remove.disabled=busy;remove.addEventListener('click',()=>{selected.delete(id);render()});row.append(copy,remove);rows.append(row)}
   if(!selected.size)rows.append(el('p','No additional models.','field-explanation'));
   const providers=new Map();
   for(const m of models.filter(m=>!m.enabled_by_default&&!selected.has(m.id))){
    if(!providers.has(m.provider))providers.set(m.provider,{id:m.provider,name:m.provider_name||m.provider,icon_url:m.provider_icon_url,icon_monochrome:m.provider_icon_monochrome,models:[]});
    providers.get(m.provider).models.push({...m.capabilities,id:m.model,display_name:m.name||m.public_id,public_id:m.public_id,setting_id:m.id});
   }
   ModelPicker.populate(select,[...providers.values()],{value:'',placeholder:'Choose a model',label:m=>m.public_id,optionValue:(_,m)=>m.setting_id});
   select.disabled=busy||select.options.length===1;add.disabled=busy||!select.value;save.disabled=busy;FormUI.update(form);
  }
  select.addEventListener('change',()=>{add.disabled=busy||!select.value});
  add.addEventListener('click',()=>{if(select.value){selected.add(select.value);message(status,'');render()}});
  save.disabled=add.disabled=select.disabled=true;
  try{const response=await api(url);if(!current())return;models=response.data||[];version=response.version;selected=new Set(response.model_ids||[]);FormUI.watch(form,save,{read:()=>JSON.stringify([...selected].sort())});render()}
  catch(error){if(current())message(status,error.message,true);return}
  form.addEventListener('submit',async e=>{e.preventDefault();if(save.disabled||save.hidden)return;busy=true;render();message(status,'Saving…');try{const response=await api(url,{method:'PUT',body:JSON.stringify({model_ids:[...selected],version})});if(!current())return;version=response.version;selected=new Set(response.model_ids);FormUI.saved(form);message(status,'');window.dispatchEvent(new Event('model-settings-change'))}catch(error){if(current())message(status,error.status===409?'Settings changed. Reopen this organization and try again.':error.message,true)}finally{busy=false;if(current())render()}});
 }
 window.ModelAccess={provider,organization};
})();
