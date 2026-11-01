const {evidencePath}=require('./evidence.cjs');
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const defaults=()=>({shared_resources:false,generation_ui:true,personal_providers:true,allow_inline_references:true,customer_workspaces:false,setup_pending:false,markup_percent:0,version:1});
let state=defaults(),writes=[],failProfile=false,readGate=null,profileGate=null,saveGate=null,markupGate=null,markupSaveGate=null,mountGate=null,settingsReads=[];
const gates=new Set();
function gate(){let arrive,release;const item={arrived:new Promise(r=>arrive=r),wait:new Promise(r=>release=r),arrive,release};gates.add(item);return item;}
const server=http.createServer(async(req,res)=>{
 try{
  const url=new URL(req.url,'http://fixture.test'),p=url.pathname;
  const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
  if(p.startsWith('/v1/')){
   if(['/v1/system/payment-providers','/v1/system/login-providers'].includes(p))settingsReads.push(p);
   if(p==='/v1/system/payment-notifications'&&mountGate){const held=mountGate;mountGate=null;held.arrive();await held.wait;return json({});}
   let data={};if(req.method==='POST'||req.method==='PUT'){const chunks=[];for await(const chunk of req)chunks.push(chunk);data=JSON.parse(Buffer.concat(chunks).toString()||'{}');}
   if(p==='/v1/me')return json({id:'aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa',session_id:'synthetic',name:'Administrator',email:'admin@example.test',is_system_admin:true});
   if(p==='/v1/info')return json({auth_mode:'google',console:state});
   if(p==='/v1/system/setup-profile'){
    writes.push({path:p,data});if(failProfile)return json({error:{message:'Fixture apply failed'}},503);
    if(data.version!==state.version)return json({error:{message:'Settings changed'}},409);
    state={...state,setup_pending:false,shared_resources:data.profile==='operator',personal_providers:data.profile==='personal'||data.allow_personal,version:state.version+1};
    const saved={...state},held=profileGate;profileGate=null;if(held){held.arrive();await held.wait;}return json(saved);
   }
   if(p==='/v1/system/settings'){
    if(req.method==='PUT'){
     writes.push({path:p,data});if(data.version!==state.version)return json({error:{message:'Settings changed'}},409);
     state={...state,...data,version:state.version+1};const saved={...state},held=saveGate;saveGate=null;if(held){held.arrive();await held.wait;}return json(saved);
    }
    const saved={...state},held=readGate;readGate=null;if(held){held.arrive();await held.wait;}return json(saved);
   }
   if(p==='/v1/system/markups'){
    if(req.method==='PUT'){writes.push({path:p,data});if(data.version!==state.version)return json({error:{message:'Settings changed'}},409);state={...state,markup_percent:data.default_percent,version:state.version+1};}
    const policy={system:{default_percent:state.markup_percent,exceptions:[],version:state.version},company:{default_percent:null,exceptions:[],version:0}},held=req.method==='PUT'?markupSaveGate:markupGate;if(req.method==='PUT')markupSaveGate=null;else markupGate=null;if(held){held.arrive();await held.wait;}return json(policy);
   }
   if(p==='/v1/system/email')return json({provider:'',from:'',key_configured:false,enabled:false,version:1,available_providers:[{id:'resend',name:'Resend'}]});
   if(p==='/v1/system/catalog/settings')return json({timezone:'UTC',email:'',version:1});
   if(p==='/v1/system/login-providers')return json({data:[],available_providers:[],email_delivery_configured:false});
   if(p==='/v1/sales/bonuses')return json({settings:{manager_percent:5,supervisor_percent:1,version:1},people:[]});
   return json({data:[]});
  }
  const name=path.basename(p),file=path.join(root,name.includes('.')?name:'index.html');
  if(!fs.existsSync(file)){res.writeHead(404);return res.end();}
  res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
 }catch(error){console.error(error);res.writeHead(500);res.end();}
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base='http://127.0.0.1:'+server.address().port,engine=process.env.UI_BROWSER_ENGINE||'chromium';
 const browser=await(engine==='webkit'?webkit:chromium).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1000}});page.setDefaultTimeout(10000);
 const inFlight=new Set();page.on('request',r=>{if(new URL(r.url()).pathname.startsWith('/v1/'))inFlight.add(r);});for(const event of ['requestfinished','requestfailed'])page.on(event,r=>inFlight.delete(r));
 async function settled(){const end=Date.now()+10000;let quiet=Date.now();while(Date.now()<end){if(inFlight.size)quiet=Date.now();else if(Date.now()-quiet>=100)return;await page.waitForTimeout(20);}assert.equal(inFlight.size,0,'previous fixture case left API requests unresolved');}
 let caseName='initial';const errors=[],results=[],externalRequests=[],failedRequests=[];page.on('requestfailed',r=>failedRequests.push({path:new URL(r.url()).pathname,error:r.failure()?.errorText}));page.on('pageerror',e=>{if(!e.message.includes('ResizeObserver loop completed'))errors.push({case:caseName,message:e.message});});
 await page.route(url=>url.origin!==base,route=>{externalRequests.push(route.request().url());return route.abort();});
 const form=()=>page.locator('form.system-settings'),dialog=()=>page.locator('dialog.setup-dialog[open]');
 const save=()=>page.locator('#settings-panel-system button[form]');
 const input=name=>form().locator('input[name='+name+']');
 const summary=()=>page.locator('.setup-section > p').first();
 const external=patch=>{state={...state,...patch,version:state.version+1};};
 async function open(){await page.goto(base+'/system');await form().waitFor();await settled();}
 async function fresh(patch={}){state={...defaults(),...patch};writes=[];failProfile=false;readGate=profileGate=saveGate=null;settingsReads=[];await open();}
 async function choose(profile='operator',personal=false){await page.getByRole('button',{name:'Change Mode',exact:true}).click();await dialog().waitFor();await dialog().locator('input[value='+profile+']').check();if(profile==='operator')await dialog().locator('input[type=checkbox]').setChecked(personal);}
 async function apply(){const response=page.waitForResponse(r=>r.url().endsWith('/v1/system/setup-profile')&&r.request().method()==='POST');await dialog().getByRole('button',{name:'Apply',exact:true}).click();const r=await response;assert.equal(r.status(),200);await dialog().waitFor({state:'hidden'});return r.json();}
 async function saveAdvanced(status=200){const response=page.waitForResponse(r=>r.url().endsWith('/v1/system/settings')&&r.request().method()==='PUT');await save().click();const r=await response;assert.equal(r.status(),status);if(status===200)await save().filter({hasText:'Saved'}).waitFor();return r;}
 async function check(name,run){if(process.env.UI_CASE_FILTER&&!name.includes(process.env.UI_CASE_FILTER))return;caseName=name;await fresh();await run();await settled();assert.deepEqual(errors,[]);results.push({name,version:state.version,writes:[...writes]});console.log('PASS '+engine+': '+name);}
 try{
  if(process.env.UI_MODE_BASELINE){await fresh();await choose('personal');for(const theme of ['light','dark'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>{document.documentElement.dataset.theme=t;document.documentElement.style.setProperty('--accent','#b5e21d');},theme);await dialog().screenshot({path:evidencePath(`/tmp/mode-before-${engine}-${theme}-${width}.png`)});}return;}
  await check('Settings actions show only a changed mode',async()=>{
   await page.getByRole('heading',{name:'Installation Mode',exact:true}).waitFor();const groupGap=await page.locator('.setup-section').evaluate(n=>n.querySelector('.field-explanation').getBoundingClientRect().top-n.querySelector('h2').getBoundingClientRect().bottom);assert.equal(groupGap,16);
   await choose('personal');assert.equal(await dialog().getByRole('button',{name:'Skip for now',exact:true}).isVisible(),false);assert.equal(await dialog().getByRole('button',{name:'Apply',exact:true}).isVisible(),false);await dialog().locator('input[value=operator]').check();assert.equal(await dialog().getByRole('button',{name:'Apply',exact:true}).isVisible(),true);await dialog().locator('input[value=personal]').check();assert.equal(await dialog().getByRole('button',{name:'Apply',exact:true}).isVisible(),false);
   for(const theme of ['light','dark'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>{document.documentElement.dataset.theme=t;document.documentElement.style.setProperty('--accent','#b5e21d');},theme);await page.waitForFunction(()=>getComputedStyle(document.querySelector('.setup-option input[type=radio]:checked')).borderTopColor==='rgb(181, 226, 29)');const radio=await dialog().locator('input[type=radio]:checked').evaluate(n=>{const s=getComputedStyle(n);return {border:s.borderTopWidth,color:s.borderTopColor,background:s.backgroundColor,width:n.getBoundingClientRect().width,height:n.getBoundingClientRect().height};});assert.deepEqual(radio,{border:'1px',color:'rgb(181, 226, 29)',background:'rgb(181, 226, 29)',width:16,height:16});await dialog().screenshot({path:evidencePath(`/tmp/mode-after-${engine}-${theme}-${width}.png`)});}await page.keyboard.press('Escape');assert.equal(writes.length,0);await page.setViewportSize({width:1440,height:1000});
  });
  await check('Sociaro copy describes a dedicated installation company',async()=>{
   await choose('sociaro');assert.match(await dialog().locator('.setup-option').filter({hasText:'Sociaro'}).textContent(),/new connection creates a dedicated company/i);const preview=await dialog().locator('[aria-live=polite]').textContent();assert.match(preview,/with you as Supermanager/);assert.match(preview,/reconnecting uses that same company/i);assert.doesNotMatch(preview,/create or select/i);assert.equal(writes.length,0);
   for(const theme of ['light','dark'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);assert.equal(await dialog().evaluate(n=>n.scrollWidth<=n.clientWidth),true);await dialog().screenshot({path:evidencePath('/tmp/dedicated-company-'+theme+'-'+width+'.png')});}await page.keyboard.press('Escape');
  });
  await check('clean apply then advanced save',async()=>{
   await choose();const next=await apply();assert.equal(next.version,2);assert.equal(state.shared_resources,true);assert.equal(state.personal_providers,false);assert.equal(await save().isVisible(),false);assert.equal(await summary().innerText(),'Service for companies');
   await input('generation').uncheck();await saveAdvanced();assert.equal(writes.at(-1).data.version,2);assert.equal(state.generation_ui,false);
  });
  await check('unsaved unrelated fields survive apply',async()=>{
   await input('generation').uncheck();await input('inlineReferences').uncheck();await choose();await apply();
   assert.equal(await input('generation').isChecked(),false);assert.equal(await input('inlineReferences').isChecked(),false);assert.equal(state.shared_resources,true);assert.equal(await save().isVisible(),true);
   await saveAdvanced();assert.equal(state.allow_inline_references,false);assert.equal(state.generation_ui,false);assert.equal(writes.at(-1).data.version,2);
  });
  await check('scenario policy has no competing advanced controls',async()=>{
   assert.equal(await form().locator('input[name=shared],input[name=providers]').count(),0);await input('generation').uncheck();await choose('operator',true);await apply();
   assert.equal(await summary().innerText(),'Mixed: shared and personal providers');assert.equal(state.personal_providers,true);await saveAdvanced();assert.equal(state.personal_providers,true);assert.equal(state.shared_resources,true);assert.equal('shared_resources' in writes.at(-1).data,false);assert.equal('personal_providers' in writes.at(-1).data,false);
  });
  await check('cancel preserves draft and performs no write',async()=>{
   await input('generation').uncheck();await choose();await page.keyboard.press('Escape');await dialog().waitFor({state:'hidden'});assert.equal(writes.length,0);assert.equal(state.version,1);assert.equal(await input('generation').isChecked(),false);await saveAdvanced();assert.equal(writes.at(-1).data.version,1);
  });
  await check('failed apply retry and repeated submit',async()=>{
   await input('inlineReferences').uncheck();failProfile=true;await choose();await dialog().getByRole('button',{name:'Apply',exact:true}).click();await dialog().getByText('Fixture apply failed',{exact:true}).waitFor();assert.equal(state.version,1);
   failProfile=false;const held=profileGate=gate();await dialog().locator('form').evaluate(f=>{f.requestSubmit();f.requestSubmit();});await held.arrived;assert.equal(writes.length,2);assert.equal(await dialog().getByRole('button',{name:'Apply',exact:true}).isDisabled(),true);
   await page.keyboard.press('Escape');assert.equal(await dialog().isVisible(),true);held.release();await dialog().waitFor({state:'hidden'});assert.equal(await input('inlineReferences').isChecked(),false);await saveAdvanced();assert.equal(writes.at(-1).data.version,2);
  });
  await check('external edit still conflicts and requires reviewed retry',async()=>{
   await choose();await apply();await input('generation').uncheck();external({allow_inline_references:false});
   await saveAdvanced(409);await page.getByText('Settings changed. Current values are loaded; your edits are retained. Review them before saving again.',{exact:true}).waitFor();
   assert.equal(writes.filter(w=>w.path==='/v1/system/settings').length,1);assert.equal(await input('generation').isChecked(),false);assert.equal(await input('inlineReferences').isChecked(),false);
   await saveAdvanced();assert.deepEqual(writes.filter(w=>w.path==='/v1/system/settings').map(w=>w.data.version),[2,3]);assert.equal(state.allow_inline_references,false);
  });
  await check('external edit before chooser is adopted without losing draft',async()=>{
   await input('inlineReferences').uncheck();external({generation_ui:false});await choose();await apply();
   assert.equal(await input('generation').isChecked(),false);assert.equal(await input('inlineReferences').isChecked(),false);await page.getByText('Settings changed elsewhere. Your edits are preserved. Review the current values before saving.',{exact:true}).waitFor();await saveAdvanced();assert.equal(writes.at(-1).data.version,3);
  });
  await check('own apply synchronizes shared markup version and preserves its draft',async()=>{
   await page.getByRole('tab',{name:'Markups',exact:true}).click();await page.getByLabel('Default markup (%)',{exact:true}).fill('10');await page.getByRole('tab',{name:'System',exact:true}).click();await choose();await apply();
   await page.getByRole('tab',{name:'Markups',exact:true}).click();assert.equal(await page.getByLabel('Default markup (%)',{exact:true}).inputValue(),'10');await page.locator('.markup-settings').getByRole('button',{name:'Save',exact:true}).click();await page.locator('.markup-settings').getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(writes.at(-1).data.version,2);
   await page.getByRole('tab',{name:'System',exact:true}).click();await input('generation').uncheck();await saveAdvanced();assert.equal(writes.at(-1).data.version,3);assert.equal(state.markup_percent,10);
  });
  await check('external markup is not silently overwritten by a stale draft',async()=>{
   await page.getByRole('tab',{name:'Markups',exact:true}).click();await page.getByLabel('Default markup (%)',{exact:true}).fill('10');external({markup_percent:20});await page.getByRole('tab',{name:'System',exact:true}).click();await choose();await apply();
   await choose('personal');await apply();await input('generation').uncheck();await saveAdvanced();
   await page.getByRole('tab',{name:'Markups',exact:true}).click();const response=page.waitForResponse(r=>r.url().endsWith('/v1/system/markups')&&r.request().method()==='PUT');await page.locator('.markup-settings').getByRole('button',{name:'Save',exact:true}).click();assert.equal((await response).status(),409);assert.equal(writes.at(-1).data.version,1);assert.equal(state.markup_percent,20);
  });
  await check('late markup acknowledgement cannot regress a newer applied version',async()=>{
   await page.getByRole('tab',{name:'Markups',exact:true}).click();await page.getByLabel('Default markup (%)',{exact:true}).fill('10');const held=markupSaveGate=gate();await page.locator('.markup-settings').getByRole('button',{name:'Save',exact:true}).click();await held.arrived;
   await page.getByRole('tab',{name:'System',exact:true}).click();await choose();await apply();const acknowledged=page.waitForResponse(r=>r.url().endsWith('/v1/system/markups')&&r.request().method()==='PUT');held.release();await acknowledged;await page.locator('.markup-settings .save-confirmation').waitFor({state:'attached'});
   await input('generation').uncheck();await saveAdvanced();assert.equal(writes.at(-1).data.version,3);
   await page.getByRole('tab',{name:'Markups',exact:true}).click();await page.getByLabel('Default markup (%)',{exact:true}).fill('11');const response=page.waitForResponse(r=>r.url().endsWith('/v1/system/markups')&&r.request().method()==='PUT');await page.locator('.markup-settings').getByRole('button',{name:'Save',exact:true}).click();assert.equal((await response).status(),200);assert.equal(writes.at(-1).data.version,4);
  });
  await check('late markup mount replays the known own version chain',async()=>{
   const held=markupGate=gate();await page.locator('[data-system-page=settings]').click();await held.arrived;await choose();await apply();held.release();
   await page.getByRole('tab',{name:'Markups',exact:true}).click();const field=page.getByLabel('Default markup (%)',{exact:true});await field.fill('10');const response=page.waitForResponse(r=>r.url().endsWith('/v1/system/markups')&&r.request().method()==='PUT');await page.locator('.markup-settings').getByRole('button',{name:'Save',exact:true}).click();assert.equal((await response).status(),200);assert.equal(writes.at(-1).data.version,2);
  });
  await check('late markup mount cannot bridge an unknown external version',async()=>{
   const held=markupGate=gate();await page.locator('[data-system-page=settings]').click();await held.arrived;external({markup_percent:20});await choose();await apply();held.release();
   await page.getByRole('tab',{name:'Markups',exact:true}).click();await page.getByLabel('Default markup (%)',{exact:true}).fill('10');await page.getByRole('tab',{name:'System',exact:true}).click();await choose('personal');await apply();
   await page.getByRole('tab',{name:'Markups',exact:true}).click();const response=page.waitForResponse(r=>r.url().endsWith('/v1/system/markups')&&r.request().method()==='PUT');await page.locator('.markup-settings').getByRole('button',{name:'Save',exact:true}).click();assert.equal((await response).status(),409);assert.equal(writes.at(-1).data.version,1);assert.equal(state.markup_percent,20);
  });
  await check('obsolete Settings mounts stop before later reads',async()=>{
   const held=mountGate=gate(),before=settingsReads.length;await page.locator('[data-system-page=settings]').click();await held.arrived;await page.locator('[data-system-page=administrators]').click();await page.getByRole('heading',{name:'Users',exact:true}).waitFor();held.release();await settled();assert.equal(settingsReads.length,before,'obsolete Settings must not initiate payment/login reads');assert.equal(writes.length,0);await page.locator('#systemView').screenshot({path:evidencePath('/tmp/obsolete-settings-after.png')});
  });
  await check('late chooser read cannot reopen a replaced settings view',async()=>{
   const held=readGate=gate();await page.getByRole('button',{name:'Change Mode',exact:true}).click();await held.arrived;
   await page.locator('[data-system-page=settings]').click();await form().waitFor();await page.waitForLoadState('domcontentloaded');held.release();await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.equal(await dialog().count(),0);assert.equal(writes.length,0);
  });
  await check('out of order chooser reads keep the latest selection',async()=>{
   const held=readGate=gate();await page.getByRole('button',{name:'Change Mode',exact:true}).click();await held.arrived;external({generation_ui:false});
   await choose();held.release();await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.equal(await dialog().locator('input[value=operator]').isChecked(),true);await apply();assert.equal(writes.at(-1).data.version,2);
  });
  await check('navigation after committed apply loads the current version',async()=>{
   await choose();const held=profileGate=gate();await dialog().getByRole('button',{name:'Apply',exact:true}).click();await held.arrived;
   await page.goto(base+'/system?returned=1');await form().waitFor();held.release();await page.waitForLoadState('networkidle');assert.equal(state.shared_resources,true);assert.equal(await dialog().count(),0);await input('generation').uncheck();await saveAdvanced();assert.equal(writes.at(-1).data.version,2);
  });
  await check('pending advanced save keeps newer edits and locks chooser',async()=>{
   await input('generation').uncheck();const held=saveGate=gate();await save().click();await held.arrived;assert.equal(await page.getByRole('button',{name:'Change Mode',exact:true}).isDisabled(),true);await input('inlineReferences').uncheck();const response=page.waitForResponse(r=>r.url().endsWith('/v1/system/settings')&&r.request().method()==='PUT');held.release();await response;await save().filter({hasText:'Save',hasNotText:'Saved'}).waitFor();await saveAdvanced();assert.equal(writes.at(-1).data.version,2);assert.equal(state.allow_inline_references,false);
  });
  await page.evaluate(()=>sessionStorage.clear());await fresh({setup_pending:true});await dialog().waitFor();assert.equal(await dialog().getByRole('button',{name:'Skip for now',exact:true}).isVisible(),true);assert.equal(await dialog().getByRole('button',{name:'Apply',exact:true}).isVisible(),true);await dialog().getByRole('button',{name:'Skip for now',exact:true}).click();await dialog().waitFor({state:'hidden'});await choose('personal');assert.equal(await dialog().getByRole('button',{name:'Skip for now',exact:true}).isVisible(),false);assert.equal(await dialog().getByRole('button',{name:'Apply',exact:true}).isVisible(),false);await page.keyboard.press('Escape');
  await fresh();await input('generation').focus();await page.keyboard.press('Space');await choose();await apply();
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>GatewayTheme.apply(t),theme);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.locator('#settings-panel-system').screenshot({path:evidencePath('/tmp/setup-settings-'+engine+'-'+theme+'-'+width+'.png')});
  }
  assert.equal(await input('generation').isChecked(),false);assert.equal(await save().isVisible(),true);assert.deepEqual(errors,[]);assert.deepEqual(externalRequests,[]);
  fs.writeFileSync(evidencePath('/tmp/setup-settings-results.json'),JSON.stringify({engine,results,errors,externalRequests},null,2));console.log('PASS: '+results.length+' settings reconciliation cases; both themes and1440/390/320; keyboard; no external calls.');
 }catch(error){fs.writeFileSync(evidencePath('/tmp/setup-settings-failure.json'),JSON.stringify({engine,results,errors,failedRequests,error:String(error)},null,2));await page.screenshot({path:evidencePath('/tmp/setup-settings-failure.png'),fullPage:true});throw error;}
 finally{for(const held of gates)held.release();await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
