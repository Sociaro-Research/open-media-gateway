// Invoked by TestLegalDocumentsBrowserWorkflow: real local API + PostgreSQL.
const {[process.env.UI_BROWSER_ENGINE||'chromium']:engine}=require('playwright'),assert=require('node:assert/strict');
(async()=>{
 const [base,adminToken,memberToken]=process.argv.slice(2);assert.equal(new URL(base).hostname,'127.0.0.1');
 const browser=await engine.launch({headless:true});const errors=[];
 try{
  const admin=await browser.newContext({viewport:{width:1440,height:1000}}),anonymous=await browser.newContext(),member=await browser.newContext();
  for(const context of [admin,anonymous,member])await context.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():r.abort());
  await admin.addCookies([{name:'media_gateway_session',value:adminToken,url:base,httpOnly:true,sameSite:'Lax'}]);
  await member.addCookies([{name:'media_gateway_session',value:memberToken,url:base,httpOnly:true,sameSite:'Lax'}]);
  const publicPage=await anonymous.newPage();await publicPage.goto(base+'/privacy');await publicPage.locator('[data-legal-default]').waitFor({state:'visible'});assert.ok((await publicPage.locator('main').innerText()).includes('Operator setup required'));
  for(const context of [anonymous,member]){const want=context===anonymous?401:403;assert.equal((await context.request.get(base+'/v1/system/legal/privacy')).status(),want);assert.equal((await context.request.put(base+'/v1/system/legal/privacy',{data:{text:'Forbidden',version:1}})).status(),want)}
  const page=await admin.newPage();page.setDefaultTimeout(15000);page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base+'/system');await page.locator('[data-system-page=settings]').click();await page.getByRole('tab',{name:'Payments',exact:true}).click();
  const seller=page.locator('.seller-vat-settings');await seller.getByRole('button',{name:'Edit Privacy Policy'}).click();
  const dialog=page.getByRole('dialog'),input=dialog.locator('textarea'),save=dialog.getByRole('button',{name:'Save',exact:true});await page.waitForFunction(()=>!document.querySelector('dialog.legal-editor textarea').disabled);
  assert.ok((await input.inputValue()).includes('Operator setup required'));
  const attack='<img src=x onerror="window.pwned=true">\n\n<script>window.pwned=true</script>';
  await input.fill(attack);await save.click();await page.locator('dialog.legal-editor').waitFor({state:'detached'});
  await publicPage.reload();await publicPage.locator('.legal-text').filter({hasText:'<img'}).waitFor();assert.equal(await publicPage.locator('.legal-text').textContent(),attack);assert.equal(await publicPage.locator('.legal-text img,.legal-text script').count(),0);assert.equal(await publicPage.evaluate(()=>window.pwned),undefined);
  await seller.getByRole('button',{name:'Edit Privacy Policy'}).click();await page.waitForFunction(()=>!document.querySelector('dialog.legal-editor textarea').disabled);assert.equal(await input.inputValue(),attack);
  const current=await (await admin.request.get(base+'/v1/system/legal/privacy')).json();assert.equal((await admin.request.put(base+'/v1/system/legal/privacy',{data:{text:'Concurrent update',version:current.version}})).status(),200);
  await input.fill('Conflicting browser draft');await save.click();await dialog.getByText(/changed elsewhere/).waitFor();assert.equal(await input.inputValue(),'Conflicting browser draft');await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await page.locator('dialog.legal-editor').waitFor({state:'detached'});
  await seller.getByRole('button',{name:'Edit Terms of Use'}).click();await page.waitForFunction(()=>!document.querySelector('dialog.legal-editor textarea').disabled);await input.fill('Terms from browser');await save.click();await page.locator('dialog.legal-editor').waitFor({state:'detached'});
  const billing=await (await admin.request.get(base+'/v1/system/billing')).json();billing.seller={...billing.seller,legal_name:'Fixture company',email:'fixture@example.test',address:{country:'CY',line1:'Fixture street',city:'Fixture city'},vat_registered:false,vat_number:'',oss_registered:false};
  const companyWrite=await admin.request.put(base+'/v1/system/billing',{data:billing});assert.equal(companyWrite.status(),200,await companyWrite.text());
  assert.equal((await (await admin.request.get(base+'/v1/system/legal/privacy')).json()).text,'Concurrent update');assert.equal((await (await admin.request.get(base+'/v1/system/legal/terms')).json()).text,'Terms from browser');
  await seller.getByRole('button',{name:'Edit Privacy Policy'}).click();await page.waitForFunction(()=>!document.querySelector('dialog.legal-editor textarea').disabled);
  await input.fill('é'.repeat(40000));assert.ok(await save.isDisabled(),'UTF-8 byte limit blocks oversized draft');
  await dialog.getByRole('button',{name:'Use default template'}).click();await save.click();await page.locator('dialog.legal-editor').waitFor({state:'detached'});
  await publicPage.reload();await publicPage.locator('[data-legal-default]').waitFor({state:'visible'});assert.ok((await publicPage.locator('main').innerText()).includes('Operator setup required'));
  await publicPage.goto(base+'/terms');await publicPage.locator('.legal-text').filter({hasText:'Terms from browser'}).waitFor();
  assert.deepEqual(errors.filter(e=>e!=='ResizeObserver loop completed with undelivered notifications.'),[]);
  console.log('PASS real sessions/API/PostgreSQL: public fallback, anonymous/member denied, Administrator company-settings editor, persisted override, literal XSS payload, edit conflict, independent documents, company save isolation, byte limit and reset.');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
