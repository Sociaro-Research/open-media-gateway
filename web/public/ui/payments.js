(() => {
 'use strict';
 const el=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
 const money=cents=>formatUSD(cents/100);
 const button=(text,fn)=>{const b=el('button',text,'secondary');b.type='button';b.onclick=fn;return b;};
 const field=(form,label,type='text')=>{const l=el('label',label),i=el('input');i.type=type;l.append(i);form.append(l);return i;};

 const events='checkout.session.completed, checkout.session.async_payment_succeeded, checkout.session.async_payment_failed, checkout.session.expired, payment_intent.succeeded, charge.updated, charge.refunded, charge.dispute.created, charge.dispute.updated, charge.dispute.closed, charge.dispute.funds_withdrawn, charge.dispute.funds_reinstated, refund.created, refund.updated, refund.failed';
 window.PaymentProviderSettings={async mount(parent,api,current){
  await window.VATSettingsUI.mount(parent,api,current);if(!current())return;
  const section=el('section',undefined,'payment-provider-settings'),head=el('div',undefined,'form-heading console-section-heading'),list=el('div',undefined,'payment-provider-list'),status=el('p');status.role='status';head.append(el('h2','Payment providers'));section.append(head,status,list);parent.append(section);
  let available=[],providers=[],draft=null;
  const add=button('Add provider',()=>{if(draft)return;draft=editor(null);list.prepend(draft);add.disabled=true;draft.querySelector('input')?.focus()});head.append(add);add.disabled=true;
  function editor(initial,kind='stripe'){
   if((initial?.kind||kind)==='stablecoin')return StablecoinUI.providerEditor(initial,{api,current,providers,onCancel:()=>{draft?.remove();draft=null;add.disabled=false},onSaved:(next,card)=>{const index=providers.findIndex(p=>p.id===next.id);if(index<0)providers.push(next);else providers[index]=next;available=available.filter(p=>p.id!=='stablecoin');const replacement=editor(next);card.replaceWith(replacement);if(draft===card){draft=null;add.disabled=false}FormUI.saved(replacement.querySelector('form'));FormUI.refresh()}});
   let data=initial,busy=false;
   const card=el('section',undefined,'card provider-settings-card payment-provider-card'),header=el('div',undefined,'provider-detail-header form-heading'),title=el('div',undefined,'provider-title-line');
   title.append(el('h3',data?.name||'New payment provider'));if(data)title.append(el('span',data.live?'Production':'Test','provider-status'));header.append(title);card.append(header);
   const enabledLabel=el('label',undefined,'master-switch'),enabled=el('input');enabled.type='checkbox';enabled.checked=!!data?.enabled;enabled.setAttribute('aria-label',(data?.name||'New payment provider')+' enabled');enabledLabel.append(enabled);header.append(enabledLabel);
   const form=el('form',undefined,'provider-settings-form payment-provider-editor'),msg=el('p');msg.role='status';form.autocomplete='off';form.append(header);card.append(form);
   const stripe=!data||data.kind==='stripe';let provider,name,key,hook,mode,save,validate,stripeInvoices;
   const ready=()=>!!data&&(data.enabled||(stripe&&data.credentials_configured&&data.webhook_configured));
   function availability(){if(validate){const changed=key.dataset.savedCredential!=='true';validate.disabled=busy||changed;validate.title=changed?'Save the API key before validating it.':'Validate the saved API key and payment availability.';}enabled.disabled=busy||!ready();enabled.title=!data?'Save the provider and configure its webhook before enabling payments.':!ready()?stripe?'Save API and webhook credentials before enabling payments.':'A disabled external integration cannot be re-enabled here.':'';}
   function lock(value){busy=value;for(const c of card.querySelectorAll('input,select,button')){if(value){c.dataset.wasDisabled=String(c.disabled);c.disabled=true}else{c.disabled=c.dataset.wasDisabled==='true';delete c.dataset.wasDisabled}}availability();if(save)FormUI.update(form)}
   enabled.onchange=async()=>{const next=enabled.checked;lock(true);FormUI.message(msg,'Saving…');try{
    const updated=await api('/v1/system/payment-providers/'+data.id+(next?'':'/disable'),{method:next?'PUT':'POST',body:JSON.stringify(next?{kind:data.kind,name:data.name,fee_mode:data.fee_mode,enabled:true,version:data.version}:{version:data.version})});
    if(!current())return;data=updated;enabled.checked=data.enabled;FormUI.message(msg,'');
   }catch(e){if(current()){enabled.checked=data.enabled;FormUI.message(msg,e.message,'error')}}finally{lock(false)}};
   if(stripe){
    const fields=el('div',undefined,'provider-form-grid');form.append(fields);
    if(!data){const label=el('label','Provider');provider=el('select');for(const p of available)provider.append(new Option(p.name,p.id));label.append(provider);fields.append(label);provider.onchange=()=>{if(provider.value==='stablecoin'){const replacement=editor(null,'stablecoin');card.replaceWith(replacement);draft=replacement;FormUI.refresh()}}}
    name=field(fields,'Display name');name.required=true;name.maxLength=100;name.value=data?.name||'Stripe';
    const feeLabel=el('label','Processing fee paid by');mode=el('select');mode.append(new Option('Service','service'),new Option('Organization','organization'));mode.value=data?.fee_mode||'service';feeLabel.append(mode);fields.append(feeLabel);
    key=field(fields,'Secret API key');key.className='secret-input';key.autocomplete='off';key.spellcheck=false;key.maxLength=512;key.required=!data?.credentials_configured;FormUI.credentials.render(key,!!data?.credentials_configured);
    if(data){
     const row=el('div',undefined,'field-action-row control-row'),label=key.parentElement;label.before(row);row.append(label);
     validate=button('Validate',async()=>{if(busy||validate.disabled)return;lock(true);FormUI.message(msg,'Validating…');try{const check=await api('/v1/system/payment-providers/'+data.id+'/check',{method:'POST'});if(current())FormUI.message(msg,check.charges_enabled?'API access verified.':'API access verified; payments are disabled in Stripe.')}catch(e){if(current())FormUI.message(msg,e.message,'error')}finally{lock(false)}});row.append(validate);key.addEventListener('input',availability);
    }

    const invoiceLabel=el('label','Use Stripe Invoices');stripeInvoices=el('input');stripeInvoices.type='checkbox';stripeInvoices.checked=data?.stripe_invoices!==false;invoiceLabel.append(stripeInvoices);form.append(invoiceLabel,el('p','When off, Gateway creates invoices using the seller company, invoice logo and VAT rules above. Stripe only processes the card payment. Changes apply to new payments.','field-explanation'));
    hook=field(fields,'Webhook signing secret');hook.className='secret-input';hook.autocomplete='off';hook.spellcheck=false;hook.maxLength=512;FormUI.credentials.render(hook,!!data?.webhook_configured);
    if(!data?.webhook_configured)form.append(el('p',data?'Add this webhook endpoint in Stripe and save its signing secret to enable payments.':'Save the API key to obtain the webhook endpoint.','field-explanation'));
    if(data){const url=field(form,'Webhook endpoint');url.readOnly=true;url.value=data.webhook_url;const details=el('details'),summary=el('summary','Stripe webhook events');details.append(summary,el('p',events));form.append(details)}
   }
   if(data){const facts=el('div',undefined,'provider-facts payment-provider-facts');for(const [label,value]of [['Account',data.account_id||'External integration'],['Last verified webhook',data.last_webhook_at?new Date(data.last_webhook_at).toLocaleString():'Not received']]){const fact=el('div');fact.append(el('span',label),el('strong',value));facts.append(fact)}form.append(facts)}
   const actions=el('div',undefined,'form-heading-actions');header.append(actions);form.append(msg);
   if(stripe){save=el('button','Save','primary compact');save.type='submit';
    if(!data)actions.append(button('Cancel',()=>{card.remove();draft=null;add.disabled=false}));
    actions.append(save,enabledLabel);
    FormUI.watch(form,save,{read:()=>JSON.stringify({provider:provider?.value,name:name.value,fee_mode:mode.value,secret_key:key.value,webhook_secret:hook.value,stripe_invoices:stripeInvoices.checked}),valid:()=>!busy&&!!name.value.trim()});
    form.onsubmit=async e=>{e.preventDefault();if(busy)return;const replacement=FormUI.credentials.replacement(key),signing=FormUI.credentials.replacement(hook);lock(true);FormUI.message(msg,'');
     try{const next=await api('/v1/system/payment-providers'+(data?'/'+data.id:''),{method:data?'PUT':'POST',body:JSON.stringify({kind:data?.kind||provider.value,name:name.value,enabled:!!data?.enabled,fee_mode:mode.value,stripe_invoices:stripeInvoices.checked,version:data?.version||0,...(replacement?{secret_key:replacement}:{}),...(signing?{webhook_secret:signing}:{})})});if(!current())return;const index=providers.findIndex(p=>p.id===next.id);if(index<0)providers.push(next);else providers[index]=next;const replacementCard=editor(next);card.replaceWith(replacementCard);if(draft===card){draft=null;add.disabled=false}FormUI.saved(replacementCard.querySelector('form'));FormUI.refresh();
     }catch(error){if(current())FormUI.message(msg,error.status===409?'Settings changed or this account is already connected. Reload Settings before saving.':error.message,'error')}finally{lock(false)}
    };
   }
   if(!stripe)actions.append(enabledLabel);availability();return card;
  }
  try{const data=await api('/v1/system/payment-providers');if(!current())return;providers=data.data||[];available=(data.available_providers||[]).filter(p=>p.id!=='stablecoin'||!providers.some(v=>v.kind==='stablecoin'));for(const provider of providers)list.append(editor(provider));add.disabled=!available.length;FormUI.refresh()}catch(e){if(current())FormUI.message(status,e.message,'error')}
 }};
 let returned=null;
 const query=new URLSearchParams(location.search),validID=id=>/^[0-9a-f]{8}-[0-9a-f-]{27}$/i.test(id||'');
 if(validID(query.get('payment'))&&validID(query.get('organization'))){returned={id:query.get('payment'),org:query.get('organization'),cancelled:query.get('cancelled')==='1',invoice:query.get('pay_invoice')==='1'};if(returned.invoice)sessionStorage.setItem('gateway-invoice-payment',JSON.stringify({...returned,opened:Date.now()}));history.replaceState(null,'',location.pathname);}
 if(!returned){try{const saved=JSON.parse(sessionStorage.getItem('gateway-invoice-payment'));if(saved?.invoice&&validID(saved.id)&&validID(saved.org)&&Date.now()-saved.opened<86400000)returned=saved}catch{}}
 const settledMoney=(usd,settlement,field)=>{
  if(!settlement||settlement.currency==='usd')return money(usd);
  const code=settlement.currency.toUpperCase(),digits=settlement.decimal_places;
  const value=settlement[field]/10**digits;
  const actual=new Intl.NumberFormat('en-US',{style:'currency',currency:code,currencyDisplay:code==='EUR'?'symbol':'code',minimumFractionDigits:digits,maximumFractionDigits:digits}).format(value);
  return money(usd)+' ('+actual+')';
 };
 window.BalanceHistory={async mount(parent,api,org,current,onChanged,manage){
  const section=el('section',undefined,'balance-history'),status=el('p'),rows=el('div'),paging=el('div',undefined,'docs-actions');status.role='status';section.append(el('h2','Balance history'),status,rows,paging);parent.append(section);
  const base='/v1/organizations/'+org.id;let offset=0;
  async function load(){
   try{const result=await api(base+'/balance-history?offset='+offset);if(!current())return;rows.replaceChildren();paging.replaceChildren();FormUI.message(status,'');
    if(!result.data.length){rows.append(el('p','No balance activity yet.'));return}
    const wrap=el('div',undefined,'system-table-wrap'),table=el('table',undefined,'docs-table'),head=el('thead'),h=el('tr'),body=el('tbody');
    for(const text of ['Date','Description','Payment','Fee','Balance change','Status','']){const th=el('th',text);th.scope='col';h.append(th)}head.append(h);
    let converted=false;
    for(const entry of result.data){
     const p=entry.payment,initial=p&&entry.kind!=='adjustment',row=el('tr');
     const description=p?(entry.kind==='adjustment'?'Payment adjustment · ':'Top-up · ')+entry.provider:({payment:'Manual top-up',promo:'Promotional credit',adjustment:'Balance adjustment'}[entry.kind]||'Balance adjustment');
     const desc=el('td',description);if(entry.reason&&!p)desc.append(el('small',entry.reason,'balance-detail'));
     const charged=p?.billing?.total_cents??p?.amount_cents;
     const payment=el('td',initial?(p.stablecoin?p.stablecoin.amount+' '+p.stablecoin.token:settledMoney(charged,p.settlement,'amount_minor'))+(p.tax_mode==='exclusive'&&!p.billing?' + tax':''):'—');
     if(initial&&p.billing)payment.append(el('small','Top-up '+money(p.billing.subtotal_cents)+' · Tax '+money(p.billing.tax_cents),'balance-detail'));
     if(initial&&p.billing?.business_name)desc.append(el('small',p.billing.business_name,'balance-detail'));
     const fee=el('td',initial?(p.stablecoin?'$'+StablecoinUI.micros(p.stablecoin.fee_micros):p.settled?settledMoney(p.fee_cents,p.settlement,'fee_minor'):'Pending'):'—');
     if(initial)fee.append(el('small','Paid by '+((p.stablecoin||p.fee_mode==='organization')?org.name:(document.querySelector('.brand')?.getAttribute('aria-label')||'Service')),'balance-detail'));
     if(initial&&p.settlement&&p.settlement.currency!=='usd')converted=true;
     const change=el('td',(entry.amount_usd>0?'+':'')+formatUSD(entry.amount_usd));
     const state=!p?'Completed':entry.kind==='adjustment'?'Applied':p.settled?'Completed':({pending:'Awaiting payment',expired:'Expired',failed:'Failed'}[p.status]||p.status.replaceAll('_',' '));
     row.append(el('td',new Date(entry.created_at).toLocaleString()),desc,payment,fee,change,el('td',(p&&!p.live?'Test · ':'')+state));
     const actions=el('td',undefined,'table-row-actions'),actionList=el('div',undefined,'balance-actions');actions.append(actionList);
     if(initial&&p.receipt_url){const a=el('a','Receipt');a.href=p.receipt_url;a.target='_blank';a.rel='noopener noreferrer';actionList.append(a)}
     if(initial&&p.invoice_mode==='local'&&manage&&InvoiceUI.available(p)){actionList.append(InvoiceUI.link(p,base,{api,status,current}))}else if(initial&&p.invoice_mode!=='local'&&p.invoice?.pdf_url){const a=el('a','Invoice');a.href=p.invoice.pdf_url;a.target='_blank';a.rel='noopener noreferrer';a.title=p.invoice.number?'Invoice '+p.invoice.number:'Download invoice PDF';actionList.append(a)}
     if(initial&&p.invoice_mode!=='local'&&p.settled&&!p.stablecoin&&p.tax_mode==='exclusive'&&!p.invoice?.pdf_url){actionList.append(el('small','Invoice is being prepared','balance-detail'));if(manage)actionList.append(button('Refresh invoice',async()=>{try{await api(base+'/payments/'+p.id+'/refresh',{method:'POST'});if(current())await onChanged()}catch(e){if(current())FormUI.message(status,e.message,'error')}}))}
     row.append(actions);body.append(row);
    }
    table.append(head,body);wrap.append(table);rows.append(wrap);
    if(converted)rows.append(el('p','Amounts in parentheses show Stripe’s settlement currency, which may differ from your card’s currency.','field-explanation'));
    if(offset||result.has_more){const prev=button('Previous',()=>{offset-=50;load()}),next=button('Next',()=>{offset+=50;load()});prev.disabled=!offset;next.disabled=!result.has_more;paging.append(prev,next)}
   }catch(e){if(current())FormUI.message(status,e.message,'error')}
  }
  await load();
 }};
 window.PaymentTopups={returned:()=>returned,async mount(parent,api,org,current,onChanged){
  const section=el('section',undefined,'organization-topups'),status=el('p');status.role='status';section.append(el('h2','Top up balance'),status);parent.append(section);
  const base='/v1/organizations/'+org.id;
  try{const config=await api(base+'/payment-methods');if(!current())return;const methods=config.data||[];
   let form;
    const viewer=el('div'),requestList=el('div');
    let selectedID=null,pollTimer,shownSignature='',requests;
    const show=async p=>{selectedID=p.id;if(form)form.hidden=true;viewer.hidden=false;if(shownSignature!==JSON.stringify(p)){shownSignature=JSON.stringify(p);StablecoinUI.card(viewer,p,{api,base,current,onBack:()=>{selectedID=null;shownSignature='';viewer.replaceChildren();if(form)form.hidden=false;requests?.reload();FormUI.refresh()},onSettled:onChanged})}clearTimeout(pollTimer);if(!p.settled&&!['cancelled','expired'].includes(p.status))pollTimer=setTimeout(async()=>{if(!current()||!selectedID||!viewer.isConnected)return;if(document.hidden){show(p);return}try{const next=await api(base+'/payments/'+selectedID);if(current()&&selectedID===next.id){if(next.settled&&!p.settled){await onChanged();return}show(next)}}catch{if(current())show(p)}},15000)};


   if(methods.length){form=el('form',undefined,'keys-form topup-form');const choiceLabel=el('label','Payment method'),choice=el('select');for(const p of methods)choice.append(new Option((p.kind==='stablecoin'?'Stablecoins':methods.filter(m=>m.kind==='stripe').length>1?'Bank card · '+p.name:'Bank card')+(p.live?'':' (test)'),p.id));choiceLabel.append(choice);form.append(choiceLabel);const amount=field(form,'Top-up amount (USD, before tax)','number');amount.min=String(config.min_cents/100);amount.max=String(config.max_cents/100);amount.step='0.01';amount.required=true;amount.value='50';const explanation=el('p',undefined,'field-explanation'),submit=el('button','Continue to Stripe','primary compact');submit.type='submit';const extras=StablecoinUI.extendTopup(form,choice,methods,org);let attemptKey=null,attemptPayload='';const sync=()=>{const method=methods.find(p=>p.id===choice.value);extras.sync();submit.textContent=method.kind==='stablecoin'?'Create payment':'Continue to Stripe';explanation.textContent=method.kind==='stablecoin'?'Tax and a payment fee of less than $0.01 are added before the exact transfer amount is shown.':(method.fee_mode==='organization'?'Applicable taxes and Stripe fees are deducted from your payment before your balance is credited.':'Applicable taxes are deducted from your payment before your balance is credited.')+(method.live?'':' This is a test payment. It will not add spendable funds.')};choice.onchange=sync;sync();form.append(explanation,submit);section.append(form);

    let busy=false;form.onsubmit=async e=>{e.preventDefault();if(busy)return;busy=true;submit.disabled=true;
     try{const method=methods.find(p=>p.id===choice.value),payload=JSON.stringify({provider_id:choice.value,amount_usd:Number(amount.value),fee_mode:method.fee_mode,...(method.kind==='stablecoin'?extras.payload():method.stripe_invoices===false?{billing:extras.payload().billing}:{})});if((method.kind!=='stablecoin'&&method.stripe_invoices!==false)||payload!==attemptPayload){attemptKey=crypto.randomUUID();attemptPayload=payload}const p=await api(base+'/payments',{method:'POST',headers:{'Idempotency-Key':attemptKey},body:payload});if(!current())return;if(p.stablecoin){attemptPayload='';show(p);requests?.reload()}else if(p.checkout_url){location.assign(p.checkout_url)}else{attemptPayload='';FormUI.message(status,'This checkout is closed. You can start a new payment.');await onChanged()}}catch(e){FormUI.message(status,e.message,'error')}finally{busy=false;submit.disabled=false}
    };
   }else{section.append(el('p','No payment provider is enabled. Contact an administrator.','field-explanation'));}
   section.append(viewer,requestList);requests=await StablecoinUI.requests(requestList,{api,base,current,onView:show});
   if(returned?.org===org.id){const ret=returned;returned=null;sessionStorage.removeItem('gateway-topup-attempt-'+org.id);if(ret.invoice){sessionStorage.removeItem('gateway-invoice-payment');FormUI.message(status,'Opening Stripe payment…');try{const p=await api(base+'/payments/'+ret.id+'/checkout',{method:'POST'});if(!current())return;if(p.settled){FormUI.message(status,'This invoice has already been paid.');await onChanged()}else if(p.checkout_url){location.assign(p.checkout_url)}else{FormUI.message(status,'This payment cannot be opened right now. Try again later.','error')}}catch(e){if(current())FormUI.message(status,e.message,'error')}return;}FormUI.message(status,ret.cancelled?'Checkout was closed. No credit is added until Stripe confirms payment.':'Checking your payment with Stripe…');let attempts=0;async function poll(){if(!current())return;try{const p=await api(base+'/payments/'+ret.id+'/refresh',{method:'POST'});if(!current())return;if(p.settled){await onChanged();return}if(InvoiceUI.expired(p)){FormUI.message(status,'This payment request has expired. Create a new payment.');return}FormUI.message(status,'Payment is awaiting confirmation. You can leave this page; confirmation is processed automatically.')}catch{if(current())FormUI.message(status,'Confirmation is taking longer. Your payment will be checked automatically.')}if(current()&&++attempts<10&&!ret.cancelled)setTimeout(poll,6000)}await poll();}
  }catch(e){if(current())FormUI.message(status,e.message,'error')}
 }};
})();
