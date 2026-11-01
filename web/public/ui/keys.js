(() => {
  'use strict';
  let content = document.querySelector('#keysContent'), organizationID = '';
 const inScope=key=>(key.organization_id||'')===organizationID;
  let pageToken = 0;
  const node = (tag, text, className) => {
    const el = document.createElement(tag);
    if (text !== undefined) el.textContent = text;
    if (className) el.className = className;
    return el;
  };
  const paragraph = (text, parent = content) => parent.append(node('p', text));
  function table(headers, rows, parent = content) {
    const wrap = node('div', undefined, 'docs-table-wrap');
    const t = node('table', undefined, 'docs-table');
    const head = node('thead'), tr = node('tr');
    headers.forEach(h => { const th = node('th', h); th.scope = 'col'; tr.append(th); }); head.append(tr);
    const body = node('tbody');
    rows.forEach(row => { const r = node('tr'); row.forEach((cell,index) => { const td = node('td',undefined,headers[index]===''?'table-row-actions':undefined); td.append(cell instanceof Node ? cell : document.createTextNode(String(cell))); r.append(td); }); body.append(r); });
    t.append(head, body); wrap.append(t); parent.append(wrap);
  }
  async function keyManagement(token) {
    FormUI.sectionHeading(content,'API keys');
    const status=node('p','Loading keys…','keys-status'); status.setAttribute('role','status'); content.append(status);
    const month=node('input'); month.type='month'; month.id='keyUsageMonth';
    const now=new Date(),timezone=Intl.DateTimeFormat().resolvedOptions().timeZone;
    month.value=`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}`; month.min='2000-01';
    let keys, usage=[], loadedMonth='', organizationMember=false,organizationRole='',members=[];
    async function loadKeys() {
      const selectedMonth=month.value;
      const [keyResult, usageResult, organizations]=await Promise.all([api('/v1/api-keys'+(organizationID?'?organization_id='+encodeURIComponent(organizationID):'')),api(`/v1/api-keys/usage?month=${encodeURIComponent(selectedMonth)}&timezone=${encodeURIComponent(timezone)}`+(organizationID?'&organization_id='+encodeURIComponent(organizationID):'')),organizationID?api('/v1/organizations'):Promise.resolve({data:[]})]);
      if(token!==pageToken || selectedMonth!==month.value) return;
      organizationRole=organizations.data.find(o=>o.id===organizationID)?.role||'';
      organizationMember=['supermanager','admin','developer','system_admin'].includes(organizationRole);
      if(organizationID&&organizationMember)members=(await api('/v1/organizations/'+organizationID+'/members')).data;
      keys=keyResult.data.filter(inScope); usage=usageResult.data; loadedMonth=usageResult.month;
    }
    try { await loadKeys(); }
    catch(e) {
      if(token!==pageToken) return;
      FormUI.message(status,e.status===401 ? 'Sign in to create your first personal key.' : e.status===403 ? 'A browser session or a personal key with keys:manage is required. The deployment service key cannot manage personal keys.' : `Could not load keys: ${e.message}`,'error');
      if(e.status===401 || e.status===403) {const a=node('a','Sign in →','docs-sign-in'); a.href='/';content.append(a);}
      return;
    }
    if(token!==pageToken) return;
    FormUI.message(status,'');
    const form=node('form',undefined,'keys-form');
    form.innerHTML='<h3>Create a key</h3><div class="keys-fields"><div><label for="newKeyName">Name</label><input id="newKeyName" name="name" required maxlength="80" autocomplete="off"></div><div><label for="newKeyExpiry">Expiration</label><select id="newKeyExpiry"><option value="90">90 days</option><option value="30">30 days</option><option value="365">1 year</option><option value="">No expiration</option></select></div></div><fieldset><legend>Permissions</legend><label><input type="checkbox" checked disabled> Generation and results</label><label><input type="checkbox" name="providers"> Manage providers</label><label><input type="checkbox" name="storage"> Manage storage</label><label><input type="checkbox" name="keys"> Manage API keys</label></fieldset><button class="primary compact" type="submit">Create</button><p class="docs-form-status" role="status"></p>';
    if(systemAdministrator){const label=node('label');label.innerHTML='<input type="checkbox" name="system"> Manage system';form.querySelector('fieldset').append(label);}
    const personalAllowed=serviceInfo.console?.installation_role!=='central'&&(systemAdministrator||serviceInfo.console?.personal_providers!==false);
    const sharedAllowed=!!serviceInfo.console?.shared_resources;
    const membershipRequired=!!organizationID&&!organizationMember;
    const unavailable=membershipRequired||(organizationID?!sharedAllowed:!personalAllowed);
    if(unavailable){FormUI.message(status,membershipRequired?'Only organization members can create keys.':'Key creation is disabled by system settings.');form.hidden=true;}
    if(organizationID){
      const recipientField=node('div'),recipientLabel=node('label','User'),recipient=node('select');recipient.id='newKeyUser';recipient.name='user_id';recipientLabel.htmlFor=recipient.id;
      for(const member of members.filter(m=>OrganizationRoles.issue(organizationRole,Workspace.isSelf(m.id),m.role))){const option=UserUI.option(member,member.name?member.name+' · '+member.email:member.email);recipient.append(option);if(Workspace.isSelf(member.id)){option.defaultSelected=true;recipient.value=member.id;}}
      recipientField.append(recipientLabel,recipient);form.querySelector('.keys-fields').prepend(recipientField);
      const orgScope=node('label');orgScope.innerHTML='<input type="checkbox" name="organizations"> Manage organization';form.querySelector('fieldset').append(orgScope);
      form.elements.providers.closest('label').remove();form.elements.storage.closest('label').remove();
    }
    content.append(form);
    FormUI.watch(form,form.querySelector('button[type=submit]'),{valid:()=>!!form.elements.name.value.trim()});
    const secretPanel=node('div',undefined,'keys-secret'); secretPanel.hidden=true; content.append(secretPanel);
    form.addEventListener('submit',async e=> {
      e.preventDefault();const button=form.querySelector('button[type=submit]');button.disabled=true;
      const message=form.querySelector('[role=status]');FormUI.message(message,'Creating…');
      const input={name:form.elements.name.value,scopes:['generation'],resource_mode:organizationID?'shared':'personal'};if(organizationID){input.organization_id=organizationID;input.user_id=form.elements.user_id.value;}
      if(form.elements.providers?.checked) input.scopes.push('providers:manage');
      if(form.elements.storage?.checked) input.scopes.push('storage:manage');
      if(form.elements.keys.checked) input.scopes.push('keys:manage');
      if(form.elements.organizations?.checked)input.scopes.push('organizations:manage');
      if(form.elements.system?.checked)input.scopes.push('system:manage');
      const days=form.querySelector('#newKeyExpiry').value;
      if(days) input.expires_at=new Date(Date.now()+Number(days)*86400000).toISOString();
      try {
        const result=await api('/v1/api-keys',{method:'POST',body:JSON.stringify(input)});
        if(token!==pageToken) return;
        secretPanel.replaceChildren(node('h3','Save your key now'),node('p','Shown once. Copy and store it safely.'));
        const secretField=node('input');secretField.readOnly=true;secretField.value=result.secret;secretField.autocomplete='off';secretField.setAttribute('aria-label','Gateway API key');secretPanel.append(secretField);
        const dismiss=node('button','I saved my key','secondary');dismiss.type='button';dismiss.addEventListener('click',()=>{secretPanel.replaceChildren();secretPanel.hidden=true;form.querySelector('button[type=submit]').disabled=false;});secretPanel.append(dismiss);secretPanel.hidden=false;
        const {secret,...metadata}=result;keys.unshift(metadata);renderKeys();form.reset();FormUI.commit(form);FormUI.message(message,'');
      } catch(err) {FormUI.message(message,`Could not create key: ${err.message}. If the response was lost, refresh the list before creating another key.`,'error');}
      finally {button.disabled=!secretPanel.hidden;}
    });

    const toolbar=node('div',undefined,'keys-toolbar'),monthField=node('div'),monthLabel=node('label','Month');monthLabel.htmlFor=month.id;monthField.append(monthLabel,month);toolbar.append(monthField);content.append(toolbar);
    const keyList=node('div');content.append(keyList);
    month.addEventListener('change',async()=>{
      if(!month.value)return;renderKeys();
      try { await loadKeys();if(token===pageToken){renderKeys();if(!unavailable)FormUI.message(status,'');} }
      catch(err){if(token===pageToken)FormUI.message(status,`Could not load usage: ${err.message}`,'error');}
    });
    const dollars=value=>new Intl.NumberFormat('en-US',{style:'currency',currency:'USD',minimumFractionDigits:0,maximumFractionDigits:2}).format(value);
    function renderKeys() {
      keyList.replaceChildren();toolbar.hidden=!keys.length;
      if(!keys.length) {paragraph('No keys yet.',keyList);return;}
      table(['Key',...(organizationID?['User','Issued by']:[]),'Spending','Permissions','Activity','Status',''],keys.map(key=>{
        const revoked=!!key.revoked_at, expired=key.expires_at && Date.parse(key.expires_at)<=Date.now();
        const identity=node('div');identity.append(node('strong',key.name),node('code',key.prefix+'…'),node('small',key.id));if(key.purpose==='playground_templates')identity.append(node('small','Created by Playground · Templates'));
        const activity=node('div');activity.append(node('small','Created '+new Date(key.created_at).toLocaleDateString()),node('small',key.last_used_at ? 'Used '+new Date(key.last_used_at).toLocaleString() : 'Not used yet'),node('small',key.expires_at ? 'Expires '+new Date(key.expires_at).toLocaleDateString() : 'No expiration'));
        const revoke=node('button',revoked?'Revoked':'Revoke','secondary');revoke.type='button';revoke.disabled=revoked;
        revoke.addEventListener('click',()=>confirmRevoke(key,revoke));
        const spend=node('div'),item=loadedMonth===month.value?usage.find(item=>item.api_key_id===key.id):null;
        spend.append(node('strong',item ? dollars(item.consumed_usd) : '—'));
        if(item) {
          spend.append(node('small',`${item.tasks} ${item.tasks===1?'task':'tasks'}`));
          if(item.unknown_cost_tasks) spend.append(node('small',`${item.unknown_cost_tasks} legacy tasks with unknown cost`, 'keys-incomplete'));
        }
        return [identity,...(organizationID?[UserUI.identity(members.find(m=>m.id===key.user_id)||{id:key.user_id,email:key.user_email,picture:key.user_picture}),UserUI.identity(members.find(m=>m.id===key.created_by)||{id:key.created_by,email:key.creator_email,picture:key.creator_picture})]:[]),spend,key.scopes.join(', '),activity,revoked?'Revoked':expired?'Expired':((key.resource_mode==='shared'?!sharedAllowed:!personalAllowed))?'Disabled by system settings':'Active',revoked?'':revoke];
      }),keyList);
    }
    function confirmRevoke(key,button) {
      const dialog=node('dialog',undefined,'keys-revoke-dialog');dialog.setAttribute('aria-labelledby','revokeKeyTitle');
      const title=node('h2','Revoke this API key?');title.id='revokeKeyTitle';
      const message=node('p',`Applications using “${key.name}” will lose access. Accepted tasks will keep running. This cannot be undone.`);
      const actions=node('div',undefined,'docs-actions'),cancel=node('button','Cancel','secondary'),confirm=node('button','Revoke key','secondary docs-danger');cancel.type=confirm.type='button';
      actions.append(cancel,confirm);dialog.append(title,message,actions);document.body.append(dialog);
      cancel.addEventListener('click',()=>dialog.close());dialog.addEventListener('close',()=>dialog.remove());
      confirm.addEventListener('click',async()=>{
        confirm.disabled=true;FormUI.message(message,'Revoking…');
        try {await api(`/v1/api-keys/${key.id}`,{method:'DELETE'});key.revoked_at=new Date().toISOString();dialog.close();renderKeys();month.focus();}
        catch(err){FormUI.message(message,`Could not revoke key: ${err.message}`,'error');confirm.disabled=false;}
      });dialog.showModal();cancel.focus();
    }
    renderKeys();
  }
  window.GatewayKeys = {
    async open(parent=document.querySelector('#keysContent'),orgID='') { const token = ++pageToken; content=parent;organizationID=orgID;content.replaceChildren(); await keyManagement(token); },
    close() { pageToken++; content.replaceChildren(); document.querySelectorAll('.keys-revoke-dialog').forEach(dialog => dialog.remove()); }
  };
})();
