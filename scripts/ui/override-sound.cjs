// Local-only early validation / retention / stale-response UI regressions.
const {chromium,webkit}=require('playwright');
const {useFixtureKey}=require('./fixture-key.cjs');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=path.resolve(__dirname,'../../web/public/ui');
const output=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/override-sound/ui-'+engine);fs.mkdirSync(output,{recursive:true});
// A real synthetic one-second WAV permits native playback verification.
const sample=Buffer.alloc(44+16000);sample.write('RIFF',0);sample.writeUInt32LE(sample.length-8,4);sample.write('WAVEfmt ',8);sample.writeUInt32LE(16,16);sample.writeUInt16LE(1,20);sample.writeUInt16LE(1,22);sample.writeUInt32LE(8000,24);sample.writeUInt32LE(16000,28);sample.writeUInt16LE(2,32);sample.writeUInt16LE(16,34);sample.write('data',36);sample.writeUInt32LE(16000,40);for(let i=0;i<8000;i++)sample.writeInt16LE(Math.round(4000*Math.sin(2*Math.PI*880*i/8000)),44+2*i);
const explanation='motion.mp4: average frame rate 23.8041220744 FPS; Strict accepts 24–60 FPS. Re-export at a supported constant frame rate.';
const model=id=>({id,display_name:id==='strict'?'Strict':'Other',media_type:'video',supports_image_input:true,supports_video_input:true,max_image_inputs:2,reference_prompt_syntax:'numbered',reference_prompt_types:['image','video'],reference_modes:[{id:'references',label:'References',min_images:0,max_images:2,min_videos:0,max_videos:2,min_audios:0,max_audios:0,supports_reference_voice:true}],resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:30,default_duration:5,pricing:{known:true,reference_price_usd:0}});
const catalog=[{id:'mock',name:'Mock',live:false,default_model:'strict',models:[model('strict'),model('other')]}];
let failVoice=false,failUpload=false,failModel='',assetCount=0,retained=[],writes=0,requests=[],uploadGate=null,validationGate=null,templateValidationGate=null,aggregateLimit=false,counter=0;
let record={id:'template',version:1,revision:1,draft:{title:'Synthetic template',description:'',provider:'mock',model:'strict',media_type:'video',syntax:'numbered',prompt:'Follow Video 1',options:{reference_mode:'references',duration:5,resolution:'720p',ratio:'16:9'},slots:[{id:'video:1',type:'video',index:1,role:'reference',label:'Motion',asset_id:'fixed'}],cover_id:'fixed'}};record.published=structuredClone(record.draft);
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture');let chunks=[];for await(const x of req)chunks.push(x);const raw=Buffer.concat(chunks);let body;try{body=JSON.parse(raw)}catch{}
 const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
 if(u.pathname.startsWith('/v1/')){
  requests.push({path:u.pathname,method:req.method,body});
  if(u.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true}});
  if(u.pathname==='/v1/me')return json({id:'owner',name:'Fixture',email:'fixture@example.test',is_system_admin:true});
  if(u.pathname==='/v1/key')return json({key:{name:'Fixture',resource_mode:'personal'}});
  if(u.pathname==='/v1/providers')return json({data:catalog});
  if(u.pathname==='/v1/references/uploads'){return json({mode:'local'});}
  if(u.pathname==='/v1/references'&&req.method==='POST'){

   if(uploadGate){const gate=uploadGate;uploadGate=null;gate.entered();await gate.promise;if(res.destroyed)return;}
   if(failUpload)return json({error:{message:explanation}},400);
   const id='owned-'+(++counter),item={id,name:raw.toString().match(/filename="([^"]+)"/)?.[1]||'motion.mp4',content_type:'audio/wav',url:'/references/owner/'+id+'?signature=fixture',size_bytes:sample.length};retained.push(item);return json(item,201);
  }
  if(u.pathname==='/v1/references')return json({data:retained,has_more:false,next_offset:retained.length});
  if(u.pathname==='/v1/references/validate'){
   if(validationGate){const gate=validationGate;validationGate=null;gate.entered();await gate.promise;if(res.destroyed)return;}
   if((failVoice&&body.validation?.role==='reference_voice')||body.validation?.model===failModel||body.reference_ids?.includes('bad-library'))return json({error:{message:explanation}},400);return json({valid:true});
  }
  if(u.pathname==='/v1/system/templates')return json({data:[record],catalog});
  if(u.pathname==='/v1/system/templates/library')return json({data:retained,has_more:false,next_offset:retained.length});
  if(u.pathname==='/v1/templates/template/metadata')return json({id:'template',revision:record.revision,template:record.published,validation_status:'pending'});
  if(u.pathname.endsWith('/metadata'))return json({id:'fixed',content_type:u.pathname.includes('/asset-')?'audio/wav':'video/mp4',name:u.pathname.includes('/asset-')?'replacement.wav':'motion.mp4'});
  if(u.pathname.includes('/assets/asset-')&&req.method==='GET'){res.writeHead(200,{'Content-Type':'audio/wav'});return res.end(sample);}
  if(u.pathname==='/v1/system/templates/template/assets'){
   if(uploadGate){const gate=uploadGate;uploadGate=null;gate.entered();await gate.promise;if(res.destroyed)return;}
   if(failUpload)return json({error:{message:explanation}},400);return json({id:'asset-'+(++assetCount),name:'replacement.wav',content_type:'audio/wav'},201);
  }
  if(u.pathname.endsWith('/validate-media')){
   if(templateValidationGate){const gate=templateValidationGate;templateValidationGate=null;gate.entered();await gate.promise;if(res.destroyed)return;if(gate.reject)return json({error:{message:'Previous reference is incompatible.'}},400);}
   if(aggregateLimit&&Object.keys(body.inputs||{}).length>1)return json({error:{message:'References: total video duration 14 seconds; maximum 12 seconds. Trim the videos.'}},400);
   if((body.draft?.model||body.model)===failModel)return json({error:{message:explanation}},400);return json({valid:true});
  }
  if(u.pathname==='/v1/system/templates/template'&&req.method==='PUT'){writes++;record={...record,version:record.version+1,draft:body.content||body.draft||record.draft,...(body.action==='unpublish'?{published:null}:{})};if(record.published){record.published=structuredClone(record.draft);record.revision=record.version;}return json(record);}
  if(u.pathname==='/v1/templates')return json({data:[{id:'template',title:'Synthetic template',cover_url:'/cover.png',cover_type:'image/png'}]});
  if(u.pathname==='/v1/templates/template')return json({validation_status:'ready',id:'template',revision:record.revision,template:record.published,catalog,default_available:true,asset_error:''});
  if(u.pathname.endsWith('/estimate'))return json({cost:{expected_usd:0},can_generate:true});
  return json({data:[]});
 }
 if(u.pathname==='/cover.png')return res.writeHead(404).end();
 const file=path.join(root,u.pathname==='/'?'index.html':path.basename(u.pathname));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
