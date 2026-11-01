const {useFixtureKey}=require('./fixture-key.cjs');
const {evidencePath} = require('./evidence.cjs');
// Cross-contract UI checks use Sonilo's actual Go catalog and local API fixtures.
const {chromium}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),{execFileSync}=require('node:child_process');
const repo=path.resolve(__dirname,'../..'),root=path.join(repo,'web/public/ui');
fs.mkdirSync(path.join(repo,'dist'),{recursive:true});
const temp=fs.mkdtempSync(path.join(repo,'dist/sonilo-ui-'));
let catalog;
try{
 fs.writeFileSync(path.join(temp,'main.go'),'package main\nimport("encoding/json";"os";"github.com/Sociaro-Research/open-media-gateway/plugins/provider/sonilo")\nfunc main(){d:=sonilo.Definition();json.NewEncoder(os.Stdout).Encode(map[string]any{"fields":d.Fields,"models":d.Profiles()})}\n');
 catalog=JSON.parse(execFileSync('go',['run',path.join(temp,'main.go')],{cwd:repo,encoding:'utf8'}));
}finally{fs.rmSync(temp,{recursive:true,force:true});}
const provider={...catalog,id:'sonilo',name:'Sonilo',live:true,configured:true,credentials_configured:true,enabled:true,status:'connected',default_model:'text-to-music',model_count:catalog.models.length,values:{base_url:'https://api.sonilo.com/v1',sound_price:'0.02'},hints:{api_key:'••••test'},base_url:'https://api.sonilo.com/v1',credential_source:'console'};
const server=http.createServer((req,res)=>{
 const name=path.basename(req.url.split('?')[0]),file=path.join(root,name.includes('.')?name:'index.html');
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[],submissions=[];
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/v1/**',async route=>{
  const req=route.request(),p=new URL(req.url()).pathname,json=body=>route.fulfill({json:body});
  if(p==='/v1/info')return json({provider:'sonilo',auth_mode:'google',console:{generation_ui:true,personal_providers:true}});
  if(p==='/v1/me')return json({id:'fixture-user',name:'Developer',email:'test@example.test',is_system_admin:true});
  if(p==='/v1/api-keys'&&req.method()==='POST')return json({name:'Sonilo fixture',secret:'fixture-generation-key'});
  if(['/v1/api-keys','/v1/organizations','/v1/references'].includes(p)||p==='/v1/generations'&&req.method()==='GET')return json({data:[]});
  if(p==='/v1/key')return json({key:{name:'Sonilo fixture'},usage:{remaining_usd:null}});
  if(p==='/v1/providers'||p==='/v1/admin/providers')return json({data:[provider]});
  if(p==='/v1/admin/providers/sonilo')return json(provider);
  if(p==='/v1/generations/estimate')return json({cost:{expected_usd:0.5,pricing:{estimate_description:'Reserves the maximum supported input duration.'}},can_generate:true});
  if(p==='/v1/generations'&&req.method()==='POST'){
   const body=req.postDataJSON();submissions.push(body);
   return json({id:'fixture-job',model:body.model,status:'queued',media_type:'music',created_at:new Date().toISOString()});
  }
  return json({});
 });
 try{
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  await useFixtureKey(page,'fixture-generation-key');
  await page.waitForFunction(()=>selectedModel()?.id==='text-to-music');
  await page.locator('#prompt').fill('Piano and strings');
  await page.locator('.advanced-settings summary').click();
  assert.equal(await page.locator('#duration').isVisible(),true);
  await page.locator('#duration').fill('45');
  const music=await page.evaluate(()=>generationRequest(estimateRequest()));
  assert.equal(music.options.duration,45);assert.equal(music.options.format,'m4a');assert.equal(music.seconds,undefined);
  for(const model of catalog.models){
   await page.locator('#model').selectOption(JSON.stringify(['sonilo',model.id]));
   await page.waitForFunction(id=>selectedModel()?.id===id,model.id);
   for(const kind of ['video','audio'])if(model[`supports_${kind}_input`]){
    const manual=page.locator(`#${kind}URLField details`);
    if(!await manual.evaluate(el=>el.open))await manual.locator('summary').click();
    await page.locator(`#${kind}URL`).fill(`https://cdn.example/${kind==='video'?'reference.mp4':'voice.wav'}`);
   }
   if(model.no_prompt)await page.locator('#model-param-music_url').fill('https://cdn.example/music.wav');
   if(model.prompt_optional&&!model.no_prompt)await page.locator('#prompt').fill('');
   if(!model.prompt_optional)await page.locator('#prompt').fill('Fixture direction');
   const body=await page.evaluate(()=>generationRequest(estimateRequest()));
   assert.equal(body.model,'sonilo/'+model.id);
   assert.equal(await page.locator('#duration').isVisible(),model.id==='text-to-music');
   assert.equal(await page.locator('#prompt').isVisible(),!model.no_prompt);
   assert.ok(!('watermark' in body.options)&&!('seed' in body.options)&&!('ratio' in body.options));
   if(model.duration_from_input)assert.equal(body.options.duration,undefined);
   if(model.no_prompt){
    assert.equal(body.prompt,'');
    await page.locator('#submitButton').click();await page.locator('#confirmDialog').waitFor({state:'visible'});
    await page.keyboard.press('Escape');await page.locator('#confirmDialog').waitFor({state:'hidden'});
   }
   if(model.media_type==='video')assert.equal(body.options.generate_audio,true);
  }
  await page.locator('#model').selectOption(JSON.stringify(['sonilo','text-to-sfx']));
  await page.locator('#prompt').fill('A click');await page.locator('#model-param-duration_seconds').fill('0.5');
  const sfx=await page.evaluate(()=>generationRequest(estimateRequest()));
  assert.equal(sfx.options.parameters.duration_seconds,0.5);assert.equal(sfx.options.format,'aac');
  await page.locator('#model').selectOption(JSON.stringify(['sonilo','video-to-sound']));
  await page.locator('#prompt').fill('');await page.locator('#videoURL').fill('https://cdn.example/reference.mp4');
  await page.locator('#model-param-sfx_prompt').fill('Footsteps on gravel');
  const speechSwitch=page.locator('#model-param-preserve_speech');await speechSwitch.focus();await page.keyboard.press('Space');assert.equal(await speechSwitch.isChecked(),true);
  await page.locator('#submitButton').click();
  await page.locator('#confirmDialog').waitFor({state:'visible'});
  await page.keyboard.press('Escape');await page.locator('#confirmDialog').waitFor({state:'hidden'});
  assert.equal(submissions.length,0);
  fs.mkdirSync(evidencePath('/tmp/gateway-sonilo-ui'),{recursive:true});
  for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});
   for(const theme of ['dark','light']){
    await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
    await page.screenshot({path:evidencePath(`/tmp/gateway-sonilo-ui/${width}-${theme}.png`),fullPage:true});
    const overflow=await page.evaluate(()=>[...document.querySelectorAll('body *')].filter(el=>el.getBoundingClientRect().right>innerWidth+1&&el.getBoundingClientRect().width).map(el=>({tag:el.tagName,id:el.id,class:el.className,right:el.getBoundingClientRect().right})).slice(0,15));
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`page overflow at ${width}px (${theme}): ${JSON.stringify(overflow)}`);
   }
  }
  assert.equal(await page.locator('#model-param-sfx_prompt').evaluate(el=>!!el.closest('.floating-field')),true);
  await page.setViewportSize({width:1440,height:1100});
  await page.goto(`http://127.0.0.1:${server.address().port}/profile`);
  await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();
  await page.locator('[data-provider-id="sonilo"]').click();
  assert.equal(await page.locator('[data-provider-field="api_key"]').inputValue(),provider.hints.api_key);
  assert.equal(await page.locator('[data-provider-field="sound_price"]').inputValue(),'0.02');
  for(const key of ['video_music_price','video_sfx_price','video_sound_price'])assert.equal(await page.locator(`[data-provider-field="${key}"]`).count(),1);
  for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});
   for(const theme of ['dark','light']){
    await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
    await page.screenshot({path:evidencePath(`/tmp/gateway-sonilo-ui/connection-${width}-${theme}.png`),fullPage:true});
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),`connection overflow at ${width}px (${theme})`);
   }
  }
  assert.deepEqual(errors,[]);
  console.log('PASS: Sonilo catalog controls, music duration, fractional SFX, optional/no prompt, video outputs, tariffs, keyboard/Escape and both themes at 1440/390/320px.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
