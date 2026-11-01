// Real authenticated Go API + PostgreSQL, launched by the opt-in Go test.
const {chromium,webkit}=require('playwright'),{PNG}=require('pngjs');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http'),{spawn}=require('node:child_process');
const fixtures=require('./provider-logo-fixtures.cjs');
const listen=s=>new Promise(r=>s.listen(0,'127.0.0.1',()=>r(s.address().port)));
(async()=>{
 const [upstream,token,owner,phase,foreignToken]=process.argv.slice(2),engine=process.env.UI_BROWSER_ENGINE||'chromium';
 assert.equal(new URL(upstream).hostname,'127.0.0.1');
 const out=process.env.UI_EVIDENCE_DIR||path.resolve('.local/logo-evidence',engine);fs.mkdirSync(out,{recursive:true});
 let base=upstream,caddy,browser;
 try {
  if(process.env.CADDY_BIN){
   const reservation=http.createServer(),port=await listen(reservation);await new Promise(r=>reservation.close(r));
   const log=fs.openSync(path.join(out,phase+'-caddy.log'),'w');
   caddy=spawn(process.env.CADDY_BIN,['run','--config',path.resolve('web/Caddyfile'),'--adapter','caddyfile'],{env:{...process.env,UI_ROOT:path.resolve('web/public'),GATEWAY_UPSTREAM:new URL(upstream).host,UI_LISTEN:':'+port,DOCS_HOST:'docs.invalid'},stdio:['ignore',log,log]});fs.closeSync(log);base='http://127.0.0.1:'+port;
   let ready=false;for(let i=0;i<100;i++){try{const r=await fetch(base+'/site.json');if(r.ok){ready=true;break}}catch{}await new Promise(r=>setTimeout(r,50));}assert.ok(ready,'isolated Caddy did not start');
  }
  browser=await ({chromium,webkit}[engine]).launch({headless:true,...(engine==='chromium'&&process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
  const context=await browser.newContext({viewport:{width:1440,height:1050}}),errors=[];
  await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
  await context.addCookies([{name:'media_gateway_session',value:token,url:base,httpOnly:true,sameSite:'Lax'}]);
  const page=await context.newPage();page.setDefaultTimeout(12000);page.on('pageerror',e=>{if(!e.message.includes('ResizeObserver loop'))errors.push(e.message)});
  const keyResponse=await context.request.post(base+'/v1/api-keys',{data:{name:'Synthetic logo browser key',resource_mode:'personal',scopes:['generation','providers:manage','system:manage']}});
  assert.equal(keyResponse.status(),201,await keyResponse.text());const {secret}=await keyResponse.json();
  await page.goto(base+'/profile');await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());

  await page.evaluate(secret=>{const input=document.querySelector('#apiKey');input.value=secret;input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));},secret);
  await page.waitForFunction(()=>GenerationKey.value() && elements.quality.options.length===2);
  await page.evaluate(()=>{elements.quality.value='high';elements.resolution.value='1024x1024';elements.background.value='transparent';elements.format.value='webp';});
  const generationDraft=await page.evaluate(()=>[elements.quality.value,elements.resolution.value,elements.background.value,elements.format.value]);
  await page.getByRole('tab',{name:'Providers',exact:true}).click();
  const row=id=>page.locator(`[data-provider-id="${id}"]`),editor=()=>page.locator('#providerLogoEditor'),input=()=>editor().locator('input[type=file]');
  async function select(id){await row(id).click();await editor().getByRole('button',{name:/^Upload logo/}).waitFor();}
  async function upload(buffer,name='logo.png',mimeType='image/png'){await input().setInputFiles({name,mimeType,buffer});await editor().getByRole('status').filter({hasText:'Logo uploaded.'}).waitFor();await editor().locator('img').evaluate(img=>img.decode());}
  async function drop(files){
   const transfer=await page.evaluateHandle(files=>{const data=new DataTransfer();for(const f of files)data.items.add(new File([new Uint8Array(f.bytes)],f.name,{type:f.type}));return data;},files.map(f=>({name:f.name||'logo.png',type:f.type||'image/png',bytes:[...f.buffer]})));
   const zone=editor().locator('.reference-dropzone');await zone.dispatchEvent('dragenter',{dataTransfer:transfer});assert.ok((await zone.getAttribute('class')).includes('dragover'));await zone.dispatchEvent('dragover',{dataTransfer:transfer});await zone.dispatchEvent('drop',{dataTransfer:transfer});await transfer.dispose();
  }
  const iconURL=async id=>{const r=await context.request.get(base+'/v1/admin/providers/'+id);assert.equal(r.status(),200);return (await r.json()).icon_url;};
  if(phase==='upload'){
   for(const id of ['openai','sonilo']){await select(id);assert.equal(await editor().locator('img').count(),0);assert.match(await editor().innerText(),/No logo/);assert.equal(await row(id).locator('img').count(),0);}
   await select('openai');await editor().screenshot({path:path.join(out,'empty-upload-control.png')});
   // Browser-side failures must not send a mutation; each error is distinct.
   let writes=0;page.on('request',r=>{if(r.method()==='PUT'&&r.url().includes('/logo'))writes++});
   for(const [label,buffer,name,mimeType,message] of [
    ['renamed SVG',Buffer.from('<svg onload="alert(1)"/>'),'logo.png','image/png','unsupported'],
    ['MIME mismatch',fixtures.valid,'logo.png','image/jpeg','unsupported'],
    ['wrong extension',fixtures.valid,'logo.jpg','image/png','unsupported'],
    ['malformed',Buffer.from([137,80,78,71,13,10,26,10]),'logo.png','image/png','Invalid PNG'],
    ['opaque RGB',fixtures.opaqueRGB,'logo.png','image/png','contain transparency'],
    ['opaque RGBA',fixtures.opaqueRGBA,'logo.png','image/png','contain transparency'],
    ['opaque16',fixtures.opaque16,'logo.png','image/png','contain transparency'],
    ['oversize',Buffer.alloc(1048577),'logo.png','image/png','1 MiB'],
    ['dimensions',fixtures.make({width:2049}),'logo.png','image/png','dimensions']
   ]){await input().setInputFiles({name,mimeType,buffer});await editor().getByRole('status').filter({hasText:message}).waitFor();assert.equal(writes,0,label+' reached server');assert.equal(await input().isEnabled(),true);assert.equal(await editor().locator('img').count(),0);}
   // Independently exercise the real server with all same content failures.
   for(const [buffer,mimeType,message] of [[Buffer.from('<svg/>'),'image/png','unsupported'],[fixtures.valid,'image/jpeg','Content-Type'],[fixtures.valid.subarray(0,35),'image/png','invalid PNG'],[fixtures.opaqueRGB,'image/png','transparency'],[fixtures.opaqueRGBA,'image/png','transparency'],[fixtures.opaque16,'image/png','transparency'],[Buffer.alloc(1048577),'image/png','1 MiB'],[fixtures.make({width:2049}),'image/png','dimensions']]){
    const r=await context.request.put(base+'/v1/admin/providers/openai/logo',{data:buffer,headers:{'Content-Type':mimeType}});assert.equal(r.status(),400);assert.match(await r.text(),new RegExp(message));assert.equal(await iconURL('openai'),undefined);
   }
   // Valid16-bit near-opaque alpha must pass both validators.
   const chooserReady=page.waitForEvent('filechooser');await editor().getByRole('button',{name:/^Upload logo/}).press('Space');await (await chooserReady).setFiles({name:'logo.png',mimeType:'image/png',buffer:fixtures.nearOpaque16});await editor().getByRole('status').filter({hasText:'Logo uploaded.'}).waitFor();let previous=await iconURL('openai');assert.ok(previous);
   await upload(fixtures.valid);assert.notEqual(await iconURL('openai'),previous);previous=await iconURL('openai');
   await select('sonilo');await drop([{buffer:fixtures.palette}]);await editor().getByRole('status').filter({hasText:'Logo uploaded.'}).waitFor();const second=await iconURL('sonilo');assert.ok(second);assert.equal(await iconURL('openai'),previous);
   await select('openai');await drop([{buffer:fixtures.valid},{buffer:fixtures.palette}]);await editor().getByRole('status').filter({hasText:'Choose one file'}).waitFor();assert.equal(await iconURL('openai'),previous);
   await select('openai');const draft=page.locator('[data-provider-field=api_key]');await draft.fill('synthetic-unsaved-draft');
   let entered,release;const gate=new Promise(r=>release=r),arrived=new Promise(r=>entered=r);
   await page.route('**/v1/admin/providers/openai/logo',async route=>{entered();await gate;await route.fulfill({status:503,json:{error:{message:'Synthetic storage interruption'}}});},{times:1});
   await input().setInputFiles({name:'logo.png',mimeType:'image/png',buffer:fixtures.valid});await arrived;
   assert.equal(await input().isDisabled(),true);assert.match(await editor().getByRole('status').innerText(),/Uploading/);release();await editor().getByRole('status').filter({hasText:'Synthetic storage interruption'}).waitFor();assert.equal(await iconURL('openai'),previous);assert.equal(await draft.inputValue(),'synthetic-unsaved-draft');
   // A transport abort keeps the confirmed preview and supports same-file retry.
   await page.route('**/v1/admin/providers/openai/logo',route=>route.abort(),{times:1});await input().setInputFiles({name:'logo.png',mimeType:'image/png',buffer:fixtures.valid});await editor().getByRole('status').filter({hasText:'retry'}).waitFor();assert.equal(await iconURL('openai'),previous);
   await upload(fixtures.palette);assert.equal(await draft.inputValue(),'synthetic-unsaved-draft');assert.equal(await iconURL('sonilo'),second);await upload(fixtures.valid);
   // A pending write remains locked across A -> B -> A; its success reconciles
   // the new editor and list without replacing newer drafts or issuing twice.
   let enterWrite,finishWrite;const writeEntered=new Promise(r=>enterWrite=r),writeGate=new Promise(r=>finishWrite=r);
   await page.route('**/v1/admin/providers/openai/logo',async route=>{enterWrite();await writeGate;await route.continue();},{times:1});
   await input().setInputFiles({name:'logo.png',mimeType:'image/png',buffer:fixtures.palette});await writeEntered;
   const beforeCount=writes;await select('sonilo');await select('openai');assert.equal(await input().isDisabled(),true);assert.match(await editor().getByRole('status').innerText(),/Uploading/);
   finishWrite();await editor().getByRole('status').filter({hasText:'Logo uploaded.'}).waitFor();assert.equal(await input().isEnabled(),true);assert.equal(writes,beforeCount);await editor().locator('img').evaluate(i=>i.decode());
   assert.equal(await editor().locator('img').getAttribute('data-logo-url'),await iconURL('openai'));await upload(fixtures.valid);
   const finalURL=await iconURL('openai');await page.waitForFunction(url=>providerCatalog.find(p=>p.id==='openai')?.icon_url?.includes(new URL(url,location.origin).searchParams.get('v')),finalURL);
   assert.deepEqual(await page.evaluate(()=>[elements.quality.value,elements.resolution.value,elements.background.value,elements.format.value]),generationDraft,'logo refresh preserves generation draft');
   await page.getByRole('button',{name:'Add model',exact:true}).click();const profileDialog=page.locator('dialog[open]');await profileDialog.locator('button[role=combobox][aria-label="API request profile"]').click();
   await page.locator('.model-select-popup img').evaluate(img=>img.decode());assert.equal(await page.locator('.model-select-popup img').getAttribute('data-logo-url'),finalURL,'API profile dialog uses newly uploaded connection logo');
   await page.keyboard.press('Escape');if(await profileDialog.count())await profileDialog.getByRole('button',{name:'Cancel',exact:true}).press('Enter');
   // Hold a real old image body across a successful replacement. Refresh must
   // invalidate pending reads too, before any img element has been appended.
   const stale=await page.evaluate(()=>providerCatalog.find(p=>p.id==='openai').icon_url.replace(/v=[^&]+/,'v=held-old'));
   let gotOld,releaseOld,deliveredOld;const oldReady=new Promise(r=>gotOld=r),oldGate=new Promise(r=>releaseOld=r),oldDone=new Promise(r=>deliveredOld=r);
   await page.route(url=>url.href===base+stale,async route=>{const old=await route.fetch();gotOld();await oldGate;try{await route.fulfill({response:old});}catch{}finally{deliveredOld();}},{times:1});
   await page.evaluate(url=>{const mark=document.createElement('div');mark.id='pendingLogoProof';document.body.append(mark);ProviderLogos.image(mark,{name:'OpenAI',icon_url:url});},stale);await oldReady;
   await upload(fixtures.palette);const replacement=await iconURL('openai');await page.waitForFunction(url=>document.querySelector('#pendingLogoProof img')?.dataset.logoUrl.includes(new URL(url,location.origin).searchParams.get('v')),replacement);
   releaseOld();await oldDone;await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   assert.ok((await page.locator('#pendingLogoProof img').getAttribute('data-logo-url')).includes(new URL(replacement,base).searchParams.get('v')),'late old body cannot replace confirmed logo');await upload(fixtures.valid);



  } else {
   await select('openai');assert.equal(await iconURL('openai'),undefined);assert.equal(await editor().locator('img').count(),0);assert.equal(await editor().getByRole('button',{name:/^Remove logo/}).count(),0);await upload(fixtures.valid);await select('sonilo');await editor().locator('img').evaluate(img=>img.decode());assert.ok((await iconURL('sonilo')).includes('/'+owner+'/sonilo/icon.png?v='));
  }
  // Cookie, bearer-only (through real Caddy), anonymous and foreign-owner reads.
  const logo=await iconURL('openai');let response=await context.request.get(base+logo);assert.equal(response.status(),200);assert.deepEqual(await response.body(),fixtures.valid);
  const bearer=await browser.newContext();response=await bearer.request.get(base+logo,{headers:{Authorization:'Bearer '+secret}});assert.equal(response.status(),200);assert.deepEqual(await response.body(),fixtures.valid);
  assert.equal((await bearer.request.get(base+logo)).status(),401);assert.equal((await context.request.get(base+logo.replace(owner,'11111111-1111-4111-8111-111111111111'))).status(),404);await bearer.close();
  // API-key-only mode must isolate memory even when a URL has no generation hint.
  // All keys/users are disposable; no screenshots include key input fields.
  const foreignContext=await browser.newContext();await foreignContext.addCookies([{name:'media_gateway_session',value:foreignToken,url:base,httpOnly:true,sameSite:'Lax'}]);
  const foreignKeyResponse=await foreignContext.request.post(base+'/v1/api-keys',{data:{name:'Other synthetic logo owner',resource_mode:'personal',scopes:['generation']}});assert.equal(foreignKeyResponse.status(),201);const foreignSecret=(await foreignKeyResponse.json()).secret;await foreignContext.close();
  const accessPage=await context.newPage();await accessPage.goto(base+'/profile');await accessPage.waitForFunction(()=>consoleInitialized&&Workspace.ready());
  await accessPage.evaluate(()=>{authMode='api_key';GenerationKey.initialize(null);});
  async function connectAccess(key){await accessPage.evaluate(key=>{const input=document.querySelector('#apiKey');input.value=key;input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));},key);await accessPage.waitForFunction(key=>GenerationKey.value()===key,key);}
  await connectAccess(secret);let ownReads=0,foreignReads=0;accessPage.on('request',r=>{if(r.url()===base+logo){if(r.headers().authorization==='Bearer '+secret)ownReads++;if(r.headers().authorization==='Bearer '+foreignSecret)foreignReads++;}});
  const accessMount=()=>accessPage.evaluate(url=>{let mark=document.querySelector('#accessLogoProof');if(!mark){mark=document.createElement('div');mark.id='accessLogoProof';document.body.append(mark);}ProviderLogos.image(mark,{name:'Private logo',icon_url:url});},logo);
  await accessMount();await accessPage.waitForFunction(()=>document.querySelector('#accessLogoProof img')?.naturalWidth);await accessMount();assert.equal(ownReads,1,'same key reuses decoded no-hint resource');
  await connectAccess(foreignSecret);assert.equal(await accessPage.locator('#accessLogoProof img').count(),0,'key change clears old visible bytes');await accessMount();await accessPage.waitForFunction(()=>document.querySelector('#accessLogoProof').dataset.logoState==='error');assert.equal(foreignReads,1);assert.equal(await accessPage.locator('#accessLogoProof img').count(),0,'foreign key cannot reuse other owner bytes');
  await connectAccess(secret);await accessMount();await accessPage.waitForFunction(()=>document.querySelector('#accessLogoProof img')?.naturalWidth);assert.equal(ownReads,2,'returning key must authorize a fresh resource');await accessPage.close();
  // All actual list/preview/picker surfaces share the same computed rendering.
  await select('openai');
  const catalogLogo=await page.evaluate(()=>providerCatalog.find(p=>p.id==='openai').icon_url);assert.match(catalogLogo,/generation=1/);
  // An already visible picker image must refresh without losing its key context.
  const bearerRead=page.waitForRequest(r=>r.url()===base+catalogLogo.replace(/v=[^&]+/,'v=stale')&&r.headers().authorization==='Bearer '+secret);
  await page.evaluate(url=>{const mark=document.createElement('div');mark.id='refreshLogoProof';document.body.append(mark);ProviderLogos.image(mark,{name:'OpenAI',icon_url:url.replace(/v=[^&]+/,'v=stale')});},catalogLogo);await bearerRead;
  await page.locator('#refreshLogoProof img').evaluate(img=>img.decode());
  await page.evaluate(url=>ProviderLogos.refresh(url),catalogLogo);await page.waitForFunction(url=>document.querySelector('#refreshLogoProof img')?.dataset.logoUrl===url,catalogLogo);
  for(const theme of ['dark','light','dark']){
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   for(const id of ['openai','sonilo']){await row(id).locator('img').evaluate(i=>i.decode());assert.equal(await row(id).locator('img').evaluate(i=>getComputedStyle(i).filter),theme==='light'?'brightness(0)':'none');}
   assert.equal(await editor().locator('img').evaluate(i=>getComputedStyle(i).filter),theme==='light'?'brightness(0)':'none');
  }
  // Render actual image pixels through the production CSS at original resolution.
  const pixels=await browser.newPage();await pixels.goto(base+'/profile');await pixels.evaluate(async data=>{
   document.body.replaceChildren();document.documentElement.style.background='transparent';document.body.style.background='transparent';document.body.style.margin='0';
   const img=new Image();img.className='provider-logo';img.style.cssText='width:3px;height:1px;display:block;position:absolute;left:0;top:0;';img.src='data:image/png;base64,'+data;document.body.append(img);await img.decode();
  },fixtures.valid.toString('base64'));
  for(const theme of ['dark','light','dark']){await pixels.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);const shot=PNG.sync.read(await pixels.locator('img').screenshot({omitBackground:true}));assert.deepEqual([...shot.data].filter((_,i)=>i%4===3),[0,127,255]);for(const index of [1,2])for(let channel=0;channel<3;channel++)assert.equal(shot.data[index*4+channel],theme==='light'?0:255);fs.writeFileSync(path.join(out,'pixels-'+theme+'.png'),PNG.sync.write(shot));}await pixels.close();
  // Shared picker is used by Playground, Templates, org grants, markup rules and API profiles.
  await page.evaluate(()=>{
   const label=document.createElement('label');label.textContent='Logo proof';const select=document.createElement('select');select.id='logoPickerProof';label.append(select);document.querySelector('#providerLogoEditor').append(label);
   ModelPicker.populate(select,providerAdminCatalog.filter(p=>['openai','sonilo'].includes(p.id)).map(p=>({...p,models:[{id:'fixture',display_name:'Fixture'}]})),{value:''});FormUI.refresh();
  });
  await page.getByRole('combobox',{name:'Logo proof',exact:true}).click();
  await page.waitForFunction(()=>document.querySelectorAll('.model-select-popup img').length===2);
  for(const theme of ['dark','light','dark']){await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);const filters=await page.locator('.model-select-popup img').evaluateAll(images=>images.map(i=>getComputedStyle(i).filter));assert.deepEqual(filters,[theme==='light'?'brightness(0)':'none',theme==='light'?'brightness(0)':'none']);}
  await page.keyboard.press('Escape');
  for(const width of [1440,390,320])for(const theme of ['dark','light']){
   await page.setViewportSize({width,height:1050});await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await select('openai');
   await editor().getByRole('button',{name:/^Upload logo/}).focus();assert.equal(await editor().getByRole('button',{name:/^Upload logo/}).evaluate(i=>document.activeElement===i),true);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'horizontal overflow '+width);if(width<=390){assert.equal(await input().isVisible(),false,'native file control stays hidden');assert.equal(await editor().locator('small').evaluate(h=>getComputedStyle(h).fontSize),'11px','format hint matches shared upload typography');assert.ok((await editor().locator('small').boundingBox()).width<=width,'small hint remains within viewport');}await editor().screenshot({path:path.join(out,`${phase}-${theme}-${width}.png`)});
  }
  if(phase==='upload'){
   // The reused component's ordinary template/reference defaults still support
   // cancel and retry; no provider-only validation leaks into other callers.
   await page.evaluate(()=>{
    const host=document.createElement('div');host.id='defaultUploadProof';document.body.append(host);let attempt=0;
    const control=ReferenceUpload.create({label:'Synthetic reference',title:'Drop image here',uploadLabel:'Upload image',kinds:['image'],onFile:(file,signal)=>{attempt++;window.defaultUploadAttempt=attempt;if(attempt===1)return new Promise((resolve,reject)=>signal.addEventListener('abort',()=>reject(new DOMException('Cancelled','AbortError')),{once:true}));return 'Ready';}});host.append(control.root);
   });
   const defaults=page.locator('#defaultUploadProof');await defaults.locator('input[type=file]').setInputFiles({name:'reference.png',mimeType:'image/png',buffer:fixtures.valid});await page.waitForFunction(()=>window.defaultUploadAttempt===1);assert.equal(await defaults.locator('input[type=file]').isDisabled(),true);await defaults.getByRole('button',{name:'Cancel upload',exact:true}).click();await defaults.getByRole('button',{name:'Retry upload',exact:true}).click();await defaults.getByRole('status').filter({hasText:'Ready'}).waitFor();assert.equal(await page.evaluate(()=>defaultUploadAttempt),2);await defaults.evaluate(node=>node.remove());
  }
  if(phase==='upload'){
   await select('openai');const before=await iconURL('openai'),second=await iconURL('sonilo');const remove=()=>editor().getByRole('button',{name:'Remove logo for OpenAI',exact:true});
   assert.equal(await remove().evaluate(b=>getComputedStyle(b).borderRadius),'50%');
   await page.route('**/v1/admin/providers/openai/logo',route=>route.abort(),{times:1});await input().setInputFiles({name:'pending.png',mimeType:'image/png',buffer:fixtures.palette});await editor().getByRole('status').filter({hasText:'retry'}).waitFor();
   await page.route('**/v1/admin/providers/openai/logo',async route=>{if(route.request().method()!=='PUT')return route.continue();await route.fetch();await route.abort();},{times:1});await input().setInputFiles({name:'committed.png',mimeType:'image/png',buffer:fixtures.palette});await editor().getByRole('status').filter({hasText:'Current server logo is shown'}).waitFor();assert.equal(await editor().locator('img').getAttribute('data-logo-url'),await iconURL('openai'),'lost upload response reconciles actual persisted logo');await upload(fixtures.valid);
   let enter,release;const entered=new Promise(r=>enter=r),gate=new Promise(r=>release=r);
   await page.route('**/v1/admin/providers/openai/logo',async route=>{if(route.request().method()!=='DELETE')return route.continue();enter();await gate;await route.fulfill({status:503,json:{error:{message:'Synthetic removal failure'}}});},{times:1});
   await remove().press('Enter');await entered;assert.equal(await remove().isDisabled(),true);assert.equal(await input().isDisabled(),true);await select('sonilo');await select('openai');assert.equal(await remove().isDisabled(),true);release();await editor().getByRole('status').filter({hasText:'Synthetic removal failure'}).waitFor();assert.equal(await editor().getByRole('button',{name:'Retry upload',exact:true}).isVisible(),false,'remove cannot retry a previous upload');assert.equal(await iconURL('openai'),before);
   await page.route('**/v1/admin/providers/openai/logo',route=>route.abort(),{times:1});await remove().press('Enter');await editor().getByRole('status').filter({hasText:'retry'}).waitFor();assert.equal(await iconURL('openai'),before);
   await page.route('**/v1/admin/providers/openai/logo',async route=>{if(route.request().method()!=='DELETE')return route.continue();await route.fetch();await route.abort();},{times:1});await remove().press('Enter');await editor().getByRole('status').filter({hasText:'Current server logo is shown'}).waitFor();assert.equal(await iconURL('openai'),undefined);assert.equal(await editor().locator('img').count(),0);assert.equal(await page.locator('#refreshLogoProof img').count(),0,'visible logo slot clears after removal');await upload(fixtures.valid);await page.locator('#refreshLogoProof img').evaluate(img=>img.decode());assert.ok((await page.locator('#refreshLogoProof img').getAttribute('data-logo-url')).includes('generation=1'),'re-upload restores existing slot and its key context');
   let deletes=0;page.on('request',r=>{if(r.method()==='DELETE'&&r.url().includes('/openai/logo'))deletes++});await remove().press('Enter');await editor().getByRole('status').filter({hasText:'Logo removed.'}).waitFor();assert.equal(deletes,1);assert.equal(await iconURL('openai'),undefined);assert.equal(await iconURL('sonilo'),second);assert.equal(await row('openai').locator('img').count(),0);assert.equal((await context.request.get(base+before)).status(),404);assert.equal(await editor().locator('img').count(),0);assert.equal(await remove().count(),0);await page.reload();await page.waitForFunction(()=>consoleInitialized&&Workspace.ready());await page.getByRole('tab',{name:'Providers',exact:true}).click();await select('openai');assert.equal(await iconURL('openai'),undefined);
  }
  assert.deepEqual(errors,[]);fs.writeFileSync(path.join(out,phase+'-results.json'),JSON.stringify({engine,phase,proxy:!!caddy,pixels:'exact RGB and alpha',layouts:[1440,390,320],errors},null,2));
  console.log('PASS '+engine+' '+phase+': real authenticated upload/API/database, PNG validation, alpha pixels, provider isolation, responsive keyboard/themes'+(caddy?', Caddy bearer reads':''));
 } finally {if(browser)await browser.close();if(caddy){caddy.kill();await new Promise(r=>caddy.once('exit',r));}}
})().catch(e=>{console.error(e);process.exitCode=1});
