(() => {
 'use strict';
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,action)=>{const b=el('button',text,'secondary');b.type='button';b.onclick=action;return b;};
 const usd=n=>formatUSD(n);
 function field(form,label,type,value='',required=true){const wrap=el('label',label,'organization-field'),input=el('input');input.type=type;input.value=value;input.required=required;wrap.append(input);form.append(wrap);return input;}
 let dialog;
 async function settings(parent,api,current){
  const f=el('form',null,'keys-form sales-bonus-settings'),h=el('h2','Sales bonuses'),save=el('button','Save','primary compact'),status=el('p'),fields=el('div',null,'keys-fields');save.type='submit';status.setAttribute('role','status');f.append(h,fields);parent.append(f);
  const manager=field(fields,'Client manager share of profit (%)','number'),supervisor=field(fields,'Direct supervisor share of profit (%)','number');
  for(const input of [manager,supervisor]){input.min='0';input.max='100';input.step='0.01';input.disabled=true;}
  f.append(el('p','Applies when profit is first calculated. Already accrued bonuses keep their original rates.','field-explanation'));const actions=el('div',null,'form-actions');actions.append(save,status);f.append(actions);save.disabled=true;
  let version;
  try{const data=await api('/v1/sales/bonuses');if(!current())return;manager.value=data.settings.manager_percent;supervisor.value=data.settings.supervisor_percent;version=data.settings.version;manager.disabled=supervisor.disabled=save.disabled=false;FormUI.watch(f,save,{valid:()=>Number(manager.value)+Number(supervisor.value)<=100});FormUI.refresh();}
  catch(e){if(current())FormUI.message(status,e.message,'error');return;}
  f.onsubmit=async e=>{e.preventDefault();if(save.hidden||save.disabled||!f.reportValidity())return;const snapshot=FormUI.snapshot(f);save.disabled=manager.disabled=supervisor.disabled=true;try{const next=await api('/v1/system/sales/bonus-settings',{method:'PUT',body:JSON.stringify({manager_percent:Number(manager.value),supervisor_percent:Number(supervisor.value),version})});if(!current())return;version=next.version;FormUI.saved(f,snapshot);FormUI.message(status,'');}catch(error){if(current())FormUI.message(status,error.message,'error');}finally{manager.disabled=supervisor.disabled=save.disabled=false;FormUI.update(f);}};
  const validate=()=>{supervisor.setCustomValidity(Number(manager.value)+Number(supervisor.value)>100?'The two shares cannot exceed 100% together.':'');};manager.oninput=supervisor.oninput=validate;
 }
 async function open(api){
  dialog?.close();const d=el('dialog',null,'keys-revoke-dialog system-dialog sales-dialog'),status=el('p'),content=el('div'),{heading:h,close}=FormUI.dialogHeading(d,'Sales bonuses',()=>d.close());h.id='sales-bonuses-title';d.setAttribute('aria-labelledby',h.id);status.setAttribute('role','status');d.append(status,content);document.body.append(d);dialog=d;d.onclose=()=>{d.remove();if(dialog===d)dialog=null;};let state,selected='',offset=0,busy=false,serial=0;
  d.addEventListener('cancel',e=>{if(busy)e.preventDefault();});d.showModal();close.focus();
  async function load(){FormUI.message(status,'Loading…');try{const next=await api('/v1/sales/bonuses');if(!d.isConnected)return;state=next;if(!state.people.some(p=>p.id===selected))selected=state.people[0]?.id||'';await render();FormUI.message(status,'');return true;}catch(e){if(d.isConnected){content.replaceChildren(button('Retry',load));FormUI.message(status,e.message,'error');}}}
  async function render(){const token=++serial;content.replaceChildren();if(!selected){content.append(el('p','No sales employees or bonus history.'));return;}
   const label=el('label','Employee','organization-field'),select=el('select');select.setAttribute('aria-label','Employee');for(const p of state.people){select.append(UserUI.option(p,`${p.name||p.email}${p.id===state.actor_id?' · You':''}`));}select.value=selected;select.onchange=()=>{if(busy)return;selected=select.value;offset=0;render();};label.append(select);content.append(label);
   const person=state.people.find(p=>p.id===selected),summary=el('dl',null,'organization-summary');for(const [caption,value]of [['Accrued',person.accrued_usd],['Paid',person.paid_usd],['Balance',person.balance_usd]]){const item=el('div');item.append(el('dt',caption),el('dd',usd(value),value<0?'operation-error':''));summary.append(item);}content.append(summary);
   if(person.can_pay){const form=el('form',null,'keys-form organization-form'),save=el('button','Record payout','primary compact'),message=el('p'),actions=el('div',null,'form-actions');save.type='submit';message.setAttribute('role','status');form.append(el('h3','Record bonus payout'));content.append(form);
    const now=new Date(),today=`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;
    const amount=field(form,'Amount paid (USD)','number'),date=field(form,'Payment date','date',today),method=field(form,'Payment method','text'),reason=field(form,'Note','text','',false),operation=crypto.randomUUID();amount.min='0.000001';amount.step='0.000001';method.maxLength=200;reason.maxLength=1000;
    form.append(el('p','Record a payment you have already made. The bonus balance may become negative.','field-explanation'));actions.append(save,message);form.append(actions);FormUI.watch(form,save,{valid:()=>Number(amount.value)>0&&!!method.value.trim()});
    form.onsubmit=async e=>{e.preventDefault();if(save.hidden||save.disabled||busy||!form.reportValidity())return;busy=true;close.disabled=select.disabled=true;const target=selected,controls=[...form.elements];controls.forEach(n=>n.disabled=true);try{await api(`/v1/sales/bonuses/${target}/payouts`,{method:'POST',body:JSON.stringify({amount_usd:Number(amount.value),paid_at:date.value,method:method.value.trim(),reason:reason.value.trim(),external_id:operation})});if(!d.isConnected)return;offset=0;if(await load())FormUI.message(status,'Payout recorded');close.focus();}catch(error){if(d.isConnected){FormUI.message(message,error.message,'error');if(error.status===401||error.status===403)content.replaceChildren();}}finally{busy=false;close.disabled=select.disabled=false;controls.forEach(n=>n.disabled=false);FormUI.update(form);}};
   }
   const history=el('section');history.append(el('h3','Bonus history'));content.append(history);FormUI.refresh();
   try{const result=await api(`/v1/sales/bonuses/${selected}/entries?offset=${offset}`);if(token!==serial||!d.isConnected)return;
    if(!result.data.length)history.append(el('p','No bonus entries yet.'));
    for(const entry of result.data){const item=el('section',null,'keys-form'),heading=el('div',null,'form-heading');heading.append(el('h3',({accrual:'Bonus accrued',correction:'Bonus corrected',payout:'Bonus paid'})[entry.kind]||entry.kind),el('strong',usd(entry.amount_usd)));const detail=el('p',null,'field-explanation');if(entry.kind==='payout')detail.append(`${entry.paid_at} · ${entry.method} · `,UserUI.identity({id:entry.actor_id,name:entry.actor_name,picture:entry.actor_picture}));else detail.textContent=`${new Date(entry.created_at).toLocaleString()} · ${entry.beneficiary==='supervisor'?'Supervisor share':'Client manager share'} · Generation ${entry.task_id}`;item.append(heading,detail);if(entry.reason)item.append(el('p',entry.reason));history.append(item);}
    const actions=el('div',null,'form-actions'),prev=button('Previous',()=>{if(busy)return;offset=Math.max(0,offset-50);render();}),next=button('Next',()=>{if(busy)return;offset+=50;render();});prev.disabled=offset===0;next.disabled=!result.has_more;if(offset||result.has_more){actions.append(prev,next);history.append(actions);}
   }catch(e){if(token===serial&&d.isConnected)history.append(el('p',e.message,'operation-error'));}
  }
  await load();
 }
 window.SalesBonuses={open,settings,close(){dialog?.close();}};
})();
