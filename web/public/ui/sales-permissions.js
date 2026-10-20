(() => {
 'use strict';
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const labels={pricing:'Change company markups',models:'Manage available models',topup:'Top up client balance',bonuses:'Record team bonus payouts'};
 let dialog;
 async function open(api,employee='',after=()=>{}){
  dialog?.close();const d=el('dialog',null,'keys-revoke-dialog system-dialog sales-dialog'),status=el('p'),content=el('div'),{heading:title,close}=FormUI.dialogHeading(d,'Sales permissions',()=>d.close());
  title.id='sales-permissions-title';d.setAttribute('aria-labelledby',title.id);status.setAttribute('role','status');d.append(status,content);document.body.append(d);dialog=d;
  let state,busy=false;
  d.onclose=()=>{d.remove();if(dialog===d)dialog=null;after();};d.addEventListener('cancel',e=>{if(busy)e.preventDefault();});d.showModal();close.focus();
  async function load(){
   FormUI.message(status,'Loading…');
   try{state=await api('/v1/sales/permissions');if(!d.isConnected)return;render();FormUI.message(status,'');return true;}
   catch(e){if(!d.isConnected)return;content.replaceChildren();FormUI.message(status,e.message,'error');const retry=el('button','Retry','secondary');retry.onclick=load;content.append(retry);return false;}
  }
  function render(){
   content.replaceChildren();if(!state.members.length){content.append(el('p','No sales employees.'));return;}
   const label=el('label','Employee','organization-field'),select=el('select');select.setAttribute('aria-label','Employee');
   for(const m of state.members){select.append(UserUI.option(m,`${m.name||m.email} · ${m.email}`));}select.value=state.members.some(m=>m.id===employee)?employee:state.members[0].id;employee=select.value;label.append(select);content.append(label);
   select.onchange=()=>{employee=select.value;render();};
   content.append(el('p','Client permissions apply to own clients and team clients. Bonus payouts apply to subordinates only. Revoking or replacing a permission also revokes permissions delegated from it.','field-explanation'));
   for(const [permission,caption] of Object.entries(labels)){
    const grant=state.grants.find(g=>g.employee_id===employee&&g.permission===permission),own=state.grants.find(g=>g.employee_id===state.actor_id&&g.permission===permission),self=employee===state.actor_id;
    const canGrant=!self&&(state.administrator||!!own),canRevoke=!self;
    const f=el('form',null,'keys-form organization-form'),toggle=el('label',null,'system-toggle'),enabled=el('input'),text=el('span'),description=el('small');enabled.type='checkbox';enabled.name='enabled';enabled.checked=!!grant;enabled.disabled=!canGrant&&(!grant||!canRevoke);text.append(el('strong',caption));
    if(grant)description.append('Granted by ',UserUI.identity({id:grant.issuer_id,name:grant.issuer_name,picture:grant.issuer_picture}),` · ${new Date(grant.created_at).toLocaleString()}`);else description.textContent='Not granted';text.append(description);toggle.append(enabled,text);f.append(toggle);
    const save=el('button','Save','primary compact'),msg=el('p'),actions=el('div',null,'form-actions');save.type='submit';msg.setAttribute('role','status');actions.append(save,msg);f.append(actions);content.append(f);
    const update=()=>FormUI.update(f);enabled.onchange=update;
    FormUI.watch(f,save,{valid:()=>!self&&(enabled.checked?canGrant:canRevoke)});update();
    f.onsubmit=async e=>{e.preventDefault();if(save.hidden||save.disabled||busy||!f.reportValidity())return;busy=true;close.disabled=true;const snapshot=FormUI.snapshot(f);
     // Lock all controls during the write without changing the saved form values.
     const controls=[...content.querySelectorAll('input,select,button')],disabled=controls.map(n=>n.disabled);controls.forEach(n=>n.disabled=true);
     try{await api(`/v1/sales/permissions/${employee}/${permission}`,{method:'PUT',body:JSON.stringify({enabled:enabled.checked,version:state.version})});if(!d.isConnected)return;FormUI.saved(f,snapshot);if(await load()){FormUI.message(status,'Saved');close.focus();}}
     catch(error){if(d.isConnected){FormUI.message(msg,error.status===409?'Permissions changed. Close and reopen to review the latest permissions.':error.message,'error');if(error.status===401||error.status===403)content.replaceChildren();}}
     finally{busy=false;close.disabled=false;controls.forEach((n,i)=>n.disabled=disabled[i]);FormUI.update(f);FormUI.refresh();}
    };
   }
   FormUI.refresh();
  }
  await load();
 }
 window.SalesPermissions={open,close(){dialog?.close();}};
})();
