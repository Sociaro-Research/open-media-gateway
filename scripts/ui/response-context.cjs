// Real response headers/body gates; every request stays on the local fixture.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {evidencePath}=require('./evidence.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const root=process.env.UI_SOURCE_DIR||path.resolve(__dirname,'../../web/public/ui');
const output=evidencePath('/tmp/gateway-response-context/'+engine+'/');
fs.mkdirSync(output,{recursive:true});
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};};
const catalog=key=>({data:[{id:'mock',name:'Mock',models:[{id:key,display_name:'Model '+key,media_type:'image'}],default_model:key}]});
let gate;
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture');
 if(url.pathname.startsWith('/v1/')){
  const key=(req.headers.authorization||'').replace('Bearer ','');
  let body={data:[]};
  if(url.pathname==='/v1/info')body={auth_mode:'api_key',provider:'mock'};
  if(url.pathname==='/v1/key')body={key:{name:key,resource_mode:'personal'}};
  if(url.pathname==='/v1/providers')body=catalog(key);
  if(url.pathname==='/v1/generations/estimate')body={cost:{expected_usd:0},can_generate:true};
  const active=gate;
  if(active&&!active.used&&url.pathname===active.path&&key==='key-a'){
   active.used=true;res.writeHead(active.status,{'Content-Type':active.blob?'image/png':'application/json'});
   res.write(active.blob?'first bytes':JSON.stringify(active.body||body).slice(0,1));
   await active.release.promise;
   return res.end(active.blob?' last bytes':JSON.stringify(active.body||body).slice(1));
  }
  res.writeHead(200,{'Content-Type':'application/json'});return res.end(JSON.stringify(body));
 }
 const file=path.join(root,url.pathname==='/'?'index.html':path.basename(url.pathname));
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),errors=[],external=[],results=[];
 page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(10000);
 await page.route('**/*',route=>{if(new URL(route.request().url()).origin===base)return route.continue();external.push(route.request().url());return route.abort();});
 await page.addInitScript(()=>{
  const fetch=window.fetch;window.bodyReads=[];
  window.fetch=async(...args)=>{const response=await fetch(...args);for(const method of ['json','blob']){const read=response[method].bind(response);response[method]=(...args)=>{window.bodyReads.push({path:new URL(response.url).pathname,method});return read(...args);};}return response;};
 });
 async function key(value){await page.locator('#apiKey').fill(value);await page.locator('#apiKey').press('Tab');await page.waitForFunction(value=>GenerationKey.value()===value,value);if(value)await page.waitForFunction(value=>selectedModel()?.id===value,value);}
 async function arm(path,status=200,body=null,blob=false){gate={path,status,body,blob,release:deferred(),used:false};return page.evaluate(()=>window.bodyReads.length);}
 async function reading(before,path){await page.waitForFunction(({before,path})=>window.bodyReads.slice(before).some(r=>r.path===path),{before,path});assert.equal(gate.used,true);}
 const record=name=>{results.push(name);console.log('PASS:',name);};
 try{
  await page.goto(base);await page.waitForFunction(()=>authState==='authenticated');await key('key-a');
  for(const status of [200,503]){
   const before=await arm('/v1/providers',status,status===503?{error:{message:'Old key request failed'}}:null);
   await page.evaluate(()=>{window.auditRead=loadProviders();});await reading(before,'/v1/providers');
   await key('key-b');gate.release.resolve();await page.evaluate(()=>window.auditRead);
   assert.equal(await page.evaluate(()=>selectedModel()?.id),'key-b','A late body changed the active key catalog');
   assert.doesNotMatch(await page.locator('#formMessage').innerText(),/Old key request failed/);
   record('Key A → B rejects the delayed '+status+' provider body and preserves B catalog');
   await key('key-a');
  }
  const returned=await arm('/v1/providers',200,catalog('obsolete-a'));
  await page.evaluate(()=>{window.auditRead=loadProviders();});await reading(returned,'/v1/providers');
  await key('key-b');await key('key-a');gate.release.resolve();await page.evaluate(()=>window.auditRead);
  assert.equal(await page.evaluate(()=>selectedModel()?.id),'key-a','A → B → A accepted an obsolete catalog from the first A request');
  record('Key A → B → A rejects the first A response and preserves the restored catalog');
  for(const blob of [false,true]){
   const pathname=blob?'/v1/generations/fixture/content':'/v1/generations/fixture';
   const before=await arm(pathname,200,{id:'old-task',prompt:'Old key result'},blob);
   await page.evaluate(({pathname,blob})=>{window.auditRead=api(pathname,blob?{responseType:'blob'}:{}).then(()=>({accepted:true}),error=>({error:error.name}));},{pathname,blob});
   await reading(before,pathname);await key('key-b');gate.release.resolve();
   assert.deepEqual(await page.evaluate(()=>window.auditRead),{error:'AbortError'});
   record('Key A → B rejects a delayed '+(blob?'media blob':'generation JSON')+' body');await key('key-a');
  }
  const cancelled=await arm('/v1/generations/fixture');
  await page.evaluate(()=>{window.auditController=new AbortController();window.auditRead=api('/v1/generations/fixture',{signal:window.auditController.signal}).then(()=>({accepted:true}),error=>({error:error.name}));});
  await reading(cancelled,'/v1/generations/fixture');await page.evaluate(()=>window.auditController.abort());
  assert.deepEqual(await page.evaluate(()=>window.auditRead),{error:'AbortError'});gate.release.resolve();
  record('Cancellation during a JSON body read remains AbortError instead of an empty success');
  const before=await arm('/v1/providers');await page.evaluate(()=>{window.auditRead=loadProviders();});await reading(before,'/v1/providers');
  await page.locator('#forgetGenerationKey').click();await page.waitForFunction(()=>!GenerationKey.value());gate.release.resolve();await page.evaluate(()=>window.auditRead);
  assert.deepEqual(await page.evaluate(()=>providerCatalog),[]);assert.equal(await page.locator('#model').isDisabled(),true);
  record('Forget key during body download leaves the model catalog empty');
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
  fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,results,errors,external},null,2));
 }catch(error){await page.screenshot({path:path.join(output,'failure.png'),fullPage:true});throw error;}
 finally{gate?.release.resolve();await browser.close();await new Promise(r=>server.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
