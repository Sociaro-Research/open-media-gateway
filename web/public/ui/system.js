(() => {
  const view=document.querySelector('#systemView'),content=document.querySelector('#systemContent');
  let epoch=0,current='settings',selfID='',settingsTab='system';
 let embedded=null,originalParent=null;
 function restore(){if(embedded){originalParent.append(embedded);embedded.hidden=true;embedded=null;}sharedConfiguration=false;}

  const el=(tag,text,cls)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;if(cls)node.className=cls;return node;};
  const button=(text,action)=>{const b=el('button',text,'secondary');b.type='button';b.addEventListener('click',action);return b;};
  function table(headers,rows){const wrap=el('div',undefined,'system-table-wrap'),table=el('table',undefined,'docs-table'),head=el('thead'),tr=el('tr'),body=el('tbody');for(const label of headers){const th=el('th',label);th.scope='col';tr.append(th);}head.append(tr);for(const row of rows){const tr=el('tr');for(const value of row){const td=el('td');td.append(value instanceof Node?value:document.createTextNode(String(value)));tr.append(td);}body.append(tr);}table.append(head,body);wrap.append(table);return wrap;}
  function title(name,description){GatewayLayout.clear(content);content.replaceChildren(el('h1',name));const status=el('p','Loading…','system-status');status.setAttribute('role','status');content.append(status);return status;}
  async function settings(token){const status=title('Settings');
    try{const data=await api('/v1/system/settings');if(token!==epoch)return;FormUI.message(status,'');
      const tabs=el('div',undefined,'organization-tabs system-settings-tabs'),panels=el('div',undefined,'system-settings-panels'),sections={};tabs.setAttribute('role','tablist');tabs.setAttribute('aria-label','Settings sections');content.append(tabs,panels);
      const selectTab=id=>{settingsTab=id;for(const [key,section]of Object.entries(sections)){const active=key===id;section.panel.hidden=!active;section.tab.setAttribute('aria-selected',String(active));section.tab.setAttribute('aria-pressed',String(active));section.tab.tabIndex=active?0:-1;}FormUI.refresh();};
      for(const [id,label]of [['system','System'],['appearance','Appearance'],['markups','Markups'],['sales','Sales'],['limits','Request limits'],['catalog','Model catalogue'],['payments','Payments'],['email','Email delivery'],['login','Login methods']]){
       const tab=button(label,()=>selectTab(id)),panel=el('section',undefined,'system-settings-panel');tab.id='settings-tab-'+id;tab.setAttribute('role','tab');tab.setAttribute('aria-controls','settings-panel-'+id);panel.id='settings-panel-'+id;panel.setAttribute('role','tabpanel');panel.setAttribute('aria-labelledby',tab.id);sections[id]={tab,panel};tabs.append(tab);panels.append(panel);
      }
      GatewayLayout.bind(tabs,{scope:content,label:'Settings',anchor:view.querySelector('[data-system-page=settings]')});
      let markupEditor,markupMounted=false;const markupWrites=new Map();const syncMarkup=()=>{if(markupMounted)for(const [previous,version]of [...markupWrites].sort((a,b)=>a[0]-b[0]))markupEditor?.setSystemVersion(version,previous);};const advanceMarkup=(version,previous)=>{if(previous!==undefined){markupWrites.set(previous,version);syncMarkup();}};
      selectTab(settingsTab);const systemPanel=sections.system.panel,setupButton=window.SetupSelector?.mount(systemPanel,data,(next,previous)=>{if(token!==epoch)return;const changedElsewhere=previous&&data.version!==previous.version;reconcile(next,confirmed,previous?.version);FormUI.message(status,changedElsewhere?'Settings changed elsewhere. Your edits are preserved. Review the current values before saving.':'');},()=>token===epoch);const advancedHeading=el('h2','Advanced settings');systemPanel.append(advancedHeading);
      const form=el('form',undefined,'system-settings');
      form.innerHTML=`<label class="system-toggle"><input type="checkbox" name="generation"><span><strong>Show Playground</strong><small>Show the Playground screen. API generation stays available when this is off.</small></span></label>
      <label class="system-toggle"><input type="checkbox" name="inlineReferences"><span><strong>Allow reference files in generation requests</strong><small>Allow supported providers to receive reference image contents with the request. When off, providers receive only HTTP(S) links or supported registered asset references. Applies to shared and personal connections when generation starts.</small></span></label>
      <label class="system-toggle"><input type="checkbox" name="customerWorkspaces"><span><strong>Allow customer workspace onboarding</strong><small>Opt in to browser-authorized installation connections and one self-service company per account. Requires shared resources and working sign-in; grants no credit or provider access without consent.</small></span></label>
      <button class="primary compact" type="submit">Save</button>`;
      form.elements.customerWorkspaces.checked=!!data.customer_workspaces;
      form.elements.inlineReferences.checked=data.allow_inline_references!==false;
      form.elements.generation.checked=data.generation_ui;
      const fields={generation:'generation_ui',inlineReferences:'allow_inline_references',customerWorkspaces:'customer_workspaces'},values=state=>Object.fromEntries(Object.values(fields).map(key=>[key,!!state[key]])),read=()=>Object.fromEntries(Object.entries(fields).map(([name,key])=>[key,form.elements[name].checked]));
      let confirmed=values(data),saving=false,refreshing=false;
      systemPanel.append(form);const save=form.querySelector('button[type=submit]');FormUI.watch(form,save,{heading:advancedHeading,read:()=>JSON.stringify(read()),valid:()=>!saving&&!refreshing});
      function reconcile(next,submitted=confirmed,previousVersion){advanceMarkup(next.version,previousVersion);if(next.version<data.version)return false;const draft=read();for(const [name,key]of Object.entries(fields))if(draft[key]===submitted[key])form.elements[name].checked=!!next[key];confirmed=values(next);Object.assign(data,next);FormUI.commit(form,JSON.stringify(confirmed));return true;}
      function update(){if(setupButton)setupButton.disabled=saving||refreshing;FormUI.update(form);}
      async function recover(){if(token!==epoch)return;try{const next=await api('/v1/system/settings');if(token!==epoch)return;if(reconcile(next)){window.applySystemAccess(next,true);window.SetupSelector?.reflect(next);}refreshing=false;update();FormUI.message(status,'Settings changed. Current values are loaded; your edits are retained. Review them before saving again.','error');}catch{if(token!==epoch)return;FormUI.message(status,'Could not load the changed settings. Retrying automatically…','error');setTimeout(recover,1500);}}
      form.addEventListener('submit',async e=>{e.preventDefault();if(saving||refreshing||save.disabled)return;const submitted=read(),version=data.version;saving=true;update();FormUI.message(status,'Saving…');
        try{const next=await api('/v1/system/settings',{method:'PUT',body:JSON.stringify({...submitted,version})});if(token!==epoch)return;if(reconcile(next,submitted,version))window.applySystemAccess(next,true);FormUI.saved(form,JSON.stringify(confirmed));FormUI.message(status,'');}
        catch(error){if(token!==epoch)return;if(error.status===409){refreshing=true;void recover();}else FormUI.message(status,error.message,'error');}
        finally{saving=false;update();}
      });
      await window.SalesBonuses.settings(sections.sales.panel,api,()=>token===epoch);
	  await window.BrandingSettings.mount(sections.appearance.panel,api,()=>token===epoch);
	  markupEditor=await window.MarkupSettings.mount(sections.markups.panel,api,{current:()=>token===epoch,onSystemSaved:version=>{data.version=Math.max(data.version,version);syncMarkup();}});markupMounted=true;syncMarkup();
      await window.RequestLimits.mount(sections.limits.panel,api,{current:()=>token===epoch});
      await window.EmailDeliverySettings.mount(sections.email.panel,api,()=>token===epoch);
      await window.CatalogSync?.settings(sections.catalog.panel,api,()=>token===epoch);
      await window.PaymentNotificationSettings.mount(sections.payments.panel,api,()=>token===epoch);
      await window.PaymentProviderSettings.mount(sections.payments.panel,api,()=>token===epoch);
      await window.LoginMethodSettings.mount(sections.login.panel,api,()=>token===epoch);
    }catch(error){if(token===epoch)FormUI.message(status,error.message,'error');}
  }
  async function administrators(token){const status=title('Users');
    const form=el('form',undefined,'system-search');form.innerHTML='<label>Search users<input type="search" name="search" maxlength="200"></label><button class="secondary" type="submit">Search</button>';
    const list=el('div'),pagination=el('div',undefined,'report-pagination');pagination.hidden=true;content.append(form,list,pagination);let offset=0,sequence=0;
    async function load(){const seq=++sequence;FormUI.message(status,'Loading users…');try{const result=await api('/v1/system/users?'+new URLSearchParams({search:form.elements.search.value,offset:String(offset)}));if(token!==epoch||seq!==sequence)return;FormUI.message(status,result.data.length?'':'No matching users.');
      list.replaceChildren(table(['User','Role'],result.data.map(user=>{
        const identity=UserUI.identity(user,{secondary:true});
        const edit=el('form',undefined,'role-editor'),select=el('select'),message=el('small');select.setAttribute('aria-label','Role for '+user.email);select.dataset.fieldLabel='Role';
        for(const [value,label] of [['user','User'],['system_admin','Administrator'],...SalesHierarchy.roles]){const option=el('option',label);option.value=value;select.append(option)}
        select.value=user.is_system_admin?'system_admin':user.is_client_manager?(user.sales_role||'client_manager'):'user';
        const apply=el('button','Apply','secondary');apply.type='submit';edit.append(select,apply,message);FormUI.watch(edit,apply);
        edit.onsubmit=async e=>{e.preventDefault();if(apply.disabled)return;const nextRole=select.value,snapshot=FormUI.snapshot(edit);apply.disabled=true;FormUI.message(message,'');try{await api(`/v1/system/users/${user.id}/role`,{method:'PUT',body:JSON.stringify({role:nextRole})});FormUI.saved(edit,snapshot);if(user.id===selfID&&nextRole!=='system_admin'){clientManager=SalesHierarchy.roles.some(([id])=>id===nextRole);clientManagerRole=clientManager?nextRole:'';window.applySystemAccess(consolePolicy,false);}}catch(error){FormUI.message(message,error.message,'error');}finally{apply.disabled=false;FormUI.update(edit);}};
        return [identity,edit];})));
      const previous=button('Previous',()=>{offset-=50;load()}),next=button('Next',()=>{offset+=50;load()});previous.disabled=!offset;next.disabled=!result.has_more;pagination.hidden=offset===0&&!result.has_more;pagination.replaceChildren(previous,el('span',`Page ${Math.floor(offset/50)+1}`),next);
    }catch(error){if(token===epoch&&seq===sequence)FormUI.message(status,error.message,'error');}}
    form.onsubmit=e=>{e.preventDefault();offset=0;load()};await load();
  }
  async function embedPage(kind){
    embedded=document.querySelector('#'+(kind==='usage'?'reports':kind)+'View');originalParent=embedded.parentNode;
    content.replaceChildren(embedded);embedded.hidden=false;sharedConfiguration=!['reports','usage'].includes(kind);
    if(kind==='providers'){embedded.querySelector('h1').textContent='Shared providers';await loadAdminProviders();}
    if(kind==='storage'){embedded.querySelector('h1').textContent='Shared storage';const token=epoch;await loadStorageSettings();if(token===epoch)await storageQuota(token);}
    if(kind==='reports')await window.GatewayReports.open(true);
    if(kind==='usage')await window.GatewayReports.openUsage(true);
  }
  async function storageQuota(token){
   const form=el('form',undefined,'keys-form shared-storage-allowance');form.innerHTML='<h2>Shared storage allowance</h2><label for="sharedStorageLimit">Per-organization limit (GB)</label><input id="sharedStorageLimit" type="number" min="0" max="1000000" step="0.000001"><p class="field-explanation">Total current space per organization, including references and results. 1 GB = 1,000,000,000 bytes. New uploads and generations are blocked only when usage is already above this limit. Running generations always save their results.</p><button class="secondary" type="submit">Save</button><p role="status"></p>';content.append(form);
   const message=form.querySelector('[role=status]'),input=form.querySelector('input'),save=form.querySelector('button');save.disabled=true;
   try{const data=await api('/v1/system/storage/quota');if(token!==epoch)return;input.value=data.limit_bytes==null?'':data.limit_bytes/1e9;save.disabled=false;FormUI.watch(form,save);
    form.onsubmit=async event=>{event.preventDefault();const baseline=FormUI.snapshot(form);save.disabled=true;FormUI.message(message,'Saving…');try{const next=await api('/v1/system/storage/quota',{method:'PUT',body:JSON.stringify({limit_bytes:input.value===''?null:Math.round(Number(input.value)*1e9),version:data.version})});data.version=next.version;FormUI.saved(form,baseline);FormUI.message(message,'');}catch(error){FormUI.message(message,error.message,'error');}finally{save.disabled=false;FormUI.update(form);}};
   }catch(error){FormUI.message(message,error.message,'error');}
  }
  async function activity(token){const status=title('Audit Log');FormUI.message(status,'');await window.AuditLog.mount(content,api,{current:()=>token===epoch});}
  async function open(){window.SalesHierarchy?.close();GatewayLayout.clear(content);restore();content.replaceChildren();content.classList.remove('organizations-content');window.GatewayReports?.close();const token=++epoch;for(const node of view.querySelectorAll('[data-system-page]')){const active=node.dataset.systemPage===current;node.classList.toggle('active',active);if(active)node.setAttribute('aria-current','page');else node.removeAttribute('aria-current');}await ({settings,administrators,activity,sales:()=>SalesHierarchy.mount(content,api,()=>token===epoch),catalog:()=>window.CatalogSync.journal(content,api,()=>token===epoch),organizations:()=>window.GatewayOrganizations.open(content,true),sharedProviders:()=>embedPage('providers'),sharedStorage:()=>embedPage('storage'),usage:()=>embedPage('usage'),reports:()=>embedPage('reports')})[current](token);}
  view.querySelectorAll('[data-system-page]').forEach(node=>node.addEventListener('click',()=>{if(node.dataset.systemPage==='monitoring'){location.assign('/monitoring/');return;}current=node.dataset.systemPage;open();}));
  window.GatewaySystem={open,identify(user){selfID=user.id;},close(){epoch++;window.SalesHierarchy?.close();GatewayLayout.clear(content);restore();document.querySelector('#providersView h1').textContent='Providers';document.querySelector('#storageView h1').textContent='Storage';content.replaceChildren();document.querySelectorAll('.system-dialog').forEach(d=>d.close());}};
})();
