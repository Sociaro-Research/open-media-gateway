// Actual request helper + archived media renderer; all routes are synthetic.
const {chromium,webkit}=require('playwright'),fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const root=path.resolve(process.env.UI_SOURCE_DIR||'web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',baseline=process.env.UI_BASELINE==='1';
const out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/portal-errors/'+engine);fs.mkdirSync(out,{recursive:true});
const app=fs.readFileSync(path.join(root,'app.js'),'utf8'),api=app.slice(app.indexOf('async function api('),app.indexOf('\nProviderLogos.configure('));
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNoaGgAAAMEAYFL09IQAAAAAElFTkSuQmCC','base64');
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}};
// WebKit downloads leave route interception; serve the actual attachment bytes
// on loopback so both engines must emit a real download event.
const server=http.createServer((req,res)=>{
 const url=new URL(req.url,'http://fixture');
 if(url.pathname==='/signed/file'&&url.searchParams.has('download'))return res.writeHead(200,{'Content-Type':'application/octet-stream','Content-Disposition':'attachment; filename=synthetic.bin'}).end('synthetic');
 res.writeHead(404).end();
});
const html=`<!doctype html><html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><body><main class="shell"><section class="panel"><h2>Journal</h2><article id="task"><div class="task-result"></div></article></section><section class="panel"><h2>Settings</h2><button id="settings" class="secondary">Save settings</button><p id="settings-status" class="form-message" role="status"></p></section><section class="panel"><h2>Keys</h2><button id="keys" class="secondary">Create key</button><p id="keys-status" class="form-message" role="status"></p></section></main><script src="/generation-artifacts.js"></script><script>
let sharedConfiguration=false,authMode='google',generationKeyVersion=0;
window.GenerationKey={ensureSession:async()=>{},value:()=> 'synthetic-key',forget:()=>{}};window.ProviderLogos={clear:()=>{}};
${api}
window.render=kind=>{const article=document.querySelector('#task');article.mediaController?.abort();article.querySelector('.task-result').replaceChildren();return GenerationArtifacts.attach(article,{id:'synthetic',artifacts:[{id:'1',media_type:kind,content_type:kind==='text'?'text/plain':'image/png',format:kind==='text'?'txt':'png'}]},{api,context:()=>generationKeyVersion,bytes:n=>n+' bytes'});};
for(const id of ['settings','keys'])document.querySelector('#'+id).onclick=async()=>{try{await api(id==='settings'?'/v1/system/settings':'/v1/api-keys',{method:id==='settings'?'PATCH':'POST',body:'{}'});document.querySelector('#'+id+'-status').textContent='Saved';}catch(e){document.querySelector('#'+id+'-status').textContent=e.message;}};
</script></html>`;
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],external=[],checks=[],metrics=[];page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));
 let issue=null,accessGate=null,textGate=null,textStatus=200,reads=0,grant=0;
 await page.route('**/*',async route=>{
  const url=new URL(route.request().url());if(url.origin!==base){external.push(url.origin);return route.abort();}
  if(url.pathname==='/')return route.fulfill({contentType:'text/html',body:html});
  if(url.pathname.startsWith('/v1/')){
   if(url.pathname.endsWith('/media-access')){reads++;if(accessGate){accessGate.started?.resolve();await accessGate.promise;}}
   if(issue?.network)return route.abort('failed');
   if(issue)return route.fulfill({status:issue.status||503,contentType:issue.raw?'text/html':'application/json',body:issue.raw||JSON.stringify(Object.hasOwn(issue,'body')?issue.body:{error:{message:issue.message,code:'safe_code',param:null}})});
   const token=++grant;return route.fulfill({contentType:'application/json',body:JSON.stringify({artifacts:[{id:'1',preview_url:'/signed/file?grant='+token+'&variant=%2F',download_url:'/signed/file?grant='+token+'&download=1'}]})});
  }
  if(url.pathname==='/signed/file'){
   if(url.searchParams.has('download'))return route.continue();
   if(textGate){const held=textGate;held.started?.resolve();await held.promise;try{return await route.fulfill({status:textStatus,contentType:'text/plain',body:'Safe synthetic text'});}catch{return;}}
   return route.fulfill({status:textStatus,contentType:'image/png',body:png});
  }
  const file=path.join(root,path.basename(url.pathname));if(fs.existsSync(file))return route.fulfill({path:file,contentType:file.endsWith('.js')?'text/javascript':'text/css'});return route.fulfill({status:404,body:''});
 });
 const note=()=>page.locator('.media-note'),download=()=>page.getByRole('button',{name:'Download ↓',exact:true}),retry=()=>page.getByRole('button',{name:'Retry preview',exact:true});
 async function activate(button){await button.click();await page.waitForFunction(()=>[...document.querySelectorAll('.media-actions button')].every(b=>!b.disabled));}
 const pass=name=>{checks.push(name);console.log('PASS:',name)};
 async function screenshots(label){for(const theme of ['dark','light'])for(const width of [1440,390,320]){
  await page.setViewportSize({width,height:1000});await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
  await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
  const m=await note().evaluate(n=>({right:n.getBoundingClientRect().right,height:n.getBoundingClientRect().height,scroll:n.scrollHeight,client:n.clientHeight,font:getComputedStyle(n).fontSize,color:getComputedStyle(n).color,actions:[...n.parentElement.querySelectorAll('button')].filter(b=>!b.hidden).map(b=>({height:b.getBoundingClientRect().height,right:b.getBoundingClientRect().right}))}));
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);assert.ok(m.right<=width);assert.ok(m.scroll<=m.client+1);for(const b of m.actions){assert.ok(b.height>=44);assert.ok(b.right<=width);}metrics.push({label,theme,width,...m});await page.screenshot({path:path.join(out,`${label}-${theme}-${width}.png`),fullPage:true});
 }}
 try{
  await page.goto(base);issue={status:401,message:'Sign in again to access saved media.'};await page.evaluate(()=>render('image'));
  assert.match(await note().textContent(),baseline?/Preview unavailable\. Try again\./:/Preview unavailable\. Sign in again/);await screenshots('sign-in');
  issue=null;await retry().click();await page.waitForFunction(()=>document.querySelector('img')?.naturalWidth===1);const original=await page.locator('img').elementHandle();
  issue={status:503,message:'Storage is temporarily unavailable. Ask your administrator to check the storage connection.'};await activate(download());
  assert.match(await note().textContent(),baseline?/Preview unavailable\. Try again\./:/Download unavailable\. Storage is temporarily unavailable/);
  await screenshots('download-error');pass('401 Preview and503 Download rendered before/after diagnosis');
  if(!baseline){
   for(const status of [401,403,503])for(const action of ['Open ↗','Download ↓']){
    issue={status,message:status===401?'Sign in again.':status===403?'Ask an administrator for access.':'Storage is temporarily unavailable.'};
    const popupWait=action==='Open ↗'?page.waitForEvent('popup'):null;await activate(page.getByRole('button',{name:action,exact:true}));const popup=popupWait?await popupWait:null;if(popup&&!popup.isClosed())await popup.waitForEvent('close');
    assert.match(await note().textContent(),new RegExp((action==='Download ↓'?'Download':'Preview')+' unavailable\\. '+issue.message.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')));
   }pass('safe401/403/503 diagnosis preserved, failed popup closed, correct action');
   for(const fault of [{network:true},{status:502,raw:'<html>private-token https://private.invalid /private/media/file</html>'},{status:200,body:null}]){
    issue=fault;await activate(download());assert.match(await note().textContent(),/Download unavailable/);assert.doesNotMatch(await note().textContent(),/private-token|private.invalid|\/private\/media/);
   }pass('network/proxy HTML safe fallback');
   issue={status:200,body:{artifacts:[null]}};await activate(download());assert.match(await note().textContent(),/unreadable response/);
   issue={status:503,message:'Safe saved-media diagnostic'};textGate={...deferred(),started:deferred()};await page.evaluate(()=>{window.mount=render('text')});await page.evaluate(()=>mount); // access failed: no text request yet
   issue=null;await retry().click();await page.waitForFunction(()=>document.querySelector('.media-note').textContent.includes('Loading text'));
   issue={status:503,message:'Download must retain this diagnosis.'};await activate(download());assert.match(await note().textContent(),/Download must retain/);
   await textGate.started.promise;textGate.resolve();textGate=null;await page.waitForFunction(()=>document.querySelector('.artifact-text').textContent==='Safe synthetic text');assert.match(await note().textContent(),/Download must retain/);pass('late successful text preview cannot erase Download error');
   issue=null;const downloadWait=page.waitForEvent('download');await activate(download());const saved=await downloadWait;assert.match(saved.url(),/grant=\d+&download=1$/);assert.doesNotMatch(await note().textContent(),/Download must retain/);
   textStatus=403;textGate={...deferred(),started:deferred()};await page.evaluate(()=>{window.mount=render('text')});await page.evaluate(()=>mount);await textGate.started.promise;textGate.resolve();textGate=null;await page.waitForFunction(()=>document.querySelector('.media-note').textContent.includes('fresh link'));assert.doesNotMatch(await note().textContent(),/Sign in/);
   const successWait=page.waitForEvent('download');await activate(download());await successWait;assert.match(await note().textContent(),/fresh link/);pass('expired signed preview requests fresh link; successful Download retains Preview error');
   textStatus=200;issue=null;await page.evaluate(()=>render('image'));await page.waitForFunction(()=>document.querySelector('img')?.naturalWidth===1);
   accessGate={...deferred(),started:deferred()};const before=reads;await page.evaluate(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent==='Download ↓');b.onclick();b.onclick();});await accessGate.started.promise;assert.equal(reads,before+1);
   await page.evaluate(()=>{generationKeyVersion++;document.querySelector('#task').mediaController.abort()});accessGate.resolve();accessGate=null;
   await page.waitForFunction(()=>!document.querySelector('img').hasAttribute('src'));assert.equal((await original.evaluate(n=>n.isConnected)),false);pass('pending duplicates suppressed; interruption clears media and ignores late response');
   issue={status:404,message:'Not found'};await page.evaluate(()=>render('image'));assert.match(await note().textContent(),/no longer available/);assert.equal(await page.locator('img').getAttribute('src'),null);issue=null;await retry().focus();await retry().press('Enter');await page.waitForFunction(()=>document.querySelector('img')?.naturalWidth===1);pass('deleted/missing media stays opaque; explicit keyboard recovery succeeds');
   for(const id of ['settings','keys'])for(const fault of [{network:true},{status:502,raw:'<html>private-token /private/media/file</html>'},{status:400,message:'Correct the named field; use the supported value.'}]){
    issue=fault;await page.locator('#'+id).click();const status=page.locator('#'+id+'-status');await status.filter({hasText:fault.message||(/unconfirmed/)}).waitFor();assert.doesNotMatch(await status.textContent(),/private-token|\/private\/media/);
   }await screenshots('adjacent-errors');pass('shared helper renders network, HTTP502 and safe JSON on settings/key actions');
   issue={status:503,message:'<img src=x onerror="window.hostile=true"> safe literal diagnosis'};await activate(download());assert.equal(await page.locator('.media-note img').count(),0);assert.equal(await page.evaluate(()=>window.hostile),undefined);pass('JSON text is rendered as text, never injected HTML');
  }
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
  fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({engine,baseline,checks,metrics,errors,external,reads,hashes:Object.fromEntries(['app.js','generation-artifacts.js','styles.css'].map(f=>[f,crypto.createHash('sha256').update(fs.readFileSync(path.join(root,f))).digest('hex')]))},null,2));
 }catch(error){await page.screenshot({path:path.join(out,'failure.png'),fullPage:true});throw error;}
 finally{accessGate?.resolve();textGate?.resolve();await browser.close();server.closeAllConnections();server.close();}
})().catch(e=>{console.error(e);server.closeAllConnections();server.close();process.exitCode=1});
