// Actual shared picker and renderer, loopback PNG fixtures; no credentials.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const ui=path.resolve(__dirname,'../../web/public/ui'),fixtures=require('./provider-logo-fixtures.cjs'),engine=process.env.UI_BROWSER_ENGINE||'chromium';
const out=process.env.UI_EVIDENCE_DIR||path.resolve('.local/logo-cache-evidence',engine);fs.mkdirSync(out,{recursive:true});
const counts={},responses=new Map(),gates=new Map(),sum=()=>Object.values(counts).reduce((a,b)=>a+b,0);
const html=`<!doctype html><html data-theme="dark"><head><meta charset="utf-8"><link rel="stylesheet" href="/ui/styles.css"></head><body><main><label>Model<select id="model"></select></label></main>${['provider-logos','model-info','model-picker','control-pickers','form-ui'].map(s=>'<script src="/ui/'+s+'.js"></script>').join('')}</body></html>`;
const server=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://fixture').pathname;
 if(p.startsWith('/provider-assets/')){counts[req.url]=(counts[req.url]||0)+1;const response=responses.get(p)||{status:200,data:fixtures.valid},send=()=>{res.writeHead(response.status,{'Content-Type':'image/png'});res.end(response.data);};const gate=gates.get(p);if(gate){gates.delete(p);gate.enter();gate.wait.then(send);}else send();return;}
 if(p==='/')return res.end(html);const file=path.join(ui,path.basename(p));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':'text/css');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await ({chromium,webkit}[engine]).launch({headless:true});
 try{
  const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(10000);
  await page.addInitScript(()=>{
   const create=URL.createObjectURL.bind(URL),revoke=URL.revokeObjectURL.bind(URL),decode=HTMLImageElement.prototype.decode;
   window.created=0;window.revoked=0;window.decodes=0;window.completed={};window.accessEpoch='A';
   URL.createObjectURL=value=>{created++;return create(value);};URL.revokeObjectURL=value=>{revoked++;return revoke(value);};HTMLImageElement.prototype.decode=function(){decodes++;return decode.call(this);};
   window.placeholderFrames=0;window.measureFrames=false;
   function frame(){if(measureFrames)for(const mark of document.querySelectorAll('.model-picker-provider-mark'))if(!mark.querySelector('img')?.naturalWidth)placeholderFrames++;requestAnimationFrame(frame);}requestAnimationFrame(frame);
  });
  const base='http://127.0.0.1:'+server.address().port;await page.goto(base);
  await page.evaluate(()=>{
   // Deliberately ignore transport abort to prove late-read invalidation guards.
   ProviderLogos.configure({request:async url=>{try{const r=await fetch(url,{cache:'no-store'});if(!r.ok)throw Error('Synthetic image '+r.status);return await r.blob();}finally{completed[url]=(completed[url]||0)+1;}},context:()=>accessEpoch});
   const render=ProviderLogos.image;window.logoRenderCalls=0;ProviderLogos.image=(...args)=>{logoRenderCalls++;return render(...args);};
   window.catalog=['a','b'].map(id=>({id,name:'Provider '+id,icon_url:'/provider-assets/owner/'+id+'/icon.png?v=1',models:['image','video'].map(media_type=>({id:media_type,display_name:media_type+' model',media_type}))}));ModelPicker.populate(document.querySelector('#model'),catalog);FormUI.refresh();
   window.mountLogo=(id,url)=>{let mark=document.getElementById(id);if(!mark){mark=document.createElement('div');mark.id=id;document.body.append(mark);}ProviderLogos.image(mark,{id,name:id,icon_url:url});};
  });
  const popup=page.locator('.model-select-popup'),settle=()=>page.waitForFunction(()=>document.querySelectorAll('.model-select-popup img').length===2&&[...document.querySelectorAll('.model-select-popup img')].every(img=>img.naturalWidth));
  async function open(){await page.getByRole('combobox',{name:'Model',exact:true}).click();await settle();}
  await open();const warm={requests:sum(),resources:await page.evaluate(()=>created),decodes:await page.evaluate(()=>decodes),renderCalls:await page.evaluate(()=>logoRenderCalls)};assert.deepEqual(warm,{requests:2,resources:2,decodes:2,renderCalls:2});await page.evaluate(()=>measureFrames=true);
  for(let i=0;i<3;i++){await page.keyboard.press('Escape');await open();}
  for(const value of ['image','video','']){await popup.getByRole('searchbox').fill(value);await settle();}
  await popup.locator('button[role=combobox][aria-label="Content type"]').click();await page.getByRole('option',{name:'Image',exact:true}).click();await settle();await popup.getByRole('button',{name:'Clear model filters'}).click();await settle();
  await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));await page.evaluate(()=>measureFrames=false);
  const after={requests:sum(),resources:await page.evaluate(()=>created),decodes:await page.evaluate(()=>decodes),placeholderFrames:await page.evaluate(()=>placeholderFrames),renderCalls:await page.evaluate(()=>logoRenderCalls)};assert.deepEqual({...after,renderCalls:2},{...warm,placeholderFrames:0});assert.ok(after.renderCalls>warm.renderCalls,'production picker rebuilt actual image marks');
  for(const theme of ['light','dark']){await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);assert.deepEqual(await popup.locator('img').evaluateAll(nodes=>nodes.map(n=>getComputedStyle(n).filter)),[theme==='light'?'brightness(0)':'none',theme==='light'?'brightness(0)':'none']);await popup.screenshot({path:path.join(out,'warm-picker-'+theme+'.png')});}
  for(const width of [390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(r)));assert.ok(await popup.evaluate(n=>n.getBoundingClientRect().width<=innerWidth));await popup.screenshot({path:path.join(out,'warm-picker-'+width+'.png')});}await page.setViewportSize({width:1440,height:1000});
  await page.keyboard.press('Escape');
  const mount=(id,url)=>page.evaluate(({id,url})=>mountLogo(id,url),{id,url});const ready=id=>page.waitForFunction(id=>document.getElementById(id)?.dataset.logoState==='ready'&&document.getElementById(id).querySelector('img')?.naturalWidth,id);
  function hold(p){let enter,release;const entered=new Promise(r=>enter=r),wait=new Promise(r=>release=r);gates.set(p,{enter,wait});return{entered,release};}
  const slow='/provider-assets/owner/slow/icon.png?v=1',slowGate=hold(slow.split('?')[0]);await Promise.all([mount('slow1',slow),mount('slow2',slow)]);await slowGate.entered;assert.equal(counts[slow],1);slowGate.release();await Promise.all([ready('slow1'),ready('slow2')]);assert.equal(counts[slow],1,'concurrent cold callers share one request');
  for(const [id,status,data]of [['network',500,fixtures.valid],['decode',200,Buffer.from('not a PNG')]]){
   const p='/provider-assets/owner/'+id+'/icon.png',url=p+'?v=1';responses.set(p,{status,data});await mount(id,url);await page.waitForFunction(id=>document.getElementById(id).dataset.logoState==='error',id);assert.match(await page.locator('#'+id).getAttribute('title'),/retry/i);responses.delete(p);await mount(id,url);await ready(id);assert.equal(counts[url],2,'failed entry retries instead of staying cached');
  }
  const one=fixtures.make({width:1,pixels:[255,255,255,127]}),hidden='/provider-assets/owner/hidden/icon.png?v=1';await mount('hidden',hidden);await ready('hidden');await page.locator('#hidden').evaluate(n=>n.remove());responses.set(hidden.split('?')[0],{status:200,data:one});await page.evaluate(url=>ProviderLogos.refresh(url.replace('v=1','v=2')),hidden);await mount('hidden',hidden);await ready('hidden');assert.equal(await page.locator('#hidden img').evaluate(n=>n.naturalWidth),1);assert.equal(counts[hidden],2,'invalidation also clears images with no displayed mark');
  responses.delete(hidden.split('?')[0]);await page.evaluate(url=>ProviderLogos.refresh(url),hidden);await ready('hidden');assert.equal(await page.locator('#hidden img').evaluate(n=>n.naturalWidth),3,'A→B→A invalidates stale version aliases');
  await page.evaluate(url=>ProviderLogos.refresh(url,true),hidden);assert.equal(await page.locator('#hidden img').count(),0);await page.evaluate(url=>ProviderLogos.refresh(url),hidden);await ready('hidden');assert.equal(counts[hidden],4,'same-version upload after removal does not resurrect old cache');
  const pending='/provider-assets/owner/context/icon.png?v=1',pendingPath=pending.split('?')[0],old=hold(pendingPath);await mount('context',pending);await old.entered;
  await page.evaluate(()=>{accessEpoch='B';ProviderLogos.prune();});responses.set(pendingPath,{status:200,data:one});await mount('context',pending);await ready('context');await page.evaluate(()=>{accessEpoch='A';ProviderLogos.prune();});await mount('context',pending);await ready('context');old.release();await page.waitForFunction(url=>completed[url]===3,pending);assert.equal(await page.locator('#context img').evaluate(n=>n.naturalWidth),1,'late A read cannot repaint or recache after A→B→A');
  await page.evaluate(()=>{window.dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}));window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));});await ready('hidden');assert.equal(await page.locator('#hidden img').evaluate(n=>n.naturalWidth),3,'BFCache restore rehydrates previously installed logos');
  const held=[];for(let i=0;i<5;i++)held.push(hold('/provider-assets/owner/pool'+i+'/icon.png'));await Promise.all(held.map((_,i)=>mount('pool'+i,'/provider-assets/owner/pool'+i+'/icon.png?v=1')));await Promise.all(held.slice(0,4).map(g=>g.entered));assert.equal(counts['/provider-assets/owner/pool4/icon.png?v=1'],undefined,'stalled fetch/decode concurrency is bounded');held[0].release();await held[4].entered;held.forEach(g=>g.release());await Promise.all(held.map((_,i)=>ready('pool'+i)));
  await page.evaluate(()=>ProviderLogos.clear());await page.waitForFunction(()=>created===revoked);for(let i=0;i<35;i++){const id='evict'+i;await mount(id,'/provider-assets/owner/'+id+'/icon.png?v=1');await ready(id);await page.locator('#'+id).evaluate(n=>n.remove());}await page.evaluate(()=>new Promise(r=>setTimeout(r,0)));assert.ok(await page.evaluate(()=>created-revoked<=32),'bounded retained resources');await page.evaluate(()=>ProviderLogos.clear());await page.waitForFunction(()=>created===revoked);
  assert.deepEqual(errors,[]);const result={engine,warm,after,concurrent:'one fetch',failureRetry:'passed',replaceDelete:'passed',authEpochRace:'passed',boundedResources:'passed',coldConcurrency:4,bfcacheRestore:'passed',errors};fs.writeFileSync(path.join(out,'results.json'),JSON.stringify(result,null,2));console.log('PASS '+JSON.stringify(result));
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
