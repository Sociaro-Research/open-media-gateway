(() => {
 'use strict';
 const NO_PROJECT='none'; // Read-only filter; real project IDs are UUIDs.
 const select=document.querySelector('#journalProject'), message=document.querySelector('#projectMessage');
 let canManage=true,company=false,hasConnectedKey=false;
 let projects=[],selected='',epoch=0,reading=null,readingQuiet=false,write=null,dialog=null,assignmentMenu=null;
 const createButton=document.querySelector('#createProject'),manageButton=document.querySelector('#manageProject');
 const current=()=>projects.find(p=>p.id===selected);
 const projectLabel=p=>{const duplicates=projects.filter(other=>other.name.toLocaleLowerCase()===p.name.toLocaleLowerCase());return duplicates.length>1?p.name+' · '+(duplicates.some(other=>other.id!==p.id&&other.id.slice(0,8)===p.id.slice(0,8))?p.id:p.id.slice(0,8)):p.name;};
 const remember=()=>{try{sessionStorage.setItem('gateway-journal-project',selected);}catch{}};
 const feedback=(text='')=>{FormUI.message(message,text,'error');message.hidden=!text;};
 const changed=(metadata=false)=>{remember();window.dispatchEvent(new CustomEvent('gateway-project-change',{detail:{metadata}}));};
 const render=()=>{
  const options=[new Option('All projects',''),new Option('No project',NO_PROJECT),...projects.map(p=>new Option(projectLabel(p),p.id))];
  createButton.disabled=!window.GenerationKey?.value()||!canManage;manageButton.disabled=createButton.disabled||!current();
  select.replaceChildren(...options);select.value=selected;select.disabled=!window.GenerationKey?.value();window.ControlPickers?.sync(select);
  const trigger=select.closest('.floating-field')?.querySelector('.select-trigger'),label=select.selectedOptions[0]?.textContent||'';
  if(trigger){trigger.title=label;trigger.setAttribute('aria-description',label);}
 };
 function accepted(previous){epoch++;reading=null;ControlPickers.dismiss(manageButton);render();feedback();changed(selected===previous);}
 async function load({quiet=false}={}){
  if(!window.GenerationKey?.value())return false;if(reading){readingQuiet=readingQuiet&&quiet;return reading;}readingQuiet=quiet;
  const version=epoch;
  reading=(async()=>{try{
   const result=await api('/v1/projects',{expectedKey:window.GenerationKey.value()});if(version!==epoch)return;
   const next=result.data||[],changedPermission=canManage!==(result.can_manage_projects!==false);canManage=result.can_manage_projects!==false;company=result.scope==='company';
   if(!projects.length&&!selected){let saved='';try{saved=sessionStorage.getItem('gateway-journal-project')||'';}catch{}if(saved===NO_PROJECT||next.some(p=>p.id===saved)){selected=saved;changed();}}
   if(selected&&selected!==NO_PROJECT&&!next.some(p=>p.id===selected)){selected='';changed();}
   if(JSON.stringify(next)!==JSON.stringify(projects)||select.value!==selected||select.disabled||changedPermission){projects=next;render();}if(!readingQuiet)feedback();return true;
  }catch(error){if(version===epoch&&!readingQuiet)feedback('Could not load projects: '+error.message);return false;}
  finally{if(version===epoch){reading=null;readingQuiet=false;}}})();return reading;
 }
 function open(title,trigger){
  if(dialog)return null;
  const version=epoch,key=window.GenerationKey.value(),previous=selected;
  const panel=document.createElement('dialog');panel.className='keys-revoke-dialog';panel.setAttribute('aria-label',title);dialog=panel;
  const form=document.createElement('form');form.className='project-dialog-form';panel.append(form);
  const close=()=>{if(write)return;panel.close();panel.remove();if(dialog===panel)dialog=null;const target=trigger?.isConnected?trigger:select.closest('.floating-field')?.querySelector('.select-trigger');target?.focus();};
  FormUI.dialogHeading(form,title,close);
  const status=document.createElement('p');status.setAttribute('role','status');
  const actions=document.createElement('div');actions.className='dialog-actions';
  const cancel=document.createElement('button');cancel.type='button';cancel.className='secondary';cancel.textContent='Cancel';cancel.onclick=close;
  const save=document.createElement('button');save.type='submit';save.className='primary compact';actions.append(cancel,save);
  panel.addEventListener('cancel',event=>{event.preventDefault();close();});
  const finish=(focus)=>{form.append(status,actions);document.body.append(panel);FormUI.refresh();panel.showModal();focus?.focus();};
  const submit=(operation,onError)=>{
   form.onsubmit=async event=>{
    event.preventDefault();if(write||!form.reportValidity())return;
    const controller=new AbortController();write=controller;for(const control of form.querySelectorAll('button,input,select'))control.disabled=true;
    FormUI.message(status,save.textContent==='Create'?'Creating…':'Saving…');
    try{
     await operation(options=>({...options,signal:controller.signal,expectedKey:key}));if(version!==epoch)return;
     write=null;close();accepted(previous);
    }catch(error){
     if(version!==epoch)return;
     if(error.status===409||error.status===400){await load();if(version!==epoch)return;}
     if(onError)await onError(error);else FormUI.message(status,error.message,'error');
    }finally{if(version===epoch){write=null;for(const control of form.querySelectorAll('button,input,select'))control.disabled=false;}}
   };
  };
  return {form,status,save,finish,submit,version,key,panel};
 }
 function editor(project=null){
  let baseline=project?{...project}:null;
  const ui=open(project?'Rename project':'Create project');if(!ui)return;
  const label=document.createElement('label');label.textContent='Project name';
  const input=document.createElement('input');input.name='name';input.required=true;input.maxLength=80;input.autocomplete='off';input.value=project?.name||'';label.append(input);ui.form.append(label);
  ui.save.textContent=project?'Save':'Create';
  input.oninput=()=>input.setCustomValidity('');
  ui.form.addEventListener('submit',event=>{const name=input.value.trim();input.setCustomValidity(!name||/[\u0000-\u001f\u007f-\u009f]/.test(name)?'Enter a project name without control characters.':'');if(!ui.form.reportValidity())event.preventDefault();});
  ui.submit(async options=>{
   const value=await api(baseline?'/v1/projects/'+baseline.id:'/v1/projects',options({method:baseline?'PATCH':'POST',body:JSON.stringify({name:input.value.trim(),...(baseline?{version:baseline.version}:{})})}));
   projects=[...projects.filter(p=>p.id!==value.id),value].sort((a,b)=>a.name.localeCompare(b.name));selected=value.id;
  },async error=>{
   const latest=baseline&&projects.find(p=>p.id===baseline.id);
   if(error.status===409&&latest){baseline={...latest};FormUI.message(ui.status,`${error.message}. Current name: ${latest.name}. Review before saving again.`,'error');}
   else FormUI.message(ui.status,error.message,'error');
  });ui.finish(input);
 }
 function remove(project){
  let baseline={...project};const ui=open('Delete project?');if(!ui)return;
  const text=document.createElement('p');text.textContent=`Delete “${project.name}”? Generations and result files will stay in the Journal. ${company?'This changes the shared project for everyone in this company. ':''}Only this project's tags will be cleared.`;ui.form.append(text);ui.save.textContent='Delete';
  ui.submit(async options=>{await api('/v1/projects/'+baseline.id,options({method:'DELETE',body:JSON.stringify({version:baseline.version})}));projects=projects.filter(p=>p.id!==baseline.id);if(selected===baseline.id)selected='';},async error=>{
   const latest=projects.find(p=>p.id===baseline.id);
   if(error.status===409&&latest){baseline={...latest};text.textContent=`The project is now named “${latest.name}”. Delete it and clear its tags? Generations and result files will stay in the Journal.`;FormUI.message(ui.status,'Project changed. Review before confirming deletion again.','error');}
   else FormUI.message(ui.status,error.message,'error');
  });ui.finish(ui.save);
 }
 async function assign(task,trigger,{focus=true}={}){
  if(trigger.disabled||task.can_change_project===false||task.content_access===false||!window.GenerationKey.value())return;
  if(assignmentMenu?.trigger===trigger&&assignmentMenu.popup.open){if(focus)assignmentMenu.popup.focus();return;}
  const version=epoch,key=window.GenerationKey.value();let taskVersion=task.project_version||0,reviewing=false,pending=false;
  const popup=ControlPickers.menu(trigger,{label:'Generation project',side:true,focus,items:[{label:'Loading projects…',disabled:true}],onClose:()=>{if(assignmentMenu?.trigger===trigger)assignmentMenu=null;}});
  assignmentMenu={taskID:task.id,trigger,popup,rebind(latest,next,restoreFocus){
   const updated=taskVersion!==(latest.project_version||0)||task.project_id!==latest.project_id;
   task=latest;trigger=next;this.trigger=next;next.closest('.task-menu').open=true;popup.rebind(next,{focus:restoreFocus});
   if(updated&&!pending){taskVersion=latest.project_version||0;popup.update(choices());if(reviewing)popup.message(reviewMessage());}
  }};
  const reviewMessage=()=> 'Generation changed. Current project: '+(projects.find(p=>p.id===task.project_id)?.name||'No project')+'. Review before choosing again.';
  const fresh=()=>version===epoch&&key===window.GenerationKey.value()&&popup.open;
  const choices=()=>projects.length?projects.map(project=>({key:project.id,label:projectLabel(project),checked:task.project_id===project.id,onSelect:async()=>{
   if(!fresh())return;const destination=task.project_id===project.id?'':project.id,controller=new AbortController();write=controller;pending=true;
   try{
    const result=await api('/v1/generations/'+task.id+'/project',{method:'PATCH',body:JSON.stringify({project_id:destination,version:taskVersion}),signal:controller.signal,expectedKey:key});
    if(!fresh())return;task.project_id=result.project_id||'';taskVersion=result.version;task.project_version=taskVersion;
    assignmentMenu?.popup.close(false);
    const menu=trigger.closest('.task-menu');menu.open=false;menu.querySelector('summary')?.focus({preventScroll:true});changed(true);
   }catch(error){
    if(!fresh())return;
    if(error.status===409){const latest=await api('/v1/generations/'+task.id,{expectedKey:key,signal:controller.signal});if(!fresh())return;task.project_id=latest.project_id||'';taskVersion=latest.project_version||0;reviewing=true;popup.update(choices());throw Error(reviewMessage());}
    if(error.status===400){await load({quiet:true});if(fresh())popup.update(choices());}
    throw error;
   }finally{pending=false;if(version===epoch&&write===controller)write=null;}
  }})):[{label:'No projects yet. Use + beside the Journal filter.',disabled:true}];
  const ok=await load({quiet:true});if(!fresh())return;
  popup.update(ok?choices():[{label:'Could not load projects. Close and reopen to retry.',disabled:true}]);if(focus)popup.focus();
 }
 // A task card is ephemeral; preserve only this authorized same-task interaction.
 function preserveAssignment(){
  const interaction=assignmentMenu;if(!interaction?.popup.open)return;
  const restoreFocus=document.activeElement===interaction.trigger,hadFocus=interaction.popup.focused;
  return tasks=>{
   if(assignmentMenu!==interaction)return;
   const task=tasks.find(t=>t.id===interaction.taskID);
   const card=[...document.querySelectorAll('#taskList .task')].find(n=>n.dataset.taskId===interaction.taskID),trigger=card?.querySelector('.change-project');
   if(!task||!trigger||trigger.disabled||task.can_change_project===false||task.content_access===false){interaction.popup.close(false);if(hadFocus)select.closest('.floating-field')?.querySelector('.select-trigger')?.focus({preventScroll:true});return;}
   interaction.rebind(task,trigger,restoreFocus);
  };
 }
 select.addEventListener('change',()=>{selected=select.value;render();changed();});
 createButton.onclick=()=>editor();
 manageButton.onclick=()=>{const project=current();if(!project)return;ControlPickers.menu(manageButton,{label:'Project actions',items:[{label:'Rename',onSelect:()=>editor(current())},{label:'Delete',onSelect:()=>remove(current())}]});};
 window.addEventListener('gateway-key-change',()=>{
  epoch++;reading=null;assignmentMenu?.popup.close(false);ControlPickers.dismiss(manageButton);write?.abort();write=null;if(dialog){dialog.close();dialog.remove();dialog=null;}projects=[];canManage=true;company=false;selected='';if(hasConnectedKey)remember();if(window.GenerationKey?.value())hasConnectedKey=true;feedback();render();load();
 });
 window.GatewayProjects={load,assign,preserveAssignment,filter:()=>selected,selected:()=>selected===NO_PROJECT?'':selected,matches:task=>!selected||(selected===NO_PROJECT?!task.project_id:task.project_id===selected)};
})();
