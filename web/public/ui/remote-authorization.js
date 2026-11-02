(() => {
 'use strict';
 const endpoint='/v1/system/remote-workspace/authorization';
 const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
 function reservePopup(){try{const popup=window.open('about:blank','_blank','popup,width=700,height=850');if(popup){popup.opener=null;const meta=popup.document.createElement('meta');meta.name='referrer';meta.content='no-referrer';popup.document.head.append(meta);}return popup;}catch{return null;}}
 async function mount(parent,connection,onConnected,current,options={}){
  const section=node('section'),status=node('p','Loading connection…'),actions=node('div',undefined,'dialog-actions'),details=node('div');status.setAttribute('role','status');section.append(details,status,actions);parent.append(section);
  let site,attempt,busy=false,timer,stopped=false,reserved=options.popup;
  const active=()=>!stopped&&current();
  const normalized=value=>{const url=new URL(value);url.hostname=url.hostname.replace(/\.$/,'');return url.origin;};
  const sameOrigin=(a,b)=>{try{return normalized(a)===normalized(b);}catch{return false;}};
  const validSaved=saved=>saved.connected&&saved.provisioning==='dedicated'&&saved.organization_id&&saved.context&&saved.operation_scope&&Number.isSafeInteger(saved.version)&&Number.isSafeInteger(attempt?.base_version)&&saved.version===attempt.base_version+1&&sameOrigin(saved.origin,attempt.origin)&&(!connection?.connected||saved.organization_id===connection.organization_id);
  async function complete(saved){if(!active()||!validSaved(saved))throw Error('Connection could not be confirmed. Check its status again.');attempt=null;stopped=true;lock(false);closeBlank();await onConnected(saved);}
  async function reconcile(){const pending=await api(endpoint);if(!active())return false;if(pending.id){if(pending.id!==attempt?.id)throw Error('A different connection request is saved. Reload to inspect it.');}const saved=await api('/v1/system/remote-workspace');if(!active())return false;if(validSaved(saved)){const confirmed=pending.id?await api(endpoint+'/poll',{method:'POST',body:JSON.stringify({id:pending.id})}):saved;await complete(confirmed);return true;}if(saved.version!==(connection?.version||0)||saved.connected!==!!connection?.connected)throw Error('The saved connection changed. Reload to inspect it.');return pending.id?false:'absent';}
  const closeBlank=()=>{try{reserved?.close();}catch{}reserved=null;};
  function lock(value){busy=value;if(current())window.RemoteWorkspace?.setBusy(value);for(const b of actions.querySelectorAll('button'))b.disabled=value;}
  function button(label,primary,action){const b=node('button',label,primary?'primary compact':'secondary');b.type='button';b.onclick=()=>{if(!busy&&active())void action();};actions.append(b);return b;}
  function verification(view){const destination=new URL(site.central_origin),url=new URL(view.verification_uri);if(!/^[A-Z2-7]{13}$/.test(view.user_code)||url.origin!==destination.origin||url.pathname!=='/'||url.search||url.username||url.password||url.hash!=='#workspace_authorize='+view.user_code)throw Error('The saved request has an unexpected destination. Cancel it before starting again.');return url.href;}
  function schedule(delay=10000){clearTimeout(timer);if(active()&&attempt)timer=setTimeout(poll,delay);}
  function ready(message=''){attempt=null;details.replaceChildren(node('p','Your installation’s name and address will be shared with Sociaro.','field-explanation'));actions.replaceChildren();button(connection?.connected?'Reconnect':'Connect to Sociaro',true,start);FormUI.message(status,message);}
  async function cancel(){if(!attempt)return;lock(true);clearTimeout(timer);try{await api(endpoint,{method:'DELETE',body:JSON.stringify({id:attempt.id})});if(!active())return;closeBlank();ready('Connection cancelled.');}catch(e){if(active())try{const result=await reconcile();if(result===true)return;if(result==='absent'){closeBlank();ready('The request has ended. Your saved connection is unchanged.');return;}FormUI.message(status,e.message+' Your request is kept. Try cancelling again before starting another.','error');}catch{FormUI.message(status,e.message+' The outcome is unknown. Check the request before starting another.','error');}}finally{lock(false);}}
  function waiting(view){
   attempt=view;clearTimeout(timer);details.replaceChildren(node('p','Check this code in Sociaro: '+view.user_code));actions.replaceChildren();let uri;
   try{uri=verification(view);}catch(e){FormUI.message(status,e.message,'error');button('Cancel request',true,cancel);closeBlank();return;}
   const link=node('a','Open Sociaro','primary compact');link.href=uri;link.target='_blank';link.rel='noopener noreferrer';actions.append(link);button('Check connection',false,poll);button('Cancel',false,cancel);
   if(reserved){try{reserved.location.replace(uri);reserved=null;}catch{closeBlank();}}
   FormUI.message(status,'Finish connecting in the Sociaro window. If it did not open, choose Open Sociaro.');schedule(5000);
  }
  async function recoverStart(error){
   try{const saved=await api(endpoint);if(!active())return;if(saved.id){waiting(saved);clearTimeout(timer);FormUI.message(status,error.message+' The same request is kept. Retry it or cancel before starting again.','error');button('Retry request',false,start);return;}}catch{/* Never replace an uncertain request automatically. */}
   if(active()){closeBlank();details.replaceChildren();actions.replaceChildren();FormUI.message(status,error.message+' Check the saved request before trying again.','error');button('Check saved request',true,load);}
  }
  async function start(){
   if(busy||!active())return;if(!reserved)reserved=reservePopup();lock(true);FormUI.message(status,'Connecting to Sociaro…');
   try{const view=await api(endpoint,{method:'POST',body:JSON.stringify({version:connection?.version||0,consent:true})});if(active())waiting(view);else closeBlank();}
   catch(e){if(active())await recoverStart(e);else closeBlank();}finally{lock(false);}
  }
  async function poll(){
   if(!active()||busy||!attempt)return;if(document.hidden){schedule(5000);return;}clearTimeout(timer);
   if(!Number.isFinite(Date.parse(attempt.expires_at))||Date.parse(attempt.expires_at)<=Date.now()){lock(true);try{if(await reconcile()===true)return;}catch{/* Do not discard the saved proof. */}finally{lock(false);}if(active())FormUI.message(status,'This connection request expired. Cancel it, then start again.','error');return;}
   lock(true);try{const saved=await api(endpoint+'/poll',{method:'POST',body:JSON.stringify({id:attempt.id})});if(!active())return;
    await complete(saved);
   }catch(e){if(!active())return;if(e.status===428)FormUI.message(status,'Finish connecting in the Sociaro window.');else if(e.status===429)FormUI.message(status,'Sociaro asked us to wait. Checking again shortly.');else{
    if(![403,409,410].includes(e.status))try{const result=await reconcile();if(result===true)return;if(result==='absent'){FormUI.message(status,'The request has ended without a new saved connection. Reload to inspect your connection.','error');return;}}catch{/* Keep the original proof and retry only this request. */}
    FormUI.message(status,e.status===410?'This connection request was declined, cancelled or expired. Cancel it before starting again.':e.message,'error');if([403,409,410].includes(e.status))return;
   }schedule();}finally{lock(false);}
  }
  async function load(){
   if(busy||!active())return false;lock(true);try{const saved=await api(endpoint);if(!active())return false;if(saved.id)waiting(saved);else ready();return true;}catch(e){if(active()){FormUI.message(status,e.message,'error');actions.replaceChildren();button('Check saved request',true,load);}return false;}finally{lock(false);}
  }
  try{site=await window.GatewaySite?.ready;if(!active()){closeBlank();return;}if(!site?.central_origin)throw Error('Sociaro connection details are unavailable. Reload this page.');if(site.installation_role==='central'){closeBlank();details.replaceChildren();FormUI.message(status,'This central server manages companies and cannot connect to another server.');return;}
   const loaded=await load();if(options.start&&loaded&&active()&&!busy){if(attempt){const uri=verification(attempt);if(reserved){reserved.location.replace(uri);reserved=null;}}else await start();}
  }catch(e){closeBlank();if(active())FormUI.message(status,e.message,'error');}
 }
 window.RemoteAuthorization={mount,reservePopup};
})();
