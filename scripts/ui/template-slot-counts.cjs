// Real editor with local synthetic API responses; server-side count semantics are
// exercised separately by TestTemplateSlotCountWarningKeepsDraftAndChecksPublication.
const {generationFixtureResponse}=require('./generation-page-fixture.cjs');
const {chromium,webkit}=require('playwright');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=path.resolve('web/public/ui'),output=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/template-slot-counts/'+engine);fs.mkdirSync(output,{recursive:true});
const prompt=n=>Array.from({length:n},(_,i)=>'@Image'+(i+1)).join(' ');
const slots=n=>Array.from({length:n},(_,i)=>({id:'image:'+(i+1),type:'image',index:i+1,role:'reference',label:'Image '+(i+1)}));
const model={id:'mock-image-v1',display_name:'Mock Image',media_type:'image',supports_image_input:true,resolutions:['1024*1024'],reference_modes:[{id:'references',label:'References',max_images:14}],pricing:{known:true,reference_price_usd:0}};
const catalog=[{id:'mock',name:'Mock',default_model:model.id,models:[model,{...model,id:'wide',display_name:'Wide fixture',reference_modes:[{id:'references',label:'References',max_images:16}]}]}];
let record={id:'template',version:1,draft:{title:'Slot counts',provider:'mock',model:model.id,media_type:'image',syntax:'ui-reference-v1',prompt:prompt(15),options:{reference_mode:'references'},slots:[],cover_id:'cover'}},requests=[];
const warning='15 image slots, maximum 14 for the selected model. Reduce slots before publishing. You can still save this draft.';
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1sAAAAASUVORK5CYII=','base64');
const server=http.createServer(async(req,res)=>{const u=new URL(req.url,'http://fixture');let raw=[];for await(const chunk of req)raw.push(chunk);let body;try{body=JSON.parse(Buffer.concat(raw))}catch{}
 const json=(data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(generationFixtureResponse(req,data,status)));};
 if(u.pathname==='/cover.png'||u.pathname.endsWith('/assets/cover')){res.setHeader('Content-Type','image/png');return res.end(png);}
 if(u.pathname.startsWith('/v1/')){
  requests.push({path:u.pathname,method:req.method,body});
  if(u.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true}});
  if(u.pathname==='/v1/me')return json({id:'owner',name:'Owner',email:'owner@fixture.test',is_system_admin:true});
  if(u.pathname==='/v1/key')return json({key:{name:'Fixture',resource_mode:'personal'}});
  if(u.pathname==='/v1/providers')return json({data:catalog});
  if(u.pathname==='/v1/playground-key/restore')return json({error:{message:'No saved fixture key'}},404);
  if(u.pathname==='/v1/templates')return json({data:[{id:'template',title:record.draft.title,cover_url:'/cover.png',cover_type:'image/png'}]});
  if(u.pathname==='/v1/system/templates')return json({data:[record],catalog});
  if(u.pathname.endsWith('/metadata'))return json({name:'Cover.png',content_type:'image/png'});
  if(u.pathname.endsWith('/validate-media'))return json({valid:true});
  if(u.pathname.endsWith('/infer')){const count=Math.max(0,...[...body.prompt.matchAll(/@Image(\d+)/g)].map(m=>+m[1]));return json({syntax:'ui-reference-v1',slots:slots(count),...(count>14&&body.model===model.id?{warning}:{})});}
  if(u.pathname==='/v1/system/templates/template'&&req.method==='PUT'){
   if(body.action==='publish'&&record.draft.slots.length>14&&record.draft.model===model.id)return json({error:{message:'15 image slots, maximum 14 for the selected model.'}},400);
   record={...record,version:record.version+1,draft:body.content||record.draft};if(body.action==='publish')record.published=structuredClone(record.draft);return json(record);
  }
  return json({data:[]});
 }
 const name=['/','/templates'].includes(u.pathname)?'index.html':path.basename(u.pathname);
 const file=name==='templates.js'&&process.env.UI_TEMPLATE_SCRIPT?process.env.UI_TEMPLATE_SCRIPT:path.join(root,name);
 if(!fs.existsSync(file)){res.writeHead(404);return res.end();}res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await({chromium,webkit}[engine]).launch({headless:true});const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'});page.setDefaultTimeout(7000);const errors=[],external=[];
 page.on('pageerror',e=>{if(!e.message.includes('ResizeObserver'))errors.push(e.message)});await page.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():(external.push(r.request().url()),r.abort()));
 try{
  await page.goto(base);await page.waitForFunction(()=>authState==='authenticated');await page.locator('#apiKey').fill('fixture-admin');await page.locator('#apiKey').press('Tab');await page.waitForFunction(()=>GenerationKey.value()==='fixture-admin');await page.locator('#templatesNav').click();await page.getByRole('button',{name:'Create template',exact:true}).waitFor();await page.getByRole('button',{name:/^Edit /}).first().click();
  const editor=page.locator('.template-editor'),infer=editor.getByRole('button',{name:'Infer and review slots',exact:true}),status=page.locator('#templatesStatus');
  await infer.focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelectorAll('[data-slot-id]').length===15);await page.waitForFunction(()=>!document.querySelector('.template-editor button.primary').disabled);
  if(process.env.UI_BASELINE){assert.doesNotMatch(await status.innerText(),/15 image slots/);await status.scrollIntoViewIfNeeded();await page.screenshot({path:path.join(output,'before-warning.png')});console.log('BASELINE all15 slots, generic review text, no count warning');return;}
  assert.equal(await status.innerText(),warning);assert.equal(await status.getAttribute('role'),'status');assert.equal(await editor.getByRole('button',{name:'Save',exact:true}).isDisabled(),false);
  const geometry=[];
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await status.scrollIntoViewIfNeeded();await page.screenshot({path:path.join(output,`warning-${theme}-${width}.png`)});geometry.push({theme,width,status:await status.boundingBox(),save:await editor.getByRole('button',{name:'Save',exact:true}).boundingBox()});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),true);}
  await editor.getByRole('button',{name:'Save',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#templatesStatus').textContent.startsWith('Saved.'));assert.equal(record.draft.slots.length,15);assert.equal(record.published,undefined);assert.equal(requests.filter(x=>x.method==='PUT'&&x.body.content?.slots.length===15).length,1);
  await editor.getByRole('switch',{name:'Published',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#templatesStatus').textContent==='15 image slots, maximum 14 for the selected model.');assert.equal(record.published,undefined);
  await infer.click();await page.waitForFunction(text=>document.querySelector('#templatesStatus').textContent===text,warning);
  await editor.getByLabel('Prompt',{exact:false}).fill(prompt(14));assert.doesNotMatch(await status.innerText(),/15 image slots/);await infer.click();await page.waitForFunction(()=>document.querySelectorAll('[data-slot-id]').length===14);assert.doesNotMatch(await status.innerText(),/maximum 14/);
  await editor.getByLabel('Prompt',{exact:false}).fill(prompt(15));await infer.click();await page.waitForFunction(text=>document.querySelector('#templatesStatus').textContent===text,warning);
  await editor.locator('select[aria-label="Default model"]').selectOption(JSON.stringify(['mock','wide']));assert.doesNotMatch(await status.innerText(),/maximum 14/);await infer.click();await page.waitForFunction(()=>!document.querySelector('.template-editor button.primary').disabled);assert.doesNotMatch(await status.innerText(),/maximum 14/);assert.equal(await page.locator('[data-slot-id]').count(),15);
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);assert.equal(requests.filter(r=>r.method==='POST'&&r.path.endsWith('/generations')).length,0);fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,checks:['15 retained with early count warning','keyboard infer','Save enabled and intact15 submitted','publication server error surfaced','14 warning clears','model switch invalidates old warning','both themes1440/390/320'],geometry,errors,external},null,2));console.log('PASS template slot warning, unchanged draft Save, publication feedback and stale-warning clearing');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
