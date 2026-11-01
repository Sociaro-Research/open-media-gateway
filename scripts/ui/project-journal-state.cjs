// Project metadata must preserve the attached Journal and native players.
// Synthetic company APIs/media only; application polling timers stay unchanged.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const repo=path.resolve(__dirname,'../..'),root=path.resolve(process.env.UI_SOURCE_DIR||path.join(repo,'web/public/ui'));
const {evidencePath}=require('./evidence.cjs');
const {execFileSync}=require('node:child_process');
const {useFixtureKey}=require(path.join(repo,'scripts/ui/fixture-key.cjs'));
const engine=process.env.UI_BROWSER_ENGINE||'chromium',output=evidencePath('/tmp/gateway-project-journal-state/'+engine+'/');fs.mkdirSync(output,{recursive:true});
const media=path.join(output,'media');fs.mkdirSync(media,{recursive:true});
execFileSync('ffmpeg',['-y','-f','lavfi','-i','color=c=blue:s=160x90:r=10','-t','30','-c:v','libx264','-pix_fmt','yuv420p',path.join(media,'synthetic.mp4')],{stdio:'ignore'});
execFileSync('ffmpeg',['-y','-f','lavfi','-i','sine=frequency=220:sample_rate=8000','-t','30',path.join(media,'synthetic.wav')],{stdio:'ignore'});
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve,started:null};};
let key='synthetic-company-generation-key',assignmentFailure=false,assignmentConflict=false,renameFailure=false,renameConflict=false,mutationGate=null,historyGate=null;
let projects=[{id:'alpha',name:'Alpha company project',version:1},{id:'beta',name:'Beta company project',version:1}];
let tasks=Array.from({length:30},(_,i)=>({id:'task-'+i,prompt:'Synthetic company journal '+i,provider:'mock',provider_model:'video',model:'mock/video',media_type:i===18?'audio':'video',status:'completed',created_at:1700000000,updated_at:1700000001,project_id:i===20?'':'alpha',project_version:0,can_change_project:true,content_access:true,options:{duration:5,resolution:'720p',ratio:'16:9'},...([17,18].includes(i)?{has_stored_result:true,stored_at:'2026-10-27T00:00:00Z',content_url:'/v1/generations/task-'+i+'/content',result_content_type:i===17?'video/mp4':'audio/wav'}:{})}));
const reads=[],writes=[],external=[],errors=[],notices=[],cases=[];
const server=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://fixture').pathname;
 if(p.startsWith('/fixture/')){
  const video=p.endsWith('.mp4'),buf=fs.readFileSync(path.join(media,video?'synthetic.mp4':'synthetic.wav'));
  res.setHeader('Content-Type',video?'video/mp4':'audio/wav');res.setHeader('Accept-Ranges','bytes');
  const range=/bytes=(\d+)-(\d*)/.exec(req.headers.range||'');if(range){const start=Number(range[1]),end=range[2]?Math.min(Number(range[2]),buf.length-1):buf.length-1;res.writeHead(206,{'Content-Range':`bytes ${start}-${end}/${buf.length}`,'Content-Length':end-start+1});return res.end(buf.subarray(start,end+1));}return res.end(buf);
 }
 const file=path.join(root,path.basename(p).includes('.')?path.basename(p):'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await({chromium,webkit}[engine]).launch({headless:true,executablePath:process.env.UI_BROWSER_PATH});
 const context=await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),page=await context.newPage();page.setDefaultTimeout(15000);
 page.on('pageerror',e=>{if(e.message==='ResizeObserver loop completed with undelivered notifications.')notices.push(e.message);else errors.push(e.message)});
 await page.route('**/*',async route=>{
  const req=route.request(),u=new URL(req.url()),p=u.pathname;if(u.origin!==base){external.push(req.url());return route.abort();}
  if(p==='/auth/methods')return route.fulfill({json:{email:true,google:false,email_delivery:false}});
  if(!p.startsWith('/v1/'))return route.continue();const json=(body,status=200)=>route.fulfill({json:body,status});
  if(req.method()!=='GET')writes.push({at:Date.now(),method:req.method(),path:p,body:req.postDataJSON()});
  if(p==='/v1/info')return json({provider:'mock',auth_mode:'google'});
  if(p==='/v1/me')return json({id:'fixture-user',session_id:'fixture-session',name:'Synthetic user',email:'synthetic@example.test',is_system_admin:true});
  if(p==='/v1/organizations')return json({data:[{id:'fixture-company',name:'Synthetic company',role:'developer',balance_usd:0}]});
  if(p==='/v1/playground-key/restore')return json({error:{message:'No saved synthetic key'}},404);
  if(p==='/v1/key')return json({key:{name:'Synthetic company key',resource_mode:'shared',organization_id:'fixture-company',user_id:'fixture-user'}});
  if(p==='/v1/providers')return json({data:[{id:'mock',name:'Mock',default_model:'video',models:[{id:'video',display_name:'Synthetic video',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5}]}]});
  if(p==='/v1/projects'){assert.equal(req.method(),'GET');return json({data:projects,can_manage_projects:true,scope:'company'});}
  if(p.startsWith('/v1/projects/')){assert.equal(req.method(),'PATCH');const data=req.postDataJSON(),id=p.split('/').at(-1),old=projects.find(x=>x.id===id);if(renameFailure)return json({error:{message:'Rename interrupted'}},503);if(renameConflict){renameConflict=false;old.name='Concurrent company rename';old.version++;}if(data.version!==old.version)return json({error:{message:'Project changed'}},409);projects=projects.map(x=>x.id===id?{...x,name:data.name,version:x.version+1}:x);return json(projects.find(x=>x.id===id));}
  if(p==='/v1/generations'){assert.equal(req.method(),'GET','No generation submission permitted');const selected=u.searchParams.get('project_id')||'';reads.push({at:Date.now(),project:selected});const snapshot=structuredClone(key==='synthetic-company-generation-key'?tasks.filter(t=>!selected||t.project_id===selected):[]);const gate=historyGate;if(gate){historyGate=null;gate.started.resolve();await gate.promise;}return json({data:snapshot});}
  if(p==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
  if(p.endsWith('/media-access')){const id=p.split('/')[3],ext=id==='task-18'?'wav':'mp4';return json({preview_url:base+'/fixture/synthetic.'+ext,download_url:base+'/fixture/synthetic.'+ext});}
  if(p.endsWith('/project')){assert.equal(req.method(),'PATCH');const data=req.postDataJSON(),task=tasks.find(t=>t.id===p.split('/')[3]);const gate=mutationGate;if(gate){mutationGate=null;gate.started.resolve();await gate.promise;}if(assignmentFailure)return json({error:{message:'Assignment interrupted'}},503);if(assignmentConflict){assignmentConflict=false;task.project_id='alpha';task.project_version++;}if(data.version!==task.project_version)return json({error:{message:'Assignment changed'}},409);task.project_id=data.project_id;task.project_version++;return json({project_id:task.project_id,version:task.project_version});}
  if(/^\/v1\/generations\/[^/]+$/.test(p))return json(tasks.find(t=>t.id===p.split('/')[3]));
  assert.equal(req.method(),'GET','Unexpected synthetic write');return json({data:[]});
 });
 const save=()=>fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,source:root,timers:'Unmodified production polling; no direct loadTasks/renderTasks calls',cases,reads,writes,external,errors,notices},null,2));
 const frames=()=>page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
 const stableLayout=()=>page.waitForFunction(()=>{const list=document.querySelector('#taskList'),signature=[list.scrollTop,list.scrollHeight,list.clientWidth,document.querySelector('[data-task-id=\"task-18\"]')?.offsetTop].join(':');const prior=window.fixtureLayout;if(!prior||prior.signature!==signature){window.fixtureLayout={signature,at:performance.now()};return false;}return performance.now()-prior.at>=250;});
 const waitPlayers=()=>page.waitForFunction(()=>document.querySelectorAll('#taskList video,#taskList audio').length===2&&[...document.querySelectorAll('#taskList video,#taskList audio')].every(p=>p.readyState>=1));
 const prime=async()=>{
  await waitPlayers();await page.locator('[data-task-id="task-20"]').scrollIntoViewIfNeeded();
  await page.evaluate(async()=>{for(const p of document.querySelectorAll('#taskList video,#taskList audio')){p.muted=true;p.loop=true;p.currentTime=p.tagName==='VIDEO'?8:9;await p.play();}});
  await page.waitForFunction(()=>[...document.querySelectorAll('#taskList video,#taskList audio')].every(p=>!p.paused&&p.currentTime>=7));
 };
 const begin=async label=>{await frames();return page.evaluate(label=>{window.qaCards=[...document.querySelectorAll('#taskList .task')];window.qaVideo=document.querySelector('#taskList video');window.qaAudio=document.querySelector('#taskList audio');window.qaEvents=[];const list=document.querySelector('#taskList');return {focused:document.activeElement.outerHTML.slice(0,300),focusTop:document.activeElement.getBoundingClientRect().top-list.getBoundingClientRect().top,label,at:Date.now(),scroll:list.scrollTop,visibleHeight:list.clientHeight,fullHeight:list.scrollHeight,cards:qaCards.length,selected:GatewayProjects.selected(),video:{time:qaVideo.currentTime,paused:qaVideo.paused,duration:qaVideo.duration},audio:{time:qaAudio.currentTime,paused:qaAudio.paused,duration:qaAudio.duration}};},label)};
 const after=async()=>{await frames();return page.evaluate(()=>{const list=document.querySelector('#taskList'),v=list.querySelector('video'),a=list.querySelector('audio');return {focused:document.activeElement.outerHTML.slice(0,300),focusTop:document.activeElement.getBoundingClientRect().top-list.getBoundingClientRect().top,fullHeight:list.scrollHeight,at:Date.now(),scroll:list.scrollTop,cards:list.querySelectorAll('.task').length,retained:qaCards.filter(n=>n.isConnected).length,selected:GatewayProjects.selected(),video:{same:v===qaVideo,oldConnected:qaVideo.isConnected,time:v?.currentTime,paused:v?.paused,ready:v?.readyState},audio:{same:a===qaAudio,oldConnected:qaAudio.isConnected,time:a?.currentTime,paused:a?.paused,ready:a?.readyState},events:qaEvents};});};
 const record=(name,before,after,classification)=>{cases.push({name,before,after,classification});save();console.log('CASE',name,classification,'scroll',before.scroll,'->',after.scroll,'retained',after.retained+'/'+before.cards,'media same',after.video.same,after.audio.same);};
 const nextPoll=async()=>{const response=await page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/generations'&&r.request().method()==='GET');await response.finished();await frames();};
 const chooseFilter=async(id,withMedia=true)=>{await page.locator('#journalProject').selectOption(id,{force:true});await page.waitForFunction(id=>GatewayProjects.selected()===id&&latestTasks.length>0&&latestTasks.every(t=>!id||t.project_id===id),id);if(withMedia)await waitPlayers();};
 const preserved=(before,result)=>{
  assert.equal(result.retained,before.cards,'Every metadata-only card remains attached');
  assert.ok(Math.abs(result.scroll-before.scroll)<2,'Journal position retained: '+JSON.stringify({before,result}));
  for(const tag of ['video','audio']){assert.equal(result[tag].same,true,tag+' node retained');assert.equal(result[tag].paused,false,tag+' keeps playing');assert.ok(result[tag].time>=before[tag].time-0.5,tag+' time retained');}
 };
 const assignment=async(destination,name,id=20)=>{
  await prime();const card=page.locator('[data-task-id="task-'+id+'"]');await card.locator('summary[aria-label="Task actions"]').press('Enter');await card.getByRole('button',{name:'Project',exact:true}).press('ArrowRight');
  const menu=page.getByRole('menu',{name:'Generation project',exact:true});await menu.getByRole('menuitemcheckbox').first().waitFor();const before=await begin(name),version=tasks.find(t=>t.id==='task-'+id).project_version;
  await page.locator('.journal-card').screenshot({path:path.join(output,name+'-before.png')});
  await menu.getByRole('menuitemcheckbox',{name:projects.find(p=>p.id===destination).name,exact:true}).click();
  await page.waitForFunction(({id,v})=>latestTasks.find(t=>t.id==='task-'+id)?.project_version===v,{id,v:version+1});await waitPlayers();const result=await after();preserved(before,result);assert.equal(await card.locator('summary[aria-label="Task actions"]').evaluate(n=>n===document.activeElement),true,'Successful assignment returns focus to its retained card summary');record(name,before,result,'Assignment preserves every card, position and playing media');
  await page.locator('.journal-card').screenshot({path:path.join(output,name+'-after.png')});
 };
 try{
  await page.goto(base);await useFixtureKey(page,'synthetic-company-generation-key');await page.waitForFunction(()=>latestTasks.length===30);await waitPlayers();
  await page.evaluate(()=>{window.qaEvents=[];addEventListener('gateway-project-change',()=>qaEvents.push({type:'project-change',at:Date.now()}));});
  await prime();let before=await begin('unchanged-poll');await nextPoll();let result=await after();assert.equal(result.retained,before.cards);assert.equal(result.video.same,true);assert.equal(result.audio.same,true);assert.ok(Math.abs(result.scroll-before.scroll)<2);assert.equal(result.video.paused,false);assert.equal(result.audio.paused,false);record('unchanged-poll',before,result,'PASS: unchanged natural polling preserves position and active players');
  await prime();before=await begin('unrelated-card-poll');tasks[0].updated_at++;await page.waitForFunction(v=>JSON.parse(document.querySelector('[data-task-id="task-0"]').taskFingerprint).updated_at===v,tasks[0].updated_at);result=await after();assert.equal(result.retained,before.cards-1);assert.equal(result.video.same,true);assert.equal(result.audio.same,true);assert.equal(result.video.paused,false);assert.equal(result.audio.paused,false);record('unrelated-card-poll',before,result,'PASS: only unrelated changed card replaced');
  for(const [id,tag] of [[17,'video'],[18,'audio']]){
   await prime();before=await begin(tag+'-metadata-poll');const task=tasks[id];task.project_version++;task.project_id='beta';await page.waitForFunction(({id,v})=>JSON.parse(document.querySelector('[data-task-id="task-'+id+'"]').taskFingerprint).project_version===v,{id,v:task.project_version});await waitPlayers();result=await after();preserved(before,result);record(tag+'-metadata-poll',before,result,'Metadata-only natural poll preserves changed and unrelated players');
  }
  await assignment('beta','assign-from-all');await assignment('alpha','change-from-all');await assignment('alpha','remove-assignment-from-all');await assignment('alpha','restore-assignment-from-all');
  await assignment('alpha','reassign-playing-video-from-all',17);await assignment('alpha','reassign-playing-audio-from-all',18);
  tasks[17].project_id=tasks[18].project_id='alpha';tasks[17].project_version++;tasks[18].project_version++;
  await chooseFilter('alpha');
  // task20 is in Alpha after the second assignment; both players are present.
  await prime();await page.getByRole('button',{name:'Edit project',exact:true}).click();await page.getByRole('menuitem',{name:'Rename',exact:true}).click();let dialog=page.getByRole('dialog',{name:'Rename project',exact:true});await dialog.getByRole('textbox',{name:'Project name'}).fill('Synthetic cancelled name');before=await begin('rename-cancel');const writesBefore=writes.length;await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await dialog.waitFor({state:'detached'});await nextPoll();result=await after();assert.equal(writes.length,writesBefore);assert.equal(result.retained,before.cards);assert.equal(result.video.same,true);assert.equal(result.audio.same,true);record('rename-cancel',before,result,'PASS: explicit cancel does not invalidate history or media');
  await prime();await page.getByRole('button',{name:'Edit project',exact:true}).click();await page.getByRole('menuitem',{name:'Rename',exact:true}).click();dialog=page.getByRole('dialog',{name:'Rename project',exact:true});await dialog.getByRole('textbox',{name:'Project name'}).fill('Renamed Alpha company project');before=await begin('rename-active-company-project');await page.locator('.journal-card').screenshot({path:path.join(output,'rename-before.png')});await dialog.getByRole('button',{name:'Save',exact:true}).click();await dialog.waitFor({state:'detached'});await page.waitForFunction(()=>latestTasks.length===30);await waitPlayers();result=await after();assert.equal(result.selected,before.selected);preserved(before,result);record('rename-active-company-project',before,result,'Rename preserves selected context, cards, position and playing media');await page.locator('.journal-card').screenshot({path:path.join(output,'rename-after.png')});
  await prime();before=await begin('explicit-filter-navigation');await chooseFilter('');result=await after();assert.equal(result.selected,'');assert.equal(result.retained,0);record('explicit-filter-navigation',before,result,'Control: intentional project-filter context change also clears list');
  // A delayed write must leave the current journal interactive while polling.
  const card=()=>page.locator('[data-task-id="task-17"]'),menu=()=>page.getByRole('menu',{name:'Generation project',exact:true});
  const open=async()=>{if(!await card().locator('.task-menu').evaluate(n=>n.open))await card().locator('summary[aria-label="Task actions"]').press('Enter');await card().locator('.change-project').press('ArrowRight');await menu().getByRole('menuitemcheckbox').first().waitFor();};
  const syncTask=async id=>page.waitForFunction(({id,v})=>JSON.parse(document.querySelector('[data-task-id="'+id+'"]')?.taskFingerprint||'{}').project_version===v,{id,v:tasks.find(t=>t.id===id).project_version});
  await prime();await open();before=await begin('delayed-repeated-assignment');
  const gate=deferred();gate.started=deferred();mutationGate=gate;const writeCount=writes.length;
  await menu().getByRole('menuitemcheckbox',{name:'Beta company project',exact:true}).click();await gate.started.promise;
  assert.equal(await menu().getByRole('menuitemcheckbox',{name:'Beta company project',exact:true}).isDisabled(),true);
  await menu().getByRole('menuitemcheckbox',{name:'Beta company project',exact:true}).evaluate(n=>n.click());
  await nextPoll();preserved(before,await after());assert.equal(writes.length,writeCount+1,'Repeated activation does not duplicate pending write');
  gate.resolve();await menu().waitFor({state:'detached'});await syncTask('task-17');result=await after();preserved(before,result);record('delayed-repeated-assignment',before,result,'Pending write and natural polling retain media; repeated activation is ignored');

  await prime();await open();before=await begin('interrupted-assignment');assignmentFailure=true;
  await menu().getByRole('menuitemcheckbox').first().click();await menu().getByText('Assignment interrupted',{exact:true}).waitFor();await nextPoll();result=await after();preserved(before,result);record('interrupted-assignment',before,result,'503 preserves attached media and retryable menu');
  assignmentFailure=false;assignmentConflict=true;await menu().getByRole('menuitemcheckbox').first().click();await menu().getByText(/Review before choosing again/).waitFor();await syncTask('task-17');await nextPoll();result=await after();preserved(before,result);record('stale-assignment-version',before,result,'409 reconciles current shared assignment without replacing the playing card');
  const reviewedVersion=tasks[17].project_version;await menu().getByRole('menuitemcheckbox',{name:'Beta company project',exact:true}).click();await menu().waitFor({state:'detached'});await syncTask('task-17');assert.equal(writes.filter(w=>w.path.endsWith('/project')).at(-1).body.version,reviewedVersion);preserved(before,await after());

  // A response captured before a successful mutation cannot roll back its metadata.
  await prime();const stale=deferred();stale.started=deferred();historyGate=stale;await stale.started.promise;
  await open();before=await begin('stale-history-after-assignment');await menu().getByRole('menuitemcheckbox',{name:projects.find(p=>p.id==='alpha').name,exact:true}).click();await menu().waitFor({state:'detached'});await syncTask('task-17');stale.resolve();await frames();await nextPoll();result=await after();preserved(before,result);assert.equal(await card().evaluate(n=>JSON.parse(n.taskFingerprint).project_id),'alpha');record('stale-history-after-assignment',before,result,'Aborted old history response cannot roll back a confirmed assignment');

  await chooseFilter('alpha');await prime();
  const rename=async()=>{await page.getByRole('button',{name:'Edit project',exact:true}).click();await page.getByRole('menuitem',{name:'Rename',exact:true}).click();return page.getByRole('dialog',{name:'Rename project',exact:true});};
  dialog=await rename();await dialog.getByRole('textbox',{name:'Project name'}).fill('Reviewed final company name');before=await begin('interrupted-rename');renameFailure=true;
  await dialog.getByRole('button',{name:'Save',exact:true}).click();await dialog.getByText('Rename interrupted',{exact:true}).waitFor();await nextPoll();result=await after();preserved(before,result);assert.equal(await dialog.getByRole('textbox').inputValue(),'Reviewed final company name');record('interrupted-rename',before,result,'Rename failure retains draft and complete journal state');
  renameFailure=false;renameConflict=true;await dialog.getByRole('button',{name:'Save',exact:true}).click();await dialog.getByText(/Current name: Concurrent company rename/).waitFor();await nextPoll();preserved(before,await after());
  await dialog.getByRole('button',{name:'Save',exact:true}).click();await dialog.waitFor({state:'detached'});result=await after();preserved(before,result);assert.equal(await page.locator('#journalProject option:checked').textContent(),'Reviewed final company name');record('stale-rename-version',before,result,'Concurrent rename requires review; explicit retry retains selected project and media');

  // Membership updates remove only excluded cards, preserving a surviving anchor.
  await prime();before=await begin('filtered-membership-change');
  const anchorBefore=await page.evaluate(()=>{const list=document.querySelector('#taskList'),top=list.getBoundingClientRect().top;const card=[...list.querySelectorAll('.task')].find(n=>n.getBoundingClientRect().bottom>top);window.qaAnchor=card;return card.getBoundingClientRect().top-top;});
  tasks[0].project_id='beta';tasks[0].project_version++;await page.locator('[data-task-id="task-0"]').waitFor({state:'detached'});result=await after();assert.equal(result.retained,before.cards-1);assert.equal(result.video.same,true);assert.equal(result.audio.same,true);assert.equal(result.video.paused,false);assert.equal(result.audio.paused,false);
  const anchorAfter=await page.evaluate(()=>qaAnchor.getBoundingClientRect().top-document.querySelector('#taskList').getBoundingClientRect().top);assert.ok(Math.abs(anchorBefore-anchorAfter)<2);record('filtered-membership-change',before,result,'Only excluded card removed; surviving visible anchor and both players retained');
  await prime();await open();before=await begin('move-playing-card-out-of-filter');await menu().getByRole('menuitemcheckbox',{name:'Beta company project',exact:true}).click();await card().waitFor({state:'detached'});result=await after();assert.equal(result.video.same,false);assert.equal(result.video.oldConnected,false);assert.equal(result.audio.same,true);assert.equal(result.audio.paused,false);assert.equal(await page.locator('#journalProject').evaluate(n=>n.closest('.floating-field').querySelector('.select-trigger')===document.activeElement),true,'Excluded focused card returns focus to the project filter');record('move-playing-card-out-of-filter',before,result,'A playing card legitimately leaving the selected project is disposed; unrelated player remains');
  tasks[17].project_id='alpha';tasks[17].project_version++;await card().waitFor();await waitPlayers();

  await prime();await page.locator('[data-task-id="task-18"] .task-menu > summary').focus();before=await begin('focused-metadata-poll');tasks[18].project_version++;await syncTask('task-18');result=await after();preserved(before,result);assert.equal(await page.locator('[data-task-id="task-18"] .task-menu > summary').evaluate(n=>n===document.activeElement),true);record('focused-metadata-poll',before,result,'Keyboard focus remains on the changed retained card');
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.evaluate(()=>document.activeElement.blur());await page.setViewportSize({width,height:1100});await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await frames();await prime();
   await page.locator('[data-task-id="task-18"] .task-menu > summary').scrollIntoViewIfNeeded();await page.locator('[data-task-id="task-18"] .task-menu > summary').focus();await stableLayout();before=await begin(theme+'-'+width+'-metadata');tasks[18].project_version++;await syncTask('task-18');result=await after();preserved(before,result);assert.equal(await page.locator('[data-task-id="task-18"] .task-menu > summary').evaluate(n=>n===document.activeElement),true);
   await page.screenshot({path:path.join(output,theme+'-'+width+'.png')});record(theme+'-'+width+'-metadata',before,result,'Metadata reconciliation preserves playing media and keyboard focus in responsive theme');
  }
  await page.setViewportSize({width:1440,height:1100});
  await prime();before=await begin('permission-loss');tasks[17].content_access=false;tasks[17].can_change_project=false;tasks[17].stored_at='';tasks[17].content_url='';tasks[17].has_stored_result=false;await page.waitForFunction(()=>document.querySelector('[data-task-id="task-17"] .change-project').disabled);result=await after();assert.equal(result.video.oldConnected,false);assert.equal(result.audio.same,true);record('permission-loss',before,result,'Permission/content loss disposes protected media instead of retaining it');

  await chooseFilter('',false);await page.locator('#taskList').evaluate(n=>n.scrollTop=0);const newTask={...tasks[1],id:'new-company-generation',prompt:'New shared company generation'};tasks.unshift(newTask);await page.locator('[data-task-id="new-company-generation"]').waitFor();assert.equal(await page.locator('#taskList').evaluate(n=>n.scrollTop),0,'Reader at top sees new company generation');
  cases.push({name:'new-generation-at-top',classification:'Natural polling at scroll zero reveals the newly inserted first generation'});
  key='other-fixture-key';await page.evaluate(()=>{window.GenerationKey.value=()=> 'other-fixture-key';window.dispatchEvent(new Event('gateway-key-change'));});await page.waitForFunction(()=>document.querySelectorAll('#taskList .task').length===0);assert.equal(await page.locator('#taskList audio').count(),0);cases.push({name:'key-context-loss',classification:'Key change clears previous company cards and players'});
  assert.deepEqual(external,[]);assert.deepEqual(errors,[]);assert.equal(writes.some(w=>w.path==='/v1/generations'),false);save();console.log('DONE',engine,cases.length,'cases',reads.length,'history reads');
 }catch(e){await page.screenshot({path:path.join(output,'failure.png'),fullPage:true});save();throw e;}finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;});
