// Invoked by TestLegalDocumentsBrowserWorkflow: real local API + PostgreSQL.
const {[process.env.UI_BROWSER_ENGINE||'chromium']:engine}=require('playwright'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
(async()=>{
 const [base,adminToken,memberToken]=process.argv.slice(2);assert.equal(new URL(base).hostname,'127.0.0.1');
 const browser=await engine.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH});const errors=[];let releaseInitialBilling,releaseInitialFooter;
 try{
  const admin=await browser.newContext({viewport:{width:1440,height:1000}}),anonymous=await browser.newContext(),member=await browser.newContext();
  for(const context of [admin,anonymous,member])await context.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():r.abort());
  await admin.addCookies([{name:'media_gateway_session',value:adminToken,url:base,httpOnly:true,sameSite:'Lax'}]);
  await member.addCookies([{name:'media_gateway_session',value:memberToken,url:base,httpOnly:true,sameSite:'Lax'}]);
  const publicPage=await anonymous.newPage();await publicPage.goto(base+'/privacy');await publicPage.locator('[data-legal-default]').waitFor({state:'visible'});assert.ok((await publicPage.locator('main').innerText()).includes('Operator setup required'));
  for(const context of [anonymous,member]){const want=context===anonymous?401:403;assert.equal((await context.request.get(base+'/v1/system/legal/privacy')).status(),want);assert.equal((await context.request.put(base+'/v1/system/legal/privacy',{data:{text:'Forbidden',version:1}})).status(),want)}
  const page=await admin.newPage();page.setDefaultTimeout(15000);page.on('pageerror',e=>errors.push(e.message));
  const footerReady=()=>{const input=document.querySelector('.legal-footer-settings input');return !!input&&!input.disabled};
  let initialBillingReached,initialFooterReached;const billingHeld=new Promise(resolve=>initialBillingReached=resolve),footerHeld=new Promise(resolve=>initialFooterReached=resolve);
  await page.route('**/v1/system/billing',async route=>{
   if(route.request().method()!=='GET'||releaseInitialBilling)return route.continue();
   const response=await route.fetch();await new Promise(resolve=>{releaseInitialBilling=resolve;initialBillingReached()});await route.fulfill({response});
  });
  await page.route('**/v1/system/legal/footer',async route=>{
   if(route.request().method()!=='GET'||releaseInitialFooter)return route.continue();
   const response=await route.fetch();await new Promise(resolve=>{releaseInitialFooter=resolve;initialFooterReached()});await route.fulfill({response});
  });
  await page.goto(base+'/system');await page.getByRole('tab',{name:'Payments',exact:true}).click();
  const seller=page.locator('.seller-vat-settings'),footerForm=seller.locator('.legal-footer-settings'),holder=footerForm.getByLabel('Copyright holder',{exact:true}),footerSave=footerForm.getByRole('button',{name:'Save',exact:true});
  await billingHeld;assert.equal(await holder.count(),0);assert.equal(await page.evaluate(footerReady),false,'readiness must not pass before the footer input exists');
  releaseInitialBilling();await footerHeld;await holder.waitFor();assert.equal(await holder.isDisabled(),true);assert.equal(await page.evaluate(footerReady),false,'readiness must wait for the persisted footer GET');
  releaseInitialFooter();await page.waitForFunction(footerReady);await page.unroute('**/v1/system/billing');await page.unroute('**/v1/system/legal/footer');
  assert.equal(await holder.inputValue(),'');assert.equal(await holder.getAttribute('placeholder'),'[Your company name]');
  for(const context of [anonymous,member]){const want=context===anonymous?401:403;assert.equal((await context.request.get(base+'/v1/system/legal/footer')).status(),want);assert.equal((await context.request.put(base+'/v1/system/legal/footer',{data:{copyright_holder:'Denied',version:1}})).status(),want)}
  const expectedFooter=value=>'© '+new Date().getFullYear()+' '+value;
  assert.equal(await publicPage.locator('.site-footer-copyright').innerText(),expectedFooter('[Your company name]'));
  const footerAttack='<img src=x onerror="window.pwned=true"> & <script>window.pwned=true</script>';
  await holder.fill(footerAttack);await footerSave.press('Enter');await footerForm.getByRole('button',{name:'Saved',exact:true}).waitFor();
  await publicPage.reload();await publicPage.evaluate(()=>GatewaySite.ready);assert.equal(await publicPage.locator('.site-footer-copyright').innerText(),expectedFooter(footerAttack));assert.equal(await publicPage.locator('.site-footer-copyright img,.site-footer-copyright script').count(),0);assert.equal(await publicPage.evaluate(()=>window.pwned),undefined);
  await page.reload();await page.getByRole('tab',{name:'Payments',exact:true}).click();await page.waitForFunction(footerReady);assert.equal(await holder.inputValue(),footerAttack);
  const currentFooter=await(await admin.request.get(base+'/v1/system/legal/footer')).json();await admin.request.put(base+'/v1/system/legal/footer',{data:{copyright_holder:'Concurrent footer holder',version:currentFooter.version}});
  await holder.fill('Retained footer draft');await footerSave.press('Enter');await footerForm.getByText(/changed elsewhere/).waitFor();assert.equal(await holder.inputValue(),'Retained footer draft');assert.equal((await(await admin.request.get(base+'/v1/system/legal/footer')).json()).copyright_holder,'Concurrent footer holder');
  await footerSave.press('Enter');await footerForm.getByRole('button',{name:'Saved',exact:true}).waitFor();
  let heldFooter,reachedFooter,footerWrites=0;const footerReached=new Promise(resolve=>reachedFooter=resolve);await page.route('**/v1/system/legal/footer',async route=>{if(route.request().method()!=='PUT')return route.continue();footerWrites++;const response=await route.fetch();await new Promise(resolve=>{heldFooter=resolve;reachedFooter()});await route.fulfill({response});});
  await holder.fill('Pending footer holder');await footerSave.press('Enter');await page.waitForFunction(()=>document.querySelector('.legal-footer-settings input').disabled);assert.equal(await footerSave.isDisabled(),true);await footerReached;heldFooter();await footerForm.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(footerWrites,1);await page.unroute('**/v1/system/legal/footer');
  const longHolder='Configured operator '+('X'.repeat(170));await holder.fill(longHolder);await footerSave.press('Enter');await footerForm.getByRole('button',{name:'Saved',exact:true}).waitFor();await publicPage.reload();await publicPage.evaluate(()=>GatewaySite.ready);
  const out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/legal-server');fs.mkdirSync(out,{recursive:true});
  for(const theme of ['dark','light'])for(const width of [1440,1024,768,600,390,320]){
   for(const target of [page,publicPage]){await target.setViewportSize({width,height:1000});await target.evaluate(t=>GatewayTheme.apply(t),theme);await target.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.ok(await target.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'real footer page overflow '+width)}
   const field=await footerForm.boundingBox(),actions=await seller.locator(':scope > .settings-form-group .form-actions').boundingBox();assert.ok(field.width<=width);assert.equal(actions.y-(field.y+field.height),16);const inputBounds=await holder.boundingBox();assert.equal(actions.y-inputBounds.y-inputBounds.height,16,'input border to legal actions');await holder.fill(longHolder+' edited');await footerSave.waitFor();await seller.locator(':scope > .settings-form-group').screenshot({path:path.join(out,'settings-'+theme+'-'+width+'.png')});await holder.fill(longHolder);
   const footer=publicPage.locator('.site-footer');await footer.scrollIntoViewIfNeeded();await footer.screenshot({path:path.join(out,'footer-'+theme+'-'+width+'.png')});
  }
  await holder.fill('é'.repeat(101));assert.equal(await footerSave.isVisible(),false,'UTF-8 holder limit blocks invalid inline Save');await holder.fill('');await footerSave.press('Enter');await footerForm.getByRole('button',{name:'Saved',exact:true}).waitFor();await publicPage.reload();await publicPage.evaluate(()=>GatewaySite.ready);assert.equal(await publicPage.locator('.site-footer-copyright').innerText(),expectedFooter('[Your company name]'));
  await holder.fill('Persisted footer holder');await footerSave.press('Enter');await footerForm.getByRole('button',{name:'Saved',exact:true}).waitFor();
  await seller.getByRole('button',{name:'Edit Privacy Policy'}).click();
  const dialog=page.getByRole('dialog'),input=dialog.locator('textarea'),save=dialog.getByRole('button',{name:'Save',exact:true});await page.waitForFunction(()=>!document.querySelector('dialog.legal-editor textarea').disabled);
  assert.ok((await input.inputValue()).includes('Operator setup required'));
  const attack='<img src=x onerror="window.pwned=true">\n\n<script>window.pwned=true</script>';
  await input.fill(attack);await save.click();await page.locator('dialog.legal-editor').waitFor({state:'detached'});
  await publicPage.reload();await publicPage.evaluate(()=>GatewaySite.ready);await publicPage.locator('.legal-text').filter({hasText:'<img'}).waitFor();assert.equal(await publicPage.locator('.legal-text').textContent(),attack);assert.equal(await publicPage.locator('.legal-text img,.legal-text script').count(),0);assert.equal(await publicPage.evaluate(()=>window.pwned),undefined);
  await seller.getByRole('button',{name:'Edit Privacy Policy'}).click();await page.waitForFunction(()=>!document.querySelector('dialog.legal-editor textarea').disabled);assert.equal(await input.inputValue(),attack);
  const current=await (await admin.request.get(base+'/v1/system/legal/privacy')).json();assert.equal((await admin.request.put(base+'/v1/system/legal/privacy',{data:{text:'Concurrent update',version:current.version}})).status(),200);
  await input.fill('Conflicting browser draft');await save.click();await dialog.getByText(/changed elsewhere/).waitFor();assert.equal(await input.inputValue(),'Conflicting browser draft');await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await page.locator('dialog.legal-editor').waitFor({state:'detached'});
  await seller.getByRole('button',{name:'Edit Terms of Use'}).click();await page.waitForFunction(()=>!document.querySelector('dialog.legal-editor textarea').disabled);await input.fill('Terms from browser');await save.click();await page.locator('dialog.legal-editor').waitFor({state:'detached'});
  const billing=await (await admin.request.get(base+'/v1/system/billing')).json();billing.seller={...billing.seller,legal_name:'Fixture company',email:'fixture@example.test',address:{country:'CY',line1:'Fixture street',city:'Fixture city'},vat_registered:false,vat_number:'',oss_registered:false};
  const companyWrite=await admin.request.put(base+'/v1/system/billing',{data:billing});assert.equal(companyWrite.status(),200,await companyWrite.text());
  assert.equal((await(await admin.request.get(base+'/v1/system/legal/footer')).json()).copyright_holder,'Persisted footer holder','billing save preserves configured footer');
  assert.equal((await (await admin.request.get(base+'/v1/system/legal/privacy')).json()).text,'Concurrent update');assert.equal((await (await admin.request.get(base+'/v1/system/legal/terms')).json()).text,'Terms from browser');
  await seller.getByRole('button',{name:'Edit Privacy Policy'}).click();await page.waitForFunction(()=>!document.querySelector('dialog.legal-editor textarea').disabled);
  await input.fill('é'.repeat(40000));assert.ok(await save.isDisabled(),'UTF-8 byte limit blocks oversized draft');
  await dialog.getByRole('button',{name:'Use default template'}).click();await save.click();await page.locator('dialog.legal-editor').waitFor({state:'detached'});
  await publicPage.reload();await publicPage.evaluate(()=>GatewaySite.ready);await publicPage.locator('[data-legal-default]').waitFor({state:'visible'});assert.ok((await publicPage.locator('main').innerText()).includes('Operator setup required'));
  await publicPage.goto(base+'/terms');await publicPage.locator('.legal-text').filter({hasText:'Terms from browser'}).waitFor();
  assert.deepEqual(errors.filter(e=>e!=='ResizeObserver loop completed with undelivered notifications.'),[]);
  console.log('PASS real sessions/API/PostgreSQL: public fallback, anonymous/member denied, Administrator company-settings editor, persisted override, literal XSS payload, edit conflict, independent documents, company save isolation, byte limit and reset; independent footer persistence, default/reset, XSS escaping, conflict draft retention, explicit retry, one pending write, UTF-8 validation and both-theme responsive layouts.');
 }finally{releaseInitialBilling?.();releaseInitialFooter?.();await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
