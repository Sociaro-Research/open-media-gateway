const {evidencePath} = require('./evidence.cjs');
// Organization console fixtures: no provider calls or production data.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');const server=http.createServer((req,res)=>{let file=path.join(root,req.url.split('?')[0].replace(/^\/ui\//,''));if(!path.extname(file))file=path.join(root,'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await (process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium).launch({headless:true,executablePath:process.env.UI_BROWSER_PATH}),page=await browser.newPage({viewport:{width:1440,height:1080}}),errors=[],writes=[];page.on('pageerror',e=>{if(e.message==='ResizeObserver loop completed with undelivered notifications.')return;errors.push(e.message)});page.setDefaultTimeout(10000);let storageFixture={driver:'gateway'};let clientManagerFixture=false,assignedManager='',admin=true,role='admin',balance=100,accepted=false;
const oid='22222222-2222-4222-8222-222222222222',uid='11111111-1111-4111-8111-111111111111';const organization=()=>({id:oid,name:'Example team',role,balance_usd:balance,available_usd:balance,credit_limit_usd:0,default_weekly_limit_usd:0,weekly_limit_usd:20,weekly_committed_usd:5,storage_bytes:2048,client_manager_email:assignedManager});
let policy={system:{default_percent:20,exceptions:[],version:1},company:{default_percent:null,exceptions:[],version:1}},rejectSave=false;
await page.route('**/auth/email/registration',route=>route.fulfill({json:{enabled:false}}));
await page.route('**/v1/**',async route=>{const req=route.request(),p=new URL(req.url()).pathname,method=req.method(),data=req.postData()?req.postDataJSON():null,json=value=>route.fulfill({json:value});if(method!=='GET')writes.push({p,method,data});
if(p.endsWith('/markups/catalog'))return json({data:[{id:'openai',name:'OpenAI',models:[{id:'gpt-image-1',name:'GPT Image 1',media_type:'image',metadata:{release_date:'2026-01-02'}},{id:'gpt-image-2',name:'GPT Image 2',media_type:'image'}]},{id:'bytedance',name:'BytePlus',models:[{id:'seedream',name:'Seedream',media_type:'image'}]}]});
if(p.endsWith('/markups')){if(method==='PUT'){if(rejectSave)return route.fulfill({status:409,json:{error:{message:'Settings changed'}}});const scope=p.startsWith('/v1/system/')?'system':'company';policy[scope]={...data,version:policy[scope].version+1}}return json(policy)}
if(p==='/v1/system/settings')return json({generation_ui:true,personal_providers:true,shared_resources:true,available_providers:[],version:policy.system.version});
if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,shared_resources:true,version:1}});
if(p==='/v1/me')return json({id:uid,name:'Organization tester',email:'tester@example.test',report_columns:['date','api_key','model','status','tokens','cost','customer_charge'],is_system_admin:admin,is_client_manager:clientManagerFixture,session_id:'fixture'});
if(p==='/v1/organizations')return json(method==='POST'?organization():{data:[organization()]});
if(p==='/v1/organizations/'+oid)return method==='PUT'?route.fulfill({status:204}):json(organization());
if(p.endsWith('/storage/check'))return json({status:'ok'});
if(p.endsWith('/storage')){if(method==='PUT'){storageFixture={...data,secret_access_key:'',credentials_configured:true};return json(storageFixture)}return json({settings:storageFixture,gateway_allowance_bytes:10000000000});}
if(p.endsWith('/client-manager')){assignedManager=data.user;return route.fulfill({status:204})}
if(p.endsWith('/members'))return json({data:[{id:uid,email:'tester@example.test',name:'Tester',role:'admin',weekly_limit_usd:20,weekly_committed_usd:5}]});
if(p.includes('/members/'))return route.fulfill({status:204});
if(p.endsWith('/credits')){if(method==='POST'){balance+=data.amount_usd;return json(data)}return json({data:[]})}
if(p.endsWith('/invitations'))return method==='POST'?json({id:'invite-id',token:'x'.repeat(43),expires_at:new Date(Date.now()+86400000).toISOString()}):json({data:[{id:'invite-id',expires_at:new Date(Date.now()+86400000).toISOString()}]});
if(p.startsWith('/v1/invitations/')){if(p.endsWith('/accept'))accepted=true;return json({organization_name:'Example team',organization_id:oid});}
if(p.endsWith('/reports'))return json({data:[{id:'billed-task',api_key_name:'Example key',created_at:'2026-09-12T00:00:00Z',status:'succeeded',model:'mock-image-v1',provider:'mock',consumed_usd:1.25,cost:{expected_usd:1,metered_usd:1,tariff_usd:1,actual_usd:1,markup_percent:25}}],has_more:false});
if(p==='/v1/api-keys')return method==='POST'?json({id:'key',secret:'fixture-key',...data}):json({data:[{id:'key',name:'Fixture key',scopes:['generation'],prefix:'gw_fixture',created_at:'2026-09-01T00:00:00Z',resource_mode:'personal'}]});
if(p==='/v1/api-keys/usage')return json({month:'2026-09',data:[]});
if(p==='/v1/api-keys/billing')return json({data:[]});
if(p==='/v1/key')return json({key:{name:'Test key',organization_id:oid,resource_mode:'shared',storage_mode:'shared'},organization:organization(),usage:{month:'2026-09',consumed_usd:0}});
if(p==='/v1/providers'||(p==='/v1/generations' && route.request().method()==='GET'))return json({data:[]});
if(p==='/v1/storage/usage')return json({used_bytes:2048,limit_bytes:null,applies:true});
return json({});});
async function capture(name){fs.mkdirSync(evidencePath('/tmp/gateway-layout'),{recursive:true});for(const width of [1440,390,320]){await page.setViewportSize({width,height:1080});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));const overflow=await page.evaluate(()=>[...document.querySelectorAll('body *')].filter(n=>{const b=n.getBoundingClientRect();return b.width&&b.right>innerWidth+1}).map(n=>({tag:n.tagName,cls:n.className,right:n.getBoundingClientRect().right})));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),name+' overflow at '+width+' '+JSON.stringify(overflow));const tableStyles=await page.locator('.docs-table:visible').evaluateAll(tables=>{const probe=document.createElement('span');probe.style.backgroundColor='var(--table-header)';document.body.append(probe);const results=tables.map(t=>({header:[...t.querySelectorAll('th')].every(n=>getComputedStyle(n).backgroundColor===getComputedStyle(probe).backgroundColor),borders:[...t.querySelectorAll('th,td')].every(n=>getComputedStyle(n).borderBottomWidth==='0px')}));probe.remove();return results;});assert.ok(tableStyles.every(t=>t.header&&t.borders),name+' shared table styles');await page.screenshot({path:evidencePath('/tmp/gateway-layout/')+name+'-'+width+'.png',fullPage:true});}await page.setViewportSize({width:1440,height:1080});}
try {
 const base='http://127.0.0.1:'+server.address().port;
 await page.goto(base+'/system');
 await page.locator('[data-system-page=organizations]').click();
 const orgRow=page.locator('.organization-row'),manage=orgRow.getByRole('button',{name:'Manage',exact:true});
 await orgRow.waitFor();await page.mouse.move(0,0);
 const opacity=locator=>locator.evaluate(n=>getComputedStyle(n).opacity);
 assert.equal(await opacity(manage),'0');
 const orgSize=await orgRow.boundingBox();await orgRow.hover();assert.equal(await opacity(manage),'1');
 assert.notEqual(await orgRow.evaluate(n=>getComputedStyle(n).backgroundColor),'rgba(0, 0, 0, 0)');
 assert.deepEqual(await orgRow.boundingBox(),orgSize,'hover does not change row geometry');
 await page.mouse.move(0,0);await orgRow.focus();assert.equal(await opacity(manage),'1','keyboard reveals Manage');
 await orgRow.press('Enter');
 const invitationRow=page.locator('tr').filter({has:page.getByRole('button',{name:'Revoke',exact:true})});
 await invitationRow.waitFor();await page.mouse.move(0,0);assert.equal(await opacity(invitationRow.getByRole('button')),'0');
 await invitationRow.hover();assert.equal(await opacity(invitationRow.getByRole('button')),'1');
 assert.equal(await invitationRow.evaluate(n=>getComputedStyle(n).backgroundColor),'rgba(0, 0, 0, 0)');
 await page.locator('.organization-tabs').getByRole('tab',{name:'Settings',exact:true}).click();
 const settings=page.locator('.organization-form');
 const markups=page.locator('.markup-settings');
 await markups.getByLabel('Organization markup (%)',{exact:true}).waitFor();
 assert.equal(await markups.getByLabel('Organization markup (%)',{exact:true}).inputValue(),'');
 for(const [value,expected] of [['12.34',12.34],['0',0],['',null]]) {
  await markups.getByLabel('Organization markup (%)',{exact:true}).fill(value);
  await markups.getByRole('button',{name:'Save',exact:true}).click();
  await markups.getByRole('button',{name:'Saved',exact:true}).waitFor();
  assert.equal(writes.filter(w=>w.p.endsWith('/markups')&&w.method==='PUT').at(-1).data.default_percent,expected);
 }
 await markups.getByRole('button',{name:'Add exception'}).click();
 let rule=markups.locator('.markup-rule').first();
 await rule.getByLabel('Markup (%)',{exact:true}).fill('0');
 await markups.getByRole('button',{name:'Save',exact:true}).click();await markups.getByRole('button',{name:'Saved',exact:true}).waitFor();
 assert.deepEqual(policy.company.exceptions,[{provider:'openai',markup_percent:0}]);
 await markups.getByRole('button',{name:'Add exception'}).click();
 let second=markups.locator('.markup-rule').nth(1);
 await second.getByLabel('Markup (%)',{exact:true}).fill('15');
 assert.equal(await second.locator('select').first().evaluate(n=>n.validity.valid),false,'duplicate provider rule rejected');
 await second.getByRole('combobox',{name:'Model',exact:true}).click();
 await page.getByRole('searchbox',{name:'Search by Name'}).fill('IMAGE-1');
 await page.getByRole('combobox',{name:'Content type',exact:true}).click();await page.getByRole('option',{name:'Image',exact:true}).click();
 await page.locator('.model-select-popup input[type=date]').fill('2026-01-01');
 assert.equal(await page.locator('.model-select-popup').getByRole('option').count(),1);
 await page.getByRole('option',{name:'GPT Image 1',exact:true}).click();
 await markups.getByRole('button',{name:'Save',exact:true}).click();await markups.getByRole('button',{name:'Saved',exact:true}).waitFor();
 assert.deepEqual(policy.company.exceptions[1],{provider:'openai',model:'gpt-image-1',markup_percent:15});
 assert.equal(await second.locator('.markup-effective').count(),0);
 assert.deepEqual(await markups.locator('thead th').allTextContents(),['Model','Markup (%)','']);
 rejectSave=true;await second.getByLabel('Markup (%)',{exact:true}).fill('30');
 await markups.getByRole('button',{name:'Save',exact:true}).click();await markups.getByText('Another administrator changed these settings.',{exact:false}).waitFor();
 assert.equal(await second.getByLabel('Markup (%)',{exact:true}).inputValue(),'30');
 assert.equal(policy.company.exceptions[1].markup_percent,15);
 rejectSave=false;await markups.getByRole('button',{name:'Save',exact:true}).click();await markups.getByRole('button',{name:'Saved',exact:true}).waitFor();
 await markups.getByRole('button',{name:'Saved',exact:true}).waitFor({state:'hidden',timeout:5000});
 await capture('organization-markup');
 await page.getByRole('tab',{name:'Billing',exact:true}).click();await page.locator('.report-table tbody tr').first().waitFor();assert.equal(await page.locator('.report-table thead th:not(.report-columns-cell)').last().innerText(),'Customer charge');assert.equal(await page.locator('.report-table tbody tr').first().locator('td[data-column=customer_charge]').innerText(),'$1.25');await capture('markup-report');
 admin=false;role='admin';await page.goto(base+'/organizations');
 assert.equal(await page.locator('.organization-tabs').getByRole('tab',{name:'Settings',exact:true}).count(),0);
 await page.locator('.organization-tabs').getByRole('tab',{name:'Management',exact:true}).click();
 await settings.getByLabel('Name',{exact:true}).waitFor();
 assert.equal(await page.locator('.markup-settings').count(),0);
 await page.getByRole('heading',{name:'File Storage',exact:true}).waitFor();
 await settings.getByLabel('Name',{exact:true}).fill('Updated team');
 await settings.getByRole('button',{name:'Save',exact:true}).click();
 await settings.getByRole('button',{name:'Saved',exact:true}).waitFor();
 assert.equal(Object.hasOwn(writes.filter(w=>w.p==='/v1/organizations/'+oid&&w.method==='PUT').at(-1).data,'markup_percent'),false);
 admin=true;await page.goto(base+'/system');await page.getByRole('tab',{name:'Markups',exact:true}).click();
 const systemMarkups=page.locator('.markup-settings');await systemMarkups.getByLabel('Default markup (%)',{exact:true}).fill('25');
 await systemMarkups.getByRole('button',{name:'Add exception'}).click();await systemMarkups.locator('.markup-rule').getByLabel('Markup (%)',{exact:true}).fill('0');
 await systemMarkups.getByRole('button',{name:'Save',exact:true}).click();await systemMarkups.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(policy.system.default_percent,25);
 assert.equal(await systemMarkups.getByRole('heading',{name:'Markups',exact:true}).count(),1);
 const header=systemMarkups.locator('.markup-list-heading'),headingBox=await header.getByRole('heading').boundingBox(),addBox=await header.getByRole('button').boundingBox();
 assert.ok(Math.abs(headingBox.y+headingBox.height/2-addBox.y-addBox.height/2)<1,'heading and action align');
 const baseBox=await systemMarkups.locator('.markup-base').boundingBox(),exceptionsBox=await systemMarkups.locator('.markup-exceptions').boundingBox();assert.ok(exceptionsBox.y>=baseBox.y+baseBox.height,'exceptions are below default');
 const cells=systemMarkups.locator('.markup-rule').first();
 const sizes=await Promise.all([cells.getByRole('combobox'),cells.getByRole('spinbutton'),cells.getByRole('button')].map(l=>l.boundingBox()));assert.ok(sizes.every(b=>b.height===44&&b.y===sizes[0].y),'table controls align at 44px');
 await page.getByRole('heading',{name:'Settings',exact:true}).click();await page.mouse.move(0,0);
 const remove=cells.getByRole('button',{name:'Remove exception'});
 assert.equal(await opacity(remove),'0');await cells.hover();assert.equal(await opacity(remove),'1');
 assert.equal(await cells.evaluate(n=>getComputedStyle(n).backgroundColor),'rgba(0, 0, 0, 0)','editable rows have no hover highlight');
 await page.mouse.move(0,0);await cells.getByRole('spinbutton').focus();await page.keyboard.press('Tab');
 assert.equal(await remove.evaluate(n=>n===document.activeElement),true);assert.equal(await opacity(remove),'1','keyboard can reach Remove');
 for(const table of [systemMarkups.locator('table')]){
  assert.equal(await table.locator('th').first().evaluate(n=>getComputedStyle(n).backgroundColor),await page.evaluate(()=>{const probe=document.createElement('span');probe.style.backgroundColor='var(--table-header)';document.body.append(probe);const color=getComputedStyle(probe).backgroundColor;probe.remove();return color;}));
  assert.equal(await table.locator('td').first().evaluate(n=>getComputedStyle(n).borderBottomWidth),'0px');
 }
 await capture('system-markups');
 const touch=await browser.newPage({viewport:{width:390,height:844},hasTouch:true,isMobile:true});
 await touch.goto(base+'/system');
 await touch.evaluate(async()=>{document.body.replaceChildren();await MarkupSettings.mount(document.body,async url=>url.endsWith('/catalog')?{data:[{id:'mock',name:'Mock',models:[]}]}:{system:{default_percent:0,exceptions:[{provider:'mock',markup_percent:10}],version:1}})});
 assert.equal(await touch.getByRole('button',{name:'Remove exception'}).evaluate(n=>getComputedStyle(n).opacity),'1','touch actions stay visible');await touch.close();
 await systemMarkups.locator('.markup-rule').hover();await systemMarkups.getByRole('button',{name:'Remove exception'}).click();await systemMarkups.getByRole('button',{name:'Save',exact:true}).click();await systemMarkups.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(policy.system.exceptions,[]);
 await page.goto(base+'/profile');await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'API keys',exact:true}).click();
 const keyRow=page.locator('tr').filter({has:page.getByRole('button',{name:'Revoke',exact:true})});
 await keyRow.waitFor();await page.mouse.move(0,0);assert.equal(await opacity(keyRow.getByRole('button')),'0');
 await keyRow.hover();assert.equal(await opacity(keyRow.getByRole('button')),'1');
 assert.equal(await keyRow.evaluate(n=>getComputedStyle(n).backgroundColor),'rgba(0, 0, 0, 0)');
 await capture('table-api-keys');
 assert.deepEqual(errors,[]);
 console.log('PASS: organization markup overrides, explicit zero, inheritance, mobile layout, organization-admin restrictions.');
} catch(e) {await page.screenshot({path:evidencePath('/tmp/gateway-markup-ui-failure.png'),fullPage:true});throw e}
finally {await browser.close();server.close()}
})().catch(e=>{console.error(e);process.exitCode=1;server.close()});