const gate=()=>{let release,entered;const promise=new Promise(r=>release=r),started=new Promise(r=>entered=r);return {promise,release,entered,started};};
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'});page.setDefaultTimeout(10000);
 let choosers=0;page.on('filechooser',()=>choosers++);const metrics=[];const errors=[],external=[],results=[];page.on('pageerror',e=>{if(!e.message.startsWith('ResizeObserver loop'))errors.push(e.message);});await page.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():(external.push(route.request().url()),route.abort()));
 const pass=text=>{results.push(text);console.log('PASS',text)};
 const upload=()=>page.locator('#videoReferenceFiles').setInputFiles({name:'motion.mp4',mimeType:'video/mp4',buffer:sample});
 const message=text=>page.waitForFunction(t=>document.querySelector('#videoReferenceStatus').textContent.includes(t),text);
 try{
 await page.goto(base);await page.waitForFunction(()=>authState==='authenticated');await useFixtureKey(page,'fixture-key');await page.waitForFunction(()=>selectedModel()?.id==='strict');
 const sound=page.locator('#generationFields > [data-override-sound]');
 const checkActions=async(scope,context)=>{const m=await scope.evaluate(n=>{const actions=n.querySelector('.reference-actions'),picker=actions.querySelector('[data-upload-picker]'),remove=actions.querySelector('[data-upload-remove]'),r=remove?.getBoundingClientRect(),box=n.querySelector('.reference-dropzone').getBoundingClientRect();return {visibleActions:[...actions.querySelectorAll('button')].filter(b=>!b.hidden).map(b=>b.textContent),gap:getComputedStyle(actions).gap,inside:!!r&&r.left>=box.left&&r.right<=box.right&&r.top>=box.top&&r.bottom<=box.bottom,pickerHidden:picker.hidden,removeDisabled:remove?.disabled};});assert.deepEqual(m.visibleActions,['Remove']);assert.equal(m.gap,'12px');assert.equal(m.inside,true);assert.equal(m.pickerHidden,true);metrics.push({context,width:page.viewportSize().width,...m});};
 assert.equal(await sound.getByRole('button',{name:'Remove',exact:true}).isVisible(),false);
 await sound.getByLabel('Override Sound file',{exact:true}).setInputFiles({name:'replacement.wav',mimeType:'audio/wav',buffer:sample});
 await sound.getByText('Override sound ready: replacement.wav',{exact:true}).waitFor();
 assert.equal(await page.evaluate(()=>estimateRequest().options.override_sound_id),'owned-1');pass('Playground upload binds validated audio ID to request options');await sound.locator('audio').evaluate(a=>a.play());await page.waitForFunction(()=>document.querySelector('#generationFields > [data-override-sound] audio').currentTime>0);await sound.locator('audio').evaluate(a=>a.pause());pass('Uploaded audio plays in native browser preview');
 await checkActions(sound,'playground-selected');const selectedWrites=requests.filter(r=>r.path==='/v1/references'&&r.method==='POST').length;
 await sound.getByLabel('Override Sound file',{exact:true}).setInputFiles({name:'ignored.wav',mimeType:'audio/wav',buffer:sample});assert.equal(requests.filter(r=>r.path==='/v1/references'&&r.method==='POST').length,selectedWrites);assert.equal(await page.evaluate(()=>estimateRequest().options.override_sound_id),'owned-1');
 await sound.getByRole('button',{name:'Remove',exact:true}).click();assert.equal(await page.evaluate(()=>estimateRequest().options.override_sound_id),undefined);assert.equal(choosers,0);assert.equal(await sound.locator('[data-upload-picker]').evaluate(n=>n===document.activeElement),true);
 failUpload=true;await sound.getByLabel('Override Sound file',{exact:true}).setInputFiles({name:'broken.wav',mimeType:'audio/wav',buffer:sample});await sound.getByText(/23.804/).waitFor();assert.equal(await page.evaluate(()=>estimateRequest().options.override_sound_id),undefined);failUpload=false;await sound.getByRole('button',{name:'Retry upload',exact:true}).click();await sound.getByText('Override sound ready: broken.wav',{exact:true}).waitFor();await checkActions(sound,'playground-retry');
 await sound.getByRole('button',{name:'Remove',exact:true}).click();assert.equal(await sound.getByRole('button',{name:'Retry upload',exact:true}).isVisible(),false);assert.equal(await sound.locator('.reference-status').innerText(),'');pass('Selected sound exposes only Remove; replacement requires clearing and failed empty uploads retry safely');
 const transfer=await page.evaluateHandle(data=>{const d=new DataTransfer();d.items.add(new File([new Uint8Array(data)],'drop.wav',{type:'audio/wav'}));return d;},[...sample]);await sound.locator('.reference-dropzone').dispatchEvent('drop',{dataTransfer:transfer});await transfer.dispose();await sound.getByText('Override sound ready: drop.wav',{exact:true}).waitFor();pass('Drag and drop and removal use shared media control');
 await sound.getByRole('button',{name:'Remove',exact:true}).click();const late=gate();uploadGate=late;await sound.getByLabel('Override Sound file',{exact:true}).setInputFiles({name:'late.wav',mimeType:'audio/wav',buffer:sample});await late.started;assert.equal(await sound.locator('[data-upload-picker]').isDisabled(),true);await sound.getByRole('button',{name:'Cancel upload',exact:true}).click();late.release();await sound.getByText(/Upload cancelled/).waitFor();assert.equal(await page.evaluate(()=>estimateRequest().options.override_sound_id),undefined);await sound.getByLabel('Override Sound file',{exact:true}).setInputFiles({name:'selected.wav',mimeType:'audio/wav',buffer:sample});await sound.getByText('Override sound ready: selected.wav',{exact:true}).waitFor();pass('Canceled empty-slot selection does not bind a stale file');
 for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>{document.documentElement.dataset.theme=t},theme);await sound.scrollIntoViewIfNeeded();assert.ok(await sound.evaluate(e=>e.getBoundingClientRect().right<=innerWidth+1));await checkActions(sound,'playground-'+theme);await sound.screenshot({path:path.join(output,`playground-${theme}-${width}.png`)});}pass('Both themes and 1440/390/320px layouts fit');
 await page.setViewportSize({width:1440,height:1000});await page.locator('#templatesNav').click();await page.getByRole('button',{name:/^Edit /}).first().click();const editor=page.locator('.template-editor');await editor.waitFor();
 const ts=editor.locator('[data-override-sound]');await ts.getByLabel('Override Sound file',{exact:true}).setInputFiles({name:'replacement.wav',mimeType:'audio/wav',buffer:sample});await ts.getByText('Override sound ready: replacement.wav',{exact:true}).waitFor();
 await page.getByRole('button',{name:'Save',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#templatesStatus').textContent.includes('Saved.'));assert.equal(record.draft.override_sound_id,'asset-1');
 await ts.getByRole('button',{name:'Remove',exact:true}).waitFor();pass('Template save/reload retains override asset independently of slots and provider options');await ts.locator('audio').waitFor();await ts.locator('audio').evaluate(a=>a.play());await page.waitForFunction(()=>document.querySelector('.template-editor [data-override-sound] audio').currentTime>0);await ts.locator('audio').evaluate(a=>a.pause());pass('Saved template audio preview is loaded and playable');for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await checkActions(ts,'template-'+theme);await ts.screenshot({path:path.join(output,`template-selected-${theme}-${width}.png`)});}await page.setViewportSize({width:1440,height:1000});
 await ts.getByRole('button',{name:'Remove',exact:true}).focus();await page.keyboard.press('Enter');await page.getByRole('button',{name:'Save',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#templatesStatus').textContent.includes('Saved.'));assert.equal(record.draft.override_sound_id,'');assert.equal(choosers,0);pass('Keyboard removal is persisted without opening a file chooser');
 const navigationUpload=gate();uploadGate=navigationUpload;await ts.getByLabel('Override Sound file',{exact:true}).setInputFiles({name:'navigation.wav',mimeType:'audio/wav',buffer:sample});await navigationUpload.started;await page.locator('#generationNav').click();navigationUpload.release();await page.locator('#templatesNav').click();await page.waitForFunction(()=>document.querySelector('.template-editor')||document.querySelector('#templatesGallery button'));if(!await editor.isVisible())await page.getByRole('button',{name:/^Edit /}).first().click();await editor.getByLabel('Title',{exact:true}).fill('After navigation');await page.getByRole('button',{name:'Save',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#templatesStatus').textContent.includes('Saved.'));assert.equal(record.draft.title,'After navigation');assert.equal(record.draft.override_sound_id,'');pass('Interrupted template upload cannot corrupt new editor state or bind a stale file');
 for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>{document.documentElement.dataset.theme=t},theme);await ts.scrollIntoViewIfNeeded();assert.ok(await ts.evaluate(e=>e.getBoundingClientRect().right<=innerWidth+1));await page.screenshot({path:path.join(output,`template-${theme}-${width}.png`)});}pass('Template audio control fits both themes and three layouts');

 assert.deepEqual(errors,[]);assert.deepEqual(external,[]);fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,results,metrics,errors,external},null,2));
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
