const {evidencePath} = require('./evidence.cjs');
// Local fixture only: no external provider calls or paid generations.
const {chromium,webkit}=require('playwright');const browserType=(process.env.UI_BROWSER_ENGINE||process.env.UI_BROWSER)==='webkit'?webkit:chromium;
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const ui=path.resolve(__dirname,'../../web/public/ui'),out=evidencePath('/tmp/gateway-provider-connections-ui-')+(process.env.UI_BROWSER||'chromium');fs.mkdirSync(out,{recursive:true});
const server=http.createServer((req,res)=>{const name=req.url.startsWith('/ui/')?path.basename(req.url.split('?')[0]):'index.html';const file=path.join(ui,name);if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file))});
const template={id:'native-image',display_name:'Image request profile',media_type:'image',resolutions:['1024x1024'],formats:['png'],no_seed:true,no_watermark:true};
const fields=[{key:'api_key',label:'API key',secret:true,required:true},{key:'base_url',label:'API endpoint',default:'https://fixture.example/v1'}];
let connections=[],writes=[],seq=0,pendingPut;
const view=p=>({...structuredClone(p),icon_url:"/provider-assets/fixture/bytedance/icon.png",configured:!!p.key,hints:{api_key:p.key?'••••1234':''},fields,values:{base_url:p.base_url||'https://fixture.example/v1'},connection:{...structuredClone(p.connection),headers:p.connection.headers.map(h=>({name:h.name,operation:h.operation,hint:'••••'}))},models:p.connection.models.map(m=>m.model),model_count:p.connection.models.length,media_counts:{image:p.connection.models.length},status:'unverified',status_message:'Manual catalog. Model access is not verified.'});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await browserType.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[];page.on('pageerror',e=>{if((process.env.UI_BROWSER_ENGINE||process.env.UI_BROWSER)==='webkit'&&e.message==='ResizeObserver loop completed with undelivered notifications.')return;errors.push(e.message)});
 await page.route('**/provider-assets/fixture/bytedance/icon.png',route=>route.fulfill({body:require('./provider-logo-fixtures.cjs').valid,contentType:'image/png'}));
 await page.route('**/v1/**',async route=>{const req=route.request(),p=new URL(req.url()).pathname,json=x=>route.fulfill({json:x});
  if(p==='/v1/me')return json({id:'admin',is_system_admin:true,name:'Admin'});
  if(p==='/v1/info')return json({provider:'mock',auth_mode:'google',console:{generation_ui:true,personal_providers:true}});
  // Keep bootstrap pending long enough to expose premature fixture interaction.
  if(p==='/v1/organizations'){await new Promise(resolve=>setTimeout(resolve,1500));return json({data:[]});}
  if(p.endsWith('/providers/types'))return json({data:[{id:'bytedance',name:'BytePlus API',icon_url:'/provider-assets/fixture/bytedance/icon.png',fields,profiles:[template]}]});
  if(p==='/v1/admin/providers'||p==='/v1/system/providers'){
   if(req.method()==='POST'){const b=req.postDataJSON();const provider={id:'bytedance--'+(++seq),api_type:b.api_type,name:b.name,version:1,enabled:false,connection:{manual_catalog:true,headers:[],models:[],endpoints:{}}};connections.push(provider);return json(view(provider))}
   return json({data:connections.filter(c=>!c.archived).map(view)});
  }
  if(/\/providers\/bytedance--/.test(p)){const provider=connections.find(c=>p.endsWith(c.id)||p.endsWith(c.id+'/check'));if(!provider)return json({});if(req.method()==='PUT'){const b=req.postDataJSON();writes.push(structuredClone(b));if(pendingPut){const gate=pendingPut;pendingPut=null;gate.enter();await gate.wait;if(gate.fail)return route.fulfill({status:500,json:{error:{message:'Synthetic save failure'}}});}if(b.connection){for(const h of b.connection.headers){if(h.value===undefined)h.value=provider.connection.headers.find(old=>old.name===h.name&&old.operation===h.operation)?.value;}provider.connection=b.connection;}if(b.api_key!==undefined)provider.key=b.api_key;for(const k of ['name','enabled','archived','base_url'])if(k in b)provider[k]=b[k];provider.version++;}return json(view(provider));}
  if(p==='/v1/system/model-settings')return json({data:connections.flatMap(c=>c.connection.models.map((m,i)=>({id:c.id+'-'+i,provider:c.id,model:m.model.id,name:m.model.display_name,public_id:c.id+'/'+m.model.id,enabled_by_default:true,version:1})))});
  if(p==='/v1/providers')return json({data:[]});return json({data:[]});
 });
 async function createProvider(name){
  await page.getByRole('button',{name:'Add provider',exact:true}).click();
  const dialog=page.locator('dialog[open]');
  // FormUI reparents inputs during enhancement; filling before it finishes can lose focus and text.
  const input=dialog.locator('.floating-field').getByLabel('Provider name',{exact:true});
  await input.fill(name);assert.equal(await input.inputValue(),name);
  await dialog.getByRole('button',{name:'Create',exact:true}).click();
  await dialog.waitFor({state:'hidden'});
  await page.waitForFunction(name=>document.querySelector('[data-connection-name]')?.value===name,name);
 }
 try{
  await page.goto(`http://127.0.0.1:${server.address().port}/profile`);
  await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());
  await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();
  await createProvider('Byte Preview');let dialog;
  const timeout=page.getByRole('spinbutton',{name:'Request timeout (seconds)',exact:true});assert.equal(await timeout.inputValue(),'30');await timeout.fill('0');assert.equal(await timeout.evaluate(e=>e.checkValidity()),false);await timeout.focus();await page.keyboard.press('ControlOrMeta+A');await page.keyboard.type('180');
  await page.getByLabel('API key',{exact:true}).fill('fixture-secret');
  await page.getByText('Advanced API settings',{exact:true}).click();await page.getByLabel('Generation URL',{exact:true}).fill('https://private.example/create');
  await page.getByRole('button',{name:'Add header',exact:true}).click();await page.getByLabel('Header name',{exact:true}).fill('X-Private');await page.getByLabel('Header value',{exact:true}).fill('beta-secret');
  await page.getByRole('button',{name:'Add model',exact:true}).click();dialog=page.locator('dialog[open]');await dialog.getByLabel('Upstream model ID',{exact:true}).fill('private-image');await dialog.getByLabel('Display name',{exact:true}).fill('Private image');
  await dialog.getByRole('combobox',{name:'API request profile',exact:true}).click();await page.locator('.model-select-popup img').evaluate(img=>img.decode());assert.equal(await page.locator('.model-select-popup img').getAttribute('data-logo-url'),'/provider-assets/fixture/bytedance/icon.png');await page.locator('.model-select-popup').getByRole('option').click();await dialog.getByLabel('Price per request (USD; optional)',{exact:true}).fill('0.25');await dialog.getByRole('button',{name:'Add',exact:true}).click();
  await page.locator('#saveProviderButton').click();await page.waitForFunction(()=>document.querySelector('#saveProviderButton').textContent==='Saved');
  assert.equal(connections[0].connection.timeout_seconds,180);assert.equal(await page.getByRole('spinbutton',{name:'Request timeout (seconds)',exact:true}).inputValue(),'180');assert.equal(connections[0].connection.models[0].model.id,'private-image');assert.equal(connections[0].connection.headers[0].value,'beta-secret');assert.equal(connections[0].connection.endpoints.generate,'https://private.example/create');
  await page.getByLabel('Provider name',{exact:true}).fill('Byte Preview renamed');await page.locator('#saveProviderButton').click();await page.waitForFunction(()=>document.querySelector('#saveProviderButton').textContent==='Saved');assert.equal(connections[0].connection.headers[0].value,'beta-secret');assert.equal(writes.at(-1).api_key,undefined);
  // Hold PUT before persisting or replying; no timer determines the interleaving.
  const baseline=structuredClone(connections[0]);
  const modelArea=()=>page.locator('#manualModelSettings');
  const headerRows=()=>page.locator('.provider-connection-settings table tbody tr');
  const activate=async button=>{await button.focus();await page.keyboard.press('Enter');};
  const readDraft=()=>page.evaluate(()=>ProviderConnections.payload(elements.providerForm));
  const publicConnection=c=>({...c,headers:c.headers.map(({name,operation})=>({name,operation}))});
  async function assertSaved(){
   await page.waitForFunction(()=>document.querySelector('#saveProviderButton').textContent==='Saved'&&!document.querySelector('[data-connection-name]').disabled);
   const visible=await readDraft(),sent=writes.at(-1);
   assert.equal(visible.name,connections[0].name);
   assert.equal(visible.name,sent.name);
   assert.deepEqual(publicConnection(visible.connection),publicConnection(sent.connection),'visible draft equals submitted connection');
   assert.deepEqual(publicConnection(visible.connection),publicConnection(connections[0].connection),'visible draft equals persisted fixture');
   assert.equal(connections[0].connection.headers[0].value,'beta-secret','saved secret is preserved');
   assert.equal(sent.connection.headers.find(h=>h.name==='X-Private')?.value,undefined,'recorded PUT omits the saved header secret');
   assert.equal(sent.api_key,undefined,'saved API key is omitted');
  }
  async function saveDraft(){
   assert.equal(await page.locator('#saveProviderButton').textContent(),'Save','new edits clear Saved');
   assert.equal(await page.locator('#saveProviderButton').isVisible(),true);
   await page.locator('#saveProviderButton').click();await assertSaved();
  }
  async function modelDialog(action,id){
   const row=modelArea().locator('tbody tr').first();
   await activate(action==='Add'?modelArea().getByRole('button',{name:'Add model',exact:true}):row.getByRole('button',{name:action,exact:true}));
   const d=page.locator('dialog[open]');
   await d.locator('.floating-field').getByLabel('Display name',{exact:true}).fill('Model '+id);
   if(action!=='Edit')await d.getByLabel('Upstream model ID',{exact:true}).fill(id);
   if(action==='Add'){await d.getByRole('combobox',{name:'API request profile',exact:true}).click();await page.locator('.model-select-popup').getByRole('option').click();}
   await d.getByRole('button',{name:action==='Edit'?'Apply':'Add',exact:true}).click();
   assert.equal(await page.locator('dialog[open]').count(),0);
  }
  for(const width of [1440,320])for(const fail of [false,true]){
   connections=[structuredClone(baseline)];await page.setViewportSize({width,height:1100});
   await page.reload();await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());
   await page.evaluate(light=>{document.documentElement.dataset.theme=light?'light':'dark';},width===320);
   await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();
   await page.locator('.provider-connection-settings .floating-field').getByLabel('Provider name',{exact:true}).fill('Pending '+width+' '+fail);
   await page.getByText('Advanced API settings',{exact:true}).click();
   const draft=await readDraft(),beforeWrites=writes.length;
   let enter,release;const entered=new Promise(resolve=>enter=resolve),wait=new Promise(resolve=>release=resolve);
   pendingPut={enter,wait,fail};
   try{
    await Promise.all([page.waitForRequest(request=>request.method()==='PUT'&&request.url().includes('/providers/')),page.locator('#saveProviderButton').click()]);
    await entered;
    assert.equal(writes.length,beforeWrites+1);
    const controls=page.locator('#providerForm input,#providerForm select,#providerForm textarea,#providerForm button,#manualModelSettings button,#archiveProvider,#adminProviderEnabled,#saveProviderButton,#checkProviderButton');
    const unlocked=await controls.evaluateAll(nodes=>nodes.filter(n=>!n.disabled).map(n=>n.textContent||n.getAttribute('aria-label')));
    assert.deepEqual(unlocked,[],'all provider draft controls must lock during PUT');
    // Native click and keyboard activation cannot open a dialog or mutate the draft.
    for(const control of await page.locator('#manualModelSettings button,.provider-connection-settings button').all()){
     await control.evaluate(button=>{button.focus();button.click();});
     assert.equal(await control.evaluate(button=>document.activeElement===button),false);
    }
    await page.keyboard.press('Enter');
    assert.equal(await page.locator('dialog[open]').count(),0,'no model modal can outlive the submitted draft');
    assert.deepEqual(await readDraft(),draft,'locked actions preserve the submitted draft');
    assert.equal(writes.length,beforeWrites+1,'pending Save cannot submit twice');
    await page.screenshot({path:path.join(out,`save-pending-${width}-${fail}.png`),fullPage:true});
   }finally{release();}
   if(fail){
    await page.waitForFunction(()=>document.querySelector('#providerMessage').textContent.includes('Could not save')&&!document.querySelector('[data-connection-name]').disabled);
    assert.deepEqual(await readDraft(),draft,'failed PUT retains the complete draft');
    assert.equal(connections[0].name,baseline.name,'failed PUT does not persist');
    assert.equal(await page.locator('#saveProviderButton').textContent(),'Save','failed PUT never claims Saved');
   }else await assertSaved();
   const recovered=page.locator('#manualModelSettings button,.provider-connection-settings button');
   assert.deepEqual(await recovered.evaluateAll(nodes=>nodes.filter(n=>n.disabled).map(n=>n.textContent)),[],'model/header/archive controls recover after PUT');
   if(fail){await page.locator('#saveProviderButton').click();await assertSaved();}
   await modelDialog('Edit','edited');await saveDraft();
   await modelDialog('Copy','copied');await saveDraft();
   await modelDialog('Add','added');await saveDraft();
   await activate(modelArea().locator('tbody tr').last().getByRole('button',{name:'Remove',exact:true}));await saveDraft();
   await page.getByText('Advanced API settings',{exact:true}).click();
   await page.getByRole('button',{name:'Add header',exact:true}).click();
   assert.equal(await page.locator('#saveProviderButton').textContent(),'Save','adding a header clears Saved before field entry');
   assert.equal(await page.locator('#saveProviderButton').isVisible(),true,'adding a header offers Save');
   await page.getByLabel('Header name',{exact:true}).last().fill('X-Later');await page.getByLabel('Header value',{exact:true}).last().fill('fixture-later');await saveDraft();
   assert.equal(writes.at(-1).connection.headers.find(h=>h.name==='X-Later')?.value,'fixture-later','new header value is submitted');
   assert.equal(connections[0].connection.headers.find(h=>h.name==='X-Later')?.value,'fixture-later','new header value is persisted');
   await page.getByText('Advanced API settings',{exact:true}).click();
   await activate(headerRows().last().getByRole('button',{name:'Remove',exact:true}));await saveDraft();
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'save-lock overflow at '+width);
   console.log(`Provider save lock passed: ${width}px, ${fail?'failed PUT and retry':'successful PUT'}, all model/header actions recover with matching payload and visible state.`);
  }
  await page.setViewportSize({width:1440,height:1100});
  await page.locator('#checkProviderButton').click();await page.waitForFunction(()=>document.querySelector('#providerCheckFeedback').textContent.includes('not verified'));
  for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.getByText('Advanced API settings',{exact:true}).click();await page.screenshot({path:path.join(out,`connections-${width}.png`),fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'page overflow at '+width);await page.getByText('Advanced API settings',{exact:true}).click();}
  await createProvider('Second connection');assert.equal(await page.getByRole('spinbutton',{name:'Request timeout (seconds)',exact:true}).inputValue(),'30');assert.equal(connections[0].connection.timeout_seconds,180);assert.equal(connections.length,2);assert.equal(connections[1].connection.models.length,0);
  await page.getByRole('button',{name:'Add model',exact:true}).click();await page.locator('dialog[open]').getByRole('combobox',{name:'API request profile',exact:true}).click();await page.keyboard.press('Escape');assert.equal(await page.locator('dialog[open]').count(),1);assert.equal(await page.locator('.model-select-popup').count(),0);await page.keyboard.press('Escape');assert.equal(await page.locator('dialog[open]').count(),0);
  await page.locator('#addProvider').focus();await page.keyboard.press('Enter');await page.locator('dialog[open]').waitFor();await page.keyboard.press('Escape');assert.equal(await page.locator('dialog[open]').count(),0);
  await page.getByRole('button',{name:'Archive provider',exact:true}).click();await page.locator('dialog[open]').getByRole('button',{name:'Archive',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#providerAdminCount').textContent==='1');assert.equal(connections[1].archived,true);
  assert.deepEqual(errors,[]);console.log('Provider connection UI passed: create, headers, manual model, secret preservation, independent connections, archive, keyboard and 1440/390/320px.');
 }catch(error){await page.screenshot({path:path.join(out,'failure.png'),fullPage:true});console.error('Provider fixture state:',JSON.stringify(await page.evaluate(()=>({initialized:consoleInitialized,ready:Workspace.ready(),section:currentConsoleSection,selected:selectedAdminProviderID,providers:providerAdminCatalog.map(p=>({id:p.id,fields:p.fields?.map(f=>f.key)})),fields:[...document.querySelectorAll('[data-provider-field]')].map(e=>({id:e.id,label:e.labels?.[0]?.textContent,visible:!!e.getClientRects().length})),dialogs:[...document.querySelectorAll('dialog[open]')].map(d=>d.textContent)}))));throw error;
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
