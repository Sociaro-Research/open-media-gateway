// GW-305: real Playground controls and serialization, with loopback fixtures only.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {generationFixtureResponse}=require('./generation-page-fixture.cjs');
const {useFixtureKey}=require('./fixture-key.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',before=process.env.UI_CAPTURE_BEFORE==='1';
const root=path.resolve(__dirname,'../../web/public/ui'),out=path.resolve(process.env.UI_EVIDENCE_DIR||`.local/generation-toggle-copy/${engine}`);
fs.mkdirSync(out,{recursive:true});
const model={id:'toggles',display_name:'Toggle fixture',media_type:'video',supports_audio:true,supports_camera_fixed:true,resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5,pricing:{known:true,reference_price_usd:0}};
const catalog=[{id:'fixture',name:'Fixture',default_model:'toggles',models:[model,{...model,id:'plain',display_name:'No toggles',supports_audio:false,supports_camera_fixed:false,no_watermark:true},{...model,id:'always',display_name:'Always audio',audio_always_on:true}]}];
const posts=[],estimates=[],groups=[],errors=[];
const record=name=>{groups.push(name);console.log('PASS '+name);};
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture'),chunks=[];for await(const c of req)chunks.push(c);const raw=Buffer.concat(chunks).toString(),body=raw?JSON.parse(raw):undefined;
 const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(generationFixtureResponse(req,value,status)));};
 if(url.pathname.startsWith('/v1/')){
  if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'fixture'});
  if(url.pathname==='/v1/me')return json({id:'fixture-owner',session_id:'fixture',name:'Fixture',email:'fixture@example.test'});
  if(url.pathname==='/v1/key')return json({key:{id:'fixture-key',name:'Fixture',resource_mode:'personal'}});
  if(url.pathname==='/v1/playground-key/restore')return json({error:{message:'No fixture key'}},404);
  if(url.pathname==='/v1/providers')return json({data:catalog});
  if(url.pathname==='/v1/generations/estimate'){estimates.push(body);return json({cost:{expected_usd:0},can_generate:true});}
  if(url.pathname==='/v1/generations'&&req.method==='POST'){posts.push(body);return json({...body,id:'synthetic-task',status:'queued'});}
  return json({data:[]});
 }
 const file=path.join(root,url.pathname==='/'?'index.html':path.basename(url.pathname));if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin=`http://127.0.0.1:${server.address().port}`;
 const browser=await({chromium,webkit}[engine]).launch({headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:1000},hasTouch:true,reducedMotion:'reduce'});page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));
 await page.route(u=>u.origin!==origin,r=>r.abort());
 const controls=[['generateAudio','Audio','generate_audio'],['cameraFixed','Fixed camera','camera_fixed'],['watermark','Watermark','watermark']];
 const values=()=>page.evaluate(()=>{const o=requestOptions(selectedModel());return {generate_audio:o.generate_audio,camera_fixed:o.camera_fixed,watermark:o.watermark};});
 const choose=id=>page.locator('#model').evaluate((e,id)=>{e.value=JSON.stringify(['fixture',id]);e.dispatchEvent(new Event('change',{bubbles:true}));},id);
 try{
  const restored=page.waitForResponse(r=>r.url().endsWith('/v1/playground-key/restore'));await page.goto(origin);await restored;await page.waitForFunction(()=>window.Workspace?.ready()&&!document.querySelector('#restoreGenerationKey').disabled);await useFixtureKey(page,'fixture-key');await page.waitForFunction(()=>selectedModel()?.id==='toggles');
  await page.locator('.advanced-settings').evaluate(e=>e.open=true);await page.locator('#prompt').fill('Synthetic toggle fixture');
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   for(const [id,label]of controls){
    const input=page.locator('#'+id),parent=input.locator('..');await input.scrollIntoViewIfNeeded();
    if(!before){assert.equal(await page.getByRole('switch',{name:label,exact:true}).count(),1);assert.equal(await parent.locator('small').count(),0);assert.equal((await parent.innerText()).trim(),label);}
    const geometry=await parent.evaluate(e=>{const label=e.querySelector('span').getBoundingClientRect(),input=e.querySelector('input').getBoundingClientRect(),box=e.getBoundingClientRect();return{gap:input.left-label.right,centers:Math.abs((label.top+label.bottom-input.top-input.bottom)/2),w:input.width,h:input.height,left:box.left,right:box.right,padding:getComputedStyle(e).paddingInlineStart,weight:getComputedStyle(e.querySelector('strong')).fontWeight};});
    assert.ok(geometry.left>=0&&geometry.right<=width+1);assert.ok(geometry.centers<2);assert.ok(geometry.w>=41&&geometry.h>=23);if(!before){assert.equal(geometry.padding,'14px');assert.equal(geometry.weight,'400');assert.equal(await parent.evaluate(e=>getComputedStyle(e).columnGap),'12px');}
    await parent.screenshot({path:path.join(out,`${theme}-${width}-${id}.png`)});
   }
   if(!before)record(`${theme}/${width}: concise accessible labels, aligned switches, no subtitle nodes`);
  }
  if(before){fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,before:true,screenshots:18},null,2));return;}
  await page.setViewportSize({width:1440,height:1000});assert.deepEqual(await values(),{generate_audio:false,camera_fixed:false,watermark:false});record('Initial off values unchanged');
  for(const [id,label]of controls){const input=page.getByRole('switch',{name:label,exact:true});await input.focus();await input.press('Space');assert.equal(await input.isChecked(),true);assert.equal(await input.evaluate(e=>e.matches(':focus-visible')),true);await input.click();assert.equal(await input.isChecked(),false);await input.tap();assert.equal(await input.isChecked(),true);}
  assert.deepEqual(await values(),{generate_audio:true,camera_fixed:true,watermark:true});record('Keyboard, mouse and touch activate all three unchanged bindings');
  await page.locator('#submitButton').click();await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);assert.equal(posts.length,1);
  for(const key of ['generate_audio','camera_fixed','watermark'])assert.equal(posts[0].options[key],true);record('Actual mocked estimate/Confirm generation preserves true values');
  await page.evaluate(()=>reuseTask({provider:'fixture',model:'toggles',media_type:'video',prompt:'Restored literal prompt',options:{generate_audio:false,camera_fixed:true,watermark:false}}));
  assert.deepEqual(await values(),{generate_audio:false,camera_fixed:true,watermark:false});assert.equal(await page.locator('#prompt').inputValue(),'Restored literal prompt');record('Existing saved-request restore retains each independent value');
  await choose('plain');for(const id of ['audioOption','cameraOption','watermarkOption'])assert.equal(await page.locator('#'+id).isVisible(),false);assert.deepEqual(await values(),{generate_audio:undefined,camera_fixed:undefined,watermark:undefined});record('Unsupported model hides controls and omits their options');
  await choose('always');assert.equal(await page.locator('#audioOption').isVisible(),false);assert.equal((await values()).generate_audio,true);record('Always-on model keeps native audio true without a redundant switch');
  await choose('toggles');await page.locator('.advanced-settings').evaluate(e=>e.open=true);
  for(const[id]of controls){const input=page.locator('#'+id);await input.evaluate(e=>e.disabled=true);assert.equal(await input.isDisabled(),true);const old=await input.isChecked();await input.evaluate(e=>e.click());assert.equal(await input.isChecked(),old);await input.evaluate(e=>e.disabled=false);}
  record('Shared native disabled semantics remain intact (synthetic disabled state)');
  assert.deepEqual(errors,[]);record('No page errors');
  const hashes=Object.fromEntries(['index.html','app.js','styles.css'].map(file=>[file,require('node:crypto').createHash('sha256').update(fs.readFileSync(path.join(root,file))).digest('hex')]));
  fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,groups,posts,estimates,hashes,screenshots:18},null,2));
 }catch(error){console.error({errors,state:await page.evaluate(()=>({url:location.href,model:document.querySelector('#model')?.innerHTML,key:document.querySelector('#apiKey')?.value,message:document.querySelector('#message')?.textContent,body:document.body.innerText.slice(-1200)}))});throw error;}finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;});
