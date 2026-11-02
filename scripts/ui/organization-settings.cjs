const {evidencePath} = require('./evidence.cjs');
// Real organization UI with local API fixtures; no production requests.
const {chromium,webkit}=require('playwright');
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const name=path.basename(new URL(req.url,'http://fixture').pathname),candidate=name.includes('.')?name:'index.html',before=process.env.UI_SOURCE_BASELINE&&path.join(process.env.UI_SOURCE_BASELINE,candidate),file=before&&fs.existsSync(before)?before:path.join(root,candidate);if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await ({chromium,webkit}[engine]).launch({headless:true,executablePath:process.env.UI_BROWSER_PATH}),page=await browser.newPage({viewport:{width:1440,height:1000}});
 page.setDefaultTimeout(10000);
 const id='11111111-1111-4111-8111-111111111111',writes=[],errors=[];
 let nativeTab='Tab';
 let admin=true,role='admin',fail=false,failLogging=false,holdLogging=false;const pendingLogging=[];let grants=[],grantVersion=1;const grantModel={id:'22222222-2222-4222-8222-222222222222',provider:'mock',model:'image',public_id:'mock/image',name:'Fixture image',enabled_by_default:false,capabilities:{media_type:'image'}};
 const company={id,name:'Example company',balance_usd:50,credit_limit_usd:100,default_weekly_limit_usd:20,storage_bytes:0,minimize_request_logging:false,billing_profile:{business_name:'Example Ltd',email:'billing@example.test'}};
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/v1/**',async route=>{
  const request=route.request(),url=new URL(request.url()),p=url.pathname,method=request.method(),json=data=>route.fulfill({json:data});
  if(method==='PUT'){
   const data=request.postDataJSON();writes.push({p,data});
   if(failLogging&&p.endsWith('/request-logging'))return route.fulfill({status:500,json:{error:{message:'Logging save failed'}}});
   if(holdLogging&&p.endsWith('/request-logging'))await new Promise(resolve=>pendingLogging.push(resolve));
   if(fail)return route.fulfill({status:500,json:{error:{message:'Save failed'}}});
   if(p==='/v1/organizations/'+id)Object.assign(company,data);
   if(p.endsWith('/client-manager'))company.client_manager_email=data.user;
   if(p.endsWith('/model-access')){assert.equal(data.version,grantVersion);grants=data.model_ids;grantVersion++;return json({model_ids:grants,version:grantVersion});}
   if(p.endsWith('/request-logging'))company.minimize_request_logging=data.minimize_request_logging;
   return route.fulfill({status:204});
  }
  if(p==='/v1/me')return json({id:'tester',name:'Tester',email:'tester@example.test',is_system_admin:admin,is_client_manager:role==='client_manager'});
  if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,shared_resources:true}});
  if(p==='/v1/organizations')return json({data:[{...company,role}]});
  if(p==='/v1/organizations/'+id)return json({...company,role});
  if(p.endsWith('/storage'))return json({settings:{driver:'gateway'},can_manage:true,gateway_allowance_bytes:10000000000});
  if(p.endsWith('/model-access'))return json({data:[grantModel],model_ids:grants,version:grantVersion});
  if(p.endsWith('/markups'))return json({system:{default_percent:10,exceptions:[],version:1},company:{default_percent:null,exceptions:[],version:1}});
  if(p.endsWith('/limits'))return json({system:{values:{key_generations_minute:60,user_generations_minute:120,key_generations_second:5,user_generations_second:10,key_reads_minute:120,user_reads_minute:240,user_key_creations_minute:10,user_active_keys:20},version:1},organization:{values:{},version:1}});
  return json({data:[],has_more:false});
 });
 const base='http://127.0.0.1:'+server.address().port;
 const tabs=page.locator('.organization-tabs'),form=page.locator('.organization-form').filter({has:page.getByRole('heading',{name:/^(Management|Settings)$/})});
 const choose=name=>tabs.getByRole('tab',{name,exact:true}).click();
 const heading=name=>page.getByRole('heading',{name,exact:true});
 const metrics=[];
 async function capture(name){for(const theme of ['light','dark'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),name+' overflow '+width);
 const geometry=await page.evaluate(()=>{const forms=[...document.querySelectorAll('.organization-page > .keys-form')];return forms.map(n=>({title:n.querySelector('h2,h3')?.textContent,width:n.getBoundingClientRect().width,left:n.getBoundingClientRect().left,fields:[...n.querySelectorAll(':scope > .organization-field,:scope > .system-toggle')].map(c=>({label:c.textContent.trim(),x:c.getBoundingClientRect().x,y:c.getBoundingClientRect().y,width:c.getBoundingClientRect().width}))}));});metrics.push({name,theme,width,geometry});
 if(!process.env.UI_BASELINE){for(const box of geometry.filter(n=>['Management','Settings','Organization request limits'].includes(n.title))){assert.ok(box.fields.length>=2,'actual field geometry captured');if(box.fields.length>=2){const [a,b]=box.fields;assert.ok(Math.abs(a.width-b.width)<1,'equal fields');if(width>760){assert.ok(Math.abs(a.y-b.y)<1,'desktop row');assert.ok(b.x>a.x,'desktop columns');}else{assert.ok(b.y>a.y,'mobile stacked');assert.ok(Math.abs(a.x-b.x)<1,'mobile aligned');}}}if(name==='settings'){const peers=geometry.filter(n=>['Settings','Additional models','Markups','Organization request limits'].includes(n.title));assert.equal(peers.length,4);assert.ok(peers.every(n=>Math.abs(n.width-peers[0].width)<1&&Math.abs(n.left-peers[0].left)<1),'full-width aligned cards');}}
 await page.screenshot({path:evidencePath('/tmp/organization-')+name+'-'+theme+'-'+width+'.png',fullPage:true});}await page.setViewportSize({width:1440,height:1000});}

 try{
  await page.setContent('<input id=tabFirst><input id=tabSwitch type=checkbox><input id=tabSecond>');await page.locator('#tabFirst').focus();await page.keyboard.press('Tab');if(!await page.locator('#tabSwitch').evaluate(n=>n===document.activeElement)){nativeTab='Alt+Tab';await page.locator('#tabFirst').focus();await page.keyboard.press(nativeTab);assert.equal(await page.locator('#tabSwitch').evaluate(n=>n===document.activeElement),true,'Native full-control keyboard shortcut reaches switch');}
  await page.goto(base+'/organizations');await choose('Management');await heading('File Storage').waitFor();
  assert.equal(await tabs.getByRole('tab',{name:'Settings',exact:true}).count(),1);
  assert.equal(await form.getByLabel('Name',{exact:true}).inputValue(),'Example company');
  assert.equal(await page.getByLabel('Credit Limit (USD)',{exact:true}).count(),0);
  assert.equal(await page.getByLabel('Client manager',{exact:true}).count(),0);
  assert.equal(await page.locator('.organization-model-access,.markup-settings,.request-limits').count(),0);
  await heading('Billing details').waitFor();await capture('management');
  if(!process.env.UI_BASELINE){assert.equal(await heading('Request logging').count(),0);assert.equal(await form.getByRole('switch',{name:/Minimize request logging/}).isChecked(),false);assert.equal(writes.length,0);}
  await form.getByLabel('Name',{exact:true}).fill('Renamed company');
  await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();
  assert.deepEqual(writes.at(-1).data,{name:'Renamed company',default_weekly_limit_usd:20});
  if(!process.env.UI_BASELINE){
   const logging=form.getByRole('switch',{name:/Minimize request logging/}),save=form.getByRole('button',{name:'Save',exact:true});let count=writes.length;
   await logging.check();assert.equal(writes.length,count,'checkbox must not autosave');await save.click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(writes.slice(count),[{p:'/v1/organizations/'+id+'/request-logging',data:{minimize_request_logging:true}}]);assert.equal(company.minimize_request_logging,true);count=writes.length;await choose('Settings');await heading('Organization request limits').waitFor();await choose('Management');assert.equal(await logging.isChecked(),true);assert.equal(writes.length,count,'navigation retains true without writing');
   await form.getByLabel('Name',{exact:true}).fill('Partial save company');await logging.uncheck();count=writes.length;failLogging=true;await save.click();await form.getByText('Logging save failed',{exact:true}).waitFor();assert.equal(company.name,'Partial save company');assert.equal(company.minimize_request_logging,true);assert.equal(await logging.isChecked(),false);assert.deepEqual(writes.slice(count).map(w=>w.p),['/v1/organizations/'+id,'/v1/organizations/'+id+'/request-logging']);
   failLogging=false;count=writes.length;await save.click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(writes.slice(count).map(w=>w.p),['/v1/organizations/'+id+'/request-logging']);assert.equal(company.minimize_request_logging,false);
   holdLogging=true;await logging.check();await save.click();for(let i=0;i<100&&!pendingLogging.length;i++)await new Promise(r=>setTimeout(r,10));assert.equal(pendingLogging.length,1);await logging.uncheck();holdLogging=false;pendingLogging.shift()();await page.waitForFunction(()=>document.querySelector('.organization-settings-grid button[type=submit]')?.disabled===false);assert.equal(company.minimize_request_logging,true);assert.equal(await logging.isChecked(),false);count=writes.length;await save.click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(writes.slice(count),[{p:'/v1/organizations/'+id+'/request-logging',data:{minimize_request_logging:false}}]);
   await form.getByLabel('Name',{exact:true}).focus();await page.keyboard.press('Tab');assert.equal(await form.getByLabel('Default weekly limit for new members (USD)',{exact:true}).evaluate(n=>n===document.activeElement),true);await page.keyboard.press(nativeTab);assert.equal(await logging.evaluate(n=>n===document.activeElement),true,'Management keyboard field order');
  }

  await choose('Settings');await heading('Additional models').waitFor();await heading('Markups').waitFor();await heading('Organization request limits').waitFor();assert.equal(await page.locator('.request-limits input').first().isEnabled(),true);
  assert.equal(await form.getByLabel('Name',{exact:true}).count(),0);
  assert.equal(await page.locator('.organization-billing-details,.organization-storage').count(),0);
  if(!process.env.UI_BASELINE){const models=page.locator('.organization-model-access');await models.getByRole('combobox',{name:'Model to enable',exact:true}).click();await page.getByRole('option',{name:'mock/image',exact:true}).click();let count=writes.length;await models.getByRole('button',{name:'Add model',exact:true}).click();assert.equal(writes.length,count);await models.getByRole('button',{name:'Save',exact:true}).click();await models.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(grants,[grantModel.id]);await models.getByText('mock/image',{exact:true}).waitFor();}
  await capture('settings');if(process.env.UI_BASELINE){fs.writeFileSync(evidencePath('/tmp/organization-layout-metrics.json'),JSON.stringify({engine,metrics},null,2));return;}
  {const models=page.locator('.organization-model-access');await models.getByRole('button',{name:'Remove',exact:true}).click();await models.getByRole('button',{name:'Save',exact:true}).click();await models.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(grants,[]);assert.equal(grantVersion,3);}
  // A credit-only save must preserve Management values, including another admin's changes.
  company.name='Changed elsewhere';company.default_weekly_limit_usd=35;
  await form.getByLabel('Credit Limit (USD)',{exact:true}).fill('500');
  await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();
  assert.deepEqual(writes.at(-1).data,{name:'Changed elsewhere',default_weekly_limit_usd:35,credit_limit_usd:500});
  const before=writes.length;
  await form.getByLabel('Client manager',{exact:true}).fill('manager@example.test');
  await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();
  assert.deepEqual(writes.slice(before),[{p:'/v1/organizations/'+id+'/client-manager',data:{user:'manager@example.test'}}]);


  fail=true;await form.getByLabel('Credit Limit (USD)',{exact:true}).fill('600');
  await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByText('Save failed',{exact:true}).waitFor();
  assert.equal(await form.getByLabel('Credit Limit (USD)',{exact:true}).inputValue(),'600');assert.equal(company.credit_limit_usd,500);fail=false;
  await choose('Management');assert.equal(await form.getByRole('switch',{name:/Minimize request logging/}).isChecked(),false);assert.equal(await form.getByLabel('Name',{exact:true}).inputValue(),'Changed elsewhere');
  assert.equal(await form.getByLabel('Default weekly limit for new members (USD)',{exact:true}).inputValue(),'35');
  admin=false;await page.reload();await choose('Management');await heading('File Storage').waitFor();
  assert.equal(await tabs.getByRole('tab',{name:'Settings',exact:true}).count(),0);
  assert.equal(await page.locator('.organization-model-access,.markup-settings,.request-limits').count(),0);
  await form.getByLabel('Name',{exact:true}).fill('Company admin edit');await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(company.name,'Company admin edit');
  for(const nextRole of ['member','client_manager']){role=nextRole;await page.reload();await tabs.getByRole('tab',{name:'Billing',exact:true}).waitFor();assert.equal(await tabs.getByRole('tab',{name:'Management',exact:true}).count(),0);assert.equal(await tabs.getByRole('tab',{name:'Settings',exact:true}).count(),0);}
  fs.writeFileSync(evidencePath('/tmp/organization-layout-metrics.json'),JSON.stringify({engine,nativeTab,metrics,writes:writes.map(w=>({path:w.p,fields:Object.keys(w.data)})),errors},null,2));assert.deepEqual(errors,[]);console.log('PASS: Management/Settings split, roles, independent saves, latest company values, failed-save retention, desktop and mobile.');
 }finally{for(const release of pendingLogging)release();await browser.close();server.close()}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
