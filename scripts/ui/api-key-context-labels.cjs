// Actual request-log renderer; all identities and API responses are synthetic.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const root=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',before=process.env.UI_CAPTURE_BEFORE==='1',out=path.resolve(process.env.UI_EVIDENCE_DIR||`dist/checks/gw315/${engine}`);fs.mkdirSync(out,{recursive:true});
const html='<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"><link rel="stylesheet" href="/docs.css"><main id="configurationView" style="max-width:1200px;margin:auto;padding:24px"><section id="journal"></section></main><script src="/form-ui.js"></script><script src="/user-ui.js"></script><script src="/organization-roles.js"></script><script src="/organization-journal.js"></script>';
const fallbackID='11111111-2222-4333-8444-555555555555',hostile='<img src=x onerror="window.injection=true"> & <script>window.injection=true</script>';
const tasks=[
 {identity_snapshot:{api_key_name:'Playground & Templates'}},
 {identity_snapshot:{api_key_name:hostile}},
 {identity_snapshot:{api_key_name:'Long readable key name '.repeat(14)}},
 {api_key_id:fallbackID},
 {},
 {identity_snapshot:{api_key_name:'Restricted key'},content_access:false},
].map((t,i)=>({id:'synthetic-request-'+i,actor_id:i===4?'deleted-user':'synthetic-user',created_at:1793800000+i,status:'failed',model:'fixture/video',prompt:'Fixture prompt',cost:{customer_expected_usd:0},...t}));
const labels=tasks.map(t=>t.identity_snapshot?.api_key_name||t.api_key_id||'—');
const server=http.createServer((req,res)=>{if(req.url==='/'){res.setHeader('Content-Type','text/html');return res.end(html);}const f=path.join(root,path.basename(req.url));if(!fs.existsSync(f))return res.writeHead(404).end();res.setHeader('Content-Type',f.endsWith('.css')?'text/css':'text/javascript');res.end(fs.readFileSync(f));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000},hasTouch:true});const errors=[],external=[],groups=[];page.on('pageerror',e=>errors.push(e.message));await page.route(u=>u.origin!==origin,r=>{external.push(r.request().url());return r.abort();});
 try{
  await page.goto(origin);await page.evaluate(async data=>{window.fixtureCalls=[];window.fixtureAPI=async(p,options={})=>{fixtureCalls.push({path:p,method:options.method||'GET'});return p.endsWith('/members')?{data:[{id:'synthetic-user',name:'Serge fixture',email:'fixture@example.test'}]}:{data,has_more:false};};await OrganizationJournal.mount(document.querySelector('#journal'),{id:'synthetic-company',role:'supermanager'},window.fixtureAPI);},tasks);
  const rows=page.locator('#journal tbody tr');await rows.first().waitFor();assert.equal(await rows.count(),6);assert.equal(await page.getByRole('columnheader',{name:'User / API key',exact:true}).count(),1);
  for(const [i,label]of labels.entries())assert.equal(await rows.nth(i).locator('.request-identity > small').textContent(),(before?'API key: ':'')+label);
  assert.equal(await rows.first().locator('.user-reference-text').innerText(),'Serge fixture');assert.match(await rows.nth(4).innerText(),/deleted-user/);assert.equal(await rows.nth(5).getByRole('button',{name:'Details',exact:true}).count(),0);assert.equal(await rows.nth(5).getByText('Content restricted',{exact:true}).count(),1);
  assert.equal(await page.evaluate(()=>window.injection),undefined);assert.equal(await page.locator('#journal .request-identity img').count(),0);groups.push('Six rows retain exact key labels, UUID/absent fallbacks, user identity and content restriction; hostile name remains text');
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:path.join(out,`${theme}-${width}.png`),fullPage:true});groups.push(`${theme}/${width} retains readable identity column and table containment`);}
  if(!before){
   for(const i of [0,1,3,4]){const trigger=rows.nth(i).getByRole('button',{name:'Details',exact:true});await trigger.focus();await trigger.press('Enter');const d=page.getByRole('dialog',{name:'Request details',exact:true});await d.waitFor();const keyFact=d.locator('.detail-facts > div').filter({has:page.locator('dt').filter({hasText:/^API key$/})});assert.equal(await keyFact.locator('dd').textContent(),labels[i]);assert.equal(await d.locator('dt').filter({hasText:/^API key$/}).count(),1);assert.equal(await page.evaluate(()=>window.injection),undefined);await page.keyboard.press('Escape');await d.waitFor({state:'detached'});assert.equal(await trigger.evaluate(n=>document.activeElement===n),true);}
   groups.push('Details retains meaningful API key fact label, exact safe values and keyboard close/focus behavior');
   assert.equal(await rows.locator('.request-identity > small').filter({hasText:/^API key:/}).count(),0);
   groups.push('No redundant prefix or replacement helper text');
  }
  const calls=await page.evaluate(()=>fixtureCalls);assert.ok(calls.every(x=>x.method==='GET'));assert.deepEqual(errors,[]);assert.deepEqual(external,[]);groups.push('Read-only synthetic calls, no page errors or external requests');
  fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,before,groups,errors,external,calls,source:Object.fromEntries(['organization-journal.js','styles.css','docs.css'].map(f=>[f,crypto.createHash('sha256').update(fs.readFileSync(path.join(root,f))).digest('hex')]))},null,2));console.log(`PASS ${engine}: ${groups.length} groups`);
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;});
