const {evidencePath} = require('./evidence.cjs');
// Isolated Reports component checks: filters, columns, roles and stale requests.
const {chromium}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{let file=path.join(root,path.basename(req.url.split('?')[0]));if(!path.extname(file))file=path.join(root,'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH});
 const page=await browser.newPage({viewport:{width:1440,height:1050},timezoneId:'Asia/Nicosia'}),errors=[],requests=[];page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(10000);
 const ids=['11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','33333333-3333-4333-8333-333333333333'];let role='system',delay=false,multiplePages=false,failSave=false;const saved={};
 const organizations=()=>ids.slice(role==='manager'?1:0,role==='manager'?2:role==='system'?3:2).map((id,i)=>({id,name:'Organization '+id[0],role:role==='manager'?'client_manager':'admin'}));
 await page.route('**/v1/**',async route=>{
  const url=new URL(route.request().url()),p=url.pathname,json=data=>route.fulfill({json:data});
  if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,shared_resources:true,version:1}});
  if(p==='/v1/me'){
   if(route.request().method()==='PATCH'){if(failSave)return route.fulfill({status:500,json:{error:{message:'Could not save columns'}}});saved[role]=route.request().postDataJSON().report_columns;}
   return json({id:'tester-'+role,name:'Report tester',email:'tester@example.test',report_columns:saved[role],is_system_admin:role==='system',is_client_manager:role==='manager'});
  }
  if(p==='/v1/organizations'){return json({data:organizations()});}
  if(p.endsWith('/reports')){
   requests.push(url);if(url.searchParams.get('format')==='csv')return route.fulfill({contentType:'text/csv',body:'Date (UTC),Cost (USD)\r\n2026-09-09T00:00:00Z,0\r\n'});const selected=p==='/v1/system/reports'?null:p.split('/')[3];
   if(delay&&selected===ids[0])await new Promise(r=>setTimeout(r,350));
   const rows=(selected?[selected]:ids).map(id=>({id,organization_name:'Organization '+id[0],user_email:'member@example.test',api_key_id:'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaabcd',api_key_name:'Key '+id[0],model:'mock-image',provider:'mock',status:'succeeded',created_at:'2026-09-09T00:00:00Z',cost:{expected_usd:0,metered_usd:0,tariff_usd:0,actual_usd:0,actual_source:'free_mock'}}));
   return json({data:url.searchParams.get('generation_id')?rows.filter(row=>row.id===url.searchParams.get('generation_id')):rows,has_more:multiplePages&&!Number(url.searchParams.get('offset'))});
  }
  if(p==='/v1/api-keys')return json({data:[]});
  return json({data:[]});
 });
 const base='http://127.0.0.1:'+server.address().port;
 const showReports=async()=>{await page.waitForFunction(()=>consoleInitialized&&consoleAuthenticated&&Workspace.ready()&&document.querySelector('#userName')?.textContent==='Report tester');await page.evaluate(async()=>{currentConsoleSection='reports';GatewayOrganizations.close();document.querySelector('#organizationsContent').replaceChildren();document.querySelector('#profileView').hidden=true;document.querySelector('#generationView').hidden=true;document.querySelector('#configurationView').hidden=false;document.querySelector('#organizationsView').hidden=true;document.querySelector('#reportsView').hidden=false;await GatewayReports.open();});};
 const ready=()=>page.waitForFunction(()=>document.querySelector('#reportsContent [role=status]')?.textContent===''&&!!document.querySelector('#reportsContent tbody tr'));
 try{
  await page.goto(base+'/profile');await showReports();await ready();
  assert.deepEqual(await page.locator('thead th:not(.report-columns-cell)').allTextContents(),['Organization','Date','API key','Model','Status','Profit status','Profit','Tokens','Cost']);
  assert.match(await page.locator('tbody').innerText(),/\*abcd/);assert.doesNotMatch(await page.locator('tbody').innerText(),/aaaaaaaa-aaaa/);
  assert.equal(await page.locator('thead th:last-child button[aria-label=Columns]').count(),1);assert.equal(await page.locator('.report-column-controls').count(),0);
  const openColumns=()=>page.getByRole('button',{name:'Columns',exact:true}).click();
  const toggle=async name=>{await page.getByRole('switch',{name,exact:true}).click();await page.waitForFunction(()=>!document.querySelector('#reportColumns').disabled);};
  await page.screenshot({path:evidencePath('/tmp/gateway-report-basic-1440.png'),fullPage:true});
  await page.getByRole('button',{name:'Columns',exact:true}).focus();await page.keyboard.press('Enter');await page.getByRole('dialog',{name:'Columns',exact:true}).waitFor();await page.keyboard.press('Escape');
  await openColumns();assert.equal(await page.getByRole('switch').count(),16);
  await page.getByRole('switch',{name:'Generation ID',exact:true}).focus();await page.keyboard.press('Space');await page.waitForFunction(()=>!document.querySelector('#reportColumns').disabled);
  assert.equal(await page.getByRole('switch',{name:'Generation ID',exact:true}).evaluate(el=>el===document.activeElement),true);await page.keyboard.press('ArrowDown');assert.equal(await page.getByRole('switch',{name:'User',exact:true}).evaluate(el=>el===document.activeElement),true);await page.keyboard.press('Tab');assert.equal(await page.getByRole('switch',{name:'API key',exact:true}).evaluate(el=>el===document.activeElement),true);
  assert.equal(await page.getByRole('dialog',{name:'Columns',exact:true}).isVisible(),true);assert.ok((await page.locator('thead th:not(.report-columns-cell)').allTextContents()).includes('Generation ID'));
  await page.keyboard.press('Escape');assert.equal(await page.getByRole('button',{name:'Columns',exact:true}).evaluate(el=>el===document.activeElement),true);
  await page.reload();await showReports();await ready();assert.ok((await page.locator('thead th:not(.report-columns-cell)').allTextContents()).includes('Generation ID'));
  await openColumns();failSave=true;await toggle('Model');await page.getByText('Could not save columns',{exact:true}).waitFor();assert.equal(await page.getByRole('switch',{name:'Model',exact:true}).isChecked(),true);assert.ok((await page.locator('thead th:not(.report-columns-cell)').allTextContents()).includes('Model'));failSave=false;
  for(const name of ['Organization','Generation ID','API key','Model','Status','Profit status','Profit','Tokens','Cost'])await toggle(name);
  await toggle('Date');await page.getByText('Keep at least one column visible.',{exact:true}).waitFor();assert.equal(await page.getByRole('switch',{name:'Date',exact:true}).isChecked(),true);assert.deepEqual(saved.system,['date']);
  for(const name of ['Organization','Generation ID','API key','Model','Status','Profit status','Profit','Tokens','Cost'])await toggle(name);
  await toggle('User');await toggle('Provider');await toggle('Estimated cost');await toggle('Confirmed cost');await toggle('Settlement');await toggle('Customer charge');
  for(const width of [1440,390,320]){await page.setViewportSize({width,height:1050});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));const gear=await page.getByRole('button',{name:'Columns',exact:true}).boundingBox();assert.ok(gear.x>=0&&gear.x+gear.width<=width,'gear left the visible table edge');const box=await page.getByRole('dialog',{name:'Columns',exact:true}).boundingBox();assert.ok(box.x>=0&&box.x+box.width<=width);await page.screenshot({path:evidencePath(`/tmp/gateway-report-columns-${width}.png`),fullPage:true});}
  await page.keyboard.press('Escape');await page.setViewportSize({width:1440,height:1050});
  await page.getByLabel('Generation ID',{exact:true}).fill(ids[1]);await page.getByRole('button',{name:'Apply',exact:true}).click();await ready();assert.equal(await page.locator('tbody tr').count(),1);assert.equal(requests.at(-1).searchParams.get('generation_id'),ids[1]);
  const filteredCSV=page.waitForEvent('download');await page.getByRole('button',{name:'Download CSV',exact:true}).click();await filteredCSV;assert.equal(requests.at(-1).searchParams.get('generation_id'),ids[1]);
  await page.getByLabel('Generation ID',{exact:true}).fill('44444444-4444-4444-8444-444444444444');await page.getByRole('button',{name:'Apply',exact:true}).click();await page.getByText('No generations match these filters.',{exact:true}).waitFor();
  await page.getByLabel('Generation ID',{exact:true}).fill('');await page.getByRole('button',{name:'Apply',exact:true}).click();await ready();
  assert.equal(await page.locator('.report-pagination').isVisible(),false);
  multiplePages=true;await page.getByRole('button',{name:'Apply',exact:true}).click();await ready();assert.equal(await page.locator('.report-pagination').isVisible(),true);await page.getByRole('button',{name:'Next',exact:true}).click();await ready();assert.equal(await page.locator('[data-page]').innerText(),'Page 2');assert.equal(await page.getByRole('button',{name:'Previous',exact:true}).isEnabled(),true);multiplePages=false;await page.getByRole('button',{name:'Apply',exact:true}).click();await ready();assert.equal(await page.locator('.report-pagination').isVisible(),false);
  assert.equal(await page.locator('#reportOrganization option').count(),4);assert.equal(await page.locator('tbody tr').count(),3);
  await page.locator('#reportFrom').fill('2026-08-01');await page.locator('#reportOrganization').selectOption(ids[1],{force:true});await ready();
  assert.equal(await page.locator('tbody tr').count(),1);assert.match(await page.locator('tbody').innerText(),/Key 2/);assert.equal(requests.at(-1).pathname,'/v1/organizations/'+ids[1]+'/reports');assert.equal(await page.locator('#reportFrom').inputValue(),'2026-08-01');
  await page.getByLabel('User ID or email').fill('person@example.test');await page.getByRole('button',{name:'Apply',exact:true}).click();await ready();assert.equal(requests.at(-1).searchParams.get('user'),'person@example.test');assert.equal(requests.at(-1).searchParams.get('timezone'),'Asia/Nicosia');const downloading=page.waitForEvent('download');await page.getByRole('button',{name:'Download CSV',exact:true}).click();const download=await downloading;assert.equal(download.suggestedFilename(),'generation-report.csv');assert.equal(requests.at(-1).searchParams.get('user'),'person@example.test');
  await page.locator('#reportOrganization').selectOption('',{force:true});await ready();assert.equal(await page.locator('tbody tr').count(),3);
  delay=true;await page.locator('#reportOrganization').selectOption(ids[0],{force:true});await page.locator('#reportOrganization').selectOption(ids[1],{force:true});await ready();await page.waitForTimeout(450);assert.match(await page.locator('tbody').innerText(),/Key 2/);assert.doesNotMatch(await page.locator('tbody').innerText(),/Key 1/);delay=false;
  for(const nextRole of ['admin','manager']){
   role=nextRole;await page.reload();await showReports();await ready();const options=await page.locator('#reportOrganization option').evaluateAll(options=>options.map(o=>o.value));assert.deepEqual(options,organizations().map(o=>o.id));assert.equal(await page.locator('tbody tr').count(),1);assert.equal(await page.getByRole('dialog').count(),0);assert.equal(await page.locator('#reportColumns option[value=estimated_cost]').count(),0);assert.deepEqual(await page.locator('thead th:not(.report-columns-cell)').allTextContents(),(role==='manager'?['Date','API key','Model','Status','Profit status','Profit','Tokens','Cost']:['Date','API key','Model','Status','Tokens','Cost']));
   if(role==='admin'){await page.waitForFunction(()=>document.querySelector('#reportKey').closest('.floating-field')?.classList.contains('raised'));const field=await page.locator('#reportFrom').boundingBox(),apply=await page.getByRole('button',{name:'Apply',exact:true}).boundingBox();assert.equal(Math.round(field.height),Math.round(apply.height));await page.locator('#reportOrganization').selectOption(ids[1],{force:true});await ready();assert.match(await page.locator('tbody').innerText(),/Key 2/);}
  }
  fs.mkdirSync(evidencePath('/tmp/gateway-report-organizations'),{recursive:true});for(const width of [1440,390]){await page.setViewportSize({width,height:1050});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:evidencePath(`/tmp/gateway-report-organizations/${width}.png`),fullPage:true});}
  assert.deepEqual(errors,[]);console.log('PASS: key suffix, profile column persistence and account isolation, failed saves, generation filter/CSV, system/all, organization admin, client manager, dates, stale responses, keyboard and desktop/mobile.');
 }catch(error){console.error('Browser errors:',errors);await page.screenshot({path:evidencePath('/tmp/gateway-reports-failure.png'),fullPage:true});throw error;}finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
