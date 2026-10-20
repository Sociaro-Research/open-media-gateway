(() => {
  'use strict';
  const select=document.querySelector('#workspaceSelect'),picker=document.querySelector('#workspacePicker');
  const opener=document.querySelector('#workspaceOpen'),toggle=document.querySelector('#workspaceToggle'),menu=document.querySelector('#workspaceMenu');
  const organizations=()=>items.filter(o=>!o.personal);
  let lastOrganization='',pointerType='';
  picker.addEventListener('pointerdown',e=>{pointerType=e.pointerType;});
  const touchInput=e=>(e.pointerType||pointerType||(matchMedia('(hover: none)').matches?'touch':'mouse'))!=='mouse';
  function menuVisible(show,focus=false){show=show&&organizations().length>1;menu.hidden=!show;toggle.setAttribute('aria-expanded',String(show));opener.setAttribute('aria-expanded',String(show));if(show&&focus)menu.querySelector('[aria-checked=true],button')?.focus();}
  function renderMenu(){const orgs=organizations();if(!orgs.some(o=>o.id===lastOrganization))lastOrganization=orgs[0]?.id||'';if(current()&&!current().personal)lastOrganization=selected;opener.textContent=orgs.find(o=>o.id===lastOrganization)?.name||'Organizations';opener.title=opener.textContent;picker.hidden=!orgs.length;toggle.hidden=orgs.length<2;if(orgs.length>1){opener.setAttribute('aria-haspopup','menu');opener.setAttribute('aria-controls','workspaceMenu');}else{opener.removeAttribute('aria-haspopup');opener.removeAttribute('aria-controls');}menuVisible(false);menu.replaceChildren(...orgs.map(o=>{const b=node('button',o.name);b.type='button';b.setAttribute('role','menuitemradio');b.setAttribute('aria-checked',String(o.id===selected));b.onclick=()=>{menuVisible(false);set(o.id);opener.focus();};return b;}));}
  opener.onclick=e=>{if(organizations().length>1&&touchInput(e)){menuVisible(menu.hidden);return;}menuVisible(false);set(lastOrganization);};
  toggle.onclick=e=>menuVisible(menu.hidden,!touchInput(e));
  picker.addEventListener('click',e=>{if(e.target===picker&&organizations().length>1)menuVisible(menu.hidden);});
  picker.addEventListener('pointerenter',e=>{if(e.pointerType==='mouse'&&items.length)menuVisible(true);});
  picker.addEventListener('pointerleave',e=>{if(e.pointerType==='mouse')menuVisible(false);});
  // Touch browsers may blur to no focus target before delivering the menu click.
  // Outside clicks close the menu below; focus dismissal is for a real new target.
  picker.addEventListener('focusout',e=>{if(pointerType!=='touch'&&pointerType!=='pen'&&e.relatedTarget&&!picker.contains(e.relatedTarget))menuVisible(false);});
  picker.addEventListener('keydown',e=>{pointerType='keyboard';if(organizations().length<2)return;if(e.key==='Escape'){menuVisible(false);toggle.focus();e.preventDefault();}else if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){e.preventDefault();const buttons=[...menu.querySelectorAll('button')],index=buttons.indexOf(document.activeElement);if(menu.hidden||index<0){menuVisible(true,true);return;}const next=e.key==='Home'?0:e.key==='End'?buttons.length-1:(index+(e.key==='ArrowUp'?-1:1)+buttons.length)%buttons.length;buttons[next]?.focus();}});
  document.addEventListener('click',e=>{if(!picker.contains(e.target))menuVisible(false);});
  let loaded=false,items=[],selected='',binding='',embedded=null,home=null,sequence=0;
  const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!=null)e.textContent=text;if(cls)e.className=cls;return e;};
  const current=()=>items.find(o=>o.id===selected)||null;
  function persist(){try{sessionStorage.setItem('gateway-workspace-'+binding,selected)}catch{}}
  function restore(){if(embedded){home.append(embedded);embedded.classList.remove('personal-section');embedded.querySelector('h1')?.removeAttribute('aria-level');embedded.hidden=true;embedded=null;}sharedConfiguration=false;}
  async function initialize(user){binding=user?.id||'api';await refresh();const ret=window.PaymentTopups?.returned();if(ret)set(ret.org,{navigate:false});}
  async function refresh(){
    const token=++sequence;
    const organizations=await GatewayOrganizations.list(false);if(token!==sequence)return;
    items=organizations;
    if(!clientManager&&consolePolicy.personal_providers)items.push({id:'personal',name:'Profile',personal:true});
    let previous=selected;if(!loaded){try{previous=sessionStorage.getItem('gateway-workspace-'+binding)||''}catch{}}
    selected=items.some(o=>o.id===previous)?previous:items[0]?.id||'';
    select.replaceChildren(...items.map(o=>new Option(o.name,o.id)));if(!items.length)select.append(new Option('No organizations',''));select.value=selected;select.disabled=!items.length;loaded=true;persist();renderMenu();FormUI.refresh();
    if(previous&&previous!==selected)window.GenerationKey?.activateWorkspace(selected);
  }
  function set(id,{key=false,navigate=true}={}){if(!items.some(o=>o.id===id))return false;restore();const changed=selected!==id;selected=id;select.value=id;persist();renderMenu();FormUI.refresh();if(changed&&!key)window.GenerationKey?.activateWorkspace(id);if(navigate)showConsoleSection('organizations');return true;}
  select.addEventListener('change',()=>set(select.value));
  select.addEventListener('click',()=>{if(currentConsoleSection!=='organizations'&&items.length===1)showConsoleSection('organizations')});
  async function open(page){
    // The same organization can be selected again before an older read settles.
    const navigation=consoleNavigation;
    if(window.PaymentTopups?.returned()?.org===selected)page='Billing';
    restore();const parent=document.querySelector('#organizationsContent'),o=current();
    if(!o){await GatewayOrganizations.open(parent,false,true);return;}
    if(sessionStorage.getItem('gateway-pending-invite')){await GatewayOrganizations.open(parent,false,true);return;}
    if(!o.personal){const latest=await api('/v1/organizations/'+o.id);if(navigation!==consoleNavigation||selected!==o.id||currentConsoleSection!=='organizations')return;await GatewayOrganizations.details(parent,latest,false,page);return;}
    await openProfile(parent,page);
  }
  async function openProfile(parent,page){
    restore();
    GatewayLayout.clear(parent);parent.replaceChildren(node('h1','Profile'));const tabs=node('div',null,'organization-tabs'),body=node('div',null,'organization-page');parent.append(tabs,body);
    let subsections;const pages=['Profile',...(!clientManager&&consolePolicy.personal_providers?['Providers','Storage','API keys','Usage','Reports']:[])];
    async function display(name){if(name!=='Profile'&&current()?.id!=='personal')set('personal',{navigate:false});restore();GatewayKeys.close();GatewayReports.close();body.replaceChildren();for(const b of tabs.children)b.setAttribute('aria-pressed',String(b.textContent===name));subsections?.sync();
      if(name==='Profile')GatewayProfile.open(body);
      else if(name==='API keys')await GatewayKeys.open(body,'');
      else if(name==='Reports')await GatewayReports.open(false,{personal:true,parent:body});
      else if(name==='Usage')await GatewayReports.openUsage(false,{personal:true,parent:body});
      else {const kind=name.toLowerCase();embedded=document.querySelector('#'+kind+'View');home=embedded.parentNode;body.append(embedded);embedded.classList.add('personal-section');embedded.querySelector('h1')?.setAttribute('aria-level','2');embedded.hidden=false;if(kind==='providers')await loadAdminProviders();else await loadStorageSettings();}
    }
    for(const name of pages){const button=node('button',name,'secondary');button.type='button';button.onclick=()=>display(name);tabs.append(button);}
    subsections=GatewayLayout.bind(tabs,{scope:parent,label:'Profile',body});
    await display(pages.includes(page)?page:pages[0]);
  }
  async function acceptKey(key){
    const id=key.organization_id||'personal';if(!loaded||selected===id)return true;
    const destination=items.find(o=>o.id===id);if(!destination)throw Error('This key belongs to a workspace unavailable to your signed-in account.');
    return new Promise(resolve=>{document.querySelectorAll('.workspace-switch-dialog').forEach(d=>d.close());const dialog=node('dialog',null,'organization-dialog workspace-switch-dialog');dialog.append(node('h2','Switch to '+destination.name+'?'),node('p','This API key belongs to '+destination.name+'. Generation will use that workspace.'));const actions=node('div',null,'form-actions'),cancel=node('button','Cancel','secondary'),yes=node('button','Switch','primary compact');cancel.type=yes.type='button';let accepted=false;cancel.onclick=()=>dialog.close();yes.onclick=()=>{accepted=true;set(id,{key:true,navigate:false});dialog.close();};actions.append(cancel,yes);dialog.append(actions);dialog.onclose=()=>{dialog.remove();resolve(accepted)};document.body.append(dialog);dialog.showModal();cancel.focus();});
  }
  const user=document.querySelector('#userMenu'),userToggle=document.querySelector('#userMenuToggle'),userDropdown=document.querySelector('#userDropdown');
  function userMenuVisible(show,focus=false){userDropdown.hidden=!show;userToggle.setAttribute('aria-expanded',String(show));if(show){menuVisible(false);if(focus)userDropdown.querySelector('button:not([hidden])')?.focus();}}
  userToggle.onclick=()=>userMenuVisible(userDropdown.hidden);
  user.addEventListener('pointerenter',e=>{if(e.pointerType==='mouse')userMenuVisible(true)});
  user.addEventListener('pointerleave',e=>{if(e.pointerType==='mouse')userMenuVisible(false)});
  user.addEventListener('focusout',e=>{if(!user.contains(e.relatedTarget))userMenuVisible(false)});
  user.addEventListener('keydown',e=>{if(e.key==='Escape'){userMenuVisible(false);userToggle.focus();e.preventDefault()}else if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){e.preventDefault();const buttons=[...userDropdown.querySelectorAll('button:not([hidden])')],index=buttons.indexOf(document.activeElement);if(userDropdown.hidden||index<0){userMenuVisible(true,true);return;}buttons[e.key==='Home'?0:e.key==='End'?buttons.length-1:(index+(e.key==='ArrowUp'?-1:1)+buttons.length)%buttons.length]?.focus();}});
  document.addEventListener('click',e=>{if(!user.contains(e.target))userMenuVisible(false)});
  document.addEventListener('keydown',e=>{if(e.key==='Escape'){if(!userDropdown.hidden){userMenuVisible(false);userToggle.focus()}if(!menu.hidden){menuVisible(false);toggle.focus()}}});
  document.querySelector('#personalSettingsButton').onclick=()=>{userMenuVisible(false);showConsoleSection('profile');userToggle.focus()};
  document.querySelector('#logoutButton').addEventListener('click',()=>userMenuVisible(false));
  window.Workspace={initialize,refresh,open,openProfile,restore,set,current,acceptKey,id:()=>selected,isSelf:id=>id===binding,ready:()=>loaded};
})();
