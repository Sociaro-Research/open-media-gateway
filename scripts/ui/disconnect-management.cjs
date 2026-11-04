// GW-311: actual company Management UI, synthetic central/local APIs only.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const root=path.resolve('web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',out=process.env.UI_EVIDENCE_DIR||'dist/checks/gw311-'+engine;
const company='11111111-1111-4111-8111-111111111111',other='22222222-2222-4222-8222-222222222222',key='synthetic-company-key';
let active=true,allowed=true,role='supermanager',version=1,mode='',deletes=[],centralWrites=[],reads=[],held=null,readHeld=null;
const record=()=>({connected:active,access:'company-api-v1',context:'context-'+version,origin:'https://central.fixture.test',organization_id:company,organization_name:'Fixture company',version});
const org=id=>({id,name:id===company?'Fixture company':'Other company',role,balance_usd:10,credit_limit_usd:0,storage_bytes:0,weekly_limit_usd:10,weekly_committed_usd:0});
function reset(){active=true;allowed=true;role='supermanager';version=1;mode='';deletes=[];centralWrites=[];reads=[];held=null;readHeld=null;}
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname;let raw='';for await(const c of req)raw+=c;const json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(v));};
 if(p==='/auth/company-connection')return json(active?{connected:true,connection:record(),key}:{connected:false});
 if(p==='/v1/system/remote-workspace'){
  reads.push({method:req.method,authorization:req.headers.authorization||''});if(!allowed)return json({error:{message:'Local administrator required'}},403);
  if(req.method==='DELETE'){
   const body=JSON.parse(raw);deletes.push(body);if(mode==='hold'){held=()=>{active=false;version++;json(record());};return;}
   if(mode==='error'){mode='';return json({error:{message:'Fixture temporary failure'}},503);}
   if(active&&body.version!==version)return json({error:{message:'Connection changed'}},409);
   if(active){active=false;version++;}
   if(mode==='lost'){mode='';return json({error:{message:'Fixture lost response'}},503);}
   return json(record());
  }
  if(mode==='read-hold'){readHeld=()=>json(record());return;}
  return json(record());
 }
 const central=p.startsWith('/auth/company-api/'),route=central?p.slice('/auth/company-api'.length):p;
 if(central){assert.equal(req.headers.authorization,'Bearer '+key);if(!['GET','HEAD'].includes(req.method))centralWrites.push({path:route,method:req.method});}
 if(route==='/v1/info')return json({auth_mode:'google',console:{generation_ui:true,shared_resources:true,personal_providers:false}});
 if(route==='/v1/me')return json({id:central?'central-owner':'local-owner',name:central?'Central operator':'Local operator',email:'fixture@example.test',session_id:'fixture-session',is_system_admin:!central});
 if(route==='/v1/organizations')return json({data:central?[org(company)]:[]});
 if(route==='/v1/organizations/'+company||route==='/v1/organizations/'+other)return json(org(route.endsWith(other)?other:company));
 if(route==='/v1/key')return json({key:{id:'connection-key',user_id:'central-owner',organization_id:company,resource_mode:'shared',scopes:['generation','organizations:manage']},organization:org(company)});
 if(route.endsWith('/payment-methods'))return json({data:[]});
 if(route.endsWith('/storage'))return json({settings:{driver:'gateway'},can_manage:role==='supermanager',gateway_allowance_bytes:1000000});
 if(route==='/v1/playground-key/restore')return json({error:{message:'None'}},404);
 if(route.startsWith('/v1/'))return json({data:[],has_more:false});
 if(p==='/auth/methods')return json({google:true,email:true});
 if(p==='/site.json')return json({api_url:'',docs_url:'',branding:{name:'Fixture'}});
 const file=path.join(root,p.startsWith('/ui/')?path.basename(p):'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 fs.mkdirSync(out,{recursive:true});await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),context=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),page=await context.newPage(),errors=[],external=[],groups=[];page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));await context.route('**/*',r=>new URL(r.request().url()).origin===origin?r.continue():(external.push(r.request().url()),r.abort()));
 const button=()=>page.locator('[data-company-connection]').getByRole('button',{name:'Disconnect',exact:true});
 async function open(){await page.goto(origin+'/organizations');await page.waitForFunction(()=>Workspace.ready());await page.getByRole('tab',{name:'Management',exact:true}).click();await button().waitFor();}
 // A local identity/URL appears before the async Playground-key restore finishes.
 // Settle its real 404 terminal UI before replacing the synthetic account/document.
 async function localReady(){await page.waitForFunction(()=>!CompanyConnection.active()&&Workspace.ready()&&document.querySelector('#userName').textContent==='Local operator'&&document.querySelector('#connectionState').textContent==='Create a Playground key to save it to your account, or paste an existing key.'&&!document.querySelector('#restoreGenerationKey').disabled);}
 try{
  reset();await open();assert.equal(await page.locator('#companyConnectionButton').count(),0);assert.equal(deletes.length,0);assert.equal(centralWrites.length,0);assert.ok(reads.every(x=>!x.authorization));assert.equal(await page.getByRole('heading',{name:'Management',exact:true}).count(),1);await page.getByText('Gateway storage',{exact:true}).waitFor();assert.equal(await page.getByText(/Cannot read properties/).count(),0);assert.ok((await button().boundingBox()).width<300);groups.push('current company Management contains local-authorized control; menu removed; reads only');
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>GatewayTheme.apply(t),theme);await button().scrollIntoViewIfNeeded();await button().focus();assert.ok(await button().evaluate(n=>n===document.activeElement));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:path.join(out,theme+'-'+width+'.png'),fullPage:true});}groups.push('both themes and three widths; visible focus and compact shared section');
  page.once('dialog',d=>d.dismiss());await button().focus();await button().press('Enter');assert.equal(deletes.length,0);assert.equal(await button().isEnabled(),true);groups.push('native confirmation cancel performs no mutation');
  mode='error';page.once('dialog',d=>d.accept());await button().click();await page.getByText('Fixture temporary failure',{exact:true}).waitFor();assert.equal(deletes.length,1);assert.equal(active,true);assert.equal(await button().isEnabled(),true);groups.push('failure keeps remote connection and permits exact retry');
  mode='hold';page.once('dialog',d=>d.accept());await button().click();await page.waitForFunction(()=>document.querySelector('[data-company-connection] button').disabled);await button().evaluate(n=>n.click());assert.equal(deletes.length,2);held();await page.waitForURL(origin+'/');await localReady();assert.equal(await page.locator('#userName').textContent(),'Local operator');assert.deepEqual(deletes,[{version:1},{version:1}]);groups.push('pending repeat suppressed; success reloads actual local mode');
  reset();await open();mode='lost';page.once('dialog',d=>d.accept());await button().click();await page.getByText('Fixture lost response',{exact:true}).waitFor();page.once('dialog',d=>d.accept());await button().click();await page.waitForURL(origin+'/');await localReady();assert.deepEqual(deletes,[{version:1},{version:1}]);assert.equal(version,2);groups.push('lost response retries original version without second disconnect');
  reset();await open();mode='hold';page.once('dialog',d=>d.accept());await button().click();await page.waitForFunction(()=>document.querySelector('[data-company-connection] button').disabled);await page.getByRole('tab',{name:'Support',exact:true}).click();held();await page.waitForURL(origin+'/');await localReady();groups.push('confirmed disconnect enters local mode after same-account navigation');
  reset();await open();version++;page.once('dialog',d=>d.accept());await button().click();await page.getByText('Connection changed',{exact:true}).waitFor();assert.equal(active,true);assert.deepEqual(deletes,[{version:1}]);groups.push('newer connection version is not disconnected');
  reset();role='member';await open();assert.equal(await page.getByRole('heading',{name:'Management',exact:true}).count(),0);assert.equal(await page.getByRole('heading',{name:'File Storage',exact:true}).count(),0);assert.equal(await page.getByLabel('Default weekly limit for new members (USD)',{exact:true}).count(),0);groups.push('local owner with remote member role sees only connection control');
  await page.evaluate(async id=>{const host=document.createElement('div');host.id='otherMount';document.body.append(host);await RemoteWorkspace.mountCompanyManagement(host,id);},other);assert.equal(await page.locator('#otherMount').textContent(),'');groups.push('different company never gets connection control');
  allowed=false;page.once('dialog',d=>d.accept());await button().click();await page.getByText('Local administrator required',{exact:true}).waitFor();assert.equal(deletes.length,0);assert.equal(active,true);groups.push('revoked local authority denied by endpoint despite remote role');
  reset();await open();await page.getByRole('tab',{name:'Management',exact:true}).click();await button().waitFor();assert.equal(await button().isEnabled(),true);await page.evaluate(()=>{window.staleDisconnect=document.querySelector('[data-company-connection] button');});await page.getByRole('tab',{name:'Support',exact:true}).click();await page.evaluate(()=>staleDisconnect.click());assert.equal(deletes.length,0);groups.push('departed Management stale button cannot disconnect');
  reset();await open();await page.getByRole('tab',{name:'Support',exact:true}).click();mode='read-hold';await page.getByRole('tab',{name:'Management',exact:true}).click();for(let i=0;i<100&&!readHeld;i++)await new Promise(r=>setTimeout(r,10));assert.ok(readHeld);await page.getByRole('tab',{name:'Support',exact:true}).click();mode='';readHeld();await page.waitForTimeout(100);assert.equal(await page.locator('[data-company-connection]').count(),0);assert.equal(await page.getByRole('heading',{name:'File Storage',exact:true}).count(),0);assert.equal(deletes.length,0);groups.push('late read cannot add connection or storage to departed page');
  reset();await open();await page.evaluate(()=>{window.staleDisconnect=document.querySelector('[data-company-connection] button');RemoteWorkspace.setActor({id:'changed-account'});staleDisconnect.click();});assert.equal(deletes.length,0);groups.push('changed account invalidates previously rendered action');
  reset();const touch=await browser.newContext({viewport:{width:390,height:900},hasTouch:true,isMobile:true}),tp=await touch.newPage();await touch.route('**/*',r=>new URL(r.request().url()).origin===origin?r.continue():r.abort());await tp.goto(origin+'/organizations');await tp.waitForFunction(()=>Workspace.ready());await tp.getByRole('tab',{name:'Management',exact:true}).tap();const tb=tp.locator('[data-company-connection]').getByRole('button',{name:'Disconnect',exact:true});await tb.waitFor();tp.once('dialog',d=>d.dismiss());await tb.tap();assert.equal(deletes.length,0);assert.equal(await tb.isEnabled(),true);await tp.screenshot({path:path.join(out,'touch-management.png'),fullPage:true});await touch.close();groups.push('touch Management and confirmation cancellation');
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);assert.equal(centralWrites.length,0);
  fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,groups,errors,external,source:Object.fromEntries(['remote-workspace.js','organizations.js','app.js','index.html','styles.css'].map(f=>[f,crypto.createHash('sha256').update(fs.readFileSync(path.join(root,f))).digest('hex')]))},null,2));console.log('PASS',groups.length,engine);
 }catch(e){fs.writeFileSync(path.join(out,'failure.json'),JSON.stringify({error:e.message,groups,errors,reads,deletes,centralWrites},null,2));await page.screenshot({path:path.join(out,'failure.png'),fullPage:true}).catch(()=>{});throw e;}finally{await browser.close();server.closeAllConnections();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
