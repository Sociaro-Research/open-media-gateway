// Shared Prompt capabilities in the actual Playground and Template fields.
// Synthetic loopback responses only; no generation or external requests.
const {chromium,webkit}=require('playwright');
const {useFixtureKey}=require('./fixture-key.cjs');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=path.resolve(__dirname,'../../web/public/ui'),out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/prompt-labels/'+engine);
fs.mkdirSync(out,{recursive:true});
const baseModel={display_name:'Range model',id:'range',max_prompt_characters:10000,media_type:'video',supports_image_input:true,reference_modes:[{id:'references',label:'References',max_images:2}],resolutions:['720p'],ratios:['16:9'],min_duration:4,max_duration:15,default_duration:10,duration_step:1,pricing:{known:true,reference_price_usd:0}};
const models=[baseModel,{...baseModel,id:'small',display_name:'Small prompt',max_prompt_characters:2000},{...baseModel,id:'unknown',display_name:'Unknown prompt',max_prompt_characters:undefined}];
const catalog=[{id:'mock',name:'Mock',live:false,default_model:'range',models}];
let record={id:'template',version:1,revision:1,draft:{title:'Prompt fixture',description:'',provider:'mock',model:'range',media_type:'video',syntax:'numbered',prompt:'Animate the scene',options:{reference_mode:'references',duration:10,resolution:'720p',ratio:'16:9'},slots:[]}};
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
  const label=input=>input.locator('..').locator('.floating-label');
  const contexts=[{name:'playground',input:page.locator('#prompt'),model:page.locator('#model')},{name:'template',input:page.locator('.template-editor textarea[aria-label="Prompt"]'),model:page.locator('.template-editor select[aria-label="Default model"]')}];
  const direct=await page.evaluate(()=>({empty:FormUI.promptState('',{max_prompt_characters:10000}),unicode:FormUI.promptState('😀e\u0301',{max_prompt_characters:2000}),spaces:FormUI.promptState(' '.repeat(20000)+'a'+' '.repeat(20000),{max_prompt_characters:2000})}));assert.equal(direct.empty.label,'Prompt (10,000 characters max)');assert.equal(direct.unicode.count,3);assert.equal(direct.spaces.error,'');
  for(const context of contexts){
   if(context.name==='template'){await page.locator('#templatesNav').click();await page.getByRole('button',{name:'Edit Prompt fixture',exact:true}).click();}
   const input=context.input,model=context.model;await input.fill('');assert.equal(await label(input).innerText(),'Prompt (10,000 characters max)');await input.fill('a'.repeat(5550));assert.equal(await label(input).innerText(),'Prompt (5,550 / 10,000 characters)');
   await input.fill('');await input.press('x');assert.equal(await label(input).innerText(),'Prompt (1 / 10,000 characters)');await input.press('ControlOrMeta+A');await page.keyboard.insertText('😀'.repeat(5001));assert.equal(await label(input).innerText(),'Prompt (5,001 / 10,000 characters)');assert.equal(await input.inputValue(),'😀'.repeat(5001));assert.equal(await input.evaluate(n=>n.validity.customError),false);
   await model.selectOption(JSON.stringify(['mock','small']),{force:true});assert.equal(await label(input).innerText(),'Prompt (5,001 / 2,000 characters)');assert.match(await input.evaluate(n=>n.validationMessage),/2,000/);await input.fill('e\u0301'.repeat(1000));assert.equal(await label(input).innerText(),'Prompt (2,000 / 2,000 characters)');assert.equal(await input.evaluate(n=>n.validity.customError),false);await input.press('End');await page.keyboard.insertText('x');assert.match(await input.evaluate(n=>n.validationMessage),/2,000/);await input.fill('');assert.equal(await label(input).innerText(),'Prompt (2,000 characters max)');
   await model.selectOption(JSON.stringify(['mock','unknown']),{force:true});assert.equal(await label(input).innerText(),'Prompt');await input.fill('😀');assert.equal(await label(input).innerText(),'Prompt (1 characters)');
   await model.selectOption(JSON.stringify(['mock','range']),{force:true});await input.fill('a'.repeat(5550));for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await input.scrollIntoViewIfNeeded();const geometry=await label(input).evaluate(n=>({clipped:n.scrollWidth>n.clientWidth,overflow:document.documentElement.scrollWidth>innerWidth}));assert.equal(geometry.clipped,false);assert.equal(geometry.overflow,false);await input.locator('..').screenshot({path:path.join(out,context.name+'-'+theme+'-'+width+'.png')});metrics.push({context:context.name,theme,width,...geometry});}await page.setViewportSize({width:1440,height:1100});
   assert.equal(await input.getAttribute('maxlength'),null);assert.equal(await page.locator('#promptCount').count(),0);
  }
  assert.equal(requests.filter(r=>r.path.endsWith('/generations')&&r.method==='POST').length,0);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({engine,checks:['actual prompt labels and effective metadata in both contexts','typing/paste/clear/model switch','Unicode code points and trimmed validation','unknown bound truthful fallback','both themes and 1440/390/320px','no generations'],metrics,errors,external},null,2));console.log('PASS shared Prompt labels, Unicode counts, effective model limits, typing/paste/clear and responsive themes');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
