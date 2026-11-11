(() => {
 'use strict';
 const endpoint='/v1/system/remote-workspace/authorization';
 const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
 function reservePopup(){try{const popup=window.open('about:blank','_blank','popup,width=700,height=850');if(popup){popup.opener=null;const meta=popup.document.createElement('meta');meta.name='referrer';meta.content='no-referrer';popup.document.head.append(meta);}return popup;}catch{return null;}}
 async function recoveryPage(cursor=''){
  try{const value=await api(endpoint+'/recovery'+(cursor?'?cursor='+encodeURIComponent(cursor):''));if(!Array.isArray(value.items)||!Number.isSafeInteger(value.current_connection_revision))throw Error('Connection recovery status could not be read.');return value;}
  catch(e){if([404,405].includes(e.status))return null;throw e;}
 }
 function recoveryID(value,destination){
  // An idempotency label, never authority. This existing bundled hash also
  // works on supported HTTP installations without WebCrypto or browser storage.
  if(typeof window.sha256!=='function')throw Error('Connection recovery could not load. Reopen this page.');
  const h=window.sha256(JSON.stringify(['legacy-recovery/v1',value.id,value.version,value.connection_revision,destination])).slice(0,32).split('');h[12]='8';h[16]=((parseInt(h[16],16)&3)|8).toString(16);
  const raw=h.join('');return [raw.slice(0,8),raw.slice(8,12),raw.slice(12,16),raw.slice(16,20),raw.slice(20)].join('-');
 }
 async function mountRecovery(parent,current,options={}){
  const panel=node('details',undefined,'remote-recovery'),summary=node('summary','Earlier connection requests'),list=node('div',undefined,'remote-recovery-list'),message=node('p');message.setAttribute('role','status');panel.append(summary,list,message);panel.hidden=true;parent.append(panel);
  let busy=false,serial=0;const active=()=>current()&&panel.isConnected;
  const lock=value=>{busy=value;if(active()){options.lock?.(value);if(!options.lock)window.RemoteWorkspace?.setBusy(value);for(const b of panel.querySelectorAll('button'))b.disabled=value;}};
  async function load(cursor='',append=false){const ticket=++serial;const value=await recoveryPage(cursor);if(!active()||ticket!==serial)return;render(value,append);}
  function render(value,append){
   if(!append)list.replaceChildren();if(!value){panel.hidden=true;return;}panel.hidden=!value.items.length&&!append&&!value.next_cursor;
   for(const item of value.items){
    const row=node('div',undefined,'remote-recovery-item'),text=node('p',item.origin+' — '+(item.state==='cancelled'?'Cancellation confirmed':'Cancellation unconfirmed'));row.append(text);
    if(item.state==='pending'){const b=node('button','Retry cancellation','secondary');b.type='button';b.onclick=async()=>{if(busy||!active()||window.RemoteWorkspace?.isBusy())return;const ticket=++serial;lock(true);try{await api(endpoint+'/recovery/cleanup',{method:'POST',body:JSON.stringify({id:item.id})});if(!active()||ticket!==serial)return;FormUI.message(message,'');await load();}catch(e){if(active()&&ticket===serial)FormUI.message(message,e.message+' Cancellation is unconfirmed; the original request is kept.','error');}finally{if(active())lock(false);}};row.append(b);}list.append(row);
   }
   if(value.next_cursor){const more=node('button','Earlier requests','secondary');more.type='button';more.onclick=async()=>{if(busy||!active()||window.RemoteWorkspace?.isBusy())return;lock(true);try{await load(value.next_cursor,true);if(active())more.remove();}catch(e){if(active())FormUI.message(message,e.message,'error');}finally{if(active())lock(false);}};list.append(more);}
  }
  try{if(options.value!==undefined)render(options.value,false);else await load();}catch(e){if(active()){panel.hidden=false;FormUI.message(message,e.message,'error');}}
  return {reload:load};
 }
 async function mount(parent,connection,onConnected,current,options={}){
  const section=node('section'),status=node('p','Loading connection…'),actions=node('div',undefined,'dialog-actions'),details=node('div');status.setAttribute('role','status');section.append(details,status,actions);parent.append(section);
  let site,attempt,busy=false,timer,stopped=false,reserved=options.popup,phase=0,archiveView;
  const active=()=>!stopped&&current()&&section.isConnected;
  const owns=token=>active()&&token===phase;
  const normalized=value=>{const url=new URL(value);url.hostname=url.hostname.replace(/\.$/,'');return url.origin;};
  const sameOrigin=(a,b)=>{try{return normalized(a)===normalized(b);}catch{return false;}};
  const validSaved=saved=>saved.connected&&saved.provisioning==='dedicated'&&saved.organization_id&&saved.context&&saved.operation_scope&&Number.isSafeInteger(saved.version)&&Number.isSafeInteger(attempt?.base_version)&&saved.version===attempt.base_version+1&&sameOrigin(saved.origin,attempt.origin)&&(!connection?.connected||saved.organization_id===connection.organization_id);
  async function snapshot(){const [pending,saved,recovery]=await Promise.all([api(endpoint),api('/v1/system/remote-workspace'),recoveryPage()]);return {pending,saved,recovery};}
  const archived=(state,view)=>state.recovery?.items.some(item=>item.version===view.base_version+1&&sameOrigin(item.origin,view.origin));
  async function complete(saved,token=phase){
   if(!owns(token)||!validSaved(saved))throw Error('Connection could not be confirmed. Check its status again.');
   const state=await snapshot();if(!owns(token))return;
   if(archived(state,attempt)||state.pending.id&&state.pending.id!==attempt.id||!validSaved(state.saved)||state.saved.context!==saved.context||state.saved.version!==saved.version)throw Error('The saved connection changed. Check its status before continuing.');
   attempt=null;stopped=true;phase++;lock(false);closeBlank();await onConnected(state.saved);
  }
  async function reconcile(token=phase){
   const state=await snapshot();if(!owns(token))return false;
   if(state.pending.id&&state.pending.id!==attempt?.id)throw Error('A different connection request is saved. Check its status before continuing.');
   if(attempt&&!archived(state,attempt)&&validSaved(state.saved)){const confirmed=state.pending.id?await api(endpoint+'/poll',{method:'POST',body:JSON.stringify({id:state.pending.id})}):state.saved;await complete(confirmed,token);return true;}
   if(state.saved.version!==(connection?.version||0)||state.saved.connected!==!!connection?.connected)throw Error('The saved connection changed. Check its status before continuing.');
   return state.pending.id?false:'absent';
  }
  async function showSnapshot(state,message=''){
   connection=state.saved;if(state.pending.id)waiting(state.pending);else ready(message);
   await archiveView?.reload();
  }
  function offerRecovery(){if(attempt?.recovery_allowed===true&&Number.isSafeInteger(attempt.current_connection_revision)&&Number.isSafeInteger(connection?.version)&&!actions.querySelector('[data-recover]')){const b=button('Recover connection',false,reviewRecovery);b.dataset.recover='true';}}
  async function reviewRecovery(){
   if(!attempt||busy)return;const savedAttempt={...attempt},value={id:attempt.id,version:connection.version,connection_revision:attempt.current_connection_revision,consent:true};
   try{value.operation_id=recoveryID(value,normalized(site.central_origin));}catch(e){FormUI.message(status,e.message,'error');return;}
   const token=++phase;clearTimeout(timer);closeBlank();details.replaceChildren(node('p','The earlier request may still have remote access. Keep its cancellation details and continue with the central server.'));actions.replaceChildren();FormUI.message(status,'');
   button('Back',false,()=>waiting(savedAttempt));button('Keep recovery and continue',true,()=>recover(value,token));
  }
  async function recover(value,token){
   if(!owns(token)||busy)return;lock(true);FormUI.message(status,'Recovering connection…');
   try{
    await api(endpoint+'/recover',{method:'POST',body:JSON.stringify(value)});
    if(!owns(token))return;
    const state=await snapshot();if(!owns(token))return;
    let page=state.recovery,receipt;const cursors=new Set();
    while(page){receipt=page.items.find(item=>item.id===value.operation_id);if(receipt||!page.next_cursor)break;if(cursors.has(page.next_cursor))throw Error('Recovery status could not be read.');cursors.add(page.next_cursor);page=await recoveryPage(page.next_cursor);if(!owns(token))return;}
    if(!receipt||receipt.version!==value.version+1||receipt.connection_revision!==value.connection_revision+1||state.pending.id===value.id||state.saved.version<receipt.version||state.recovery.current_connection_revision<receipt.connection_revision)throw Error('Recovery could not be confirmed. Retry the same recovery.');
    await showSnapshot(state,'The earlier request is kept for cancellation.');
   }catch(e){
    if(!owns(token))return;
    // Retry only the frozen operation; never refresh its version or revision
    // automatically after conflict. A later load reads authoritative state.
    FormUI.message(status,e.message+' The original recovery details are kept.','error');actions.replaceChildren();button('Check saved request',false,load);if(e.status!==409)button('Retry same recovery',true,()=>recover(value,token));
   }finally{if(active())lock(false);}
  }
  const closeBlank=()=>{try{reserved?.close();}catch{}reserved=null;};
  function lock(value){busy=value;if(current()&&section.isConnected)window.RemoteWorkspace?.setBusy(value);for(const b of actions.querySelectorAll('button'))b.disabled=value;}
  function button(label,primary,action){const b=node('button',label,primary?'primary compact':'secondary');b.type='button';b.onclick=()=>{if(!busy&&active())void action();};actions.append(b);return b;}
  function verification(view){const destination=new URL(site.central_origin),url=new URL(view.verification_uri);if(!/^[A-Z2-7]{13}$/.test(view.user_code)||url.origin!==destination.origin||url.pathname!=='/'||url.search||url.username||url.password||url.hash!=='#workspace_authorize='+view.user_code)throw Error('The saved request has an unexpected destination. Cancel it before starting again.');return url.href;}
  function schedule(delay=10000){clearTimeout(timer);if(active()&&attempt)timer=setTimeout(poll,delay);}
  function ready(message=''){phase++;attempt=null;details.replaceChildren(node('p','Your installation’s name and address will be shared with the central server.','field-explanation'));actions.replaceChildren();button(connection?.connected?'Reconnect':'Connect to central server',true,start);FormUI.message(status,message);}
  async function cancel(){if(!attempt)return;const token=phase,id=attempt.id;lock(true);clearTimeout(timer);try{await api(endpoint,{method:'DELETE',body:JSON.stringify({id})});if(!owns(token))return;closeBlank();const state=await snapshot();if(!owns(token))return;await showSnapshot(state,state.pending.id?'':'Connection cancelled.');}catch(e){if(owns(token))try{const result=await reconcile(token);if(result===true)return;if(result==='absent'){closeBlank();ready('The request has ended. Your saved connection is unchanged.');return;}FormUI.message(status,e.message+' Your request is kept.','error');offerRecovery();}catch{if(owns(token)){FormUI.message(status,e.message+' The outcome is unknown. Check the request before starting another.','error');offerRecovery();}}}finally{if(active())lock(false);}}
  function waiting(view){
   phase++;attempt=view;clearTimeout(timer);details.replaceChildren(node('p','Check this code in the central server: '+view.user_code));actions.replaceChildren();let uri;
   try{uri=verification(view);}catch(e){FormUI.message(status,e.message,'error');button('Cancel request',true,cancel);offerRecovery();closeBlank();return;}
   const link=node('a','Open central server','primary compact');link.href=uri;link.target='_blank';link.rel='noopener noreferrer';actions.append(link);button('Check connection',false,poll);button('Cancel',false,cancel);
   if(reserved){try{reserved.location.replace(uri);reserved=null;}catch{closeBlank();}}
   FormUI.message(status,'Finish connecting in the central server window. If it did not open, choose Open central server.');if(Date.parse(view.expires_at)<=Date.now())offerRecovery();schedule(5000);
  }
  async function recoverStart(error){
   try{const saved=await api(endpoint);if(!active())return;if(saved.id){waiting(saved);clearTimeout(timer);FormUI.message(status,error.message+' The same request is kept. Retry it or cancel before starting again.','error');button('Retry request',false,start);offerRecovery();return;}}catch{/* Never replace an uncertain request automatically. */}
   if(active()){closeBlank();details.replaceChildren();actions.replaceChildren();FormUI.message(status,error.message+' Check the saved request before trying again.','error');button('Check saved request',true,load);}
  }
  async function start(){
   if(busy||!active())return;if(!reserved)reserved=reservePopup();lock(true);FormUI.message(status,'Connecting to central server…');
   try{const view=await api(endpoint,{method:'POST',body:JSON.stringify({version:connection?.version||0,consent:true})});if(active())waiting(view);else closeBlank();}
   catch(e){if(active())await recoverStart(e);else closeBlank();}finally{lock(false);}
  }
  async function poll(){
   if(!active()||busy||!attempt)return;const token=phase,id=attempt.id;if(document.hidden){schedule(5000);return;}clearTimeout(timer);
   if(!Number.isFinite(Date.parse(attempt.expires_at))||Date.parse(attempt.expires_at)<=Date.now()){lock(true);try{if(await reconcile(token)===true)return;}catch{/* Do not discard the saved proof. */}finally{lock(false);}if(owns(token)){FormUI.message(status,'This connection request expired. Cancel it or recover the connection.','error');offerRecovery();}return;}
   lock(true);try{const saved=await api(endpoint+'/poll',{method:'POST',body:JSON.stringify({id})});if(!owns(token))return;
    await complete(saved,token);
   }catch(e){if(!owns(token))return;if(e.status===428)FormUI.message(status,'Finish connecting in the central server window.');else if(e.status===429)FormUI.message(status,'Central server asked us to wait. Checking again shortly.');else{
    if(![403,409,410].includes(e.status))try{const result=await reconcile(token);if(result===true)return;if(result==='absent'){FormUI.message(status,'The request has ended without a new saved connection. Reload to inspect your connection.','error');return;}}catch{/* Keep the original proof and retry only this request. */}
    FormUI.message(status,e.status===410?'This connection request was declined, cancelled or expired. Cancel it before starting again.':e.message,'error');offerRecovery();if([403,409,410].includes(e.status))return;
   }schedule();}finally{lock(false);}
  }
  async function load(){
   if(busy||!active())return false;const token=++phase;lock(true);try{const state=await snapshot();if(!owns(token))return false;await showSnapshot(state);return true;}catch(e){if(owns(token)){FormUI.message(status,e.message,'error');actions.replaceChildren();button('Check saved request',true,load);}return false;}finally{if(active())lock(false);}
  }
  try{site=await window.GatewaySite?.ready;if(!active()){closeBlank();return;}if(!site?.central_origin)throw Error('Central server connection details are unavailable. Reload this page.');if(site.installation_role==='central'){closeBlank();details.replaceChildren();FormUI.message(status,'This central server manages companies and cannot connect to another server.');return;}
   archiveView=await mountRecovery(section,active,{lock,value:null});const loaded=await load();if(options.start&&loaded&&active()&&!busy){if(attempt){const uri=verification(attempt);if(reserved){reserved.location.replace(uri);reserved=null;}}else await start();}
  }catch(e){closeBlank();if(active())FormUI.message(status,e.message,'error');}
 }
 window.RemoteAuthorization={mount,mountRecovery,reservePopup};
})();
