// Journal media actions against an expiring, authorized localhost fixture.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {evidencePath}=require('./evidence.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=process.env.UI_SOURCE_DIR||path.resolve(__dirname,'../../web/public/ui');
const output=evidencePath('/tmp/gateway-stored-media/'+engine+'/');fs.mkdirSync(output,{recursive:true});
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};};
let requests=0,now=0,gate=null,unavailable=0,unauthorized=false;
const previewAccesses=new Set(),reads=[],accessKeys=[],results=[],errors=[],external=[];
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture');
 if(url.pathname.startsWith('/v1/')){
  let body={data:[]};
  if(url.pathname==='/v1/info')body={auth_mode:'api_key',provider:'mock'};
  if(url.pathname==='/v1/key')body={key:{name:'Fixture',resource_mode:'personal'}};
  if(url.pathname==='/v1/providers')body={data:[{id:'mock',name:'Mock',default_model:'image',models:[{id:'image',display_name:'Image',media_type:'image'}]}]};
  if(url.pathname==='/v1/generations/estimate')body={can_generate:true,cost:{expected_usd:0}};
  if(url.pathname.endsWith('/media-access')){
   requests++;accessKeys.push(req.headers.authorization);
   const token=now+60,base='/media/result?expires='+token+'&signature=fixture&access='+requests;
   body={preview_url:base,download_url:base+'&download=1',last_frame_url:base+'&artifact=last-frame',expires_at:new Date(token*1000).toISOString()};
   const held=gate;gate=null;if(held){held.used.resolve();await held.release.promise;}
   if(unauthorized||unavailable){res.writeHead(unauthorized?403:unavailable,{'Content-Type':'application/json'});return res.end(JSON.stringify({error:{message:'Not available'}}));}
  }
  res.writeHead(200,{'Content-Type':'application/json','Cache-Control':'no-store'});return res.end(JSON.stringify(body));
 }
 if(url.pathname==='/media/result'){
  const expired=Number(url.searchParams.get('expires'))<now;
  reads.push({expired,access:Number(url.searchParams.get('access')),download:url.searchParams.has('download'),frame:url.searchParams.has('artifact'),referrer:req.headers.referer||''});
  if(expired)return res.writeHead(403,{'Content-Type':'application/json'}).end('{"error":"expired"}');
  if(url.searchParams.has('download'))res.setHeader('Content-Disposition','attachment; filename="result.png"');
  res.setHeader('Content-Type','image/png');return res.end(Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a5S0AAAAASUVORK5CYII=','base64'));
 }
 const file=path.join(root,url.pathname==='/'?'index.html':path.basename(url.pathname));
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const context=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),page=await context.newPage();page.setDefaultTimeout(10000);
 context.on('page',p=>p.on('pageerror',e=>errors.push(e.message)));
 page.on('pageerror',e=>errors.push(e.message));
 await context.route('**/*',route=>{if(new URL(route.request().url()).origin===base)return route.continue();external.push('External request blocked');return route.abort();});
 const record=name=>{results.push(name);console.log('PASS:',name);};
 async function key(value){await page.locator('#apiKey').fill(value);await page.locator('#apiKey').press('Tab');await page.waitForFunction(v=>GenerationKey.value()===v,value);if(value)await page.waitForFunction(()=>selectedModel()?.id==='image');}
 async function card(type='image'){
  await page.evaluate(type=>renderTasks([{id:'result',provider:'mock',model:'mock/image',media_type:type,prompt:'Stored result',status:'completed',created_at:1,updated_at:2,has_stored_result:true,stored_at:'2026-10-26T00:00:00Z',content_url:'/v1/generations/result/content',result_content_type:type==='image'?'image/png':type==='audio'?'audio/wav':'video/mp4'}]),type);
  await page.waitForFunction(()=>document.querySelector('.task-media')?.getAttribute('src'));
  await page.waitForFunction(()=>[...document.querySelectorAll('.media-actions button')].every(b=>!b.disabled));
  previewAccesses.add(Number(new URL(await page.locator('.task-media').getAttribute('src'),base).searchParams.get('access')));
 }
 const action=name=>page.locator('.media-actions').getByText(name,{exact:true});
 async function open(name){const event=context.waitForEvent('page');await action(name).click();const tab=await event;await tab.waitForURL('**/media/result?**');assert.equal(await tab.evaluate(()=>opener===null),true);await tab.close();}
 async function download(){const event=page.waitForEvent('download');await action('Download ↓').click();await event;}
 async function arm(){const held={used:deferred(),release:deferred()};gate=held;return held;}
 try{
  await page.goto(base);await page.waitForFunction(()=>authState==='authenticated');await key('fixture-key-a');await card();
  const initial=requests;now+=7200;
  await open('Open ↗');assert.equal(requests,initial+1,'Opening an old card must request fresh authorized access');
  await download();await open('Last frame ↗');assert.ok(reads.every(r=>!r.expired),'An expired URL was opened');record('Open, Download and Last frame refresh after two hours without a reload');
  const src=await page.locator('.task-media').getAttribute('src');
  for(let i=0;i<2;i++){now+=7200;await open('Open ↗');await download();}
  assert.equal(await page.locator('.task-media').getAttribute('src'),src);record('Repeated actions after simulated tab sleep preserve the player and source');
  for(const type of ['audio','video']){await card(type);const src=await page.locator('.task-media').getAttribute('src');await page.locator('.task-media').evaluate(p=>{p.dataset.retained='yes';});now+=7200;await download();assert.equal(await page.locator('.task-media').getAttribute('src'),src);assert.equal(await page.locator('.task-media').getAttribute('data-retained'),'yes');}
  record('Audio and video players remain the same DOM element with the same source');await card();
  let held=await arm(),before=requests;await action('Download ↓').click();await held.used.promise;
  assert.equal(await action('Download ↓').isDisabled(),true);await page.evaluate(()=>document.querySelectorAll('.media-actions button').forEach(b=>b.click()));assert.equal(requests,before+1);const downloaded=page.waitForEvent('download');held.release.resolve();await downloaded;record('Pending authorization locks all actions against duplicate clicks');
  for(const code of [404,503]){unavailable=code;await action('Download ↓').click();await page.locator('.media-note [role=status]').filter({hasText:code===404?'no longer available':'Try again'}).waitFor();assert.equal(await action('Download ↓').isEnabled(),true);unavailable=0;await download();}
  unauthorized=true;await action('Download ↓').click();await page.locator('.media-note [role=status]').filter({hasText:'Try again'}).waitFor();unauthorized=false;await download();record('Deleted media, access denial and temporary errors show feedback and allow an explicit retry');
  for(const change of ['switch','forget','remove','return']){
   await card();held=await arm();const event=context.waitForEvent('page');await action('Open ↗').click();const tab=await event;const closed=tab.waitForEvent('close');await held.used.promise;const count=reads.length;
   if(change==='remove')await page.evaluate(()=>renderTasks([]));
   else if(change==='forget')await page.locator('#forgetGenerationKey').click();
   else {await key('fixture-key-b');if(change==='return')await key('fixture-key-a');}
   held.release.resolve();await closed;assert.equal(reads.length,count,'Late response navigated after context changed');if(change!=='return'&&change!=='remove')await key('fixture-key-a');
  }
  record('Late responses after key switch, Forget, A→B→A or card removal close the reserved tab without navigation');
  await card();
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await action('Download ↓').scrollIntoViewIfNeeded();await action('Download ↓').focus();const event=page.waitForEvent('download');await page.keyboard.press('Enter');await event;
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   for(const button of await page.locator('.media-actions button').all())assert.equal(await button.evaluate(b=>b.scrollWidth<=b.clientWidth),true);await page.screenshot({path:path.join(output,theme+'-'+width+'.png'),fullPage:true});
  }
  record('Keyboard activation, both themes and 1440/390/320px layouts');
  assert.ok(accessKeys.every(k=>k==='Bearer fixture-key-a'));assert.ok(reads.filter(r=>r.download||!previewAccesses.has(r.access)).every(r=>!r.expired));assert.ok(reads.filter(r=>!r.download&&!previewAccesses.has(r.access)).every(r=>!r.referrer));assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
  fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,results,errors,external,requests},null,2));
 }catch(error){await page.screenshot({path:path.join(output,'failure.png'),fullPage:true});throw error;}
 finally{gate?.release.resolve();await browser.close();await new Promise(r=>server.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
