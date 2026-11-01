// Synthetic localhost fixtures; never touches a deployed gateway or remote provider.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui'),source=process.env.UI_SOURCE_DIR||root;
const engine=process.env.UI_BROWSER_ENGINE||'chromium',out=process.env.UI_EVIDENCE_DIR||path.resolve('.local/setup-selector-'+engine);
let state={version:1,setup_pending:false,shared_resources:true,personal_providers:false,generation_ui:true,customer_workspaces:false},writes=[],mode='',pending,heldReady,unavailableReads=0,remoteOpens=0;
const server=http.createServer(async(req,res)=>{
 let body='';for await(const part of req)body+=part;
 const json=(code,value)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
 if(req.url==='/')return res.end(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"><main class="shell"><h1>Settings</h1><section id="fixture"></section></main><script>window.api=async(p,o={})=>{const r=await fetch(p,{...o,headers:{'Content-Type':'application/json',...o.headers}}),v=await r.json();if(!r.ok){const e=new Error(v.error.message);e.status=r.status;throw e;}return v;};window.applySystemAccess=next=>window.applied=next;window.RemoteWorkspace={setActor(){},open:async()=>fetch('/remote-open',{method:'POST'})};</script><script src="/form-ui.js"></script><script src="/setup-selector.js"></script>`);
 if(req.url==='/remote-open'){remoteOpens++;return json(200,{});}
 if(req.url==='/v1/system/settings'){if(unavailableReads){unavailableReads--;return json(503,{error:{message:'Synthetic read outage'}});}return json(200,state);}
 if(req.url==='/v1/system/setup-profile'){
  const input=JSON.parse(body);writes.push(input);
  if(mode==='failure')return json(503,{error:{message:'Synthetic save failure'}});
  if(mode==='conflict'){state={...state,version:state.version+1,shared_resources:false,personal_providers:true};unavailableReads=1;return json(409,{error:{message:'Settings changed'}});}
  if(input.version!==state.version)return json(409,{error:{message:'Settings changed'}});
  const save=()=>{state={...state,version:state.version+1,setup_pending:false,shared_resources:input.profile==='operator',personal_providers:input.profile==='personal'||input.allow_personal};if(mode==='interrupted')return res.destroy();json(200,state);};
  if(mode==='pending'){pending=save;heldReady?.();}else save();return;
 }
 const name=path.basename(new URL(req.url,'http://fixture').pathname),candidate=path.join(source,name),file=fs.existsSync(candidate)?candidate:path.join(root,name);
 if(!fs.existsSync(file)){res.writeHead(404).end();return;}res.setHeader('Content-Type',file.endsWith('.css')?'text/css':'text/javascript');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin='http://127.0.0.1:'+server.address().port;
 const browser=await(engine==='webkit'?webkit:chromium).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[],measurements=[];
 page.on('pageerror',e=>errors.push(e.message));await page.route('**/*',r=>new URL(r.request().url()).origin===origin?r.continue():r.abort());
 const dialog=page.locator('.setup-dialog'),apply=()=>dialog.getByRole('button',{name:'Apply',exact:true});
 const init=()=>page.evaluate(async()=>{await SetupSelector.initialize({id:'synthetic-owner',session_id:'fixture',is_system_admin:true});const state=await api('/v1/system/settings');SetupSelector.mount(document.querySelector('#fixture'),state);});
 const open=async()=>{await page.getByRole('button',{name:'Change Mode'}).click();await dialog.waitFor({state:'visible'});};
 const chosen=()=>dialog.locator('input[name=scenario]:checked').getAttribute('value');
 async function matrix(label){
  fs.mkdirSync(out,{recursive:true});
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.evaluate(()=>document.fonts.ready);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const m=await dialog.evaluate(d=>{
    const box=n=>{const r=n.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height};};
    const buttons=[...d.querySelectorAll('.dialog-actions>button')].map(b=>{const r=b.getBoundingClientRect(),range=document.createRange();range.selectNodeContents(b);const text=range.getBoundingClientRect(),s=getComputedStyle(b);return {...box(b),font:s.fontSize,centerX:Math.abs(text.x+text.width/2-r.x-r.width/2),centerY:Math.abs(text.y+text.height/2-r.y-r.height/2)};});
    const legend=d.querySelector('legend'),option=d.querySelector('.setup-option'),style=getComputedStyle(d);
    return {dialog:box(d),overflow:d.scrollWidth>d.clientWidth+1,buttons,legendGap:box(option).y-box(legend).y-box(legend).h,padding:parseFloat(style.paddingLeft),checked:d.querySelector('input:checked')?.value};
   });measurements.push({label,theme,width,...m});
   if(!process.env.UI_BASELINE){assert.equal(m.overflow,false);assert.equal(m.padding,width>700?24:20);assert.ok(Math.abs(m.legendGap-16)<1);assert.ok(Math.abs(m.buttons[0].h-44)<1);assert.equal(m.buttons[0].h,m.buttons[1].h);assert.equal(m.buttons[0].w,m.buttons[1].w);for(const b of m.buttons){assert.equal(b.font,'14px');assert.ok(b.centerX<=1);assert.ok(b.centerY<=2);}assert.equal(m.checked,'operator');}
   await page.screenshot({path:path.join(out,`${label}-${engine}-${theme}-${width}.png`)});
  }await page.setViewportSize({width:1440,height:1100});
 }
 try{
  await page.goto(origin);await init();await open();await matrix('saved-service');
  if(process.env.UI_BASELINE)return;
  assert.equal(await chosen(),'operator');assert.equal(await apply().isDisabled(),true);
  assert.equal(await dialog.locator('input:focus').getAttribute('value'),'operator');
  await page.keyboard.press('ArrowUp');assert.equal(await chosen(),'personal');assert.equal(await apply().isEnabled(),true);
  await page.keyboard.press('Escape');assert.equal(writes.length,0);await open();assert.equal(await chosen(),'operator');
  await dialog.locator('input[value=personal]').check();await dialog.getByRole('button',{name:'Skip for now'}).click();assert.equal(writes.length,0);await open();assert.equal(await chosen(),'operator');
  await dialog.locator('input[value=personal]').check();await dialog.getByRole('button',{name:/Close choose/}).click();assert.equal(writes.length,0);await open();
  await dialog.locator('input[value=personal]').check();mode='failure';await apply().click();await dialog.getByText('Synthetic save failure').waitFor();assert.equal(await chosen(),'personal');assert.equal(state.shared_resources,true);mode='';
  await apply().click();await dialog.waitFor({state:'hidden'});assert.equal(state.personal_providers,true);assert.equal(state.shared_resources,false);assert.equal(await page.locator('.setup-section>.field-explanation').textContent(),'For myself');
  await page.reload();await init();await open();assert.equal(await chosen(),'personal');assert.equal(await apply().isDisabled(),true);
  await dialog.locator('input[value=operator]').check();await dialog.getByLabel('Also allow users’ own provider connections').uncheck();mode='pending';await apply().click();
  assert.equal(await dialog.locator('input[value=operator]').isDisabled(),true);assert.equal(await dialog.getByRole('button',{name:'Skip for now'}).isDisabled(),true);await page.keyboard.press('Escape');assert.equal(await dialog.isVisible(),true);
  const count=writes.length;await page.evaluate(()=>SetupSelector.open());assert.equal(await dialog.count(),1);assert.equal(writes.length,count);await new Promise(r=>pending?r():heldReady=r);mode='';pending();pending=null;heldReady=null;await dialog.waitFor({state:'hidden'});
  await open();assert.equal(await chosen(),'operator');assert.equal(await dialog.getByLabel('Also allow users’ own provider connections').isChecked(),false);
  await dialog.getByLabel('Also allow users’ own provider connections').check();assert.equal(await apply().isEnabled(),true);await apply().click();await dialog.waitFor({state:'hidden'});await open();assert.equal(await chosen(),'operator');assert.equal(await dialog.getByLabel('Also allow users’ own provider connections').isChecked(),true);
  await dialog.getByLabel('Also allow users’ own provider connections').uncheck();mode='conflict';await apply().click();await dialog.getByText(/Retrying automatically/).waitFor();assert.equal(await apply().isDisabled(),true);await dialog.getByText(/Current configuration has been loaded/).waitFor();assert.equal(await chosen(),'personal');assert.equal(await apply().isDisabled(),true);mode='';
  await dialog.locator('input[value=operator]').check();mode='interrupted';await apply().click();await dialog.locator('[role=status]').filter({hasText:/fetch|network|load|failed/i}).waitFor();assert.equal(state.shared_resources,true);mode='';await page.keyboard.press('Escape');await open();assert.equal(await chosen(),'operator');assert.equal(await apply().isDisabled(),true);
  await page.keyboard.press('Escape');state={...state,shared_resources:false,personal_providers:false};await open();assert.equal(await dialog.locator('input:checked').count(),0);await dialog.getByText(/Custom: personal and shared access disabled/).waitFor();assert.equal(await apply().isDisabled(),true);
  const beforeRemote=JSON.stringify(state),beforeWrites=writes.length;await dialog.locator('input[value=sociaro]').check();await Promise.all([page.waitForResponse(r=>r.url()===origin+'/remote-open'),dialog.getByRole('button',{name:'Open Sociaro workspace'}).click()]);assert.equal(remoteOpens,1);assert.equal(JSON.stringify(state),beforeRemote);assert.equal(writes.length,beforeWrites);
  state={...state,shared_resources:true,personal_providers:false,setup_pending:true};await open();assert.equal(await chosen(),'operator');assert.equal(await apply().isEnabled(),true);await apply().click();await dialog.waitFor({state:'hidden'});assert.equal(state.setup_pending,false);
  await open();await dialog.locator('input[value=personal]').check();mode='pending';await apply().click();await new Promise(r=>pending?r():heldReady=r);const applied=await page.evaluate(()=>window.applied);await page.evaluate(()=>SetupSelector.initialize(null));mode='';await Promise.all([page.waitForResponse(r=>r.url().endsWith('/v1/system/setup-profile')),Promise.resolve().then(()=>pending())]);assert.equal(await dialog.isVisible(),false);assert.deepEqual(await page.evaluate(()=>window.applied),applied);assert.deepEqual(errors,[]);
  console.log('PASS: saved policy/reload, shared plus personal, custom legacy, native radio keyboard, Skip/Close/Escape without writes, failure retry, pending locks, conflicts, interrupted response reconciliation, remote separation and themes at 1440/390/320px.');
 }finally{fs.mkdirSync(out,{recursive:true});fs.writeFileSync(path.join(out,'measurements.json'),JSON.stringify(measurements,null,2));await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
