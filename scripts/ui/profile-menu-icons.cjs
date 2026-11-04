// Real profile-menu markup and handlers, synthetic loopback APIs only.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const root=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',baseline=process.env.UI_BASELINE==='1',out=process.env.UI_EVIDENCE_DIR||path.resolve('dist/checks/gw295-'+engine);
fs.mkdirSync(out,{recursive:true});
const company='11111111-1111-4111-8111-111111111111',key='synthetic-menu-key';
const connection={connected:true,access:'company-api-v1',context:'menu-fixture',origin:'https://central.fixture.test',organization_id:company,organization_name:'Fixture company',version:1};
let connected=false,administrator=false,manager=false,anonymous=false,logoutFailure=false,logoutCount=0;
const writes=[],external=[],errors=[],notices=[],checks=[],geometry=[];
const json=(res,value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
const server=http.createServer((req,res)=>{
 const url=new URL(req.url,'http://fixture');let p=url.pathname;
 if(p==='/auth/company-connection')return json(res,connected&&!anonymous?{connected:true,connection,key}:{connected:false});
 if(p==='/auth/logout'){writes.push({path:p,method:req.method});logoutCount++;if(logoutFailure)return json(res,{error:{message:'Synthetic sign-out failure'}},503);anonymous=true;return json(res,{});}
 if(p.startsWith('/auth/company-api/'))p=p.slice('/auth/company-api'.length);
 if(p==='/site.json')return json(res,{installation_role:'ordinary',central_origin:'https://central.fixture.test'});
 if(p==='/auth/methods')return json(res,{google:true,email:true});
 if(p==='/v1/me')return json(res,anonymous?{error:{message:'Signed out'}}:{id:'fixture-user',name:'Fixture Person',email:'person@example.test',session_id:'fixture',is_system_admin:administrator,is_client_manager:manager},anonymous?401:200);
 if(p==='/v1/info')return json(res,{auth_mode:'google',console:{generation_ui:true,personal_providers:false,shared_resources:true,setup_pending:false}});
 if(p==='/v1/organizations')return json(res,{data:[{id:company,name:'Fixture company',role:'member'}]});
 if(p==='/v1/organizations/'+company)return json(res,{id:company,name:'Fixture company',role:'member'});
 if(p==='/v1/key')return json(res,{key:{id:'fixture-key',user_id:'fixture-user',organization_id:company,resource_mode:'shared',scopes:['generation']},organization:{id:company,available_usd:10}});
 if(p==='/v1/playground-key/restore')return json(res,{error:{message:'No fixture key'}},404);
 if(p==='/v1/system/remote-workspace')return json(res,connected?connection:{connected:false});
 if(p==='/v1/system/remote-workspace/authorization')return json(res,{pending:false});
 if(p.startsWith('/v1/')){if(req.method!=='GET'&&req.method!=='HEAD'){writes.push({path:p,method:req.method});return json(res,{error:{message:'Unexpected write'}},405);}return json(res,{data:[],has_more:false});}
 const name=p.startsWith('/ui/')?path.basename(p):'index.html',file=path.join(root,name);
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base='http://127.0.0.1:'+server.address().port;
 const browser=await({chromium,webkit}[engine]).launch({headless:true});
 const context=await browser.newContext({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),page=await context.newPage();page.setDefaultTimeout(12000);
 await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==base){external.push(route.request().url());return route.abort();}return route.continue();});
 page.on('pageerror',e=>{if(e.message==='ResizeObserver loop completed with undelivered notifications.')notices.push(e.message);else errors.push(e.message);});
 const trigger=()=>page.locator('#userMenuToggle'),menu=()=>page.locator('#userDropdown');
 async function boot(){await page.goto(base+'/profile');await trigger().waitFor();await page.waitForFunction(()=>Workspace.ready());assert.equal(await page.locator('#companyConnectionButton').count(),0);await page.mouse.move(0,900);}
 async function open(){await page.evaluate(async()=>{await document.fonts.ready;await new Promise(resolve=>{let n=0;const frame=()=>++n===6?resolve():requestAnimationFrame(frame);requestAnimationFrame(frame);});});await trigger().focus();await trigger().press('ArrowDown');await menu().waitFor();await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))}
 async function shut(){await page.keyboard.press('Escape');await menu().waitFor({state:'hidden'});await page.mouse.move(0,900);}
 const measurements=()=>menu().evaluate(n=>{const box=n.getBoundingClientRect();return {box:{x:box.x,y:box.y,width:box.width,height:box.height},rows:[...n.querySelectorAll('button:not([hidden])')].map(b=>{const r=b.getBoundingClientRect(),s=getComputedStyle(b),icon=b.querySelector('svg'),label=b.querySelector('.menu-label'),ib=icon?.getBoundingClientRect(),lb=label?.getBoundingClientRect();return {id:b.id,name:b.textContent.trim(),height:r.height,color:s.color,background:s.backgroundColor,gap:s.columnGap,icon:icon?{width:ib.width,height:ib.height,left:ib.left,right:ib.right,top:ib.top,stroke:getComputedStyle(icon).stroke,strokeWidth:icon.getAttribute('stroke-width'),viewBox:icon.getAttribute('viewBox'),hidden:icon.getAttribute('aria-hidden'),focusable:icon.getAttribute('focusable'),kind:b.dataset.menuIcon,bounds:((b)=>({x:b.x,y:b.y,width:b.width,height:b.height}))(icon.getBBox())}:null,label:lb?{left:lb.left,top:lb.top,height:lb.height}:null};})};});
 function verify(m){assert.ok(m.box.x>=0&&m.box.x+m.box.width<=2000);for(const row of m.rows){if(!baseline){assert.ok(row.height>=44);assert.ok(row.icon,row.id);assert.equal(row.icon.width,16);assert.equal(row.icon.height,16);assert.equal(row.icon.strokeWidth,'1.6');assert.equal(row.icon.viewBox,'0 0 24 24');assert.equal(row.icon.stroke,row.color);assert.equal(row.icon.hidden,'true');assert.equal(row.icon.focusable,'false');const bb=row.icon.bounds,pad=Number(row.icon.strokeWidth)/2;assert.ok(bb.x>=pad&&bb.y>=pad&&bb.x+bb.width<=24-pad&&bb.y+bb.height<=24-pad,JSON.stringify(row.icon));assert.ok(row.icon.right<row.label.left);assert.equal(row.gap,'8px');}}}
 try{
  for(const variant of [{name:'member',connected:false,administrator:false,manager:false},{name:'administrator',connected:false,administrator:true,manager:false},{name:'client-manager',connected:false,administrator:false,manager:true},{name:'connected-company',connected:true,administrator:false,manager:false}]){
   ({connected,administrator,manager}=variant);anonymous=false;await boot();await open();
   const expected=['Profile','Sign out'];
   assert.deepEqual(await menu().locator('[role=menuitem]:visible').allTextContents(),expected);
   assert.equal(await page.locator('#workspaceInstallations').count(),0);
   const m=await measurements();verify(m);if(!baseline)assert.deepEqual(m.rows.map(x=>x.icon.kind),['profile','sign-out']);
   await page.keyboard.press('End');assert.equal(await page.locator('#logoutButton').evaluate(n=>n===document.activeElement),true);
   await page.keyboard.press('Home');assert.equal(await page.locator('#personalSettingsButton').evaluate(n=>n===document.activeElement),true);
   await shut();assert.equal(await trigger().evaluate(n=>n===document.activeElement),true);
   checks.push(variant.name+' visibility, names, icons, Home/End/Escape');
  }
  connected=true;administrator=false;manager=false;await boot();
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>GatewayTheme.apply(t),theme);await open();
   const normal=await measurements();verify(normal);assert.ok(normal.box.x>=0&&normal.box.x+normal.box.width<=width);
   await page.locator('#personalSettingsButton').hover();const hover=await measurements();verify(hover);
   await page.locator('#logoutButton').focus();const focus=await measurements();verify(focus);
   assert.deepEqual(normal.box,focus.box);assert.deepEqual(normal.rows.map(r=>r.height),focus.rows.map(r=>r.height));
   await page.screenshot({path:path.join(out,theme+'-'+width+'-profile-menu.png'),fullPage:true});
   geometry.push({theme,width,normal,hover,focus});await shut();
   // Render the existing project menu component with its real icon definitions.
   await page.evaluate(()=>ControlPickers.menu(document.querySelector('#userMenuToggle'),{label:'Project actions',items:[{label:'Rename',icon:'rename'},{label:'Delete',icon:'delete',danger:true}]}));
   const project=page.getByRole('menu',{name:'Project actions',exact:true});await project.waitFor();
   const icons=await project.locator('svg').evaluateAll(ns=>ns.map(n=>({width:n.getBoundingClientRect().width,stroke:n.getAttribute('stroke-width'),viewBox:n.getAttribute('viewBox')})));
   assert.deepEqual(icons,[{width:16,stroke:'1.6',viewBox:'0 0 24 24'},{width:16,stroke:'1.6',viewBox:'0 0 24 24'}]);
   await page.screenshot({path:path.join(out,theme+'-'+width+'-project-reference.png'),fullPage:true});await page.keyboard.press('Escape');
  }
  checks.push('both themes and 1440/390/320: profile/project icon family, hover/focus geometry and screenshots');
  if(!baseline){
   await page.setViewportSize({width:390,height:1000});await page.evaluate(()=>{document.documentElement.style.setProperty('--highlight','#0055aa');document.documentElement.style.setProperty('--text','#442266');});await open();await page.locator('#personalSettingsButton').hover();verify(await measurements());await shut();
   if(engine==='chromium'){await page.emulateMedia({forcedColors:'active'});await open();verify(await measurements());await page.screenshot({path:path.join(out,'forced-colors.png')});await shut();await page.emulateMedia({forcedColors:'none'});}
   await page.evaluate(()=>{document.documentElement.style.removeProperty('--highlight');document.documentElement.style.removeProperty('--text');});
   await open();await page.locator('#personalSettingsButton').press('Enter');await menu().waitFor({state:'hidden'});await page.getByLabel('Display name',{exact:true}).waitFor();assert.equal(new URL(page.url()).pathname,'/profile');
   assert.equal(await page.locator('#companyConnectionButton').count(),0);
   await trigger().hover();await menu().waitFor();await page.mouse.move(0,900);await menu().waitFor({state:'hidden'});
   for(let i=0;i<3;i++){await open();await page.locator('main').click({position:{x:2,y:2}});await menu().waitFor({state:'hidden'});}
   checks.push('custom palette, forced-colors where supported, Profile Enter, no personal connection action, hover and repeated outside dismissal');
   // Native touch events, real menu callbacks, synthetic logout only.
   const touch=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true});
   await touch.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():r.abort());const tp=await touch.newPage();await tp.goto(base+'/profile');await tp.locator('#userMenuToggle').waitFor();await tp.waitForFunction(()=>Workspace.ready());
   await tp.locator('#userMenuToggle').tap();await tp.locator('#userDropdown').waitFor();await tp.locator('#personalSettingsButton').tap();await tp.getByLabel('Display name',{exact:true}).waitFor();await tp.locator('#userDropdown').waitFor({state:'hidden'});
   await tp.locator('#userMenuToggle').tap();await tp.getByLabel('Display name',{exact:true}).tap();await tp.locator('#userDropdown').waitFor({state:'hidden'});
   logoutFailure=true;await tp.locator('#userMenuToggle').tap();await tp.locator('#logoutButton').tap();await tp.getByText('Sign out could not be completed. Please try again.',{exact:true}).waitFor();assert.equal(logoutCount,1);assert.equal(await tp.locator('#logoutButton').isEnabled(),true);
   logoutFailure=false;await tp.locator('#userMenuToggle').tap();await tp.locator('#logoutButton').tap();await tp.locator('#authGate').waitFor();assert.equal(logoutCount,2);await touch.close();
   checks.push('touch toggle/Profile/outside; failed synthetic logout retry and successful sign-out');
  }
  assert.deepEqual(external,[]);assert.deepEqual(errors,[]);assert.ok(writes.every(x=>x.path==='/auth/logout'&&x.method==='POST'));
  console.log('PASS '+checks.length+' profile-menu groups ('+engine+(baseline?', baseline':'')+')');
 }finally{
  const hashes={};for(const name of ['index.html','styles.css','control-pickers.js','workspace.js','app.js'])hashes[name]=crypto.createHash('sha256').update(fs.readFileSync(path.join(root,name))).digest('hex');
  fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,baseline,checks,geometry,errors,notices,external,writes,hashes},null,2));await browser.close();await new Promise(r=>server.close(r));
 }
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
