(() => {
 'use strict';
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,action)=>{const b=el('button',text,'secondary');b.type='button';b.onclick=action;return b;};
 const field=(parent,label,value='',tag='input')=>{const input=el(tag);input.setAttribute('aria-label',label);input.value=value;parent.append(input);return input;};
 const object=(input)=>{const value=JSON.parse(input.value.trim()||'{}');if(!value||Array.isArray(value)||typeof value!=='object')throw Error('Enter a JSON object for '+input.getAttribute('aria-label'));return value;};
 let typesPromise;
 const types=()=>typesPromise||=(api('/v1/admin/providers/types').then(r=>r.data||[]).catch(e=>{typesPromise=null;throw e;}));
 function dialog(title){
  const d=el('dialog',null,'keys-revoke-dialog system-dialog'),form=el('form',null,'provider-connection-settings');FormUI.sectionHeading(form,title);
  const content=el('div',null,'connection-editor'),status=el('p'),actions=el('div',null,'form-actions'),submit=el('button','Create','primary'),cancel=button('Cancel',()=>d.close());
  status.setAttribute('role','status');submit.type='submit';actions.append(cancel,submit);form.append(content,status,actions);d.append(form);document.body.append(d);
  d.addEventListener('close',()=>d.remove());d.showModal();return{d,form,content,status,submit};
 }
 async function create(){
  const {d,form,content,status,submit}=dialog('Add provider');const name=field(content,'Provider name');name.required=true;name.maxLength=120;
  const type=field(content,'API type','','select');submit.disabled=true;
  try{for(const item of await types())type.append(new Option(item.name,item.id));submit.disabled=!type.options.length;FormUI.refresh();}catch(e){FormUI.message(status,e.message,'error')}
  form.onsubmit=async event=>{event.preventDefault();if(!form.reportValidity())return;submit.disabled=true;
   try{const provider=await api('/v1/admin/providers',{method:'POST',body:JSON.stringify({name:name.value,api_type:type.value})});selectedAdminProviderID=provider.id;d.close();await loadAdminProviders();}
   catch(e){FormUI.message(status,e.message,'error')}finally{submit.disabled=false}
  };name.focus();
 }
 function setup(){const hero=document.querySelector('#providersView .providers-hero');let b=document.querySelector('#addProvider');if(!b){b=button('Add provider',create);b.id='addProvider';hero.append(b)}b.hidden=!systemAdministrator;}
 function render(provider){
  setup();const form=elements.providerForm;form.querySelector('.provider-connection-settings')?.remove();
  document.querySelector('#manualModelSettings')?.remove();document.querySelector('#archiveProvider')?.remove();
  if(!systemAdministrator||!provider.api_type||provider.api_type==='mock')return;
  const state=structuredClone(provider.connection||{headers:[],models:[],endpoints:{}});state.headers||=[];state.models||=[];state.endpoints||={};form.connectionDraft=state;
  const area=el('div',null,'provider-connection-settings');form.querySelector('.provider-dynamic-fields').after(area);
  const name=field(area,'Provider name',provider.name);name.dataset.connectionName='';name.maxLength=120;name.required=true;
  const timeout=field(area,'Request timeout (seconds)',state.timeout_seconds??provider.default_timeout_seconds??30);timeout.type='number';timeout.min=1;timeout.max=9223372036;timeout.step=1;timeout.required=true;timeout.dataset.connectionTimeout='';
  area.append(el('p','Default wait for each provider API response. A generation request can override it with timeout_seconds.','field-explanation'));
  const manualLabel=el('label','Use manual catalog only'),manual=el('input');manual.type='checkbox';manual.checked=!!state.manual_catalog;manual.dataset.manualCatalog='';manualLabel.append(manual);area.append(manualLabel,el('p','Manual models work without a public model list. Saving does not test access or start a paid generation.','field-explanation'));
  const advanced=el('details'),summary=el('summary','Advanced API settings');advanced.append(summary);area.append(advanced);
  const endpoints=el('div',null,'provider-dynamic-fields');for(const [key,label]of [['models','Model list URL'],['generate','Generation URL'],['poll','Task status URL']]){const input=field(endpoints,label,state.endpoints[key]||'');input.dataset.endpoint=key;}
  advanced.append(endpoints,el('p','Leave URLs empty for adapter defaults. Use a full URL or absolute path. Task status URLs may contain {task_id}.','field-explanation'));
  const headerHeading=FormUI.sectionHeading(advanced,'Additional headers','h3'),addHeader=button('Add header',()=>{state.headers.push({name:'',value:'',operation:''});renderHeaders();FormUI.update(form);});headerHeading.parentElement.append(addHeader);
  const scroll=el('div',null,'connection-table-scroll');advanced.append(scroll);
  function renderHeaders(){scroll.replaceChildren();const table=el('table',null,'docs-table'),head=el('thead'),tr=el('tr'),body=el('tbody');for(const label of ['Header','Value','Operation',''])tr.append(el('th',label));head.append(tr);table.append(head,body);scroll.append(table);
   for(const header of state.headers){const row=el('tr');const cells=Array.from({length:4},()=>el('td'));row.append(...cells);body.append(row);
    const key=field(cells[0],'Header name',header.name);key.required=true;key.oninput=()=>header.name=key.value;
    const value=field(cells[1],'Header value');FormUI.credentials.render(value,!!header.hint,header.hint||'');if(header.value!==undefined){value.value=header.value;value.dataset.savedCredential='false'}value.oninput=()=>{const v=FormUI.credentials.updateValue(value);if(v!==undefined)header.value=v;};
    const scope=field(cells[2],'Header operation','','select');scope.dataset.controlVariant='table';for(const [id,label]of [['','All API requests'],['models','Model list'],['generate','Generation'],['poll','Task status']])scope.append(new Option(label,id));scope.value=header.operation||'';scope.onchange=()=>header.operation=scope.value;
    const actions=el('div',null,'table-row-actions');actions.append(button('Remove',()=>{state.headers.splice(state.headers.indexOf(header),1);renderHeaders();FormUI.update(form);}));cells[3].append(actions);
   }FormUI.refresh();
  }renderHeaders();
  const extra=field(advanced,'Additional request body (JSON)',JSON.stringify(state.extra_body||{},null,2),'textarea');extra.dataset.connectionBody='';
  advanced.append(el('p','Connection defaults override generated optional parameters; model settings override connection defaults. Model IDs and input content are protected.','field-explanation'));
  const modelArea=el('div',null,'provider-panel');modelArea.id='manualModelSettings';document.querySelector('.provider-insights').prepend(modelArea);const heading=FormUI.sectionHeading(modelArea,'Manual models','h3');heading.parentElement.append(button('Add model',()=>editModel(provider,state,null,modelArea,form)));
  modelArea.append(el('p','Add a new ID or copy a supported request profile. Set an explicit price per request to use budgeted generation; an empty price means unavailable, not free.','field-explanation'));
  renderModels(provider,state,modelArea,form);
  const archive=button('Archive provider',()=>{
   const {d,form:confirm,content,status,submit}=dialog('Archive '+provider.name);submit.textContent='Archive';content.append(el('p','This stops new generations. Accepted tasks and history are retained.'));
   confirm.onsubmit=async e=>{e.preventDefault();submit.disabled=true;try{await api('/v1/admin/providers/'+provider.id,{method:'PUT',body:JSON.stringify({archived:true,version:provider.version})});d.close();await loadAdminProviders();await loadProviders()}catch(error){FormUI.message(status,error.message,'error');submit.disabled=false}};
  });archive.id='archiveProvider';area.append(archive);
 }
 function renderModels(provider,state,area,form){area.querySelector('.manual-model-table')?.remove();const scroll=el('div',null,'connection-table-scroll manual-model-table'),table=el('table',null,'docs-table'),head=el('thead'),tr=el('tr'),body=el('tbody');for(const text of ['Model','API profile','Price / request',''])tr.append(el('th',text));head.append(tr);table.append(head,body);scroll.append(table);area.append(scroll);
  for(const item of state.models){const row=el('tr');row.append(el('td',item.model.display_name||item.model.id),el('td',item.profile),el('td',item.price_usd==null?'Unknown':'$'+item.price_usd));const actions=el('td'),buttons=el('div',null,'table-row-actions');buttons.append(button('Edit',()=>editModel(provider,state,item,area,form)),button('Copy',()=>editModel(provider,state,item,area,form,true)),button('Remove',()=>{state.models.splice(state.models.indexOf(item),1);renderModels(provider,state,area,form);FormUI.update(form)}));actions.append(buttons);row.append(actions);body.append(row)}
  if(!state.models.length){const td=el('td','No manual models.');td.colSpan=4;const row=el('tr');row.append(td);body.append(row)}
 }
 async function editModel(provider,state,item,area,providerForm,copy=false){
  const {d,form,content,status,submit}=dialog(item&&!copy?'Edit model':'Add model');submit.textContent=item&&!copy?'Apply':'Add';
  const id=field(content,'Upstream model ID',copy?'':item?.model.id||'');id.required=true;id.maxLength=200;
  const name=field(content,'Display name',item?.model.display_name||'');const profile=field(content,'API request profile','','select');
  const price=field(content,'Price per request (USD; optional)',item?.price_usd??'');price.type='number';price.min=0;price.max=1000000;price.step='0.000001';
  const details=el('details');details.append(el('summary','Model capabilities and additional parameters'));content.append(details);
  const capabilities=field(details,'Model capabilities (JSON)',JSON.stringify(item?.model||{},null,2),'textarea'),extra=field(details,'Model request body (JSON)',JSON.stringify(item?.extra_body||{},null,2),'textarea');
  content.append(el('p','The profile selects a compatible request and response format. Your exact upstream ID is sent to the API. Add the model, then save the provider settings.','field-explanation'));
  let profiles=[];submit.disabled=true;
  try{const type=(await types()).find(t=>t.id===provider.api_type);profiles=type?.profiles||[];ModelPicker.populate(profile,[{id:provider.api_type,name:type?.name||provider.api_type,icon_url:provider.icon_url,models:profiles}],{value:item?.profile||'',placeholder:'Choose an API request profile',optionValue:(_,m)=>m.id});submit.disabled=false;FormUI.refresh();}catch(e){FormUI.message(status,e.message,'error')}
  profile.onchange=()=>{const template=profiles.find(m=>m.id===profile.value);if(template){const clone=structuredClone(template);delete clone.pricing;delete clone.metadata;delete clone.public_id;capabilities.value=JSON.stringify(clone,null,2);FormUI.refresh();}};
  form.onsubmit=e=>{e.preventDefault();try{if(!form.reportValidity()||!profile.value)throw Error('Choose an API request profile');const model={...object(capabilities),id:id.value,display_name:name.value||id.value};if(state.models.some(m=>m!==item&&m.model.id===model.id)||copy&&state.models.some(m=>m.model.id===model.id))throw Error('This upstream model ID already exists in this connection');const next={model,profile:profile.value,extra_body:object(extra)};if(price.value!=='')next.price_usd=Number(price.value);if(item&&!copy)state.models[state.models.indexOf(item)]=next;else state.models.push(next);renderModels(provider,state,area,providerForm);FormUI.update(providerForm);d.close();}catch(error){FormUI.message(status,error.message,'error')}};
 }
 function payload(form){if(!form.connectionDraft)return{};const state=structuredClone(form.connectionDraft);state.manual_catalog=form.querySelector('[data-manual-catalog]').checked;state.timeout_seconds=Number(form.querySelector('[data-connection-timeout]').value);for(const input of form.querySelectorAll('[data-endpoint]'))state.endpoints[input.dataset.endpoint]=input.value.trim();state.extra_body=object(form.querySelector('[data-connection-body]'));return{name:form.querySelector('[data-connection-name]').value,connection:state};}
 window.ProviderConnections={setup,render,payload,snapshot:form=>JSON.stringify(form.connectionDraft||null)};
})();
