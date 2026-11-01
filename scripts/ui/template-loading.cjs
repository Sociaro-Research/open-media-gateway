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
const definition=id=>({title:id==='first'?'First template':'Second template',description:'Lightweight template description',provider:'mock',model:'motion',media_type:'video',syntax:'ui-reference-v1',prompt:'Animate these references',options:{reference_mode:'references',duration:5,resolution:'720p',ratio:'16:9'},cover_id:'cover',slots:[{id:'video:1',type:'video',index:1,role:'reference',label:'Motion',asset_id:'fixed-video'},{id:'image:1',type:'image',index:1,role:'reference',label:'Portrait',asset_id:'fixed-image'}]});
const records=['first','second'].map(id=>({id,version:1,revision:1,content:definition(id),draft:definition(id),published:definition(id),is_published:true}));
const gate=()=>{let release;const promise=new Promise(r=>release=r);return {promise,release};};
let adminGate=null,detailGate=null,mediaMode='hung',adminStatus=200,detailsVersion=1,assetError='',workflowError='',cancelled=0,unexpected=0;
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
 if(type==='detail'){
  const id=u.pathname.split('/').at(-1),pending=detailGate;const d=definition(id),revision=detailsVersion;
  if(pending)await pending.promise;
  return json({id,revision,content:d,template:d,catalog,default_available:true,asset_error:assetError,workflow_error:workflowError,fixed_references:[{slot_id:'video:1',asset_id:'fixed-video',name:'Motion',content_type:'video/mp4',url:'/fixture-media/fixed.mp4'},{slot_id:'image:1',asset_id:'fixed-image',name:'Portrait',content_type:'image/png',url:'/fixture-media/fixed.png'}]});
 }
 if(u.pathname.startsWith('/v1/')){
  if(u.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true}});
  if(u.pathname==='/v1/me')return json({id:'fixture-owner',name:'Fixture',email:'fixture@example.test',is_system_admin:true});
  if(u.pathname==='/v1/key')return json({key:{name:'Fixture key',resource_mode:'personal'}});
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
 const browser=await({chromium,webkit}[engine]).launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:1000}});page.setDefaultTimeout(6000);
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
  assert.equal(await page.locator('form.template-use').count(),0);assert.equal(await page.getByRole('button',{name:'Review generation',exact:true}).count(),0);
  assert.equal(await title('second').isVisible(),true);
  const selectedTextMs=null;pass('Selected generation controls remain absent until authoritative server validation completes');
  await page.locator('#generationNav').click();detailGate.release();detailGate=null;
  await page.locator('#templatesNav').click();await page.locator('form.template-use h2').waitFor();assert.equal(await page.locator('form.template-use h2').innerText(),'Second template');
  pass('Navigation aborts obsolete detail work and restores only the current selected template');
  detailGate=gate();await title('first').click();assert.equal(await page.locator('form.template-use').count(),0);
  await page.locator('#generationNav').click();await key('synthetic-other-context');detailGate.release();detailGate=null;
  await page.locator('#templatesNav').click();await title('first').waitFor();assert.equal(await page.locator('form.template-use').count(),0);
  pass('Key changes clear the previous selection and prevent old detail hydration');
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
  fs.writeFileSync(path.join(output,'result-'+engine+'.json'),JSON.stringify({at:new Date().toISOString(),engine,gallery_held_admin_ms:Math.round(galleryMs),selected_held_detail_ms:selectedTextMs,selected_text_waits_for_validated_detail:true,checks:results,page_errors:errors,external_requests:external,cancelled_http_readers:cancelled,input_values_read:false,full_dom_captured:false},null,2)+'\n');
 }finally{adminGate?.release();detailGate?.release();for(const r of heldMedia)r.destroy();await browser.close();server.closeAllConnections();await new Promise(r=>server.close(r));}
})().catch(error=>{fs.writeFileSync(path.join(output,'failure-'+engine+'.json'),JSON.stringify({engine,failure_name:error.name,completed_checks:results,requests:reads.map(({kind,finished})=>({kind,finished})),unexpected_server_errors:unexpected},null,2)+'\n');console.error('Loading regression failed:',error.name);server.closeAllConnections();server.close();process.exitCode=1;});
