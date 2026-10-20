const {evidencePath} = require('./evidence.cjs');
// Content-first model selection across providers. All API responses are fixtures.
const {chromium, webkit} = require('playwright');
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const ui = path.resolve(__dirname, '../../web/public/ui');
const output = process.env.UI_SCREENSHOT_DIR || evidencePath('/tmp/media-gateway-model-information-ui');
fs.mkdirSync(output, {recursive:true});
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : path.basename(req.url.split('?')[0]);
  const file = path.join(ui,name);
  if (!fs.existsSync(file)) return res.writeHead(404).end();
  res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.png':'image/png','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser = await (process.env.UI_BROWSER_ENGINE === 'webkit' ? webkit.launch({headless:true}) : chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH ? {executablePath:process.env.UI_BROWSER_PATH} : {})}));
  // Keep currency/date assertions independent of the host OS/browser locale.
  const page = await browser.newPage({locale:'en-GB',hasTouch:true,viewport:{width:1440,height:1100}});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const video={id:'shared-id',display_name:'Video A',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5,pricing:{known:true,reference_price_usd:0}};
  let catalog=[
    {id:'mock',name:'BytePlus',default_model:'shared-id',models:[video,{id:'image',display_name:'Image A',media_type:'image',resolutions:['1024*1024']}]},
    {id:'other',name:'Alibaba',models:[{...video,display_name:'Video B',resolutions:['1080p'],default_duration:8},{id:'speech',display_name:'Speech B',media_type:'speech',supports_voice:true,voices:['Serena']}]},
    {id:'empty',name:'Empty provider',models:[]}
  ];
  const estimates=[],posts=[];
  let tasks=[];
  await page.route('**/v1/**',async route=>{
    const request=route.request(), url=new URL(request.url());
    const json=value=>route.fulfill({json:value});
    if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock'});
    if(url.pathname==='/v1/me')return json({id:'test',session_id:'test-session',name:'Reference test',email:'test@example.test'});
    if(url.pathname==='/v1/organizations')return json({data:[{id:'11111111-1111-4111-8111-111111111111',name:'Test organization',role:'admin',balance_usd:0,available_usd:0,storage_bytes:2500}]});
 if(url.pathname==='/v1/api-keys'&&request.method()==='POST')return json({secret:'fixture-key'});
    if(url.pathname==='/v1/key')return json({key:{id:'fixture-key',name:'Reference test',organization_id:'11111111-1111-4111-8111-111111111111',resource_mode:'shared',scopes:['generation']},usage:{consumed_usd:0},organization:{id:'11111111-1111-4111-8111-111111111111',name:'Test organization',role:'admin',balance_usd:10,credit_limit_usd:0}});
    if(url.pathname==='/v1/providers')return json({data:catalog});
    if(url.pathname==='/v1/generations' && request.method()==='GET')return json({data:tasks});
    if(url.pathname==='/v1/references')return json({data:[]});
    if(url.pathname==='/v1/generations/estimate'){estimates.push(request.postDataJSON());return json({cost:{expected_usd:0},can_generate:true});}
    if(url.pathname==='/v1/generations'){posts.push(request.postDataJSON());return json({...posts.at(-1),id:'task-1',status:'queued',created_at:Math.floor(Date.now()/1000)});}
    return json({});
  });
  try {
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    assert.equal(await page.locator('#generationFields').isVisible(),false);
    assert.equal(await page.locator('#provider').count(),0);
    await page.locator('#createGenerationKey').click();
    await page.waitForFunction(()=>selectedModel()?.id==='shared-id');
    assert.equal(await page.locator('#mediaType').count(),0);
    assert.deepEqual(await page.locator('#model optgroup').evaluateAll(es=>es.map(e=>[e.label,e.children.length])),[['Alibaba',2],['BytePlus',2]]);
    const choose=async(label,option)=>{await page.getByRole('combobox',{name:label,exact:true}).click();await page.getByRole('option',{name:option}).click();};
    await page.getByRole('combobox',{name:'Model',exact:true}).click();
    assert.deepEqual(await page.locator('.picker-group').allTextContents(),['Alibaba','BytePlus']);
    await page.screenshot({path:path.join(output,'grouped-models-desktop.png')});
    await page.getByRole('option',{name:/Video B/}).click();
    assert.equal(await page.locator('#resolution').inputValue(),'1080p');
    assert.equal(await page.locator('#duration').inputValue(),'8');
    await page.locator('#prompt').fill('A free fixture generation');
    await page.waitForFunction(()=>document.querySelector('#priceValue').textContent==='Free');
    assert.equal(estimates.at(-1).provider,undefined);assert.equal(estimates.at(-1).model,'other/shared-id');
    await page.locator('#submitButton').click();
    assert.match(await page.locator('#confirmFields').innerText(),/Alibaba/);
    await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);
    assert.equal(posts[0].provider,undefined);assert.equal(posts[0].model,'other/shared-id');
    await choose('Model',/Image A/);
    assert.equal(await page.locator('#durationField').isVisible(),false);
    await choose('Model',/Speech B/);
    assert.equal(await page.locator('#voiceField').isVisible(),true);
    await page.evaluate(()=>reuseTask({provider:'other',model:'shared-id',media_type:'video',prompt:'Reuse',options:{resolution:'1080p',duration:7}}));
    assert.deepEqual(await page.evaluate(()=>[selectedProvider().id,selectedModel().id]),['other','shared-id']);
    assert.equal(await page.evaluate(()=>selectedModel().media_type),'video');
    assert.equal(await page.locator('#duration').inputValue(),'7');
    await choose('Model',/Video A/);
    assert.equal(await page.locator('#resolution').inputValue(),'720p');
    for(const width of [390,320]){
      await page.setViewportSize({width,height:900});
      await page.getByRole('combobox',{name:'Model',exact:true}).click();
      assert.deepEqual(await page.locator('.picker-group').allTextContents(),['Alibaba','BytePlus']);
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
      await page.screenshot({path:path.join(output,`grouped-models-${width}.png`)});
      await page.getByRole('option',{name:/Video B/}).click();
      assert.equal(await page.evaluate(()=>selectedProvider().id),'other');
    }
    await page.evaluate(()=>{
      const p=providerCatalog.find(p=>p.id==='other');
      p.models[0].pricing={known:true,reference_price_usd:1.25,reference_description:'1 image. Gateway price. Final cost depends on actual usage.',reference_quote:{expected_usd:1.25,customer_expected_usd:1.25}};
      p.models[0].metadata={description:'<script>unsafe</script> safe text',release_date:'2026-07-20',checked_at:'2026-09-13',facts:[{label:'Details',value:'Useful information '.repeat(100)}],sources:[{title:'Official source',url:'https://example.com/models'},{title:'Unsafe link',url:'javascript:alert(1)'}]};
      p.models.push({...p.models[0],id:'new',display_name:'Video New',metadata:{description:'Newest',release_date:'2026-09-08'}});
      updateModelSelection();
    });
    assert.deepEqual(await page.locator('#model optgroup').first().locator('option').evaluateAll(es=>es.map(e=>e.textContent)),['Video New — ≈ US$1.25','Video B — ≈ US$1.25','Speech B']);
    await page.setViewportSize({width:1440,height:1000});
    await page.getByRole('combobox',{name:'Model',exact:true}).click();
    const info=page.getByRole('button',{name:'Information about Video B',exact:true});
    const before=await page.locator('#model').inputValue();
    await info.hover();await page.waitForSelector('.model-info-popup');
    assert.equal(await page.locator('#model').inputValue(),before);
    assert.match(await page.locator('.model-info-popup').innerText(),/<script>unsafe<\/script> safe text/);
    assert.equal(await page.locator('.model-info-popup script').count(),0);
    assert.match(await page.locator('.model-info-popup').innerText(),/Gateway price/);
    assert.match(await page.locator('.model-info-popup').innerText(),/1.25/);
    assert.doesNotMatch(await page.locator('.model-info-popup').innerText(),/Billing region|tokens ×/);
    assert.equal(await page.locator('.model-info-popup a').count(),1);
    await page.screenshot({path:path.join(output,'information-desktop.png')});
    await info.click();await page.keyboard.press('Escape');
    assert.equal(await page.locator('.model-info-popup').count(),0);
    assert.equal(await page.locator('.select-popup').count(),1);
    await info.focus();await page.keyboard.press('Enter');
    assert.equal(await page.locator('.model-info-popup').count(),1);
    await page.keyboard.press('Escape');
    for(const width of [390,320]){
      await page.setViewportSize({width,height:800});
      await info.tap();await page.waitForSelector('.model-info-popup');
      const box=await page.locator('.model-info-popup').boundingBox();
      assert.ok(box.x>=0&&box.x+box.width<=width&&box.y>=0&&box.y+box.height<=800);
      assert.equal(await page.locator('#model').inputValue(),before);
      await page.screenshot({path:path.join(output,'information-'+width+'.png')});
      await page.getByRole('button',{name:'Close model information',exact:true}).tap();
      assert.equal(await page.locator('.model-info-popup').count(),0);
    }
    await page.getByRole('option',{name:'Video New — ≈ US$1.25',exact:true}).tap();
    assert.equal(await page.evaluate(()=>selectedModel().id),'new');

    // Public generation responses carry distinct public and native model IDs.
    const seedance={...video,id:'dreamina-seedance-2-0-260128',public_id:'bytedance/dreamina-seedance-2-0-260128',display_name:'Seedance 2.0',resolutions:['480p','720p'],max_duration:15,supports_audio:true,supports_video_input:true,max_video_inputs:3,reference_modes:[{id:'none',label:'Text only',max_images:0,max_videos:0,max_audios:0},{id:'references',label:'References',min_images:0,max_images:9,min_videos:0,max_videos:3,min_audios:0,max_audios:3}]};
    const seedanceProvider={id:'bytedance',name:'BytePlus Seedance',models:[seedance]};
    catalog.push(seedanceProvider);
    const reference='/references/fixture/video.mp4?signature=fixture';
    tasks=[{id:'failed-seedance',provider:'bytedance',provider_model:seedance.id,model:seedance.public_id,media_type:'video',status:'failed',prompt:'Use @Video1 as the cinematic reference.',seconds:'15',size:'480p',options:{ratio:'16:9',generate_audio:true,seed:0,reference_mode:'references',video_urls:[reference]},created_at:Math.floor(Date.now()/1000),updated_at:Math.floor(Date.now()/1000),error:{message:'Fixture provider rejection'}}];
    await page.evaluate(async()=>{await loadProviders();await loadTasks(true);});
    const copiedTask=page.locator('[data-task-id="failed-seedance"]');
    for(const width of [1440,390,320]){
      await page.setViewportSize({width,height:1000});
      await page.evaluate(()=>updateModelSelection(modelSelectionValue('mock','shared-id')));
      await page.locator('#prompt').fill('Replace this draft');
      await copiedTask.locator('.task-menu summary').focus();await page.keyboard.press('Enter');
      await copiedTask.locator('.reuse-task').focus();await page.keyboard.press('Enter');
      assert.equal(await page.locator('#formMessage').innerText(),'Parameters copied. Ready to edit.');
      assert.deepEqual(await page.evaluate(()=>[selectedProvider().id,selectedModel().id]),['bytedance',seedance.id]);
      assert.equal(await page.locator('#prompt').inputValue(),tasks[0].prompt);
      assert.equal(await page.locator('#resolution').inputValue(),'480p');
      assert.equal(await page.locator('#duration').inputValue(),'15');
      assert.equal(await page.locator('#generateAudio').isChecked(),true);
      assert.equal(await page.locator('#seed').inputValue(),'0');
      assert.equal(await page.locator('#referenceMode').inputValue(),'references');
      assert.equal(await page.locator('#videoURL').inputValue(),reference);
      assert.equal(await page.locator('#videoSelectedReferences .reference-card').count(),1);
      assert.equal(await page.locator('#prompt').evaluate(e=>e===document.activeElement),true);
      assert.equal(await copiedTask.locator('.task-menu').getAttribute('open'),null);
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
      await page.screenshot({path:path.join(output,`seedance-copy-${width}.png`)});
    }
    // Renaming a public alias must not prevent copying the original task.
    seedance.public_id='cinema-renamed';await page.evaluate(()=>loadProviders());
    await copiedTask.locator('.task-menu summary').click();await copiedTask.locator('.reuse-task').click();
    assert.equal(await page.locator('#formMessage').innerText(),'Parameters copied. Ready to edit.');
    assert.equal(await page.evaluate(()=>estimateRequest().model),seedance.id);
    assert.equal(await page.evaluate(()=>generationRequest(estimateRequest()).model),'cinema-renamed');
    seedanceProvider.models=[];await page.evaluate(()=>loadProviders());
    await page.locator('#prompt').fill('Keep this draft');
    const selection=await page.locator('#model').inputValue();
    await copiedTask.locator('.task-menu summary').click();await copiedTask.locator('.reuse-task').click();
    assert.match(await page.locator('#formMessage').innerText(),/provider or model is unavailable/);
    assert.equal(await page.locator('#prompt').inputValue(),'Keep this draft');
    assert.equal(await page.locator('#model').inputValue(),selection);
    // Connection IDs and refreshed mode descriptors are part of model identity.
    for(const theme of ['light','dark'])for(const width of [1440,390,320]){
      await page.setViewportSize({width,height:1000});
      await page.evaluate(({theme})=>{
        document.documentElement.dataset.theme=theme;
        const model=(id)=>({id:'shared-reference-model',display_name:id,media_type:'video',supports_image_input:true,max_image_inputs:2,reference_modes:[{id,label:id,min_images:1,max_images:2,max_videos:0,max_audios:0}]});
        providerCatalog=[{id:'connection-a',name:'A',models:[model('first_frame')]},{id:'connection-b',name:'B',models:[model('references')]}];
        updateModelSelection(modelSelectionValue('connection-a','shared-reference-model'));
      },{theme});
      assert.equal(await page.locator('#referenceMode').inputValue(),'first_frame');
      await page.evaluate(()=>updateModelSelection(modelSelectionValue('connection-b','shared-reference-model')));
      assert.equal(await page.locator('#referenceMode').inputValue(),'references');
      await page.evaluate(()=>{
        selectedModel().reference_modes.unshift({id:'first_frame',label:'First frame',min_images:1,max_images:1,max_videos:0,max_audios:0});
        selectedModel().reference_modes[1].label='Updated references';
        referencePicker.configure();
      });
      assert.equal(await page.locator('#referenceMode').inputValue(),'references');
      assert.deepEqual(await page.locator('#referenceMode option').allTextContents(),['First frame','Updated references']);
      await page.evaluate(()=>{selectedModel().reference_modes.shift();selectedModel().reference_modes[0]={id:'first_frame',label:'Replacement',min_images:1,max_images:1,max_videos:0,max_audios:0};referencePicker.configure();});
      assert.equal(await page.locator('#referenceMode').inputValue(),'first_frame');
      await page.getByRole('combobox',{name:'Reference mode',exact:true}).focus();
      await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter');
      assert.equal(await page.locator('#referenceMode').inputValue(),'first_frame');
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
    }
    catalog=[];await page.evaluate(()=>loadProviders());
    assert.equal(await page.locator('#model').isDisabled(),true);
    assert.equal(await page.locator('#generationSettings').isVisible(),false);
    assert.equal(await page.locator('#submitButton').isDisabled(),true);
    assert.equal(await page.locator('#priceCard').isVisible(),false);
    assert.deepEqual(errors.filter(e=>!e.includes('ResizeObserver loop completed')),[]);
    console.log('Grouped model selection passed: media types, provider groups, duplicate model IDs, capabilities, estimates, submission, failed Seedance reuse, renamed aliases, unavailable models, keyboard, narrow screens and empty catalog.');
  } finally {await browser.close();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
