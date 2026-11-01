(() => {
 'use strict';
 const base='/v1/system/remote-workspace',proxy=base+'/proxy';
 const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
 let dialog,epoch=0,timer,connection,writing=false,actor,viewID=0;
 function close(){if(writing)return;epoch++;clearTimeout(timer);dialog?.close();}
 function field(parent,label,name,type='text'){const frame=node('label',label),input=node(type==='textarea'?'textarea':'input');input.name=name;if(type!=='textarea')input.type=type;frame.append(input);parent.append(frame);return input;}
 async function open(){
  if(writing)return;close();dialog?.remove();dialog=node('dialog',undefined,'keys-revoke-dialog sales-dialog remote-dialog');document.body.append(dialog);const shell=node('div');dialog.append(shell);FormUI.dialogHeading(shell,'Sociaro remote workspace',close);dialog.addEventListener('cancel',e=>{e.preventDefault();close();});dialog.showModal();
  const content=node('div',undefined,'remote-content');shell.append(content);const ticket=epoch;const current=()=>ticket===epoch&&dialog.open;
  const status=node('p','Loading connection…');status.setAttribute('role','status');content.append(status);
  try{connection=await api(base);if(!current())return;content.replaceChildren();if(!connection.connected){connect(content,current);return;}await workspace(content,current);}catch(e){if(current())FormUI.message(status,e.message,'error');}
 }
 function connect(parent,rootCurrent){
  const view=++viewID,current=()=>rootCurrent()&&view===viewID;
  const container=parent,legacy=connection?.connected&&connection.provisioning!=='dedicated';if(!legacy){const authorization=node('div');container.append(authorization);void window.RemoteAuthorization?.mount(authorization,connection,async saved=>{connection=saved;container.replaceChildren();await workspace(container,rootCurrent);},current);return;}const advanced=node('details'),summary=node('summary','Replace this legacy company key');advanced.open=true;advanced.append(summary);parent.append(advanced);parent=advanced;
  parent.append(node('p','This existing connection keeps its original company, balance and history. Replace its generation-only key without changing the company. This connection remains available only to this local administrator.','field-explanation'),node('p','Create the replacement key in that same company using your own account. A new dedicated company requires a separate onboarding after explicit disconnection; existing activity is never moved.','field-explanation'));
  const form=node('form',undefined,'keys-form'),origin=field(form,'Server origin for existing key','origin','url'),company=field(form,'Remote company ID','organization_id'),token=field(form,'Company generation key','token');origin.required=company.required=token.required=true;origin.value=connection.origin;company.value=connection.organization_id;origin.readOnly=company.readOnly=true;FormUI.credentials.setup(token);
  const consent=node('label',undefined,'system-toggle'),check=node('input');check.type='checkbox';check.required=true;consent.append(check,node('span','I authorize this installation to send requests to this remote company. Its usage is charged to the remote balance.'));form.append(consent);
  const status=node('p');status.setAttribute('role','status');const submit=node('button','Verify and connect','primary compact');submit.type='submit';form.append(submit,status);parent.append(form);FormUI.refresh();let pending=false;
  form.onsubmit=async e=>{e.preventDefault();if(writing||pending||!form.reportValidity())return;pending=true;writing=true;submit.disabled=true;FormUI.message(status,'Verifying company and key…');
   try{const saved=await api(base,{method:'POST',body:JSON.stringify({origin:origin.value,organization_id:company.value,token:token.value,consent:check.checked,version:connection?.version||0})});token.value='';if(!current())return;connection=saved;container.replaceChildren();await workspace(container,rootCurrent);}
   catch(error){if(current())FormUI.message(status,error.message,'error');}finally{pending=false;writing=false;submit.disabled=false;}
  };
 }
 async function workspace(parent,rootCurrent){
  const view=++viewID,current=()=>rootCurrent()&&view===viewID,reviewed={...connection};
  const heading=node('div',undefined,'form-heading');heading.append(node('h2',reviewed.organization_name||'Remote company'));const disconnect=node('button','Disconnect','secondary');disconnect.type='button';const renew=node('button',reviewed.provisioning==='dedicated'?'Renew connection':'Replace legacy key','secondary');renew.type='button';renew.onclick=()=>{if(writing)return;clearTimeout(timer);parent.replaceChildren();connect(parent,rootCurrent);};heading.append(renew,disconnect);parent.append(heading,node('p',reviewed.origin+' · '+reviewed.organization_id,'field-explanation'));
  const account=node('p','Checking remote balance…'),message=node('p');account.setAttribute('role','status');message.setAttribute('role','status');parent.append(account);
  disconnect.onclick=async()=>{if(writing)return;if(hasUnresolved()){FormUI.message(message,'Resolve the saved submission before disconnecting. Renew the same company/account if its key expired.','error');return;}if(!confirm('Disconnect this installation? An unresolved submission may already have been accepted. Remote jobs, charges and files stay with the company. Revoke this installation’s key remotely to remove its remote access.'))return;disconnect.disabled=true;writing=true;try{await api(base,{method:'DELETE',body:JSON.stringify({version:reviewed.version})});writing=false;retain(null);if(current())await open();}catch(e){if(current())FormUI.message(message,e.message,'error');}finally{writing=false;disconnect.disabled=false;}};
  const form=node('form',undefined,'keys-form'),label=node('label','Model'),select=node('select');select.required=true;select.name='model';label.append(select);form.append(label);const prompt=field(form,'Prompt','prompt','textarea');prompt.rows=4;prompt.maxLength=20000;
  const controls=node('div',undefined,'remote-controls');form.append(controls);const estimate=node('button','Review estimate','primary compact');estimate.type='submit';form.append(estimate);const confirmation=node('div',undefined,'remote-confirmation');form.append(confirmation,message);parent.append(form);
  const history=node('section');history.append(node('h2','Remote generation history'));const list=node('div');history.append(list);parent.append(history);
  let models=[],pending=null,sending=false;
  const intentPrefix='gateway-remote-intent:'+actor?.id+':';
  const intentKey=intentPrefix+reviewed.operation_scope;
  // Older clients did not save a stable context/operation namespace. Keep
  // those retries for explicit history reconciliation; never silently replay
  // them with a newly derived remote idempotency key.
  try{pending=JSON.parse(sessionStorage.getItem(intentKey)||'null');}catch{}
  const otherIntents=[];try{for(let index=0;index<sessionStorage.length;index++){const key=sessionStorage.key(index);if(key?.startsWith(intentPrefix)&&key!==intentKey)otherIntents.push(key);}}catch{}
  let blockedOther=otherIntents.length>0;
  if(pending&&pending.scope!==reviewed.operation_scope){FormUI.message(message,'The saved retry belongs to another remote account. Restore its original connection before retrying.','error');pending=null;blockedOther=true;otherIntents.push(intentKey);}
  function hasUnresolved(){try{return !!sessionStorage.getItem(intentKey)||blockedOther;}catch{return true;}}

  function retain(operation){pending=operation;if(operation)sessionStorage.setItem(intentKey,JSON.stringify(operation));else sessionStorage.removeItem(intentKey);}
  function lockDraft(locked){for(const input of form.querySelectorAll('input,select,textarea'))input.disabled=locked;estimate.disabled=locked;}
  function submissionButton(retry=false){
   const send=node('button',retry?'Retry same submission':'Generate remotely','primary compact');send.type='button';confirmation.append(send);
   send.onclick=async()=>{if(sending||!pending)return;const operation=pending;
    try{retain(operation);}catch{FormUI.message(message,'Browser session storage is unavailable. Enable it before submitting so an interrupted request can be retried safely.','error');return;}
    sending=true;writing=true;lockDraft(true);send.disabled=true;FormUI.message(message,'Submitting the remote request…');
    try{await remote('/v1/generations',{method:'POST',headers:{'Idempotency-Key':operation.key},body:JSON.stringify(operation.payload)});retain(null);if(current()){confirmation.replaceChildren();lockDraft(blockedOther);await refresh();FormUI.message(message,'Accepted by the remote workspace.');}}
    catch(e){if(current()){send.textContent='Retry same submission';FormUI.message(message,e.message+' Retry this same submission to resolve its outcome before starting another.','error');}}
    finally{sending=false;writing=false;send.disabled=false;}
   };
  }
  if(pending){lockDraft(true);confirmation.append(node('p','A previous submission has an unresolved outcome. Its request is retained in this browser session. Retry it with the same operation key before starting another.'));submissionButton(true);}

  if(blockedOther){lockDraft(true);const notice=node('section'),resolved=node('button','I checked the earlier outcome','secondary');resolved.type='button';notice.append(node('p','An older client or another host, company or remote account has an unresolved saved submission. Its retry is preserved. Reconnect the original context and inspect its history before starting another request.','operation-error'),resolved);resolved.onclick=async()=>{if(writing||!confirm('Clear earlier saved retries only after verifying their final outcomes in the original remote history, or verifying they were never accepted. If an outcome is still unknown, cancel and keep the retry.'))return;for(const key of otherIntents)sessionStorage.removeItem(key);await open();};parent.prepend(notice);}
  const remote=(path,options={})=>api(proxy+path,{...options,headers:{...options.headers,'X-Workspace-Context':reviewed.context}});
  function selected(){return models.find(m=>m.id===select.value);}
  function options(){if(hasUnresolved())return;controls.replaceChildren();pending=null;confirmation.replaceChildren();const m=selected()?.capabilities||{};prompt.required=!m.prompt_optional&&!m.no_prompt;prompt.disabled=!!m.no_prompt;
   for(const [name,title,values]of [['size','Size',m.resolutions],['voice','Voice',m.voices],[m.media_type==='image'?'output_format':'response_format','Format',m.media_type==='speech'||m.media_type==='image'?m.formats:null]])if(values?.length){const l=node('label',title),c=node('select');c.name=name;c.append(new Option('Default',''),...values.map(v=>new Option(v,v)));l.append(c);controls.append(l);}
   if(m.media_type==='video'||m.media_type==='music'||m.media_type==='audio'){const duration=field(controls,'Duration (seconds)','seconds','number');duration.min=m.min_duration||1;duration.max=m.max_duration||3600;duration.step=m.duration_step||1;duration.value=m.default_duration||'';}
   FormUI.refresh();
  }
  select.onchange=options;form.addEventListener('input',()=>{if(!sending&&!hasUnresolved()){pending=null;confirmation.replaceChildren();}});
  function request(){const m=selected(),c=m?.capabilities||{},data={model:m.id};if(!c.no_prompt)data[c.media_type==='speech'?'input':'prompt']=prompt.value;for(const input of controls.querySelectorAll('input,select'))if(input.value){if(input.name==='seconds'&&c.media_type!=='video')data.options={...data.options,duration:Number(input.value)};else data[input.name]=input.value;}return data;}
  form.onsubmit=async e=>{e.preventDefault();if(sending||hasUnresolved()||!form.reportValidity()||!selected())return;sending=true;estimate.disabled=true;FormUI.message(message,'Checking the remote estimate…');
   try{const payload=request(),quote=await remote('/v1/generations/estimate',{method:'POST',body:JSON.stringify(payload)});if(!current())return;confirmation.replaceChildren();const cost=quote.cost?.expected_usd;if(typeof cost!=='number'||quote.can_generate===false)throw new Error('A funded remote estimate is required before generation.');pending={payload:{...payload,max_expected_cost_usd:cost},key:crypto.randomUUID(),scope:reviewed.operation_scope,origin:reviewed.origin,organization_id:reviewed.organization_id,reviewed_context:reviewed.context};confirmation.append(node('p',`Estimated remote charge: $${cost.toFixed(6)}. Final cost depends on actual usage. No local company balance is charged.`));submissionButton();
    FormUI.message(message,'');
   }catch(e){if(current())FormUI.message(message,e.message,'error');}finally{sending=false;estimate.disabled=false;}
  };
  async function refresh(){
   const [key,jobs]=await Promise.all([remote('/v1/key'),remote('/v1/generations?limit=20&offset=0')]);if(!current())return;
   const org=key.organization;if(!org||org.id!==reviewed.organization_id)throw new Error('Remote company binding changed. Disconnect and verify the connection.');account.textContent=org.available_usd!=null?`Remote available balance: $${org.available_usd}`:org.balance_usd!=null?`Remote balance: $${org.balance_usd}`:'Remote company connected. Balance is shown only when the key recipient’s role permits it.';
   list.replaceChildren();for(const task of jobs.data||[]){const article=node('article',undefined,'remote-job');article.append(node('strong',task.model||'Generation'),node('p',(task.status||'unknown')+' · '+(task.prompt||task.id)));
    if(task.status==='completed'){const link=node('a','Download result');link.href=proxy+'/v1/generations/'+encodeURIComponent(task.id)+'/content?workspace_context='+encodeURIComponent(reviewed.context);link.target='_blank';link.rel='noopener';article.append(link);}if(task.error)article.append(node('p',task.error.message||'Remote generation failed','operation-error'));list.append(article);}
   if(!list.children.length)list.append(node('p','No remote generations yet.'));
  }
  async function poll(){try{if(current()&&!document.hidden)await refresh();}catch(e){if(current())FormUI.message(message,e.message,'error');}if(current())timer=setTimeout(poll,5000);}
  try{const catalogue=await remote('/v1/models');if(!current())return;models=catalogue.data||[];const groups=new Map();for(const m of models){const id=m.provider||m.owned_by||'remote';if(!groups.has(id))groups.set(id,{id,name:id,models:[]});groups.get(id).models.push({...m.capabilities,id:m.id,public_id:m.id});}ModelPicker.populate(select,[...groups.values()],{placeholder:'Choose a model',optionValue:(p,m)=>m.id});FormUI.refresh();await refresh();timer=setTimeout(poll,5000);}catch(e){if(current())FormUI.message(message,e.message,'error');}
 }
 window.RemoteWorkspace={open,close,setBusy(value){writing=value;},isBusy(){return writing;},setActor(user){if(actor?.id!==user?.id){epoch++;clearTimeout(timer);dialog?.close();dialog?.remove();connection=null;}actor=user;}};
})();
