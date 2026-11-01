(() => {
  'use strict';
  const input = document.querySelector('#apiKey'), status = document.querySelector('#connectionState');
  const create = document.querySelector('#createGenerationKey'), forgetButton = document.querySelector('#forgetGenerationKey');
  const restore = document.querySelector('#restoreGenerationKey');
  const managedHelp=document.querySelector('#managedGenerationHelp'),keyPolicy=document.querySelector('#generationKeyPolicy');
  const storageKey = 'gateway-generation-key', ttl = 24 * 60 * 60 * 1000;
  let cache={},paused={},requests={},validation=0,managedFlight=0;let saved = null, binding = '', google = false, checkedAt = 0, checking = null, expiryTimer;
  const setStatus = (message,type='') => { FormUI.message(status,message,type); status.hidden = !message; };
  const notify = () => window.dispatchEvent(new Event('gateway-key-change'));
  function persist(){try{sessionStorage.setItem(storageKey,JSON.stringify({binding,keys:cache,paused,requests}))}catch{}}
  const workspaceID=()=>window.Workspace?.id()||'personal';
  function syncActions(){const active=saved?.source==='managed'&&saved.workspaceID===workspaceID();create.textContent=active?'Playground key saved':'Create Playground key';create.disabled=active||(!systemAdministrator&&serviceInfo.console?.personal_providers===false&&!serviceInfo.console?.shared_resources);managedHelp.hidden=!google||active;}
  function forget(message = '', pause = true) {
    validation++;if(saved)delete cache[saved.workspaceID];persist();
    if(pause&&google){paused[workspaceID()]=true;persist();}
    saved = null; input.value = ''; clearTimeout(expiryTimer);
    setStatus(message); forgetButton.disabled = true;syncActions(); notify();
  }
  function value() {
    if (saved && Date.now() >= saved.expiresAt) forget('The 24-hour storage period ended. Paste a key or generate a new one.');
    return saved&&(!window.Workspace?.ready()||saved.workspaceID===Workspace.id())?saved.secret:'';
  }
  function remember(secret, expiresAt = Date.now() + ttl, source='manual') {
    saved = {secret, expiresAt, binding,workspaceID:workspaceID(),source};cache[saved.workspaceID]=saved;delete paused[saved.workspaceID]; input.value = secret; forgetButton.disabled = false;
    syncActions();
    keyPolicy.textContent=source==='managed'?'Playground key saved in your account for this workspace. Forget clears this tab only; sign-out clears local access.':'Pasted keys stay in this tab for 24h; cleared on sign-out. Forget clears this tab without revoking the key.';
    let persisted = true;
    try { sessionStorage.setItem(storageKey, JSON.stringify({binding,keys:cache,paused,requests})); } catch { persisted = false; }
    setStatus(persisted ? '' : 'Browser storage unavailable. This key will be forgotten on reload.');
    clearTimeout(expiryTimer); expiryTimer = setTimeout(() => forget('The 24-hour storage period ended. Enter a key again.'), Math.max(0, expiresAt - Date.now()));
    notify();
  }
  async function ensureSession(force = false) {
    if (!google) return;
    if (!force && Date.now() - checkedAt < 15000) return;
    if (!checking) {const expected=binding;const pending = (async () => {
      const response = await fetch('/v1/me', {credentials:'same-origin', cache:'no-store'});
      const user = response.ok ? await response.json() : null;
      if(expected!==binding)return;
      if (!user || (user.session_id || user.id) !== binding) {
        cache={};paused={};requests={};forget('Your sign-in session ended or changed. Sign in again and enter a key.',false);
        throw new Error('Your sign-in session ended or changed. Refresh and sign in again.');
      }
      checkedAt = Date.now();
    })();const wrapped=pending.finally(()=>{if(checking===wrapped)checking=null});checking=wrapped;}
    return checking;
  }
  function initialize(user) {
    validation++;saved=null;input.value='';clearTimeout(expiryTimer);checking=null;
    google = !!user; binding = user ? (user.session_id || user.id) : 'api-key-mode'; checkedAt = Date.now();
    create.hidden = !google;
    syncActions();
    let stored;try{stored=JSON.parse(sessionStorage.getItem(storageKey)||'null')}catch{}
    cache=stored?.binding===binding?(stored.keys||{}):{};
    paused=stored?.binding===binding?(stored.paused||{}):{};requests=stored?.binding===binding?(stored.requests||{}):{};
    const cached=cache[window.Workspace?.id()||'personal']|| (stored?.binding===binding&&stored.secret?stored:null);
    if(cached&&cached.source!=='managed'&&cached.expiresAt>Date.now()&&cached.expiresAt<=Date.now()+ttl)manualConnect(cached.secret,cached.expiresAt);
    else{forget('',false);if(google&&!paused[workspaceID()])managed(false);}
  }
  function manualConnect(secret,expiresAt=Date.now()+ttl){const pending=connect(secret,expiresAt);const token=validation;pending.catch(e=>{if(token===validation){setStatus(e.message,'error');input.value=saved?.secret||''}});}
  let connecting=null,connectingSecret='',connectingVersion=0;
  function connect(secret,expiresAt=Date.now()+ttl,source='manual'){
    if(connecting&&connectingSecret===secret&&connectingVersion===validation)return connecting;
    connectingSecret=secret;const pending=connectAttempt(secret,expiresAt,source);connectingVersion=validation;
    connecting=pending.finally(()=>{if(connecting===wrapped)connecting=null});const wrapped=connecting;return wrapped;
  }
  async function connectAttempt(secret,expiresAt,source){
    const token=++validation;await ensureSession();
    if(token!==validation)return;
    const response=await fetch('/v1/key',{headers:{Authorization:'Bearer '+secret},credentials:'omit',cache:'no-store'});
    const result=await response.json();if(token!==validation)return;
    if(!response.ok||!result.key){throw Error(result.error?.message||'Enter a valid personal or organization API key.');}
    if(window.Workspace?.ready()&&!await Workspace.acceptKey(result.key)){input.value=saved?.secret||'';return;}
    if(token!==validation)return;remember(secret,expiresAt,source);
  }
  function activateWorkspace(id){
    validation++;saved=null;input.value='';clearTimeout(expiryTimer);forgetButton.disabled=true;syncActions();notify();setStatus('');
    const cached=cache[id];if(cached&&cached.source!=='managed'&&cached.expiresAt>Date.now())manualConnect(cached.secret,cached.expiresAt);else if(google&&!paused[id])managed(false);
  }
  // Manual entry always wins over a late account restore, even before blur.
  input.addEventListener('input',()=>{validation++;});
  input.addEventListener('change',()=>{const secret=input.value.trim();if(!secret){forget();return;}manualConnect(secret);});
  forgetButton.addEventListener('click', () => { forget(); input.focus(); });
  async function managed(creating) {
    if(!google)return;
    const token=++validation,flight=++managedFlight,login=binding,id=workspaceID();
    delete paused[id];persist();
    const fresh=()=>token===validation&&login===binding&&id===workspaceID();
    create.disabled = restore.disabled = true; setStatus(creating?'Creating Playground key…':'Restoring Playground key…');
    try {
      await ensureSession(true);
      if(!fresh())return;
      if(creating&&!requests[id]){requests[id]=crypto.randomUUID();persist();}
      const response=await fetch('/v1/playground-key/'+(creating?'create':'restore'),{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json'},body:JSON.stringify({organization_id:id==='personal'?'':id,...(creating?{request_id:requests[id]}:{})})});
      const result=await response.json();if(!fresh())return;
      await ensureSession(true);
      if(!fresh())return;
      if(!response.ok){if(creating&&[400,403,409,410].includes(response.status)){delete requests[id];persist();}if(response.status===410)forget(result.error?.message||'Saved key is inactive. Create a new key explicitly.');else if(response.status===404&&!creating)setStatus('Create a Playground key to save it to your account, or paste an existing key.');else throw Error(result.error?.message||'Saved key unavailable');return;}
      if(!result.secret||result.key?.purpose!=='playground_templates')throw Error('Saved key unavailable');
      const pending=connect(result.secret,Date.now()+ttl,'managed'),connected=validation;
      try{await pending;}catch(error){if(connected===validation&&login===binding&&id===workspaceID())setStatus(error.message,'error');return;}
      if(saved?.secret===result.secret){delete requests[id];persist();}
    } catch (error) { if(fresh())setStatus(error.message,'error'); }
    finally { if(flight===managedFlight&&login===binding){syncActions();restore.disabled=false;} }
  }
  create.addEventListener('click',()=>managed(true));restore.addEventListener('click',()=>managed(false));
  window.addEventListener('storage', event => { if (event.key === 'gateway-sign-out') {cache={};paused={};requests={};forget('Signed out. The saved generation key was cleared.',false);} });
  window.addEventListener('focus', () => { value(); if (google) ensureSession(true).catch(() => {}); });
  window.addEventListener('pageshow', () => { value(); if (google) ensureSession(true).catch(() => {}); });
  setInterval(() => { if (google && saved) ensureSession().catch(() => {}); }, 15000);
  document.querySelector('#manageGenerationKeys').onclick=e=>{e.preventDefault();showConsoleSection('keys')};
  window.GenerationKey = {value, initialize, forget, activateWorkspace, ensureSession, signOut() { cache={};paused={};requests={};forget('Signed out.',false);try{sessionStorage.removeItem(storageKey)}catch{} try { localStorage.setItem('gateway-sign-out', String(Date.now())); } catch {} }};
})();
