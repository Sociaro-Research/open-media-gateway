const {useFixtureKey}=require('./fixture-key.cjs');
// Playground repeat/retry regressions against localhost fixtures only.
const {chromium, webkit} = require('playwright');
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const {evidencePath} = require('./evidence.cjs');
const root = process.env.UI_SOURCE_DIR || path.resolve(__dirname, '../../web/public/ui');
const engine = process.env.UI_BROWSER_ENGINE || 'chromium';
const output = evidencePath('/tmp/gateway-playground-repeat/' + engine + '/');
fs.mkdirSync(output, {recursive:true});
const server = http.createServer((req, res) => {
  const file = path.join(root, req.url === '/' ? 'index.html' : path.basename(req.url.split('?')[0]));
  if (!fs.existsSync(file)) return res.writeHead(404).end();
  res.setHeader('Content-Type', file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html');
  res.end(fs.readFileSync(file));
});
const video = {id:'video',display_name:'Test Video',media_type:'video',resolutions:['480p','720p'],ratios:['16:9','9:16'],min_duration:2,max_duration:15,default_duration:5,supports_audio:true,supports_camera_fixed:true,supports_draft:true,supports_last_frame:true,supports_image_input:true,supports_video_input:true,supports_audio_input:true,supports_negative_prompt:true};
const speech = {id:'speech',display_name:'Test Speech',media_type:'speech',supports_voice:true,voices:['Cherry','Serena'],formats:['wav','mp3']};
const music = {id:'music',display_name:'Test Music',media_type:'music',supports_lyrics:true,supports_gender:true,formats:['mp3','wav']};
const image = {id:'image',display_name:'Test Image',media_type:'image',supports_image_input:true,resolutions:['1024*1024'],qualities:['standard','high'],backgrounds:['auto','transparent'],formats:['png','webp']};
const required = {...video,id:'required',display_name:'Required Video',requires_image_input:true};
const markers = {...image,id:'markers',display_name:'Numbered Image',reference_prompt_syntax:'numbered'};
const audio = {id:'audio',display_name:'Audio Conversion',media_type:'audio',no_prompt:true,supports_audio_input:true,requires_audio_input:true};
const parameterized = {...image,id:'parameters',display_name:'Custom Image',parameters:[{key:'strength',type:'number',required:true,min:0,max:1}]};
const providers = [{id:'mock',name:'Test provider',live:false,credentials_configured:true,models:[video,speech,music,image,required,markers,audio,parameterized],default_model:'video'}];
const historical = (id, model, fields) => ({id,provider:'mock',provider_model:model.id,model:'mock/'+model.id,media_type:model.media_type,status:'completed',created_at:1700000000,updated_at:1700000001,...fields});
const history = [
  historical('history-video',video,{prompt:'Historic video',seconds:'8',size:'720p',options:{ratio:'9:16',seed:0,watermark:true,generate_audio:true,camera_fixed:true,draft:true,return_last_frame:true,negative_prompt:'blur',image_urls:['https://fixture.test/image.png'],video_urls:['https://fixture.test/video.mp4','https://fixture.test/video-2.mp4'],audio_urls:['https://fixture.test/audio.wav','https://fixture.test/audio-2.wav'],reference_voices:{'https://fixture.test/image.png':'https://fixture.test/voice.wav'}}}),
  historical('history-speech',speech,{input:'Historic speech',voice:'Serena',instructions:'Warm',response_format:'mp3',options:{language:'English'}}),
  historical('history-music',music,{prompt:'',options:{lyrics:'Verse one\nChorus',gender:'male',format:'wav'}}),
  historical('history-image',image,{prompt:'Historic image',size:'1024x1024',quality:'high',background:'transparent',output_format:'webp',options:{image_urls:['https://fixture.test/image.png']}}),
  historical('history-audio',audio,{prompt:'',options:{audio_urls:['https://fixture.test/audio.wav']}}),
  historical('history-parameters',parameterized,{prompt:'Historic parameters',options:{parameters:{strength:0}}}),
  historical('missing-prompt',video,{options:{}}),
  historical('missing-input',required,{prompt:'Historic missing input',options:{}}),
  historical('missing-marker',markers,{prompt:'Use Image 2',options:{image_urls:['https://fixture.test/image.png']}}),
  historical('unavailable', {...video,id:'removed'}, {prompt:'Historic unavailable',options:{}})
];
const deferred = () => { let resolve; const promise = new Promise(r => {resolve=r;}); return {promise,resolve}; };
(async () => {
  await new Promise(resolve => server.listen(0,'127.0.0.1',resolve));
  const browser = await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH ? {executablePath:process.env.UI_BROWSER_PATH} : {})});
  const context = await browser.newContext({viewport:{width:1440,height:1100},reducedMotion:'reduce'});
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  const errors=[],notices=[],external=[],posts=[],estimates=[],results=[];
  let tasks=[...history], postGate, quoteGate, failure, quoteFailure, canGenerate=true, price=0.25;
  page.on('pageerror', error => {
    // WebKit reports this scheduling notice when shared ResizeObservers settle
    // after viewport changes; retain it separately, as in provider-assets.cjs.
    if(engine==='webkit'&&error.message==='ResizeObserver loop completed with undelivered notifications.')notices.push(error.message);
    else errors.push(error.message);
  });
  await page.route('**/*', async route => {
    const req=route.request(),url=new URL(req.url());
    if(url.hostname!=='127.0.0.1') {external.push(req.url());return route.abort();}
    if(!url.pathname.startsWith('/v1/'))return route.continue();
    const json=body=>route.fulfill({json:body});
    if(url.pathname==='/v1/playground-key/restore')return route.fulfill({status:404,json:{error:{message:'No saved fixture key'}}});
    if(url.pathname==='/v1/info')return json({provider:'mock',auth_mode:'google'});
    if(url.pathname==='/v1/me')return json({id:'preview',name:'Preview',email:'preview@example.test',is_system_admin:true});
    if(url.pathname==='/v1/organizations')return json({data:[]});
    if(url.pathname==='/v1/api-keys'&&req.method()==='POST')return json({name:'Fixture key',secret:'fixture-generation-key'});
    if(url.pathname==='/v1/key')return json({key:{name:'Fixture key',resource_mode:'personal'}});
    if(url.pathname==='/v1/providers')return json({data:providers});
    if(url.pathname==='/v1/generations/estimate'){
      assert.equal(req.headers().authorization,'Bearer fixture-generation-key');
      const body=req.postDataJSON();estimates.push(body);
      if(quoteGate&&body.prompt==='Historic video')await quoteGate.promise;
      if(quoteFailure)return route.fulfill({status:403,json:{error:{message:quoteFailure}}});
      // The real estimate API runs prepare/normalizeOptions; fixture its validation failure.
      if(body.model==='mock/required'&&!body.input_reference?.length&&!body.options.image_urls?.length)return route.fulfill({status:400,json:{error:{message:'This model requires an image reference'}}});
      return json({cost:{expected_usd:0.1,customer_expected_usd:price},can_generate:canGenerate,remaining_usd:0});
    }
    if(url.pathname==='/v1/generations'&&req.method()==='GET')return json({data:tasks});
    if(url.pathname==='/v1/generations'&&req.method()==='POST'){
      assert.equal(req.headers().authorization,'Bearer fixture-generation-key');
      const body=req.postDataJSON();posts.push({key:req.headers()['idempotency-key'],body});
      if(postGate)await postGate.promise;
      if(failure)return route.fulfill({status:failure.status,json:{error:{code:failure.code,message:failure.message}}});
      const model=providers[0].models.find(m=>'mock/'+m.id===body.model);
      const task=historical('new-'+posts.length,model,{...body,status:'queued',created_at:Math.floor(Date.now()/1000)});
      tasks.unshift(task);return json(task);
    }
    return json({data:[]});
  });
  const state=()=>page.locator('#taskForm').evaluate(form=>[...form.querySelectorAll('input:not([type=file]),textarea,select')].map(input=>({id:input.id,value:input.value,checked:input.checked,disabled:input.disabled})));
  const record=name=>{results.push(name);console.log('PASS:',name);};
  const hidden=()=>page.locator('#confirmDialog').waitFor({state:'hidden'});
  const ready=()=>page.waitForFunction(()=>document.querySelector('#confirmDialog').open&&!document.querySelector('#confirmSubmit').disabled);
  const retry=async id=>{const card=page.locator(`[data-task-id="${id}"]`);await card.locator('.task-menu summary').click();await card.locator('.retry-task').click();};
  const confirm=async()=>{await ready();await page.locator('#confirmSubmit').click();await hidden();};
  try {
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.locator('#managedGenerationHelp').waitFor({state:'visible'});
    await useFixtureKey(page,'fixture-generation-key');
    await page.waitForFunction(()=>selectedModel()?.id==='video');
    await page.locator('#prompt').fill('A repeatable draft');
    await page.locator('#duration').fill('8');await page.locator('#ratio').selectOption('9:16');
    await page.locator('#negativePrompt').fill('blur');
    await page.locator('#imageURLsField .reference-manual summary').click();await page.locator('#imageURLs').fill('https://fixture.test/draft.png');
    await page.locator('.advanced-settings summary').click();await page.locator('#seed').fill('0');
    await page.locator('#watermark').check();await page.locator('#generateAudio').check();
    const original=await state();
    for(let i=0;i<2;i++){
      await page.locator('#submitButton').click();await ready();
      await page.locator('#confirmSubmit').evaluate(button=>{button.click();button.click();});
      await hidden();assert.deepEqual(await state(),original);assert.equal(posts.length,i+1);
    }
    assert.deepEqual(posts[0].body,posts[1].body);assert.notEqual(posts[0].key,posts[1].key);
    record('Identical repeat submit retains every field; each confirmed generation has a fresh idempotency key; duplicate confirms send once');

    postGate=deferred();await page.locator('#submitButton').click();await ready();await page.locator('#confirmSubmit').click();
    await page.waitForFunction(()=>document.querySelector('#confirmDialog').getAttribute('aria-busy')==='true');
    await page.evaluate(()=>{document.querySelector('#prompt').value='Newer draft during request';document.querySelector('#prompt').dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('#seed').value='21';});
    let edited=await state();postGate.resolve();postGate=null;await hidden();assert.deepEqual(await state(),edited);
    record('A late accepted response preserves newer prompt and settings');

    failure={status:503,message:'Temporary fixture failure'};
    postGate=deferred();await retry('history-video');await ready();await page.locator('#confirmSubmit').click();
    await page.waitForFunction(()=>document.querySelector('#confirmDialog').getAttribute('aria-busy')==='true');
    await page.evaluate(()=>{document.querySelector('#prompt').value='Newer draft during failed request';document.querySelector('#prompt').dispatchEvent(new Event('input',{bubbles:true}));});
    edited=await state();postGate.resolve();postGate=null;
    await page.getByText('Could not submit: Temporary fixture failure',{exact:true}).waitFor();
    assert.deepEqual(await state(),edited);const failed=posts.at(-1);
    failure=null;await confirm();assert.deepEqual(posts.at(-1),failed);assert.deepEqual(await state(),edited);
    record('History submission error preserves draft; retry uses identical body and idempotency key');

    for(const close of ['#cancelConfirm','#closeConfirm','Escape']){
      const before=posts.length;await retry('history-video');await ready();
      if(close==='Escape')await page.keyboard.press('Escape');else await page.locator(close).click();
      await hidden();assert.equal(posts.length,before);assert.deepEqual(await state(),edited);
    }
    record('Cancel, Close and Escape submit nothing and preserve the full draft');

    // A cancelled estimate cannot enable or replace a subsequently opened confirmation.
    quoteGate=deferred();const beforeQuote=posts.length;await retry('history-video');
    await page.locator('#confirmSubmit').evaluate(button=>button.click());assert.equal(posts.length,beforeQuote);
    await page.waitForFunction(()=>document.querySelector('#confirmPrice').textContent==='Calculating…');
    await page.locator('#cancelConfirm').click();await retry('history-speech');await ready();
    const speechSummary=await page.locator('#confirmFields').innerText();quoteGate.resolve();quoteGate=null;
    await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
    assert.equal(await page.locator('#confirmFields').innerText(),speechSummary);await page.locator('#cancelConfirm').click();
    record('Cancelled quote response cannot overwrite a newer confirmation');

    for(const [id,model] of [['history-video',video],['history-speech',speech],['history-music',music],['history-image',image],['history-audio',audio],['history-parameters',parameterized]]){
      const before=posts.length;price=0.37;await retry(id);
      await page.locator(`[data-task-id="${id}"] .retry-task`).evaluate(button=>{button.click();button.click();});
      await ready();assert.equal(posts.length,before);
      assert.match(await page.locator('#confirmPrice').innerText(),/0.37/);
      const estimated=estimates.filter(body=>body.model==='mock/'+model.id).at(-1);
      await page.locator('#confirmSubmit').evaluate(button=>{button.click();button.click();});await hidden();
      assert.equal(posts.length,before+1);assert.deepEqual(posts.at(-1).body,{...estimated,max_expected_cost_usd:0.37});
      assert.deepEqual(await state(),edited);
    }
    const submitted=id=>posts.find(post=>post.body.model==='mock/'+id)?.body;
    const historicVideo=posts.find(post=>post.body.prompt==='Historic video').body;
    assert.deepEqual(historicVideo.options.video_urls,history[0].options.video_urls);assert.deepEqual(historicVideo.options.audio_urls,history[0].options.audio_urls);
    assert.equal(submitted('speech').input,'Historic speech');assert.equal(submitted('speech').voice,'Serena');assert.equal(submitted('speech').response_format,'mp3');
    assert.equal(submitted('music').options.lyrics,'Verse one\nChorus');assert.equal(submitted('image').quality,'high');assert.equal(submitted('image').output_format,'webp');
    assert.deepEqual(submitted('audio').options.audio_urls,['https://fixture.test/audio.wav']);assert.deepEqual(submitted('parameters').options.parameters,{strength:0});
    record('Video, speech, lyrics-only music, image, no-prompt audio and custom parameters use saved inputs and fresh customer costs without editing the draft');

    for(const [id,message] of [['unavailable','provider or model is unavailable'],['missing-prompt','missing its prompt'],['missing-marker','Missing image references: Image 2']]){
      const before=posts.length;await retry(id);assert.equal(await page.locator('#confirmDialog').isVisible(),false);assert.match(await page.locator('#formMessage').innerText(),new RegExp(message));assert.equal(posts.length,before);assert.deepEqual(await state(),edited);
    }
    await retry('missing-input');await page.getByText('This model requires an image reference',{exact:true}).waitFor();assert.equal(await page.locator('#confirmSubmit').isDisabled(),true);await page.locator('#cancelConfirm').click();
    record('Unavailable models, missing prompts, missing prompt references and estimate input validation block generation clearly');

    for(const error of ['Permission denied','Pricing unavailable']){
      quoteFailure=error;const before=posts.length;await retry('history-video');await page.locator('#confirmStatus').getByText(error,{exact:true}).waitFor();assert.equal(await page.locator('#confirmSubmit').isDisabled(),true);await page.locator('#cancelConfirm').click();assert.equal(posts.length,before);
    }
    quoteFailure=null;canGenerate=false;await retry('history-video');await page.getByText(/Your budget is exhausted/).waitFor();assert.equal(await page.locator('#confirmSubmit').isDisabled(),true);await page.locator('#cancelConfirm').click();canGenerate=true;
    record('Permission, pricing and exhausted budget estimates cannot be confirmed');

    failure={status:409,code:'estimate_changed',message:'Price changed'};await retry('history-video');await ready();await page.locator('#confirmSubmit').click();await hidden();assert.deepEqual(await state(),edited);
    await page.getByText(/Price changed. Review the updated estimate/).waitFor();failure=null;price=0.49;await retry('history-video');await ready();assert.match(await page.locator('#confirmPrice').innerText(),/0.49/);await page.locator('#cancelConfirm').click();
    record('Changed-price submission closes for a fresh review and preserves the draft');

    for(const theme of ['dark','light'])for(const width of [1440,390,320]){
      await page.setViewportSize({width,height:1100});await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;},theme);
      const card=page.locator('[data-task-id="history-video"]');await card.locator('.task-menu summary').focus();await page.keyboard.press('Enter');await card.locator('.retry-task').focus();await page.keyboard.press('Enter');await ready();
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'page overflow');
      const box=await page.locator('#confirmDialog').boundingBox();assert.ok(box.x>=0&&box.x+box.width<=width+1,'dialog fits viewport');
      await page.screenshot({path:path.join(output,`confirmation-${theme}-${width}.png`)});
      await page.keyboard.press('Escape');await hidden();assert.equal(await card.locator('.task-menu summary').evaluate(el=>el===document.activeElement),true);assert.deepEqual(await state(),edited);
    }
    record('Keyboard history action, Escape focus return and both themes at 1440/390/320px');
    assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
    if(notices.length)console.log(`WebKit ResizeObserver scheduling notices: ${notices.length}`);
    fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,results,posts:posts.length,estimates:estimates.length,pageErrors:errors,browserNotices:notices,externalRequests:external},null,2));
  } catch(error){await page.screenshot({path:path.join(output,'failure.png'),fullPage:true});console.error({errors,external});throw error;}
  finally {postGate?.resolve();quoteGate?.resolve();await browser.close();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
