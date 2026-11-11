const {evidencePath} = require('./evidence.cjs');
// Stripe UI fixtures only: no account credentials, payments, or external calls.
const {[process.env.UI_BROWSER_ENGINE||'chromium']:chromium}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=process.env.UI_SOURCE_DIR||path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{if(req.method==='GET'&&new URL(req.url,'http://fixture').pathname==='/auth/company-connection'){res.writeHead(404,{'Content-Type':'application/json'});return res.end(JSON.stringify({connected:false}));}let file=path.join(root,path.basename(req.url.split('?')[0]));if(!path.extname(file))file=path.join(root,'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})}),page=await browser.newPage({viewport:{width:1440,height:1080}});page.setDefaultTimeout(10000);
 const org='22222222-2222-4222-8222-222222222222',pid='55555555-5555-4555-8555-555555555555',uid='11111111-1111-4111-8111-111111111111';
 const policy={shared_resources:true,generation_ui:true,personal_providers:true,available_providers:[],version:1};let returnReads=0,failToggle=false,invoiceReady=false,providers=[],writes=[],member=false,administrator=true,checkoutFail=true,noticeConflict=false,noticeSettings={critical_emails:[],success_emails:[],version:1,email_enabled:false,delivery:{pending:0,retrying:0}};const errors=[];page.on('pageerror',e=>errors.push(e.message));
 let footerSettings={copyright_holder:'',version:1};
 let billing={version:1,seller:{legal_name:'Fixture company',email:'billing@example.test',address:{country:'GB',line1:'1 Test Street',city:'London'},vat_number:'GB123456789',vat_registered:true},rules:[{country:'domestic',status:'any',treatment:'standard',rate_percent:20}]};
 const company=()=>({id:org,name:'Payments team',role:member?'member':'admin',balance_usd:50,credit_limit_usd:0,weekly_limit_usd:null,default_weekly_limit_usd:0,storage_bytes:0});
 await page.route('**/v1/**',async route=>{const req=route.request(),url=new URL(req.url()),p=url.pathname,method=req.method(),data=req.postData()?req.postDataJSON():null,json=value=>route.fulfill({json:value});
  if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:policy});
  if(p==='/v1/me')return json({id:uid,name:'Tester',email:'test@example.test',is_system_admin:administrator&&!member});
  if(p==='/v1/system/settings')return json(policy);
  if(p==='/v1/system/legal/footer'){if(method==='PUT')footerSettings={...data,version:footerSettings.version+1};return json(footerSettings)}
  if(p==='/v1/system/billing'){if(method==='PUT')billing={...data,version:billing.version+1};return json(billing)}
  if(/^\/v1\/system\/legal\/(privacy|terms)$/.test(p))return json({text:'Fixture legal document.',version:1});
  if(p==='/v1/organizations')return json({data:[company()]});
  if(p==='/v1/organizations/'+org)return json(company());
  if(p==='/v1/system/payment-notifications'){if(method==='PUT'){if(noticeConflict)return route.fulfill({status:409,json:{error:{message:'conflict'}}});writes.push({notifications:data});noticeSettings={...noticeSettings,...data,version:data.version+1}}return json(noticeSettings)}
  if(p==='/v1/system/email')return json({provider:'',available_providers:[],version:1});
  if(p==='/v1/system/payment-providers'&&method==='GET')return json({data:providers,available_providers:[{id:'stripe',name:'Stripe'}]});
  if(p==='/v1/system/payment-providers/'+pid+'/check')return json({charges_enabled:true});
  if(p==='/v1/system/payment-providers/'+pid+'/disable'){if(failToggle)return route.fulfill({status:503,json:{error:{message:'Provider update failed'}}});writes.push(data);providers[0]={...providers[0],enabled:false,version:providers[0].version+1};return json(providers[0])}
  if(p==='/v1/system/payment-providers'||/^\/v1\/system\/payment-providers\/[0-9a-f-]+$/.test(p)){writes.push(data);const id=method==='POST'?pid:p.split('/').at(-1),index=providers.findIndex(p=>p.id===id),old=providers[index];if(old)assert.equal(data.version,old.version,'save uses latest version after toggling');const next={id,kind:'stripe',name:data.name,enabled:data.enabled,fee_mode:data.fee_mode,stripe_invoices:data.stripe_invoices??old?.stripe_invoices??true,live:true,account_id:'acct_fixture',version:(old?.version||0)+1,credentials_configured:true,webhook_configured:!!data.webhook_secret||!!old?.webhook_configured,webhook_url:'https://gateway.test/v1/payments/webhooks/'+id};if(index<0)providers.push(next);else providers[index]=next;return json(next)}
  if(p.endsWith('/payment-methods'))return json({data:member?[]:providers.filter(p=>p.enabled),min_cents:100,max_cents:1000000,currency:'usd'});
  if(p.endsWith('/payments')&&method==='POST'){writes.push({checkout:data,key:req.headers()['idempotency-key']});return route.fulfill({status:checkoutFail?503:200,json:checkoutFail?{error:{message:'The payment service is temporarily unavailable. Please try again.'}}:{id:uid,status:'paid',credited_cents:4500}})}
  if(p.endsWith('/payments/'+pid+'/refresh')){invoiceReady=true;return json({id:pid,status:'paid',settled:true,credited_cents:4800})}
  if(p.endsWith('/payments/'+uid)&&method==='GET'){returnReads++;return json({id:uid,organization_id:org,status:'paid',settled:true,credited_cents:4500})}
  if(p.endsWith('/payments/'+uid+'/refresh')){writes.push({refresh:true});return json({id:uid,status:'paid',settled:true,credited_cents:4500})}
  if(p.endsWith('/balance-history'))return json({data:[
   {id:'ledger-payment',amount_usd:4.54,kind:'payment',reason:'Payment paid',provider:'Stripe',created_at:'2026-09-16T06:28:56Z',payment:{id:uid,amount_cents:500,fee_cents:46,fee_mode:'organization',credited_cents:454,live:true,status:'paid',settled:true,receipt_url:'https://pay.stripe.com/receipts/fixture',settlement:{currency:'eur',amount_minor:433,fee_minor:40,decimal_places:2}}},
   {id:'ledger-taxed',amount_usd:48,kind:'payment',provider:'Stripe',created_at:'2026-09-19T06:28:56Z',payment:{id:pid,amount_cents:5000,fee_cents:200,fee_mode:'organization',credited_cents:4800,live:true,status:'paid',settled:true,tax_mode:'exclusive',billing:{subtotal_cents:5000,tax_cents:1000,total_cents:6000,business_name:'Buyer Ltd'},invoice:invoiceReady?{id:'in_fixture',status:'paid',number:'INV-100',pdf_url:'https://pay.stripe.com/invoice/test/pdf'}:{id:'in_fixture',status:'pending'}}},
   {id:'ledger-manual',amount_usd:50,kind:'payment',reason:'Top up by credit card',provider:'',created_at:'2026-09-10T06:16:24Z'}
  ],has_more:false});
  if(p.endsWith('/payments'))return json({data:[{id:uid,amount_cents:5000,fee_cents:500,fee_mode:'organization',credited_cents:4500,live:true,status:'paid',settled:true,created_at:'2026-09-15T12:00:00Z'}],has_more:false});
  if(p.endsWith('/storage'))return json({settings:{driver:'gateway'},gateway_allowance_bytes:10000000000});
  return json({data:[]});
 });
 await page.route('**/auth/methods',r=>r.fulfill({json:{google:true,email:false}}));
 const base='http://127.0.0.1:'+server.address().port;
 const layoutMetrics=[];
 async function settingsLayout(state){
  for(const theme of ['dark','light'])for(const width of [1440,1024,768,600,390,320]){
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await page.setViewportSize({width,height:1100});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const geometry=await page.locator('#settings-panel-payments').evaluate(panel=>{
    const notices=panel.querySelector('.payment-notification-settings'),seller=panel.querySelector('.seller-vat-settings'),form=seller.querySelector('form'),head=form.querySelector('.form-heading'),legal=seller.querySelector(':scope > .settings-form-group'),providers=panel.querySelector('.payment-provider-settings'),rect=e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y+scrollY,width:r.width,height:r.height,right:r.right,bottom:r.bottom+scrollY}},buttons=[...legal.querySelector('.form-actions').querySelectorAll('button')].map(rect);
    return {sectionToken:parseFloat(getComputedStyle(panel).getPropertyValue('--space-section')),sellerGap:rect(head).y-rect(notices).bottom,legalGap:rect(legal).y-rect(form).bottom,providersGap:rect(providers).y-rect(seller).bottom,legalContentGap:rect(legal.querySelector('.legal-footer-settings')).y-rect(legal.querySelector('h3')).bottom,legalActionGap:buttons[0].y-rect(legal.querySelector('.legal-footer-settings')).bottom,legalFieldGap:buttons[0].y-rect(legal.querySelector('.legal-footer-settings input')).bottom,buttons,legal:rect(legal),panel:rect(panel),sellerHead:rect(head),noticeBottom:rect(notices).bottom,overflow:document.documentElement.scrollWidth>innerWidth};
   });
   layoutMetrics.push({state,theme,width,...geometry});
   fs.writeFileSync(evidencePath('/tmp/gateway-payment-section-layout/metrics.json'),JSON.stringify(layoutMetrics,null,2));
   for(const [name,top,bottom]of [['seller',geometry.noticeBottom-64,geometry.sellerHead.bottom+100],['legal',geometry.legal.y-80,geometry.legal.bottom+24]]){
    await page.screenshot({path:evidencePath(`/tmp/gateway-payment-section-layout/${state}-${theme}-${width}-${name}.png`),fullPage:true,clip:{x:geometry.panel.x,y:Math.max(0,top),width:geometry.panel.width,height:bottom-Math.max(0,top)}});
   }
   if(!process.env.UI_BASELINE){
    assert.equal(geometry.sellerGap,geometry.sectionToken,'notifications → Seller section gap');
    assert.equal(geometry.legalGap,geometry.sectionToken,'VAT form → Public legal documents gap');
    assert.equal(geometry.providersGap,geometry.sectionToken,'Public legal documents → providers gap');
    assert.equal(geometry.legalContentGap,16,'legal heading → footer field gap');assert.equal(geometry.legalActionGap,16,'footer field → legal actions gap');assert.equal(geometry.legalFieldGap,16,'input border → legal actions gap');assert.equal(geometry.overflow,false,'Payments page overflow at '+width);
    const [privacy,terms]=geometry.buttons;for(const button of geometry.buttons){assert.ok(button.height>=44,'legal action touch target');assert.ok(button.x>=geometry.legal.x&&button.right<=geometry.legal.right+1,'legal action fits section');}
    if(width>=390)assert.equal(privacy.y,terms.y,'legal actions share one row');
    if(privacy.y===terms.y){assert.equal(terms.x-privacy.right,12,'legal action gap');assert.ok(privacy.width<200&&terms.width<200,'legal actions retain compact widths');}
    else assert.ok(terms.y>=privacy.bottom+12,'legal actions wrap with a clear gap');
   }
  }
 }
 try{
  await page.goto(base+'/system');await page.getByRole('tab',{name:'Payments',exact:true}).click();
  await page.locator('.payment-provider-settings').waitFor();
  assert.equal(await page.getByRole('button',{name:'Configure',exact:true}).count(),0);
  assert.ok(await page.locator('.payment-notification-settings').evaluate(e=>!!(e.compareDocumentPosition(document.querySelector('.payment-provider-settings'))&Node.DOCUMENT_POSITION_FOLLOWING)));
  const notices=page.locator('.payment-notification-settings');await notices.getByText('Email delivery is disabled.',{exact:false}).waitFor();
  await settingsLayout('clean');
  if(process.env.UI_BASELINE&&process.env.UI_PAYMENT_SETTINGS_LAYOUT_ONLY){console.log('Captured historical Payments section geometry; baseline is not an acceptance run.');return;}
  // Both legal actions keep native keyboard activation and return focus on close.
  for(const [name,key]of [['Edit Privacy Policy','Enter'],['Edit Terms of Use','Space']]){const open=page.getByRole('button',{name,exact:true});await open.focus();await page.keyboard.press(key);await page.getByRole('dialog').waitFor();await page.waitForFunction(()=>!document.querySelector('.legal-editor textarea').disabled);await page.keyboard.press('Escape');await page.locator('.legal-editor').waitFor({state:'detached'});assert.equal(await open.evaluate(e=>e===document.activeElement),true);}
  await notices.getByLabel('Critical payment errors',{exact:true}).fill('ADMIN@example.test, ops@example.test');
  await notices.getByLabel('Successful balance top-ups',{exact:true}).fill('finance@example.test');
  await notices.getByRole('button',{name:'Save',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.payment-notification-settings textarea').value==='admin@example.test\nops@example.test');
  assert.deepEqual(writes.at(-1).notifications,{critical_emails:['admin@example.test','ops@example.test'],success_emails:['finance@example.test'],version:1});
  noticeConflict=true;await notices.getByLabel('Successful balance top-ups',{exact:true}).fill('');await notices.getByRole('button',{name:'Save',exact:true}).click();await notices.getByText('Another administrator changed these lists. Reopen Settings before saving.',{exact:true}).waitFor();noticeConflict=false;
  const seller=page.locator('.seller-vat-settings');await seller.getByLabel('Legal entity name',{exact:true}).fill('Changed fixture company');await seller.getByRole('button',{name:'Save',exact:true}).waitFor();await seller.getByRole('button',{name:'Remove',exact:true}).press('Enter');await settingsLayout('dirty-feedback-empty-rules');
  await seller.getByRole('button',{name:'Save',exact:true}).press('Enter');await seller.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(billing.seller.legal_name,'Changed fixture company');await settingsLayout('saved');
  if(process.env.UI_PAYMENT_SETTINGS_LAYOUT_ONLY){assert.deepEqual(errors,[]);console.log('PASS: actual Settings composition, section gaps, compact responsive legal actions, keyboard/focus, dirty/error/saved states, both themes at 1440/1024/768/600/390/320px.');return;}
  for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'notifications overflow');await notices.screenshot({path:evidencePath(`/tmp/gateway-payment-notifications-${width}.png`)})}
  await page.setViewportSize({width:1440,height:1080});await page.getByRole('button',{name:'Add provider'}).click();let form=page.locator('.payment-provider-editor');await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await form.getByLabel('Secret API key',{exact:true}).fill('sk_live_fixture');for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await page.setViewportSize({width,height:1100});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));const g=await form.evaluate(form=>{const header=form.querySelector('.provider-detail-header'),buttons=[...header.querySelectorAll('button')],save=header.querySelector('button[type=submit]');return {heights:buttons.map(b=>b.getBoundingClientRect().height),width:save.getBoundingClientRect().width,gap:buttons[1].getBoundingClientRect().left-buttons[0].getBoundingClientRect().right,bound:save.form===form}});assert.ok(g.heights.every(h=>h===40));assert.ok(g.width<=100);assert.equal(g.gap,12);assert.equal(g.bound,true);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Stripe heading overflow '+width);await page.locator('.payment-provider-card').screenshot({path:evidencePath('/tmp/gateway-stripe-provider-actions-')+theme+'-'+width+'.png'})}await page.setViewportSize({width:1440,height:1080});await form.getByRole('button',{name:'Save',exact:true}).press('Enter');await form.getByRole('textbox',{name:/Webhook endpoint/}).waitFor();assert.equal(writes.at(-1).enabled,false);assert.equal(await form.getByLabel('Secret API key',{exact:true}).inputValue(),'************');
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await form.getByLabel('Webhook signing secret',{exact:true}).fill('whsec_fixture');assert.equal(await form.getByLabel('Webhook signing secret',{exact:true}).inputValue(),'whsec_fixture');await form.locator('select').selectOption('organization',{force:true});await form.getByRole('button',{name:'Save',exact:true}).click();await page.waitForFunction(()=>Array.from(document.querySelectorAll('.payment-provider-editor input')).filter(e=>e.value==='************').length===2);
  assert.equal(writes.at(-1).fee_mode,'organization');assert.equal('secret_key' in writes.at(-1),false);
  assert.equal(await page.locator('.payment-provider-card .provider-status').textContent(),'Production');
  assert.equal(await page.locator('.payment-provider-card .master-switch').innerText(),'');
  await form.getByRole('button',{name:'Validate',exact:true}).click();await form.getByText('API access verified.',{exact:true}).waitFor();
  await form.getByLabel('Secret API key',{exact:true}).fill('sk_live_unsaved');assert.equal(await form.getByRole('button',{name:'Validate',exact:true}).isDisabled(),true);
  await page.reload();await page.getByRole('tab',{name:'Payments',exact:true}).click();await form.getByRole('button',{name:'Validate',exact:true}).waitFor();
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.evaluate(theme=>GatewayTheme.apply(theme),theme);await page.setViewportSize({width,height:1100});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
   const boxes=await Promise.all([form.getByLabel('Secret API key',{exact:true}),form.getByRole('button',{name:'Validate',exact:true})].map(n=>n.boundingBox()));assert.ok(boxes.every(b=>b.height===58),'shared control height');assert.ok(boxes[1].y===boxes[0].y||boxes[1].y>=boxes[0].y+boxes[0].height+12,'Validate aligns or wraps with a shared gap');if(process.env.UI_BASELINE!=='1')assert.ok(boxes[0].width>=200,'key field remains readable');
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'provider page overflow '+await page.evaluate(()=>JSON.stringify([...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right>innerWidth+1&&getComputedStyle(e).position!=='absolute').slice(0,12).map(e=>({tag:e.tagName,cls:e.className,width:e.getBoundingClientRect().width,text:e.textContent.slice(0,50)})))));await form.screenshot({path:evidencePath(`/tmp/gateway-payment-settings-${theme}-${width}.png`)})}
  await form.locator('select').selectOption('service',{force:true});await form.getByRole('button',{name:'Save',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.payment-provider-editor select')?.value==='service'&&Array.from(document.querySelectorAll('.payment-provider-editor input')).filter(e=>e.value==='************').length===2);assert.equal(writes.at(-1).fee_mode,'service');assert.equal('webhook_secret' in writes.at(-1),false);
  // Row switches save immediately, preserve credentials, and roll back on failure.
  const providerSwitch=page.locator('.payment-provider-card').getByRole('switch',{name:'Stripe enabled',exact:true});
  await page.waitForFunction(()=>!document.querySelector('.payment-provider-card input[type=checkbox]').disabled);
  await providerSwitch.check();await page.waitForFunction(()=>document.querySelector('.payment-provider-card input[type=checkbox]').checked&&!document.querySelector('.payment-provider-card input[type=checkbox]').disabled);
  // A failed update may roll back before WebKit's uncheck() postcondition runs.
  failToggle=true;assert.equal(await providerSwitch.isChecked(),true);await providerSwitch.click();await page.getByText('Provider update failed',{exact:true}).waitFor();assert.equal(await providerSwitch.isChecked(),true);assert.equal(await providerSwitch.isEnabled(),true);
  failToggle=false;await providerSwitch.uncheck();await page.waitForFunction(()=>!document.querySelector('.payment-provider-card input[type=checkbox]').checked&&!document.querySelector('.payment-provider-card input[type=checkbox]').disabled);
  assert.equal(providers[0].enabled,false);const disabledVersion=providers[0].version;
  await providerSwitch.check();await page.waitForFunction(()=>document.querySelector('.payment-provider-card input[type=checkbox]').checked&&!document.querySelector('.payment-provider-card input[type=checkbox]').disabled);
  assert.equal(providers[0].enabled,true);assert.equal(writes.at(-1).version,disabledVersion);assert.equal('secret_key' in writes.at(-1),false);assert.equal('webhook_secret' in writes.at(-1),false);
  await page.locator('.payment-provider-settings').screenshot({path:evidencePath('/tmp/gateway-payment-switches.png')});
  // Expanded cards keep independent drafts through another card's save/toggle.
  providers.push({...providers[0],id:uid,name:'Second Stripe',account_id:'acct_second'});
  await page.setViewportSize({width:1440,height:1080});await page.goto(base+'/system');await page.getByRole('tab',{name:'Payments',exact:true}).click();
  const cards=page.locator('.payment-provider-card');await cards.nth(1).getByLabel('Display name',{exact:true}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Configure',exact:true}).count(),0);
  await cards.nth(1).getByLabel('Display name',{exact:true}).fill('Unsaved second provider');
  await cards.first().getByLabel('Display name',{exact:true}).fill('Stripe updated');
  await cards.first().getByRole('switch',{name:'Stripe enabled',exact:true}).uncheck();await page.waitForFunction(()=>!document.querySelector('.payment-provider-card input[type=checkbox]').disabled);
  assert.equal(await cards.first().getByLabel('Display name',{exact:true}).inputValue(),'Stripe updated');
  await cards.first().getByRole('switch',{name:'Stripe enabled',exact:true}).check();await page.waitForFunction(()=>!document.querySelector('.payment-provider-card input[type=checkbox]').disabled);
  await cards.first().getByRole('button',{name:'Save',exact:true}).click();await cards.first().getByRole('heading',{name:'Stripe updated',exact:true}).waitFor();
  assert.equal(await cards.nth(1).getByLabel('Display name',{exact:true}).inputValue(),'Unsaved second provider');
  assert.equal(providers[1].name,'Second Stripe');
  await page.locator('#settings-panel-payments').screenshot({path:evidencePath('/tmp/gateway-payment-cards-desktop.png')});
  // Organization administrators see the current mode before payment.
  administrator=false;await page.goto(base+'/organizations');await page.getByRole('button',{name:'Top Up',exact:true}).click();const topup=page.locator('.topup-form');await topup.waitFor();await topup.getByText('Applicable taxes are deducted from your payment before your balance is credited.',{exact:true}).waitFor();const balance=page.locator('.balance-history');await balance.getByText('$5 (€4.33)',{exact:true}).waitFor();await balance.getByText('Paid by Payments team',{exact:true}).first().waitFor();
  assert.equal(await balance.locator('tbody tr').count(),3);assert.equal(await page.getByRole('heading',{name:'Payment history',exact:true}).count(),0);assert.equal(await balance.getByRole('button',{name:'Refresh',exact:true}).count(),0);
  await balance.getByText('Invoice is being prepared',{exact:true}).waitFor();await balance.locator('tr').filter({has:page.getByRole('button',{name:'Refresh invoice',exact:true})}).hover();await balance.getByRole('button',{name:'Refresh invoice',exact:true}).click();await balance.getByRole('link',{name:'Invoice',exact:true}).waitFor();assert.equal(await balance.getByRole('link',{name:'Invoice',exact:true}).getAttribute('href'),'https://pay.stripe.com/invoice/test/pdf');await balance.getByText('Top-up $50 · Tax $10',{exact:true}).waitFor();await topup.getByLabel('Top-up amount (USD, before tax)',{exact:true}).waitFor();
  const historyText=await balance.innerText();assert.ok(historyText.includes('$0.46 (€0.40)'));assert.ok(historyText.includes('+$4.54'));assert.ok(historyText.includes('+$50'));assert.ok(!/US\$|Operation ID|Administrator|ledger-payment/.test(historyText));
  await topup.getByRole('button',{name:'Continue to Stripe'}).click();await page.getByText('The payment service is temporarily unavailable. Please try again.',{exact:true}).waitFor();const first=writes.at(-1);await topup.getByRole('button',{name:'Continue to Stripe'}).click();await page.waitForTimeout(100);assert.notEqual(writes.at(-1).key,first.key);assert.equal(writes.at(-1).checkout.fee_mode,'service');
  for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'organization page overflow '+await page.evaluate(()=>JSON.stringify([...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right>innerWidth+1&&getComputedStyle(e).position!=='absolute').slice(0,20).map(e=>({tag:e.tagName,cls:e.className,width:e.getBoundingClientRect().width,right:e.getBoundingClientRect().right,text:e.textContent.slice(0,40)})))));await page.locator('.organization-summary').screenshot({path:evidencePath(`/tmp/gateway-balance-summary-${width}.png`)});await topup.screenshot({path:evidencePath(`/tmp/gateway-payment-topup-${width}.png`)});await balance.screenshot({path:evidencePath(`/tmp/gateway-balance-history-${width}.png`)})}
  await page.goto(base+'/organizations?payment='+uid+'&organization='+org);await page.locator('.topup-form').waitFor();await page.waitForFunction(()=>location.search==='');await page.waitForTimeout(150);assert.ok(returnReads>0,'return did not read payment status');assert.equal(writes.some(w=>w.refresh),false,'return mutated payment');
  member=true;await page.reload();assert.equal(await page.getByRole('button',{name:'Top Up',exact:true}).count(),0);assert.deepEqual(errors,[]);console.log('PASS: notification subscriptions, stale-save handling, provider setup, saved secret masks, both fee modes, disclosure, unified history, settlement amounts, organization fee payer, hidden IDs, independent checkout attempts, membership gating, Checkout return reconciliation, desktop/mobile layout.');
 }catch(e){console.log((await page.locator('body').innerText()).slice(-9000));await page.screenshot({path:evidencePath('/tmp/gateway-payment-ui-failure.png'),fullPage:true});throw e}finally{await browser.close();server.close()}
})().catch(e=>{console.error(e);process.exitCode=1;server.close()});
