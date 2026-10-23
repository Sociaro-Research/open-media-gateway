(() => {
 'use strict';
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const memberRoleLabel=role=>OrganizationRoles.label(role);
 const money=v=>v==null?'Unlimited':formatUSD(v);
 const button=(text,fn)=>{const b=el('button',text,'secondary');b.type='button';b.onclick=fn;return b;};
 const field=(form,label,type='text',value='',required=false)=>{const wrap=el('label',label,'organization-field'),n=el('input');n.type=type;n.value=value??'';n.required=required;if(type==='number'){n.step='0.000001';n.min='0';n.max='1000000';n.title=required?'':'Unlimited when empty';}wrap.append(n);form.append(wrap);return n;};
 const table=(parent,heads,rows)=>{const wrap=el('div',null,'docs-table-wrap'),t=el('table',null,'docs-table'),head=el('tr');heads.forEach(h=>{const th=el('th');th.append(h instanceof Node?h:document.createTextNode(h));th.scope='col';head.append(th)});const thead=el('thead');thead.append(head);t.append(thead);const body=el('tbody');rows.forEach(values=>{const tr=el('tr');values.forEach((v,index)=>{const td=el('td',null,heads[index]===''?'table-row-actions':null);td.append(v instanceof Node?v:document.createTextNode(String(v??'')));tr.append(td)});body.append(tr)});t.append(body);wrap.append(t);parent.append(wrap);return t;};
 const nullable=n=>n.value===''?null:Number(n.value);
 function form(parent,title,submit){const f=el('form',null,'keys-form organization-form');f.append(el('h2',title));parent.append(f);const status=el('p');status.setAttribute('role','status');const save=el('button','Save','primary compact');save.type='submit';f.finish=(caption='Save',valid=()=>true)=>{save.textContent=caption;const actions=el('div',null,'form-actions');actions.append(save,status);f.append(actions);FormUI.watch(f,save,{valid})};f.onsubmit=async e=>{e.preventDefault();if(save.disabled)return;const saved=FormUI.snapshot(f);save.disabled=true;FormUI.message(status,'Saving…');try{await submit();FormUI.saved(f,saved);FormUI.message(status,'')}catch(error){FormUI.message(status,error.message,'error')}finally{save.disabled=false;FormUI.update(f)}};return f;}
 function confirmAction(title,text,action){const d=el('dialog',null,'keys-revoke-dialog organization-dialog'),message=el('p'),cancel=button('Cancel',()=>d.close()),yes=button('Confirm',async()=>{yes.disabled=true;message.replaceChildren(text.cloneNode?text.cloneNode(true):document.createTextNode(text));try{await action();d.close()}catch(e){FormUI.message(message,e.message,'error');yes.disabled=false}});message.append(text.cloneNode?text.cloneNode(true):text);const actions=el('div',null,'form-actions');actions.append(cancel,yes);d.append(el('h2',title),message,actions);document.body.append(d);d.onclose=()=>d.remove();d.showModal();cancel.focus();}
 let epoch=0;
 let managerFilters=null;
 async function list(all=false){return (await api('/v1/organizations'+(all?'?all=true':''))).data;}
 async function open(parent=document.querySelector('#organizationsContent'),all=false,forceList=false){if(!all&&!forceList&&window.Workspace?.ready())return Workspace.open();const token=++epoch;GatewayLayout.clear(parent);parent.classList.add('organizations-content');parent.replaceChildren(el('h1','Organizations'));const status=el('p','Loading…');parent.append(status);
 if(clientManager&&!all){await managerList(parent,status,token);return;}
 try{const items=await list(all);if(token!==epoch)return;FormUI.message(status,items.length?'':'No organizations.');
 if(all){let name,domain,admin;const f=form(parent,'Create organization',async()=>{await api('/v1/organizations',{method:'POST',body:JSON.stringify({name:name.value,domain:domain.value,admin_user:admin.value})});await window.Workspace?.refresh();await open(parent,all)});name=field(f,'Name','text','',true);domain=field(f,'Company website','text','',true);domain.maxLength=2048;domain.autocomplete='url';admin=field(f,'First supermanager (optional)');admin.title='Email or user ID';f.classList.add('organization-create');f.finish('Create',()=>!!name.value.trim()&&!!domain.value.trim());}
 const organizationsTable=table(parent,['Organization','Balance','Credit Limit','Shared storage',''],items.map(o=>[o.name,!OrganizationRoles.statistics(o.role)?'—':money(o.balance_usd),!OrganizationRoles.statistics(o.role)?'—':money(o.credit_limit_usd),!OrganizationRoles.statistics(o.role)?'—':StorageQuota.size(o.storage_bytes),button('Manage',()=>details(parent,o,all))]));
 organizationsTable.querySelectorAll('tbody tr').forEach((row,index)=>{
  const manage=row.querySelector('button');if(!manage||manage.disabled)return;
  row.classList.add('organization-row');row.tabIndex=0;row.setAttribute('aria-label','Manage '+items[index].name);
  row.addEventListener('click',e=>{if(!e.target.closest('button,a,input,select,textarea'))manage.click();});
  row.addEventListener('keydown',e=>{if(e.target===row&&(e.key==='Enter'||e.key===' ')){e.preventDefault();manage.click();}});
 });
 const pending=sessionStorage.getItem('gateway-pending-invite');if(pending&&!clientManager)await invitation(parent,pending);
 }catch(e){FormUI.message(status,e.message,'error')}}

 async function managerList(parent,status,token){
  parent.insertBefore(button('Bonuses',()=>SalesBonuses.open(api)),status);
  parent.insertBefore(button('Sales permissions',()=>SalesPermissions.open(api)),status);
  const now=new Date(),today=`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`,timezone=Intl.DateTimeFormat().resolvedOptions().timeZone;
  const filters=managerFilters||{from:today.slice(0,8)+'01',to:today,activity:'all',sort:'name'};
  const form=el('form',null,'report-filters'),host=el('div'),caption=el('p',null,'field-explanation');status.setAttribute('role','status');
  function control(label,type,value,choices){
   const wrap=el('div'),id='company'+label.replaceAll(' ',''),labelNode=el('label',label),input=el(choices?'select':'input');labelNode.htmlFor=id;input.id=id;
   if(choices)for(const [value,text] of choices){const option=el('option',text);option.value=value;input.append(option);}else{input.type=type;input.required=true;}
   input.value=value;wrap.append(labelNode,input);form.append(wrap);return input;
  }
  const from=control('From','date',filters.from),to=control('To','date',filters.to);
  const activity=control('Activity',null,filters.activity,[['all','All companies'],['inactive','No use in the last 30 days']]);
  const sort=control('Sort by',null,filters.sort,[['name','Company name'],['spend_desc','Spend: highest first'],['spend_asc','Spend: lowest first'],['balance_asc','Balance: lowest first'],['balance_desc','Balance: highest first']]);
  const apply=el('button','Apply','secondary');apply.type='submit';form.append(apply);parent.insertBefore(form,status);parent.append(caption,host);FormUI.refresh();
  let items=[],asOf=0,loaded=false,request=0;
  function snapshot(){return {from:from.value,to:to.value,activity:activity.value,sort:sort.value};}
  function render(){
   if(!loaded)return;
   const visible=items.filter(o=>activity.value!=='inactive'||o.usage&&(!o.usage.last_activity_at||new Date(o.usage.last_activity_at).getTime()<asOf-30*86400000));
   visible.sort((a,b)=>{
    const field=sort.value.startsWith('spend')?'spend_usd':'balance_usd',value=o=>field==='spend_usd'?o.usage?.spend_usd:o.balance_usd;
    const compared=sort.value==='name'?0:(Number(value(a)??0)-Number(value(b)??0))*(sort.value.endsWith('desc')?-1:1);
    return compared||a.name.localeCompare(b.name)||a.id.localeCompare(b.id);
   });
   host.replaceChildren();
   const spend=o=>!o.usage?'Unavailable':o.usage.unknown_costs?(o.usage.spend_usd?`${money(o.usage.spend_usd)} · ${o.usage.unknown_costs} costs not yet known`:'Not yet known'):money(o.usage.spend_usd);
   const last=o=>{if(!o.usage)return 'Unavailable';if(!o.usage.last_activity_at)return 'Never used';const value=el('time',new Date(o.usage.last_activity_at).toLocaleString());value.dateTime=o.usage.last_activity_at;return value;};
   const t=table(host,['Organization','Balance','Spend for period','Last activity','Credit Limit','Shared storage',''],visible.map(o=>[o.name,money(o.balance_usd),spend(o),last(o),money(o.credit_limit_usd),StorageQuota.size(o.storage_bytes),button('Manage',()=>details(parent,o,false))]));
   t.style.minWidth='960px';
   t.querySelectorAll('tbody tr').forEach((row,index)=>{row.classList.add('organization-row');row.tabIndex=0;row.setAttribute('aria-label','Manage '+visible[index].name);const open=()=>details(parent,visible[index],false);row.onclick=e=>{if(!e.target.closest('button,a,input,select,textarea'))open();};row.onkeydown=e=>{if(e.target===row&&(e.key==='Enter'||e.key===' ')){e.preventDefault();open();}};});
   FormUI.message(status,visible.length?`${visible.length} ${visible.length===1?'company':'companies'}`:items.length?'No companies match this filter.':'No assigned companies.');
  }
  async function load(){
   to.setCustomValidity(from.value>to.value?'Choose an end date on or after the start date.':'');if(!form.reportValidity())return;
   const serial=++request,chosen=snapshot();apply.disabled=true;FormUI.message(status,'Loading…');
   try{
    const query=new URLSearchParams({view:'summary',from:chosen.from,to:chosen.to,timezone}),result=await api('/v1/organizations?'+query);
    if(token!==epoch||serial!==request)return;
    const reference=new Date(result.as_of).getTime();if(!Number.isFinite(reference)||!Array.isArray(result.data))throw new Error('Company activity data is unavailable.');
    items=result.data;asOf=reference;loaded=true;managerFilters={...chosen,activity:activity.value,sort:sort.value};
    caption.textContent=`Spend: ${chosen.from} – ${chosen.to} · Timezone: ${timezone} · Updated ${new Date(asOf).toLocaleString()}`;render();
   }catch(e){if(token===epoch&&serial===request){if(e.status===403||e.status===401){host.replaceChildren();caption.textContent='';loaded=false;}FormUI.message(status,(loaded?'Could not refresh; previous data is shown. ':'')+e.message,'error');}}
   finally{if(token===epoch&&serial===request)apply.disabled=false;}
  }
  form.onsubmit=e=>{e.preventDefault();load();};from.oninput=to.oninput=()=>to.setCustomValidity('');
  activity.onchange=sort.onchange=()=>{if(loaded){managerFilters={...managerFilters,activity:activity.value,sort:sort.value};render();}};
  await load();
  await window.ClientOnboarding?.mount(parent,{current:()=>token===epoch,refresh:async()=>{await load();await window.Workspace?.refresh();}});
 }
 async function details(parent,o,all,selectedPage=null){const token=++epoch;const base='/v1/organizations/'+o.id;GatewayLayout.clear(parent);parent.replaceChildren(el('h1',o.name));
 if(clientManager){const heading=el('div',null,'report-heading console-section-heading');heading.append(parent.firstChild,button('Bonuses',()=>SalesBonuses.open(api)),button('Companies',()=>open(parent,false,true)));parent.prepend(heading);}
 const summary=el('dl',null,'organization-summary'),summaryValues={};const stat=(label,value)=>{const item=el('div'),description=el('dd'),valueNode=el('span',value);summaryValues[label]=valueNode;description.append(valueNode);item.append(el('dt',label),description);summary.append(item)};
 const refreshSummary=()=>{if(token!==epoch)return;parent.querySelector('h1').textContent=o.name;for(const [label,value] of [['Balance',money(o.balance_usd)],['Credit Limit',money(o.credit_limit_usd)],['Your week',`${money(o.weekly_committed_usd)} / ${money(o.weekly_limit_usd)}`]]){if(summaryValues[label])summaryValues[label].textContent=value}};
 if(OrganizationRoles.statistics(o.role)){stat('Balance',money(o.balance_usd));stat('Credit Limit',money(o.credit_limit_usd));stat('Shared storage',StorageQuota.size(o.storage_bytes));}
 if(['member','developer','admin','supermanager'].includes(o.role))stat('Your week',`${money(o.weekly_committed_usd)} / ${money(o.weekly_limit_usd)}`);
 parent.append(summary);
 let activePage=selectedPage,subsections;
 const reload=async()=>{const next=await api(base);if(token===epoch)await details(parent,next,all,activePage)};
 const status=el('p');parent.append(status);
 if(clientManager&&['lead_client_manager','regional_client_manager'].includes(clientManagerRole)){
  const assignment=el('form',null,'organization-form role-editor'),label=el('label','Client manager'),select=el('select'),save=button('Save',()=>{}),message=el('p');save.type='submit';message.setAttribute('role','status');select.setAttribute('aria-label','Client manager');select.dataset.fieldLabel='Client manager';label.append(select);assignment.append(label,save,message);parent.append(assignment);
  try{const result=await api('/v1/sales/team');if(token!==epoch)return;for(const m of result.data){select.append(UserUI.option(m,`${m.name||m.email} · ${m.email}`));}select.value=o.client_manager_id||'';FormUI.watch(assignment,save,{valid:()=>!!select.value});FormUI.refresh();
   assignment.onsubmit=async e=>{e.preventDefault();if(save.disabled)return;const snapshot=FormUI.snapshot(assignment),selected=select.value;save.disabled=true;try{await api(base+'/client-manager',{method:'PUT',body:JSON.stringify({user:selected})});if(token!==epoch)return;FormUI.saved(assignment,snapshot);o.client_manager_id=selected;FormUI.message(message,'');}catch(error){if(token===epoch)FormUI.message(message,error.message,'error');}finally{save.disabled=false;FormUI.update(assignment);}};
  }catch(error){assignment.remove();if(token===epoch)FormUI.message(status,error.message,'error');}
 }
 let commercial={};if(clientManager){try{commercial=await api(base+'/sales-permissions');if(token!==epoch)return;}catch(e){FormUI.message(status,e.message,'error');}}
 const tabs=el('div',null,'organization-tabs'),body=el('div',null,'organization-page');parent.append(tabs,body);
 const manage=OrganizationRoles.manage(o.role);
 if(manage){try{const methods=await api(base+'/payment-methods');if(token!==epoch)return;if(methods.data?.length)summaryValues.Balance.parentElement.append(button('Top Up',async()=>{await page('Billing');body.querySelector('.organization-topups')?.scrollIntoView({block:'start',behavior:'smooth'})}));}catch{}}
 const readStatistics=OrganizationRoles.statistics(o.role);
 const canIssue=manage||o.role==='developer';
 const pages=manage?['Members','API keys','Requests','Billing','Usage']:readStatistics?(o.role==='client_manager'?['Billing','Usage','Members']:['Requests','Billing','Usage','Members']):[...(canIssue?['API keys']:[]),'Requests','Billing','Usage'];
 pages.push('Support');
 if(clientManager&&(commercial.pricing||commercial.models))pages.push('Commercial settings');
 if(manage)pages.push('Audit Log');if(manage||systemAdministrator)pages.push('Management');if(systemAdministrator)pages.push('Settings');
 async function page(name){if(!pages.includes(name))return;activePage=name;for(const tab of tabs.children){tab.setAttribute('aria-pressed',String(tab.textContent===name))}subsections?.sync();GatewayLayout.clear(body);body.replaceChildren();FormUI.message(status,'');try{
 if(name==='Support'){await ContactLinks.support(body,api,o,()=>token===epoch&&activePage==='Support');
 }else if(name==='Requests'){await window.OrganizationJournal.mount(body,o,api);
 }else if(name==='Audit Log'){await window.AuditLog.mount(body,api,{organization:o,current:()=>token===epoch&&activePage==='Audit Log'});
 }else if(name==='API keys'){await window.GatewayKeys.open(body,o.id);
 }else if(name==='Management'){
 let orgName,weekly;
 const f=form(body,'Management',async()=>{const input={name:orgName.value,default_weekly_limit_usd:nullable(weekly)};await api(base,{method:'PUT',body:JSON.stringify(input)});Object.assign(o,input);refreshSummary();await window.Workspace?.refresh()});
 orgName=field(f,'Name','text',o.name,true);weekly=field(f,'Default weekly limit for new members (USD)','number',o.default_weekly_limit_usd);f.finish();
 const logging=form(body,'Request logging',async()=>{await api(base+'/request-logging',{method:'PUT',body:JSON.stringify({minimize_request_logging:minimize.checked})});o.minimize_request_logging=minimize.checked});
 const loggingLabel=el('label',null,'system-toggle'),minimize=el('input'),loggingText=el('span');minimize.type='checkbox';minimize.checked=!!o.minimize_request_logging;loggingText.append(el('strong','Minimize request logging'),el('small','After a request finishes, its prompt, instructions and input links are removed. Usage, cost and status remain. Applies to new requests only.'));loggingLabel.append(minimize,loggingText);logging.append(loggingLabel);logging.finish();
 if(o.billing_profile){const b=o.billing_profile,a=b.address||{},card=el('section',null,'organization-billing-details'),list=el('dl');card.append(el('h2','Billing details'),list);for(const [label,value] of [['Legal name',b.business_name],['Billing email',b.email],['Billing address',[a.line1,a.line2,a.city,a.state,a.postal_code,a.country].filter(Boolean).join(', ')],['Tax IDs',(b.tax_ids||[]).map(t=>t.value).join(', ')]]){if(!value)continue;const row=el('div');row.append(el('dt',label),el('dd',value));list.append(row)}body.append(card);}
 await window.OrganizationStorage.open(body,o.id);
 }else if(name==='Commercial settings'){
 const current=()=>token===epoch&&activePage==='Commercial settings';
 if(commercial.models){await window.ModelAccess.organization(body,api,{organization:o.id,current});if(!current())return;}
 if(commercial.pricing)await window.MarkupSettings.mount(body,api,{organization:o.id,current});
 }else if(name==='Settings'){
 let credit,manager,savedCredit=o.credit_limit_usd,savedManager=o.client_manager_email||o.client_manager_id||'';
 const f=form(body,'Settings',async()=>{
  const nextCredit=nullable(credit)??0,managerValue=manager.value.trim();
  if(nextCredit!==savedCredit){
   // This endpoint requires company fields too; keep their latest saved values.
   const latest=await api(base),input={name:latest.name,default_weekly_limit_usd:latest.default_weekly_limit_usd??null,credit_limit_usd:nextCredit};
   await api(base,{method:'PUT',body:JSON.stringify(input)});savedCredit=nextCredit;Object.assign(o,input);refreshSummary();
  }
  if(managerValue!==savedManager){await api(base+'/client-manager',{method:'PUT',body:JSON.stringify({user:managerValue})});savedManager=managerValue;o.client_manager_email=managerValue.includes('@')?managerValue:'';o.client_manager_id=managerValue.includes('@')?'':managerValue}
  await window.Workspace?.refresh();
 });
 credit=field(f,'Credit Limit (USD)','number',o.credit_limit_usd,true);manager=field(f,'Client manager','text',savedManager);manager.title='Email or user ID';f.finish();
 const current=()=>token===epoch&&activePage==='Settings';
 await window.ModelAccess.organization(body,api,{organization:o.id,current});if(!current())return;
 await window.MarkupSettings.mount(body,api,{organization:o.id,current});if(!current())return;
 await window.RequestLimits.mount(body,api,{organization:o.id,current});
 }else if(name==='Members'){
 const members=(await api(base+'/members')).data;if(token!==epoch)return;
 if(members.length)FormUI.sectionHeading(body,'Members');
 const memberIdentity=m=>{const box=el('div',null,'member-contact');box.append(UserUI.identity(m,{secondary:true}));const links=ContactLinks.links(m.contacts);if(links.children.length)box.append(links);return box;};
 if(!manage){if(members.length)table(body,['Member',OrganizationRoles.heading(),'This week','Total consumption','Weekly limit'],members.map(m=>[memberIdentity(m),memberRoleLabel(m.role),money(m.weekly_committed_usd),money(m.total_consumed_usd??0),money(m.weekly_limit_usd)]));return;}
 if(members.length)table(body,['Member',OrganizationRoles.heading(),'This week','Total consumption','Weekly limit (USD)'],members.map(m=>{
   const edit=el('form',null,'member-limit-form'),limit=el('input'),roleForm=el('form',null,'role-editor member-role-editor'),role=el('select'),roleApply=el('button','Apply','secondary'),save=el('button','Save','secondary'),msg=el('small'),roleMsg=el('small');
   limit.type='number';limit.min='0';limit.max='1000000';limit.step='0.000001';limit.title='Unlimited when empty';limit.value=m.weekly_limit_usd??'';limit.setAttribute('aria-label','Weekly limit (USD)');
   const choices=OrganizationRoles.assign(systemAdministrator?'system_admin':o.role,m.role);
   if(!choices.length&&m.role!=='admin')return [memberIdentity(m),memberRoleLabel(m.role),money(m.weekly_committed_usd),money(m.total_consumed_usd??0),money(m.weekly_limit_usd)];
   for(const [value,label] of [...choices.map(value=>[value,memberRoleLabel(value)]),['remove','Remove from organization']]){const opt=el('option',label);opt.value=value;role.append(opt)}role.value=m.role;role.setAttribute('aria-label','Role');
   roleApply.type=save.type='submit';roleForm.append(role,roleApply,roleMsg);edit.append(limit,save,msg);FormUI.watch(edit,save);FormUI.watch(roleForm,roleApply);
   let saving=false;
   const update=async(nextRole,nextLimit,target)=>{if(saving)return;saving=true;const snapshot=FormUI.snapshot(target),previousRole=m.role;save.disabled=roleApply.disabled=true;try{await api(base+'/members/'+m.id,{method:'PUT',body:JSON.stringify({role:nextRole,weekly_limit_usd:nextLimit})});m.role=nextRole;m.weekly_limit_usd=nextLimit;FormUI.saved(target,snapshot);if(window.Workspace?.isSelf(m.id)){o.weekly_limit_usd=nextLimit;refreshSummary();if(target===roleForm&&nextRole!==previousRole)await reload();}}finally{saving=false;save.disabled=roleApply.disabled=false;FormUI.update(edit);FormUI.update(roleForm)}};
   edit.onsubmit=async e=>{e.preventDefault();save.disabled=true;FormUI.message(msg,'');try{await update(m.role,nullable(limit),edit)}catch(e){FormUI.message(msg,e.message,'error')}finally{save.disabled=false;FormUI.update(edit)}};
   roleForm.onsubmit=async e=>{e.preventDefault();if(role.value==='remove'){confirmAction('Remove member?',(()=>{const text=document.createDocumentFragment();text.append(UserUI.identity(m,{secondary:true}),' will lose access. Their API keys will be revoked. Files and history remain in this organization.');return text;})(),async()=>{await api(base+'/members/'+m.id,{method:'DELETE'});await reload()});return;}roleApply.disabled=true;FormUI.message(roleMsg,'');try{await update(role.value,m.weekly_limit_usd,roleForm)}catch(e){FormUI.message(roleMsg,e.message,'error')}finally{roleApply.disabled=false;FormUI.update(roleForm)}};
   return [memberIdentity(m),choices.length?roleForm:memberRoleLabel(m.role),money(m.weekly_committed_usd),money(m.total_consumed_usd??0),edit];
 }));
 if(systemAdministrator){let user;const add=form(body,'Assign an existing user',async()=>{await api(base+'/members/'+encodeURIComponent(user.value),{method:'PUT',body:JSON.stringify({role:'supermanager'})});await reload()});user=field(add,'Email or user ID','text','',true);add.finish('Assign',()=>!!user.value.trim());}

 const invitations=el('section',null,'member-invitations'),heading=el('div',null,'report-heading console-section-heading'),linkPanel=el('div',null,'invitation-link'),history=el('div');
 const invitationForm=el('form',null,'keys-form organization-form'),roleLabel=el('label','Invitation role','organization-field'),inviteRole=el('select'),createInvite=el('button','Create invitation','primary compact'),inviteStatus=el('p'),actions=el('div',null,'form-actions');
 inviteRole.setAttribute('aria-label','Invitation role');
 for(const value of OrganizationRoles.assign(systemAdministrator?'system_admin':o.role,'member')){const option=el('option',memberRoleLabel(value));option.value=value;inviteRole.append(option)}inviteRole.value='member';
 roleLabel.append(inviteRole);createInvite.type='submit';inviteStatus.setAttribute('role','status');actions.append(createInvite,inviteStatus);invitationForm.append(roleLabel,actions);
 invitationForm.onsubmit=async event=>{event.preventDefault();if(createInvite.disabled)return;const role=inviteRole.value;createInvite.disabled=inviteRole.disabled=true;inviteStatus.classList.remove('settings-warning');FormUI.message(inviteStatus,'');try{const invite=await api(base+'/invitations',{method:'POST',body:JSON.stringify({role})});if(token!==epoch||activePage!=='Members')return;const url=location.origin+'/organizations#invite='+invite.token,input=el('input');input.readOnly=true;input.value=url;input.setAttribute('aria-label','One-time invitation link');linkPanel.replaceChildren(el('p','Invited role: '+memberRoleLabel(invite.role||role)),input);await InvitationFeedback.created(inviteStatus,()=>token===epoch&&activePage==='Members'&&inviteStatus.isConnected);await loadInvitations();}catch(e){FormUI.message(inviteStatus,e.message,'error')}finally{createInvite.disabled=inviteRole.disabled=false}};
 heading.append(el('h2','Invitations'));invitations.append(heading,invitationForm,linkPanel,history);body.append(invitations);
 async function loadInvitations(){
  const items=(await api(base+'/invitations')).data;if(token!==epoch||activePage!=='Members')return;
  history.replaceChildren();if(!items.length)return;
  const now=Date.now();
  table(history,['Role','Expires','Status','Accepted by',''],items.map(i=>{
   const expires=new Date(i.expires_at),state=i.accepted_by?'Accepted':i.revoked_at?'Revoked':expires.getTime()<=now?'Expired':'Pending';
   return [memberRoleLabel(i.role||'member'),expires.toLocaleString(),state,i.accepted_by_email?UserUI.identity({id:i.accepted_by,email:i.accepted_by_email,picture:i.accepted_by_picture}):'—',state==='Pending'?button('Revoke',async()=>{try{await api(base+'/invitations/'+i.id,{method:'DELETE'});await loadInvitations()}catch(e){FormUI.message(status,e.message,'error')}}):''];
  }));
 }
 await loadInvitations();
 }else if(name==='Billing'){
 if(readStatistics){
 if(manage)await window.PaymentTopups.mount(body,api,o,()=>token===epoch&&activePage==='Billing',async()=>{await window.Workspace?.refresh();await reload()});
 if(token!==epoch||activePage!=='Billing')return;
 if(clientManager&&commercial.topup){
 let amount,reason;const operation=crypto.randomUUID();
 const f=form(body,'Top up balance',async()=>{await api(base+'/credits',{method:'POST',body:JSON.stringify({amount_usd:Number(amount.value),kind:'payment',reason:reason.value.trim(),external_id:operation})});await reload()});
 amount=field(f,'Amount (USD)','number','',true);amount.min='0.000001';amount.removeAttribute('max');
 reason=field(f,'Reason / payment reference','text','',true);reason.maxLength=1000;
 f.append(el('p','Confirm receipt of payment before crediting the balance.','field-explanation'));
 f.finish('Top up',()=>Number(amount.value)>0&&!!reason.value.trim());
 }
 if(systemAdministrator)await window.VATSettingsUI.verification(body,api,o,()=>token===epoch&&activePage==='Billing');
 if(systemAdministrator){let amount,kind,reason,external;const f=form(body,'Add credit or correction',async()=>{await api(base+'/credits',{method:'POST',body:JSON.stringify({amount_usd:Number(amount.value),kind:kind.value,reason:reason.value,external_id:external.value})});await reload()});f.querySelector('h2').outerHTML='<h3>Add credit or correction</h3>';amount=field(f,'Amount (USD)','number','',true);amount.min='-1000000';amount.removeAttribute('max');kind=el('select');kind.setAttribute('aria-label','Credit type');['payment','promo','adjustment'].forEach(v=>{const opt=el('option',v);opt.value=v;kind.append(opt)});const kindLabel=el('label','Credit type','organization-field');kindLabel.append(kind);f.append(kindLabel);reason=field(f,'Reason');reason.maxLength=1000;external=field(f,'Payment / operation ID','text',crypto.randomUUID(),true);const validateAmount=()=>{amount.min=kind.value==='adjustment'?'-1000000':'0.000001';amount.setCustomValidity(amount.value!==''&&Number(amount.value)===0?'Enter a non-zero amount.':'');};kind.addEventListener('change',validateAmount);amount.addEventListener('input',validateAmount);validateAmount();f.finish('Add credit');}
 await window.BalanceHistory.mount(body,api,o,()=>token===epoch&&activePage==='Billing',async()=>{await window.Workspace?.refresh();await reload()},manage);
 }
 const reports=el('section',null,'organization-reports');body.append(reports);
 await window.GatewayReports.open(false,{organization:o,parent:reports});
 }else if(name==='Usage'){
 const usage=el('section',null,'organization-usage');body.append(usage);
 await window.GatewayReports.openUsage(false,{organization:o,parent:usage});
 }
 }catch(e){FormUI.message(status,e.message,'error')}}
 pages.forEach(p=>tabs.append(button(p,()=>page(p))));subsections=GatewayLayout.bind(tabs,{scope:parent,label:o.name,body});await page(pages.includes(selectedPage)?selectedPage:pages[0]);
 }
 async function invitation(parent,token){
  const box=el('div',null,'keys-form organization-invitation'),viewEpoch=epoch;
  const current=()=>epoch===viewEpoch&&sessionStorage.getItem('gateway-pending-invite')===token;
  async function enter(i){
   if(sessionStorage.getItem('gateway-pending-invite')===token)sessionStorage.removeItem('gateway-pending-invite');box.remove();
   if(window.Workspace?.ready()){await Workspace.refresh();Workspace.set(i.organization_id);}else await open(parent);
  }
  try{
   const i=await api('/v1/invitations/preview',{method:'POST',body:JSON.stringify({token})});
   if(!current())return;
   if(i.already_member){await enter(i);return;}
   parent.append(box);
   const accept=button('Accept invitation',async()=>{
    accept.disabled=true;
    try{await api('/v1/invitations/accept',{method:'POST',body:JSON.stringify({token})});await enter(i)}catch(e){box.append(el('p',e.message,'operation-error'));accept.disabled=false}
   });
   box.append(el('h2','Join '+i.organization_name+'?'),el('p',i.first_owner?'You will become the first Supermanager and can manage company members and API keys.':'You will join with the '+memberRoleLabel(i.role||'member')+' role. Your other organizations are unchanged.'),button('Decline',()=>{sessionStorage.removeItem('gateway-pending-invite');box.remove()}),accept);
  }catch(e){if(current()){parent.append(box);box.append(el('p',e.message,'operation-error'),button('Dismiss',()=>{sessionStorage.removeItem('gateway-pending-invite');box.remove()}))}}
 }

 function captureInvitation(){if(location.pathname!=='/organizations'||!location.hash.startsWith('#invite='))return false;sessionStorage.setItem('gateway-pending-invite',location.hash.slice(8));history.replaceState(null,'',location.pathname+location.search);return true;}
 captureInvitation();window.addEventListener('hashchange',()=>{if(captureInvitation())showConsoleSection('organizations');});
 window.GatewayOrganizations={open,list,details,close(){epoch++;ModelInfo.close();window.SalesPermissions?.close();window.SalesBonuses?.close()}};
})();
