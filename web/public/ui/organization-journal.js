(() => {
 'use strict';
 let sequence=0;
 const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!=null)e.textContent=text;if(cls)e.className=cls;return e;};
 async function mount(parent,organization,api,offset=0){
  const current=++sequence,base='/v1/organizations/'+organization.id;
  FormUI.sectionHeading(parent,'Request logs');const status=node('p');status.setAttribute('role','status');parent.append(status);FormUI.message(status,'Loading requests…');
  try {
   const [result,directory]=await Promise.all([api(base+'/generations?limit=50&offset='+offset),OrganizationRoles.statistics(organization.role)?api(base+'/members'):Promise.resolve({data:[]})]);if(current!==sequence||!parent.isConnected)return;
   const users=new Map(directory.data.map(m=>[m.id,m]));FormUI.message(status,result.data.length?'':'No generations yet.');
   const wrap=node('div',null,'docs-table-wrap'),table=node('table',null,'docs-table'),head=node('thead'),body=node('tbody'),header=node('tr');for(const label of ['Time','User','API key','Model','Status','Cost','Details']){const th=node('th',label);th.scope='col';header.append(th);}head.append(header);table.append(head,body);wrap.append(table);parent.append(wrap);
   for(const task of result.data){const row=node('tr'),details=node('div');
    if(task.content_access===false)details.append(node('span','Content restricted'));
    else {
     const disclosure=node('details'),summary=node('summary','View request');disclosure.append(summary);
     const facts=node('dl','', 'model-info-facts');for(const [label,value] of [['Prompt',task.prompt||task.input||'Not retained'],['Instructions',task.instructions],['Model',task.model],['Voice',task.voice],['Size',task.size],['Duration',task.seconds?task.seconds+' seconds':'']]){if(value){facts.append(node('dt',label),node('dd',value));}}disclosure.append(facts);
     for(const input of [task.image,task.input_reference,task.options?.video_urls,task.options?.audio_urls].flat(2).filter(Boolean)){const link=node('a','Input material');try{const url=new URL(input,location.origin);if(!['https:','http:'].includes(url.protocol))continue;link.href=url.href;}catch{continue;}link.target='_blank';link.rel='noopener noreferrer';disclosure.append(link,node('br'));}
     if(task.error?.message)disclosure.append(node('p',task.error.message,'operation-error'));
     if(task.status==='completed'&&!task.result_deleted_at){const link=node('a','Open result ↗');link.href=base+'/generations/'+task.id+'/content';link.target='_blank';link.rel='noopener noreferrer';disclosure.append(link);}
     if(task.can_delete&&['completed','failed'].includes(task.status)&&!task.result_deleted_at){const remove=node('button','Delete files','secondary docs-danger');remove.type='button';remove.onclick=()=>{
      const dialog=node('dialog',null,'keys-revoke-dialog');dialog.append(node('h2','Delete stored result files?'),node('p','Request history and accounting records are preserved.'));const actions=node('div',null,'docs-actions'),cancel=node('button','Cancel','secondary'),confirm=node('button','Delete files','secondary docs-danger'),message=node('p');message.setAttribute('role','status');cancel.type=confirm.type='button';cancel.onclick=()=>dialog.close();confirm.onclick=async()=>{confirm.disabled=true;try{await api(base+'/generations/'+task.id+'/content',{method:'DELETE'});dialog.close();parent.replaceChildren();await mount(parent,organization,api,offset);}catch(e){FormUI.message(message,e.message,'error');confirm.disabled=false;}};actions.append(cancel,confirm);dialog.append(message,actions);dialog.onclose=()=>dialog.remove();document.body.append(dialog);dialog.showModal();cancel.focus();};disclosure.append(remove);}
     const record=node('details'),recordSummary=node('summary','All retained details'),recordData=node('pre',JSON.stringify(task,null,2));record.append(recordSummary,recordData);disclosure.append(record);details.append(disclosure);
    }
    const cost=task.cost?.customer_tariff_usd??task.cost?.customer_metered_usd??task.cost?.customer_expected_usd??task.cost?.expected_usd;
    for(const value of [new Date(task.created_at*1000).toLocaleString(),UserUI.identity({...users.get(task.actor_id||task.user_id),id:task.actor_id||task.user_id,email:task.identity_snapshot?.actor_email||users.get(task.actor_id||task.user_id)?.email}),task.identity_snapshot?.api_key_name||task.api_key_id||'—',task.model,task.status,cost==null?'—':new Intl.NumberFormat(undefined,{style:'currency',currency:'USD',maximumFractionDigits:6}).format(cost),details]){const cell=node('td');cell.append(value instanceof Node?value:document.createTextNode(value));row.append(cell);}body.append(row);
   }
   const pagination=node('div',null,'report-pagination'),previous=node('button','Previous','secondary'),next=node('button','Next','secondary');previous.type=next.type='button';previous.disabled=offset===0;next.disabled=!result.has_more;previous.onclick=()=>{parent.replaceChildren();mount(parent,organization,api,Math.max(0,offset-50));};next.onclick=()=>{parent.replaceChildren();mount(parent,organization,api,result.next_offset);};pagination.append(previous,node('span','Page '+(Math.floor(offset/50)+1)),next);pagination.hidden=offset===0&&!result.has_more;parent.append(pagination);
  } catch(e){if(current===sequence)FormUI.message(status,e.message,'error');}
 }
 window.OrganizationJournal={mount};
})();
