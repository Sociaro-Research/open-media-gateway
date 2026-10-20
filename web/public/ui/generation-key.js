(() => {
  'use strict';
  const input = document.querySelector('#apiKey'), status = document.querySelector('#connectionState');
  const create = document.querySelector('#createGenerationKey'), forgetButton = document.querySelector('#forgetGenerationKey');
  const storageKey = 'gateway-generation-key', ttl = 24 * 60 * 60 * 1000;
  let cache={},validation=0;let saved = null, binding = '', google = false, checkedAt = 0, checking = null, expiryTimer;
  const setStatus = (message,type='') => { FormUI.message(status,message,type); status.hidden = !message; };
  const notify = () => window.dispatchEvent(new Event('gateway-key-change'));
  function persist(){try{sessionStorage.setItem(storageKey,JSON.stringify({binding,keys:cache}))}catch{}}
  function forget(message = '') {
    validation++;if(saved)delete cache[saved.workspaceID];persist();
    saved = null; input.value = ''; clearTimeout(expiryTimer);
    setStatus(message); forgetButton.disabled = true; notify();
  }
  function value() {
    if (saved && Date.now() >= saved.expiresAt) forget('The 24-hour storage period ended. Paste a key or generate a new one.');
    return saved&&(!window.Workspace?.ready()||saved.workspaceID===Workspace.id())?saved.secret:'';
  }
  function remember(secret, expiresAt = Date.now() + ttl) {
    saved = {secret, expiresAt, binding,workspaceID:window.Workspace?.id()||'personal'};cache[saved.workspaceID]=saved; input.value = secret; forgetButton.disabled = false;
    let persisted = true;
    try { sessionStorage.setItem(storageKey, JSON.stringify({binding,keys:cache})); } catch { persisted = false; }
    setStatus(persisted ? '' : 'Browser storage unavailable. This key will be forgotten on reload.');
    clearTimeout(expiryTimer); expiryTimer = setTimeout(() => forget('The 24-hour storage period ended. Enter a key again.'), Math.max(0, expiresAt - Date.now()));
    notify();
  }
  async function ensureSession(force = false) {
    if (!google) return;
    if (!force && Date.now() - checkedAt < 15000) return;
    if (!checking) checking = (async () => {
      const response = await fetch('/v1/me', {credentials:'same-origin', cache:'no-store'});
      const user = response.ok ? await response.json() : null;
      if (!user || (user.session_id || user.id) !== binding) {
        cache={};forget('Your sign-in session ended or changed. Sign in again and enter a key.');
        throw new Error('Your sign-in session ended or changed. Refresh and sign in again.');
      }
      checkedAt = Date.now();
    })().finally(() => { checking = null; });
    return checking;
  }
  function initialize(user) {
    google = !!user; binding = user ? (user.session_id || user.id) : 'api-key-mode'; checkedAt = Date.now();
    create.hidden = !google;
    let stored;try{stored=JSON.parse(sessionStorage.getItem(storageKey)||'null')}catch{}
    cache=stored?.binding===binding?(stored.keys||{}):{};
    const cached=cache[window.Workspace?.id()||'personal']|| (stored?.binding===binding&&stored.secret?stored:null);
    if(cached&&cached.expiresAt>Date.now()&&cached.expiresAt<=Date.now()+ttl)connect(cached.secret,cached.expiresAt).catch(e=>setStatus(e.message,'error'));else forget();
  }
  let connecting=null,connectingSecret='',connectingVersion=0;
  function connect(secret,expiresAt=Date.now()+ttl){
    if(connecting&&connectingSecret===secret&&connectingVersion===validation)return connecting;
    connectingSecret=secret;const pending=connectAttempt(secret,expiresAt);connectingVersion=validation;
    connecting=pending.finally(()=>{if(connecting===wrapped)connecting=null});const wrapped=connecting;return wrapped;
  }
  async function connectAttempt(secret,expiresAt){
    const token=++validation;await ensureSession();
    const response=await fetch('/v1/key',{headers:{Authorization:'Bearer '+secret},credentials:'omit',cache:'no-store'});
    const result=await response.json();if(token!==validation)return;
    if(!response.ok||!result.key){throw Error(result.error?.message||'Enter a valid personal or organization API key.');}
    if(window.Workspace?.ready()&&!await Workspace.acceptKey(result.key)){input.value=saved?.secret||'';return;}
    if(token!==validation)return;remember(secret,expiresAt);
  }
  function activateWorkspace(id){
    validation++;saved=null;input.value='';clearTimeout(expiryTimer);forgetButton.disabled=true;notify();setStatus('');
    const cached=cache[id];if(cached&&cached.expiresAt>Date.now())connect(cached.secret,cached.expiresAt).catch(e=>setStatus(e.message,'error'));
  }
  input.addEventListener('change',()=>{const secret=input.value.trim();if(!secret){forget();return;}connect(secret).catch(e=>{setStatus(e.message,'error');input.value=saved?.secret||''});});
  forgetButton.addEventListener('click', () => forget());
  create.addEventListener('click', async () => {
    create.disabled = true; setStatus('Creating key…');
    try {
      await ensureSession(true);
      const workspace=window.Workspace.current();if(!workspace)throw Error('Select an organization or Profile first.');
      const organizationID=workspace.personal?'':workspace.id;
      const result = await api('/v1/api-keys', {method:'POST', body:JSON.stringify({organization_id:organizationID,name:`Generation ${new Date().toISOString().slice(0,16).replace('T',' ')}`, scopes:['generation'],resource_mode:organizationID?'shared':'personal'})});
      await ensureSession(true);
      await connect(result.secret);
    } catch (error) { setStatus(`Could not create key: ${error.message}`,'error'); }
    finally { create.disabled = !systemAdministrator && serviceInfo.console?.personal_providers===false && !serviceInfo.console?.shared_resources; }
  });
  window.addEventListener('storage', event => { if (event.key === 'gateway-sign-out') {cache={};forget('Signed out. The saved generation key was cleared.');} });
  window.addEventListener('focus', () => { value(); if (google) ensureSession(true).catch(() => {}); });
  window.addEventListener('pageshow', () => { value(); if (google) ensureSession(true).catch(() => {}); });
  setInterval(() => { if (google && saved) ensureSession().catch(() => {}); }, 15000);
  document.querySelector('#manageGenerationKeys').onclick=e=>{e.preventDefault();showConsoleSection('keys')};
  window.GenerationKey = {value, initialize, forget, activateWorkspace, ensureSession, signOut() { cache={};forget('Signed out.'); try { localStorage.setItem('gateway-sign-out', String(Date.now())); } catch {} }};
})();
