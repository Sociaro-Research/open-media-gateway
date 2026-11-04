(() => {
 'use strict';
 let sequence=0,active=null;
 const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!=null)e.textContent=text;if(cls)e.className=cls;return e;};
 const usd=new Intl.NumberFormat(undefined,{style:'currency',currency:'USD',currencyDisplay:'narrowSymbol',maximumFractionDigits:6});
 const costOf=task=>task.cost?.customer_tariff_usd??task.cost?.customer_metered_usd??task.cost?.customer_expected_usd??task.cost?.expected_usd;
 const costText=task=>costOf(task)==null?'—':usd.format(costOf(task));
 function closeDetails(restore=true){if(active){active.restore=restore;active.dialog.close();}}
 function requestDetails(task,person,keyLabel,trigger,parent,organization,api,offset,current){
  if(current!==sequence||!trigger.isConnected||task.content_access===false)return;
  closeDetails(false);const dialog=node('dialog',null,'keys-revoke-dialog detail-dialog organization-request-dialog'),state={dialog,restore:true};active=state;
  const {heading,close:dismiss}=FormUI.dialogHeading(dialog,'Request details',()=>dialog.close());heading.parentElement.classList.add('detail-heading');dialog.setAttribute('aria-label','Request details');
  const section=(title,content)=>{const part=node('section',null,'detail-section');part.append(node('h3',title),content);dialog.append(part);};
  const facts=entries=>{const dl=node('dl',null,'detail-facts');for(const [label,value] of entries){if(value==null||value==='')continue;const pair=node('div'),dd=node('dd');dd.append(value instanceof Node?value:document.createTextNode(String(value)));pair.append(node('dt',label),dd);dl.append(pair);}return dl;};
  section('Overview',facts([['User',UserUI.identity(person)],['API key',keyLabel],['Created',new Date(task.created_at*1000).toLocaleString()],['Model',task.model],['Status',task.status],['Cost',costText(task)]]));
  section('Prompt',node('div',task.prompt||task.input||'Not retained','detail-text'));
  if(task.instructions)section('Instructions',node('div',task.instructions,'detail-text'));
  const settings=facts([['Voice',task.voice],['Size',task.size],['Duration',task.seconds?task.seconds+' seconds':'']]);if(settings.children.length)section('Saved settings',settings);
  const materials=node('div',null,'detail-links');
  for(const input of [task.image,task.input_reference,task.options?.video_urls,task.options?.audio_urls].flat(2).filter(Boolean)){
   const link=node('a','Input material '+(materials.children.length+1));try{const url=new URL(input,location.origin);if(!['https:','http:'].includes(url.protocol))continue;link.href=url.href;}catch{continue;}link.target='_blank';link.rel='noopener noreferrer';materials.append(link);
  }
  if(materials.children.length)section('Input materials',materials);
  if(task.error?.message)section('Error',node('p',task.error.message,'operation-error'));
  const base='/v1/organizations/'+organization.id,results=node('div',null,'detail-links');
  if(task.status==='completed'&&!task.result_deleted_at){const link=node('a','Open result ↗');link.href=base+'/generations/'+task.id+'/content';link.target='_blank';link.rel='noopener noreferrer';results.append(link);}
  if(task.can_delete&&['completed','failed'].includes(task.status)&&!task.result_deleted_at){const remove=node('button','Delete files','secondary danger');remove.type='button';remove.onclick=()=>{
   const confirmation=node('dialog',null,'keys-revoke-dialog'),isCurrent=()=>current===sequence&&active===state&&dialog.open&&trigger.isConnected&&parent.isConnected;state.confirmation=confirmation;confirmation.append(node('h2','Delete stored result files?'),node('p','Request history and accounting records are preserved.'));const actions=node('div',null,'docs-actions'),cancel=node('button','Cancel','secondary'),confirm=node('button','Delete files','secondary danger'),message=node('p');message.setAttribute('role','status');cancel.type=confirm.type='button';cancel.onclick=()=>confirmation.close();confirm.onclick=async()=>{if(!isCurrent())return;confirm.disabled=true;try{await api(base+'/generations/'+task.id+'/content',{method:'DELETE'});if(!isCurrent())return;confirmation.close();closeDetails(false);parent.replaceChildren();await mount(parent,organization,api,offset);}catch(e){if(!isCurrent()||!confirmation.open)return;FormUI.message(message,e.message,'error');confirm.disabled=false;}};actions.append(cancel,confirm);confirmation.append(message,actions);confirmation.onclose=()=>{if(state.confirmation===confirmation)state.confirmation=null;confirmation.remove();};document.body.append(confirmation);confirmation.showModal();cancel.focus();};results.append(remove);}
  if(results.children.length)section('Result files',results);
  const record=node('details',null,'detail-record');record.append(node('summary','All retained details'),node('pre',JSON.stringify(task,null,2)));dialog.append(record);
  const observer=new MutationObserver(()=>{if(active===state&&(current!==sequence||!trigger.isConnected||!parent.isConnected))closeDetails(false);});
  dialog.onclose=()=>{observer.disconnect();state.confirmation?.close();const restore=active===state;if(restore)active=null;dialog.remove();if(restore&&state.restore&&trigger.isConnected)trigger.focus({preventScroll:true});};
  document.body.append(dialog);observer.observe(document.body,{childList:true,subtree:true});dialog.showModal();dismiss.focus();
 }
 async function mount(parent,organization,api,offset=0){
  closeDetails(false);const current=++sequence,base='/v1/organizations/'+organization.id;
  FormUI.sectionHeading(parent,'Request logs');const status=node('p');status.setAttribute('role','status');parent.append(status);FormUI.message(status,'Loading requests…');
  try {
   const [result,directory]=await Promise.all([api(base+'/generations?limit=50&offset='+offset),OrganizationRoles.statistics(organization.role)?api(base+'/members'):Promise.resolve({data:[]})]);if(current!==sequence||!parent.isConnected)return;
   const users=new Map(directory.data.map(m=>[m.id,m]));FormUI.message(status,result.data.length?'':'No generations yet.');
   const wrap=node('div',null,'docs-table-wrap'),table=node('table',null,'docs-table'),head=node('thead'),body=node('tbody'),header=node('tr');for(const label of ['Time','User / API key','Model','Status','Cost','']){const th=node('th',label);th.scope='col';header.append(th);}head.append(header);table.append(head,body);wrap.append(table);parent.append(wrap);
   for(const task of result.data){
    const row=node('tr'),person={...users.get(task.actor_id||task.user_id),id:task.actor_id||task.user_id,email:task.identity_snapshot?.actor_email||users.get(task.actor_id||task.user_id)?.email},keyLabel=task.identity_snapshot?.api_key_name||task.api_key_id||'—',identity=node('div',null,'request-identity');identity.append(UserUI.identity(person),node('small',keyLabel));
    const details=node('div');if(task.content_access===false)details.append(node('span','Content restricted'));else{const open=node('button','Details','secondary');open.type='button';open.setAttribute('aria-haspopup','dialog');open.onclick=()=>requestDetails(task,person,keyLabel,open,parent,organization,api,offset,current);details.append(open);}
    const values=[new Date(task.created_at*1000).toLocaleString(),identity,task.model,task.status,costText(task),details];for(const [index,value] of values.entries()){const cell=node('td',null,index===values.length-1?'table-row-actions':null);cell.append(value instanceof Node?value:document.createTextNode(value));row.append(cell);}body.append(row);
   }
   const pagination=node('div',null,'report-pagination'),previous=node('button','Previous','secondary'),next=node('button','Next','secondary');previous.type=next.type='button';previous.disabled=offset===0;next.disabled=!result.has_more;previous.onclick=()=>{parent.replaceChildren();mount(parent,organization,api,Math.max(0,offset-50));};next.onclick=()=>{parent.replaceChildren();mount(parent,organization,api,result.next_offset);};pagination.append(previous,node('span','Page '+(Math.floor(offset/50)+1)),next);pagination.hidden=offset===0&&!result.has_more;parent.append(pagination);
  } catch(e){if(current===sequence)FormUI.message(status,e.message,'error');}
 }
 window.OrganizationJournal={mount};
})();
