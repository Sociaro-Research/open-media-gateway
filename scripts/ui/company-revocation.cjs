// Local synthetic HTTP fixtures; no deployed instance, real key or paid call.
const {chromium,webkit}=require('playwright');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict'),vm=require('node:vm');
const source=path.resolve(process.env.UI_SOURCE_DIR||'web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',out=process.env.UI_EVIDENCE_DIR||path.resolve('dist/checks/gw318/browser-'+engine);
fs.mkdirSync(out,{recursive:true});
let connected=true,mode='ok',generation=1,proxyCalls=0,localCustomerWrites=0,reconnectOpens=0;
const key='synthetic-company-key',record=()=>({connected:true,access:'company-api-v1',context:'connection-'+generation,version:generation,origin:'https://console.sociaro.ai',organization_id:'fixture-company'});
const settings={version:1,setup_pending:false,shared_resources:true,personal_providers:false,installation_role:'ordinary'};
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
 if(u.pathname==='/auth/company-connection')return json(connected?{connected:true,connection:record(),key}:{connected:false});
 if(u.pathname.startsWith('/auth/company-api/')){
  proxyCalls++;assert.equal(req.headers.authorization,'Bearer '+key);assert.equal(req.headers['x-company-context'],record().context);
  if(mode==='network')return res.destroy();
  if(mode==='outage')return json({error:{code:'service_unavailable',message:'Temporary outage'}},503);
  if(mode==='generic')return json({error:{code:'unauthorized',message:'Key has been revoked'}},401);
  if(mode==='expired')return json({error:{code:'unauthorized',message:'Invalid key'}},401);
  if(mode==='revoked'){connected=false;generation++;return json({error:{code:'company_connection_revoked',message:'Installation is disconnected'}},410);}
  if(mode==='fake410')return json({error:{code:'other_problem',message:'Unrelated failure'}},410);
  return json({ok:true});
 }
 if(u.pathname==='/v1/system/settings')return json(settings);
 if(u.pathname.startsWith('/v1/')){if(req.method!=='GET')localCustomerWrites++;return json({ok:true});}
 if(u.pathname==='/')return res.end(`<!doctype html><html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"><body><main class="shell"><h1>Installation</h1><p id="state" role="status">Checking</p><button id="request" class="secondary">Check central</button><section id="settings"></section></main><script src="/company-connection.js"></script><script src="/form-ui.js"></script><script>window.api=async(p,o={})=>{const r=await fetch(p,o),v=await r.json();if(!r.ok){const e=new Error(v.error.message);e.status=r.status;throw e;}return v};window.RemoteWorkspace={setActor(){},open(){document.body.dataset.reconnect='requested';fetch('/reconnect-open')}};window.applySystemAccess=()=>{};</script><script src="/setup-selector.js"></script><script>(async()=>{const c=await CompanyConnection.initialize();document.querySelector('#state').textContent=c?'Connected':'Local owner';document.querySelector('#request').onclick=()=>fetch('/v1/test',{method:'POST',body:'original-operation'}).catch(()=>{});if(!c){await SetupSelector.initialize({id:'local-owner',is_system_admin:true});SetupSelector.mount(document.querySelector('#settings'),await api('/v1/system/settings'));}})();</script></body></html>`);
 if(u.pathname==='/reconnect-open'){reconnectOpens++;return json({});}
 const file=path.join(source,path.basename(u.pathname));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':'text/css');res.end(fs.readFileSync(file));
});

