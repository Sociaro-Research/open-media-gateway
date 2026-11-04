// Actual Profile UI against isolated loopback fixtures; no live account changes.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const root=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',baseline=process.env.UI_BASELINE==='1',out=path.resolve(process.env.UI_EVIDENCE_DIR||'dist/checks/gw307-'+engine);
fs.mkdirSync(out,{recursive:true});
const id='11111111-1111-4111-8111-111111111111',org={id,name:'Fixture company',role:'admin',balance_usd:20,credit_limit_usd:30};
let personal=false,delayOrganizations=false,releaseOrganizations=null,theme='light';
const writes=[],errors=[],external=[],groups=[],metrics=[];
const json=(res,data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
const server=http.createServer(async(req,res)=>{
 const p=new URL(req.url,'http://fixture').pathname;
 if(p==='/auth/company-connection')return json(res,{connected:false},404);
 if(p==='/site.json')return json(res,{branding:{name:'Fixture',sidebar_subsections:true,site_max_width:1280,content_max_width:null}});
 if(p==='/v1/info')return json(res,{auth_mode:'google',console:{generation_ui:true,personal_providers:personal,shared_resources:!personal}});
 if(p==='/v1/me'){
  if(req.method==='PATCH'){let body='';for await(const part of req)body+=part;writes.push(JSON.parse(body));return json(res,{error:{message:'Synthetic profile save failed'}},503);}
  return json(res,{id:'fixture-user',name:'Fixture Person',email:'fixture@example.test',theme,is_system_admin:true,contacts:[{label:'Website',url:'https://example.test/contact'}]});
 }
 if(p==='/v1/organizations'){if(delayOrganizations)await new Promise(r=>releaseOrganizations=r);return json(res,{data:[org]});}
 if(p==='/v1/organizations/'+id)return json(res,org);
 if(p.endsWith('/members'))return json(res,{data:[{id:'fixture-user',name:'Fixture Person',email:'fixture@example.test',role:'admin'}]});
 if(p==='/v1/system/branding')return json(res,{settings:{sidebar_subsections:true,version:1},defaults:{},colors:[]});
 if(p.startsWith('/v1/'))return json(res,{data:[],has_more:false});
 const file=path.join(root,path.basename(p).includes('.')?path.basename(p):'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage();page.setDefaultTimeout(12000);
 await context.route('**/*',r=>{if(new URL(r.request().url()).origin!==base){external.push(r.request().url());return r.abort();}return r.continue();});page.on('pageerror',e=>errors.push(e.message));
 const input=()=>page.getByLabel('Display name',{exact:true});
 const settled=()=>page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
 const boot=async()=>{await page.goto(base+'/profile');await input().waitFor();await page.waitForFunction(()=>Workspace.ready());await settled();};
 const geometry=()=>page.evaluate(()=>{const box=n=>{const r=n.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height};};return{profile:box(document.querySelector('#profileContent')),form:box(document.querySelector('.profile-settings')),configuration:box(document.querySelector('#configurationView')),rail:document.querySelector('#configurationView').classList.contains('has-sidebar-subsections'),aside:document.querySelectorAll('#configurationView .workspace-subsections-sidebar').length,visibleNav:[...document.querySelectorAll('#configurationView nav')].filter(n=>n.getClientRects().length).length,overflow:document.documentElement.scrollWidth>innerWidth};});
 try{
  for(const mode of ['light','dark']){
   theme=mode;await boot();
   for(const width of [1440,390,320]){
    await page.setViewportSize({width,height:1000});await input().fill('A long but valid unsaved profile display name');await settled();const m=await geometry();metrics.push({theme,width,...m});
    assert.equal(m.overflow,false);assert.ok(m.form.width<=560.1);assert.equal(await page.locator('#profileContent h1').innerText(),'Profile');assert.equal(await page.getByRole('heading',{name:'Contact links',exact:true}).count(),1);
    if(!baseline){assert.equal(m.rail,false);assert.equal(m.aside,0);assert.equal(m.visibleNav,0);assert.equal(await page.locator('#profileContent [role=tab]').count(),0);assert.ok(Math.abs(m.profile.x-m.configuration.x)<1,'no reserved rail');}
    await input().focus();await page.screenshot({path:path.join(out,mode+'-'+width+'.png'),fullPage:true});groups.push(mode+' '+width+' layout/draft/focus');
   }
  }
  if(!baseline){
   // GW-277: late bootstrap must not replace a mounted Profile draft or its focus.
   delayOrganizations=true;await page.goto(base+'/profile');await input().waitFor();await input().fill('Retain pending workspace draft');await input().focus();const handle=await input().elementHandle();assert.ok(releaseOrganizations);releaseOrganizations();delayOrganizations=false;await page.waitForFunction(()=>Workspace.ready());await settled();assert.equal(await handle.evaluate(n=>n.isConnected&&n===document.activeElement),true);assert.equal(await input().inputValue(),'Retain pending workspace draft');assert.equal((await geometry()).aside,0);groups.push('delayed workspace completion preserves exact draft node and focus');
   await page.setViewportSize({width:1440,height:1000});await page.evaluate(()=>Workspace.set('11111111-1111-4111-8111-111111111111'));await page.locator('#organizationsContent .member-limit-form').first().waitFor();await settled();assert.equal(await page.locator('#configurationView').evaluate(n=>n.classList.contains('has-sidebar-subsections')),true);assert.ok(await page.locator('#configurationView .workspace-subsections-sidebar [role=tab]').count()>1);
   await page.locator('#userMenuToggle').focus();await page.keyboard.press('ArrowDown');await page.getByRole('menuitem',{name:'Profile',exact:true}).press('Enter');await input().waitFor();assert.equal((await geometry()).aside,0);assert.equal(new URL(page.url()).pathname,'/profile');
   await page.locator('#systemNav').click();await page.locator('#systemView').waitFor();assert.equal(await page.locator('#systemView > .configuration-sidebar').isVisible(),true);await page.locator('#generationNav').click();await page.goBack();await page.locator('#systemView').waitFor();await page.goForward();await page.locator('#generationView').waitFor();await page.locator('#userMenuToggle').focus();await page.keyboard.press('ArrowDown');await page.getByRole('menuitem',{name:'Profile',exact:true}).press('Enter');await input().waitFor();assert.equal((await geometry()).aside,0);groups.push('organization and Administration sidebars restore; header history and Profile return');
   personal=true;await boot();assert.ok(await page.locator('#profileContent [role=tab]').count()+await page.locator('#configurationView .workspace-subsections-sidebar [role=tab]').count()>1);await page.getByRole('tab',{name:'API keys',exact:true}).click();await page.locator('#profileContent #newKeyName').waitFor();await page.getByRole('tab',{name:'Profile',exact:true}).click();await input().waitFor();personal=false;await boot();assert.equal((await geometry()).aside,0);groups.push('real multi-section personal workspace remains navigable');
   await input().fill('Failed synthetic save draft');await page.locator('#profileContent form').filter({has:input()}).getByRole('button',{name:'Save',exact:true}).click();await page.getByText('Synthetic profile save failed',{exact:true}).waitFor();assert.equal(await input().inputValue(),'Failed synthetic save draft');assert.equal((await geometry()).aside,0);await page.screenshot({path:path.join(out,'profile-error.png'),fullPage:true});assert.equal(writes.length,1);assert.deepEqual(writes[0],{name:'Failed synthetic save draft'});groups.push('existing failed-save feedback and draft remain intact');
  }
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);console.log('PASS '+groups.length+' GW307 groups '+engine+(baseline?' baseline':''));
 }finally{
  releaseOrganizations?.();fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,baseline,groups,metrics,errors,external,writes,hashes:Object.fromEntries(['workspace.js','app.js','styles.css','layout.js','profile.js'].map(p=>[p,crypto.createHash('sha256').update(fs.readFileSync(path.join(root,p))).digest('hex')]))},null,2));await browser.close();await new Promise(r=>server.close(r));
 }
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
