// Exact filter membership and rendered width, using disposable loopback fixtures.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {useFixtureKey}=require('./fixture-key.cjs');
const {evidencePath}=require('./evidence.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',output=evidencePath('/tmp/gateway-project-filter/'+engine+'/');fs.mkdirSync(output,{recursive:true});
const root=path.resolve(process.env.UI_SOURCE_DIR||path.join(__dirname,'../../web/public/ui'));
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};};
const projects=[{id:'11111111-0000-4000-8000-000000000001',name:'A'},{id:'22222222-0000-4000-8000-000000000002',name:'abcdefghijklmnopqrst'},{id:'33333333-0000-4000-8000-000000000003',name:'Проект для команды Ш'},{id:'44444444-0000-4000-8000-000000000004',name:'Long Latin '+ 'W'.repeat(65)},{id:'55555555-0000-4000-8000-000000000005',name:'Длинный проект '+ 'Ш'.repeat(65)}].map(p=>({...p,version:1}));
const task=(id,project_id='')=>({id,project_id,prompt:'Synthetic '+id,project_version:0,provider:'mock',provider_model:'video',model:'mock/video',media_type:'video',status:'completed',created_at:1700000000,updated_at:1700000001,options:{duration:5,ratio:'16:9',resolution:'720p'}});
let tasks=[...Array.from({length:60},(_,i)=>task('assigned-'+i,projects[i%2].id)),task('older-unassigned'),task('oldest-unassigned')],key='fixture-project-key',gate=null,readOnly=false;
const initial=structuredClone(tasks),reads=[],writes=[],external=[],errors=[],notices=[],cases=[],geometry=[];
const server=http.createServer((req,res)=>{const p=new URL(req.url,'http://fixture').pathname;if(p==='/away'){res.setHeader('Content-Type','text/html');return res.end('<title>Fixture away</title>');}const file=path.join(root,p==='/'?'index.html':path.basename(p));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const context=await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),page=await context.newPage();page.setDefaultTimeout(15000);
 page.on('pageerror',e=>{if(e.message==='ResizeObserver loop completed with undelivered notifications.')notices.push(e.message);else errors.push(e.message)});
 await page.route('**/*',async route=>{
  const req=route.request(),u=new URL(req.url()),p=u.pathname;if(u.origin!==base){external.push(req.url());return route.abort();}if(!p.startsWith('/v1/'))return route.continue();
  const json=(body,status=200)=>route.fulfill({json:body,status});
  if(req.method()!=='GET')writes.push({method:req.method(),path:p,body:req.postDataJSON()});
  if(p==='/v1/info')return json({provider:'mock',auth_mode:'google'});
  if(p==='/v1/me')return json({id:'fixture-user',session_id:'fixture-session',name:'Fixture',email:'fixture@example.test',is_system_admin:true});
  if(p==='/v1/organizations')return json({data:[]});
  if(p==='/v1/playground-key/restore')return json({error:{message:'No saved synthetic key'}},404);
  if(p==='/v1/key')return json({key:{name:'Fixture',resource_mode:'personal'}});
  if(p==='/v1/providers')return json({data:[{id:'mock',name:'Mock',default_model:'video',models:[{id:'video',display_name:'Test video',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5}]}]});
  if(p==='/v1/projects'){assert.equal(req.method(),'GET');return json({data:key==='fixture-project-key'?projects:[],can_manage_projects:!readOnly,scope:'company'});}
  if(p==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
  if(p==='/v1/generations'){
   if(req.method()==='POST'){const body=req.postDataJSON();assert.notEqual(body.project_id,'none');const created={...task('submitted',body.project_id),...body,status:'queued'};tasks.unshift(created);return json(created);}
   assert.equal(req.method(),'GET');const filter=u.searchParams.get('project_id')||'',offset=Number(u.searchParams.get('offset')||0),limit=Number(u.searchParams.get('limit')||50);
   reads.push({filter,offset,limit});const all=key==='fixture-project-key'?tasks.filter(t=>!filter||(filter==='none'?!t.project_id:t.project_id===filter)):[],data=structuredClone(all.slice(offset,offset+limit));
   const pending=gate;if(pending&&pending.filter===filter){pending.started.resolve();await pending.promise;}
   return json({data,has_more:all.length>offset+limit,next_offset:offset+data.length});
  }
  assert.equal(req.method(),'GET','Unexpected fixture write: '+p);return json({data:[]});
 });
 const record=name=>{cases.push(name);console.log('PASS:',name);};
 const waitIDs=ids=>page.waitForFunction(ids=>JSON.stringify([...document.querySelectorAll('#taskList .task')].map(n=>n.dataset.taskId))===JSON.stringify(ids),ids);
 const choose=async name=>{await page.keyboard.press('Escape');await page.getByRole('combobox',{name:'Project',exact:true}).click();await page.getByRole('option').filter({hasText:new RegExp('^'+name.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+'$')}).click();};
 const filter=()=>page.locator('#journalProject').inputValue();
 const current=()=>page.locator('#taskList .task').evaluateAll(nodes=>nodes.map(n=>n.dataset.taskId));
 const poll=async()=>{const r=await page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/generations'&&r.request().method()==='GET');await r.finished();await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));};
 try{
  await page.goto(base);await page.locator('#managedGenerationHelp').waitFor({state:'visible'});
  const defaultWidth=(await page.getByRole('combobox',{name:'Project',exact:true}).boundingBox()).width;
  await useFixtureKey(page,key);await page.waitForFunction(()=>selectedModel()?.id==='video');await waitIDs(tasks.slice(0,50).map(t=>t.id));
  const mutations=()=>writes.filter(w=>!['/v1/generations/estimate','/v1/playground-key/restore'].includes(w.path)).length;const writesBefore=mutations();await page.locator('#prompt').fill('Retained draft');
  const draft=await page.locator('#taskForm').evaluate(f=>[...f.querySelectorAll('input:not([type=file]),textarea,select')].map(n=>[n.id,n.value]));
  await choose('No Projects');await waitIDs(['older-unassigned','oldest-unassigned']);assert.equal(await page.locator('#manageProject').isVisible(),false);assert.equal(await page.locator('#journalProject option[value="new-project-action"]').evaluate(n=>n.disabled),false);
  assert.equal(await page.evaluate(()=>GatewayProjects.selected()),'');assert.equal(await page.evaluate(()=>GatewayProjects.filter()),'none');
  await choose(projects[0].name);await waitIDs(tasks.filter(t=>t.project_id===projects[0].id).map(t=>t.id));await choose(projects[1].name);await waitIDs(tasks.filter(t=>t.project_id===projects[1].id).map(t=>t.id));await choose('All Projects');await waitIDs(tasks.slice(0,50).map(t=>t.id));
  assert.deepEqual(tasks,initial);assert.equal(mutations(),writesBefore);assert.deepEqual(await page.locator('#taskForm').evaluate(f=>[...f.querySelectorAll('input:not([type=file]),textarea,select')].map(n=>[n.id,n.value])),draft);
  record('Exact All/two-project/No Projects membership; older unassigned outside loaded All page; selecting filters writes nothing and preserves form');
  await choose('No Projects');await waitIDs(['older-unassigned','oldest-unassigned']);await page.reload();await waitIDs(['older-unassigned','oldest-unassigned']);assert.equal(await filter(),'none');await page.goto(base+'/away');await page.goBack();await waitIDs(['older-unassigned','oldest-unassigned']);assert.equal(await filter(),'none');
  record('No Projects is distinct from All and concrete project IDs after reload and browser Back');
  const beforePoll=reads.length;tasks.push(task('poll-arrival'));await waitIDs(['older-unassigned','oldest-unassigned','poll-arrival']);assert.ok(reads.length>beforePoll);assert.ok(reads.slice(beforePoll).every(r=>r.filter==='none'&&r.offset===0));
  tasks=tasks.filter(t=>t.project_id);await page.locator('#taskList .empty-state').waitFor();assert.match(await page.locator('#taskList').innerText(),/without a project/);await poll();assert.equal(await filter(),'none');assert.equal(await current().then(ids=>ids.length),0);tasks=structuredClone(initial);await waitIDs(['older-unassigned','oldest-unassigned']);
  record('Unmodified natural 2000ms polling retains active filter and handles arrivals and empty No Projects results');
  gate={...deferred(),started:deferred(),filter:'none'};await gate.started.promise;await choose(projects[0].name);await waitIDs(tasks.filter(t=>t.project_id===projects[0].id).map(t=>t.id));gate.resolve();gate=null;await poll();assert.ok((await current()).every(id=>id.startsWith('assigned-')));
  await choose('No Projects');await waitIDs(['older-unassigned','oldest-unassigned']);gate={...deferred(),started:deferred(),filter:'none'};await gate.started.promise;await choose('All Projects');await waitIDs(tasks.slice(0,50).map(t=>t.id));await choose('No Projects');gate.resolve();gate=null;await waitIDs(['older-unassigned','oldest-unassigned']);
  record('Late No Projects responses cannot mix into concrete filters or an All→No Projects return');
  await page.locator('#prompt').fill('No Projects submission');await page.locator('#submitButton').click();await page.waitForFunction(()=>document.querySelector('#confirmDialog').open&&!document.querySelector('#confirmSubmit').disabled);await page.locator('#confirmSubmit').click();await page.locator('#confirmDialog').waitFor({state:'hidden'});assert.equal(writes.filter(w=>w.path==='/v1/generations').at(-1).body.project_id,undefined);await waitIDs(['submitted','older-unassigned','oldest-unassigned']);
  record('No Projects keeps ordinary unassigned submission semantics; sentinel never enters assignment payload');
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   const sizes=[];
   for(const name of ['All Projects','No Projects',...projects.map(p=>p.name)]){
    await choose(name);const trigger=page.getByRole('combobox',{name:'Project',exact:true}),box=await trigger.boundingBox();sizes.push(box.width);
    assert.equal(await trigger.getAttribute('aria-description'),name);assert.equal(await trigger.getAttribute('title'),name);
    const layout=await page.evaluate(()=>{const c=document.querySelector('.journal-project-controls'),f=c.querySelector('.select-trigger'),a=document.querySelector('#manageProject'),v=f.querySelector('.select-value');return {overflow:document.documentElement.scrollWidth>innerWidth,field:f.getBoundingClientRect().toJSON(),edit:a.getClientRects().length?a.getBoundingClientRect().toJSON():null,nested:f.querySelectorAll('button').length,ellipsis:getComputedStyle(v).textOverflow,whiteSpace:getComputedStyle(v).whiteSpace,fullText:v.textContent};});
    assert.equal(layout.overflow,false);assert.equal(layout.ellipsis,'ellipsis');assert.equal(layout.whiteSpace,'nowrap');assert.equal(layout.fullText,name);assert.equal(layout.nested,0);if(layout.edit){assert.ok(layout.edit.x>=layout.field.x&&layout.edit.right<=layout.field.right,JSON.stringify({theme,width,name,layout}));assert.ok(Math.abs(layout.edit.y+layout.edit.height/2-layout.field.y-layout.field.height/2)<1);}
   }
   assert.ok(sizes.every(s=>Math.abs(s-sizes[0])<0.1),JSON.stringify({theme,width,sizes}));if(width===1440){assert.equal(sizes[0],240);assert.equal(sizes[0],defaultWidth);}
   geometry.push({theme,width,sizes});await page.screenshot({path:path.join(output,`${theme}-${width}-long-label.png`)});
   await choose('No Projects');const trigger=page.getByRole('combobox',{name:'Project',exact:true});await trigger.focus();await trigger.press('ArrowDown');await page.keyboard.press('Home');await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter');assert.equal(await filter(),'none');await trigger.press('ArrowDown');await page.keyboard.press('Escape');assert.equal(await trigger.evaluate(n=>n===document.activeElement),true);await page.screenshot({path:path.join(output,`${theme}-${width}-no-project.png`)});
  }
  record('Identical rendered width for default/short/20-character/long Latin/Cyrillic labels; full accessible labels; keyboard/Escape, no overlap or overflow in both themes at 1440/390/320px');
  readOnly=true;await page.evaluate(()=>GatewayProjects.load());assert.equal(await page.locator('#journalProject option[value="new-project-action"]').evaluate(n=>n.disabled),true);assert.equal(await page.getByRole('combobox',{name:'Project',exact:true}).isEnabled(),true);await choose('No Projects');await waitIDs(['submitted','older-unassigned','oldest-unassigned']);
  key='other-fixture-key';await page.evaluate(()=>{GenerationKey.value=()=> 'other-fixture-key';window.dispatchEvent(new Event('gateway-key-change'));});await page.waitForFunction(()=>document.querySelector('#journalProject').options.length===3);assert.equal(await filter(),'');assert.equal(await current().then(ids=>ids.length),0);
  key='fixture-project-key';await page.evaluate(()=>{GenerationKey.value=()=> 'fixture-project-key';window.dispatchEvent(new Event('gateway-key-change'));});await page.waitForFunction(()=>document.querySelector('#journalProject').options.length>3);assert.equal(await filter(),'');
  await choose(projects[0].name);const saved=projects.shift();await page.evaluate(()=>GatewayProjects.load());await page.waitForFunction(()=>document.querySelector('#journalProject').value==='');projects.unshift(saved);
  record('Read-only users retain filtering; key/context changes reset No Projects; inaccessible/deleted concrete project resets to All');
  const catalog=projects.splice(0);await page.evaluate(()=>GatewayProjects.load());await choose('No Projects');await page.reload();await waitIDs(['submitted','older-unassigned','oldest-unassigned']);assert.equal(await filter(),'none');assert.equal(await page.getByRole('combobox',{name:'Project',exact:true}).innerText(),'No Projects');await page.goto(base+'/away');await page.goBack();await waitIDs(['submitted','older-unassigned','oldest-unassigned']);assert.equal(await filter(),'none');assert.equal(await page.getByRole('combobox',{name:'Project',exact:true}).innerText(),'No Projects');projects.push(...catalog);
  record('Empty project catalog restores both rendered No Projects selector and unassigned Journal after reload and Back');
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,browserVersion:browser.version(),cases,geometry,reads,writes,errors,notices,external},null,2));
 }catch(error){await page.screenshot({path:path.join(output,'failure.png')});fs.writeFileSync(path.join(output,'failure.json'),JSON.stringify({error:String(error),cases,geometry,reads,writes,errors},null,2));throw error;}finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