// Hold navigation deliberately: an old page must not fall through to local
// processing after terminal recovery while the new document is still pending.
async function terminalDispatchLatch(){
 const calls=[],navigations=[],origin='https://local.fixture.test';
 const original=async(input)=>{const p=new URL(input instanceof Request?input.url:String(input),origin).pathname;calls.push(p);if(p==='/auth/company-connection')return Response.json({connected:true,connection:record(),key});if(p.startsWith('/auth/company-api'))return Response.json({error:{code:'company_connection_revoked'}},{status:410});return Response.json({});};
 const location={href:origin+'/',origin,assign:p=>navigations.push(p)},window={fetch:original};
 vm.runInNewContext(fs.readFileSync(path.join(source,'company-connection.js'),'utf8'),{window,location,Request,Response,URL,Headers,DOMException});
 await window.CompanyConnection.initialize();await assert.rejects(window.fetch('/v1/generations',{method:'POST',body:'kept'}),{name:'AbortError'});
 for(const destination of ['/v1/generations','/v1/info','https://console.sociaro.ai/v1/models'])await assert.rejects(window.fetch(destination,{method:'POST'}),{name:'AbortError'});
 await assert.rejects(window.CompanyConnection.initialize(),{name:'AbortError'});
 assert.equal(window.CompanyConnection.active(),false);assert.equal(window.CompanyConnection.key(),'');assert.deepEqual(navigations,['/']);assert.equal(calls.filter(x=>x.startsWith('/auth/company-api')).length,1);assert.equal(calls.some(x=>x==='/v1/generations'),false);
 await window.fetch('/v1/system/remote-workspace');assert.equal(calls.at(-1),'/v1/system/remote-workspace');
}
(async()=>{
 await terminalDispatchLatch();
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],inspections=[];
 page.on('pageerror',e=>errors.push(e.message));await page.route(u=>u.origin!==origin,r=>r.abort());
 try{
  await page.goto(origin);await page.getByText('Connected',{exact:true}).waitFor();
  for(const failure of ['outage','generic','expired','fake410','network']){mode=failure;await page.locator('#request').click();await page.waitForTimeout(120);assert.equal(await page.locator('#state').textContent(),'Connected');assert.equal(await page.evaluate(()=>CompanyConnection.active()),true);assert.equal(connected,true);assert.equal(localCustomerWrites,0);}
  mode='ok';await page.reload();await page.getByText('Connected',{exact:true}).waitFor();
  // Actual browser navigation consumes the durable local proxy signal, then
  // bootstraps as the same local owner with the unchanged mode settings.
  mode='revoked';await page.locator('#request').click();await page.getByText('Local owner',{exact:true}).waitFor();await page.getByRole('button',{name:'Change Mode',exact:true}).waitFor();assert.equal(localCustomerWrites,0);
  await page.reload();await page.getByText('Local owner',{exact:true}).waitFor();await page.getByRole('button',{name:'Change Mode',exact:true}).click();await page.locator('.setup-dialog').waitFor({state:'visible'});
  assert.equal(await page.locator('input[name=scenario]').count(),3);assert.equal(await page.locator('input[value=operator]').isChecked(),true);
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.screenshot({path:path.join(out,theme+'-'+width+'.png')});const m=await page.locator('.setup-dialog').evaluate(d=>({width:d.getBoundingClientRect().width,scroll:d.scrollWidth,client:d.clientWidth}));assert.ok(m.scroll<=m.client+1);inspections.push({theme,width,...m});}
  await page.locator('input[value=personal]').check();assert.equal(await page.getByRole('button',{name:'Apply',exact:true}).isEnabled(),true);await page.keyboard.press('Escape');assert.equal(localCustomerWrites,0);
  await page.getByRole('button',{name:'Change Mode',exact:true}).click();await page.locator('input[value=central]').check();await page.getByRole('button',{name:'Connect to central server',exact:true}).click();await page.waitForFunction(()=>document.body.dataset.reconnect==='requested');assert.equal(reconnectOpens,1);assert.equal(localCustomerWrites,0);
  connected=true;generation++;mode='ok';await page.reload();await page.getByText('Connected',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>CompanyConnection.record().version),generation);assert.deepEqual(errors,[]);
  fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({engine,passed:10,groups:['terminal-dispatch-latch','outage/generic/expired/unrelated/network','connected-reload','revocation-local-owner','disconnected-reload','three-mode-choices','themes-widths','keyboard-no-write','reconnect-entry','new-connection-reload'],errors,inspections,proxyCalls,localCustomerWrites},null,2));console.log('PASS 10 revoked connection groups '+engine);
 }catch(error){await page.screenshot({path:path.join(out,'failure.png')}).catch(()=>{});fs.writeFileSync(path.join(out,'failure.json'),JSON.stringify({message:error.message,errors,mode,connected,proxyCalls,localCustomerWrites},null,2));throw error;}finally{await browser.close();server.closeAllConnections();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
