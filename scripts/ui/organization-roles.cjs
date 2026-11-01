const {generationFixtureResponse}=require('./generation-page-fixture.cjs');
const {evidencePath} = require('./evidence.cjs');
// Organization console fixtures: no provider calls or production data.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');const server=http.createServer((req,res)=>{let file=path.join(root,path.basename(req.url.split('?')[0]));if(!path.extname(file))file=path.join(root,'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await (process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium).launch({headless:true,executablePath:process.env.UI_BROWSER_PATH}),page=await browser.newPage({viewport:{width:1440,height:1080},hasTouch:true}),errors=[],writes=[];page.on('pageerror',e=>{if(!e.message.includes('ResizeObserver loop completed with undelivered notifications'))errors.push(e.message)});page.setDefaultTimeout(10000);let storageFixture={driver:'gateway'};let clientManagerFixture=false,assignedManager='',admin=true,role='supermanager',balance=100,accepted=false,invitationRows=[],failInvitation=false,previewRole='member',firstOwnerPreview=false;
const oid='22222222-2222-4222-8222-222222222222',uid='11111111-1111-4111-8111-111111111111';const organization=()=>({id:oid,name:'Example team',role,balance_usd:balance,available_usd:balance,credit_limit_usd:0,default_weekly_limit_usd:0,weekly_limit_usd:20,weekly_committed_usd:5,storage_bytes:2048,client_manager_email:assignedManager});
 let loginMethods={email:true,google:false,email_delivery:true},loginReadFailure=false,loginReads=0;
 await page.route('**/auth/methods',async route=>{loginReads++;return loginReadFailure?route.fulfill({status:503,json:{error:{message:'Configuration unavailable'}}}):route.fulfill({json:loginMethods});});
