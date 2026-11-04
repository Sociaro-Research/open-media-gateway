// Current portal modules, synthetic loopback APIs; no live accounts or revocation.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const out=path.resolve(process.env.UI_EVIDENCE_DIR||'/tmp/gw290-ui-'+engine);
const company='11111111-1111-4111-8111-111111111111',historyCompany='22222222-2222-4222-8222-222222222222',emptyCompany='33333333-3333-4333-8333-333333333333';
const key='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',legacyKey='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb';
let longName=false;const longHost=['a'.repeat(63),'b'.repeat(63),'c'.repeat(63),'d'.repeat(61)].join('.')+':65535';
let central=true,admin=true,theme='dark',failRead=false,failWrite=false,loseWrite=false,holdWrite=false,releaseWrite,holdRead=false,releaseRead;
const calls=[],errors=[],checks=[],screens=[];
const issued='2026-11-01T10:00:00Z',expires='2099-12-01T00:00:00Z';
const org=id=>({id,name:id===company?(longName?longHost:'Connected studio'):id===historyCompany?'Revoked studio':'Ordinary company',role:'system_admin',balance_usd:0,credit_limit_usd:0,storage_bytes:0});
const baseGrant={organization_id:company,company_name:org(company).name,name:'studio.example.test',client_origin:'https://studio.example.test',created_at:issued,prefix:'gw_fixture_a',provisioning:'dedicated'};
let grants,extraCompanies=0;
function reset(){grants=[{...baseGrant,id:key,access:'company-api-v1',expires_at:null},{...baseGrant,id:legacyKey,access:'generation-v1',expires_at:expires,provisioning:'legacy',prefix:'gw_fixture_b'}];}
reset();
const json=(res,status,value)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://localhost'),p=url.pathname;
 if(p.startsWith('/v1/')||p.startsWith('/auth/'))calls.push({path:p,method:req.method});
 if(p==='/auth/company-connection')return json(res,200,{connected:false});
 if(p==='/auth/methods')return json(res,200,{google:true,email:false});
 if(p==='/site.json')return json(res,200,{});
 const policy={installation_role:central?'central':'ordinary',generation_ui:true,personal_providers:false,shared_resources:true,version:1,setup_pending:false};
 if(p==='/v1/info')return json(res,200,{auth_mode:'google',provider:'mock',console:policy});
 if(p==='/v1/me')return json(res,200,{id:'fixture-user',session_id:'fixture-session',name:'Administrator',email:'admin@fixture.test',theme,is_system_admin:admin,is_client_manager:false});
 if(p==='/v1/system/settings')return json(res,200,policy);
 if(p==='/v1/organizations')return json(res,200,{data:[org(company),org(historyCompany),org(emptyCompany),...Array.from({length:extraCompanies},(_,i)=>({id:'extra-'+i,name:'Extra company '+i,role:'system_admin',balance_usd:0,credit_limit_usd:0,storage_bytes:0}))]});
 if(p==='/v1/organizations/'+company)return json(res,200,org(company));
 if(p==='/v1/system/installation-access'){
  if(failRead){failRead=false;return json(res,503,{error:{message:'Access list temporarily unavailable'}});}
  return json(res,200,{data:[{organization_id:company,active_count:grants.filter(g=>!g.revoked_at&&(!g.expires_at||Date.parse(g.expires_at)>Date.now())).length}]});
 }
 if(p.startsWith('/v1/system/organizations/')){
  const m=p.match(/^\/v1\/system\/organizations\/([^/]+)\/installation-access(?:\/([^/]+))?$/);assert.ok(m,'exact scoped route');
  const cid=m[1],id=m[2];
  if(req.method==='GET'){
   if(failRead){failRead=false;return json(res,503,{error:{message:'Access list temporarily unavailable'}});}
   const data=cid===company?structuredClone(grants):cid===historyCompany?[{...baseGrant,id:'cccccccc-cccc-4ccc-8ccc-cccccccccccc',organization_id:historyCompany,access:'company-api-v1',expires_at:null,revoked_at:issued}]:[];
   if(holdRead){holdRead=false;releaseRead=()=>json(res,200,{data});return;}
   return json(res,200,{data});
  }
  assert.equal(req.method,'DELETE');assert.equal(cid,company);const grant=grants.find(g=>g.id===id);assert.ok(grant,'exact immutable key');
  const finish=()=>{if(failWrite){failWrite=false;return json(res,503,{error:{message:'Revoke failed for this fixture'}});}grant.revoked_at=new Date().toISOString();if(loseWrite){loseWrite=false;return json(res,502,{error:{message:'Response lost; retry to confirm'}});}return json(res,200,{revoked:true});};
  if(holdWrite){holdWrite=false;releaseWrite=finish;return;}return finish();
 }
 if(p==='/v1/playground-key/restore'||p==='/v1/playground-key/create')return json(res,404,{error:{message:'No fixture generation key'}});
 if(p==='/v1/workspace-installations')throw Error('Removed personal grant list called');
 if(p.startsWith('/v1/')){assert.equal(req.method,'GET','no unrelated mutation: '+p);return json(res,200,{data:[]});}
 const relative=p.startsWith('/ui/')?p.slice(4):'index.html',file=path.join(root,relative);
 if(!file.startsWith(root+path.sep)||!fs.existsSync(file)){res.writeHead(404);return res.end();}
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':file.endsWith('.woff2')?'font/woff2':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 fs.mkdirSync(out,{recursive:true});await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const origin='http://127.0.0.1:'+server.address().port;
 const browser=await({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const context=await browser.newContext({viewport:{width:1440,height:1100}}),page=await context.newPage();
 page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/*',r=>new URL(r.request().url()).origin===origin?r.continue():r.abort());
 const row=id=>page.locator('#systemContent tbody tr').filter({hasText:org(id).name});
 const modal=page.locator('dialog.connection-dialog[open]');
 const action=id=>row(id).getByRole('button',{name:'Revoke installation access for '+org(id).name,exact:true});
 const open=async()=>{await page.goto(origin+'/system');await page.locator('[data-system-page="organizations"]').click();await row(company).waitFor();await page.waitForFunction(()=>document.querySelector('#systemContent tbody tr .danger:not(:disabled)'));};
 const reveal=async()=>{await row(company).hover();await action(company).focus();};
 const dialogClose=async()=>modal.getByRole('button',{name:'Close installation access',exact:true}).click();
 const writeCount=()=>calls.filter(x=>x.method==='DELETE').length;
 const choose=()=>modal.locator('.remote-job').filter({hasText:'Company access'});
 try{
  await open();assert.equal(await page.locator('#workspaceGrantsButton').count(),0);assert.equal(calls.filter(x=>x.path==='/v1/workspace-installations').length,0);
  assert.equal(await action(historyCompany).isDisabled(),true);assert.equal(await action(emptyCompany).isDisabled(),true);
  checks.push('personal entry absent; history-only/empty companies disabled');
  await reveal();await action(company).press('Enter');await choose().waitFor();assert.equal(await modal.locator('.remote-job').count(),2);
  await page.keyboard.press('Escape');await modal.waitFor({state:'hidden'});assert.equal(writeCount(),0);
  checks.push('company row keyboard opens scoped permanent/legacy list; Escape nonmutating');

  await reveal();await action(company).click();await choose().waitFor();let prompt;
  page.once('dialog',async d=>{prompt=d.message();await d.dismiss();});await choose().getByRole('button',{name:'Revoke',exact:true}).click();
  assert.match(prompt,/studio\.example\.test/);assert.match(prompt,/gw_fixture_a/);assert.equal(writeCount(),0);
  failWrite=true;page.once('dialog',d=>d.accept());await choose().getByRole('button',{name:'Revoke',exact:true}).click();
  await modal.getByText('Revoke failed for this fixture',{exact:true}).waitFor();assert.equal(await choose().getByRole('button',{name:'Revoke',exact:true}).isEnabled(),true);
  checks.push('exact-target confirmation/cancel and visible failure retry');

  holdWrite=true;page.once('dialog',d=>d.accept());await choose().getByRole('button',{name:'Revoke',exact:true}).click();
  await page.waitForFunction(()=>[...document.querySelectorAll('dialog[open] .remote-job button')].some(b=>b.textContent==='Revoke'&&b.disabled));
  await page.evaluate(()=>{for(const button of document.querySelectorAll('dialog[open] .remote-job button'))button.click();});
  await page.keyboard.press('Escape');assert.equal(await modal.count(),1);assert.equal(writeCount(),2);
  releaseWrite();await choose().getByRole('button',{name:'Revoked',exact:true}).waitFor();
  assert.equal(grants[0].revoked_at!=null,true);assert.equal(grants[1].revoked_at,undefined);
  await dialogClose();await reveal();await action(company).click();await choose().getByRole('button',{name:'Revoked',exact:true}).waitFor();
  assert.equal(await choose().getByRole('button',{name:'Revoked',exact:true}).isDisabled(),true);
  checks.push('pending double click blocked; exact key revoked; other grant/history preserved on reopen');
  const legacy=modal.locator('.remote-job').filter({hasText:'Generation-only access'});
  loseWrite=true;page.once('dialog',d=>d.accept());await legacy.getByRole('button',{name:'Revoke',exact:true}).click();
  await modal.getByText('Response lost; retry to confirm',{exact:true}).waitFor();
  page.once('dialog',d=>d.accept());await legacy.getByRole('button',{name:'Revoke',exact:true}).click();
  await legacy.getByRole('button',{name:'Revoked',exact:true}).waitFor();await dialogClose();
  await page.waitForFunction(name=>{const b=[...document.querySelectorAll('#systemContent button')].find(b=>b.getAttribute('aria-label')===name);return b?.disabled;},'Revoke installation access for '+org(company).name);
  checks.push('lost response same-key retry; revoked-only row disabled');

  reset();await open();await reveal();holdRead=true;await action(company).click();await modal.getByText('Loading…',{exact:true}).waitFor();await dialogClose();releaseRead();
  await reveal();await action(company).click();await choose().waitFor();assert.equal(await modal.locator('.remote-job').count(),2);await dialogClose();
  checks.push('closed slow read cannot append to reopened dialog');
  failRead=true;await page.locator('[data-system-page="organizations"]').click();await row(company).getByRole('button',{name:'Revoke installation access for '+org(company).name}).filter({hasText:'Retry access'}).waitFor();
  await reveal();await action(company).click();await page.waitForFunction(()=>[...document.querySelectorAll('#systemContent button')].some(b=>b.textContent==='Revoke'&&!b.disabled));
  checks.push('row access-read failure retries without mutation');

  for(const mode of ['dark','light'])for(const width of [1440,390,320]){
   theme=mode;await page.setViewportSize({width,height:1100});await open();await reveal();
   const geometry=await row(company).locator('.form-actions').evaluate(el=>{const b=[...el.querySelectorAll('button')].map(x=>x.getBoundingClientRect());return {gap:getComputedStyle(el).gap,buttons:b.map(x=>({x:x.x,y:x.y,w:x.width,h:x.height})),documentWidth:document.documentElement.scrollWidth,viewport:innerWidth};});
   assert.equal(geometry.gap,'12px');assert.ok(geometry.buttons.every(b=>b.h>=44));assert.ok(geometry.documentWidth<=width+1,'no page overflow');
   const listFile=path.join(out,mode+'-'+width+'-organizations.png');await page.screenshot({path:listFile,fullPage:true});screens.push(listFile);
   await action(company).click();await choose().waitFor();
   const dialogRect=await modal.boundingBox();assert.ok(dialogRect.x>=0&&dialogRect.x+dialogRect.width<=width+1,'dialog fits viewport');
   const grantTargets=await modal.locator('.remote-job button').evaluateAll(buttons=>buttons.map(b=>({width:b.getBoundingClientRect().width,height:b.getBoundingClientRect().height})));assert.ok(grantTargets.length>=2&&grantTargets.every(b=>b.width>=44&&b.height>=44),'all modal grant targets at least 44px');
   await choose().getByRole('button',{name:'Revoke',exact:true}).focus();
   const file=path.join(out,mode+'-'+width+'-installation-access.png');await page.screenshot({path:file,fullPage:true});screens.push(file);await dialogClose();
  }
  checks.push('dark/light 1440/390/320 spacing, targets, keyboard focus and screenshots');
  await page.emulateMedia({forcedColors:'active'});await reveal();await action(company).focus();await page.screenshot({path:path.join(out,'forced-colors.png'),fullPage:true});await page.emulateMedia({forcedColors:'none'});
  longName=true;await page.setViewportSize({width:320,height:1100});await open();await reveal();await action(company).click();await choose().waitFor();const longGeometry=await modal.evaluate(d=>({width:d.clientWidth,scroll:d.scrollWidth,viewport:innerWidth,right:d.getBoundingClientRect().right}));assert.ok(longGeometry.scroll<=longGeometry.width+1,'long address wraps inside modal');assert.ok(longGeometry.right<=320);await page.screenshot({path:path.join(out,'long-address-320.png'),fullPage:true});await dialogClose();longName=false;checks.push('259-byte canonical Host remains usable in narrow table and dialog');
  extraCompanies=70;const readsBefore=calls.filter(c=>c.path==='/v1/system/installation-access').length;await open();assert.equal(calls.filter(c=>c.path==='/v1/system/installation-access').length-readsBefore,1);assert.equal(await page.locator('#systemContent tbody tr').count(),73);extraCompanies=0;checks.push('73 companies use one summary read; no per-row throttling');
  const touchContext=await browser.newContext({viewport:{width:390,height:1100},hasTouch:true,isMobile:true}),touch=await touchContext.newPage();touch.setDefaultTimeout(10000);touch.on('pageerror',e=>errors.push(e.message));await touch.route('**/*',r=>new URL(r.request().url()).origin===origin?r.continue():r.abort());
  try{
   const beforeTouch=writeCount();await touch.goto(origin+'/system');await touch.locator('[data-system-page="organizations"]').tap();const touchRow=touch.locator('#systemContent tbody tr').filter({hasText:org(company).name}),touchRevoke=touchRow.getByRole('button',{name:'Revoke installation access for '+org(company).name,exact:true});await touchRevoke.waitFor();assert.ok(await touchRow.getByRole('button',{name:'Manage',exact:true}).isVisible());await touchRevoke.tap();const touchModal=touch.locator('dialog.connection-dialog[open]');await touchModal.locator('.remote-job').first().waitFor();touch.once('dialog',d=>d.dismiss());await touchModal.locator('.remote-job').first().getByRole('button',{name:'Revoke',exact:true}).tap();assert.equal(writeCount(),beforeTouch);await touchModal.getByRole('button',{name:'Close installation access',exact:true}).tap();await touchModal.waitFor({state:'hidden'});assert.equal(writeCount(),beforeTouch);await touch.screenshot({path:path.join(out,'touch-390.png'),fullPage:true});
  }finally{await touchContext.close();}
  checks.push('actual touch context taps row Revoke, cancels exact confirmation and closes without mutation');
  central=false;await page.setViewportSize({width:1440,height:1100});await page.goto(origin+'/system');await page.locator('[data-system-page="organizations"]').click();await row(company).waitFor();assert.equal(await page.locator('#systemContent .danger').count(),0);
  central=true;admin=false;await page.goto(origin+'/organizations');await page.locator('#organizationsContent').waitFor();assert.equal(await page.locator('[aria-label^="Revoke installation access"]').count(),0);assert.equal(await page.locator('#workspaceGrantsButton').count(),0);
  checks.push('ordinary Administrator and company member have no installation action');
  assert.deepEqual(errors,[]);
  fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,checks,calls,errors,screens},null,2));console.log(JSON.stringify({engine,passed:checks.length,errors,output:out},null,2));
 }catch(e){fs.writeFileSync(path.join(out,'failure.json'),JSON.stringify({engine,error:String(e),checks,calls,errors},null,2));await page.screenshot({path:path.join(out,'failure.png'),fullPage:true}).catch(()=>{});throw e;}
 finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;});
