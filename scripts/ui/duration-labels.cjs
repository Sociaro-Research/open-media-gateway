// Selected model capabilities in the actual Playground and Template fields.
// Synthetic loopback responses only; no generation or external requests.
const {chromium,webkit}=require('playwright');
const {useFixtureKey}=require('./fixture-key.cjs');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=path.resolve(__dirname,'../../web/public/ui'),out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/duration-labels/'+engine);
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
 async function capture(scope,name){for(const theme of ['dark','light'])for(const width of [1440,951,950,760,601,600,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await scope.scrollIntoViewIfNeeded();const geometry=await scope.evaluate(n=>({width:n.getBoundingClientRect().width,overflow:document.documentElement.scrollWidth>innerWidth,labelClipped:[...n.querySelectorAll('.floating-field:has(input[type=number]) .floating-label')].some(l=>l.scrollWidth>l.clientWidth)}));assert.equal(geometry.overflow,false);assert.equal(geometry.labelClipped,false);await scope.screenshot({path:path.join(out,name+'-'+theme+'-'+width+'.png')});metrics.push({name,theme,width,...geometry});}await page.setViewportSize({width:1440,height:1100});}
 try{
  await page.goto(base);await page.waitForFunction(()=>authState==='authenticated');await useFixtureKey(page,'fixture-key');await page.waitForFunction(()=>selectedModel()?.id==='range');
  const cases=await page.evaluate(()=>[{}, {min_duration:4}, {max_duration:30}, {min_duration:5,max_duration:10,duration_step:5}, {min_duration:5,max_duration:5}, {min_duration:20,max_duration:10}, {duration_from_input:true}, {max_duration:0}].map(m=>ModelInfo.durationLabel(m)));
  assert.deepEqual(cases,['Duration (s)','Duration (from 4 s)','Duration (up to 30 s)','Duration (5–10 s; step 5 s)','Duration (5 s)','Duration (s)','Duration (from input)','Duration (unavailable)']);
  assert.equal(await page.locator('#durationHint').count(),0);
  const duration=page.locator('#duration');
  for(const [id,label] of [['range','Duration (4–15 s)'],['step','Duration (5–10 s; step 5 s)'],['upper','Duration (up to 30 s)'],['fixed','Duration (5 s)']]){
   await page.locator('#model').selectOption(JSON.stringify(['mock',id]),{force:true});await page.getByRole('spinbutton',{name:label,exact:true}).waitFor();assert.equal(await duration.locator('..').locator('.floating-label').innerText(),label);
   if(id==='range'){await duration.fill('16');assert.equal(await duration.evaluate(n=>n.validity.rangeOverflow),true);await duration.fill('10');}
   if(id==='step'){await duration.fill('6');assert.equal(await duration.evaluate(n=>n.validity.stepMismatch),true);await duration.fill('10');}
   if(id==='range'||id==='step')await capture(page.locator('#generationSettings'),'playground-'+id);
  }
  await page.locator('#model').selectOption(JSON.stringify(['mock','input']),{force:true});assert.equal(await page.locator('#durationField').isVisible(),false);assert.equal(await duration.isDisabled(),true);assert.equal(await page.locator('#inputDurationHint').isVisible(),true);
  await page.locator('#model').selectOption(JSON.stringify(['mock','none']),{force:true});assert.equal(await page.locator('#durationField').isVisible(),false);
  await page.locator('#templatesNav').click();await page.getByRole('button',{name:'Edit Duration fixture',exact:true}).click();const editor=page.locator('.template-editor'),model=editor.locator('select[aria-label="Default model"]');
  for(const [id,label] of [['range','Duration (4–15 s)'],['step','Duration (5–10 s; step 5 s)'],['upper','Duration (up to 30 s)'],['fixed','Duration (5 s)']]){
   await model.selectOption(JSON.stringify(['mock',id]),{force:true});const input=editor.getByRole('spinbutton',{name:label,exact:true});await input.waitFor();assert.equal(await input.locator('..').locator('.floating-label').innerText(),label);assert.doesNotMatch(await editor.locator('.generation-settings:not(.override-sound-panel) > .field-hint').innerText(),/seconds|step|4–15|5–10/);
   if(id==='range'){await input.fill('16');assert.equal(await input.evaluate(n=>n.validity.rangeOverflow),true);await input.fill('10');}
   if(id==='step'){await input.fill('6');assert.equal(await input.evaluate(n=>n.validity.stepMismatch),true);await input.fill('10');}
   if(id==='range'||id==='step')await capture(editor.locator('.generation-settings:not(.override-sound-panel)'),'template-'+id);
  }
  await model.selectOption(JSON.stringify(['mock','input']),{force:true});assert.equal(await editor.getByRole('spinbutton',{name:/^Duration/}).count(),0);await editor.getByText('Duration follows the input media.',{exact:false}).waitFor();
  await model.selectOption(JSON.stringify(['mock','none']),{force:true});assert.equal(await editor.getByRole('spinbutton',{name:/^Duration/}).count(),0);
  assert.equal(requests.filter(r=>r.path.endsWith('/generations')&&r.method==='POST').length,0);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({engine,checks:['actual model changes in both contexts','continuous/stepped/one-sided/fixed/unknown/input-derived/unsupported labels','native range and step validation','no detached range caption','both themes and 1440/390/320px','no generations'],metrics,errors,external},null,2));console.log('PASS shared Duration labels, model changes, validation and responsive themes');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