await page.route('**/v1/**',async route=>{const req=route.request(),p=new URL(req.url()).pathname,method=req.method(),data=req.postData()?req.postDataJSON():null,json=value=>route.fulfill({json:generationFixtureResponse(route.request(),value)});if(method!=='GET')writes.push({p,method,data});
if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,shared_resources:true,version:1}});
if(p==='/v1/me')return json({id:uid,name:'Organization tester',email:'tester@example.test',is_system_admin:admin,is_client_manager:clientManagerFixture,session_id:'fixture'});
if(p==='/v1/organizations')return json(method==='POST'?organization():{data:[organization()]});
if(p==='/v1/organizations/'+oid)return method==='PUT'?route.fulfill({status:204}):json(organization());
if(p.endsWith('/storage/check'))return json({status:'ok'});
if(p.endsWith('/storage')){if(method==='PUT'){storageFixture={...data,secret_access_key:'',credentials_configured:true};return json(storageFixture)}return json({settings:storageFixture,can_manage:true,gateway_allowance_bytes:10000000000});}
if(p.endsWith('/client-manager')){assignedManager=data.user;return route.fulfill({status:204})}
if(p.endsWith('/members'))return json({data:[{id:uid,email:'tester@example.test',name:'Tester',role,weekly_limit_usd:20,weekly_committed_usd:5},{id:'33333333-3333-4333-8333-333333333333',email:'member@example.test',name:'Recipient',role:'member',weekly_limit_usd:20},{id:'44444444-4444-4444-8444-444444444444',email:'officer@example.test',role:'content_officer'}]});
if(p.includes('/members/'))return route.fulfill({status:204});
if(p.endsWith('/credits')){if(method==='POST'){balance+=data.amount_usd;return json(data)}return json({data:[]})}
if(p.endsWith('/invitations')){
 if(method==='POST'){
  if(failInvitation)return route.fulfill({status:503,json:{error:{message:'Invitation fixture unavailable'}}});
  const invite={id:'invite-id',token:'x'.repeat(43),role:data.role,expires_at:new Date(Date.now()+7*86400000).toISOString()};invitationRows=[invite];return json(invite);
 }
 return json({data:invitationRows});
}
if(p.startsWith('/v1/invitations/')){if(p.endsWith('/accept'))accepted=true;return json({organization_name:'Example team',organization_id:oid,role:previewRole,first_owner:firstOwnerPreview});}
if(p.endsWith('/generations'))return json({data:[{id:'generation-fixture',actor_id:uid,created_at:Math.floor(Date.now()/1000),status:'completed',media_type:'image',model:'mock/mock-image-v1',prompt:role==='admin'?undefined:'Retained prompt',content_access:role!=='admin',can_delete:['supermanager','developer','member'].includes(role),cost:{customer_expected_usd:0}}]});
if(p.endsWith('/reports'))return json({data:[],has_more:false});
if(p==='/v1/api-keys')return method==='POST'?json({id:'key',secret:'fixture-key',...data}):json({data:[]});
if(p==='/v1/api-keys/usage')return json({month:'2026-09',data:[]});
if(p==='/v1/api-keys/billing')return json({data:[]});
if(p==='/v1/key')return json({key:{name:'Test key',organization_id:oid,resource_mode:'shared',storage_mode:'shared'},organization:organization(),usage:{month:'2026-09',consumed_usd:0}});
if(p==='/v1/providers'||(p==='/v1/generations' && route.request().method()==='GET'))return json({data:[]});
if(p==='/v1/storage/usage')return json({used_bytes:2048,limit_bytes:null,applies:true});
return json({});});
async function capture(name){fs.mkdirSync(evidencePath('/tmp/gateway-role-layout'),{recursive:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),name+' overflow');await page.screenshot({path:evidencePath('/tmp/gateway-role-layout/')+name+'.png',fullPage:true});}
try{const base='http://127.0.0.1:'+server.address().port;
 await page.goto(base+'/organizations');await page.getByRole('tab',{name:'Members',exact:true}).waitFor();
 const info=page.getByRole('button',{name:'Organization role permissions',exact:true});
 await info.scrollIntoViewIfNeeded();const infoBox=await info.boundingBox();await page.mouse.move(infoBox.x+infoBox.width/2,infoBox.y+infoBox.height/2);await page.getByRole('dialog',{name:'Organization role permissions',exact:true}).waitFor();
 assert.equal(await page.locator('.role-permissions-popup th').count(),6);
 await page.mouse.move(0,0);await page.keyboard.press('Escape');await page.locator('.role-permissions-popup').waitFor({state:'detached'});assert.equal(await info.evaluate(e=>e===document.activeElement),true);
 await info.focus();await page.keyboard.press('Enter');await page.getByRole('dialog',{name:'Organization role permissions',exact:true}).waitFor();await capture('role-permissions-desktop');await page.keyboard.press('Escape');
 for(const width of [390,320]){
  await page.setViewportSize({width,height:850});await info.scrollIntoViewIfNeeded();await page.mouse.move(0,0);await page.keyboard.press('Escape');await page.locator('.role-permissions-popup').waitFor({state:'detached'});const box=await info.boundingBox();await page.touchscreen.tap(box.x+box.width/2,box.y+box.height/2);await page.getByRole('dialog',{name:'Organization role permissions',exact:true}).waitFor();
  assert.ok(await page.locator('.role-permissions-popup').evaluate(e=>{const r=e.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth&&r.top>=0&&r.bottom<=innerHeight}));
  assert.ok(await page.locator('.role-permissions-popup .docs-table-wrap').evaluate(e=>e.scrollWidth>e.clientWidth));await capture('role-permissions-'+width);const dismiss=page.locator('.role-permissions-popup .model-info-close');assert.equal(await dismiss.getAttribute('aria-label'),'Close organization role permissions');const closeBox=await dismiss.boundingBox();await page.touchscreen.tap(closeBox.x+closeBox.width/2,closeBox.y+closeBox.height/2);await page.locator('.role-permissions-popup').waitFor({state:'detached'});
 }
 await page.setViewportSize({width:1440,height:1080});
 await page.getByRole('tab',{name:'Members',exact:true}).click();
 const invitationForm=page.locator('.member-invitations form'),invitationRole=invitationForm.locator('select'),roleTrigger=invitationForm.getByRole('combobox',{name:'Invitation role',exact:true});
 await invitationRole.waitFor({state:'attached'});
 assert.deepEqual(await invitationRole.locator('option').evaluateAll(o=>o.map(n=>n.value)),['supermanager','admin','content_officer','developer','member']);
 assert.equal(await invitationRole.inputValue(),'member');
 // Choose Developer with the shared picker using only the keyboard.
 await roleTrigger.scrollIntoViewIfNeeded();await roleTrigger.press('ArrowDown');await page.getByRole('listbox',{name:'Invitation role',exact:true}).waitFor();await page.waitForFunction(()=>document.activeElement?.getAttribute('role')==='listbox');await page.keyboard.press('Home');for(let i=0;i<3;i++)await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter');assert.equal(await invitationRole.inputValue(),'developer');
 failInvitation=true;await invitationForm.getByRole('button',{name:'Create invitation',exact:true}).click();await invitationForm.getByRole('status').filter({hasText:'Invitation fixture unavailable'}).waitFor();assert.equal(await invitationRole.inputValue(),'developer');failInvitation=false;
 await invitationForm.getByRole('button',{name:'Create invitation',exact:true}).focus();await page.keyboard.press('Enter');await page.getByLabel('One-time invitation link',{exact:true}).waitFor();assert.equal(writes.filter(w=>w.p.endsWith('/invitations')).at(-1).data.role,'developer');
 const feedback=invitationForm.getByRole('status');
 for(const scenario of [
  {name:'none',methods:{email:false,google:false,email_delivery:true},warning:true},
  {name:'email_without_delivery',methods:{email:false,google:false,email_delivery:false,email_confirmation:true},warning:true},
  {name:'email_available',methods:{email:true,google:false,email_delivery:true}},
  {name:'legacy_email_without_delivery',methods:{email:true,google:false,email_delivery:false},warning:true},
  {name:'missing_email_readiness',methods:{email:true,google:false},unknown:true},
  {name:'google',methods:{email:false,google:true,email_delivery:false}},
  {name:'both',methods:{email:true,google:true}},
  {name:'read_failure',methods:{email:false,google:false},failure:true,unknown:true},
  {name:'invalid_response',methods:{},unknown:true}
 ]){
  loginMethods=scenario.methods;loginReadFailure=!!scenario.failure;const reads=loginReads;
  await invitationForm.getByRole('button',{name:'Create invitation',exact:true}).click();
  await page.waitForFunction(()=>!document.querySelector('.member-invitations form button[type=submit]').disabled);
  assert.equal(loginReads,reads+1,'fresh login config after '+scenario.name);
  const message=await feedback.textContent();assert.ok(message.startsWith('Invitation created.'));
  assert.equal(message.includes('No sign-in method is currently available.'),!!scenario.warning,scenario.name);
  assert.equal(message.includes('Could not check sign-in availability.'),!!scenario.unknown,scenario.name);
  assert.ok(await page.getByLabel('One-time invitation link',{exact:true}).isVisible());
 }
 loginReadFailure=false;loginMethods={email:false,google:false};failInvitation=true;const readsBeforeFailure=loginReads;
 await invitationForm.getByRole('button',{name:'Create invitation',exact:true}).click();await feedback.filter({hasText:'Invitation fixture unavailable'}).waitFor();
 assert.equal(loginReads,readsBeforeFailure,'failed creation must not check login configuration');assert.ok(!(await feedback.textContent()).includes('No sign-in method'));
 failInvitation=false;await invitationForm.getByRole('button',{name:'Create invitation',exact:true}).click();await feedback.filter({hasText:'No sign-in method is currently available'}).waitFor();
 await page.locator('.member-invitations .docs-table tbody tr').waitFor();assert.match(await page.locator('.member-invitations .docs-table tbody tr').innerText(),/Developer/);assert.equal(await page.getByText('Invited role: Developer',{exact:true}).count(),1);
 for(const theme of ['dark','light']){await page.evaluate(theme=>GatewayTheme.apply(theme),theme);for(const width of [1440,390,320]){
  await page.setViewportSize({width,height:950});await invitationForm.scrollIntoViewIfNeeded();await capture('invitation-role-'+(process.env.UI_BROWSER_ENGINE||'chromium')+'-'+width);await page.locator('.member-invitations').screenshot({path:evidencePath('/tmp/gateway-role-layout/invitation-form-')+(process.env.UI_BROWSER_ENGINE||'chromium')+'-'+theme+'-'+width+'.png'});
  await roleTrigger.click();await page.getByRole('listbox',{name:'Invitation role',exact:true}).waitFor();await capture('invitation-role-picker-'+(process.env.UI_BROWSER_ENGINE||'chromium')+'-'+width);await page.keyboard.press('Escape');
 }}
 await page.setViewportSize({width:1440,height:1080});
 admin=false;role='admin';await page.goto(base+'/organizations');await page.getByRole('tab',{name:'Members',exact:true}).click();await invitationRole.waitFor({state:'attached'});assert.deepEqual(await invitationRole.locator('option').evaluateAll(o=>o.map(n=>n.value)),['developer','member']);
 role='supermanager';await page.goto(base+'/organizations');await page.getByRole('tab',{name:'Members',exact:true}).click();await invitationRole.waitFor({state:'attached'});assert.equal(await invitationRole.locator('option').count(),5);
 admin=false;role='developer';await page.goto(base+'/organizations');await page.getByRole('tab',{name:'API keys',exact:true}).click();await page.locator('#newKeyUser').waitFor({state:'attached'});
 assert.deepEqual(await page.locator('#newKeyUser option').evaluateAll(options=>options.map(o=>o.value)),[uid,'33333333-3333-4333-8333-333333333333']);
 await page.locator('#newKeyUser').selectOption('33333333-3333-4333-8333-333333333333',{force:true});await page.locator('#newKeyName').fill('Member working key');await page.getByRole('button',{name:'Create',exact:true}).click();await page.getByRole('heading',{name:'Save your key now',exact:true}).waitFor();
 assert.ok(writes.some(w=>w.p==='/v1/api-keys'&&w.data.user_id==='33333333-3333-4333-8333-333333333333'));await capture('assigned-member-key');
 role='admin';await page.goto(base+'/organizations');await page.getByRole('tab',{name:'Requests',exact:true}).click();await page.getByText('Content restricted',{exact:true}).waitFor();assert.equal(await page.getByText('Retained prompt',{exact:true}).count(),0);
 role='content_officer';await page.goto(base+'/organizations');await page.getByText('View request',{exact:true}).click();await page.getByText('Retained prompt',{exact:true}).waitFor();assert.equal(await page.getByRole('button',{name:'Delete files',exact:true}).count(),0);assert.equal(await page.getByRole('tab',{name:'Management',exact:true}).count(),0);assert.equal(await page.getByRole('tab',{name:'API keys',exact:true}).count(),0);await capture('content-officer-journal');
 role='member';await page.goto(base+'/organizations');await page.getByText('View request',{exact:true}).waitFor();assert.equal(await page.getByRole('tab',{name:'API keys',exact:true}).count(),0);assert.equal(await page.getByRole('tab',{name:'Members',exact:true}).count(),0);
 previewRole='supermanager';await page.goto(base+'/organizations#invite='+('s'.repeat(43)));await page.getByText('You will join with the Supermanager role. Your other organizations are unchanged.',{exact:true}).waitFor();assert.equal(await page.getByText('You will become the first Supermanager and can manage company members and API keys.',{exact:true}).count(),0);
 await page.getByRole('button',{name:'Accept invitation',exact:true}).click();await page.waitForFunction(()=>!sessionStorage.getItem('gateway-pending-invite'));assert.deepEqual(Object.keys(writes.filter(w=>w.p==='/v1/invitations/accept').at(-1).data),['token']);
 firstOwnerPreview=true;await page.goto(base+'/organizations?owner=1#invite='+('o'.repeat(43)));await page.getByText('You will become the first Supermanager and can manage company members and API keys.',{exact:true}).waitFor();
 console.log('PASS: invitation role picker, keyboard creation, failure/retry, history, role restrictions, recipient role display and first-owner distinction in '+(process.env.UI_BROWSER_ENGINE||'chromium'));
 assert.deepEqual(errors,[]);console.log('PASS: role permissions hover/touch/keyboard at 1440/390/320 px; delegated recipient selection; Manager redaction; Content Officer read-only journal; Member controls');
}catch(e){console.error(errors);await page.screenshot({path:evidencePath('/tmp/gateway-role-ui-failure.png')});throw e}finally{await browser.close();server.close()}})().catch(e=>{console.error(e);process.exitCode=1;server.close()});
