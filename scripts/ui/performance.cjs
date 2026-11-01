const {generationFixtureResponse}=require('./generation-page-fixture.cjs');
const {useFixtureKey}=require('./fixture-key.cjs');
const {evidencePath} = require('./evidence.cjs');
// Deterministic browser request/DOM budgets. Real backend load is measured separately.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const ui=path.resolve(__dirname,'../../web/public/ui');
const output=process.env.GATEWAY_PERF_OUTPUT||evidencePath('/tmp/gateway-ui-performance');
const mockSource=fs.readFileSync(path.resolve(__dirname,'../../plugins/provider/mock/mock.go'),'utf8');
const video=Buffer.from(mockSource.match(/embeddedVideoDataURL = "data:video\/mp4;base64,([^"]+)"/)[1],'base64');
const audio=Buffer.alloc(44+16000);audio.write('RIFF');audio.writeUInt32LE(audio.length-8,4);audio.write('WAVEfmt ',8);audio.writeUInt32LE(16,16);audio.writeUInt16LE(1,20);audio.writeUInt16LE(1,22);audio.writeUInt32LE(8000,24);audio.writeUInt32LE(16000,28);audio.writeUInt16LE(2,32);audio.writeUInt16LE(16,34);audio.write('data',36);audio.writeUInt32LE(16000,40);
for(let i=0;i<8000;i++)audio.writeInt16LE(Math.round(Math.sin(i*2*Math.PI*220/8000)*2000),44+i*2);
fs.mkdirSync(output,{recursive:true});
const server=http.createServer((req,res)=>{const name=req.url==='/'?'index.html':path.basename(req.url.split('?')[0]);const file=path.join(ui,name);if(!fs.existsSync(file)){res.writeHead(404).end();return;}res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));});
const engine=process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium;
const sourceHash=require('node:crypto').createHash('sha256');
for(const file of [__filename,path.resolve(__dirname,'../../plugins/provider/mock/mock.go'),...fs.readdirSync(ui).sort().map(name=>path.join(ui,name)).filter(file=>fs.statSync(file).isFile())])sourceHash.update(path.basename(file)+'\0').update(fs.readFileSync(file));
fs.writeFileSync(path.join(output,`ui-${engine===webkit?'webkit':'chromium'}-source.json`),JSON.stringify({started_at:new Date().toISOString(),source_sha256:sourceHash.digest('hex'),node_version:process.version,playwright_version:require('playwright/package.json').version},null,2)+'\n');
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await engine.launch({headless:true,...(engine===chromium?(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{channel:process.env.UI_BROWSER_CHANNEL||'chrome'}):{})});
 const results=[];
 try{for(const [width,theme] of [[1440,'dark'],[1440,'light'],[390,'dark'],[320,'light']]){
  const context=await browser.newContext({viewport:{width,height:1000},colorScheme:theme});const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  let version=1,lists=0,media=0,inFlight=0,peak=0,slow=false;
  const model={id:'test-video',display_name:'Test video',media_type:'video',ratios:['16:9'],min_duration:2,max_duration:30,default_duration:5,pricing:{known:true,reference_price_usd:0}};
  const tasks=()=>Array.from({length:50},(_,i)=>({id:'task-'+i,provider:'mock',model:'mock/test-video',media_type:i%3===0?'image':i%3===1?'video':'speech',prompt:'Fixture '+i,input:'Fixture '+i,status:i===0?'in_progress':'completed',created_at:1700000000,updated_at:1700000010+(i===0?version:0),options:{},content_url:i===0?null:'/v1/generations/task-'+i+'/content',stored_at:i===0?null:'2026-10-01T00:00:00Z',result_content_type:i%3===0?'image/svg+xml':i%3===1?'video/mp4':'audio/wav',has_stored_result:i!==0,cost:{expected_usd:0,actual_source:'free_provider'}}));
  await page.route('**/*',async route=>{
   const url=new URL(route.request().url());const json=body=>route.fulfill({json:generationFixtureResponse(route.request(),body)});
   if(url.pathname==='/v1/info')return json({provider:'mock',auth_mode:'google'});
   if(url.pathname==='/v1/me')return json({id:'preview',name:'Performance',email:'preview@example.test',session_id:'perf',is_system_admin:true});
   if(url.pathname==='/v1/organizations')return json({data:[]});
   if(url.pathname==='/v1/api-keys'&&route.request().method()==='POST')return json({name:'Fixture',secret:'fixture-generation-key'});
   if(url.pathname==='/v1/key')return json({key:{name:'Fixture',resource_mode:'personal'},usage:{month:'2026-10',remaining_usd:null}});
   if(url.pathname==='/v1/providers')return json({data:[{id:'mock',name:'Fixture',credentials_configured:true,default_model:model.id,models:[model]}]});
   if(url.pathname==='/v1/generations/estimate')return json({cost:{expected_usd:0,pricing:{currency:'USD',unit:'generation',price:0,per:1}},can_generate:true,remaining_usd:null});
   if(url.pathname==='/v1/generations'){
    lists++;inFlight++;peak=Math.max(peak,inFlight);
    try{if(slow)await new Promise(resolve=>setTimeout(resolve,3500));return await json({data:tasks()});}catch(e){if(!page.isClosed()&&!/Target.*closed|disposed|cancel|abort/i.test(e.message))throw e;}finally{inFlight--;}
   }
   if(url.pathname.endsWith('/media-access')){media++;const id=Number(url.pathname.match(/task-(\d+)/)[1]);const file=id%3===0?'/fixture.svg':id%3===1?'/fixture-video.mp4':'/fixture-audio.wav';return json({preview_url:file,download_url:file});}
   if(url.pathname==='/fixture-video.mp4')return route.fulfill({contentType:'video/mp4',body:video});
   if(url.pathname==='/fixture-audio.wav')return route.fulfill({contentType:'audio/wav',body:audio});
   if(url.pathname==='/fixture.svg')return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="green"/></svg>'});
   if(url.pathname.startsWith('/v1/'))return json({});
   return route.continue();
  });
  await page.addInitScript(()=>{
   window.perfLongTasks=[];try{new PerformanceObserver(list=>window.perfLongTasks.push(...list.getEntries().map(e=>e.duration))).observe({type:'longtask',buffered:true});}catch{}
  });
  await page.goto(`http://127.0.0.1:${server.address().port}`);await useFixtureKey(page,'fixture-generation-key');await page.waitForFunction(()=>document.querySelectorAll('.task').length===10);while(await page.locator('.task').count()<50){const loaded=await page.locator('.task').count();await page.evaluate(()=>document.querySelector('#journalLoadMore').click());await page.waitForFunction(n=>document.querySelectorAll('.task').length>=n,Math.min(loaded+10,50));}assert.equal(await page.locator('.task').count(),50);
  await page.waitForFunction(()=>[...document.querySelectorAll('.task-media')].every(e=>e.getAttribute('src')));
  await page.evaluate(()=>{window.preservedCard=document.querySelector('[data-task-id="task-1"]');window.preservedPlayer=window.preservedCard.querySelector('.task-media');});
  await page.evaluate(async()=>{window.preservedPlayer.muted=true;window.preservedPlayer.loop=true;await window.preservedPlayer.play();});
  await page.locator('[data-task-id="task-1"] .task-parameters summary').focus();await page.keyboard.press('Enter');
  const initialMedia=media;version++;
  await page.waitForFunction(()=>latestTasks[0].updated_at===1700000012);
  const reused=await page.evaluate(()=>window.preservedCard===document.querySelector('[data-task-id="task-1"]')&&window.preservedPlayer===document.querySelector('[data-task-id="task-1"] .task-media'));
  assert.equal(reused,true,'Updating one generation must preserve unaffected cards and players');
  assert.equal(await page.evaluate(()=>!window.preservedPlayer.paused),true,'Unrelated updates must preserve video playback');
  assert.equal(await page.evaluate(()=>document.activeElement===window.preservedCard.querySelector('.task-parameters summary')&&window.preservedCard.querySelector('.task-parameters').open),true,'Unrelated updates must preserve keyboard focus and expanded parameters');
  assert.equal(media,initialMedia,'An active task update must not reauthorize unchanged saved media');
  // A slow response spans multiple poll ticks. Only one request may be active.
  slow=true;peak=0;await page.waitForTimeout(8500);assert.equal(peak,1,'Slow list responses must not overlap');slow=false;
  // Changing section must stop background generation list traffic.
  await page.evaluate(()=>showConsoleSection('profile'));await page.waitForTimeout(4000);const inactiveLists=lists;await page.waitForTimeout(4500);assert.equal(lists,inactiveLists,'Hidden generation section must not keep polling');
  await page.evaluate(()=>showConsoleSection('generation'));await page.waitForFunction(n=>latestTasks.length===50&&document.querySelectorAll('.task').length===50,lists);
  await page.locator('#prompt').fill('Responsive during repeated updates');assert.equal(await page.locator('#prompt').inputValue(),'Responsive during repeated updates');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'No horizontal overflow');assert.deepEqual(errors,[]);
  const timing=await page.evaluate(()=>({long_tasks:window.perfLongTasks,heap_bytes:performance.memory?.usedJSHeapSize||null}));
  await page.screenshot({path:path.join(output,`journal-${engine===webkit?'webkit':'chromium'}-${width}-${theme}.png`)});
  // Forgetting a key while a list response is delayed must clear old content
  // and dispose the detached media even when that response arrives later.
  slow=true;await page.waitForRequest(request=>new URL(request.url()).pathname==='/v1/generations',{timeout:10000});
  await page.evaluate(()=>GenerationKey.forget());
  assert.equal(await page.locator('.task').count(),0,'Key reset must clear the old journal immediately');
  assert.equal(await page.evaluate(()=>!window.preservedPlayer.isConnected&&!window.preservedPlayer.getAttribute('src')),true,'Removed players must release their media source');
  await page.waitForTimeout(4500);assert.equal(await page.locator('.task').count(),0,'A stale list response must not restore content after key reset');
  assert.deepEqual(errors,[]);
  results.push({engine:engine===webkit?'webkit':'chromium',browser_version:browser.version(),width,theme,lists,media,initial_media:initialMedia,slow_request_peak:peak,stale_response_cleared:true,...timing});
  await context.close();
 }}finally{fs.writeFileSync(path.join(output,`ui-${engine===webkit?'webkit':'chromium'}.json`),JSON.stringify(results,null,2)+'\n');await browser.close();server.close();}
 console.log(JSON.stringify(results));
})().catch(error=>{console.error(error);server.close();process.exitCode=1;});
