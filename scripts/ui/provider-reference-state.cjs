const {generationFixtureResponse}=require('./generation-page-fixture.cjs');
const {useFixtureKey}=require('./fixture-key.cjs');
const {evidencePath}=require('./evidence.cjs');
// Delayed synthetic API responses exercise picker continuations, never BytePlus.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=process.env.UI_SOURCE_DIR||path.resolve(__dirname,'../../web/public/ui');
const output=process.env.UI_SCREENSHOT_DIR||evidencePath('/tmp/gateway-provider-reference-state');
const width=Number(process.env.UI_VIEWPORT_WIDTH||1440),theme=process.env.UI_THEME||'light';
fs.mkdirSync(output,{recursive:true});
const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve};};
const model={id:'seedance',display_name:'Synthetic references',media_type:'video',reference_prompt_syntax:'numbered',supports_asset_references:true,supports_video_input:true,supports_image_input:true,max_image_inputs:9,resolutions:['720p'],ratios:['16:9'],default_duration:5,reference_modes:[{id:'references',label:'References',min_images:0,max_images:9,min_videos:0,max_videos:1,min_audios:0,max_audios:0}]};
const asset={id:'binding-existing',asset_id:'asset-existing',uri:'asset://asset-existing',group_id:'group-existing',name:'Existing character',type:'Image',status:'Active'};
const org={id:'11111111-1111-4111-8111-111111111111',name:'Synthetic studio',role:'supermanager',available_usd:10};
const server=http.createServer((req,res)=>{
 const name=req.url.startsWith('/ui/')?path.basename(req.url.split('?')[0]):'index.html',file=path.join(root,name);
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const origin='http://127.0.0.1:'+server.address().port,engine=process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium;
 const browser=await engine.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})}),results=[];
 async function run(name,test){
  if(process.env.UI_CASE_FILTER&&!name.includes(process.env.UI_CASE_FILTER))return;
  const context=await browser.newContext({viewport:{width,height:1100},colorScheme:theme}),page=await context.newPage(),errors=[],gates=[];
  page.setDefaultTimeout(6000);page.on('pageerror',e=>errors.push(e.message));
  const state={assets:[],groups:[{id:'group-existing',name:'Characters'}],imports:0,groupCreates:0,gate:null,localReferences:[],registrationPosts:0};
  state.holdResponse=match=>{const gate={match,arrived:deferred(),release:deferred(),finished:deferred()};state.responseGate=gate;gates.push(gate);return gate;};
  state.hold=match=>{const gate={match,arrived:deferred(),release:deferred()};state.gate=gate;gates.push(gate);return gate;};
  await context.route('**/*',async route=>{
   const req=route.request(),url=new URL(req.url()),p=url.pathname;
   if(url.origin!==origin){errors.push('External request: '+url.origin);return route.abort();}
   if(!p.startsWith('/v1/'))return route.continue();
   const gate=state.gate;
   if(gate&&gate.match(p,req.method())){state.gate=null;gate.arrived.resolve();await gate.release.promise;}
   const json=async data=>{const snapshot=structuredClone(data),pending=state.responseGate;if(pending&&pending.match(p,req.method())){state.responseGate=null;pending.arrived.resolve();await pending.release.promise;await route.fulfill({json:generationFixtureResponse(route.request(),snapshot)}).catch(()=>{});pending.finished.resolve();return;}return route.fulfill({json:generationFixtureResponse(route.request(),snapshot)}).catch(()=>{});};
   if(p==='/v1/info')return json({auth_mode:'google',provider:'bytedance'});
   if(p==='/v1/me')return json({id:'synthetic-admin',session_id:'fixture',name:'Synthetic admin',is_system_admin:true});
   if(p==='/v1/organizations')return json({data:[org]});
   if(p==='/v1/api-keys'&&req.method()==='POST')return json({secret:'synthetic-key'});
   if(p==='/v1/key')return json({key:{id:'synthetic-key',name:'Fixture',organization_id:org.id,resource_mode:'shared',scopes:['generation']},organization:org,usage:{}});
   if(p==='/v1/providers')return json({data:['bytedance','second'].map(id=>({id,name:id,default_model:model.id,models:[model,{...model,id:'local',display_name:'Local references',supports_asset_references:false}]}))});
   if(p==='/v1/references')return json({data:state.localReferences});
   if(p.startsWith('/v1/media/'))return route.fulfill({contentType:p.endsWith('local-video')?'video/mp4':'image/png',body:p.endsWith('local-video')?fs.readFileSync(path.resolve(process.env.GATEWAY_TEMPLATE_CLIP_DIR||'.local/video-workflow/clips','cfr24.mp4')):Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aT0sAAAAASUVORK5CYII=','base64')});
   if(p.includes('/references/providers/')&&req.method()==='POST')state.registrationPosts++;
   if(p==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
   if(p==='/v1/generations'&&req.method()==='POST'){errors.push('Unexpected generation submission');return route.abort();}
   if(p.endsWith('/groups')){
    if(req.method()==='POST'){state.groupCreates++;const group={id:'created-'+state.groupCreates,name:req.postDataJSON().name};state.groups.push(group);return json(group);}
    return json({data:state.groups,can_import:true});
   }
   if(p.endsWith('/inventory'))return json({data:[asset]});
   if(p.endsWith('/import')){state.imports++;state.assets=[asset];return json(asset);}
   if(p.includes('/assets/')&&req.method()==='DELETE'){state.assets=state.assets.filter(a=>a.id!==p.split('/').at(-1));return json({});}
   if(p.endsWith('/assets'))return json({data:state.assets});
   return json({data:[]});
  });
  const picker=page.locator('#referenceLibrary');
  async function open(){await page.locator('#chooseReferences').click();}
  async function source(){await picker.locator('select[aria-label="image reference source"]').selectOption('provider');await picker.getByRole('button',{name:'Import existing assets',exact:true}).waitFor();await page.waitForFunction(()=>!document.querySelector('#referenceLibrary .provider-reference-library .reference-actions button').disabled);}
  async function inventory(){await picker.getByRole('button',{name:'Import existing assets',exact:true}).click();await picker.getByRole('button',{name:'Import Existing character into this workspace',exact:true}).waitFor();}
  async function close(){await picker.getByRole('button',{name:'Close library',exact:true}).click();}
  async function selectProvider(id){await page.locator('#model').selectOption(JSON.stringify([id,model.id]));await page.waitForFunction(id=>selectedProvider()?.id===id,id);}
  try{
   await page.goto(origin);await useFixtureKey(page,'synthetic-key');await page.waitForFunction(()=>selectedModel()?.id==='seedance');
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   await test({page,picker,state,open,source,inventory,close,selectProvider});
   assert.deepEqual(errors,[]);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'no horizontal page overflow');
   await page.screenshot({path:path.join(output,name.replaceAll(' ','-')+'.png'),fullPage:true});
   results.push({name,result:'passed'});console.log('PASS '+name);
  }catch(error){results.push({name,result:'failed',error:error.message});await page.screenshot({path:path.join(output,name.replaceAll(' ','-')+'.png'),fullPage:true});console.error('FAIL '+name+': '+error.message);}
  finally{gates.forEach(g=>g.release.resolve());await context.close();}
 }
 try{
  await run('local library has no registration actions across model and provider changes',async({page,picker,state,open,close,selectProvider})=>{
   state.localReferences=[{id:'local-image',name:'Local image.png',content_type:'image/png',url:'/v1/media/local-image',size_bytes:68},{id:'local-video',name:'Local video.mp4',content_type:'video/mp4',url:'/v1/media/local-video',size_bytes:2000}];
   for(const provider of ['bytedance','second']){await selectProvider(provider);for(const id of ['seedance','local']){await page.locator('#model').selectOption(JSON.stringify([provider,id]));await open();await picker.getByRole('button',{name:'Use Local image.png in generation',exact:true}).waitFor();if(!process.env.UI_STYLE_BASELINE)assert.equal(await picker.getByRole('button',{name:/^(Register Local|Add to BytePlus)/}).count(),0);await close();}}
   await page.locator('#model').selectOption(JSON.stringify(['bytedance','seedance']));await open();const image=picker.getByRole('button',{name:'Use Local image.png in generation',exact:true});await image.click();await page.waitForFunction(()=>document.querySelector('#imageURLs').value.includes('local-image'));await picker.getByRole('button',{name:'Delete Local image.png from library',exact:true}).click();await picker.getByRole('button',{name:'Keep Local image.png',exact:true}).click();
   for(const t of ['light','dark'])for(const w of [1440,390,320]){await page.setViewportSize({width:w,height:1100});await page.evaluate(t=>{document.documentElement.dataset.theme=t;document.documentElement.style.setProperty('--accent','#b5e21d');},t);await image.focus();assert.equal(await image.locator('..').evaluate(n=>getComputedStyle(n,'::after').borderTopColor),'rgb(181, 226, 29)');await picker.screenshot({path:path.join(output,`local-image-${t}-${w}.png`)});}await close();
   await page.locator('#videoChooseReferences').click();const videoPicker=page.locator('#videoReferenceLibrary');await videoPicker.getByRole('button',{name:'Use Local video.mp4 in generation',exact:true}).waitFor();if(!process.env.UI_STYLE_BASELINE)assert.equal(await videoPicker.getByRole('button',{name:/^(Register Local|Add to BytePlus)/}).count(),0);await videoPicker.getByRole('button',{name:'Play Local video.mp4',exact:true}).click();await videoPicker.locator('[data-player]').evaluate(v=>v.play());assert.equal(await videoPicker.locator('[data-player]').evaluate(v=>v.paused),false);await videoPicker.screenshot({path:path.join(output,'local-video-play.png')});await videoPicker.getByRole('button',{name:'Close library',exact:true}).click();assert.equal(state.registrationPosts,0);await page.setViewportSize({width:1440,height:1100});
  });
  await run('repeat import preserves selected reference',async({page,picker,state,open,source,inventory,close})=>{
   await open();await source();await inventory();
   await picker.getByRole('button',{name:'Import Existing character into this workspace',exact:true}).click();
   await page.waitForFunction(()=>document.querySelector('#imageURLs').value==='asset://asset-existing');
   await inventory();
   const gate=state.hold((p,m)=>p.endsWith('/import')&&m==='POST');
   await picker.getByRole('button',{name:'Import Existing character into this workspace',exact:true}).click();await gate.arrived.promise;
   gate.release.resolve();await picker.getByRole('button',{name:'Import existing assets',exact:true}).waitFor();
   await page.waitForFunction(()=>document.querySelector('#referenceLibrary [data-asset-id="asset-existing"]'));
   // A final API response and its continuation must settle before asserting absence.
   await page.waitForTimeout(150);
   assert.equal(await page.locator('#imageURLs').inputValue(),asset.uri,'reimporting an existing selection must not toggle it off');
   assert.equal(state.imports,2);await close();
  });
  await run('reset during import refresh discards old selection',async({page,picker,state,open,source,inventory,close})=>{
   await open();await source();await inventory();
   const gate=state.hold((p,m)=>p.endsWith('/groups')&&m==='GET');
   await picker.getByRole('button',{name:'Import Existing character into this workspace',exact:true}).click();await gate.arrived.promise;
   await close();await page.locator('#forgetGenerationKey').click();
   gate.release.resolve();await page.waitForTimeout(250);
   assert.equal(await page.locator('#imageURLs').inputValue(),'','a completed old-key import must not repopulate the reset form');
   await useFixtureKey(page,'synthetic-key');await page.waitForFunction(()=>selectedModel()?.id==='seedance');
   assert.equal(await page.locator('#selectedReferences .reference-card').count(),0);
  });
  await run('overlapping imports select only once',async({page,picker,state,open,source,inventory})=>{
   await open();await source();await inventory();const choose=picker.getByRole('button',{name:'Import Existing character into this workspace',exact:true});
   const gate=state.hold((p,m)=>p.endsWith('/import')&&m==='POST');
   await choose.click();await gate.arrived.promise;await choose.click();
   await page.waitForFunction(()=>document.querySelector('#imageURLs').value==='asset://asset-existing');
   gate.release.resolve();await picker.getByRole('button',{name:'Import existing assets',exact:true}).waitFor();await page.waitForTimeout(250);
   assert.equal(await page.locator('#imageURLs').inputValue(),asset.uri,'two pending imports must leave exactly one selected URI');assert.equal(state.imports,2);
  });
  for(const reset of ['provider change','key reset'])await run('group create recovers after '+reset,async({page,picker,state,open,source,close,selectProvider})=>{
   await open();await source();await picker.getByRole('button',{name:'New group',exact:true}).click();
   await picker.getByRole('textbox',{name:'image group name',exact:true}).fill('Interrupted group');
   const gate=state.hold((p,m)=>p.endsWith('/groups')&&m==='POST');
   await picker.getByRole('button',{name:'Create group',exact:true}).click();await gate.arrived.promise;
   await close();
   if(reset==='provider change')await selectProvider('second');
   else{await page.locator('#forgetGenerationKey').click();await useFixtureKey(page,'synthetic-key');await page.waitForFunction(()=>selectedModel()?.id==='seedance');}
   gate.release.resolve();await open();await source();
   const create=picker.getByRole('button',{name:'Create group',exact:true});
   if(!await create.isVisible())await picker.getByRole('button',{name:'New group',exact:true}).click();
   assert.equal(await create.isEnabled(),true,'the abandoned request must not leave Create group disabled');
   await picker.getByRole('textbox',{name:'image group name',exact:true}).fill('New group');await create.focus();await page.keyboard.press('Enter');
   await page.waitForFunction(()=>document.querySelector('#referenceLibrary select[aria-label="image asset group"]').value.startsWith('created-'));
   await close();
  });
  await run('automatic read recovers after provider change',async({page,picker,state,open,source,close,selectProvider})=>{
   await open();await source();const gate=state.hold((p,m)=>p.endsWith('/groups')&&m==='GET');
   await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await gate.arrived.promise;
   await close();await selectProvider('second');gate.release.resolve();await open();await source();
   assert.equal(await picker.getByRole('button',{name:/Refresh|Reload/}).count(),0);await picker.getByRole('button',{name:'Import existing assets',exact:true}).waitFor();
  });
  for(const action of ['detach','delete'])await run('late read cannot restore '+(action==='detach'?'detached':'deleted')+' asset',async({page,picker,state,open,source})=>{
   state.assets=[asset];await open();await source();await picker.getByRole('button',{name:'Use Existing character in generation',exact:true}).waitFor();
   const deletion=state.hold((p,m)=>p.includes('/assets/')&&m==='DELETE');
   if(action==='detach')await picker.getByRole('button',{name:'Remove Existing character from workspace',exact:true}).click();
   else{await picker.getByRole('button',{name:'Delete Existing character from BytePlus',exact:true}).click();await picker.getByRole('button',{name:'Delete asset',exact:true}).click();}
   await deletion.arrived.promise;const gate=state.holdResponse((p,m)=>p.endsWith('/assets')&&m==='GET');await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await gate.arrived.promise;deletion.release.resolve();
   await picker.locator('[data-asset-id="asset-existing"]').waitFor({state:'detached'});gate.release.resolve();await gate.finished.promise;await page.evaluate(()=>new Promise(done=>requestAnimationFrame(()=>requestAnimationFrame(done))));assert.equal(await picker.locator('[data-asset-id="asset-existing"]').count(),0);assert.equal(state.assets.length,0);
  });
  await run('keyboard navigation closes reference preview',async({page})=>{
   await page.locator('#imageURLs').evaluate(e=>{e.value=location.origin+'/references/image.png';e.dispatchEvent(new Event('input',{bubbles:true}));});
   await page.locator('#prompt').fill('Use @Image1');await page.locator('.prompt-reference-token').waitFor();
   await page.locator('#prompt').evaluate(e=>e.setSelectionRange(7,7));await page.keyboard.press('Alt+ArrowDown');await page.locator('#promptReferencePreview').waitFor();
   await page.locator('#systemNav').focus();await page.keyboard.press('Enter');await page.locator('#systemView').waitFor();
   await page.waitForFunction(()=>!document.querySelector('#promptReferencePreview'));
   await page.locator('#generationNav').focus();await page.keyboard.press('Enter');await page.locator('#prompt').waitFor();
   assert.equal(await page.locator('#prompt').inputValue(),'Use @Image1');
  });
 }finally{fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine:process.env.UI_BROWSER_ENGINE||'chromium',version:browser.version(),width,theme,source:root,results},null,2)+'\n');await browser.close();server.close();}
 if(results.some(r=>r.result==='failed'))process.exitCode=1;
})().catch(error=>{console.error(error);server.close();process.exitCode=1});
