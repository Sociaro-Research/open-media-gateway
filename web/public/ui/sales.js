(() => {
 'use strict';
 const roles=[['regional_client_manager','Regional client manager'],['lead_client_manager','Lead client manager'],['client_manager','Client manager']];
 const rank=role=>roles.some(([id])=>id===role)?3-roles.findIndex(([id])=>id===role):0,label=role=>roles.find(([id])=>id===role)?.[1]||role;
 const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,fn)=>{const b=node('button',text,'secondary');b.type='button';b.onclick=fn;return b;};
 let cleanup=()=>{};
 async function mount(parent,api,current){
  cleanup();let closed=false,busy=false,loading=false,readError=false,readSerial=0,timer,state={version:0,members:[]},dragged='',dialog=null;
  const alive=()=>!closed&&current()&&parent.isConnected;
  const interacting=()=>dragged||document.querySelector('.system-dialog[open]');
  function schedule(delay=15000){clearTimeout(timer);if(alive()&&!document.hidden)timer=setTimeout(async()=>{await load({background:true});schedule();},delay);}
  function resume(){if(document.hidden)clearTimeout(timer);else schedule(0);}
  cleanup=()=>{closed=true;readSerial++;clearTimeout(timer);window.removeEventListener('focus',resume);document.removeEventListener('visibilitychange',resume);dialog?.close();window.SalesPermissions?.close();window.SalesBonuses?.close();};
  parent.replaceChildren();const heading=FormUI.sectionHeading(parent,'Sales management','h1'),actions=node('div',null,'form-heading-actions'),status=node('p'),tree=node('div',null,'sales-tree');
  const add=button('Add employee',()=>edit());actions.append(button('Bonuses',()=>SalesBonuses.open(api)),add);actions.style.flexWrap='wrap';actions.style.maxWidth='100%';heading.parentElement.append(actions);
  status.setAttribute('role','status');parent.append(node('p','Drag employees to another supervisor or role slot. Click an employee to edit their role and reporting line.','field-explanation'),status,tree);
  function message(text,type=''){FormUI.message(status,text,type);}
  function setBusy(value){busy=value;if(value){readSerial++;loading=false;}add.disabled=value;tree.querySelectorAll('button').forEach(b=>b.disabled=value);}
  function member(id){return state.members.find(m=>m.id===id);}
  function eligibleSupervisor(role,parentID){while(parentID&&rank(member(parentID)?.role)<=rank(role))parentID=member(parentID)?.supervisor_id||'';return parentID;}
  function below(candidate,ancestor){const seen=new Set();for(let m=member(candidate);m&&!seen.has(m.id);m=member(m.supervisor_id)){if(m.id===ancestor)return true;seen.add(m.id);}return false;}
  function title(id){const m=member(id);return m?`${m.name||m.email} (${label(m.role)})`:'Administrator';}
  async function load({background=false}={}){
   if(busy||!alive()||background&&(loading||document.hidden||interacting()))return false;
   if(!background)setBusy(true);const request=++readSerial;loading=true;
   try{const result=await api('/v1/system/sales');if(!alive()||request!==readSerial||background&&(busy||document.hidden||interacting()))return false;
    const changed=JSON.stringify(state)!==JSON.stringify(result),wasEmpty=!state.members.length,focused=document.activeElement,employee=focused?.closest('[data-sales-id]')?.dataset.salesId,action=focused?.classList.contains('sales-drag')?'.sales-drag':focused?.classList.contains('sales-identity')?'.sales-identity':'.sales-permissions';state=result;add.hidden=false;
    if(changed){render();if(employee)(tree.querySelector(`[data-sales-id="${employee}"] ${action}`)||add).focus({preventScroll:true});}if(!background||readError||wasEmpty!==!state.members.length)message(state.members.length?'':'No sales employees. Add an existing user to start.');readError=false;return true;
   }catch(e){if(alive()&&request===readSerial){if(e.status===401||e.status===403){state={version:0,members:[]};tree.replaceChildren();add.hidden=true;}readError=true;message(e.message,'error');}return false;}
   finally{if(request===readSerial){loading=false;if(alive()&&!background)setBusy(false);}}
  }
  async function save(id,role,supervisor,version,fillVacancy=false,newTeam=false){
   const result=await api('/v1/system/sales/'+encodeURIComponent(id),{method:'PUT',body:JSON.stringify({role,supervisor_id:supervisor,version,fill_vacancy:fillVacancy,new_team:newTeam})});
   if(alive()){state=result;render();}return result;
  }
  function peopleList(people){const list=node('ul',null,'sales-review-people');for(const person of people){const item=node('li');item.append(UserUI.identity(person));list.append(item);}return list;}
  function impact(id,role,parentID,newTeam=false){
   const m=member(id),fragment=document.createDocumentFragment();
   if(!m){fragment.append(node('p','This user becomes a sales employee. Existing API keys are revoked. Organization owners and managers must first leave those roles.'));return fragment;}
   const reports=state.members.filter(c=>c.supervisor_id===id&&(newTeam||rank(c.role)>=rank(role)));
   fragment.append(node('p','Existing clients stay assigned to this employee.'));
   if(reports.length){const groups=new Map();for(const report of reports){const parent=newTeam?m.supervisor_id:eligibleSupervisor(report.role,parentID);if(!groups.has(parent))groups.set(parent,[]);groups.get(parent).push(report);}for(const [parent,people]of groups){const supervisor=member(parent),description=node('p',newTeam?'Start an empty team. The existing team stays with ':`${people.length} direct report(s) will move to `);description.append(supervisor?UserUI.identity(supervisor):'Administrator',':');fragment.append(description,peopleList(people));}}
   else if(state.members.some(c=>c.supervisor_id===id))fragment.append(node('p','Direct reports move with this employee.'));
   return fragment;
  }
  async function move(id,role,parentID,fillVacancy=false,newTeam=false){
   if(busy||!alive())return;const m=member(id);if(!m)return;
   if(id===parentID||(parentID&&rank(member(parentID)?.role)<=rank(role))){message('Choose a supervisor with a higher sales role.','error');return;}
   if(m.role===role&&m.supervisor_id===parentID&&!fillVacancy&&(!newTeam||!state.members.some(c=>c.supervisor_id===id))){message(newTeam?'This employee already leads an empty team here.':'This employee is already in this position.');return;}
   if(m.role!==role||fillVacancy||newTeam){edit(m,{role,parentID,fillVacancy,newTeam});return;}
   setBusy(true);let conflict=false;
   try{await save(id,role,parentID,state.version);if(alive()){message('Sales structure updated.');tree.querySelector(`[data-sales-id="${id}"] .sales-identity`)?.focus();}}
   catch(e){conflict=e.status===409;if(alive()&&!conflict)message(e.message,'error');}
   finally{if(alive())setBusy(false);}
   if(conflict&&await load())message('The structure changed. Review the updated tree and try again.','error');
  }
  function dropTarget(element,parentID,role,fillVacancy=false,newTeam=false){
   element.dataset.salesParent=parentID;element.dataset.salesRole=role||'';element.dataset.salesVacancy=String(fillVacancy);
   element.addEventListener('dragover',e=>{if(busy||!dragged)return;e.preventDefault();e.stopPropagation();e.dataTransfer.dropEffect='move';element.classList.add('sales-drop-active');});
   element.addEventListener('dragleave',e=>{if(!element.contains(e.relatedTarget))element.classList.remove('sales-drop-active');});
   element.addEventListener('drop',e=>{e.preventDefault();e.stopPropagation();element.classList.remove('sales-drop-active');const id=e.dataTransfer.getData('text/plain');if(id!==dragged)return;const m=member(id);if(m){let destination=parentID,adopt=fillVacancy;if(role&&below(parentID,id)){destination=eligibleSupervisor(role,m.supervisor_id);adopt=false;}move(id,role||m.role,destination,adopt,newTeam);}dragged='';});
  }
  function render(){
   tree.replaceChildren();const root=node('div','Administrator','sales-root');tree.append(root);
   dropTarget(root,'',null);const list=node('ul',null,'sales-branches');list.setAttribute('aria-label','Sales hierarchy');tree.append(list);level(list,'',3);
  }
  function level(list,parentID,expected){
   const role=roles[3-expected][0],direct=state.members.filter(m=>m.supervisor_id===parentID),peers=direct.filter(m=>m.role===role);
   for(const m of peers){
    const item=node('li',null,'sales-branch'),card=node('div',null,'sales-person');card.dataset.salesId=m.id;
    const identity=node('button',null,'sales-identity'),name=node('strong',m.name||m.email),email=node('small',m.email),badge=node('span',`${label(m.role)} · ${m.own_clients} clients`,'sales-role');identity.type='button';identity.setAttribute('aria-label','Edit sales employee '+(m.name||m.email));identity.onclick=()=>edit(m);const details=node('span',null,'sales-identity-details');details.append(name,email,badge);identity.append(UserUI.avatar(m),details);
    const drag=button('⠿',()=>edit(m));drag.classList.add('sales-drag');drag.draggable=true;drag.setAttribute('aria-label','Move '+(m.name||m.email));drag.title='Drag to move';
    drag.addEventListener('dragstart',e=>{if(busy){e.preventDefault();return;}dragged=m.id;e.dataTransfer.setData('text/plain',m.id);e.dataTransfer.effectAllowed='move';card.classList.add('sales-dragging');});
    drag.addEventListener('dragend',()=>{dragged='';card.classList.remove('sales-dragging');tree.querySelectorAll('.sales-drop-active').forEach(n=>n.classList.remove('sales-drop-active'));});
    const permissions=button('Permissions',()=>SalesPermissions.open(api,m.id,()=>load()));permissions.classList.add('sales-permissions');card.append(drag,identity,permissions);item.append(card);list.append(item);
    if(expected>1){dropTarget(card,m.id,null);const children=node('ul',null,'sales-branches');item.append(children);level(children,m.id,expected-1);}
   }
   const skipped=direct.filter(m=>rank(m.role)<expected);
   if(expected>1&&skipped.length){const vacant=node('li',null,'sales-vacancy'),slot=node('div',null,'sales-role-slot'),children=node('ul',null,'sales-branches');dropTarget(slot,parentID,role,true);slot.append(node('span',`${label(role)} · vacant · Lead this team`));vacant.append(slot,children);list.append(vacant);level(children,parentID,expected-1);}
   if(expected>1||parentID||peers.length){const separate=node('li',null,'sales-role-slot sales-new-team');dropTarget(separate,parentID,role,false,expected>1);separate.append(node('span',expected===3?`+ New region · ${label(role)}`:expected===2?`+ New team · ${label(role)}`:'+ Client manager'));list.append(separate);}
   if(expected>1&&!skipped.length)level(list,parentID,expected-1);
  }

  function selectField(form,text,name,options){const wrap=node('label',text),select=node('select');select.name=name;select.setAttribute('aria-label',text);for(const [value,text]of options){const o=node('option',text);o.value=value;select.append(o);}wrap.append(select);form.append(wrap);return select;}
  function edit(employee,preset={}){
   if(busy||!alive())return;dialog?.close();const d=node('dialog',null,'keys-revoke-dialog system-dialog sales-dialog sales-editor-dialog'),form=node('form'),note=node('div',null,'field-explanation sales-role-note'),error=node('p'),actions=node('div',null,'dialog-actions');
   let userSelect,submitting=false,fillVacancy=false,newTeam=false;const {heading:h,close}=FormUI.dialogHeading(form,employee?'Move / change role':'Add sales employee',()=>{if(!submitting)d.close();});
   d.setAttribute('aria-labelledby','sales-dialog-title');h.id='sales-dialog-title';
   const submit=node('button',employee?'Apply':'Add employee','primary compact');submit.type='submit';
   if(!employee){
    userSelect=selectField(form,'Employee','employee',[['','Select an existing user']]);userSelect.required=true;
   }else{const person=UserUI.identity(employee,{secondary:true});person.classList.add('sales-editor-person');form.append(person);}
   const role=selectField(form,'Sales role','role',roles),supervisor=selectField(form,'Reports to','supervisor',[]);role.value=employee?.role||'client_manager';
   const team=selectField(form,'Team','team',[['keep','Keep current team'],['independent','New / independent team'],['existing','Lead existing team']]),teamGroup=team.parentElement;
   let desired=employee?.supervisor_id??'';
   function updateTeam(){const count=state.members.filter(m=>m.id!==employee?.id&&m.supervisor_id===supervisor.value&&rank(m.role)<rank(role.value)).length,hasReports=!!employee&&state.members.some(m=>m.supervisor_id===employee.id);teamGroup.hidden=rank(role.value)<2||!count&&!hasReports;if(teamGroup.hidden)fillVacancy=newTeam=false;team.options[0].disabled=!hasReports;team.options[2].disabled=!count;team.options[2].textContent=`Lead existing team (${count} people)`;team.value=fillVacancy?'existing':newTeam||!hasReports?'independent':'keep';}
   team.onchange=()=>{fillVacancy=team.value==='existing';newTeam=team.value==='independent';updateNote();FormUI.update(form);};
   function updateParents(selected){const previous=typeof selected==='string'?selected:supervisor.value||desired;supervisor.replaceChildren();supervisor.append(new Option('Administrator',''));for(const m of state.members.filter(m=>!below(m.id,employee?.id)&&rank(m.role)>rank(role.value)))supervisor.append(UserUI.option(m,title(m.id)));supervisor.value=[...supervisor.options].some(o=>o.value===previous)?previous:'';desired=supervisor.value;updateTeam();updateNote();FormUI.refresh();FormUI.update(form);}
   function updateNote(){note.replaceChildren(impact(employee?.id,role.value,supervisor.value,newTeam));if(fillVacancy){const team=state.members.filter(m=>m.id!==employee?.id&&m.supervisor_id===supervisor.value&&rank(m.role)<rank(role.value));note.append(node('p','Lead the existing team:'),team.length?peopleList(team):node('p','No unassigned team members.'));}}
   role.onchange=()=>{fillVacancy=newTeam=false;updateParents();};supervisor.onchange=()=>{fillVacancy=newTeam=false;desired=supervisor.value;updateTeam();updateNote();FormUI.update(form);};updateParents();form.append(note,error);actions.append(submit);form.append(actions);d.append(form);document.body.append(d);dialog=d;
   const valid=()=>FormUI.valid(form)&&!submitting;
   FormUI.watch(form,submit,{valid,placement:'footer',read:()=>JSON.stringify([role.value,supervisor.value,userSelect?.value,fillVacancy,newTeam])});if(employee&&preset.role){role.value=preset.role;fillVacancy=!!preset.fillVacancy;newTeam=!!preset.newTeam;updateParents(preset.parentID??'');FormUI.update(form);}
   const editingVersion=state.version;
   form.onsubmit=async e=>{e.preventDefault();if(submitting||submit.disabled||!form.reportValidity())return;submitting=true;let conflict=false;const controls=[...form.elements].filter(control=>!control.disabled);controls.forEach(control=>control.disabled=true);submit.disabled=close.disabled=true;setBusy(true);d.setAttribute('aria-busy','true');FormUI.message(error,'');
    try{await save(employee?.id||userSelect.value,role.value,supervisor.value,editingVersion,fillVacancy,newTeam);d.close();if(alive())message(employee?'Sales structure updated.':'Employee added.');}
    catch(e){conflict=e.status===409;if(conflict)d.close();else if(d.isConnected)FormUI.message(error,e.message,'error');}
    finally{submitting=false;if(alive())setBusy(false);if(d.isConnected){controls.forEach(control=>control.disabled=false);close.disabled=false;d.removeAttribute('aria-busy');FormUI.update(form);}}
    if(conflict&&await load())message('The structure changed or this placement is unavailable. Review the updated tree and try again.','error');
   };
   d.addEventListener('cancel',e=>{if(submitting)e.preventDefault();});d.onclose=()=>{d.remove();if(dialog===d)dialog=null;schedule(0);requestAnimationFrame(()=>{if(alive()&&!dialog&&!busy)(employee?tree.querySelector(`[data-sales-id="${employee.id}"] .sales-identity`):add)?.focus();});};
   d.showModal();FormUI.refresh();if(userSelect)ControlPickers.search(userSelect,{label:'Search by name or email',empty:'No eligible users. Users must sign in before they can be added.',load:async query=>{const result=await api('/v1/system/users?'+new URLSearchParams({search:query}));return {options:result.data.filter(u=>!u.is_system_admin&&!u.is_client_manager).map(u=>UserUI.option(u,`${u.name||u.email} · ${u.email}`)),message:result.has_more?'Showing the first 50 matches. Narrow the search to find another user.':''};}});d.querySelector('[role=combobox]')?.focus();
  }
  await load();
  if(alive()){window.addEventListener('focus',resume);document.addEventListener('visibilitychange',resume);schedule();}
 }
 window.SalesHierarchy={mount,roles,close:()=>cleanup()};
})();
