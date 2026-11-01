// Input preparation is scoped to an authorized media workflow, never a model.
(() => {
 'use strict';
 const node=(tag,text='',cls='')=>Object.assign(document.createElement(tag),{textContent:text,className:cls});
 const matches=(preparation,schema)=>preparation.action?.kind===schema.kind||(preparation.outputs||[]).some(output=>output.kind===schema.kind)&&(!preparation.create||!schema.subtypes?.length||preparation.create.subtypes?.some(type=>schema.subtypes.includes(type)));
 async function load(api,provider,model,signal){const response=await api('/v1/models/'+encodeURIComponent(model.public_id||model.id)+'/preparations?provider='+encodeURIComponent(provider.id),{signal});return response.data||[];}
 function open({api,provider,model,schema,operations,binding=null,signal,onReady=()=>{},trigger=document.activeElement,scope='personal'}){
  const controller=new AbortController(),dialog=node('dialog','','reference-library resource-library media-preparation-dialog'),abort=()=>{controller.abort();if(dialog.open)dialog.close();};signal?.addEventListener('abort',abort,{once:true});window.addEventListener('gateway-key-change',abort);
  const current=()=>!controller.signal.aborted&&dialog.open&&dialog.isConnected,request=(url,options={})=>api(url,{...options,signal:options.signal?AbortSignal.any([options.signal,controller.signal]):controller.signal});
  const heading=FormUI.dialogHeading(dialog,'Prepare media input',()=>dialog.close());heading.heading.id='mediaPreparation'+(++open.sequence);dialog.setAttribute('aria-labelledby',heading.heading.id);
  const intro=node('p','Prepare an input for '+(model.display_name||model.id)+'. Review any cost before submitting.','field-hint'),label=node('label','Input preparation'),select=document.createElement('select'),host=node('section','','resource-controls'),status=node('p','','reference-status'),actions=node('div','','reference-actions form-actions'),review=node('button','Review preparation','secondary'),confirm=node('button','Confirm preparation','primary compact');
  review.type=confirm.type='button';confirm.hidden=true;status.setAttribute('role','status');label.append(select);actions.append(review,confirm);dialog.append(intro,label,host,status,actions);document.body.append(dialog);
  const available=operations.filter(operation=>matches(operation,schema)&&(!operation.action||!!binding));for(const operation of available)select.append(new Option(operation.label,operation.operation));label.hidden=available.length<2;
  let editor=null,selected=null,quoted=null,busy=false,locked=false,task=null,poll=null,key=null,quoteVersion=0,quoteController=null;
  const invalidate=()=>{quoteVersion++;quoteController?.abort();quoteController=null;quoted=null;if(!locked)status.textContent="";confirm.hidden=true;review.hidden=false;review.disabled=busy||locked;};
  function render(){editor?.dispose();host.replaceChildren();selected=available.find(operation=>operation.operation===select.value);invalidate();status.textContent='';if(!selected){status.textContent='No input preparation is available for this workflow.';review.disabled=true;return;}
   const face=!selected.create&&!selected.action&&(selected.outputs||[]).some(output=>output.kind==='face_session');
   const create=selected.create||face&&{kind:'face_session',subtypes:['video'],name_max_characters:64,variants:[{subtype:'video',min_sources:1,max_sources:1,source_kinds:['video'],limits:selected.reference_media_limits}]};
   const helper={id:selected.operation,resource_operation:create||undefined,session_operation:selected.action,resource_input_schemas:selected.action?[{kind:selected.action.kind,max_count:1}]:[]};
   editor=ResourceControls.mount(host,{api:request,provider,model:helper,scope,preparing:true,initial:binding?[{binding_id:binding.id,reference_id:binding.kind+'_1'}]:[],signal:controller.signal,locked:()=>locked,onChange:invalidate});
   if(face){const name=host.querySelector('input[type="text"]');if(name){name.value='Video input';name.closest('label').hidden=true;}}
   FormUI.refresh();
  }
  function payload(){if(!editor?.valid())throw Error('Complete the input fields before reviewing.');const workflow_model=model.public_id||model.id;
   if(selected.action){const {binding_id,action,...intent}=editor.action();return {path:'/v1/provider-resources/'+encodeURIComponent(binding_id)+'/actions/'+encodeURIComponent(action),body:{model:selected.operation,workflow_model,...intent}};}
   const intent=editor.creation();if(!selected.create)return {path:'/v1/media-inputs/faces',body:{provider:provider.id,workflow_model,reference_id:intent.reference_ids[0]}};
   return {path:'/v1/provider-resources',body:{...intent,workflow_model}};
  }
  function pollTask(){poll?.stop();poll=GatewayAutoUpdate.watch({root:dialog,active:()=>current()&&!!task&&task.status!=='completed'&&task.status!=='failed',load:async({current:fresh})=>{
   try{const next=await request('/v1/generations/'+encodeURIComponent(task.id));if(!fresh()||!current())return;task=next;status.textContent=task.status==='failed'?(task.error?.message||task.error||'Input preparation failed. No retry was submitted.'):'Preparing input…';if(task.status==='failed'){poll.stop();return;}if(task.status==='completed'){poll.stop();const id=task.resources?.[0]?.binding_id||binding?.id;if(!id){status.textContent='Input preparation completed; refresh the input library to inspect its result.';return;}const ready=await request('/v1/provider-resources/'+encodeURIComponent(id));if(current()){onReady(ready);dialog.close();}}}
   catch(error){if(current()&&error.name!=='AbortError')status.textContent='Could not read preparation status. Retrying the read only. '+error.message;}
  }});poll.refresh();}
  review.onclick=async()=>{if(busy||locked||!current())return;busy=true;review.disabled=true;try{const submission=payload(),version=++quoteVersion,operation=selected.operation,stamp=JSON.stringify(submission),read=new AbortController();quoteController=read;const quote=await request(submission.path+'/estimate',{method:'POST',body:JSON.stringify(submission.body),signal:read.signal});if(!current()||read.signal.aborted||version!==quoteVersion||operation!==selected?.operation||stamp!==JSON.stringify(payload()))return;const amount=quote.cost?.customer_expected_usd??quote.cost?.expected_usd;if(quote.cost?.quantity_unknown||!Number.isFinite(amount)||amount<0||quote.can_generate!==true)throw Error('A verified, permitted estimate is unavailable. No preparation was submitted.');quoted={...submission,amount,version,operation,stamp};key=crypto.randomUUID();status.textContent='Estimated cost '+new Intl.NumberFormat('en-US',{style:'currency',currency:'USD',maximumFractionDigits:5}).format(amount)+'. This prepares an input; media generation is separate.';confirm.hidden=false;review.hidden=true;}
   catch(error){if(current()&&error.name!=='AbortError')status.textContent=error.message;}finally{busy=false;if(current())review.disabled=locked;}};
  confirm.onclick=async()=>{if(busy||locked||!quoted||!current())return;if(quoted.version!==quoteVersion||quoted.operation!==selected?.operation||quoted.stamp!==JSON.stringify(payload())){invalidate();status.textContent="Inputs changed. Review the preparation again.";return;}busy=true;locked=true;host.inert=true;for(const control of host.querySelectorAll('input,select,textarea,button'))control.disabled=true;confirm.disabled=true;select.disabled=true;const submission=quoted;try{task=await request(submission.path,{method:'POST',headers:{'Idempotency-Key':key},body:JSON.stringify({...submission.body,max_expected_usd:submission.amount})});if(!current())return;status.textContent='Preparing input…';pollTask();}
   catch(error){if(current()&&error.name!=='AbortError')status.textContent=(!error.status||error.status>=500?'Preparation outcome is unconfirmed. No mutation will be retried. Check the Journal and input state. ': 'Preparation was rejected. Close this window and review the current inputs. ')+error.message;}
   finally{busy=false;if(current())editor?.sync();}};
  select.onchange=()=>{if(!locked)render();};
  dialog.addEventListener('close',()=>{controller.abort();poll?.stop();editor?.dispose();signal?.removeEventListener('abort',abort);window.removeEventListener('gateway-key-change',abort);dialog.remove();if(trigger?.isConnected&&!trigger.disabled)trigger.focus();},{once:true});
  dialog.showModal();render();return {dialog,dispose:abort};
 }
 open.sequence=0;
 window.MediaPreparations={load,matches,open};
})();
