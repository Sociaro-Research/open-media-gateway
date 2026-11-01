// Real UI with gated localhost API fixtures. No sleeps, accounts or provider calls.
const {chromium}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const a='11111111-1111-4111-8111-111111111111',b='22222222-2222-4222-8222-222222222222';
const company=(id,name)=>({id,name:name||(id===a?'Organization A':'Organization B'),role:'admin',balance_usd:50,credit_limit_usd:100,default_weekly_limit_usd:10,storage_bytes:0});
const server=http.createServer((req,res)=>{
 const name=path.basename(new URL(req.url,'http://fixture').pathname),file=path.join(root,name.includes('.')?name:'index.html');
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});

(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH});
 const results=[];
 async function run(name,test){
  const context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage(),errors=[],gates=[];
  page.setDefaultTimeout(8000);page.on('pageerror',e=>errors.push(e.message));
  // Any unexpected external request is rejected rather than leaving the fixture.
  await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.fallback():route.abort());
  await page.route('**/v1/**',async route=>{
   const request=route.request(),p=new URL(request.url()).pathname;
   // Automatic managed-key restore is a read-only lookup transported as POST; creation remains forbidden.
   if(p==='/v1/playground-key/restore'){assert.equal(request.method(),'POST');const body=request.postDataJSON();assert.deepEqual(Object.keys(body),['organization_id']);assert.equal(typeof body.organization_id,'string');return route.fulfill({status:404,json:{error:{message:'No synthetic saved key'}}});}
   assert.equal(request.method(),'GET','Navigation must not write account data');
   const gate=gates.find(g=>!g.used&&p==='/v1/organizations/'+g.id);
   if(gate){gate.used=true;gate.arrive();return route.fulfill(await gate.response);}
   const json=value=>route.fulfill({json:value});
   if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,shared_resources:true}});
   if(p==='/v1/me')return json({id:'fixture-user',email:'fixture@example.test',name:'Fixture User'});
   if(p==='/v1/organizations')return json({data:[company(a),company(b)]});
   if([a,b].some(id=>p==='/v1/organizations/'+id))return json(company(p.split('/').pop()));
   if(p.endsWith('/members'))return json({data:[{id:'fixture-user',email:'fixture@example.test',role:'admin',weekly_limit_usd:10,weekly_committed_usd:0,total_consumed_usd:0}]});
   return json({data:[],has_more:false});
  });
  function next(id){
   let arrive,release,arrivalTimeout;
   const reached=new Promise((resolve,reject)=>{
    arrivalTimeout=setTimeout(()=>reject(new Error('Organization GET did not arrive within 8000ms: '+id)),8000);
    arrive=()=>{clearTimeout(arrivalTimeout);resolve();};
   }),response=new Promise(resolve=>{release=resolve;});
   // A preceding navigation failure can occur before the caller awaits reached.
   reached.catch(()=>{});
   const gate={id,arrive,response,used:false,clear:()=>clearTimeout(arrivalTimeout)};gates.push(gate);
   return {reached,succeed:name=>release({json:company(id,name)}),fail:()=>release({status:503,json:{error:{message:'Workspace unavailable; try opening it again.'}}})};
  }
  async function navigate(id){
   const n=await page.evaluate(()=>window.workspaceAttempts.length);
   await page.locator('#workspaceSelect').selectOption(id,{force:true});
   assert.equal(await page.evaluate(()=>window.workspaceAttempts.length),n+1);
   return n;
  }
  async function settled(n){
   // Observe the actual Workspace.open promise, including app.js's rejection
   // handler, so negative assertions cannot run before the released request.
   await page.evaluate(async n=>{await window.workspaceAttempts[n];await Promise.resolve();},n);
  }
  async function loaded(id){
   const n=await navigate(id);await settled(n);
   await page.locator('#organizationsContent .member-limit-form').first().waitFor();
   assert.equal(await page.locator('#organizationsContent h1').innerText(),company(id).name);
   return n;
  }
  async function retain(id){
   const heading=await page.locator('#organizationsContent h1').elementHandle();
   // FormUI reparents the input during enhancement; wait before fill selects it.
   const input=page.locator('#organizationsContent .member-limit-form .floating-field input').first();
   await input.fill('25');
   assert.equal(await input.inputValue(),'25','The current draft must exist before releasing the stale response');
   return async()=>{
    assert.equal(await heading.evaluate(node=>node.isConnected),true,'Stale response replaced the current heading');
    assert.equal(await page.locator('#workspaceSelect').inputValue(),id);
    assert.equal(await page.locator('#organizationsContent h1').innerText(),company(id).name);
    assert.equal(await input.inputValue(),'25','Stale response discarded the current draft');
    assert.equal(await page.locator('#organizationsContent .operation-error').count(),0);
   };
  }
  try{
   await page.goto(base+'/');await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());
   await page.evaluate(()=>{
    const open=Workspace.open;
    window.workspaceAttempts=[];
    Workspace.open=function(...args){const pending=open.apply(this,args);window.workspaceAttempts.push(pending.then(()=>({ok:true}),error=>({ok:false,message:error.message})));return pending;};
   });
   await test({page,next,navigate,settled,loaded,retain});
   assert.deepEqual(errors,[]);assert.ok(gates.every(g=>g.used),'A request gate was never reached');
   results.push({name,passed:true});console.log('PASS: '+name);
  }catch(error){results.push({name,passed:false,error:error.message});console.error('FAIL: '+name+'\n'+error.stack);}
  finally{gates.forEach(gate=>gate.clear());await context.close();}
 }
 try{
  await run('A→B late failure preserves B and its draft',async({next,navigate,settled,loaded,retain})=>{
   const old=next(a),n=await navigate(a);await old.reached;await loaded(b);const unchanged=await retain(b);old.fail();await settled(n);await unchanged();
  });
  for(const outcome of ['fail','succeed']){
   await run('A→B→A stale '+outcome+' preserves the new A attempt',async({next,navigate,settled,loaded,retain})=>{
    const old=next(a),n=await navigate(a);await old.reached;await loaded(b);await loaded(a);const unchanged=await retain(a);old[outcome]('Obsolete A response');await settled(n);await unchanged();
   });
  }
  await run('A→B stale success preserves B',async({next,navigate,settled,loaded,retain})=>{
   const old=next(a),n=await navigate(a);await old.reached;await loaded(b);const unchanged=await retain(b);old.succeed('Obsolete A response');await settled(n);await unchanged();
  });
  await run('Active destination failure stays visible and can be retried with the keyboard',async({page,next,navigate,settled,loaded})=>{
   await loaded(a);const active=next(b),n=await navigate(b);await active.reached;active.fail();await settled(n);
   const error=page.locator('#organizationsContent .operation-error');await error.waitFor();
   assert.equal(await error.innerText(),'Workspace unavailable; try opening it again.');
   assert.equal(await page.locator('#workspaceSelect').inputValue(),b);
   for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.ok(await error.evaluate(node=>{const box=node.getBoundingClientRect();return box.left>=0&&box.right<=innerWidth;}),'Error overflow at '+width);assert.equal(await error.isVisible(),true);}
   await page.setViewportSize({width:1440,height:1000});
   const retry=await page.evaluate(()=>window.workspaceAttempts.length);
   await page.locator('#workspaceToggle').focus();await page.keyboard.press('ArrowDown');await page.keyboard.press('End');await page.keyboard.press('Enter');
   assert.equal(await page.evaluate(()=>window.workspaceAttempts.length),retry+1);await settled(retry);
   await page.locator('#organizationsContent .member-limit-form').first().waitFor();
   assert.equal(await page.locator('#organizationsContent h1').innerText(),'Organization B');assert.equal(await error.count(),0);
  });
  await run('Navigation away before rejection leaves visible and hidden views intact',async({page,next,navigate,settled,loaded})=>{
   await loaded(b);
   const old=next(a),n=await navigate(a);await old.reached;await page.locator('#generationNav').click();
   const before=await page.locator('#organizationsContent').innerHTML();
   old.fail();await settled(n);assert.equal(await page.locator('#generationView').isVisible(),true);assert.equal(await page.locator('#organizationsView').isVisible(),false);
   assert.equal(await page.locator('#organizationsContent').innerHTML(),before,'Stale rejection modified the hidden workspace');
  });
  for(const outcome of ['fail','succeed']){
   await run('Leave and return to A before stale '+outcome,async({page,next,navigate,settled,loaded,retain})=>{
    const old=next(a),n=await navigate(a);await old.reached;await page.locator('#generationNav').click();await loaded(a);const unchanged=await retain(a);
    old[outcome]('Obsolete A response');await settled(n);await unchanged();
   });
  }
  await run('An old failure cannot replace a current destination error',async({page,next,navigate,settled})=>{
   const old=next(a),first=await navigate(a);await old.reached;
   const active=next(b),second=await navigate(b);await active.reached;active.fail();await settled(second);
   const error=await page.locator('#organizationsContent .operation-error').elementHandle();assert.ok(error);
   old.fail();await settled(first);assert.equal(await error.evaluate(node=>node.isConnected),true,'Stale failure replaced the active error');
  });
  if(process.env.UI_NAVIGATION_RESULTS)fs.writeFileSync(process.env.UI_NAVIGATION_RESULTS,JSON.stringify(results,null,2)+'\n');
  assert.ok(results.every(result=>result.passed),results.filter(result=>!result.passed).length+' navigation regressions failed');
 }finally{await browser.close();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
