// Local browser fixtures only. No remote account, grant, payment or provider calls.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui'),baseRoute='/v1/system/remote-workspace';
const company='11111111-1111-4111-8111-111111111111',job='44444444-4444-4444-8444-444444444444';
let state={version:1,setup_pending:true,shared_resources:false,personal_providers:true,generation_ui:true,allow_inline_references:false},saved=[],connected=false,connects=0,submissions=[],rejectProfile=false,rejectSubmit=true,proxyReads=0;
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture');let data='';for await(const chunk of req)data+=chunk;
 const json=(status,value)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
 if(req.url==='/')return res.end(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"><main id="fixture"></main><script>window.api=async(p,o={})=>{const r=await fetch(p,{...o,headers:{'Content-Type':'application/json',...o.headers}}),v=await r.json();if(!r.ok){const e=new Error(v.error.message);e.status=r.status;throw e;}return v;};window.applySystemAccess=(next)=>{window.fixtureApplied=next;};</script><script src="/model-info.js"></script><script src="/model-picker.js"></script><script src="/control-pickers.js"></script><script src="/form-ui.js"></script><script src="/setup-selector.js"></script><script src="/remote-workspace.js"></script>`);
 if(req.url==='/v1/system/settings')return json(200,state);
 if(req.url==='/v1/system/setup-profile'){
  const v=JSON.parse(data);saved.push(v);if(rejectProfile)return json(503,{error:{message:'Fixture save failed'}});if(v.version!==state.version)return json(409,{error:{message:'Settings changed'}});
  state={...state,shared_resources:v.profile==='operator',personal_providers:v.profile==='personal'||v.allow_personal,version:state.version+1,setup_pending:false};return json(200,state);
 }
 if(req.url===baseRoute){
  if(req.method==='GET')return json(200,{connected,version:connected?1:0,origin:'https://remote.example.test',organization_id:company,organization_name:'Fixture company',context:'fixture-context',operation_scope:'fixture-scope'});
  if(req.method==='DELETE'){connected=false;return json(200,{connected:false,version:2});}
  connects++;assert.equal(JSON.parse(data).consent,true);connected=true;return json(200,{connected:true,version:1,origin:'https://remote.example.test',organization_id:company,organization_name:'Fixture company',context:'fixture-context',operation_scope:'fixture-scope'});
 }
 if(url.pathname.startsWith(baseRoute+'/proxy')){
  if(req.method==='GET')proxyReads++;
  const remote=url.pathname.slice((baseRoute+'/proxy').length);
  if(remote==='/v1/key')return json(200,{organization:{id:company,available_usd:25}});
  if(remote==='/v1/models')return json(200,{data:[{id:'mock/image',provider:'mock',capabilities:{id:'image',display_name:'Fixture image',media_type:'image',prompt_optional:false,resolutions:['1024x1024'],pricing:{known:true,reference_price_usd:0.1}}}]});
  if(remote==='/v1/generations/estimate')return json(200,{can_generate:true,cost:{expected_usd:0.1}});
  if(remote==='/v1/generations'&&req.method==='POST'){submissions.push({key:req.headers['idempotency-key'],body:JSON.parse(data)});if(rejectSubmit){rejectSubmit=false;return json(503,{error:{message:'Fixture lost response — retry the same operation'}});}return json(202,{id:job,status:'queued'});}
  if(remote==='/v1/generations')return json(200,{data:submissions.length>1?[{id:job,model:'mock/image',prompt:'Fixture prompt',status:'completed'}]:[]});
  if(remote.endsWith('/content'))return res.end('fixture image');
  return json(404,{error:{message:'Unexpected remote route'}});
 }
 const file=path.join(root,path.basename(url.pathname));if(fs.existsSync(file)){res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/plain');return res.end(fs.readFileSync(file));}res.writeHead(404).end();
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await(process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());page.setDefaultTimeout(10000);
 await page.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
 const init=()=>page.evaluate(()=>SetupSelector.initialize({id:'owner',session_id:'fixture',is_system_admin:true}));
 try{
  await page.goto(base);await init();const setup=page.locator('.setup-dialog');await setup.waitFor({state:'visible'});assert.equal(await setup.locator('input[type=radio]').count(),3);
  fs.mkdirSync(process.env.UI_SCREENSHOT_DIR||'/tmp/gw170-ui',{recursive:true});for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.ok(await setup.evaluate(n=>n.scrollWidth<=n.clientWidth+1),'setup dialog overflow '+width);await page.screenshot({path:path.join(process.env.UI_SCREENSHOT_DIR||'/tmp/gw170-ui',`setup-${process.env.UI_BROWSER_ENGINE||'chromium'}-${theme}-${width}.png`)});}await page.setViewportSize({width:1440,height:1100});await page.keyboard.press('Escape');assert.equal(saved.length,0);await page.evaluate(()=>SetupSelector.open());await setup.waitFor({state:'visible'});
  await setup.locator('input[value=sociaro]').check();assert.equal(proxyReads,0);assert.equal(connects,0);await setup.getByRole('button',{name:'Skip for now'}).click();assert.equal(saved.length,0);await page.reload();await init();assert.equal(await setup.isVisible(),false);
  await page.evaluate(()=>SetupSelector.open());await setup.locator('input[value=operator]').check();rejectProfile=true;await setup.getByRole('button',{name:'Apply',exact:true}).click();await setup.getByText('Fixture save failed').waitFor();assert.equal(state.shared_resources,false);rejectProfile=false;await setup.getByRole('button',{name:'Apply',exact:true}).click();await setup.waitFor({state:'hidden'});assert.equal(state.shared_resources,true);assert.equal(state.allow_inline_references,false);
  await page.evaluate(()=>SetupSelector.open());await setup.locator('input[value=personal]').check();await setup.getByRole('button',{name:'Apply',exact:true}).click();await setup.waitFor({state:'hidden'});assert.equal(state.shared_resources,false);assert.equal(state.personal_providers,true);
  state={...state,shared_resources:true,personal_providers:true};await page.evaluate(()=>SetupSelector.open());await setup.getByText(/Current configuration: Mixed/).waitFor();assert.equal(await setup.locator('input[value=operator]').isChecked(),true);assert.equal(await setup.getByLabel('Also allow users’ own provider connections').isChecked(),true);
  await setup.locator('input[value=sociaro]').check();await setup.getByRole('button',{name:'Open Sociaro workspace'}).click();const remote=page.locator('.remote-dialog');await remote.locator('summary').click();await remote.getByLabel('Server origin for existing key').fill('https://remote.example.test');await remote.getByLabel('Remote company ID').fill(company);await remote.getByLabel('Company generation key').fill('synthetic-fixture-key-for-test');await remote.locator('input[type=checkbox]').check();await remote.getByRole('button',{name:'Verify and connect'}).click();await remote.getByRole('heading',{name:'Fixture company'}).waitFor();assert.equal(connects,1);assert.equal(state.shared_resources,true);assert.equal(state.personal_providers,true);
  await remote.locator('select[name=model]').selectOption('mock/image');await remote.getByLabel('Prompt',{exact:true}).fill('Fixture prompt');await remote.getByRole('button',{name:'Review estimate'}).click();await remote.getByRole('button',{name:'Generate remotely'}).click();await remote.getByRole('button',{name:'Retry same submission'}).waitFor();assert.equal(await remote.getByLabel('Prompt',{exact:true}).isDisabled(),true);await page.reload();await init();await page.evaluate(()=>RemoteWorkspace.open());await remote.getByRole('button',{name:'Retry same submission'}).waitFor();await remote.getByRole('button',{name:'Retry same submission'}).click();await remote.getByRole('link',{name:'Download result'}).waitFor();assert.equal(submissions.length,2);assert.equal(submissions[0].key,submissions[1].key);assert.deepEqual(submissions[0].body,submissions[1].body);
  fs.mkdirSync(process.env.UI_SCREENSHOT_DIR||'/tmp/gw170-ui',{recursive:true});for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.ok(await remote.evaluate(n=>n.scrollWidth<=n.clientWidth+1),'remote dialog overflow '+width);await page.screenshot({path:path.join(process.env.UI_SCREENSHOT_DIR||'/tmp/gw170-ui',`remote-${process.env.UI_BROWSER_ENGINE||'chromium'}-${theme}-${width}.png`)});}
  await remote.getByRole('button',{name:'Disconnect',exact:true}).click();await remote.locator('summary').waitFor();assert.equal(connected,false);assert.deepEqual(errors,[]);
  console.log('PASS: three scenarios, first-owner prompt, no-write skip/reload, failed save, reversible profiles, mixed state, explicit isolated connection, remote estimate/job/history/download, stable retry idempotency, disconnect and both themes at 1440/390/320px.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
