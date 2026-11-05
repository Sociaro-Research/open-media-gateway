// Current console UI against loopback-only, read-only synthetic API fixtures.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=path.resolve('web/public/ui');
const out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/header-keys/'+engine);fs.mkdirSync(out,{recursive:true});
const a='11111111-1111-4111-8111-111111111111',b='22222222-2222-4222-8222-222222222222';
const organization=(id,role='admin')=>({id,name:id===a?'Organization A':'Organization B',role,balance_usd:10,credit_limit_usd:20});
const server=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://fixture').pathname;
 if(p==='/auth/company-connection'){res.writeHead(404,{'Content-Type':'application/json'});return res.end('{"connected":false}');}
 const file=path.join(root,path.basename(p).includes('.')?path.basename(p):'index.html');
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port,browser=await ({chromium,webkit}[engine]).launch({headless:true,executablePath:process.env.UI_BROWSER_PATH}),results=[];
 async function run(name,settings,test){
  if(process.env.UI_CASE&&!name.includes(process.env.UI_CASE))return;
  const context=await browser.newContext({viewport:{width:1440,height:1000},hasTouch:!!settings.touch}),page=await context.newPage(),errors=[],requests=[],forbidden=[];
  page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));
  await context.addInitScript(selected=>sessionStorage.setItem('gateway-workspace-fixture-user',selected),settings.selected||b);
  await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.fallback():route.abort());
  await page.route('**/site.json',route=>route.fulfill({json:{ui_url:base,docs_url:base+'/external-docs'}}));
  await page.route('**/v1/**',async route=>{
   const request=route.request(),url=new URL(request.url()),p=url.pathname;requests.push({method:request.method(),path:p,query:url.search});
   if(p==='/v1/playground-key/restore'&&request.method()==='POST')return route.fulfill({status:404,json:{error:{message:'No synthetic key'}}});
   if(p==='/v1/invitations/preview'&&request.method()==='POST')return route.fulfill({json:{organization_name:'Invited organization',role:'supermanager',first_owner:false}});
   if(request.method()!=='GET'){forbidden.push(request.method()+' '+p);return route.fulfill({status:405,json:{error:{message:'Writes forbidden'}}});}
   const json=value=>route.fulfill({json:value}),organizations=settings.empty?[]:[organization(a,settings.role),organization(b,settings.role)];
   if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:!!settings.personal,shared_resources:!settings.personal}});
   if(p==='/v1/me')return settings.anonymous?route.fulfill({status:401,json:{error:{message:'Sign in'}}}):json({id:'fixture-user',email:'fixture@example.test',name:'Fixture User',is_client_manager:!!settings.clientManager,is_system_admin:!!settings.admin});
   if(p==='/v1/organizations')return json({data:organizations});
   if([a,b].some(id=>p==='/v1/organizations/'+id))return json(organization(p.split('/').pop(),settings.role));
   if(p.endsWith('/members'))return json({data:[{id:'fixture-user',email:'fixture@example.test',role:settings.role||'admin'}]});
   if(p.endsWith('/usage'))return json({data:[],month:url.searchParams.get('month')});
   return json({data:[],has_more:false});
  });
  try{
   await page.goto(base+(settings.entry||'/'));
   if(settings.anonymous)await page.waitForFunction(()=>authState==='anonymous');
   else await page.waitForFunction(()=>consoleInitialized&&Workspace.ready());
   await test({page,requests,base});assert.deepEqual(errors,[]);assert.deepEqual(forbidden,[]);
   results.push({name,passed:true});console.log('PASS '+name);
  }catch(error){results.push({name,passed:false,error:error.message});await page.screenshot({path:path.join(out,'failure-'+results.length+'.png'),fullPage:true});console.error('FAIL '+name+'\n'+error.stack);}
  finally{await context.close();}
 }
 const keysReady=async page=>{await page.locator('#organizationsContent #newKeyName').waitFor();assert.equal(new URL(page.url()).pathname,'/api-keys');assert.equal(await page.locator('#keysNav').getAttribute('aria-current'),'page');};
 try{
  await run('Header order, selected-company route, reload and Back/Forward',{entry:'/api-keys'},async({page,requests})=>{
   await keysReady(page);
   assert.deepEqual(await page.locator('#sectionNav > button:visible').allTextContents(),['Keys','Docs','Playground','Templates']);
   assert.equal(await page.locator('#keysNav').count(),1);
   assert.equal(await page.locator('#organizationsContent h1').innerText(),'Organization B');
   assert.ok(requests.some(r=>r.path==='/v1/api-keys'&&r.query.includes(b)));assert.ok(!requests.some(r=>r.path==='/v1/api-keys'&&r.query.includes(a)));
   await page.reload();await keysReady(page);
   await page.locator('#generationNav').click();assert.equal(new URL(page.url()).pathname,'/');
   await page.goBack();await keysReady(page);await page.goForward();await page.locator('#generationView').waitFor();
   await page.locator('#keysNav').focus();await page.keyboard.press('Enter');await keysReady(page);
   for(const theme of ['light','dark'])for(const width of [1440,390,320]){
    await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.setViewportSize({width,height:1000});
    await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
    await page.locator('#keysNav').scrollIntoViewIfNeeded();
    assert.ok(await page.locator('#keysNav').evaluate(n=>{const b=n.getBoundingClientRect();return b.width>0&&b.left>=0&&b.right<=innerWidth;}));
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    await page.screenshot({path:path.join(out,theme+'-'+width+'.png'),fullPage:true});
   }
  });
  for(const role of ['supermanager','admin','developer','system_admin'])await run('Existing '+role+' key capability',{role,entry:'/api-keys'},async({page})=>{await keysReady(page);assert.equal(await page.locator('#keysNav').isVisible(),true);});
  for(const role of ['member','content_officer','client_manager'])await run('No key page for '+role,{role,entry:'/api-keys'},async({page,requests})=>{
   await page.waitForFunction(()=>currentConsoleRoute==='organizations');assert.equal(await page.locator('#keysNav').isVisible(),false);assert.equal(await page.locator('#organizationsContent .keys-form').count(),0);
   // Requests/Billing may read eligible key labels for the fixed company filter.
   // This grants no key-management page, issuance or unrelated company scope.
   await page.getByText('No requests yet.',{exact:true}).waitFor();
   const labelReads=requests.filter(r=>r.path==='/v1/api-keys');
   assert.ok(labelReads.every(r=>r.method==='GET'&&r.query==='?organization_id='+b));
   assert.equal(await page.locator('#newKeyName').count(),0);
   assert.equal(await page.getByRole('tab',{name:'API keys',exact:true}).count(),0);
   assert.equal(new URL(page.url()).pathname,'/organizations');
  });
  await run('Existing direct organization route remains intact',{entry:'/organizations'},async({page})=>{await page.locator('#organizationsContent .member-limit-form').first().waitFor();assert.equal(new URL(page.url()).pathname,'/organizations');assert.equal(await page.locator('#keysNav').getAttribute('aria-current'),null);});
  await run('Legacy configuration Keys resolves current workspace',{entry:'/configuration#keys'},async({page})=>{await keysReady(page);assert.equal(await page.locator('#organizationsContent h1').innerText(),'Organization B');});
  await run('Same-document invitation fragment keeps existing capture',{entry:'/organizations',role:'member'},async({page,base})=>{await page.goto(base+'/organizations#invite='+'s'.repeat(43));await page.getByText('You will join with the Supermanager role. Your other organizations are unchanged.',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>sessionStorage.getItem('gateway-pending-invite')),'s'.repeat(43));});
  await run('Keys subsection exits and Back restore actual page',{entry:'/api-keys'},async({page})=>{
   await keysReady(page);
   for(const tab of ['Members','Billing']){
    await page.getByRole('tab',{name:tab,exact:true}).click();assert.equal(new URL(page.url()).pathname,'/organizations');assert.equal(await page.locator('#keysNav').getAttribute('aria-current'),null);
    await page.goBack();await keysReady(page);await page.goForward();await page.waitForFunction(label=>document.querySelector('[role=tab][aria-selected=true]')?.textContent===label,tab);
    await page.locator('#keysNav').click();await keysReady(page);
   }
   await page.locator('#userMenuToggle').focus();await page.keyboard.press('ArrowDown');await page.getByRole('menuitem',{name:'Profile',exact:true}).press('Enter');await page.locator('#profileContent').waitFor();assert.equal(new URL(page.url()).pathname,'/profile');
   await page.goBack();await keysReady(page);
  });
  await run('Personal Keys to Profile subsection and Back',{personal:true,selected:'personal',entry:'/api-keys'},async({page})=>{await keysReady(page);await page.getByRole('tab',{name:'Profile',exact:true}).click();assert.equal(new URL(page.url()).pathname,'/organizations');assert.equal(await page.locator('#keysNav').getAttribute('aria-current'),null);await page.goBack();await keysReady(page);});
  await run('Administrator header wraps all rows above page content',{admin:true,entry:'/profile'},async({page})=>{
   await page.locator('#profileContent').waitFor();
   for(const theme of ['light','dark'])for(const width of [1440,390,320]){
    await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    await page.setViewportSize({width,height:1000});await page.evaluate(async()=>{for(let frame=0;frame<20;frame++)await new Promise(requestAnimationFrame);});
    const metrics=await page.evaluate(()=>{const box=n=>{const r=n.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height,bottom:r.bottom};};return{header:box(document.querySelector('.topbar')),nav:box(sectionNav),children:[...sectionNav.children].filter(n=>!n.hidden).map(n=>({id:n.id,...box(n)}))};});
    fs.writeFileSync(path.join(out,'admin-header-'+theme+'-'+width+'.json'),JSON.stringify(metrics,null,2));await page.screenshot({path:path.join(out,'admin-header-'+theme+'-'+width+'.png'),fullPage:true});
    if(process.env.UI_HEADER_DIAGNOSTIC&&width===320)for(const css of ['.topbar.wrapped .section-nav{width:100%;min-width:0}', '.topbar.wrapped .section-nav{align-self:start}', '.topbar.wrapped .section-nav{max-width:none;min-width:0;width:100%}']){const style=await page.addStyleTag({content:css});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));console.log('HEADER CSS',css,JSON.stringify(await page.evaluate(()=>({header:document.querySelector('.topbar').getBoundingClientRect().bottom,nav:sectionNav.getBoundingClientRect().bottom,child:workspacePicker.getBoundingClientRect().bottom}))));await style.evaluate(n=>n.remove());}
    assert.ok(metrics.children.every(n=>n.bottom<=metrics.header.bottom+1),'Header child overlaps main content at '+width+': '+JSON.stringify(metrics));
    await page.evaluate(()=>workspaceOpen.textContent='A very long organization name that must remain contained in navigation');await page.evaluate(async()=>{for(let frame=0;frame<20;frame++)await new Promise(requestAnimationFrame);});
    const long=await page.evaluate(()=>{const r=workspacePicker.getBoundingClientRect(),h=document.querySelector('.topbar').getBoundingClientRect();return{left:r.left,right:r.right,bottom:r.bottom,headerBottom:h.bottom,headerLeft:h.left,headerRight:h.right,width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth};});fs.writeFileSync(path.join(out,'admin-long-'+theme+'-'+width+'.json'),JSON.stringify(long,null,2));assert.ok(long.left>=long.headerLeft-1&&long.right<=long.headerRight+1&&!long.overflow&&long.bottom<=long.headerBottom+1,'Long company header containment '+JSON.stringify(long));await page.screenshot({path:path.join(out,'admin-long-'+theme+'-'+width+'.png'),fullPage:true});
   }
  });
  await run('Personal Playground to header Keys preserves Back entry',{personal:true,selected:'personal'},async({page})=>{
   await page.locator('#generationView').waitFor();assert.equal(new URL(page.url()).pathname,'/');
   const length=await page.evaluate(()=>history.length);
   await page.locator('#keysNav').click();await keysReady(page);
   assert.equal(await page.evaluate(()=>history.length),length+1,'One explicit Keys click must add exactly one history entry');
   await page.goBack();assert.equal(new URL(page.url()).pathname,'/');await page.locator('#generationView').waitFor();assert.equal(await page.locator('#generationNav').getAttribute('aria-current'),'page');
   await page.goForward();await keysReady(page);assert.equal(await page.locator('#organizationsContent h1').innerText(),'Profile');
  });
  await run('Personal Keys retain personal selection',{personal:true,selected:'personal',entry:'/api-keys'},async({page,requests})=>{await keysReady(page);assert.equal(await page.locator('#organizationsContent h1').innerText(),'Profile');assert.ok(requests.some(r=>r.path==='/v1/api-keys'&&r.query===''));});
  await run('No workspace does not invent a key scope',{empty:true,entry:'/api-keys'},async({page,requests})=>{await page.waitForFunction(()=>currentConsoleRoute==='organizations');assert.equal(await page.locator('#keysNav').isVisible(),false);assert.ok(!requests.some(r=>r.path==='/v1/api-keys'));});
  await run('Client manager header remains restricted',{clientManager:true,role:'client_manager',entry:'/api-keys'},async({page,requests})=>{assert.equal(await page.locator('#keysNav').isVisible(),false);assert.ok(!requests.some(r=>r.path==='/v1/api-keys'));});
  await run('Touch Keys action and workspace switching',{touch:true},async({page,requests})=>{
   await page.setViewportSize({width:390,height:1000});await page.locator('#keysNav').tap();await keysReady(page);
   await page.evaluate(id=>Workspace.set(id),a);await page.locator('#keysNav').tap();await keysReady(page);
   assert.equal(await page.locator('#organizationsContent h1').innerText(),'Organization A');assert.ok(requests.some(r=>r.path==='/v1/api-keys'&&r.query.includes(a)));
  });
  await run('Docs retains configured destination and theme',{},async({page,base})=>{
   await page.evaluate(()=>localStorage.setItem('gateway-theme','dark'));
   const target=await page.evaluate(()=>GatewaySite.docsURL());assert.equal(new URL(target).origin,base);
   let assigned;await page.route(base+'/?**',route=>{assigned=route.request().url();return route.fulfill({body:'Configured Docs fixture'});});
   await page.locator('#docsNav').click();await page.waitForURL(url=>url.pathname==='/'&&url.search.length>0);assert.equal(page.url(),target);assert.equal(assigned,target);
  });
  await run('Anonymous Keys does not reveal management',{anonymous:true,entry:'/api-keys'},async({page,requests})=>{assert.equal(await page.locator('#organizationsView').isVisible(),false);assert.equal(await page.locator('#keysNav').isVisible(),false);assert.ok(!requests.some(r=>r.path==='/v1/api-keys'));});
  fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({engine,results},null,2)+'\n');assert.ok(results.every(r=>r.passed),results.filter(r=>!r.passed).length+' groups failed');
 }finally{await browser.close();server.closeAllConnections();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
