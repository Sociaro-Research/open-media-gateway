(() => {
 'use strict';
 const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const action=(text,fn)=>{const b=node('button',text,'secondary');b.type='button';b.onclick=fn;return b;};
 const labels={sending:'Sending invitation',sent:'Invitation sent',send_failed:'Email delivery failed',accepted:'Owner joined',expired:'Invitation expired',revoked:'Invitation revoked'};
 window.ClientOnboarding={mount:async(parent,{current,refresh})=>{
  const section=node('section',null,'member-invitations'),heading=node('div',null,'report-heading console-section-heading'),status=node('p'),history=node('div');status.setAttribute('role','status');
  const create=action('Create company',()=>createDialog());heading.append(node('h2','New companies'),create);section.append(heading,status,history);parent.append(section);
  let busy=false,loading=false,items=[],timer,closed=false,fingerprint=null;
  const alive=()=>!closed&&current()&&section.isConnected;
  function render(){
   history.replaceChildren();
   if(!items.length){history.append(node('p','Create a company and invite its first Supermanager.','field-explanation'));return;}
   const wrap=node('div',null,'docs-table-wrap'),table=node('table',null,'docs-table'),head=node('thead'),headRow=node('tr'),body=node('tbody');
   table.style.minWidth='760px';for(const label of ['Company','First Supermanager','Invitation','Expires','']){const th=node('th',label);th.scope='col';headRow.append(th);}head.append(headRow);table.append(head,body);wrap.append(table);history.append(wrap);
   for(const c of items){
    const row=node('tr'),buttons=node('td',null,'table-row-actions');
    for(const value of [c.name,UserUI.identity({email:c.email,name:c.contact_name},{label:c.email}),labels[c.status]||c.status,new Date(c.expires_at).toLocaleString()]){const cell=node('td');cell.append(value instanceof Node?value:document.createTextNode(value));row.append(cell);}
    if(c.status!=='accepted'){
     const resend=action(c.status==='send_failed'?'Retry email':'Resend invitation',()=>change(c,'resend'));
     resend.disabled=busy||c.status==='sending';buttons.append(resend);
     if(c.status!=='revoked'){const revoke=action('Revoke',()=>confirmRevoke(c));revoke.disabled=busy;buttons.append(revoke);}
    }
    row.append(buttons);body.append(row);
   }
  }
  async function load(){
   if(!alive()||loading)return;loading=true;
   try{const result=await api('/v1/client-onboardings');if(!alive())return;const next=Array.isArray(result.data)?result.data:[],value=JSON.stringify(next);if(value!==fingerprint){items=next;fingerprint=value;render();}}
   catch(e){if(alive()){if(e.status===401||e.status===403){items=[];render();closed=true;create.disabled=true;}FormUI.message(status,e.message,'error');}}
   finally{loading=false;}
  }
  const operations=new Map();
  async function change(c,kind){
   if(busy||!alive())return;busy=true;render();FormUI.message(status,'');
   const key=[c.organization_id,c.version,kind].join('/');if(!operations.has(key))operations.set(key,crypto.randomUUID());
   try{await api(`/v1/client-onboardings/${c.organization_id}/${kind}`,{method:'POST',body:JSON.stringify({operation_id:operations.get(key),version:c.version})});operations.delete(key);await load();}
   catch(e){if(alive())FormUI.message(status,e.message,'error');if(e.status===409)await load();}
   finally{busy=false;if(alive())render();}
  }
  function confirmRevoke(c){
   const dialog=node('dialog',null,'keys-revoke-dialog organization-dialog'),cancel=action('Cancel',()=>dialog.close()),confirm=action('Revoke',async()=>{dialog.close();await change(c,'revoke');}),actions=node('div',null,'form-actions');
   actions.append(cancel,confirm);dialog.append(node('h2','Revoke invitation?'),(()=>{const message=node('p');message.append('The link sent to ',UserUI.identity({email:c.email,name:c.contact_name},{label:c.email}),' will stop working. The company will remain.');return message;})(),actions);document.body.append(dialog);dialog.onclose=()=>dialog.remove();dialog.showModal();cancel.focus();
  }
  function createDialog(){
   const dialog=node('dialog',null,'keys-revoke-dialog organization-dialog'),form=node('form',null,'connection-editor'),message=node('p'),submit=node('button','Create and invite','primary compact'),cancel=action('Cancel',()=>dialog.close()),actions=node('div',null,'form-actions');message.setAttribute('role','status');submit.type='submit';
   dialog.setAttribute('aria-label','Create company');const {close}=FormUI.dialogHeading(form,'Create company',()=>dialog.close());form.append(node('p','The invited person becomes the first Supermanager and manages members and API keys.','field-explanation'));
   function field(label,name,type,required,max){const wrap=node('label',label,'organization-field'),input=node('input');input.name=name;input.type=type;input.required=required;input.maxLength=max;wrap.append(input);form.append(wrap);return input;}
   const name=field('Company name','name','text',true,160);field('Country','country','text',true,100);
   field('Company website','domain','text',true,2048);field('Contact name','contact_name','text',true,160);field('First Supermanager email','email','email',true,254);field('Comment (optional)','comment','text',false,2000);
   message.hidden=true;actions.append(cancel,submit);form.append(message,actions);dialog.append(form);document.body.append(dialog);FormUI.watch(form,submit,{valid:()=>FormUI.valid(form)});FormUI.refresh();
   let submitting=false,operation=crypto.randomUUID(),submittedValues=null;
   form.onsubmit=async e=>{
    e.preventDefault();if(submitting||!form.reportValidity())return;
    const input=Object.fromEntries(new FormData(form)),snapshot=JSON.stringify(input);if(submittedValues&&submittedValues!==snapshot)operation=crypto.randomUUID();submittedValues=snapshot;
    submitting=true;submit.disabled=cancel.disabled=close.disabled=true;form.setAttribute('aria-busy','true');message.hidden=false;FormUI.message(message,'Creating company…');
    try{await api('/v1/client-onboardings',{method:'POST',body:JSON.stringify({...input,operation_id:operation})});dialog.close();await load();}
    catch(error){FormUI.message(message,error.message,'error');}
    finally{submitting=false;form.removeAttribute('aria-busy');submit.disabled=cancel.disabled=close.disabled=false;FormUI.update(form);}
   };
   dialog.addEventListener('cancel',e=>{if(submitting)e.preventDefault();});dialog.onclose=()=>{dialog.remove();create.focus();};dialog.showModal();name.focus();
  }
  await load();
  async function poll(){if(!alive()){clearTimeout(timer);return;}if(!busy&&!document.hidden){const pending=new Set(items.filter(c=>c.status!=='accepted').map(c=>c.organization_id));await load();if(alive()&&items.some(c=>c.status==='accepted'&&pending.has(c.organization_id)))await refresh();}if(alive())timer=setTimeout(poll,5000);}
  if(alive())timer=setTimeout(poll,5000);
 }};
})();
