// Applied-filter pagination and CSV contract; localhost fixtures only.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=process.env.UI_ROOT||path.resolve(__dirname,'../../web/public/ui');
const output=process.env.UI_SCREENSHOT_DIR||'/tmp/gateway-report-pagination';
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const id=n=>`11111111-1111-4111-8111-${String(n).padStart(12,'0')}`;
const organization=id(999),user=id(998),key=id(997);
const rows=Array.from({length:123},(_,i)=>({id:id(i+1),organization_name:'Fixture organization',user_id:i<3?user:id(996),user_email:i<3?'few@example.test':'many@example.test',api_key_id:i<3?key:id(995),api_key_name:'Fixture key',model:'mock-image',provider:'mock',status:'succeeded',created_at:i<3?'2026-08-10T12:00:00Z':'2026-09-10T12:00:00Z',cost:{actual_usd:0,actual_source:'free_provider'}}));
const filter=query=>rows.filter(row=>(!query.user||[row.user_id,row.user_email].includes(query.user))&&(!query.generation_id||row.id===query.generation_id)&&(!query.api_key_id||row.api_key_id===query.api_key_id)&&(!query.from||row.created_at.slice(0,10)>=query.from)&&(!query.to||row.created_at.slice(0,10)<=query.to));
const server=http.createServer((req,res)=>{let file=path.join(root,path.basename(req.url.split('?')[0]));if(!path.extname(file))file=path.join(root,'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 fs.mkdirSync(output,{recursive:true});await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await ({chromium,webkit}[engine]).launch({headless:true,executablePath:process.env.UI_BROWSER_PATH});
 const page=await browser.newPage({viewport:{width:1440,height:1050},timezoneId:'Asia/Nicosia'});
 page.setDefaultTimeout(8000);
 const base='http://127.0.0.1:'+server.address().port,requests=[],errors=[],notices=[],results=[];
 let failNext=false,failCSV=false,holdNext=false,release=null;
 page.on('pageerror',error=>{
  // Match the existing shared-control suites' WebKit viewport notice handling.
  if(engine==='webkit'&&error.message==='ResizeObserver loop completed with undelivered notifications.'){notices.push(error.message);console.warn('WebKit resize notice: '+error.message);return;}
  errors.push(error.message);
 });
 await page.route('**/*',async route=>{
  const url=new URL(route.request().url()),p=url.pathname,json=data=>route.fulfill({json:data});
  if(url.origin!==base)throw new Error('External request forbidden: '+url.origin);
  if(!p.startsWith('/v1/'))return route.continue();
  // Automatic managed-key restore is a read-only lookup transported as POST; creation remains forbidden.
  if(p==='/v1/playground-key/restore'){assert.equal(route.request().method(),'POST');const body=route.request().postDataJSON();assert.deepEqual(Object.keys(body),['organization_id']);assert.equal(typeof body.organization_id,'string');return route.fulfill({status:404,json:{error:{message:'No synthetic saved key'}}});}
  assert.equal(route.request().method(),'GET','Fixture allows reads only');
  if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,shared_resources:true,version:1}});
  if(p==='/v1/me')return json({id:'fixture-admin',name:'Pagination tester',email:'tester@example.test',is_system_admin:true,report_columns:['generation_id','user','date']});
  if(p==='/v1/organizations')return json({data:[{id:organization,name:'Fixture organization',role:'admin'}]});
  if(p.endsWith('/reports')){
   const query=Object.fromEntries(url.searchParams),matches=filter(query),offset=Number(query.offset||0),data=matches.slice(offset,offset+Number(query.limit||50));
   const entry={url:p+url.search,path:p,query,returnedIDs:(query.format==='csv'?matches:data).map(row=>row.id),failed:false};requests.push(entry);
   if(query.format==='csv'){
    if(failCSV){failCSV=false;entry.failed=true;entry.returnedIDs=[];return route.fulfill({status:503,json:{error:{message:'Fixture export unavailable'}}});}
    return route.fulfill({contentType:'text/csv',body:'Generation ID,User\r\n'+matches.map(row=>row.id+','+row.user_email+'\r\n').join('')});
   }
   if(holdNext){holdNext=false;await new Promise(resolve=>{release=resolve;});}
   if(failNext){failNext=false;entry.failed=true;entry.returnedIDs=[];return route.fulfill({status:503,json:{error:{message:'Fixture report unavailable'}}});}
   return json({data,has_more:offset+data.length<matches.length});
  }
  return json({data:[]});
 });
 const content=page.locator('#reportsContent'),status=content.locator('[role=status]').first();
 const button=name=>content.getByRole('button',{name,exact:true});
 const displayed=()=>content.locator('tbody tr').evaluateAll(elements=>elements.map(el=>el.getAttribute('aria-label').replace('Open generation ','')));
 const ready=async()=>{await page.waitForFunction(()=>{const s=document.querySelector('#reportsContent [role=status]');return s&&(s.textContent===''||s.textContent.startsWith('No generations'));});};
 const apply=async()=>{await button('Apply').click();await ready();};
 const show=async()=>{
  await page.waitForFunction(()=>consoleInitialized&&consoleAuthenticated&&Workspace.ready());
  await page.evaluate(async()=>{currentConsoleSection='reports';GatewayOrganizations.close();document.querySelector('#organizationsContent').replaceChildren();document.querySelector('#profileView').hidden=true;document.querySelector('#generationView').hidden=true;document.querySelector('#configurationView').hidden=false;document.querySelector('#organizationsView').hidden=true;document.querySelector('#reportsView').hidden=false;await GatewayReports.open(false,null,null,{from:'2026-08-01',to:'2026-09-30'});});await ready();
 };
 const reset=async()=>{await show();assert.deepEqual(await displayed(),rows.slice(0,50).map(row=>row.id));};
 const exportReady=async()=>{
  // A table collapse can move the header's hover menu under the stationary mouse.
  await page.mouse.move(1,1);await page.keyboard.press('Escape');
  await page.evaluate(()=>window.scrollTo({top:0,behavior:'instant'}));
  await page.waitForFunction(()=>{const el=document.querySelector('#reportsContent [data-export]'),box=el.getBoundingClientRect();return !el.hidden&&!el.disabled&&document.querySelector('#userDropdown').hidden&&el.contains(document.elementFromPoint(box.x+box.width/2,box.y+box.height/2));});
 };
 const csv=async(expected,pointer=false)=>{
  await exportReady();await button('Download CSV').focus();
  assert.equal(await button('Download CSV').evaluate(el=>el===document.activeElement),true);
  const [download]=await Promise.all([page.waitForEvent('download'),pointer?button('Download CSV').click():page.keyboard.press('Enter')]);
  assert.equal(download.suggestedFilename(),'generation-report.csv');
  assert.deepEqual(fs.readFileSync(await download.path(),'utf8').trim().split('\r\n').slice(1).map(line=>line.split(',')[0]),expected);
  const query=requests.at(-1).query;assert.equal(query.format,'csv');assert.equal(query.offset,undefined);assert.equal(query.limit,undefined);await ready();return query;
 };
 const check=async(name,run)=>{
  const start=requests.length;
  try{await reset();await run();results.push({name,passed:true,requests:requests.slice(start),displayedIDs:await displayed()});}
  catch(error){results.push({name,passed:false,error:error.message,requests:requests.slice(start),displayedIDs:await displayed(),status:await status.textContent()});await page.screenshot({path:path.join(output,`${engine}-${results.length}-failure.png`),fullPage:true});console.error('FAIL:',name,error.message);}
 };
 try{
  await page.goto(base+'/profile');
  const drafts=[['email','#reportUser','few@example.test'],['user ID','#reportUser',user],['generation ID','#reportGeneration',id(2)],['from date','#reportFrom','2026-09-01'],['to date','#reportTo','2026-08-31'],['API key','#reportKey',key]];
  for(const [name,selector,value] of drafts){
   await check('Next retains applied '+name,async()=>{
    const applied={...requests.at(-1).query};await page.locator(selector).fill(value);
    await button('Next').click();await ready();
    assert.deepEqual(await displayed(),rows.slice(50,100).map(row=>row.id));assert.deepEqual(requests.at(-1).query,{...applied,offset:'50'});
    assert.equal(await page.locator(selector).inputValue(),value);assert.equal(await content.locator('[data-page]').innerText(),'Page 2');
    assert.deepEqual(await csv(rows.map(row=>row.id)),{...Object.fromEntries(Object.entries(applied).filter(([name])=>!['offset','limit'].includes(name))),format:'csv'});
   });
   await check('Previous retains applied '+name,async()=>{
    await button('Next').click();await ready();const applied={...requests.at(-1).query};await page.locator(selector).fill(value);
    await button('Previous').click();await ready();assert.deepEqual(await displayed(),rows.slice(0,50).map(row=>row.id));assert.deepEqual(requests.at(-1).query,{...applied,offset:'0'});
   });
  }
  await check('CSV retains visible applied filters before paging',async()=>{
   await page.locator('#reportUser').fill('few@example.test');await page.locator('#reportGeneration').fill(id(2));await page.locator('#reportTo').fill('2026-08-31');
   const query=await csv(rows.map(row=>row.id),true);assert.equal(query.user,'');assert.equal(query.generation_id,'');assert.equal(query.to,'2026-09-30');
  });
  await check('Apply resets third page and applies all drafts to CSV',async()=>{
   await button('Next').click();await ready();await button('Next').click();await ready();assert.deepEqual(await displayed(),rows.slice(100).map(row=>row.id));
   await page.locator('#reportUser').fill('few@example.test');await page.locator('#reportTo').fill('2026-08-31');await page.locator('#reportKey').fill(key);await apply();
   assert.equal(requests.at(-1).query.offset,'0');assert.equal(await content.locator('[data-page]').innerText(),'Page 1');assert.deepEqual(await displayed(),rows.slice(0,3).map(row=>row.id));assert.equal(await content.locator('.report-pagination').isVisible(),false);
   await page.locator('#reportUser').fill('many@example.test');await page.locator('#reportTo').fill('2026-09-30');
   await page.locator('#userMenuToggle').hover();await page.waitForFunction(()=>!document.querySelector('#userDropdown').hidden);
   assert.equal(await button('Download CSV').evaluate(el=>{const box=el.getBoundingClientRect();return el.contains(document.elementFromPoint(box.x+box.width/2,box.y+box.height/2));}),false,'Hover menu must obstruct the pointer control before export readiness');
   const query=await csv(rows.slice(0,3).map(row=>row.id));assert.equal(query.user,'few@example.test');assert.equal(query.to,'2026-08-31');assert.equal(query.api_key_id,key);
  });
  await check('Failed page read and Apply retry preserve drafts and reset offset',async()=>{
   await button('Next').click();await ready();await page.locator('#reportUser').fill('few@example.test');failNext=true;await button('Next').click();
   await status.getByText('Could not load report: Fixture report unavailable',{exact:true}).waitFor();
   assert.equal(requests.at(-1).query.user,'');assert.equal(requests.at(-1).query.offset,'100');assert.equal(await button('Download CSV').isVisible(),false);assert.equal(await content.locator('.report-pagination').isVisible(),false);assert.equal(await page.locator('#reportUser').inputValue(),'few@example.test');
   await apply();assert.equal(requests.at(-1).query.offset,'0');assert.equal(requests.at(-1).query.user,'few@example.test');assert.deepEqual(await displayed(),rows.slice(0,3).map(row=>row.id));await csv(rows.slice(0,3).map(row=>row.id));
  });
  await check('Failed Apply retries submitted drafts; later edits stay unapplied',async()=>{
   await page.locator('#reportUser').fill('few@example.test');failNext=true;await button('Apply').click();await status.getByText('Could not load report: Fixture report unavailable',{exact:true}).waitFor();assert.equal(await page.locator('#reportUser').inputValue(),'few@example.test');
   holdNext=true;await button('Apply').click();await page.waitForFunction(()=>document.querySelector('#reportsContent [role=status]').textContent==='Loading report…');
   await page.locator('#reportUser').fill('many@example.test');release();await ready();assert.deepEqual(await displayed(),rows.slice(0,3).map(row=>row.id));assert.equal((await csv(rows.slice(0,3).map(row=>row.id))).user,'few@example.test');
  });
  await check('Apply generation ID on later page returns match at offset zero',async()=>{
   await button('Next').click();await ready();await page.locator('#reportGeneration').fill(' '+id(2)+' ');await apply();assert.equal(requests.at(-1).query.offset,'0');assert.equal(requests.at(-1).query.generation_id,id(2));assert.deepEqual(await displayed(),[id(2)]);await csv([id(2)]);
  });
  await check('Nonempty applied filters survive paging and failed CSV retry',async()=>{
   await page.locator('#reportUser').fill('many@example.test');await page.locator('#reportFrom').fill('2026-09-01');await apply();
   const applied={...requests.at(-1).query};await page.locator('#reportUser').fill('few@example.test');await page.locator('#reportFrom').fill('2026-08-01');
   await button('Next').click();await ready();assert.deepEqual(requests.at(-1).query,{...applied,offset:'50'});assert.deepEqual(await displayed(),rows.slice(53,103).map(row=>row.id));
   failCSV=true;await exportReady();await button('Download CSV').focus();await page.keyboard.press('Enter');await status.getByText('Fixture export unavailable',{exact:true}).waitFor();
   assert.deepEqual(await displayed(),rows.slice(53,103).map(row=>row.id));assert.equal(await button('Download CSV').isEnabled(),true);assert.equal(requests.at(-1).query.user,'many@example.test');
   assert.equal((await csv(rows.slice(3).map(row=>row.id))).user,'many@example.test');
   await button('Previous').click();await ready();assert.deepEqual(requests.at(-1).query,applied);assert.deepEqual(await displayed(),rows.slice(3,53).map(row=>row.id));
  });
  await check('Keyboard paging with drafts at desktop and mobile widths',async()=>{
   for(const width of [1440,390,320]){
    await page.setViewportSize({width,height:1050});await reset();await page.locator('#reportUser').fill('few@example.test');await page.locator('#reportTo').fill('2026-08-31');
    await button('Next').focus();await page.keyboard.press('Enter');await ready();assert.deepEqual(await displayed(),rows.slice(50,100).map(row=>row.id));
    await button('Previous').focus();await page.keyboard.press('Space');await ready();assert.deepEqual(await displayed(),rows.slice(0,50).map(row=>row.id));
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:path.join(output,`${engine}-${width}.png`),fullPage:true});
   }
  });
  assert.deepEqual(errors,[]);
 }finally{const hash=file=>require('node:crypto').createHash('sha256').update(fs.readFileSync(file)).digest('hex');fs.writeFileSync(path.join(output,`${engine}-results.json`),JSON.stringify({engine,browser:browser.version(),runtimeSHA256:hash(path.join(root,'reports.js')),fixtureSHA256:hash(__filename),results,errors,notices},null,2)+'\n');await browser.close();server.close();}
 const failures=results.filter(result=>!result.passed);console.log(`${results.length-failures.length}/${results.length} pagination cases passed (${engine}). Evidence: ${output}`);assert.equal(failures.length,0);
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
