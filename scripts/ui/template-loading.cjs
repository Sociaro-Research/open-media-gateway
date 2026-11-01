// Real UI and synthetic APIs only. Held reads prove independence without media completion.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const root=path.resolve(process.env.UI_SOURCE_DIR||'web/public/ui');
const output=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/template-loading/'+engine);
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aT0sAAAAASUVORK5CYII=','base64');
// Reuse the documented synthetic H.264 fixture generator; no live media.
const video=fs.readFileSync(path.resolve(process.env.GATEWAY_TEMPLATE_CLIP_DIR||'.local/video-workflow/clips','cfr24.mp4'));
const model={id:'motion',display_name:'Motion',media_type:'video',supports_image_input:true,supports_video_input:true,reference_modes:[{id:'references',label:'References',max_images:3,max_videos:2}],min_duration:4,max_duration:15,default_duration:5,resolutions:['720p'],ratios:['16:9'],pricing:{known:true,reference_price_usd:0}};
const catalog=[{id:'mock',name:'Mock',default_model:model.id,models:[model]}];
const baseDefinition=id=>({title:id==='first'?'First template':'Second template',description:'Lightweight template description',provider:'mock',model:'motion',media_type:'video',syntax:'ui-reference-v1',prompt:'Animate these references',options:{reference_mode:'references',duration:5,resolution:'720p',ratio:'16:9'},cover_id:'cover',slots:[{id:'video:1',type:'video',index:1,role:'reference',label:'Motion',asset_id:'fixed-video'},{id:'image:1',type:'image',index:1,role:'reference',label:'Portrait',asset_id:'fixed-image'}]});
let estimateGate=null,inputGate=null,generationWrites=0,libraryUploads=0;let variableSlot=false;const definition=id=>{const d=baseDefinition(id);if(variableSlot)d.slots.push({id:'image:2',type:'image',index:2,role:'reference',label:'User image'});return d;};
const records=['first','second'].map(id=>({id,version:1,revision:1,content:definition(id),draft:definition(id),published:definition(id),is_published:true}));
const gate=()=>{let release;const promise=new Promise(r=>release=r);return {promise,release};};
let adminGate=null,detailGate=null,mediaMode='hung',adminStatus=200,detailsVersion=1,metadataMode='normal',detailMode='normal',assetError='',workflowError='',cancelled=0,unexpected=0;
const heldMedia=new Set(),reads=[],results=[];
const server=http.createServer((req,res)=>{void(async()=>{
 const u=new URL(req.url,'http://fixture');const type=u.pathname==='/v1/system/templates'?'admin':u.pathname==='/v1/templates'?'gallery':/^\/v1\/templates\/(first|second)$/.test(u.pathname)?'detail':u.pathname.startsWith('/fixture-media/')?'media':'other';
 if(type!=='other')reads.push({kind:type,at:performance.now(),finished:false});
 const entry=type==='other'?null:reads.at(-1);res.once('finish',()=>{if(entry)entry.finished=true;});
 const json=(body,status=200)=>{if(res.destroyed)return;res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(body));};
 if(type==='media'){
  if(mediaMode==='hung'){heldMedia.add(res);await new Promise(r=>res.once('close',r));heldMedia.delete(res);return;}
  if(mediaMode==='fail'){res.writeHead(503);return res.end();}
  const isVideo=u.pathname.endsWith('.mp4'),body=isVideo?video:png,range=/^bytes=(\d+)-(\d*)$/.exec(req.headers.range||'');
  if(range){const start=Number(range[1]),end=range[2]?Math.min(Number(range[2]),body.length-1):body.length-1;if(start>=body.length||end<start){res.writeHead(416,{'Content-Range':'bytes */'+body.length});return res.end();}res.writeHead(206,{'Content-Type':isVideo?'video/mp4':'image/png','Content-Length':end-start+1,'Content-Range':`bytes ${start}-${end}/${body.length}`,'Accept-Ranges':'bytes'});return res.end(body.subarray(start,end+1));}
  res.writeHead(200,{'Content-Type':isVideo?'video/mp4':'image/png','Content-Length':body.length,'Accept-Ranges':'bytes'});return res.end(body);
 }
 if(type==='admin'){const pending=adminGate;if(pending)await pending.promise;return json(adminStatus===200?{data:records,catalog}:{error:{message:adminStatus===403?'Not allowed':'Admin read unavailable'}},adminStatus);}
 if(type==='gallery')return json({data:records.map(r=>({id:r.id,revision:r.revision,title:r.content.title,description:r.content.description,cover_url:'/fixture-media/cover-'+r.id+'.png',cover_type:'image/png'}))});
 if(/^\/v1\/templates\/(first|second)\/metadata$/.test(u.pathname)){const id=u.pathname.split('/')[3];if(metadataMode==='missing')return json({error:{message:'Template metadata unavailable; update the server.'}},404);return json({id,revision:detailsVersion,template:definition(id),...(metadataMode==='legacy'?{}:{validation_status:'pending'})});}
 if(type==='detail'){
  const id=u.pathname.split('/').at(-1),pending=detailGate;const d=definition(id),revision=detailsVersion;
  if(pending)await pending.promise;
  if(detailMode==='removed')return json({error:{message:'Template was unpublished'}},404);
  if(detailMode==='conflict'){detailMode='normal';detailsVersion++;return json({error:{code:'template_conflict',message:'Template changed'}},409);}
  return json({...(detailMode==='legacy'?{}:{validation_status:assetError||workflowError?'failed':'ready'}),id:detailMode==='wrong-id'?'other':id,revision:detailMode==='wrong-revision'?revision+1:revision,content:d,template:d,catalog,default_available:true,asset_error:assetError,workflow_error:workflowError,fixed_references:[{slot_id:'video:1',asset_id:'fixed-video',name:'Motion',content_type:'video/mp4',url:'/fixture-media/fixed.mp4'},{slot_id:'image:1',asset_id:'fixed-image',name:'Portrait',content_type:'image/png',url:'/fixture-media/fixed.png'}]});
 }
 if(u.pathname.startsWith('/v1/')){
  if(u.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true}});
  if(u.pathname==='/v1/me')return json({id:'fixture-owner',name:'Fixture',email:'fixture@example.test',is_system_admin:true});
  if(u.pathname==='/v1/key')return json({key:{name:'Fixture key',resource_mode:'personal'}});
  if(u.pathname==='/v1/references/uploads'){libraryUploads++;return json({mode:'existing',reference:{id:'user-image',name:'user.png',content_type:'image/png'}});}
  if(u.pathname==='/v1/references')return json({data:[{id:'user-image',name:'user.png',content_type:'image/png',url:'/fixture-media/user.png'}]});
  if(u.pathname.endsWith('/validate-media')){const pending=inputGate;if(pending)await pending.promise;return json({});}
  if(u.pathname.endsWith('/estimate')){const pending=estimateGate;if(pending)await pending.promise;return json({cost:{expected_usd:0,customer_expected_usd:0},can_generate:true});}
  if((u.pathname==='/v1/generations'||/^\/v1\/templates\/[^/]+\/generations$/.test(u.pathname))&&req.method==='POST'){generationWrites++;return json({id:'synthetic-unexpected-job'});}
  if(u.pathname==='/v1/providers')return json({data:catalog});
  if(u.pathname==='/v1/playground-key/restore')return json({error:{message:'No saved fixture key'}},404);
  return json({data:[]});
 }
 const file=path.join(root,['/','/templates'].includes(u.pathname)?'index.html':path.basename(u.pathname));
 if(!fs.existsSync(file)){res.writeHead(404);return res.end();}
 res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});res.end(fs.readFileSync(file));
})().catch(error=>{if(['ECONNRESET','ERR_STREAM_PREMATURE_CLOSE'].includes(error.code))cancelled++;else unexpected++;if(!res.destroyed)res.destroy();});});
(async()=>{
 fs.mkdirSync(output,{recursive:true});await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});const page=await browser.newPage({viewport:{width:1440,height:1000}});page.setDefaultTimeout(6000);
 let errors=0,external=0;page.on('pageerror',()=>errors++);
 await page.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():(external++,r.abort()));
 const pass=name=>{results.push(name);console.log('PASS',name);};
 const title=id=>page.locator('[data-template-id="'+id+'"] .template-card-title');
 async function key(value){await page.locator('#apiKey').fill(value);await page.locator('#apiKey').press('Tab');await page.waitForFunction(v=>GenerationKey.value()===v&&providerCatalog.length>0,value);}
 async function load(){await page.goto(base,{waitUntil:'domcontentloaded'});await page.waitForFunction(()=>authState==='authenticated');await key('synthetic-loading-fixture');}
 try{
  await load();adminGate=gate();let t=performance.now();await page.locator('#templatesNav').click();
  await title('first').waitFor({timeout:1000});assert.ok(reads.some(x=>x.kind==='admin'&&!x.finished));assert.ok(reads.some(x=>x.kind==='gallery'&&x.finished));
  assert.equal(await page.getByRole('button',{name:'Create template',exact:true}).count(),0);
  pass('Authorized gallery text renders while the independent administrator read remains unresolved');
  const galleryMs=performance.now()-t;
  await title('first').click();await page.locator('form.template-use h2').waitFor({timeout:1000});
  assert.equal(await page.locator('form.template-use h2').innerText(),'First template');assert.ok(heldMedia.size>0);
  pass('Validated selected settings render with administrator and heavy media responses still unresolved');
  adminGate.release();adminGate=null;await page.getByRole('button',{name:'Create template',exact:true}).waitFor();
  adminStatus=403;await page.locator('#generationNav').click();await page.locator('#templatesNav').click();await page.locator('form.template-use h2').waitFor();
  await page.waitForFunction(()=>![...document.querySelectorAll('button')].some(b=>b.textContent==='Create template'));
  assert.equal(await title('first').isVisible(),true);pass('Revoked authoring permission removes editing actions without hiding authorized consumer content');
  adminStatus=503;await load();await page.locator('#templatesNav').click();await title('first').waitFor();await page.getByText('Admin read unavailable',{exact:true}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Create template',exact:true}).count(),0);pass('An independent authoring read failure cannot erase or block the authorized gallery');
  adminStatus=200;await page.locator('#generationNav').click();await page.locator('#templatesNav').click();await page.getByRole('button',{name:'Create template',exact:true}).waitFor();
  workflowError='Synthetic workflow requires repair';await title('first').click();await page.getByText(workflowError,{exact:true}).waitFor();assert.equal(await page.getByRole('button',{name:'Review generation',exact:true}).isDisabled(),true);
  workflowError='';assetError='Synthetic fixed reference unavailable';await title('second').click();await page.getByText(assetError,{exact:true}).waitFor();assert.equal(await page.getByRole('button',{name:'Review generation',exact:true}).isDisabled(),true);
  assetError='';await title('first').click();await page.locator('form.template-use h2').waitFor();detailsVersion++;
  await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.getByText('Template updated. Review the current settings and choose references again.',{exact:true}).waitFor();
  pass('Renderer workflow/asset errors and changed-template guidance survive independent read completion');
  detailGate=gate();t=performance.now();await title('second').click();
  assert.equal(await page.locator('form.template-use').count(),0);await page.locator('.template-validation-pending h2').waitFor();assert.equal(await page.getByRole('button',{name:'Review generation',exact:true}).isDisabled(),true);assert.equal(await page.getByRole('button',{name:'Advanced editing',exact:true}).isDisabled(),true);assert.equal(await page.locator('#templateSelection input[type=file]').count(),0);
  assert.equal(await title('second').isVisible(),true);
  const selectedTextMs=Math.round(performance.now()-t);for(const theme of ['light','dark'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.locator('#templateSelection').screenshot({path:path.join(output,'pending-'+theme+'-'+width+'.png'),mask:[page.locator('#connectionPanel')]});}await page.setViewportSize({width:1440,height:1000});pass('Metadata text renders while validation is held; all admission and upload actions stay blocked');
  await page.locator('#generationNav').click();detailGate.release();detailGate=null;
  await page.locator('#templatesNav').click();await page.locator('form.template-use h2').waitFor();assert.equal(await page.locator('form.template-use h2').innerText(),'Second template');
  pass('Navigation aborts obsolete detail work and restores only the current selected template');
  detailGate=gate();await title('first').click();assert.equal(await page.locator('form.template-use').count(),0);
  await page.locator('#generationNav').click();await key('synthetic-other-context');detailGate.release();detailGate=null;
  await page.locator('#templatesNav').click();await title('first').waitFor();assert.equal(await page.locator('form.template-use').count(),0);
  pass('Key changes clear the previous selection and prevent old detail hydration');
  for(const mode of ['legacy','wrong-id','wrong-revision']){detailMode=mode;await title('first').click();await page.locator('.template-validation-pending[data-validation-status=failed]').waitFor();assert.equal(await page.getByRole('button',{name:'Review generation',exact:true}).isDisabled(),true);assert.equal(await page.locator('form.template-use').count(),0);}detailMode='normal';
  metadataMode='legacy';await title('second').click();await page.getByText('Template validation is unavailable. Update the server and retry.',{exact:true}).waitFor();assert.equal(await page.locator('form.template-use').count(),0);metadataMode='missing';await title('first').click();await page.locator('.template-selection-panel').waitFor({state:'hidden'});metadataMode='normal';pass('Legacy metadata/detail and mismatched identity or revision fail closed');
  detailMode='conflict';await title('first').click();await page.locator('form.template-use h2').waitFor();assert.ok(reads.filter(r=>r.kind==='detail').length>1);pass('Conflict refreshes metadata before hydrating the new revision');
  detailGate=gate();await title('first').click();await page.locator('.template-validation-pending h2').waitFor();const oldGate=detailGate;detailGate=null;await title('second').click();await page.locator('form.template-use h2').filter({hasText:'Second template'}).waitFor();oldGate.release();await page.waitForTimeout(50);assert.equal(await page.locator('form.template-use h2').innerText(),'Second template');pass('Late first-template validation cannot hydrate a newer selection');
  detailMode='removed';detailGate=gate();await title('first').click();await page.locator('.template-validation-pending h2').waitFor();detailGate.release();detailGate=null;await page.locator('.template-selection-panel').waitFor({state:'hidden'});detailMode='normal';pass('Unpublication after metadata prevents validated form hydration');
  await title('first').click();await page.locator('form.template-use').waitFor();
  for(const lateEstimate of [true,false]){estimateGate=lateEstimate?gate():null;const requested=page.waitForRequest(r=>r.url().endsWith('/estimate'));await page.getByRole('button',{name:'Review generation',exact:true}).click();await requested;if(!lateEstimate)await page.waitForFunction(()=>!document.querySelector('#confirmSubmit').disabled);detailGate=gate();await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.locator('#confirmDialog').waitFor({state:'hidden'});estimateGate?.release();estimateGate=null;await page.evaluate(()=>document.querySelector('#confirmSubmit').click());assert.equal(generationWrites,0);detailGate.release();detailGate=null;await page.waitForFunction(()=>!document.querySelector('#templateSelection button[role=combobox]').disabled);}pass('Pending validation invalidates existing reviews and late estimate callbacks cannot submit');
  variableSlot=true;await title('first').click();await page.locator('form.template-use input[type=file]').setInputFiles({name:'user.png',mimeType:'image/png',buffer:png});await page.getByRole('button',{name:'Remove',exact:true}).waitFor();
  for(const target of [()=>page.locator('#templateSelection button[role=combobox]'),()=>page.getByRole('button',{name:'Remove',exact:true})]){const node=target();await node.focus();detailGate=gate();await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.waitForFunction(()=>document.querySelector('#templateSelection button[role=combobox]').disabled);await page.waitForTimeout(200);detailGate.release();detailGate=null;await page.waitForFunction(()=>!document.querySelector('#templateSelection button[role=combobox]').disabled);await page.waitForFunction(()=>document.activeElement?.matches('#templateSelection button[role=combobox],#templateSelection [data-upload-remove]'));assert.equal(await node.evaluate(n=>n===document.activeElement),true);}
  const removal=page.getByRole('button',{name:'Remove',exact:true});await removal.focus();detailGate=gate();await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.waitForFunction(()=>document.querySelector('#templateSelection button[role=combobox]').disabled);await title('first').focus();detailGate.release();detailGate=null;await page.waitForFunction(()=>!document.querySelector('#templateSelection button[role=combobox]').disabled);assert.equal(await title('first').evaluate(n=>n===document.activeElement),true);pass('Same-revision validation restores Model and Remove focus without stealing later user focus');
  await removal.click();await page.getByRole('button',{name:'Choose from library',exact:true}).click();const library=page.locator('dialog.reference-library[open]'),oldUpload=await library.locator('input[type=file]').elementHandle();inputGate=gate();const choosing=page.waitForRequest(r=>r.url().endsWith('/validate-media'));await library.getByRole('button',{name:'Use user.png in generation',exact:true}).click();await choosing;detailGate=gate();await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await library.waitFor({state:'detached'});const uploadCount=libraryUploads;await oldUpload.evaluate(n=>{const d=new DataTransfer();d.items.add(new File(['synthetic'],'late.png',{type:'image/png'}));n.files=d.files;n.dispatchEvent(new Event('change',{bubbles:true}));});inputGate.release();inputGate=null;detailGate.release();detailGate=null;await page.waitForFunction(()=>!document.querySelector('#templateSelection button[role=combobox]').disabled);assert.equal(await page.getByRole('button',{name:'Remove',exact:true}).count(),0);assert.equal(libraryUploads,uploadCount);pass('Pending validation closes the reference library, aborts late selection and blocks its detached uploader');variableSlot=false;
  mediaMode='normal';for(const r of heldMedia)r.destroy();await title('first').click();await page.locator('form.template-use video').waitFor();
  const player=page.locator('form.template-use video');await page.waitForFunction(()=>document.querySelector('form.template-use video')?.videoWidth>0);
  assert.deepEqual(await player.evaluate(v=>({muted:v.muted,loop:v.loop,autoplay:v.autoplay,inline:v.playsInline,controls:v.controls})),{muted:true,loop:true,autoplay:true,inline:true,controls:false});
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   const geometry=await player.evaluate(v=>{const r=v.getBoundingClientRect();return {width:r.width,height:r.height,natural:v.videoWidth/v.videoHeight};});assert.ok(Math.abs(geometry.width/geometry.height-geometry.natural)<.03);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
   await page.locator('#templatesGallery').screenshot({path:path.join(output,'gallery-'+theme+'-'+width+'.png'),mask:[page.locator('#connectionPanel')]});
   await page.locator('#templateSelection').screenshot({path:path.join(output,'selection-'+theme+'-'+width+'.png'),mask:[page.locator('#connectionPanel')]});
  }
  pass('Fixed video remains muted inline looping with natural ratio; layouts fit both themes at1440/390/320px');
  mediaMode='fail';await page.locator('#generationNav').click();await page.locator('#templatesNav').click();await page.locator('form.template-use h2').waitFor();
  await page.locator('.template-fixed-media').getByText('Preview unavailable.',{exact:true}).first().waitFor();assert.equal(await page.locator('form.template-use h2').innerText(),'First template');
  pass('Media failures retain selected text/settings and scoped textual preview feedback');
  assert.equal(errors,0);assert.equal(external,0);assert.equal(unexpected,0);
  fs.writeFileSync(path.join(output,'result-'+engine+'.json'),JSON.stringify({at:new Date().toISOString(),engine,gallery_held_admin_ms:Math.round(galleryMs),selected_held_detail_ms:selectedTextMs,selected_text_waits_for_validated_detail:false,checks:results,page_errors:errors,external_requests:external,cancelled_http_readers:cancelled,input_values_read:false,full_dom_captured:false},null,2)+'\n');
 }finally{adminGate?.release();detailGate?.release();inputGate?.release();estimateGate?.release();for(const r of heldMedia)r.destroy();await browser.close();server.closeAllConnections();await new Promise(r=>server.close(r));}
})().catch(error=>{fs.writeFileSync(path.join(output,'failure-'+engine+'.json'),JSON.stringify({engine,failure_name:error.name,completed_checks:results,requests:reads.map(({kind,finished})=>({kind,finished})),unexpected_server_errors:unexpected},null,2)+'\n');console.error('Loading regression failed:',error.name);server.closeAllConnections();server.close();process.exitCode=1;});
