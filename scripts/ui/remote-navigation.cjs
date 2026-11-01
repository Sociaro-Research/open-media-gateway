// Real console/header/Settings/setup/remote scripts, synthetic loopback APIs only.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui'),source=process.env.UI_SOURCE_DIR||root,engine=process.env.UI_BROWSER_ENGINE||'chromium',out=process.env.UI_EVIDENCE_DIR||path.resolve('.local/remote-navigation-'+engine),company='11111111-1111-4111-8111-111111111111';
const settings={version:1,shared_resources:true,personal_providers:false,generation_ui:true,allow_inline_references:true,customer_workspaces:false,setup_pending:false};
let connected=false,administrator=true,remoteReads=0,companyReads=0,writes=[];
const server=http.createServer((req,res)=>{
 const url=new URL(req.url,'http://fixture'),p=url.pathname,json=data=>{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
 if(p==='/v1/playground-key/restore'){res.writeHead(404,{'Content-Type':'application/json'});return res.end(JSON.stringify({error:{code:'not_found',message:'No saved fixture key'}}));}
 if(req.method!=='GET'){writes.push({path:p,method:req.method});return res.writeHead(405).end();}
 if(p==='/site.json')return json({});
 if(p==='/v1/info')return json({auth_mode:'google',console:settings});
 if(p==='/v1/me')return json({id:'synthetic-owner',name:'Fixture Owner',email:'owner@fixture.test',session_id:'fixture-session',is_system_admin:administrator});
 if(p==='/auth/methods')return json({google:true,email:true});
 if(p==='/v1/system/settings')return json(settings);
 if(p==='/v1/organizations')return json({data:[{id:company,name:'Sociaro',role:'member'}]});
 if(p==='/v1/organizations/'+company){companyReads++;return json({id:company,name:'Sociaro',role:'member'});}
 if(p==='/v1/system/remote-workspace'){remoteReads++;return json({connected,version:connected?1:0,origin:'https://remote.fixture.test',organization_id:company,organization_name:'Remote fixture company',context:'fixture-context',operation_scope:'fixture-scope'});}
 if(p==='/v1/system/remote-workspace/authorization')return json({pending:false});
 if(p==='/v1/system/remote-workspace/proxy/v1/key')return json({organization:{id:company,available_usd:0}});
 if(p.startsWith('/v1/'))return json({data:[]});
 const name=p.startsWith('/ui/')?path.basename(p):'index.html',candidate=path.join(source,name),file=fs.existsSync(candidate)?candidate:path.join(root,name);if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await(engine==='webkit'?webkit:chromium).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})}),page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[];
 page.on('pageerror',e=>{if(!e.message.startsWith('ResizeObserver loop'))errors.push(e.message);});await page.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():r.abort());
 const setup=page.locator('.setup-dialog'),remote=page.locator('.remote-dialog');
 // The real surrounding settings modules are outside this fixture's API scope.
 async function settingsPage(){await page.evaluate(()=>{for(const name of ['SalesBonuses','BrandingSettings','RequestLimits','EmailDeliverySettings','CatalogSync','PaymentNotificationSettings','PaymentProviderSettings','LoginMethodSettings'])window[name]={mount:async()=>{},settings:async()=>{},close(){}};window.MarkupSettings={mount:async()=>({setSystemVersion(){}})};});await page.locator('#systemNav').click();await page.getByRole('button',{name:'Change Mode'}).waitFor();}
 async function matrix(label){fs.mkdirSync(out,{recursive:true});for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' overflow '+width);await page.screenshot({path:path.join(out,`${label}-${engine}-${theme}-${width}.png`)});}await page.setViewportSize({width:1440,height:1100});}
 async function viaScenario(){await page.getByRole('button',{name:'Change Mode'}).focus();await page.keyboard.press('Enter');await setup.waitFor({state:'visible'});await setup.locator('input[value=sociaro]').focus();await page.keyboard.press('Space');const open=setup.getByRole('button',{name:'Open Sociaro workspace'});await open.focus();await page.keyboard.press('Enter');await remote.waitFor({state:'visible'});}
 try{
  await page.goto(base);await page.locator('#userMenu').waitFor();await page.locator('#workspaceOpen').waitFor();assert.equal(await page.locator('#workspaceOpen').textContent(),'Sociaro');await settingsPage();await matrix('company-service-header');
  if(process.env.UI_BASELINE){await viaScenario();await remote.getByRole('heading',{name:'Connect your Sociaro account'}).waitFor();await matrix('scenario-connection');return;}
  assert.equal(await page.locator('#remoteWorkspaceNav').count(),0);assert.equal(await page.locator('#sectionNav').getByRole('button',{name:'Sociaro',exact:true}).count(),1);assert.equal(remoteReads,0);
  await page.locator('#workspaceOpen').focus();await page.keyboard.press('Enter');await page.locator('#organizationsView').waitFor();await page.getByRole('heading',{name:'Sociaro',exact:true}).waitFor();assert.ok(companyReads>0);assert.equal(remoteReads,0);await settingsPage();
  await page.getByRole('button',{name:'Change Mode'}).click();await setup.locator('input[value=sociaro]').check();await page.keyboard.press('Escape');assert.equal(remoteReads,0);assert.deepEqual(writes,[]);
  await viaScenario();await remote.getByRole('heading',{name:'Connect your Sociaro account'}).waitFor();assert.equal(remoteReads,1);assert.equal(settings.customer_workspaces,false);await matrix('scenario-connection');await page.keyboard.press('Escape');await remote.waitFor({state:'hidden'});assert.equal(await page.getByRole('button',{name:'Change Mode'}).evaluate(n=>n===document.activeElement),true);
  connected=true;await viaScenario();await remote.getByRole('heading',{name:'Remote fixture company',exact:true}).waitFor();assert.equal(remoteReads,2);await remote.getByRole('button',{name:'Close sociaro remote workspace'}).click();await remote.waitFor({state:'hidden'});assert.equal(connected,true);assert.equal(await page.locator('#remoteWorkspaceNav').count(),0);
  await page.reload();await page.locator('#userMenu').waitFor();await settingsPage();await viaScenario();await remote.getByRole('heading',{name:'Remote fixture company',exact:true}).waitFor();await page.keyboard.press('Escape');assert.equal(connected,true);assert.deepEqual(writes,[]);assert.deepEqual(settings,{version:1,shared_resources:true,personal_providers:false,generation_ui:true,allow_inline_references:true,customer_workspaces:false,setup_pending:false});
  administrator=false;await page.reload();await page.locator('#userMenu').waitFor();assert.equal(await page.locator('#systemNav').isVisible(),false);assert.equal(await page.locator('#remoteWorkspaceNav').count(),0);assert.equal(await page.locator('#workspaceOpen').isVisible(),true);assert.deepEqual(errors,[]);
  console.log('PASS: real console has one local Sociaro company control and no outbound shortcut; keyboard local navigation, scenario-only remote entry, no-write cancel/close, focus return, connected reopen/reload, retained onboarding gate, ordinary-role visibility and themes/1440/390/320px.');
 }finally{fs.mkdirSync(out,{recursive:true});fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({remoteReads,companyReads,writes,errors},null,2));await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
