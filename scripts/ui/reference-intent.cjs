const {useFixtureKey}=require('./fixture-key.cjs');
const {evidencePath} = require('./evidence.cjs');
// Real browser controls; every API/file response is a synthetic localhost fixture.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const root=process.env.UI_SOURCE_DIR||path.resolve(__dirname,'../../web/public/ui');
const nativeVoice=process.env.UI_VOICE_UPLOAD==='chooser';
const output=process.env.UI_SCREENSHOT_DIR||evidencePath('/tmp/gateway-reference-intent');
fs.mkdirSync(output,{recursive:true});
const image=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=','base64');
const audio=Buffer.from('synthetic audio fixture; never sent to a provider');
const profile=(id,label,i,v,a)=>({id,label,min_images:0,max_images:i,min_videos:0,max_videos:v,min_audios:0,max_audios:a});
const seedance={id:'dreamina-seedance-2-0-260128',display_name:'Seedance 2.0',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:15,default_duration:5,supports_image_input:true,supports_audio_input:true,supports_video_input:true,max_image_inputs:9,reference_modes:[profile('references','References',9,3,3)],pricing:{known:true,reference_price_usd:0}};
const wan={...seedance,id:'wan2.7-r2v',display_name:'Wan 2.7',supports_audio_input:false,reference_modes:[{...profile('references','References',5,5,0),supports_reference_voice:true}]};
if(nativeVoice){
 seedance.reference_modes.push({...profile('first_last_frames','First and last frames',2,0,0),min_images:2,image_roles:['First frame','Last frame']});
 wan.reference_modes.push({...profile('first_frame_references','First frame and references',6,5,0),supports_reference_voice:true,voice_image_start:1,image_roles:['First frame']});
}
const provider={id:'mock',name:'Test provider',credentials_configured:true,default_model:wan.id,models:[seedance,wan]};
const organization={id:'11111111-1111-4111-8111-111111111111',name:'Test organization',role:'admin',balance_usd:0,available_usd:0,storage_bytes:0};
const initialLibrary=[{id:'i1',name:'first.png',content_type:'image/png',size_bytes:image.length},{id:'i2',name:'last.png',content_type:'image/png',size_bytes:image.length},{id:'a1',name:'voice.mp3',content_type:'audio/mpeg',size_bytes:audio.length}].map(item=>({...item,url:'/references/test/'+item.id}));
const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve};};
let receiveReferences;
const server=http.createServer((req,res)=>{
 const url=new URL(req.url,'http://fixture');
 if(url.pathname==='/v1/references')return receiveReferences(req,res);
 if(url.pathname.startsWith('/references/')){res.setHeader('Content-Type','image/png');return res.end(image);}
 const file=path.join(root,url.pathname==='/'?'index.html':path.basename(url.pathname));
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.html':'text/html','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const engine=process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium;
 const browser=await engine.launch({headless:true,...(engine===chromium&&process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const results=[];
 async function run(name,test){
  if(process.env.UI_CASE_FILTER&&!name.includes(process.env.UI_CASE_FILTER))return;
  const context=await browser.newContext({viewport:{width:1440,height:1100}}),page=await context.newPage(),errors=[],browserNotices=[],external=[];
  let library=initialLibrary.map(item=>({...item})),uploads=0,preparations=0,uploadGate=null,existingPlan=false,preparationFailures=0;
  const chooserEvents=[];
  const gates=[];
  page.setDefaultTimeout(8000);page.on('pageerror',error=>{
   // Match the existing model-picker fixture's WebKit viewport notice policy,
   // restricted here to the scenario that changes viewport dimensions.
   if(engine===webkit&&name==='repeated cancellation keyboard and mobile'&&error.message==='ResizeObserver loop completed with undelivered notifications.'){
    browserNotices.push(error.message);console.warn('WebKit resize notice: '+error.message);return;
   }
   errors.push(error.message);
  });
  const origin='http://127.0.0.1:'+server.address().port;
  // Read actual multipart bytes at the loopback receiver. Browser protocol
  // request metadata can omit file parts (as observed in hosted WebKit).
  receiveReferences=async(req,res)=>{
   let gate;
   try{
    res.setHeader('Content-Type','application/json');
    if(req.method==='GET')return res.end(JSON.stringify({data:library,has_more:false}));
    assert.equal(req.method,'POST');
    const chunks=[];for await(const chunk of req)chunks.push(chunk);
    const body=Buffer.concat(chunks);
    assert.match(req.headers['content-type'],/^multipart\/form-data; boundary=/);
    assert.ok(body.includes(audio),'upload carries only synthetic bytes');
    gate=uploadGate;uploadGate=null;
    const item={id:'upload'+(++uploads),name:'uploaded.mp3',content_type:'audio/mpeg',size_bytes:audio.length,url:'/references/test/upload'+uploads};library.push(item);
    if(gate){gate.arrived.resolve();await gate.release.promise;}
    res.end(JSON.stringify(item));
   }catch(error){errors.push(error.message);res.writeHead(500,{'Content-Type':'application/json'}).end(JSON.stringify({error:{message:error.message}}));}
   finally{gate?.finished.resolve();}
  };
  await context.route(url=>url.origin!==origin||url.pathname!=='/v1/references',async route=>{
   const req=route.request(),url=new URL(req.url()),json=data=>route.fulfill({json:data});
   if(url.origin!==origin){external.push(req.url());return route.abort();}
   if(!url.pathname.startsWith('/v1/'))return route.continue();
   if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock'});
   if(url.pathname==='/v1/me')return json({id:'test',session_id:'session',name:'Test account',is_system_admin:true});
   if(url.pathname==='/v1/organizations')return json({data:[organization]});
   if(url.pathname==='/v1/organizations/'+organization.id)return json(organization);
   if(url.pathname==='/v1/api-keys')return req.method()==='POST'?json({secret:'fixture-key'}):json({data:[]});
   if(url.pathname==='/v1/key')return json({key:{id:'fixture-key',name:'Reference fixture',organization_id:organization.id,resource_mode:'shared'},organization,usage:{}});
   if(url.pathname==='/v1/providers')return json({data:[provider]});
   if(url.pathname==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
   if(url.pathname==='/v1/generations'&&req.method()!=='GET')throw Error('This regression must not submit generation');
   if(url.pathname==='/v1/references/uploads'){
    preparations++;const data=req.postDataJSON();assert.equal(data.sha256,crypto.createHash('sha256').update(audio).digest('hex'));assert.equal(data.size_bytes,audio.length);
    if(preparationFailures){preparationFailures--;return route.fulfill({status:503,json:{error:{message:'Fixture preparation failure'}}});}
    if(existingPlan)return json({mode:'existing',reference:{...library.find(item=>item.id==='a1'),duplicate:true}});
    return json({mode:'local'});
   }
   return json({data:[]});
  });
  const dialog=page.locator('#audioReferenceLibrary');
  async function model(label){await page.getByRole('combobox',{name:'Model',exact:true}).click();await page.locator('.model-select-popup').getByRole('option').filter({hasText:label}).click();await page.waitForFunction(label=>selectedModel()?.display_name===label,label);}
  async function referenceMode(label){await page.getByRole('combobox',{name:'Reference mode',exact:true}).click();await page.getByRole('listbox',{name:'Reference mode',exact:true}).getByRole('option',{name:label,exact:true}).click();}
  const libraryReady=()=>page.waitForFunction(()=>!document.querySelector('#audioReferenceLibrary [data-status]').textContent.includes('Loading library'));
  async function voice(visual='first.png',ready=true){await page.getByRole('button',{name:'Voice reference for '+visual,exact:true}).click();await dialog.waitFor();if(ready){await dialog.locator('.reference-card').first().waitFor();await libraryReady();}}
  async function dismiss(action){if(action==='Escape')await page.keyboard.press('Escape');else await dialog.getByRole('button',{name:'Close library',exact:true}).click();await page.waitForFunction(()=>!document.querySelector('#audioReferenceLibrary').open);}
  async function choose(){await page.locator('#audioChooseReferences').click();await dialog.getByRole('button',{name:'Use voice.mp3 in generation',exact:true}).click();if(await dialog.isVisible())await dismiss('Close');}
  async function openChooser(inLibrary=false,activation='click'){
   const button=inLibrary?dialog.locator('[data-upload]'):page.locator('#audioUploadReference');
   const [chooser]=await Promise.all([page.waitForEvent('filechooser'),activation==='click'?button.click():button.press(activation)]);
   assert.equal(await chooser.element().isDisabled(),false,'genuine chooser must target an enabled input');
   assert.equal(await chooser.element().getAttribute('id'),'audioReferenceFiles');
   assert.equal(await chooser.element().getAttribute('accept'),'audio/mpeg,audio/wav,audio/x-wav,audio/ogg,audio/flac');
   chooserEvents.push({activation,in_library:inLibrary,input_enabled:true});
   return chooser;
  }
  async function startUpload(inLibrary=false,activation='click'){
   const chooser=await openChooser(inLibrary,activation);
   const posted=page.waitForRequest(req=>new URL(req.url()).pathname==='/v1/references/uploads'&&req.method()==='POST');
   await Promise.all([posted,chooser.setFiles({name:'uploaded.mp3',mimeType:'audio/mpeg',buffer:audio})]);
  }
  async function startVoiceUpload(){
   if(nativeVoice)return startUpload(true);
   // Retain PR34's original drop path as an independent regression. The native
   // variant above requires a real chooser event and an enabled input.
   const dataTransfer=await page.evaluateHandle(bytes=>{const data=new DataTransfer();data.items.add(new File([new Uint8Array(bytes)],'uploaded.mp3',{type:'audio/mpeg'}));return data;},[...audio]);
   try{await Promise.all([page.waitForRequest(req=>new URL(req.url()).pathname==='/v1/references'&&req.method()==='POST'),dialog.locator('#libraryDropzone').dispatchEvent('drop',{dataTransfer})]);}finally{await dataTransfer.dispose();}
  }
  const waitUpload=(text='saved to your library.')=>page.waitForFunction(text=>!document.querySelector('#audioReferenceFiles').value&&document.querySelector('#audioReferenceStatus').textContent.includes(text),text);
  async function upload(inLibrary=false){if(inLibrary){await page.locator('#audioChooseReferences').click();await dialog.locator('.reference-card').first().waitFor();await libraryReady();}await startUpload(inLibrary);await waitUpload();if(inLibrary&&await dialog.isVisible())await dismiss('Close');}
  function holdUpload(){const gate={arrived:deferred(),release:deferred(),finished:deferred()};uploadGate=gate;gates.push(gate);return gate;}
  const read=()=>page.evaluate(()=>({audio:document.querySelector('#audioURL').value,images:document.querySelector('#imageURLs').value,voices:referencePicker.voices()}));
  try{
   await page.goto(origin);await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());await useFixtureKey(page,'fixture-key');await page.waitForFunction(()=>selectedModel()?.id==='wan2.7-r2v');
   await page.locator('#chooseReferences').click();await page.getByRole('button',{name:'Use first.png in generation',exact:true}).click();await page.getByRole('button',{name:'Use last.png in generation',exact:true}).click();await page.locator('#referenceLibrary').getByRole('button',{name:'Close library',exact:true}).click();
   await test({page,dialog,model,referenceMode,voice,dismiss,choose,upload,openChooser,startUpload,startVoiceUpload,waitUpload,holdUpload,read,origin,operations:()=>({preparations,uploads,choosers:chooserEvents.length}),useExisting:()=>{existingPlan=true;},failPreparation:()=>{preparationFailures=1;}});
   assert.deepEqual(errors,[],'no application errors');assert.deepEqual(external,[],'no external requests');
   results.push({name,status:'pass',browser_notices:browserNotices,chooser_events:chooserEvents});console.log('PASS: '+name);
  }catch(error){
   console.error('FAIL: '+name+' '+error.message);
   results.push({name,status:'fail',error:error.message,state:await read().catch(()=>null),uploadDisabled:await page.locator('#audioReferenceFiles').isDisabled().catch(()=>null),browser_notices:browserNotices,chooser_events:chooserEvents});
   await page.screenshot({path:path.join(output,name.replace(/[^a-z0-9]+/gi,'-')+'.png'),fullPage:true}).catch(diagnostic=>console.warn('Failure screenshot unavailable: '+diagnostic.message));
  }
  finally{gates.forEach(gate=>gate.release.resolve());await context.close();}
 }
 try{
  if(nativeVoice)for(const activation of ['click','Enter'])await run('native chooser opens with '+activation,async({page,voice,startUpload,waitUpload,read,origin})=>{
   await voice();await startUpload(true,activation);await waitUpload();
   assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/upload1'});
   assert.equal((await read()).audio,'');assert.equal(await page.locator('#audioURLField').isVisible(),false);
   assert.equal(await page.locator('#audioURL').isDisabled(),true);assert.equal(await page.locator('#audioReferenceFiles').isDisabled(),true,'file input restores after successful voice upload');
  });
  if(nativeVoice){
   await run('native chooser empty selection then keyboard retry',async({page,dialog,voice,openChooser,startUpload,waitUpload,operations,read,origin})=>{
    await voice();const chooser=await openChooser(true);await chooser.setFiles([]);
    assert.equal(await dialog.isVisible(),true);assert.deepEqual(operations(),{preparations:0,uploads:0,choosers:1});assert.deepEqual((await read()).voices,{});
    assert.equal(await page.locator('#audioURL').isDisabled(),true);
    await startUpload(true,'Space');await waitUpload();
    assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/upload1'});assert.equal((await read()).audio,'');
   });
   await run('native chooser change clear model return and ordinary upload',async({page,model,voice,startVoiceUpload,waitUpload,upload,read,origin})=>{
    await voice();await startVoiceUpload();await waitUpload();
    assert.equal(await page.getByRole('button',{name:'Voice reference for first.png',exact:true}).innerText(),'Change voice reference');
    await voice();await startVoiceUpload();await waitUpload();
    assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/upload2'});
    await model('Seedance 2.0');await upload();await model('Wan 2.7');
    assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/upload2'});
    assert.equal((await read()).audio,origin+'/references/test/upload3');
    await page.getByRole('button',{name:'Clear voice for first.png',exact:true}).click();assert.deepEqual((await read()).voices,{});
    await voice('last.png');await startVoiceUpload();await waitUpload();
    assert.deepEqual((await read()).voices,{[origin+'/references/test/i2']:origin+'/references/test/upload4'});assert.equal((await read()).audio,origin+'/references/test/upload3','voice upload cannot change the existing ordinary audio draft');
   });
   await run('native chooser capability and dismissal scope',async({page,model,referenceMode,voice,dismiss})=>{
    assert.equal(await page.locator('#audioReferenceFiles').isDisabled(),true);
    await referenceMode('First frame and references');
    assert.equal(await page.getByRole('button',{name:'Voice reference for first.png',exact:true}).count(),0);
    for(const action of ['Escape','Close']){
     await voice('last.png');assert.equal(await page.locator('#audioReferenceFiles').isDisabled(),false);assert.equal(await page.locator('#audioURL').isDisabled(),true);
     await dismiss(action);assert.equal(await page.locator('#audioReferenceFiles').isDisabled(),true,'dismiss restores the hidden input');
    }
    await model('Seedance 2.0');await referenceMode('First and last frames');
    assert.equal(await page.locator('#audioURLField').isVisible(),false);assert.equal(await page.locator('#audioReferenceFiles').isDisabled(),true);assert.equal(await page.locator('.reference-voice').count(),0);
    await referenceMode('References');assert.equal(await page.locator('#audioReferenceFiles').isDisabled(),false);assert.equal(await page.locator('#audioURL').isDisabled(),false);
    await model('Wan 2.7');assert.equal(await page.locator('#audioReferenceFiles').isDisabled(),true);
   });
   await run('native chooser busy guard prevents repeated chooser and upload',async({page,dialog,voice,startVoiceUpload,waitUpload,holdUpload,operations,read,origin})=>{
    const gate=holdUpload();await voice();await startVoiceUpload();await gate.arrived.promise;
    let extraChoosers=0;const unexpected=()=>extraChoosers++;page.on('filechooser',unexpected);
    await dialog.locator('[data-upload]').click();await dialog.locator('[data-upload]').press('Enter');await page.evaluate(()=>new Promise(requestAnimationFrame));page.off('filechooser',unexpected);
    assert.equal(extraChoosers,0);assert.deepEqual(operations(),{preparations:1,uploads:1,choosers:1});
    gate.release.resolve();await gate.finished.promise;await waitUpload();
    assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/upload1'});
   });
   await run('native chooser existing duplicate uses one library object',async({voice,startVoiceUpload,waitUpload,useExisting,operations,read,origin})=>{
    useExisting();await voice();await startVoiceUpload();await waitUpload('Already in your library');
    assert.deepEqual(operations(),{preparations:1,uploads:0,choosers:1});assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/a1'});assert.equal((await read()).audio,'');
   });
   await run('native chooser validation preserves type size and retry',async({voice,openChooser,startVoiceUpload,waitUpload,operations,read,origin})=>{
    await voice();let chooser=await openChooser(true);await chooser.setFiles({name:'invalid.txt',mimeType:'text/plain',buffer:audio});await waitUpload('use a supported audio file');
    assert.equal(operations().preparations,0);assert.equal(operations().uploads,0);assert.deepEqual((await read()).voices,{});
    const oversized=path.join(output,'oversized.mp3');fs.closeSync(fs.openSync(oversized,'w'));fs.truncateSync(oversized,50*1024*1024+1);
    try{chooser=await openChooser(true);await chooser.setFiles(oversized);await waitUpload('up to 50 MB');}finally{fs.unlinkSync(oversized);}
    assert.equal(operations().preparations,0);assert.equal(operations().uploads,0);
    await startVoiceUpload();await waitUpload();assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/upload1'});assert.equal((await read()).audio,'');
   });
   await run('native chooser failed preparation then retry',async({voice,startVoiceUpload,waitUpload,failPreparation,operations,read,origin})=>{
    failPreparation();await voice();await startVoiceUpload();await waitUpload('Fixture preparation failure');assert.deepEqual((await read()).voices,{});
    await startVoiceUpload();await waitUpload();assert.deepEqual(operations(),{preparations:2,uploads:1,choosers:2});assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/upload1'});assert.equal((await read()).audio,'');
   });
  }
  for(const action of ['Escape','Close'])for(const operation of ['selection','library upload','direct upload'])await run(action+' then normal '+operation,async({model,voice,dismiss,choose,upload,read,origin})=>{
   await voice();await dismiss(action);await model('Seedance 2.0');
   if(operation==='selection')await choose();else await upload(operation==='library upload');
   const state=await read();await model('Wan 2.7');const returned=await read();
   assert.deepEqual({audio:state.audio,voices:returned.voices},{audio:origin+'/references/test/'+(operation==='selection'?'a1':'upload1'),voices:{}},'cancelled voice intent must not steal normal audio');
  });
  for(const action of ['Escape','Close'])await run(action+' interrupts voice upload',async({page,model,voice,dismiss,choose,startVoiceUpload,waitUpload,holdUpload,read,origin})=>{
   const gate=holdUpload();await voice();await startVoiceUpload();await gate.arrived.promise;await dismiss(action);await model('Seedance 2.0');
   gate.release.resolve();await gate.finished.promise;await waitUpload();
   assert.equal((await read()).audio,'','cancelled voice upload must not become ordinary audio');
   assert.match(await page.locator('#audioReferenceStatus').innerText(),/saved without adding/);
   await choose();await model('Wan 2.7');
   assert.deepEqual(await read(),{audio:origin+'/references/test/a1',images:origin+'/references/test/i1\n'+origin+'/references/test/i2',voices:{}},'late upload cannot attach or steal the next selection');
  });
  await run('reopen same visual during old upload',async({dialog,voice,dismiss,startVoiceUpload,waitUpload,holdUpload,read,origin})=>{
   const gate=holdUpload();await voice();await startVoiceUpload();await gate.arrived.promise;await dismiss('Close');await voice();
   gate.release.resolve();await gate.finished.promise;await waitUpload();
   assert.equal(await dialog.isVisible(),true,'old completion must leave the new picker open');assert.deepEqual((await read()).voices,{});
   await dialog.getByRole('button',{name:'Use voice.mp3 in generation',exact:true}).click();
   assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/a1'});
  });
  await run('reopen different visual during old upload',async({dialog,voice,dismiss,startVoiceUpload,waitUpload,holdUpload,read,origin})=>{
   const gate=holdUpload();await voice();await startVoiceUpload();await gate.arrived.promise;await dismiss('Close');await voice('last.png');
   gate.release.resolve();await gate.finished.promise;await waitUpload();
   assert.equal(await dialog.isVisible(),true);assert.deepEqual((await read()).voices,{});
   await dialog.getByRole('button',{name:'Use voice.mp3 in generation',exact:true}).click();
   assert.deepEqual((await read()).voices,{[origin+'/references/test/i2']:origin+'/references/test/a1'});
  });
  await run('ordinary pending upload cannot consume newer voice intent',async({dialog,model,voice,startUpload,waitUpload,holdUpload,read,origin})=>{
   await model('Seedance 2.0');const gate=holdUpload();await startUpload();await gate.arrived.promise;
   await model('Wan 2.7');await voice('first.png',false);gate.release.resolve();await gate.finished.promise;await waitUpload();
   assert.equal(await dialog.isVisible(),true);assert.deepEqual((await read()).voices,{});assert.equal((await read()).audio,'');
   await dialog.getByRole('button',{name:'Use voice.mp3 in generation',exact:true}).click();
   assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/a1'});
  });
  await run('explicit voice selection upload clear and model return',async({page,dialog,model,voice,dismiss,choose,startVoiceUpload,waitUpload,read,origin})=>{
   await voice();await dialog.getByRole('button',{name:'Use voice.mp3 in generation',exact:true}).press('Enter');await page.waitForFunction(()=>!document.querySelector('#audioReferenceLibrary').open);
   assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/a1'});assert.equal((await read()).audio,'');
   await model('Seedance 2.0');await choose();await model('Wan 2.7');
   assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/a1'},'model return preserves a successful voice');
   await voice('last.png');await startVoiceUpload();await waitUpload();
   assert.equal(await dialog.isVisible(),false);assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/a1',[origin+'/references/test/i2']:origin+'/references/test/upload1'});
   await page.getByRole('button',{name:'Clear voice for last.png',exact:true}).click();
   await voice('first.png');await dismiss('Escape');
   assert.deepEqual((await read()).voices,{[origin+'/references/test/i1']:origin+'/references/test/a1'},'cancelling Change voice preserves the existing assignment');
   await page.getByRole('button',{name:'Clear voice for first.png',exact:true}).click();assert.deepEqual((await read()).voices,{});assert.equal((await read()).audio,origin+'/references/test/a1');
  });
  await run('reset while voice upload is pending',async({page,model,voice,dismiss,startVoiceUpload,holdUpload,upload,read,origin})=>{
   const gate=holdUpload();await voice();await startVoiceUpload();await gate.arrived.promise;await dismiss('Escape');
   await page.locator('#forgetGenerationKey').click();await page.waitForFunction(()=>!GenerationKey.value());
   gate.release.resolve();await gate.finished.promise;
   await useFixtureKey(page,'fixture-key');await page.waitForFunction(()=>selectedModel()?.id==='wan2.7-r2v');
   await model('Seedance 2.0');await upload();await model('Wan 2.7');
   assert.deepEqual(await read(),{audio:origin+'/references/test/upload2',images:'',voices:{}},'reset invalidates old upload and new key has clean references');
  });
  await run('repeated cancellation keyboard and mobile',async({page,dialog,model,voice,dismiss,choose,startVoiceUpload,waitUpload,read,origin})=>{
   for(const [width,theme] of [[1440,'dark'],[390,'light'],[320,'dark']]){
    await page.setViewportSize({width,height:1000});await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
    await model('Wan 2.7');await voice();
    await page.screenshot({path:path.join(output,'picker-'+width+'.png'),fullPage:true});
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'page fits at '+width);
    const box=await dialog.boundingBox();assert.ok(box.x>=0&&box.x+box.width<=width,'dialog fits at '+width);
    if(nativeVoice){
     await startVoiceUpload();await waitUpload();
     assert.deepEqual(Object.keys((await read()).voices),[origin+'/references/test/i1']);assert.equal((await read()).audio,'');
     await page.getByRole('button',{name:'Clear voice for first.png',exact:true}).click();await voice();
    }
    await dismiss('Escape');await voice('last.png');await dismiss('Close');await voice();await dismiss('Close');
    await model('Seedance 2.0');await choose();
    assert.equal((await read()).audio,origin+'/references/test/a1');
    await page.locator('#audioSelectedReferences').getByRole('button',{name:'Remove voice.mp3 from generation',exact:true}).click();
    await model('Wan 2.7');assert.deepEqual((await read()).voices,{});
   }
  });
 }finally{fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine:engine.name(),browser:browser.version(),results},null,2)+'\n');await browser.close();server.close();}
 if(results.some(result=>result.status==='fail'))process.exitCode=1;
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
