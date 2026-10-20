(() => {
 'use strict';
 window.PaymentNotificationSettings={async mount(parent,api,current){
  const form=document.createElement('form');form.className='keys-form payment-notification-settings';
  form.innerHTML=`<h2>Payment notifications</h2>
   <p class="field-explanation">Administrator emails, separated by commas or new lines.</p>
   <label>Critical payment errors<textarea name="critical" rows="3" maxlength="5200" autocomplete="off" spellcheck="false" placeholder="admin@example.com"></textarea></label>
   <label>Successful balance top-ups<textarea name="success" rows="3" maxlength="5200" autocomplete="off" spellcheck="false" placeholder="admin@example.com"></textarea></label>
   <p class="payment-email-state" role="status"></p><p class="payment-delivery-state"></p>
   <button class="primary compact" type="submit">Save</button><p class="payment-notification-message" role="status"></p>`;
  parent.append(form);
  const critical=form.elements.critical,success=form.elements.success,save=form.querySelector('button'),status=form.querySelector('.payment-notification-message');let data,busy=false;
  function show(next){data=next;critical.value=next.critical_emails.join('\n');success.value=next.success_emails.join('\n');
   FormUI.message(form.querySelector('.payment-email-state'),next.email_enabled?'':'Email delivery is disabled. Enable it in Email delivery to send notifications.',next.email_enabled?'':'error');
   const d=next.delivery||{};form.querySelector('.payment-delivery-state').textContent=d.pending||d.retrying?`Queued: ${d.pending||0} · Retrying: ${d.retrying||0}`:'';
  }
  save.disabled=true;
  try{const next=await api('/v1/system/payment-notifications');if(!current())return;show(next);save.disabled=false;FormUI.watch(form,save);}catch(e){if(current())FormUI.message(status,e.message,'error');return}
  const emails=value=>[...new Set(value.split(/[,;\n]+/).map(x=>x.trim().toLowerCase()).filter(Boolean))];
  form.onsubmit=async e=>{e.preventDefault();if(busy)return;busy=true;for(const c of [critical,success,save])c.disabled=true;FormUI.message(status,'');
   try{const next=await api('/v1/system/payment-notifications',{method:'PUT',body:JSON.stringify({critical_emails:emails(critical.value),success_emails:emails(success.value),version:data.version})});if(!current())return;show(next);FormUI.saved(form);}
   catch(e){if(current())FormUI.message(status,e.status===409?'Another administrator changed these lists. Reopen Settings before saving.':e.message,'error');}
   finally{busy=false;for(const c of [critical,success,save])c.disabled=false;FormUI.update(form);}
  };
 }};
})();
