const {useFixtureKey}=require('./fixture-key.cjs');
// Natural journal polling, synthetic APIs only; never pause application timers.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {evidencePath}=require('./evidence.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',output=evidencePath('/tmp/gateway-project-polling/'+engine+'/');fs.mkdirSync(output,{recursive:true});
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};};
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const file=path.join(root,req.url==='/'?'index.html':path.basename(req.url.split('?')[0]));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const context=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),page=await context.newPage();page.setDefaultTimeout(15000);
 const errors=[],external=[],patches=[],snapshots=[],cases=[];let reads=0,key='fixture-generation-key',projectFailure=false,conflictNext=false,projectGate;
 const projects=[{id:'alpha',name:'Alpha project',version:1},{id:'beta',name:'Beta project',version:1}];
 const generation=id=>({id,prompt:'Synthetic '+id,project_version:0,provider:'mock',provider_model:'video',model:'mock/video',media_type:'video',status:'completed',created_at:1700000000,updated_at:1700000001,can_change_project:true,options:{duration:5,resolution:'720p',ratio:'16:9'}});
 let target=generation('target'),other=generation('other'),tasks=[target,other];
 page.on('pageerror',e=>{if(e.message!=='ResizeObserver loop completed with undelivered notifications.')errors.push(e.message);});
 await page.route('**/*',async route=>{
  const req=route.request(),url=new URL(req.url());if(url.hostname!=='127.0.0.1'){external.push(req.url());return route.abort();}if(!url.pathname.startsWith('/v1/'))return route.continue();
  const json=(body,status=200)=>route.fulfill({status,json:body});
  if(url.pathname==='/v1/info')return json({provider:'mock',auth_mode:'google'});
  if(url.pathname==='/v1/me')return json({id:'preview',session_id:'preview',name:'Preview',email:'preview@example.test',is_system_admin:true});
  if(url.pathname==='/v1/playground-key/restore')return json({error:{message:'No saved fixture key'}},404);
  if(url.pathname==='/v1/key')return json({key:{name:'Fixture key',resource_mode:'personal'}});
  if(url.pathname==='/v1/providers')return json({data:[{id:'mock',name:'Mock',default_model:'video',models:[{id:'video',media_type:'video',display_name:'Synthetic video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5}]}]});
  if(url.pathname==='/v1/projects'){assert.equal(req.method(),'GET');if(projectFailure){const gate=projectGate;if(gate){gate.started.resolve();await gate.promise;}return json({error:{message:'Project list interrupted'}},503);}return json({data:key==='fixture-generation-key'?projects:[]});}
  if(url.pathname==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
  if(url.pathname==='/v1/generations'){assert.equal(req.method(),'GET','No generation submission in polling regression');reads++;return json({data:key==='fixture-generation-key'?tasks:[]});}
  if(url.pathname==='/v1/generations/target')return json(target);
  if(url.pathname==='/v1/generations/target/project'){
   assert.equal(req.method(),'PATCH');const body=req.postDataJSON();
   if(conflictNext){conflictNext=false;target.project_id='alpha';target.project_version++;target.updated_at++;}
   const status=body.version===target.project_version?200:409;patches.push({body,status});
   if(status===409)return json({error:{message:'Assignment changed'}},409);
   target.project_id=body.project_id;target.project_version++;target.updated_at++;return json({project_id:target.project_id,version:target.project_version});
  }
  assert.equal(req.method(),'GET','Unexpected synthetic mutation: '+url.pathname);return json({data:[]});
 });
 const card=()=>page.locator('[data-task-id="target"]'),menu=()=>page.getByRole('menu',{name:'Generation project',exact:true});
 const open=async(wait=true)=>{const c=card();if(!await c.locator('.task-menu').evaluate(n=>n.open))await c.locator('summary[aria-label="Task actions"]').press('Enter');await c.getByRole('button',{name:'Project',exact:true}).press('ArrowRight');if(wait)await menu().getByRole('menuitemcheckbox').first().waitFor();else await menu().waitFor();};
 const close=async()=>{await page.keyboard.press('Escape');await page.keyboard.press('Escape');};
 const nextPoll=async()=>{const r=await page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/generations'&&r.request().method()==='GET');await r.finished();await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));};
 const fingerprint=async(field,value)=>page.waitForFunction(({field,value})=>{const c=document.querySelector('[data-task-id="target"]');return c&&JSON.parse(c.taskFingerprint)[field]===value;},{field,value});
 const snapshot=async label=>{const data=await page.evaluate(label=>({label,at:Date.now(),focused:document.activeElement.tagName,focusInMenu:!!document.activeElement.closest('[role="menu"]'),anchorConnected:document.querySelector('[data-task-id="target"] .change-project')?.isConnected,menuVisible:!!document.querySelector('[role="menu"][aria-label="Generation project"]'),message:document.querySelector('[role="menu"] [role="status"]')?.textContent}),label);snapshots.push(data);return data;};
 const record=text=>{cases.push(text);console.log('PASS:',text);};
 try{
  await page.goto('http://127.0.0.1:'+server.address().port);await useFixtureKey(page,key);await card().waitFor();await open();
  await page.evaluate(()=>{window.originalCard=document.querySelector('[data-task-id="target"]');window.originalMenu=document.querySelector('[role="menu"][aria-label="Generation project"]');});
  await nextPoll();assert.equal(await page.evaluate(()=>originalCard.isConnected&&originalMenu.isConnected),true);record('Unchanged natural poll preserves target card and submenu');
  other.updated_at++;await page.waitForFunction(v=>JSON.parse(document.querySelector('[data-task-id="other"]').taskFingerprint).updated_at===v,other.updated_at);assert.equal(await page.evaluate(()=>originalCard.isConnected&&originalMenu.isConnected),true);record('Unrelated natural update preserves active target interaction');
  await menu().getByRole('menuitemcheckbox',{name:'Alpha project',exact:true}).focus();target.project_id='beta';target.project_version=1;await fingerprint('project_version',1);
  assert.equal(await page.evaluate(()=>originalCard.isConnected),true);assert.equal(await page.evaluate(()=>originalMenu.isConnected),true);assert.equal(await menu().getByRole('menuitemcheckbox',{name:'Beta project',exact:true}).getAttribute('aria-checked'),'true');assert.equal(await menu().getByRole('menuitemcheckbox',{name:'Alpha project',exact:true}).evaluate(n=>n===document.activeElement),true);await snapshot('target-refreshed');record('Project-only update retains the same card and submenu, updates confirmed checkmark and preserves item focus');
  await card().getByRole('button',{name:'Project',exact:true}).focus();target.status='in_progress';target.updated_at++;await fingerprint('status','in_progress');assert.equal(await card().getByRole('button',{name:'Project',exact:true}).evaluate(n=>n===document.activeElement),true);await menu().waitFor();await close();await nextPoll();assert.equal(await menu().count(),0);record('Refreshed anchor focus survives status update; explicit dismissal remains closed');
  await open();conflictNext=true;await menu().getByRole('menuitemcheckbox',{name:'Alpha project',exact:true}).click();await menu().getByText(/Current project: Alpha project/).waitFor();await page.screenshot({path:path.join(output,'conflict-before-poll.png')});await snapshot('conflict-before-poll');
  await fingerprint('project_version',2);await nextPoll();await menu().getByText(/Current project: Alpha project/).waitFor();assert.equal(await menu().getByRole('menuitemcheckbox',{name:'Alpha project',exact:true}).getAttribute('aria-checked'),'true');assert.equal((await snapshot('conflict-after-poll')).focusInMenu,true);assert.equal(patches.length,1);assert.equal(patches[0].status,409);await page.screenshot({path:path.join(output,'conflict-after-poll.png')});
  await menu().getByRole('menuitemcheckbox',{name:'Beta project',exact:true}).click();await menu().waitFor({state:'hidden'});await fingerprint('project_version',3);assert.equal(patches[1].body.version,2);assert.equal(target.project_id,'beta');record('409 recovery and focus survive natural polling; only an explicit second choice writes the confirmed version');
  projectFailure=true;projectGate={...deferred(),started:deferred()};await open(false);await projectGate.started.promise;await nextPoll();projectGate.resolve();projectGate=null;await menu().getByText(/Could not load projects/).waitFor();const count=patches.length;await page.waitForFunction(()=>!document.querySelector('#projectMessage').hidden&&document.querySelector('#projectMessage').textContent.includes('Project list interrupted'));assert.equal(await menu().isVisible(),true);assert.equal(patches.length,count);await snapshot('background-503');await close();projectFailure=false;await open();await menu().getByRole('menuitemcheckbox').first().waitFor();record('Independent background 503 remains visible without dismissing local failure/recovery or mutating a project');
  target.can_change_project=false;target.updated_at++;await fingerprint('can_change_project',false);await menu().waitFor({state:'hidden'});assert.equal(await card().locator('.change-project').isEnabled(),false);record('Permission loss closes assignment instead of preserving unauthorized interaction');
  target.can_change_project=true;target.updated_at++;await fingerprint('can_change_project',true);await open();tasks=[other];await card().waitFor({state:'detached'});await menu().waitFor({state:'hidden'});tasks=[target,other];await card().waitFor();assert.equal(await menu().count(),0);record('Task removal closes the submenu; reappearing task does not resurrect it');
  await open();key='different-fixture-key';await page.evaluate(()=>{window.GenerationKey.value=()=> 'different-fixture-key';window.dispatchEvent(new Event('gateway-key-change'));});await menu().waitFor({state:'hidden'});await page.waitForFunction(()=>document.querySelector('#journalProject').options.length===2);record('Key/context change closes rebound interaction and clears previous names');
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);assert.equal(patches.length,2);assert.ok(reads>=8);fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,polling:'Unmodified application 2000ms timer; no explicit loadTasks calls or interval replacement',cases,snapshots,patches,reads,errors,external},null,2));
 }catch(error){await page.screenshot({path:path.join(output,'failure.png')});fs.writeFileSync(path.join(output,'failure.json'),JSON.stringify({error:String(error),cases,snapshots,patches,reads},null,2));throw error;}finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
