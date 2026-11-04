(() => {
 'use strict';
 const remembered='gateway-workspace-authorization';let dialog,actor,revision=0,writing=null,memory,completion,authorizationRevision;
 const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
 // Presentation only: keep the authoritative origin intact and omit its repeated host label.
 function addressLabel(name,origin){
  if(!name||name===origin)return true;
  try{const url=new URL(origin);return ['http:','https:'].includes(url.protocol)&&!url.username&&!url.password&&url.pathname==='/'&&!url.search&&!url.hash&&name===url.host;}catch{return false;}
 }
 function capture(){const value=location.hash.match(/^#workspace_authorize=([A-Z2-7]{13})$/)?.[1];if(value){memory={code:value,time:Date.now()};try{sessionStorage.setItem(remembered,JSON.stringify(memory));history.replaceState(null,'',location.pathname+location.search);}catch{/* Keep the link usable when session storage is unavailable. */}}try{const stored=JSON.parse(sessionStorage.getItem(remembered)||'null');if(stored)memory=stored;}catch{}return memory&&/^[A-Z2-7]{13}$/.test(memory.code)&&Number.isFinite(memory.time)&&Date.now()-memory.time>=0&&Date.now()-memory.time<15*60*1000?memory.code:null;}
 const clearCompletion=()=>{completion?.remove();completion=null;};
 function finishApproval(){
  memory=null;try{sessionStorage.removeItem(remembered);}catch{}
  revision++;dialog?.close();dialog?.remove();clearCompletion();
  // The existing popup deliberately has no opener. Only close this window;
  // the browser decides whether it is script-closable. Never redirect or message.
  try{window.close();}catch{}
  if(!window.closed){completion=node('p',undefined,'message');completion.dataset.workspaceApproved='';completion.setAttribute('role','status');(document.querySelector('main')||document.body).prepend(completion);FormUI.message(completion,'Approved. You can close this tab and return to your installation.');}
 }
 const close=()=>{if(writing===revision)return;revision++;dialog?.close();};
 function shell(title){clearCompletion();revision++;dialog?.close();dialog?.remove();dialog=node('dialog',undefined,'keys-revoke-dialog sales-dialog remote-dialog connection-dialog');const box=node('form');dialog.append(box);document.body.append(dialog);FormUI.dialogHeading(box,title,close);dialog.addEventListener('cancel',e=>{e.preventDefault();close();});dialog.showModal();return box;}
 async function authorize(code){
  if(!/^[A-Z2-7]{13}$/.test(code))return;
  const account=actor,box=shell('Connect this installation?'),version=revision,status=node('p','Loading request…');status.setAttribute('role','status');box.append(status);authorizationRevision=version;
  const current=()=>version===revision&&dialog.open&&actor?.id===account?.id&&actor?.session_id===account?.session_id;
  try{const data=await api('/v1/workspace-authorizations/inspect',{method:'POST',body:JSON.stringify({user_code:code})});if(!current())return;
   const request=data.request,renewal=request.provisioning==='renewal',companyAccess=request.access==='company-api-v1';box.replaceChildren();FormUI.dialogHeading(box,renewal?'Reconnect this installation?':'Connect this installation?',close);
   if(window.UserUI&&account)box.append(UserUI.identity(account,{secondary:true}));else box.append(node('p','Signed in as '+(account?.email||account?.name||'your account')));
   const accountSwitch=node('button','Use a different account','secondary');accountSwitch.type='button';accountSwitch.onclick=()=>{if(writing===version)return;const logout=document.querySelector('#logoutButton');if(logout){close();logout.click();}else FormUI.message(status,'Close this dialog, sign in with another account, then reopen this request.');};box.append(accountSwitch);
   if(!addressLabel(request.client_name,request.client_origin))box.append(node('strong',request.client_name));
   box.append(node('p',request.client_origin),node('p','Check that this code matches the one in your installation: '+request.user_code));
   if(request.status!=='pending'){if(request.status==='approved'||request.status==='issued')finishApproval();else box.append(node('p','This connection request has ended. Return to your installation.'));return;}
   if(!['new','renewal'].includes(request.provisioning)){box.append(node('p','This is an older connection. Keep its existing company and manage its current key; no access has been changed.'));return;}
   box.append(node('p',companyAccess?'Allow this installation to use your company’s generation, members, billing and API keys. Access remains active until revoked. No credit or system administration is granted.':'Generation-only access for up to 30 days. This does not add credit or grant account administration.','field-explanation'));
   const replace=node('input');replace.type='checkbox';if(renewal){const label=node('label',undefined,'system-toggle');label.append(replace,node('span','Revoke previous access after reconnecting'));box.append(label);}
   const actions=node('div',undefined,'dialog-actions'),cancel=node('button','Cancel','secondary'),approve=node('button',renewal?'Reconnect':'Connect','primary compact');cancel.type='button';approve.type='submit';actions.append(cancel,approve);status.textContent='';box.append(status,actions);FormUI.refresh();let busy=false,submitted;
   async function send(deny){if(busy||!current())return;if(submitted&&submitted.deny!==deny){FormUI.message(status,'The earlier response is unknown. Retry the same action before changing it.','error');return;}submitted||={user_code:code,...(companyAccess?{access:'company-api-v1'}:{}),deny,consent:!deny,replace_previous:renewal&&replace.checked};busy=true;writing=version;replace.disabled=true;approve.disabled=cancel.disabled=accountSwitch.disabled=true;FormUI.message(status,deny?'Cancelling…':'Connecting…');
    try{await api('/v1/workspace-authorizations/approve',{method:'POST',body:JSON.stringify(submitted)});if(!current())return;
     if(!deny){finishApproval();return;}
     memory=null;try{sessionStorage.removeItem(remembered);}catch{}box.replaceChildren();FormUI.dialogHeading(box,'Connection cancelled',close);box.append(node('p','Connection cancelled. Return to your installation.'));
     const done=node('button','Close this window','secondary');done.type='button';done.onclick=()=>{try{window.close();}catch{};};box.append(done);
    }catch(e){if(current()){FormUI.message(status,e.message+' Retry the same action to confirm its outcome.','error');approve.disabled=!!submitted.deny;cancel.disabled=!submitted.deny;}}finally{busy=false;if(writing===version)writing=null;if(current()){approve.disabled=!!submitted?.deny;cancel.disabled=!submitted?.deny;accountSwitch.disabled=!!submitted;}}
   }
   box.onsubmit=e=>{e.preventDefault();void send(false);};cancel.onclick=()=>send(true);approve.focus();
  }catch(e){if(current())FormUI.message(status,e.message,'error');}
 }
 const adminAccess=()=>typeof systemAdministrator!=='undefined'&&systemAdministrator&&typeof consolePolicy!=='undefined'&&consolePolicy.installation_role==='central';
 const activeGrant=grant=>!grant.revoked_at&&(!grant.expires_at||Date.parse(grant.expires_at)>Date.now());
 const grantBase=company=>'/v1/system/organizations/'+encodeURIComponent(company)+'/installation-access';
 let administration=false;
 function closeInstallations(){if(administration){revision++;dialog?.close();administration=false;}}
 async function grants(company,isCurrent,refresh){
  if(!adminAccess()||!isCurrent())return;
  const box=shell('Installation access'),version=revision,status=node('p','Loading…'),account=actor;
  administration=true;status.setAttribute('role','status');box.append(node('strong',company.name),status);
  const current=()=>version===revision&&dialog.open&&adminAccess()&&isCurrent()&&actor?.id===account?.id&&actor?.session_id===account?.session_id;
  const retry=node('button','Retry','secondary');retry.type='button';retry.hidden=true;box.append(retry);
  let loading=false;const controls=[];
  const sync=()=>controls.forEach(({grant,button})=>{button.disabled=writing===version||!activeGrant(grant);});
  async function load(){
   if(loading||!current())return;loading=true;retry.hidden=true;FormUI.message(status,'Loading…');
   try{
    const result=await api(grantBase(company.id));if(!current())return;
    FormUI.message(status,result.data.length?'':'No installation access.');
    for(const grant of result.data){
     const row=node('article',undefined,'remote-job'),issued=new Date(grant.created_at).toLocaleString(),scope=grant.access==='company-api-v1'?'Company access':'Generation-only access';
     row.append(node('strong',grant.name),node('p',grant.client_origin),node('p',scope+' · '+grant.prefix),node('p','Issued '+issued),node('p',grant.expires_at?'Access expires '+new Date(grant.expires_at).toLocaleString():'Active until revoked'));
     const revoke=node('button',grant.revoked_at?'Revoked':activeGrant(grant)?'Revoke':'Expired','secondary danger');revoke.type='button';revoke.disabled=!activeGrant(grant);let busy=false;
     revoke.onclick=async()=>{
      if(busy||writing===version||!current()||!activeGrant(grant))return;
      if(!confirm('Revoke '+scope.toLowerCase()+' for '+grant.name+' ('+grant.client_origin+'), issued '+issued+' ('+grant.prefix+')? Other access, jobs, charges and files are preserved.'))return;
      busy=true;writing=version;sync();FormUI.message(status,'Revoking…');
      try{await api(grantBase(company.id)+'/'+encodeURIComponent(grant.id),{method:'DELETE'});if(!current())return;grant.revoked_at=new Date().toISOString();revoke.textContent='Revoked';FormUI.message(status,'');void refresh();}
      catch(e){if(current())FormUI.message(status,e.message,'error');}
      finally{busy=false;if(writing===version)writing=null;if(current())sync();}
     };
     controls.push({grant,button:revoke});const actions=node('div',undefined,'dialog-actions');actions.append(revoke);row.append(actions);box.append(row);
    }
   }catch(e){if(current()){FormUI.message(status,e.message,'error');retry.hidden=false;}}
   finally{loading=false;}
  }
  retry.onclick=load;void load();
 }
 function organizationActions(companies,isCurrent){
  const actions=new Map();if(!adminAccess())return actions;
  let loading=false,failed=false;
  const current=()=>isCurrent()&&adminAccess();
  async function refresh(){
   if(loading||!current())return;loading=true;
   for(const button of actions.values())button.disabled=true;
   try{
    const result=await api('/v1/system/installation-access');if(!current())return;failed=false;
    const active=new Map(result.data.map(item=>[item.organization_id,item.active_count]));
    for(const [id,button]of actions){button.textContent='Revoke';button.disabled=!(active.get(id)>0);}
   }catch{if(current()){failed=true;for(const button of actions.values()){button.textContent='Retry access';button.disabled=false;}}}
   finally{loading=false;}
  }
  for(const company of companies){
   const revoke=node('button','Revoke','secondary danger');revoke.type='button';revoke.disabled=true;revoke.setAttribute('aria-label','Revoke installation access for '+company.name);
   revoke.onclick=()=>{if(!current()||!revoke.isConnected)return;if(failed){void refresh();return;}void grants(company,()=>current()&&revoke.isConnected,refresh);};
   actions.set(company.id,revoke);
  }
  queueMicrotask(refresh);return actions;
 }
 capture();
 window.addEventListener('popstate',()=>{clearCompletion();if(authorizationRevision===revision){revision++;dialog?.close();}});
 window.addEventListener('hashchange',()=>{clearCompletion();if(/^#workspace_authorize=[A-Z2-7]{13}$/.test(location.hash)){const code=capture();if(actor&&code)void authorize(code);}});
 window.WorkspaceAuthorization={async initialize(user){if(actor?.id!==user?.id||actor?.session_id!==user?.session_id){clearCompletion();revision++;dialog?.close();}actor=user;document.querySelector('#workspaceGrantsButton')?.remove();const code=capture();if(!user){close();return false;}if(code){await authorize(code);return true;}return false;},authorize,organizationActions,closeInstallations};
})();
