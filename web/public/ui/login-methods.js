(() => {
 'use strict';
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,fn)=>{const b=el('button',text,'secondary');b.type='button';b.onclick=fn;return b;};
 const field=(parent,label)=>{const l=el('label',label),i=el('input');l.append(i);parent.append(l);return i;};
 window.LoginMethodSettings={async mount(parent,api,current){
  const section=el('section',null,'login-method-settings'),heading=el('div',null,'form-heading console-section-heading'),list=el('div',null,'payment-provider-list'),status=el('p');status.role='status';heading.append(el('h2','Login methods'));section.append(heading,status,list);parent.append(section);
  let records=[],available=[],delivery=false,draft=null;const warnings=new Map();
  const add=button('Add method',()=>{if(draft)return;const candidates=records.filter(p=>!p.connected);if(!candidates.length)return;draft=card(candidates[0],true);list.prepend(draft);updateAdd();FormUI.refresh()});heading.append(add);add.disabled=true;
  const updateAdd=()=>{add.disabled=!!draft;add.hidden=!records.some(p=>!p.connected)};
  function card(initial,creating=false){
   let data={...initial},busy=false;
   const box=el('section',null,'card provider-settings-card login-provider-card');box.dataset.provider=data.provider;
   const header=el('div',null,'provider-detail-header'),title=el('div',null,'provider-title-line');title.append(el('h3',creating?'New login method':data.name));header.append(title);box.append(header);
   const label=el('label',null,'master-switch'),enabled=el('input');enabled.type='checkbox';enabled.checked=creating?false:data.enabled;enabled.setAttribute('aria-label',data.name+' enabled');label.append(enabled);header.append(label);
   const form=el('form',null,'provider-settings-form login-provider-editor'),message=el('p'),warning=el('p',null,'settings-warning');message.role='status';warning.role='status';box.append(form);form.autocomplete='off';
   const warn=()=>{warning.textContent=data.provider==='email'&&data.enabled&&!delivery?'Email login is enabled, but it will not be shown to users until Email delivery is configured. The enabled preference is preserved.':'';warning.hidden=!warning.textContent};warnings.set(box,warn);
   let client,secret;
   if(creating){const l=el('label','Provider'),select=el('select');for(const p of available.filter(p=>records.some(r=>r.provider===p.id&&!r.connected)))select.append(new Option(p.name,p.id));select.value=data.provider;l.append(select);form.append(l);select.onchange=()=>{const next=card(records.find(r=>r.provider===select.value),true);warnings.delete(box);box.replaceWith(next);draft=next;FormUI.refresh()}}
   if(data.provider==='google'){
    const fields=el('div',null,'provider-form-grid');form.append(fields);client=field(fields,'Client ID');client.required=true;client.maxLength=512;client.value=data.client_id||'';
    secret=field(fields,'Client Secret');secret.maxLength=512;secret.required=!data.secret_configured;secret.autocomplete='off';secret.spellcheck=false;FormUI.credentials.render(secret,!!data.secret_configured);
    const callback=field(form,'Redirect URI');callback.readOnly=true;callback.value=data.redirect_url||'';
   }
   form.append(warning);const actions=el('div',null,'provider-actions control-row'),save=el('button','Save','primary compact');save.type='submit';
   if(data.provider==='google'||creating)actions.append(save);
   if(creating)actions.append(button('Cancel',()=>{warnings.delete(box);box.remove();draft=null;updateAdd()}));if(actions.children.length)form.append(actions);form.append(message);
   const ready=()=>!creating&&data.configured;
   function lock(value){busy=value;for(const c of box.querySelectorAll('input,select,button')){if(value){c.dataset.wasDisabled=String(c.disabled);c.disabled=true}else{c.disabled=c.dataset.wasDisabled==='true';delete c.dataset.wasDisabled}}enabled.disabled=busy||!ready();if(data.provider==='google'||creating)FormUI.update(form)}
   enabled.disabled=!ready();enabled.title=creating?'Save this method before enabling it.':'';
   warn();
   enabled.onchange=async()=>{const next=enabled.checked;lock(true);FormUI.message(message,'');try{const saved=await api('/v1/system/login-providers/'+data.provider,{method:'PUT',body:JSON.stringify({enabled:next,version:data.version})});if(!current())return;data=saved;records=records.map(r=>r.provider===data.provider?data:r);enabled.checked=data.enabled;warn()}catch(e){if(current()){enabled.checked=data.enabled;FormUI.message(message,e.status===409?'Settings changed. Reload before saving.':e.message,'error')}}finally{lock(false)}};
   if(data.provider==='google'||creating){FormUI.watch(form,save,{valid:()=>!busy&&(!client||!!client.value.trim())});form.onsubmit=async event=>{event.preventDefault();if(busy)return;const replacement=secret?FormUI.credentials.replacement(secret):'';const input={enabled:creating?false:data.enabled,version:data.version,...(client?{client_id:client.value.trim()}:{}),...(replacement?{client_secret:replacement}:{})};lock(true);FormUI.message(message,'');try{
    const next=await api('/v1/system/login-providers/'+data.provider,{method:'PUT',body:JSON.stringify(input)});if(!current())return;records=records.map(r=>r.provider===next.provider?next:r);warnings.delete(box);const nextCard=card(next);box.replaceWith(nextCard);if(draft===box)draft=null;updateAdd();FormUI.refresh();FormUI.saved(nextCard.querySelector('form'));
   }catch(e){if(current())FormUI.message(message,e.status===409?'Settings changed. Reload before saving.':e.message,'error')}finally{lock(false)}}}
   return box;
  }
  parent.parentElement.addEventListener('email-delivery-change',event=>{if(!current())return;delivery=event.detail.enabled;for(const warn of warnings.values())warn()});
  try{const data=await api('/v1/system/login-providers');if(!current())return;records=data.data||[];available=data.available_providers||[];delivery=!!data.email_delivery_configured;for(const p of records.filter(p=>p.connected).sort((a,b)=>a.provider==='google'?-1:b.provider==='google'?1:0))list.append(card(p));updateAdd();FormUI.refresh()}catch(e){if(current())FormUI.message(status,e.message,'error')}
 }};
})();
