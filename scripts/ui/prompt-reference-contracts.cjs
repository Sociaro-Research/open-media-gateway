const {useFixtureKey}=require('./fixture-key.cjs');
// Shared reference recognizer against synthetic localhost contracts. No provider calls.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {evidencePath}=require('./evidence.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',output=evidencePath('/tmp/gateway-prompt-reference-contracts/'+engine+'/');fs.mkdirSync(output,{recursive:true});
const root=path.resolve(__dirname,'../../web/public/ui'),png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=','base64');
const server=http.createServer((req,res)=>{if(req.url.startsWith('/fixture/')){res.setHeader('Content-Type','image/png');return res.end(png)}const file=path.join(root,req.url==='/'?'index.html':path.basename(req.url.split('?')[0]));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
const mode={id:'references',label:'References',max_images:9,max_videos:3,max_audios:3};
const base={media_type:'video',supports_image_input:true,supports_video_input:true,supports_audio_input:true,max_image_inputs:9,resolutions:['720p'],ratios:['16:9'],default_duration:5,min_duration:2,max_duration:15,reference_modes:[mode]};
const models=[
 {...base,id:'seedance-renamed',reference_prompt_recommendation:{kind:'example',text:'The API examples use @Image1 and @Video1.',source_url:'https://api.example.test/endpoint',modes:['references']},reference_prompt_syntax:'numbered',reference_prompt_types:['image','video','audio']},
 {...base,id:'kling4-renamed',reference_prompt_syntax:'kling-id',reference_prompt_types:['image'],supports_video_input:false,supports_audio_input:false,reference_modes:[{id:'first_last_frame',label:'First and last frames',max_images:2,max_videos:0,max_audios:0}]},
 {...base,id:'happyhorse-renamed',reference_prompt_syntax:'bracket',reference_prompt_types:['image'],supports_video_input:false,supports_audio_input:false,reference_modes:[{...mode,max_videos:0,max_audios:0}]},
 {...base,id:'agnes-renamed',reference_prompt_syntax:'picture',reference_prompt_types:['image','video','audio']},
 {...base,id:'agnes-flash-renamed',reference_prompt_syntax:'picture',reference_prompt_types:['image','audio'],supports_video_input:false,reference_modes:[{...mode,max_videos:0}]},
 {...base,id:'minimax-h3-mini'}, {...base,id:'plain'},
 {...base,id:'native-keyframe-ratio',ratios:['16:9','adaptive'],reference_prompt_syntax:'numbered',reference_prompt_types:['image','video','audio'],reference_modes:[{id:'first_frame',label:'First frame',min_images:1,max_images:1,max_videos:0,max_audios:0,ratios:['adaptive']},mode]}
].map(m=>({...m,display_name:m.id}));
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin=`http://127.0.0.1:${server.address().port}`,browser=await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})}),context=await browser.newContext({viewport:{width:1440,height:1100},hasTouch:true,reducedMotion:'reduce'}),page=await context.newPage();page.setDefaultTimeout(15000);
 const errors=[],posts=[],estimates=[],external=[],results=[];
 const historic=models.slice(0,5).flatMap((model,i)=>{const prompt=["@Picture_2 {Video1} [Audio 1]","@image_2","[Image 2]","<Picture 2> <Video 1> <Audio 1>","<Picture 2> <Audio 1>"][i],options={reference_mode:model.reference_modes[0].id,image_urls:[origin+'/fixture/i1.png',origin+'/fixture/i2.png'],...(model.supports_video_input?{video_urls:[origin+'/fixture/v1.mp4']} : {}),...(model.supports_audio_input?{audio_urls:[origin+'/fixture/a1.wav']} : {})};const task={id:'historic-'+i,provider:'renamed-connection',provider_model:model.id,model:'renamed-connection/'+model.id,media_type:'video',status:'completed',created_at:1700000000,updated_at:1700000001,prompt,options};return [task,{...task,id:'missing-'+i,prompt:prompt.replace('2','3')}]});
 page.on('pageerror',e=>{if(engine==='webkit'&&e.message==='ResizeObserver loop completed with undelivered notifications.')return;errors.push(e.message)});
 await page.route('**/*',async route=>{const req=route.request(),url=new URL(req.url()),p=url.pathname;if(url.protocol==='blob:'&&url.origin===origin)return route.continue();if(url.hostname!=='127.0.0.1'){external.push(req.url());return route.abort()}if(!p.startsWith('/v1/'))return route.continue();const json=body=>route.fulfill({json:body});
 if(p==='/v1/info')return json({provider:'renamed-connection',auth_mode:'google'});
 if(p==='/v1/me')return json({id:'preview',name:'Preview',email:'preview@example.test',is_system_admin:true});
 if(p==='/v1/api-keys'&&req.method()==='POST')return json({name:'Fixture',secret:'fixture-key'});
 if(p==='/v1/key')return json({key:{name:'Fixture',resource_mode:'personal'}});
 if(p==='/v1/providers')return json({data:[{id:'renamed-connection',name:'Renamed connection',live:false,credentials_configured:true,default_model:models[0].id,models}]});
 if(p==='/v1/generations'&&req.method()==='GET')return json({data:historic});
 if(p==='/v1/generations/estimate'){estimates.push(req.postDataJSON());return json({cost:{expected_usd:0,customer_expected_usd:0},can_generate:true,remaining_usd:0})}
 if(p==='/v1/generations'&&req.method()==='POST'){assert.equal(req.headers().authorization,'Bearer fixture-key');const body=req.postDataJSON();posts.push(body);return json({id:'queued-'+posts.length,provider:body.model.split('/')[0],provider_model:body.model.split('/')[1],media_type:'video',status:'queued',created_at:1700000000,updated_at:1700000000})}
 return json({data:[]});});
 async function choose(id){await page.locator('#model').selectOption(JSON.stringify(['renamed-connection',id]));await page.waitForFunction(id=>selectedModel()?.id===id,id);}
 async function references(){await page.evaluate(origin=>{for(const [id,urls] of Object.entries({imageURLs:['/fixture/i1.png','/fixture/i2.png'],videoURL:['/fixture/v1.mp4'],audioURL:['/fixture/a1.wav']})){const e=document.getElementById(id);e.value=urls.map(v=>origin+v).join('\n');e.dispatchEvent(new Event('input',{bubbles:true}));}},origin);}
 async function fill(value){await page.locator('#prompt').fill(value);await page.waitForFunction(value=>document.querySelector('.prompt-reference-text').textContent===value,value);}
 async function confirm(){await page.locator('#submitButton').click();await page.locator('#confirmDialog[open]').waitFor();await page.waitForFunction(()=>!document.querySelector('#confirmSubmit').disabled);await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);}
 try{
 await page.goto(origin);await useFixtureKey(page,'fixture-key');await page.waitForFunction(()=>selectedModel()?.id==='seedance-renamed');
 for(const [id,value,kinds] of [
  ['seedance-renamed','Use @Image2, {Video_1} and [Audio1]. Literal <img src=x onerror=alert(1)>.',['image','video','audio']],
  ['kling4-renamed','Use @image_1 and @image_2. Literal <tag>.',['image','image']],
  ['happyhorse-renamed','Use [Image 1] and [Image 2]. Literal <tag>.',['image','image']],
  ['agnes-renamed','Use <Picture 2>, <Video 1> and <Audio 1>.',['image','video','audio']],
  ['agnes-flash-renamed','Use <Picture 2> and <Audio 1>.',['image','audio']]
 ]){
  await choose(id);await references();await fill(value);
  assert.deepEqual(await page.locator('.prompt-reference-token').evaluateAll(es=>es.map(e=>e.dataset.referenceKind)),kinds);assert.equal(await page.locator('.prompt-reference-token.missing').count(),0);assert.equal(await page.locator('.prompt-reference-mirror img').count(),0);assert.equal(await page.locator('#prompt').inputValue(),value);
  await confirm();const body=posts.at(-1);assert.equal(body.prompt,value);assert.deepEqual(body.input_reference,[origin+'/fixture/i1.png',origin+'/fixture/i2.png']);assert.equal(body.options.image_urls,undefined);
  const model=models.find(m=>m.id===id);assert.deepEqual(body.options.video_urls,model.supports_video_input?[origin+'/fixture/v1.mp4']:undefined);assert.deepEqual(body.options.audio_urls,model.supports_audio_input?[origin+'/fixture/a1.wav']:undefined);assert.equal(JSON.stringify(body).includes('reference_prompt_'),false);assert.equal(await page.locator('#prompt').inputValue(),value);results.push(id+': typed markers and exact immutable outgoing prompt/reference order');
 }
 await choose('seedance-renamed');await page.locator('.reference-recommendation').waitFor({state:'visible'});assert.match(await page.locator('.reference-recommendation').innerText(),/Provider API example:.*@Image1/);const literal='No marker: @Image 1, Image 1, character1, (Image1).';await fill(literal);assert.equal(await page.locator('.prompt-reference-token').count(),0);await confirm();assert.equal(posts.at(-1).prompt,literal);await choose('plain');await page.locator('.reference-recommendation').waitFor({state:'hidden'});results.push('Provider recommendation is informational, disappears on model change, and excluded forms transmit literally');
 await choose('native-keyframe-ratio');await page.locator('#referenceMode').selectOption('first_frame',{force:true});await page.waitForFunction(()=>document.querySelector('#ratio').value==='adaptive');assert.deepEqual(await page.locator('#ratio option').evaluateAll(nodes=>nodes.map(n=>n.value)),['adaptive']);
 await page.locator('#referenceMode').selectOption('references',{force:true});await page.waitForFunction(()=>document.querySelector('#ratio').options.length===2);await page.locator('#ratio').selectOption('16:9',{force:true});
 await page.locator('#referenceMode').selectOption('first_frame',{force:true});await page.waitForFunction(()=>document.querySelector('#ratio').value==='adaptive');results.push('Native workflow ratio constraints apply on mode changes without provider-name inference');
 await choose('seedance-renamed');await references();await fill('@Image3 @Video2 @Audio2');assert.equal(await page.locator('.prompt-reference-token.missing').count(),3);assert.match(await page.locator('#promptReferenceError').innerText(),/@Image3, @Video2, @Audio2/);const count=posts.length;await page.locator('#taskForm').evaluate(e=>e.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})));assert.equal(await page.locator('#confirmDialog').evaluate(e=>e.open),false);assert.equal(posts.length,count);
 await choose('kling4-renamed');await references();await fill('@image_0 @Image_1');assert.match(await page.locator('#promptReferenceError').innerText(),/Invalid reference/);await fill('@video_1');assert.match(await page.locator('#promptReferenceError').innerText(),/Unsupported reference type/);
 await choose('happyhorse-renamed');await references();await fill('Image 999 @Image 999 [Image 2]');assert.equal(await page.locator('.prompt-reference-token').count(),1);assert.equal(await page.locator('#prompt').evaluate(e=>e.validity.valid),true);
 await choose('agnes-flash-renamed');await references();await fill('<Video 1>');assert.match(await page.locator('#promptReferenceError').innerText(),/Unsupported reference type/);
 for(const id of ['minimax-h3-mini','plain']){await choose(id);await page.waitForFunction(()=>!document.querySelector('.prompt-reference-mirror').hidden);assert.equal(await page.locator('#prompt').evaluate(e=>e.validity.valid),true);assert.equal(await page.locator('#prompt').inputValue(),'<Video 1>');}
 results.push('Missing, malformed and unsupported types block form; model switch clears stale validity; capabilities without grammar metadata still use the common recognizer');
 const draft=await page.locator('#taskForm').evaluate(form=>[...form.querySelectorAll('input:not([type=file]),textarea,select')].map(e=>[e.id,e.value,e.checked,e.disabled]));
 const state=()=>page.locator('#taskForm').evaluate(form=>[...form.querySelectorAll('input:not([type=file]),textarea,select')].map(e=>[e.id,e.value,e.checked,e.disabled]));
 for(let i=0;i<5;i++){
  const good=historic.find(t=>t.id==='historic-'+i),card=page.locator('[data-task-id="'+good.id+'"]');await card.locator('.task-menu summary').click();await card.locator('.retry-task').click();await page.waitForFunction(()=>document.querySelector('#confirmDialog').open&&!document.querySelector('#confirmSubmit').disabled);await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);assert.equal(posts.at(-1).prompt,good.prompt);assert.deepEqual(posts.at(-1).input_reference,good.options.image_urls);assert.deepEqual(posts.at(-1).options.video_urls,good.options.video_urls);assert.deepEqual(posts.at(-1).options.audio_urls,good.options.audio_urls);assert.deepEqual(await state(),draft);
  const bad=page.locator('[data-task-id="missing-'+i+'"]'),before=posts.length;await bad.locator('.task-menu summary').click();await bad.locator('.retry-task').click();assert.equal(await page.locator('#confirmDialog').evaluate(e=>e.open),false);assert.match(await page.locator('#formMessage').innerText(),/Missing image references/);assert.equal(posts.length,before);assert.deepEqual(await state(),draft);
 }
 results.push('All five supported model retry snapshots use the common recognizer with their own supported media and preserve a different-model draft; missing historical inputs block retry');

 await choose('seedance-renamed');await references();const prompt='Use @Image1, @Video1 and @Audio1.';await fill(prompt);
 for(const theme of ['light','dark'])for(const width of [1440,390,320]){
  await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await page.setViewportSize({width,height:width===1440?1100:900});await page.locator('#prompt').scrollIntoViewIfNeeded();await page.waitForTimeout(100);
  for(const kind of ['image','video','audio']){const caret=prompt.indexOf(kind[0].toUpperCase()+kind.slice(1))+2;await page.locator('#prompt').focus();await page.locator('#prompt').evaluate((e,n)=>e.setSelectionRange(n,n),caret);await page.keyboard.press('Alt+ArrowDown');const media=page.locator('#promptReferencePreview '+(kind==='image'?'img':kind));await media.waitFor();assert.equal(await media.getAttribute('src'),origin+'/fixture/'+({image:'i1.png',video:'v1.mp4',audio:'a1.wav'}[kind]));if(kind!=='image'){assert.equal(await media.getAttribute('preload'),'none');assert.equal(await media.getAttribute('autoplay'),null)}const box=await page.locator('#promptReferencePreview').boundingBox();assert.ok(box.x>=0&&box.x+box.width<=width+1);if(kind==='audio')await page.screenshot({path:path.join(output,theme+'-'+width+'.png')});await page.keyboard.press('Escape');assert.equal(await page.locator('#promptReferencePreview').count(),0);}
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 }
 results.push('Typed image/video/audio keyboard previews, Escape, no autoplay, both themes at 1440/390/320 px');assert.deepEqual(errors,[]);assert.deepEqual(external,[]);fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,results,posts,estimateCount:estimates.length,errors,external},null,2));console.log('PASS '+engine+': '+results.join('; '));
 }catch(e){await page.screenshot({path:path.join(output,'failure.png'),fullPage:true});console.error(await page.locator('body').innerText());throw e}finally{await browser.close();server.close()}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
