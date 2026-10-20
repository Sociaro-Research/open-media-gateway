const {evidencePath} = require('./evidence.cjs');
// Real organization UI with local API fixtures; no production requests.
const {chromium}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const name=path.basename(new URL(req.url,'http://fixture').pathname),file=path.join(root,name.includes('.')?name:'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH}),page=await browser.newPage({viewport:{width:1440,height:1000}});
 page.setDefaultTimeout(10000);
 const id='11111111-1111-4111-8111-111111111111',writes=[],errors=[];
 let admin=true,role='admin',fail=false;
 const company={id,name:'Example company',balance_usd:50,credit_limit_usd:100,default_weekly_limit_usd:20,storage_bytes:0,billing_profile:{business_name:'Example Ltd',email:'billing@example.test'}};
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/v1/**',async route=>{
  const request=route.request(),url=new URL(request.url()),p=url.pathname,method=request.method(),json=data=>route.fulfill({json:data});
  if(method==='PUT'){
   const data=request.postDataJSON();writes.push({p,data});
   if(fail)return route.fulfill({status:500,json:{error:{message:'Save failed'}}});
   if(p==='/v1/organizations/'+id)Object.assign(company,data);
   if(p.endsWith('/client-manager'))company.client_manager_email=data.user;
   return route.fulfill({status:204});
  }
  if(p==='/v1/me')return json({id:'tester',name:'Tester',email:'tester@example.test',is_system_admin:admin,is_client_manager:role==='client_manager'});
  if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,shared_resources:true}});
  if(p==='/v1/organizations')return json({data:[{...company,role}]});
  if(p==='/v1/organizations/'+id)return json({...company,role});
  if(p.endsWith('/storage'))return json({settings:{driver:'gateway'},can_manage:true,gateway_allowance_bytes:10000000000});
  if(p.endsWith('/model-access'))return json({data:[],model_ids:[],version:1});
  if(p.endsWith('/markups'))return json({system:{default_percent:10,exceptions:[],version:1},company:{default_percent:null,exceptions:[],version:1}});
  if(p.endsWith('/limits'))return json({system:{values:{key_generations_minute:60,user_generations_minute:120,key_generations_second:5,user_generations_second:10,key_reads_minute:120,user_reads_minute:240,user_key_creations_minute:10,user_active_keys:20},version:1},organization:{values:{},version:1}});
  return json({data:[],has_more:false});
 });
 const base='http://127.0.0.1:'+server.address().port;
 const tabs=page.locator('.organization-tabs'),form=page.locator('.organization-form:not(.request-limits)');
 const choose=name=>tabs.getByRole('tab',{name,exact:true}).click();
 const heading=name=>page.getByRole('heading',{name,exact:true});
 async function capture(name){for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),name+' overflow '+width);await page.screenshot({path:evidencePath('/tmp/organization-')+name+'-'+width+'.png',fullPage:true});}await page.setViewportSize({width:1440,height:1000});}
 try{
  await page.goto(base+'/organizations');await choose('Management');await heading('File Storage').waitFor();
  assert.equal(await tabs.getByRole('tab',{name:'Settings',exact:true}).count(),1);
  assert.equal(await form.getByLabel('Name',{exact:true}).inputValue(),'Example company');
  assert.equal(await page.getByLabel('Credit Limit (USD)',{exact:true}).count(),0);
  assert.equal(await page.getByLabel('Client manager',{exact:true}).count(),0);
  assert.equal(await page.locator('.organization-model-access,.markup-settings,.request-limits').count(),0);
  await heading('Billing details').waitFor();await capture('management');
  await form.getByLabel('Name',{exact:true}).fill('Renamed company');
  await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();
  assert.deepEqual(writes.at(-1).data,{name:'Renamed company',default_weekly_limit_usd:20});
  await choose('Settings');await heading('Additional models').waitFor();await heading('Markups').waitFor();await heading('Organization request limits').waitFor();assert.equal(await page.locator('.request-limits input').first().isEnabled(),true);
  assert.equal(await form.getByLabel('Name',{exact:true}).count(),0);
  assert.equal(await page.locator('.organization-billing-details,.organization-storage').count(),0);
  await capture('settings');
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
  await choose('Management');assert.equal(await form.getByLabel('Name',{exact:true}).inputValue(),'Changed elsewhere');
  assert.equal(await form.getByLabel('Default weekly limit for new members (USD)',{exact:true}).inputValue(),'35');
  admin=false;await page.reload();await choose('Management');await heading('File Storage').waitFor();
  assert.equal(await tabs.getByRole('tab',{name:'Settings',exact:true}).count(),0);
  assert.equal(await page.locator('.organization-model-access,.markup-settings,.request-limits').count(),0);
  await form.getByLabel('Name',{exact:true}).fill('Company admin edit');await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(company.name,'Company admin edit');
  for(const nextRole of ['member','client_manager']){role=nextRole;await page.reload();await tabs.getByRole('tab',{name:'Billing',exact:true}).waitFor();assert.equal(await tabs.getByRole('tab',{name:'Management',exact:true}).count(),0);assert.equal(await tabs.getByRole('tab',{name:'Settings',exact:true}).count(),0);}
  assert.deepEqual(errors,[]);console.log('PASS: Management/Settings split, roles, independent saves, latest company values, failed-save retention, desktop and mobile.');
 }finally{await browser.close();server.close()}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
