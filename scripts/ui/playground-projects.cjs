const {generationPage}=require('./generation-page-fixture.cjs');
const {useFixtureKey}=require('./fixture-key.cjs');
// Projects + repeat-generation integration, entirely synthetic localhost APIs.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {evidencePath}=require('./evidence.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',output=evidencePath('/tmp/gateway-playground-projects/'+engine+'/');fs.mkdirSync(output,{recursive:true});
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const file=path.join(root,req.url==='/'?'index.html':path.basename(req.url.split('?')[0]));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};};
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const context=await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce',hasTouch:true}),page=await context.newPage();page.setDefaultTimeout(15000);
 const errors=[],notices=[],external=[],posts=[],results=[],reads=[];let manageAllowed=true;let projects=[],tasks=[],listGate,key='fixture-generation-key',createGate,createFailure=false,readGate,readFailure=false,postGate,assignmentFailure=false,projectReadFailure=false,assignmentGate,assignmentConflict=false;
 const model={id:'video',display_name:'Test Video',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5};
 const historical=(id,prompt,project_id)=>({id,prompt,project_id,project_version:0,provider:'mock',provider_model:'video',model:'mock/video',media_type:'video',status:'completed',created_at:1700000000,updated_at:1700000001,options:{duration:5,ratio:'16:9',resolution:'720p'}});
 tasks=[historical('old','Older ungrouped')];
 page.on('pageerror',e=>{if(engine==='webkit'&&e.message==='ResizeObserver loop completed with undelivered notifications.')notices.push(e.message);else errors.push(e.message);});
 await page.route('**/*',async route=>{
  const req=route.request(),url=new URL(req.url());if(url.hostname!=='127.0.0.1'){external.push(req.url());return route.abort();}if(!url.pathname.startsWith('/v1/'))return route.continue();
  const json=body=>route.fulfill({json:url.pathname==='/v1/generations'&&req.method()==='GET'&&body.data?generationPage(url,body.data):body}),auth=req.headers().authorization;
  if(url.pathname==='/v1/playground-key/restore')return route.fulfill({status:404,json:{error:{message:'No saved fixture key'}}});
  if(url.pathname==='/v1/info')return json({provider:'mock',auth_mode:'google'});
  if(url.pathname==='/v1/me')return json({id:'preview',session_id:'preview',name:'Preview',email:'preview@example.test',is_system_admin:true});
  if(url.pathname==='/v1/organizations')return json({data:[]});
  if(url.pathname==='/v1/api-keys'&&req.method()==='POST')return json({secret:key});
  if(url.pathname==='/v1/key')return json({key:{name:'Fixture key',resource_mode:'personal'}});
  if(url.pathname==='/v1/providers')return json({data:[{id:'mock',name:'Mock',default_model:'video',models:[model]}]});
  if(url.pathname==='/v1/projects'){
   const requestKey=auth?.replace(/^Bearer /,'');assert.ok(['fixture-generation-key','other-key'].includes(requestKey));
   if(req.method()==='GET'){if(projectReadFailure)return route.fulfill({status:503,json:{error:{message:'Project list interrupted'}}});const snapshot=requestKey==='fixture-generation-key'?[...projects]:[];if(listGate){listGate.started.resolve();await listGate.promise;}return json({data:snapshot,can_manage_projects:manageAllowed,scope:"company"});}
   const name=req.postDataJSON().name;
   if(createGate)await createGate.promise;
   if(createFailure)return route.fulfill({status:503,json:{error:{message:'Save interrupted'}}});
   let p=projects.find(p=>p.name===name);if(!p){p={id:'project-'+(projects.length+1),name,created_at:'2026-10-24T00:00:00Z',version:1};projects.push(p);}return json(p);
  }
  if(url.pathname.startsWith('/v1/projects/')){
   const id=url.pathname.split('/').at(-1),body=req.postDataJSON(),project=projects.find(p=>p.id===id);
   if(!project)return route.fulfill({status:400,json:{error:{message:'Project unavailable'}}});
   if(body.version!==project.version)return route.fulfill({status:409,json:{error:{message:'Project changed'}}});
   if(req.method()==='DELETE'){projects=projects.filter(p=>p.id!==id);tasks=tasks.map(t=>t.project_id===id?{...t,project_id:'',project_version:(t.project_version||0)+1}:t);return route.fulfill({status:204});}
   if(projects.some(p=>p.id!==id&&p.name.toLowerCase()===body.name.toLowerCase()))return route.fulfill({status:409,json:{error:{message:'A project with this name already exists'}}});
   const value={...project,name:body.name,version:project.version+1};projects=projects.map(p=>p.id===id?value:p);return json(value);
  }
  if(/^\/v1\/generations\/[^/]+\/project$/.test(url.pathname)){
   if(assignmentGate){assignmentGate.started.resolve();await assignmentGate.promise;}if(assignmentFailure)return route.fulfill({status:503,json:{error:{message:'Assignment interrupted'}}});const id=url.pathname.split('/')[3],body=req.postDataJSON(),task=tasks.find(t=>t.id===id);
   if(!task)return route.fulfill({status:404,json:{error:{message:'Generation not found'}}});if(assignmentConflict){assignmentConflict=false;task.project_id='project-2';task.project_version++;}
   if(body.version!==(task.project_version||0))return route.fulfill({status:409,json:{error:{message:'Assignment changed'}}});
   if(body.project_id&&!projects.some(p=>p.id===body.project_id))return route.fulfill({status:400,json:{error:{message:'Project unavailable'}}});
   task.project_id=body.project_id||'';task.project_version=(task.project_version||0)+1;return json({project_id:task.project_id,version:task.project_version});
  }
  if(/^\/v1\/generations\/[^/]+$/.test(url.pathname)&&req.method()==='GET')return json(tasks.find(t=>t.id===url.pathname.split('/').at(-1))||{});
  if(url.pathname==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
  if(url.pathname==='/v1/generations'&&req.method()==='GET'){
   const project=url.searchParams.get('project_id');reads.push(project||'all');const snapshot=key==='fixture-generation-key'?tasks.filter(t=>!project||(project==='none'?!t.project_id:t.project_id===project)):[];
   const gate=readGate;if(gate&&project===gate.project)await gate.promise;
   if(readFailure)return route.fulfill({status:503,json:{error:{message:'History interrupted'}}});
   return json({data:snapshot,can_manage_projects:manageAllowed,scope:"company"});
  }
  if(url.pathname==='/v1/generations'&&req.method()==='POST'){
   posts.push({body:req.postDataJSON(),key:req.headers()['idempotency-key']});if(postGate)await postGate.promise;
   const body=posts.at(-1).body,task={...historical('new-'+posts.length,body.prompt,body.project_id),...body,status:'queued',created_at:Math.floor(Date.now()/1000)};tasks.unshift(task);return json(task);
  }
  return json({data:[]});
 });
 const record=name=>{results.push(name);console.log('PASS:',name);};
 const choose=async name=>{await page.keyboard.press('Escape');if(name==='Create project…'){await page.getByRole('combobox',{name:'Project',exact:true}).click();return page.getByRole('option',{name:'New Project…',exact:true}).click();}if(name==='Rename project…'||name==='Delete project…'){await page.getByRole('button',{name:'Edit project',exact:true}).click();return page.getByRole('menuitem',{name:name.startsWith('Rename')?'Rename':'Delete',exact:true}).click();}await page.getByRole('combobox',{name:'Project',exact:true}).click();await page.getByRole('option').filter({hasText:new RegExp('^'+name.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+'$')}).click();};
 const create=async name=>{await choose('Create project…');await page.getByRole('dialog',{name:'Create project',exact:true}).getByRole('textbox',{name:'Project name'}).fill(name);assert.ok(await page.getByRole('dialog',{name:'Create project',exact:true}).getByRole('button',{name:'Create',exact:true}).evaluate(n=>n.classList.contains('primary')));await page.getByRole('dialog',{name:'Create project',exact:true}).getByRole('button',{name:'Create',exact:true}).click();};
 const ready=()=>page.waitForFunction(()=>document.querySelector('#confirmDialog').open&&!document.querySelector('#confirmSubmit').disabled);
 const hidden=()=>page.locator('#confirmDialog').waitFor({state:'hidden'});
 const state=()=>page.locator('#taskForm').evaluate(form=>[...form.querySelectorAll('input:not([type=file]),textarea,select')].map(x=>({id:x.id,value:x.value,checked:x.checked,disabled:x.disabled})));
 const ids=()=>page.locator('#taskList .task').evaluateAll(nodes=>nodes.map(n=>n.dataset.taskId));
 const waitIDs=expected=>page.waitForFunction(expected=>JSON.stringify([...document.querySelectorAll('#taskList .task')].map(n=>n.dataset.taskId))===JSON.stringify(expected),expected);
 const selectDuringFlight=id=>page.evaluate(id=>{const select=document.querySelector('#journalProject');select.value=id;select.dispatchEvent(new Event('change'));},id);
 try{
  await page.goto('http://127.0.0.1:'+server.address().port);await page.locator('#managedGenerationHelp').waitFor({state:'visible'});await useFixtureKey(page,key);await page.waitForFunction(()=>selectedModel()?.id==='video');await waitIDs(['old']);await page.evaluate(()=>{window.projectMenuTrace=[];for(const type of ['toggle','pointerdown','click','keydown'])document.addEventListener(type,e=>{window.projectMenuTrace.push({type,target:e.target.className||e.target.tagName,open:e.target.open,newState:e.newState,key:e.key});if(window.projectMenuTrace.length>40)window.projectMenuTrace.shift();},true);});
  tasks=['queued','in_progress','failed','completed'].map(status=>({...historical('state-'+status,status),status,can_change_project:true}));tasks.push({...historical('foreign','Foreign generation'),can_change_project:false},{...historical('redacted','Hidden content'),content_access:false});await page.evaluate(()=>loadTasks());await waitIDs(tasks.map(t=>t.id));
  for(const status of ['queued','in_progress','failed','completed'])assert.equal(await page.locator('[data-task-id="state-'+status+'"] .change-project').isEnabled(),true);
  for(const id of ['foreign','redacted']){const item=page.locator('[data-task-id="'+id+'"]');await item.locator('summary[aria-label="Task actions"]').click();assert.equal(await item.getByRole('button',{name:'Project',exact:true}).isVisible(),true);assert.equal(await item.getByRole('button',{name:'Project',exact:true}).isEnabled(),false);assert.match(await item.locator('.change-project').getAttribute('title'),/workspace or role/);await page.keyboard.press('Escape');}
  tasks=[historical('old','Older ungrouped')];await page.evaluate(()=>loadTasks());await waitIDs(['old']);
  record('Project action visible for every status; restricted/redacted actions disabled without leaking project names');
  await choose('Create project…');await page.keyboard.press('Escape');assert.equal(projects.length,0);
  await choose('Create project…');await page.getByRole('dialog',{name:'Create project',exact:true}).getByRole('button',{name:'Cancel',exact:true}).click();assert.equal(projects.length,0);
  await create('   ');assert.equal(projects.length,0);assert.equal(await page.getByRole('dialog',{name:'Create project',exact:true}).isVisible(),true);await page.keyboard.press('Escape');
  assert.equal(await page.locator('#manageProject').isVisible(),false);assert.deepEqual(await page.locator('#journalProject option').allTextContents(),['All Projects','No Projects','New Project…']);
  record('Filter contains only project choices; New Project action creates; All hides Edit; Cancel/Escape create nothing');
  await create('Film A');await page.getByRole('dialog',{name:'Create project',exact:true}).waitFor({state:'hidden'});await page.locator('#taskList .empty-state').waitFor();assert.match(await page.locator('#taskList').innerText(),/in this project/);assert.equal(await page.locator('#journalProject').inputValue(),'project-1');
  await create('Film B');await page.getByRole('dialog',{name:'Create project',exact:true}).waitFor({state:'hidden'});tasks.unshift(historical('a','A generation','project-1'),historical('b','B generation','project-2'));
  await choose('Film A');await waitIDs(['a']);await choose('Film B');await waitIDs(['b']);await choose('All Projects');await waitIDs(['a','b','old']);
  record('Create selects empty project; All and project filters preserve older ungrouped generations');
  await choose('Film A');await waitIDs(['a']);await page.reload();await page.waitForFunction(()=>window.GenerationKey?.value());await waitIDs(['a']);assert.equal(await page.locator('#journalProject').inputValue(),'project-1');
  record('Projects and selected filter survive reload with key restoration');
  await page.locator('#prompt').fill('Retained project draft');const draft=await state();
  postGate=deferred();await page.locator('#submitButton').click();await ready();await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>document.querySelector('#confirmDialog').getAttribute('aria-busy')==='true');
  await selectDuringFlight('project-2');postGate.resolve();postGate=null;await hidden();await waitIDs(['b']);assert.equal(posts.at(-1).body.project_id,'project-1');assert.deepEqual(await state(),draft);assert.ok(!(await ids()).includes('new-1'));
  record('Form snapshot captures project at submission; filter change during acceptance cannot reassign or display it in another project; draft retained');
  await choose('All Projects');await waitIDs(['new-1','a','b','old']);
  const card=page.locator('[data-task-id="a"]');await card.locator('.task-menu summary').click();await card.locator('.retry-task').click();await ready();await page.locator('#confirmSubmit').click();await hidden();assert.equal(posts.at(-1).body.project_id,'project-1');assert.deepEqual(await state(),draft);
  await page.locator('#submitButton').click();await ready();await page.locator('#confirmSubmit').click();await hidden();assert.equal(posts.at(-1).body.project_id,undefined);assert.deepEqual(await state(),draft);
  record('Try again from All retains historical project; ordinary generation from All remains ungrouped; both preserve draft');
  createFailure=true;await create('Retry project');await page.getByText('Save interrupted',{exact:true}).waitFor();assert.equal(await page.getByRole('dialog',{name:'Create project',exact:true}).getByRole('textbox',{name:'Project name'}).inputValue(),'Retry project');createFailure=false;await page.getByRole('dialog',{name:'Create project',exact:true}).getByRole('button',{name:'Create',exact:true}).click();await page.getByRole('dialog',{name:'Create project',exact:true}).waitFor({state:'hidden'});
  record('Interrupted project save preserves name and supports retry');
  listGate={...deferred(),started:deferred()};await page.evaluate(()=>{GatewayProjects.load();});await listGate.started.promise;
  await create('Created after list snapshot');await page.getByRole('dialog',{name:'Create project',exact:true}).waitFor({state:'hidden'});const createdID=await page.locator('#journalProject').inputValue();const pendingList=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/projects'&&r.request().method()==='GET');listGate.resolve();listGate=null;await pendingList;await page.evaluate(()=>GatewayProjects.load());assert.equal(await page.locator('#journalProject').inputValue(),createdID);
  record('A stale project list cannot remove a newly created project or its selection');
  readGate={...deferred(),project:'project-1'};await choose('Film A');await page.waitForFunction(()=>document.querySelector('#taskList .empty-state'));await choose('Film B');await waitIDs(['b']);readGate.resolve();readGate=null;await page.evaluate(()=>loadTasks());await waitIDs(['b']);
  record('Late history response cannot replace a newer selected project');
  readFailure=true;await page.evaluate(()=>loadTasks(true));assert.match(await page.locator('#formMessage').innerText(),/interrupted/i);assert.deepEqual(await ids(),['b']);readFailure=false;await page.evaluate(()=>loadTasks());await waitIDs(['b']);
  record('Failed background history read preserves existing cards and recovers');
  const projectDialog=()=>page.getByRole('dialog',{name:'Rename project',exact:true});
  await choose('Film A');await choose('Rename project…');await projectDialog().getByRole('textbox',{name:'Project name'}).fill('Film A renamed');await projectDialog().getByRole('button',{name:'Save',exact:true}).click();await projectDialog().waitFor({state:'hidden'});assert.equal(projects.find(p=>p.id==='project-1').name,'Film A renamed');assert.equal(await page.locator('#journalProject').inputValue(),'project-1');
  record('Rename preserves project identity, selected filter and generation membership');
  await choose('Rename project…');await projectDialog().getByRole('textbox',{name:'Project name'}).fill('Film B');await projectDialog().getByRole('button',{name:'Save',exact:true}).click();await projectDialog().getByText(/already exists/).waitFor();assert.equal(await projectDialog().getByRole('textbox',{name:'Project name'}).inputValue(),'Film B');await page.keyboard.press('Escape');
  await choose('Rename project…');await projectDialog().getByRole('textbox',{name:'Project name'}).fill('My retained draft');projects=projects.map(p=>p.id==='project-1'?{...p,name:'Other tab name',version:p.version+1}:p);await projectDialog().getByRole('button',{name:'Save',exact:true}).click();await projectDialog().getByText(/Current name: Other tab name/).waitFor();assert.equal(projects.find(p=>p.id==='project-1').name,'Other tab name');assert.equal(await projectDialog().getByRole('textbox',{name:'Project name'}).inputValue(),'My retained draft');await projectDialog().getByRole('button',{name:'Save',exact:true}).click();await projectDialog().waitFor({state:'hidden'});
  record('Duplicate and concurrent rename keep drafts and require explicit review/retry');
  await choose('All Projects');await page.waitForFunction(()=>document.querySelector('[data-task-id="old"]'));
  const assignMenu=()=>page.getByRole('menu',{name:'Generation project',exact:true});
  const changeOld=async(wait=true)=>{await page.keyboard.press('Escape');const card=page.locator('[data-task-id="old"]');if(!await card.locator('.task-menu').evaluate(n=>n.open))await card.locator('.task-menu summary').click();await card.getByRole('button',{name:'Project',exact:true}).click();if(wait)await assignMenu().getByRole('menuitemcheckbox').first().waitFor();};
  const assignment=async name=>{await assignMenu().getByRole('menuitemcheckbox',{name,exact:true}).click();};
  await changeOld();await assignment('Film B');await assignMenu().waitFor({state:'hidden'});assert.equal(tasks.find(t=>t.id==='old').project_id,'project-2');assert.equal(tasks.find(t=>t.id==='old').prompt,'Older ungrouped');
  await choose('Film B');await page.locator('[data-task-id="old"]').waitFor();await changeOld();assert.equal(await assignMenu().getByRole('menuitemcheckbox',{name:'Film B',exact:true}).getAttribute('aria-checked'),'true');await assignment('Film B');await assignMenu().waitFor({state:'hidden'});await page.locator('[data-task-id="old"]').waitFor({state:'detached'});await choose('All Projects');await page.locator('[data-task-id="old"]').waitFor();
  record('Project submenu assigns and toggles the checked project off, preserving history and prompt');
  await changeOld();const oldTask=tasks.find(t=>t.id==='old');assignmentConflict=true;await assignment('My retained draft');await assignMenu().getByText(/Current project: Film B/).waitFor();assert.equal(await assignMenu().getByRole('menuitemcheckbox',{name:'Film B',exact:true}).getAttribute('aria-checked'),'true');assert.equal(oldTask.project_id,'project-2');await assignment('My retained draft');await assignMenu().waitFor({state:'hidden'});assert.equal(oldTask.project_id,'project-1');
  record('Concurrent reassignment shows confirmed current checkmark and requires another explicit choice');
  await changeOld();assert.equal(await assignMenu().getByRole('menuitemcheckbox',{name:'My retained draft',exact:true}).getAttribute('aria-checked'),'true');assignmentFailure=true;await assignment('Film B');await assignMenu().getByText('Assignment interrupted',{exact:true}).waitFor();assert.equal(oldTask.project_id,'project-1');assert.equal(await assignMenu().getByRole('menuitemcheckbox',{name:'My retained draft',exact:true}).getAttribute('aria-checked'),'true');assignmentFailure=false;await assignment('Film B');await assignMenu().waitFor({state:'hidden'});assert.equal(oldTask.project_id,'project-2');
  record('Assignment failure preserves confirmed checkmark and supports explicit retry');
  await changeOld();await page.keyboard.press('Escape');const oldCard=page.locator('[data-task-id="old"]'),projectTrigger=oldCard.getByRole('button',{name:'Project',exact:true});assert.equal(await projectTrigger.evaluate(n=>n===document.activeElement),true);assert.equal(await oldCard.locator('.task-menu').evaluate(n=>n.open),true);await projectTrigger.press('ArrowRight');await assignMenu().getByRole('menuitemcheckbox').first().waitFor();await page.keyboard.press('End');assert.equal(await assignMenu().getByRole('menuitemcheckbox').last().evaluate(n=>n===document.activeElement),true);await page.keyboard.press('Home');await page.keyboard.press('ArrowDown');await page.keyboard.press('ArrowUp');await page.keyboard.press('ArrowLeft');assert.equal(await assignMenu().count(),0);assert.equal(await projectTrigger.evaluate(n=>n===document.activeElement),true);await page.mouse.move(0,0);await projectTrigger.hover();await assignMenu().getByRole('menuitemcheckbox').first().waitFor();await page.keyboard.press('Escape');await projectTrigger.tap();await assignMenu().getByRole('menuitemcheckbox').first().waitFor();await page.keyboard.press('Escape');await page.keyboard.press('Escape');
  record('Hover, touch and keyboard Right/Left/Arrow/Home/End/Escape open and dismiss submenu accessibly');
  projectReadFailure=true;await changeOld(false);await assignMenu().getByText(/Could not load projects/).waitFor();assert.equal(await assignMenu().isVisible(),true);assert.equal(oldTask.project_id,'project-2');await page.keyboard.press('Escape');projectReadFailure=false;await changeOld();await assignment('My retained draft');await assignMenu().waitFor({state:'hidden'});
  record('Project read failure shows recoverable message; reopening reloads choices');

  await choose('My retained draft');const beforeDelete=tasks.map(t=>{const copy={...t};delete copy.project_id;delete copy.project_version;return copy;});
  await choose('Delete project…');await page.getByRole('dialog',{name:'Delete project?',exact:true}).getByRole('button',{name:'Cancel',exact:true}).click();assert.ok(projects.some(p=>p.id==='project-1'));await choose('Delete project…');await page.keyboard.press('Escape');assert.ok(projects.some(p=>p.id==='project-1'));
  await choose('Delete project…');await page.getByRole('dialog',{name:'Delete project?',exact:true}).getByText(/Generations and result files will stay/).waitFor();await page.getByRole('dialog',{name:'Delete project?',exact:true}).getByRole('button',{name:'Delete',exact:true}).click();await page.getByRole('dialog',{name:'Delete project?',exact:true}).waitFor({state:'hidden'});assert.equal(await page.locator('#journalProject').inputValue(),'');assert.ok(!projects.some(p=>p.id==='project-1'));assert.ok(!tasks.some(t=>t.project_id==='project-1'));assert.deepEqual(tasks.map(t=>{const copy={...t};delete copy.project_id;delete copy.project_version;return copy;}),beforeDelete);await page.locator('[data-task-id="old"]').waitFor();assert.deepEqual(await state(),draft);
  record('Confirmed populated deletion detaches every tag, preserves all generations/settings and returns to All; Cancel/Escape do nothing');
  await choose('Film B');await choose('Delete project…');projects=projects.map(p=>p.id==='project-2'?{...p,name:'Updated before deletion',version:p.version+1}:p);await page.getByRole('dialog',{name:'Delete project?',exact:true}).getByRole('button',{name:'Delete',exact:true}).click();await page.getByRole('dialog',{name:'Delete project?',exact:true}).getByText(/Review before confirming deletion again/).waitFor();assert.ok(projects.some(p=>p.id==='project-2'));await page.keyboard.press('Escape');
  record('A stale deletion never removes a renamed project without another confirmation');
  const longName='Project '+ 'x'.repeat(72);projects.find(p=>p.id==='project-4').name=longName;await page.evaluate(()=>GatewayProjects.load());
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   console.log('Layout:',theme,width);await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await choose('All Projects');await page.getByRole('combobox',{name:'Project',exact:true}).focus();await page.keyboard.press('ArrowDown');await page.keyboard.press('End');assert.equal(await page.getByRole('listbox',{name:'Project',exact:true}).evaluate(n=>n.scrollWidth<=n.clientWidth),true);await page.screenshot({path:path.join(output,theme+'-'+width+'-dropdown.png')});await page.keyboard.press('Escape');assert.equal(await page.getByRole('combobox',{name:'Project',exact:true}).evaluate(n=>n===document.activeElement),true);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:path.join(output,theme+'-'+width+'.png')});
   await choose('Create project…');await page.getByRole('dialog',{name:'Create project',exact:true}).getByRole('textbox',{name:'Project name'}).fill('A new project');await page.screenshot({path:path.join(output,theme+'-'+width+'-create.png')});await page.keyboard.press('Escape');
   await choose('Updated before deletion');await choose('Rename project…');await page.screenshot({path:path.join(output,theme+'-'+width+'-rename.png')});await page.keyboard.press('Escape');await choose('Delete project…');await page.screenshot({path:path.join(output,theme+'-'+width+'-delete.png')});await page.keyboard.press('Escape');
   await choose('All Projects');await page.locator('[data-task-id="old"]').waitFor();await changeOld();await assignMenu().getByRole('menuitemcheckbox',{name:longName,exact:true}).waitFor();const popupBox=await assignMenu().boundingBox();assert.ok(popupBox.x>=0&&popupBox.x+popupBox.width<=width+1);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:path.join(output,theme+'-'+width+'-assign.png')});await page.keyboard.press('Escape');
  }
  record('Shared dropdown keyboard/Escape and both themes at 1440/390/320px');
  await page.setViewportSize({width:1440,height:1100});
  projects.push({id:'11111111-0000-4000-8000-000000000001',name:'Legacy duplicate',version:1},{id:'22222222-0000-4000-8000-000000000002',name:'Legacy duplicate',version:1});
  await page.evaluate(()=>GatewayProjects.load());await choose('Legacy duplicate · 11111111');assert.equal(await page.locator('#journalProject').inputValue(),'11111111-0000-4000-8000-000000000001');
  await choose('All Projects');tasks.find(t=>t.id==='old').user_id='other-company-member';tasks.find(t=>t.id==='old').can_change_project=true;await page.evaluate(()=>loadTasks());await changeOld();await assignment('Legacy duplicate · 22222222');await assignMenu().waitFor({state:'hidden'});assert.equal(tasks.find(t=>t.id==='old').project_id,'22222222-0000-4000-8000-000000000002');
  record('Company-shared generation assigns across authors; duplicate legacy names preserve distinct IDs in filter/submenu');
  projects.push({id:'11111111-aaaa-4000-8000-000000000003',name:'Legacy duplicate',version:1});await page.evaluate(()=>GatewayProjects.load());
  await choose('Legacy duplicate · 11111111-0000-4000-8000-000000000001');assert.equal(await page.locator('#journalProject').inputValue(),'11111111-0000-4000-8000-000000000001');
  await choose('Legacy duplicate · 11111111-aaaa-4000-8000-000000000003');assert.equal(await page.locator('#journalProject').inputValue(),'11111111-aaaa-4000-8000-000000000003');await choose('All Projects');
  record('Same-name legacy projects with colliding short UUID prefixes remain unambiguous');

  manageAllowed=false;await page.evaluate(()=>GatewayProjects.load());await page.waitForFunction(()=>document.querySelector('#journalProject option[value="new-project-action"]').disabled);assert.equal(await page.locator('#journalProject option[value="new-project-action"]').evaluate(n=>n.disabled),true);assert.equal(await page.locator('#manageProject').isVisible(),false);assert.equal(await page.getByRole('combobox',{name:'Project',exact:true}).isEnabled(),true);
  record('Server read-only capability disables project mutations while company project filter stays readable');
  manageAllowed=true;await page.evaluate(()=>GatewayProjects.load());await page.waitForFunction(()=>!document.querySelector('#journalProject option[value="new-project-action"]').disabled);
  await choose('All Projects');await changeOld();assignmentGate={...deferred(),started:deferred()};await assignment('Updated before deletion');await assignmentGate.started.promise;key='other-key';await page.evaluate(()=>{window.GenerationKey.value=()=> 'other-key';window.dispatchEvent(new Event('gateway-key-change'));});await assignMenu().waitFor({state:'hidden'});assignmentGate.resolve();assignmentGate=null;await page.waitForFunction(()=>document.querySelector('#journalProject').options.length===3);assert.equal(await page.locator('#journalProject').innerText(),'All Projects\nNo Projects\nNew Project…');
  key='fixture-generation-key';await page.evaluate(()=>{window.GenerationKey.value=()=> 'fixture-generation-key';window.dispatchEvent(new Event('gateway-key-change'));});await page.waitForFunction(()=>document.querySelector('#journalProject').options.length>1);
  record('Key change closes pending assignment and prevents old project names or response from repopulating another context');
  createGate=deferred();await create('Interrupted by key change');await page.getByText('Creating…',{exact:true}).waitFor();key='other-key';await page.evaluate(()=>{window.GenerationKey.value=()=> 'other-key';window.dispatchEvent(new Event('gateway-key-change'));});createGate.resolve();createGate=null;await page.waitForFunction(()=>document.querySelector('#journalProject').options.length===3);assert.equal(await page.getByRole('dialog',{name:'Create project',exact:true}).count(),0);assert.equal(await page.locator('#journalProject').inputValue(),'');assert.equal(await page.locator('#journalProject').innerText(),'All Projects\nNo Projects\nNew Project…');
  record('Key change cancels project dialog and ignores old create response without disclosing project names');
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,results,posts,reads,errors,notices,external},null,2));
 }catch(error){await page.screenshot({path:path.join(output,'failure.png')});console.error(await page.evaluate(()=>({active:document.activeElement?.tagName,projectOptionCount:document.querySelector('#journalProject').options.length,popupCount:document.querySelectorAll('.control-popup').length})));throw error;}finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
