// Real portal routing with isolated loopback responses; no live writes or credentials.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const root=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',out=path.resolve(process.env.UI_EVIDENCE_DIR||'dist/checks/history-navigation/'+engine);
fs.mkdirSync(out,{recursive:true});
const organization={id:'11111111-1111-4111-8111-111111111111',name:'History company',role:'admin',balance_usd:10,credit_limit_usd:20};
const server=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://fixture').pathname;
 const file=path.join(root,path.basename(p).includes('.')?path.basename(p):'index.html');
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
const fingerprint=()=>Object.fromEntries(['app.js','workspace.js','templates.js'].map(name=>[name,crypto.createHash('sha256').update(fs.readFileSync(path.join(root,name))).digest('hex')]));
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),results=[],sourceBefore=fingerprint();
 async function run(name,{personal=false,entry='/profile'}={},test){
  const context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage(),errors=[],external=[],writes=[],requests=[];
  page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message));
  await context.addInitScript(selected=>sessionStorage.setItem('gateway-workspace-history-user',selected),personal?'personal':organization.id);
  await context.route('**/*',async route=>{
   const req=route.request(),url=new URL(req.url()),p=url.pathname;
   if(url.origin!==base){external.push(req.url());return route.abort();}
   if(p==='/auth/company-connection')return route.fulfill({status:404,json:{connected:false}});
   if(p==='/site.json')return route.fulfill({json:{branding:{sidebar_subsections:true}}});
   if(!p.startsWith('/v1/'))return route.continue();
   requests.push({method:req.method(),path:p,query:url.search});
   if(p==='/v1/playground-key/restore'&&req.method()==='POST')return route.fulfill({status:404,json:{error:{message:'No synthetic key'}}});
   if(req.method()!=='GET'){writes.push({method:req.method(),path:p});return route.fulfill({status:405,json:{error:{message:'Fixture writes forbidden'}}});}
   const json=value=>route.fulfill({json:value});
   if(p==='/v1/info')return json({auth_mode:'google',console:{generation_ui:true,personal_providers:personal,shared_resources:!personal}});
   if(p==='/v1/me')return json({id:'history-user',name:'Saved history name',email:'history@example.test',is_system_admin:true});
   if(p==='/v1/organizations')return json({data:[organization]});
   if(p==='/v1/organizations/'+organization.id)return json(organization);
   if(p.endsWith('/members'))return json({data:[{id:'history-user',name:'Saved history name',email:'history@example.test',role:'admin'}]});
   if(p.endsWith('/usage'))return json({data:[],month:url.searchParams.get('month')});
   return json({data:[],has_more:false});
  });
  const settled=()=>page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
  const traverse=async(direction)=>{await page.evaluate(()=>{window.__historyObserved=new Promise(resolve=>addEventListener('popstate',()=>resolve(),{once:true}));});await page[direction]();await page.evaluate(()=>window.__historyObserved);await settled();};
  try{
   await page.goto(base+entry);await page.waitForFunction(()=>consoleInitialized&&Workspace.ready()&&!initializationPending);await settled();
   await test({page,requests,settled,traverse});assert.deepEqual(errors,[]);assert.deepEqual(external,[]);assert.deepEqual(writes,[]);
   results.push({name,passed:true,requests});console.log('PASS '+name);
  }catch(error){results.push({name,passed:false,error:error.message,errors,external,writes,requests});await page.screenshot({path:path.join(out,'failure-'+results.length+'.png'),fullPage:true});console.error(error.stack);}
  finally{await context.close();}
 }
 const profile=page=>page.getByLabel('Display name',{exact:true});
 const keysReady=async page=>{await page.locator('#organizationsContent #newKeyName').waitFor();assert.equal(new URL(page.url()).pathname,'/api-keys');assert.equal(await page.locator('#keysNav').getAttribute('aria-current'),'page');};
 try{
  await run('Fragment Back and Forward preserve the exact unsaved Profile input',{},async({page,traverse,settled})=>{
   const input=profile(page);await input.waitFor();await input.fill('Unsaved fragment draft');await input.focus();const handle=await input.elementHandle();
   await page.evaluate(()=>history.pushState(history.state,'',location.pathname+'#history-fixture'));
   await traverse('goBack');assert.equal(new URL(page.url()).hash,'');assert.equal(await handle.evaluate(n=>n.isConnected&&n===document.activeElement),true);assert.equal(await input.inputValue(),'Unsaved fragment draft');
   await traverse('goForward');assert.equal(new URL(page.url()).hash,'#history-fixture');assert.equal(await handle.evaluate(n=>n.isConnected),true);assert.equal(await input.inputValue(),'Unsaved fragment draft');
   await traverse('goBack');for(const [theme,width] of [['light',1440],['dark',320]]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await settled();assert.equal(await input.inputValue(),'Unsaved fragment draft');assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:path.join(out,'profile-'+theme+'-'+width+'.png'),fullPage:true});}
  });
  for(const personal of [false,true])await run((personal?'Personal':'Company')+' Keys navigation preserves real Back/Forward routing',{personal,entry:'/'},async({page,traverse})=>{
   const length=await page.evaluate(()=>history.length);await page.locator('#keysNav').click();await keysReady(page);assert.equal(await page.evaluate(()=>history.length),length+1);
   await traverse('goBack');await page.locator('#generationView').waitFor();assert.equal(new URL(page.url()).pathname,'/');assert.equal(await page.locator('#generationNav').getAttribute('aria-current'),'page');
   await traverse('goForward');await keysReady(page);assert.equal(await page.locator('#organizationsContent h1').innerText(),personal?'Profile':'History company');
  });
  await run('Changed query re-enters the route instead of preserving an obsolete Profile mount',{},async({page,traverse})=>{
   const input=profile(page);await input.fill('Query-specific draft');const handle=await input.elementHandle();
   // Traversal targets a genuinely different query while the rendered route is bare /profile.
   await page.evaluate(()=>{history.pushState(history.state,'','/profile?history-fixture=changed');history.pushState(history.state,'','/profile#history-fixture');});
   await traverse('goBack');await page.waitForFunction(n=>!n.isConnected,handle);await profile(page).waitFor();assert.equal(await profile(page).inputValue(),'Saved history name');assert.equal(await page.locator('#profileView').isVisible(),true);
  });
  await run('Same-path workspacePage history restores Members and Management',{entry:'/organizations'},async({page,traverse})=>{
   const tabs=page.getByRole('tab');await tabs.filter({hasText:/^Members$/}).click();await page.locator('#organizationsContent .member-limit-form').first().waitFor();
   assert.equal(await page.evaluate(()=>history.state.workspacePage),'Members');
   await page.evaluate(()=>history.pushState(history.state,'',location.pathname));await tabs.filter({hasText:/^Management$/}).click();await page.getByRole('heading',{name:'Management',exact:true}).waitFor();assert.equal(await page.evaluate(()=>history.state.workspacePage),'Management');
   await traverse('goBack');await page.locator('#organizationsContent .member-limit-form').first().waitFor();assert.equal(await page.evaluate(()=>history.state.workspacePage),'Members');assert.equal(new URL(page.url()).pathname,'/organizations');
   await traverse('goForward');await page.getByRole('heading',{name:'Management',exact:true}).waitFor();assert.equal(await page.evaluate(()=>history.state.workspacePage),'Management');
  });
 }finally{const sourceAfter=fingerprint();fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({engine,sourceBefore,sourceAfter,results},null,2));await browser.close();server.closeAllConnections();server.close();}
 assert.deepEqual(fingerprint(),sourceBefore,'source changed during fixture');assert.ok(results.every(r=>r.passed),'history navigation regression failed');console.log('PASS '+results.length+' history navigation groups');
})().catch(error=>{console.error(error);server.close();process.exitCode=1;});
