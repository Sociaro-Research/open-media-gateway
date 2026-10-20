(() => {
  'use strict';
  window.EmailDeliverySettings = {async mount(parent, api, current) {
    const form=document.createElement('form');
    form.className='keys-form email-delivery-settings';
    form.innerHTML=`<h2>Email delivery</h2>
      <div class="form-actions"><p role="status"></p><button class="primary compact" type="submit">Save</button></div>
      <label>Email provider<select name="provider"><option value="">Not configured</option></select></label>
      <div class="email-delivery-fields" hidden>
        <label>Sender address<input name="sender" type="text" maxlength="320" autocomplete="off" required></label>
        <label>API key<input name="key" type="text" class="secret-input" maxlength="512" autocomplete="off" spellcheck="false" required></label>
        <p class="field-explanation">Use an address on a domain verified in Resend. Used for account emails and system notifications.</p>
      </div>`;
    parent.append(form);
    const provider=form.elements.provider,sender=form.elements.sender,key=form.elements.key,fields=form.querySelector('.email-delivery-fields'),save=form.querySelector('button'),status=form.querySelector('[role=status]');
    let data,busy=false;
    function sync(){
      const enabled=provider.value!=='';
      fields.hidden=!enabled;
      sender.disabled=key.disabled=!enabled;
      key.required=enabled&&!data?.key_configured;
      FormUI.refresh();
    }
    function show(next){
      data=next;provider.value=data.provider;sender.value=data.from||'';
      FormUI.credentials.render(key,data.key_configured);
      sync();
    }
    save.disabled=true;
    try {
      data=await api('/v1/system/email');if(!current())return;
      for(const choice of data.available_providers||[]){const option=document.createElement('option');option.value=choice.id;option.textContent=choice.name;provider.append(option);}
      show(data);save.disabled=false;FormUI.watch(form,save);
    } catch(error){if(current())FormUI.message(status,error.message,'error');return;}
    provider.addEventListener('change',sync);
    form.addEventListener('submit',async event=>{
      event.preventDefault();if(busy)return;busy=true;
      const input={provider:provider.value,from:sender.value,version:data.version},replacement=FormUI.credentials.replacement(key);if(provider.value&&replacement)input.api_key=replacement;
      for(const control of [provider,sender,key,save])control.disabled=true;
      FormUI.message(status,'');
      try {
        const next=await api('/v1/system/email',{method:'PUT',body:JSON.stringify(input)});if(!current())return;
        show(next);FormUI.saved(form);form.dispatchEvent(new CustomEvent("email-delivery-change",{bubbles:true,detail:{enabled:next.enabled}}));
      }catch(error){if(current())FormUI.message(status,error.status===409?'Another administrator changed these settings. Reopen Settings before saving.':error.message,'error');}
      finally{busy=false;provider.disabled=false;sync();save.disabled=false;FormUI.update(form);}
    });
  }};
})();
