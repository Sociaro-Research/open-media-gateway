// Selected model capabilities in the actual Playground and Template fields.
// Synthetic loopback responses only; no generation or external requests.
const {chromium,webkit}=require('playwright');
const {useFixtureKey}=require('./fixture-key.cjs');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=path.resolve(__dirname,'../../web/public/ui'),out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/disclosures/'+engine);
fs.mkdirSync(out,{recursive:true});
const baseModel={display_name:'Range model',id:'range',media_type:'video',supports_image_input:true,reference_modes:[{id:'references',label:'References',max_images:2}],resolutions:['720p'],ratios:['16:9'],min_duration:4,max_duration:15,default_duration:10,duration_step:1,pricing:{known:true,reference_price_usd:0}};
const models=[baseModel,{...baseModel,id:'step',display_name:'Stepped model',min_duration:5,max_duration:10,default_duration:5,duration_step:5},{...baseModel,id:'upper',display_name:'Upper bound',min_duration:0,max_duration:30},{...baseModel,id:'input',display_name:'Input duration',duration_from_input:true},{...baseModel,id:'fixed',display_name:'Fixed duration',min_duration:5,max_duration:5,default_duration:5},{...baseModel,id:'none',display_name:'No duration',media_type:'image',min_duration:0,max_duration:0}];
const catalog=[{id:'mock',name:'Mock',live:false,default_model:'range',models}];
let record={id:'template',version:1,revision:1,draft:{title:'Duration fixture',description:'',provider:'mock',model:'range',media_type:'video',syntax:'numbered',prompt:'Animate the scene',options:{reference_mode:'references',duration:10,resolution:'720p',ratio:'16:9'},slots:[]}};
const requests=[],external=[],errors=[],metrics=[];
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture');let chunks=[];for await(const b of req)chunks.push(b);let body;try{body=JSON.parse(Buffer.concat(chunks))}catch{}
 const json=(data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
 if(url.pathname.startsWith('/v1/')){
  requests.push({method:req.method,path:url.pathname});
  if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true}});
  if(url.pathname==='/v1/me')return json({id:'owner',name:'Fixture',email:'fixture@example.test',is_system_admin:true});
  if(url.pathname==='/v1/key')return json({key:{name:'Fixture',resource_mode:'personal'}});
  if(url.pathname==='/v1/providers')return json({data:catalog});
  if(url.pathname==='/v1/playground-key/restore')return json({error:{message:'No key'}},404);
  if(url.pathname==='/v1/templates')return json({data:[]});
  if(url.pathname==='/v1/system/templates')return json({data:[record],catalog});
  if(url.pathname.endsWith('/validate-media'))return json({valid:true});
  if(url.pathname.endsWith('/estimate'))return json({cost:{expected_usd:0},can_generate:true});
  return json({data:[]});
 }
 const file=path.join(root,url.pathname==='/'?'index.html':path.basename(url.pathname));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})}),page=await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'});
 page.on('pageerror',e=>{if(!e.message.startsWith('ResizeObserver loop'))errors.push(e.message)});await page.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():(external.push('blocked external request'),r.abort()));
 try{
  await page.goto(base);await page.waitForFunction(()=>authState==='authenticated');await useFixtureKey(page,'fixture-key');await page.waitForFunction(()=>selectedModel()?.id==='range');
  const urlDetails=page.locator('#imageURLsField details'),advanced=page.locator('#generationSettings .advanced-settings'),sound=page.locator('#generationFields > [data-override-sound]');
  await urlDetails.waitFor();assert.equal(await sound.count(),1);assert.equal(await page.locator('#generationSettings [data-override-sound]').count(),0);await sound.getByRole('heading',{name:'Override Sound',exact:true}).waitFor();
  for(const details of [urlDetails,advanced]){const summary=details.locator('summary');await summary.focus();await summary.press('Enter');await details.evaluate(n=>{if(!n.open)throw Error('Native disclosure did not open');});assert.equal(await summary.locator('.select-chevron').count(),1);}
  await page.locator('#imageURLs').fill('https://fixture.example.test/image.png');await page.locator('#seed').fill('42');
  for(let i=0;i<3;i++)for(const details of [urlDetails,advanced]){const summary=details.locator('summary');await summary.focus();await summary.press('Space');assert.equal(await details.evaluate(n=>n.open),false);await summary.press('Enter');assert.equal(await details.evaluate(n=>n.open),true);}
  assert.equal(await page.locator('#imageURLs').inputValue(),'https://fixture.example.test/image.png');assert.equal(await page.locator('#seed').inputValue(),'42');
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));for(const details of [urlDetails,advanced]){const layout=await details.evaluate(n=>{const summary=n.querySelector('summary'),label=summary.querySelector('[data-disclosure-label]')||summary.firstElementChild,chevron=summary.querySelector('.select-chevron'),first=[...n.children].find(c=>c!==summary&&c.getClientRects().length),s=getComputedStyle(summary),ref=getComputedStyle(document.querySelector('#referencesLabel')),a=summary.getBoundingClientRect(),b=first.getBoundingClientRect(),l=label.getBoundingClientRect(),c=chevron.getBoundingClientRect();return {font:[s.fontSize,s.fontWeight,s.lineHeight],referenceFont:[ref.fontSize,ref.fontWeight,ref.lineHeight],gap:b.top-a.bottom,chevronAfter:c.left>=l.right,marker:s.listStyleType,separator:getComputedStyle(n).borderTopWidth,overflow:document.documentElement.scrollWidth>innerWidth};});assert.deepEqual(layout.font,layout.referenceFont);assert.ok(layout.gap>=16,JSON.stringify(layout));assert.equal(layout.chevronAfter,true);assert.equal(layout.marker,'none');if(layout.overflow){await page.screenshot({path:path.join(out,'overflow-'+theme+'-'+width+'.png'),fullPage:true,mask:[page.locator('#apiKey')]});console.log(JSON.stringify({theme,width,scrollWidth:await page.evaluate(()=>document.documentElement.scrollWidth),layout,boxes:await page.evaluate(()=>[...document.querySelectorAll('body *')].filter(n=>n.getBoundingClientRect().right>innerWidth+1).map(n=>({tag:n.tagName,id:n.id,classes:n.className,right:n.getBoundingClientRect().right,width:n.getBoundingClientRect().width})).slice(0,20))}));}assert.equal(layout.overflow,false);if(details===advanced)assert.equal(layout.separator,'0px');metrics.push({theme,width,...layout});}await page.locator('#taskForm').screenshot({path:path.join(out,'disclosures-'+theme+'-'+width+'.png'),mask:[page.locator('#apiKey')]});}
  await page.setViewportSize({width:1440,height:1100});await page.locator('#templatesNav').click();await page.getByRole('button',{name:'Edit Duration fixture',exact:true}).click();assert.equal(await page.locator('.template-editor > [data-override-sound]').count(),1);assert.equal(await page.locator('.template-editor .generation-settings:not(.override-sound-panel) [data-override-sound]').count(),0);for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.locator('.template-editor').screenshot({path:path.join(out,'template-panels-'+theme+'-'+width+'.png')});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);}
  assert.equal(requests.filter(r=>r.path.endsWith('/generations')&&r.method==='POST').length,0);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({engine,checks:['native disclosure keyboard and repeated state','values retained','matching heading typography/trailing chevron/16px gap/no separator','Override Sound sibling in both contexts','both themes and 1440/390/320px','no generations'],metrics,errors,external},null,2));console.log('PASS native disclosure keyboard/repeat/value retention, shared typography/spacing and separate Override Sound panels');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
