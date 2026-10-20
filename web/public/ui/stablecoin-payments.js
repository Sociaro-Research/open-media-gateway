(() => {
 'use strict';
 const el=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
 const button=(text,fn)=>{const b=el('button',text,'secondary');b.type='button';b.onclick=fn;return b;};
 const field=(parent,label,value='',required=true)=>{const l=el('label',label),i=el('input');i.value=value;i.required=required;i.maxLength=300;l.append(i);parent.append(l);return i;};
 const select=(parent,label,options,value)=>{const l=el('label',label),s=el('select');for(const [v,t]of options)s.append(new Option(t,v));if(value!==undefined)s.value=value;l.append(s);parent.append(l);return s;};
 const networks=[['ethereum','Ethereum'],['bsc','BNB Smart Chain'],['polygon','Polygon']];
 const networkName=id=>networks.find(n=>n[0]===id)?.[1]||id;
 const states={pending:'Awaiting payment',confirming:'Confirming payment',paid:'Paid',cancelled:'Cancelled',expired:'Expired'};
 const micros=v=>(BigInt(v)/1000000n).toString()+'.'+(BigInt(v)%1000000n).toString().padStart(6,'0');
 window.StablecoinUI={
  providerEditor(initial,{api,current,providers,onSaved,onCancel}){
   let data=initial,busy=false;
   const card=el('section',undefined,'card provider-settings-card payment-provider-card stablecoin-provider-card'),header=el('div',undefined,'provider-detail-header form-heading'),title=el('div',undefined,'provider-title-line');title.append(el('h3',data?.name||'Stablecoins'),el('span','Production','provider-status'));header.append(title);
   const enabledLabel=el('label',undefined,'master-switch'),enabled=el('input');enabled.type='checkbox';enabled.checked=!!data?.enabled;enabled.disabled=!data;enabled.setAttribute('aria-label','Stablecoins enabled');enabledLabel.append(enabled);header.append(enabledLabel);card.append(header);
   const form=el('form',undefined,'provider-settings-form payment-provider-editor'),msg=el('p');msg.role='status';form.append(header);card.append(form);
   const name=field(form,'Display name',data?.name||'Stablecoins');name.maxLength=100;
   const hint=el('p','We recommend using Safe smart accounts to receive payments. Create and activate your Safe on each network you plan to accept payments on. ','field-explanation'),link=el('a','Create a Safe');link.href='https://app.safe.global/';link.target='_blank';link.rel='noopener noreferrer';hint.append(link);form.append(hint);
   const wrap=el('div',undefined,'system-table-wrap'),table=el('table',undefined,'docs-table stablecoin-wallet-table'),thead=el('thead'),tr=el('tr'),tbody=el('tbody');for(const t of ['Network','Receiving address','Tokens',''])tr.append(el('th',t));thead.append(tr);table.append(thead,tbody);wrap.append(table);form.append(wrap);
   const records=[];
   const validateNetworks=()=>{for(const r of records)r.network.setCustomValidity(records.filter(v=>v.network.value===r.network.value).length>1?'Choose each network only once.':'')};
   const add=button('Add network',()=>addWallet()),networkActions=el('div',undefined,'form-actions');networkActions.append(add);form.append(networkActions);
   function addWallet(value){
    const used=records.map(r=>r.network.value),available=networks.filter(n=>!used.includes(n[0]));if(!value&&!available.length)return;
    const row=el('tr'),cells=Array.from({length:4},()=>el('td'));row.append(...cells);tbody.append(row);
    const network=select(cells[0],'Network',networks,value?.network||available[0][0]);network.setAttribute('aria-label','Network');
    const address=field(cells[1],'Receiving address',value?.address||'');address.pattern='0x[0-9a-fA-F]{40}';address.spellcheck=false;
    const tokens=select(cells[2],'Tokens',[['USDT','USDT'],['USDC','USDC']]);tokens.multiple=true;tokens.required=true;for(const o of tokens.options)o.selected=(value?.tokens||['USDT','USDC']).includes(o.value);
    const record={network,address,tokens,row};records.push(record);
    const rowActions=el('div',undefined,'table-row-actions control-row');rowActions.append(button('Remove',()=>{records.splice(records.indexOf(record),1);row.remove();validateNetworks();add.disabled=records.length>=3;form.dispatchEvent(new Event('input',{bubbles:true}));FormUI.update(form)}));cells[3].append(rowActions);
    network.onchange=validateNetworks;
    add.disabled=records.length>=3;FormUI.refresh();form.dispatchEvent(new Event('input',{bubbles:true}));
   }
   for(const w of data?.stablecoin?.wallets||[])addWallet(w);if(!records.length)addWallet();
   const actions=el('div',undefined,'form-heading-actions'),save=el('button','Save','primary compact');save.type='submit';if(!data)actions.append(button('Cancel',onCancel));actions.append(save,enabledLabel);header.append(actions);form.append(msg);
   const payload=()=>({kind:'stablecoin',name:name.value.trim(),fee_mode:'service',enabled:!!data?.enabled,version:data?.version||0,stablecoin:{wallets:records.map(r=>({network:r.network.value,address:r.address.value.trim(),tokens:[...r.tokens.selectedOptions].map(o=>o.value)}))}});
   FormUI.watch(form,save,{read:()=>{const {enabled,version,...draft}=payload();return JSON.stringify(draft)},valid:()=>!busy&&records.length>0&&!!name.value.trim()});
   const lock=value=>{busy=value;for(const c of card.querySelectorAll('input,select,button'))c.disabled=value;enabled.disabled=value||!data;add.disabled=value||records.length>=3;FormUI.update(form)};
   enabled.onchange=async()=>{const checked=enabled.checked;lock(true);try{const next=await api('/v1/system/payment-providers/'+data.id+(checked?'':'/disable'),{method:checked?'PUT':'POST',body:JSON.stringify(checked?{kind:'stablecoin',name:data.name,fee_mode:'service',enabled:true,version:data.version,stablecoin:data.stablecoin}:{version:data.version})});if(current()){data=next;enabled.checked=next.enabled;FormUI.message(msg,'')}}catch(e){if(current()){enabled.checked=data.enabled;FormUI.message(msg,e.message,'error')}}finally{lock(false)}};
   form.onsubmit=async e=>{e.preventDefault();if(busy||!form.reportValidity())return;const input=payload();lock(true);try{const next=await api('/v1/system/payment-providers'+(data?'/'+data.id:''),{method:data?'PUT':'POST',body:JSON.stringify(input)});if(current())onSaved(next,card)}catch(e){if(current())FormUI.message(msg,e.status===409?'Payment settings changed. Reopen Settings before saving.':e.message,'error')}finally{lock(false)}};
   return card;
  },
  extendTopup(form,choice,methods,org){
   const extra=el('div',undefined,'stablecoin-topup-fields');form.append(extra);
   const token=select(extra,'Stablecoin',[['USDT','USDT'],['USDC','USDC']]),network=select(extra,'Network',[]);
   const details=el('details',undefined,'stablecoin-billing-details'),summary=el('summary','Company billing details');details.append(summary);extra.append(details);
   const profile=org.billing_profile||{},address=profile.address||{},grid=el('div',undefined,'provider-form-grid');details.append(grid);
   const fields={business_name:field(grid,'Legal business name',profile.business_name||org.name||''),email:field(grid,'Billing email',profile.email||'',false),line1:field(grid,'Address',address.line1||''),line2:field(grid,'Address line 2',address.line2||'',false),city:field(grid,'City',address.city||''),state:field(grid,'State / region',address.state||'',false),postal_code:field(grid,'Postal code',address.postal_code||''),country:field(grid,'Country code',address.country||'')};fields.country.maxLength=2;fields.country.pattern='[A-Z]{2}';fields.email.type='email';
   const taxType=select(grid,'Tax ID type',[['','No tax ID'],['eu_vat','EU VAT'],['gb_vat','UK VAT'],['vat','VAT number (other country)'],...(profile.tax_ids||[]).filter(t=>!['eu_vat','gb_vat','vat'].includes(t.type)).map(t=>[t.type,t.type])],profile.tax_ids?.[0]?.type||'');
   const taxValue=field(grid,'VAT number (with country prefix)',profile.tax_ids?.[0]?.value||'',false);taxValue.maxLength=100;
   taxType.onchange=()=>{taxValue.required=!!taxType.value;taxValue.disabled=!taxType.value};
   const syncNetwork=()=>{const method=methods.find(m=>m.id===choice.value),wallets=method?.stablecoin?.wallets||[],old=network.value;network.replaceChildren(...wallets.filter(w=>w.tokens.includes(token.value)).map(w=>new Option(networkName(w.network),w.network)));if([...network.options].some(o=>o.value===old))network.value=old;FormUI.refresh()};token.onchange=syncNetwork;
   const sync=()=>{const method=methods.find(m=>m.id===choice.value),stable=method?.kind==='stablecoin',local=stable||method?.stripe_invoices===false;extra.hidden=!local;for(const f of extra.querySelectorAll('input,select'))f.disabled=!local;token.parentElement.hidden=network.parentElement.hidden=!stable;token.disabled=network.disabled=!stable;
    const accepted=new Set((method?.stablecoin?.wallets||[]).flatMap(w=>w.tokens));for(const option of token.options)option.disabled=!accepted.has(option.value);if(!accepted.has(token.value))token.value=[...accepted][0]||'';syncNetwork();if(local){taxType.onchange();details.open=!profile.address?.line1}FormUI.refresh()};
   const billing=()=>({business_name:fields.business_name.value.trim(),email:fields.email.value.trim(),address:Object.fromEntries(['line1','line2','city','state','postal_code','country'].map(k=>[k,fields[k].value.trim()])),tax_ids:taxType.value?[{type:taxType.value,value:taxValue.value.trim()}]:[]});
   return {sync,payload:()=>({network:network.value,token:token.value,billing:billing()}),extra};
  },
  card(parent,p,{api,base,current,onBack,onSettled}){
   const expired=InvoiceUI.expired(p),inactive=expired||p.status==='cancelled';const s=p.stablecoin,card=el('section',undefined,'card stablecoin-payment-card'),head=el('div',undefined,'form-heading console-section-heading'),status=el('p',expired?'Expired':states[p.status]||p.status,'stablecoin-payment-status');status.role='status';head.append(el('h3',p.settled?'Payment received':expired?'Payment expired':p.status==='cancelled'?'Payment cancelled':'Send your payment'));card.append(head,status);if(s.vat?.seller?.legal_name)card.append(el('p','Payable to '+s.vat.seller.legal_name,'field-explanation'));
   const summary=el('p',expired?'This payment request has expired. Do not send funds. Create a new payment to top up your balance.':p.status==='cancelled'?'This payment was cancelled. Do not send funds using these instructions.':(p.settled?'Received ':'Send exactly ')+s.amount+' '+s.token+' on '+networkName(s.network)+'.');card.append(summary);
   const amount=field(card,(p.settled?'Amount received':expired?'Expired amount':p.status==='cancelled'?'Cancelled amount':'Exact amount to send')+' ('+s.token+')',s.amount,false);amount.readOnly=true;
   const address=field(card,'Receiving address',s.address,false);address.readOnly=true;const qr=el('img',undefined,'stablecoin-address-qr');qr.alt='Receiving address on '+networkName(s.network);qr.width=256;qr.height=256;qr.src=base+'/payments/'+p.id+'/qr';if(!p.settled&&!inactive)card.append(qr);
   const network=field(card,'Network',networkName(s.network),false);network.readOnly=true;if(p.expires_at){const due=field(card,'Pay by',new Date(p.expires_at).toLocaleString(),false);due.readOnly=true;}
   const amounts=el('dl',undefined,'stablecoin-breakdown');for(const [label,value]of [['Balance top-up',formatUSD(p.amount_cents/100)],['VAT'+(s.vat?.rule?.treatment==='standard'?' ('+s.vat.rule.rate_percent+'%)':''),formatUSD((p.billing?.tax_cents||0)/100)],['Payment fee','$'+micros(s.fee_micros)]]){const row=el('div');row.append(el('dt',label),el('dd',value));amounts.append(row)}card.append(amounts);
   if(!p.settled&&!inactive)card.append(el('p','Send one transfer for the exact amount, without rounding, using the selected token and network. Pay your wallet or exchange withdrawal fee separately. Payments are checked automatically every minute.','field-explanation'));
   if(s.transaction_hash){const link=el('a','View transaction');link.href=s.explorer_url;link.target='_blank';link.rel='noopener noreferrer';card.append(link)}
   const actions=el('div',undefined,'control-row stablecoin-payment-actions');actions.append(button('Back to payments',onBack));if(p.invoice_mode==='local'&&InvoiceUI.available(p))actions.append(InvoiceUI.link(p,base,{api,status,current}));
   if(p.status==='pending'&&!expired)actions.append(button('Delete payment',async()=>{const action=actions.lastElementChild;action.disabled=true;try{await api(base+'/payments/'+p.id,{method:'DELETE'});if(current())onBack()}catch(e){if(current())FormUI.message(status,e.message,'error');action.disabled=false}}));card.append(actions);parent.replaceChildren(card);FormUI.refresh();return card;
  },
  async requests(parent,{api,base,current,onView}){
   const section=el('section',undefined,'stablecoin-requests'),status=el('p'),rows=el('div'),paging=el('div',undefined,'control-row');status.role='status';section.append(el('h3','Payments'),status,rows,paging);parent.append(section);
   let offset=0,reading=false,signature='',timer;
   async function load(){if(reading||!current()||!section.isConnected||document.hidden)return;reading=true;try{const result=await api(base+'/payments?offset='+offset);if(!current())return;const key=JSON.stringify(result);if(key===signature)return;signature=key;
    const table=el('table',undefined,'docs-table'),thead=el('thead'),tr=el('tr'),tbody=el('tbody');for(const label of ['Date','Method','Amount','Status',''])tr.append(el('th',label));thead.append(tr);
    for(const p of result.data){const r=el('tr'),actions=el('td',undefined,'table-row-actions');if(p.stablecoin)actions.append(button('View payment',()=>onView(p)));else if(!InvoiceUI.expired(p)&&!p.settled&&p.status==='pending'){actions.append(InvoiceUI.payment(p,base,{api,status,current,onPaid:load}))}else if(!InvoiceUI.expired(p)&&p.checkout_url&&!p.settled&&p.status==='pending'){const a=el('a','Continue payment');a.href=p.checkout_url;actions.append(a)}if(p.invoice_mode==='local'&&InvoiceUI.available(p))actions.append(InvoiceUI.link(p,base,{api,status,current}));else if(p.invoice_mode!=='local'&&p.invoice?.pdf_url)actions.append(InvoiceUI.link(p,base,{api,status,current}));r.append(el('td',new Date(p.created_at).toLocaleString()),el('td',p.stablecoin?'Stablecoins':'Bank card'),el('td',p.stablecoin?p.stablecoin.amount+' '+p.stablecoin.token:formatUSD((p.billing?.total_cents??p.amount_cents)/100)),el('td',states[p.status]||p.status),actions);tbody.append(r)}table.append(thead,tbody);const wrap=el('div',undefined,'system-table-wrap');wrap.append(table);rows.replaceChildren(result.data.length?wrap:el('p','No payments yet.','field-explanation'));paging.replaceChildren();
    if(offset||result.has_more){const prev=button('Previous',()=>{offset-=50;signature='';load()}),next=button('Next',()=>{offset+=50;signature='';load()});prev.disabled=!offset;next.disabled=!result.has_more;paging.append(prev,next)}FormUI.message(status,'');
   }catch(e){if(current())FormUI.message(status,e.message,'error')}finally{reading=false}}
   async function poll(){if(!current()||!section.isConnected){clearTimeout(timer);return}await load();timer=setTimeout(poll,15000)}await load();timer=setTimeout(poll,15000);return {reload:()=>{signature='';return load()}};
  },networkName,micros
 };
})();
