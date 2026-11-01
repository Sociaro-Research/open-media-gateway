// Real Playground, synthetic loopback APIs, unmodified production poll timers.
const {chromium,webkit}=require('playwright');
const {createHash}=require('node:crypto');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {useFixtureKey}=require('./fixture-key.cjs');
const {evidencePath}=require('./evidence.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',output=evidencePath('/tmp/gateway-journal-pagination/'+engine+'/');fs.mkdirSync(output,{recursive:true});
const root=path.resolve(process.env.UI_SOURCE_DIR||path.join(__dirname,'../../web/public/ui'));
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};};
const projects=[{id:'11111111-0000-4000-8000-000000000001',name:'Project Alpha',version:1},{id:'22222222-0000-4000-8000-000000000002',name:'Empty project',version:1}];
const task=(i)=>({id:`00000000-0000-4000-8000-${String(10000-i).padStart(12,'0')}`,project_id:i%2?projects[0].id:'',project_version:0,prompt:`Synthetic generation ${i}. A retained journal card with its original settings.`,provider:'mock',provider_model:'video',model:'mock/video',media_type:'video',status:i===0?'in_progress':i===2?'failed':'completed',error:i===2?'Synthetic provider failure. Retry remains available.':'',created_at:1700000000-Math.floor(i/3),updated_at:1700000100,options:{duration:5,ratio:'16:9',resolution:'720p'},can_change_project:true,content_access:true});
let tasks=Array.from({length:27},(_,i)=>task(i)),key='journal-fixture-key',scopeRevision=0,gate=null,failMore=false;
const reads=[],writes=[],external=[],errors=[],cases=[],geometry=[];
const server=http.createServer((req,res)=>{const p=new URL(req.url,'http://fixture').pathname;const file=path.join(root,p==='/'?'index.html':path.basename(p));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const context=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),page=await context.newPage();page.setDefaultTimeout(15000);
 page.on('pageerror',e=>{if(e.message!=='ResizeObserver loop completed with undelivered notifications.')errors.push(e.message);});
 const ordered=rows=>rows.sort((a,b)=>b.created_at-a.created_at||b.id.localeCompare(a.id));
 const position=t=>({t:t.created_at,id:t.id,scope:(key+':'+(new URL(page.url()).search||''))});
 const encode=(t,filter)=>Buffer.from(JSON.stringify({...position(t),scope:key+':'+filter+':'+scopeRevision})).toString('base64url');
 const decode=(raw,filter)=>{if(!raw)return null;const p=JSON.parse(Buffer.from(raw,'base64url'));if(p.scope!==key+':'+filter+':'+scopeRevision)return false;return p;};
 const compare=(a,b)=>a.created_at-b.t||a.id.localeCompare(b.id);
 await page.route('**/*',async route=>{
  const req=route.request(),u=new URL(req.url()),p=u.pathname;if(u.origin!==base){external.push(req.url());return route.abort();}if(!p.startsWith('/v1/'))return route.continue();
  const json=(body,status=200)=>route.fulfill({json:body,status});
  if(req.method()!=='GET')writes.push({path:p,method:req.method()});
  if(p==='/v1/info')return json({provider:'mock',auth_mode:'google'});
  if(p==='/v1/me')return json({id:'fixture-user',session_id:'fixture-session',name:'Fixture',email:'fixture@example.test',is_system_admin:true});
  if(p==='/v1/organizations')return json({data:[]});
  if(p==='/v1/playground-key/restore')return json({error:{message:'No saved synthetic key'}},404);
  if(p==='/v1/key')return json({key:{name:'Fixture',resource_mode:'personal'}});
  if(p==='/v1/providers')return json({data:[{id:'mock',name:'Mock',default_model:'video',models:[{id:'video',display_name:'Test video',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5}]}]});
  if(p==='/v1/projects')return json({data:projects,scope:'company',can_manage_projects:true});
  if(p==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
  if(p==='/v1/generations'){
   assert.equal(req.method(),'GET','This fixture never submits a generation');
   const filter=u.searchParams.get('project_id')||'',before=decode(u.searchParams.get('cursor'),filter),through=decode(u.searchParams.get('through'),filter),limit=Number(u.searchParams.get('limit')||50);
   if(before===false||through===false)return json({error:{message:'Cursor scope changed',param:through===false?'through':'cursor'}},400);
   const all=ordered(tasks.filter(t=>!filter||(filter==='none'?!t.project_id:t.project_id===filter)));
   let range=all.filter(t=>(!before||compare(t,before)<0)&&(!through||compare(t,through)>=0));const ids=u.searchParams.get('ids')?.split(','),view=u.searchParams.get('view');if(ids)range=range.filter(t=>ids.includes(t.id));const data=structuredClone(range.slice(0,limit)).map(t=>({...t,journal_revision:key+':'+filter+':'+scopeRevision+':'+createHash('sha256').update(JSON.stringify(t)).digest('base64url')}));
   const read={filter,cursor:u.searchParams.get('cursor'),through:u.searchParams.get('through'),limit,view,ids,includeTotal:u.searchParams.get('include_total')!=='false',returned:data.length,fullCards:view==='versions'?0:data.length,total:all.length};reads.push(read);
   const pending=gate;if(pending&&pending.match(read)){pending.started.resolve();await pending.promise;}
   if(before&&!through&&failMore)return json({error:{message:'Synthetic page interrupted'}},503);
   return json({journal_scope:key+':'+filter+':'+scopeRevision,data:view==='versions'?data.map(t=>({id:t.id,journal_revision:t.journal_revision})):data,...(u.searchParams.get('include_total')==='false'?{}:{total:all.length}),has_more:range.length>limit,next_cursor:data.length?encode(data.at(-1),filter):null});
  }
  assert.equal(req.method(),'GET','Unexpected fixture mutation '+p);return json({data:[]});
 });
 const record=name=>{cases.push(name);console.log('PASS:',name);};
 const ids=()=>page.locator('#taskList .task').evaluateAll(nodes=>nodes.map(n=>n.dataset.taskId));
 const count=async(n,total)=>{await page.waitForFunction(({n,total})=>document.querySelectorAll('#taskList .task').length===n&&document.querySelector('#journalCount').textContent===`Shown ${n} of ${total}`,{n,total});};
 const top=()=>page.evaluate(()=>scrollTo(0,0));
 const choose=async name=>{await page.keyboard.press('Escape');await top();await page.getByRole('combobox',{name:'Project',exact:true}).click();await page.getByRole('option').filter({hasText:name}).click();};
 const save=()=>fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,browserVersion:browser.version(),loopback:base,profile:'fresh temporary Playwright profile and nonpersistent context',source:root,cases,reads,writes,external,errors,geometry},null,2));
 try{
  await page.goto(base);await useFixtureKey(page,key);await page.waitForFunction(()=>selectedModel()?.id==='video');
  if(process.env.UI_BASELINE==='1'){
   await page.locator('#taskList .task').first().waitFor();
   for(const theme of ['light','dark'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await top();await page.screenshot({path:path.join(output,`${theme}-${width}-before.png`),fullPage:true});}
   record('Baseline captured with identical safe source data');return;
  }
  await count(10,27);assert.ok(reads.every(r=>r.view==='versions'?r.limit===100&&r.fullCards===0:r.ids?r.limit===50&&r.returned<=50:r.limit===10&&r.returned<=10));
  const layout=await page.locator('#taskList').evaluate(n=>({maxHeight:getComputedStyle(n).maxHeight,overflow:getComputedStyle(n).overflowY,scroll:n.scrollHeight,client:n.clientHeight}));assert.equal(layout.maxHeight,'none');assert.equal(layout.overflow,'visible');assert.equal(layout.scroll,layout.client);
  const initial=await ids();await page.locator('#prompt').fill('Retained generation draft');await page.evaluate(()=>window.retainedCards=[...document.querySelectorAll('#taskList .task')]);
  record('Initial request returns exactly ten; journal grows naturally with page scrolling');
  gate={...deferred(),started:deferred(),match:r=>!!r.cursor&&!r.through};await page.locator('#journalPagination').scrollIntoViewIfNeeded();await gate.started.promise;await page.getByText('Loading generations…',{exact:true}).waitFor();
  const pendingReads=reads.filter(r=>r.cursor&&!r.through).length;await page.locator('#journalLoadMore').evaluate(n=>{n.click();n.click();});assert.equal(reads.filter(r=>r.cursor&&!r.through).length,pendingReads);
  failMore=true;gate.resolve();gate=null;await page.getByText(/Could not load more generations/).waitFor();await count(10,27);assert.deepEqual(await ids(),initial);await page.screenshot({path:path.join(output,'page-error.png')});
  record('Approaching bottom starts one visible load; a failed page preserves every loaded record and count');
  failMore=false;await page.locator('#journalLoadMore').focus();await page.keyboard.press('Enter');await count(20,27);assert.equal(await page.evaluate(()=>retainedCards.every(n=>n.isConnected)),true);assert.equal(await page.locator('#prompt').inputValue(),'Retained generation draft');
  record('Keyboard retry appends the next ten without replacing cards or the form draft');
  await top();const twenty=await ids();const added={...task(-1),project_id:'',created_at:1700001000,prompt:'New arrival during pagination'};tasks.unshift(added);tasks.find(t=>t.id===initial[0]).status='completed';
  await count(21,28);const afterArrival=await ids();assert.equal(afterArrival[0],added.id);assert.deepEqual(afterArrival.slice(1),twenty);
  const originalTasks=[...tasks],loadedIDs=await ids();tasks=tasks.filter(t=>loadedIDs.includes(t.id));
  await page.locator('#journalPagination').scrollIntoViewIfNeeded();await count(21,21);await top();
  await page.waitForResponse(r=>new URL(r.url()).searchParams.get('view')==='versions');await count(21,21);
  assert.deepEqual(await ids(),loadedIDs);record('An empty final append preserves its tail and every loaded card through the next natural poll');
  tasks=[...originalTasks];
  gate={...deferred(),started:deferred(),match:r=>r.view==='versions'};await gate.started.promise;
  await page.locator('#journalPagination').scrollIntoViewIfNeeded();gate.resolve();gate=null;
  await count(28,28);assert.equal(new Set(await ids()).size,28);assert.equal(await page.locator('#journalLoadMore').isVisible(),false);assert.deepEqual(await ids(),ordered([...tasks]).map(t=>t.id));
  record('Natural polling updates statuses and arrivals; scrolling during a pending poll still loads the final page without duplicates/skips');
  await top();const burst=Array.from({length:150},(_,i)=>({...task(-100-i),project_id:'',created_at:1700002000+i}));tasks.push(...burst);
  await count(178,178);assert.deepEqual(await ids(),ordered([...tasks]).map(t=>t.id));
  const beforeQuiet=reads.length;await page.waitForResponse(r=>new URL(r.url()).searchParams.get('view')==='versions');await page.waitForFunction(()=>document.querySelector('#taskList').getAttribute('aria-busy')==='false');
  assert.equal(reads.slice(beforeQuiet).reduce((n,r)=>n+r.fullCards,0),0,'Unchanged growing history never re-downloads full cards');
  assert.ok(reads.slice(beforeQuiet).every(r=>r.view==='versions'));record('150 arrivals cross metadata/data boundaries without gaps; unchanged 178-card polling transfers zero full card payloads');
  tasks=[...originalTasks];await count(28,28);
  await choose('No Projects');await count(10,15);assert.ok((await ids()).every(id=>!tasks.find(t=>t.id===id).project_id));
  gate={...deferred(),started:deferred(),match:r=>r.filter==='none'&&!!r.cursor&&!r.through};await page.locator('#journalPagination').scrollIntoViewIfNeeded();await gate.started.promise;
  await choose('Project Alpha');await count(10,13);gate.resolve();gate=null;await page.waitForFunction(()=>!document.querySelector('#taskList').getAttribute('aria-busy')||document.querySelector('#taskList').getAttribute('aria-busy')==='false');assert.ok((await ids()).every(id=>tasks.find(t=>t.id===id).project_id===projects[0].id));
  record('All/No Projects/concrete project filters use their own totals and cancel late pages');
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await choose('Empty project');await count(0,0);await choose('All Projects');await count(10,28);await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await top();await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   geometry.push(await page.evaluate(({theme,width})=>{const list=document.querySelector('#taskList'),footer=document.querySelector('#journalPagination'),journal=document.querySelector('.journal-card'),left=document.querySelector('.create-card');return {theme,width,pageOverflow:document.documentElement.scrollWidth>innerWidth,listHeight:list.clientHeight,fullListHeight:list.scrollHeight,journalHeight:journal.clientHeight,inputHeight:left.clientHeight,footerGap:footer.getBoundingClientRect().top-list.getBoundingClientRect().bottom,offenders:[...document.querySelectorAll('body *')].filter(n=>{const r=n.getBoundingClientRect();return r.width&&r.right>innerWidth+1}).map(n=>[n.tagName,n.id,n.className,n.getBoundingClientRect().right]).slice(0,12)};},{theme,width}));
   assert.equal(geometry.at(-1).pageOverflow,false);assert.equal(geometry.at(-1).fullListHeight,geometry.at(-1).listHeight);assert.equal(geometry.at(-1).footerGap,0);
   await page.screenshot({path:path.join(output,`${theme}-${width}-after.png`),fullPage:true});await page.locator('.journal-heading').scrollIntoViewIfNeeded();await page.screenshot({path:path.join(output,`${theme}-${width}-journal-top.png`)});
   gate={...deferred(),started:deferred(),match:r=>!!r.cursor&&!r.through};await page.locator('#journalPagination').scrollIntoViewIfNeeded();await gate.started.promise;await page.screenshot({path:path.join(output,`${theme}-${width}-loading.png`)});gate.resolve();gate=null;await count(20,28);
  }
  record('Both themes at 1440/390/320px: growing cards, no inner/page overflow, aligned wrapping footer and visible pending state');
  await choose('All Projects');await count(10,28);scopeRevision++;tasks=tasks.map(t=>({...t,prompt:'Current role view',content_access:false,can_change_project:false}));
  await count(10,28);await page.waitForFunction(()=>[...document.querySelectorAll('#taskList .task')].every(n=>JSON.parse(n.taskFingerprint).content_access===false));
  assert.equal((await page.locator('#taskList').innerText()).includes('Synthetic generation'),false);record('Same-key role/scope invalidation disposes old content and restarts with current restricted views');
  await choose('Empty project');await count(0,0);await page.locator('#taskList .empty-state').waitFor();assert.equal(await page.locator('#journalLoadMore').isVisible(),false);await page.screenshot({path:path.join(output,'empty.png')});
  await choose('All Projects');await count(10,28);gate={...deferred(),started:deferred(),match:r=>!!r.cursor&&!r.through};await page.locator('#journalPagination').scrollIntoViewIfNeeded();await gate.started.promise;
  tasks=[{...task(900),prompt:'Replacement key task'}];key='replacement-key';await page.evaluate(key=>{GenerationKey.value=()=>key;dispatchEvent(new Event('gateway-key-change'));},key);await count(1,1);gate.resolve();gate=null;await page.waitForFunction(()=>document.querySelectorAll('#taskList .task').length===1);assert.match(await page.locator('#taskList').innerText(),/Replacement key task/);
  record('Empty and end states are exact; a key change clears old pages and ignores delayed results');
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);assert.ok(writes.every(w=>['/v1/generations/estimate','/v1/playground-key/restore'].includes(w.path)));assert.ok(reads.every(r=>r.view==='versions'?r.limit===100&&r.fullCards===0:r.ids?r.limit===50&&r.returned<=50:r.limit===10&&r.returned<=10));
 }catch(error){await page.screenshot({path:path.join(output,'failure.png'),fullPage:true});console.log(await page.locator('#journalPagination').innerText());throw error;}finally{if(gate)gate.resolve();save();await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
