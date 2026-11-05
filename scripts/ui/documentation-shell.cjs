// Real Caddy serving policy with intercepted, synthetic HTTPS origins only.
const {chromium,webkit}=require('playwright'),assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),{spawn}=require('node:child_process');
const root=path.resolve(__dirname,'../..'),out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/documentation-shell'),engine=process.env.UI_BROWSER_ENGINE||'chromium';fs.mkdirSync(out,{recursive:true});
const listen=s=>new Promise(r=>s.listen(0,'127.0.0.1',()=>r(s.address().port))),pause=n=>new Promise(r=>setTimeout(r,n));
let ui='https://console.sociaro.ai',docs='https://docs.sociaro.ai',mode='light',failConfig=false,holdConfig=false,setupRequired=false,held=[],legalMode='custom',caddy,browser;
const json=(res,value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
const site=()=>({ui_url:ui,api_url:ui,docs_url:docs===ui?'':docs,branding:{name:'Local Gateway',version:1,dark:{},light:{},assets:{logo:{mime:'image/svg+xml'},logo_light:{mime:'image/svg+xml'},symbol:{mime:'image/svg+xml'}}}});
const upstream=http.createServer((req,res)=>{const p=new URL(req.url,'http://fixture').pathname;
 if(p==='/site.json'){if(failConfig)return json(res,{},503);if(holdConfig){held.push(()=>json(res,site()));return;}return json(res,site());}
 if(p==='/auth/session')return res.writeHead(req.headers.cookie?.includes('fixture_session=ok')?204:401).end();
 if(p==='/auth/methods')return json(res,{email:true,email_delivery:true,google:true,setup_required:setupRequired});
 if(p==='/v1/info')return json(res,{auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,version:1}});
 if(p==='/v1/me')return json(res,{id:'fixture',name:'Local user',email:'fixture@example.test',theme:mode,is_system_admin:true});
 if(p==='/v1/system/settings')return json(res,{generation_ui:true,personal_providers:true,version:1});
 if(p==='/legal/privacy'||p==='/legal/terms')return json(res,{text:legalMode==='default'?'':'Synthetic local legal fixture.'},legalMode==='error'?503:200);
 if(p==='/openapi.json')return json(res,{openapi:'3.1.0',info:{title:'Local API',version:'fixture'},tags:[],paths:{},components:{schemas:{}}});
 if(p.startsWith('/branding/assets/'))return res.writeHead(200,{'Content-Type':'image/svg+xml'}).end('<svg xmlns="http://www.w3.org/2000/svg" width="190" height="40"><text x="0" y="28" font-size="26" fill="#888">Gateway</text></svg>');
 if(p.startsWith('/v1/'))return json(res,{data:[]});return res.writeHead(404).end();
});
const results=[];
(async()=>{
 const apiPort=await listen(upstream),reserve=http.createServer(),port=await listen(reserve);await new Promise(r=>reserve.close(r));
 const log=fs.openSync(path.join(out,'caddy.log'),'w');caddy=spawn(process.env.CADDY_BIN||'caddy',['run','--config',path.join(root,'web/Caddyfile'),'--adapter','caddyfile'],{env:{...process.env,UI_ROOT:path.join(root,'web/public'),GATEWAY_UPSTREAM:'127.0.0.1:'+apiPort,UI_LISTEN:':'+port,DOCS_HOST:'docs.sociaro.ai docs-staging.sociaro.ai reference.other.test'},stdio:['ignore',log,log]});fs.closeSync(log);
 let ready=false;for(let i=0;i<80;i++){try{if((await fetch('http://127.0.0.1:'+port+'/ui/site.js')).status===200){ready=true;break}}catch{}if(caddy.exitCode!==null)throw Error(fs.readFileSync(path.join(out,'caddy.log'),'utf8'));await pause(100);}assert.ok(ready,'Local Caddy readiness');
 browser=await ({chromium,webkit}[engine]).launch({headless:true});
 async function scenario(name,settings,run){if(process.env.UI_SCENARIO&&!name.includes(process.env.UI_SCENARIO))return;({ui,docs,mode='light'}=settings);failConfig=false;holdConfig=false;setupRequired=false;held=[];legalMode='custom';const errors=[],documents=[],requests=[];const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:mode==='dark'?'light':'dark'});
  if(settings.auth!==false)await context.addCookies([{name:'fixture_session',value:'ok',url:ui,httpOnly:true,sameSite:'Lax',secure:true}]);
  if(settings.blocked)await context.addInitScript(()=>{Object.defineProperty(window,'localStorage',{get(){throw new DOMException('Blocked','SecurityError')}});Object.defineProperty(Document.prototype,'cookie',{get(){return ''},set(){throw new DOMException('Blocked','SecurityError')}})});
  else await context.addInitScript(value=>localStorage.setItem('gateway-theme',value),mode);
  await context.route('**/*',async route=>{const req=route.request(),u=new URL(req.url());assert.ok([ui,docs].includes(u.origin),'Unexpected network destination '+u.origin);requests.push({host:u.host,path:u.pathname,method:req.method()});if(req.resourceType()==='document')documents.push({host:u.host,path:u.pathname,query:u.search});
   const headers={...await req.allHeaders(),host:u.host,'accept-encoding':'identity'};delete headers['content-length'];
   // WebKit's intercepted request omits Cookie even when the scoped jar has it.
   // The loopback proxy forwards only that exact origin's synthetic browser cookies.
   const cookies=await context.cookies(req.url());if(cookies.length)headers.cookie=cookies.map(c=>c.name+'='+c.value).join('; ');else delete headers.cookie;
   const answer=await new Promise((resolve,reject)=>{const r=http.request({hostname:'127.0.0.1',port,path:u.pathname+u.search,method:req.method(),headers},res=>{const chunks=[];res.on('data',c=>chunks.push(c));res.on('end',()=>resolve({status:res.statusCode,headers:Object.fromEntries(Object.entries(res.headers).map(([k,v])=>[k,Array.isArray(v)?v.join(', '):String(v)])),body:Buffer.concat(chunks)}));});r.on('error',reject);if(req.postDataBuffer())r.write(req.postDataBuffer());r.end();});
   if(process.env.UI_BASELINE_DIR&&['/ui/site.js','/ui/app.js'].includes(u.pathname)){answer.body=fs.readFileSync(path.join(process.env.UI_BASELINE_DIR,'web/public',u.pathname));delete answer.headers['content-length'];}
   await route.fulfill(answer);
  });
  const page=await context.newPage();page.setDefaultTimeout(15000);context.on('page',p=>p.on('pageerror',e=>errors.push(e.message)));page.on('pageerror',e=>errors.push(e.message));
  try{await run({page,context,documents,requests});assert.deepEqual(errors,[]);results.push({name,passed:true,document_requests:documents,request_count:requests.length});fs.writeFileSync(path.join(out,engine+'.json'),JSON.stringify(results,null,2));}
  finally{holdConfig=false;for(const release of held.splice(0))release();await context.close();}
 }
 const loaded=async page=>{await page.waitForFunction(()=>window.GatewaySite);await page.evaluate(()=>GatewaySite.ready);};
 async function footer(page){await loaded(page);const f=page.getByRole('contentinfo');assert.equal(await f.count(),1);assert.equal(await f.getByRole('link',{name:'Terms of Use',exact:true}).getAttribute('href'),ui+'/terms');assert.equal(await f.getByRole('link',{name:'Privacy Policy',exact:true}).getAttribute('href'),ui+'/privacy');}
 async function clickDocs(page,documents){await page.locator('#docsNav').waitFor({state:'visible'});documents.length=0;await page.locator('#docsNav').click();await page.waitForURL(u=>u.origin===docs&&(!u.searchParams.has('theme')));await page.locator('#docsContent h1').waitFor();return documents.slice();}
 try{
  await scenario('actual Console click document count',{ui:'https://console.sociaro.ai',docs:'https://docs.sociaro.ai'},async({page,documents})=>{
   await page.goto(ui+'/');await loaded(page);
   // Site configuration alone does not finish authentication/workspace bootstrap.
   // Seed the legacy fragment after the native generation field is ready, so the
   // normal Console URL reconciliation cannot erase test setup before the click.
   await page.locator('#apiKey:enabled').waitFor({state:'visible'});
   await page.evaluate(()=>history.replaceState(null,'','/#docs/authentication'));
   const docsSeen=await clickDocs(page,documents);assert.equal(docsSeen.length,process.env.UI_BASELINE_DIR?2:1);assert.equal(page.url(),docs+'/#docs/authentication');
  });
  if(!process.env.UI_BASELINE_DIR){
   for(const pair of [['https://console.sociaro.ai','https://docs.sociaro.ai'],['https://console-staging.sociaro.ai','https://docs-staging.sociaro.ai']])for(const color of ['light','dark'])await scenario('shared shell '+pair[0]+' '+color,{ui:pair[0],docs:pair[1],mode:color},async({page,documents})=>{
    await page.goto(ui+'/');await footer(page);await clickDocs(page,documents);await footer(page);assert.equal(await page.locator('html').getAttribute('data-theme'),color);assert.equal(await page.getByRole('link',{name:'Docs',exact:true}).getAttribute('aria-current'),'page');assert.equal(await page.getByRole('link',{name:'Console',exact:true}).count(),1);
    for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Docs overflow '+width);const gap=await page.getByRole('contentinfo').evaluate(f=>f.getBoundingClientRect().top-document.querySelector('main').getBoundingClientRect().bottom);assert.ok(gap>=-1,'Footer overlaps main');await page.screenshot({path:path.join(out,engine+'-'+new URL(ui).hostname+'-'+color+'-'+width+'.png'),fullPage:true});}
    const privateJS=await page.evaluate(async()=>({app:(await fetch('/ui/app.js')).status,site:(await fetch('/ui/site.js')).status}));assert.deepEqual(privateJS,{app:404,site:200});
    documents.length=0;await page.getByRole('link',{name:'Console',exact:true}).click();await page.waitForURL(ui+'/');await loaded(page);assert.equal(documents.length,1);assert.equal(await page.locator('html').getAttribute('data-theme'),color);await page.goBack();await page.waitForURL(docs+'/');await page.goForward();await page.waitForURL(ui+'/');
   });
   await scenario('click before config resolves',{ui:'https://console-staging.sociaro.ai',docs:'https://docs-staging.sociaro.ai'},async({page,documents})=>{holdConfig=true;await page.goto(ui+'/');await page.locator('#docsNav').waitFor({state:'visible'});documents.length=0;await page.locator('#docsNav').click({noWaitAfter:true});assert.equal(documents.length,0);holdConfig=false;for(const release of held.splice(0))release();await page.waitForURL(docs+'/');await footer(page);assert.equal(documents.length,1);});
   await scenario('newer Console navigation cancels pending Docs',{ui:'https://console-staging.sociaro.ai',docs:'https://docs-staging.sociaro.ai'},async({page,documents})=>{holdConfig=true;await page.goto(ui+'/');await page.locator('#docsNav').waitFor({state:'visible'});documents.length=0;await page.locator('#docsNav').click({noWaitAfter:true});await page.locator('#templatesNav').click();holdConfig=false;for(const release of held.splice(0))release();await loaded(page);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));assert.equal(page.url(),ui+'/templates');assert.equal(documents.length,0);});
   await scenario('same origin self-host',{ui:'https://self.example.test',docs:'https://self.example.test',mode:'dark'},async({page,documents})=>{await page.goto(ui+'/');await loaded(page);await clickDocs(page,documents);assert.equal(page.url(),ui+'/docs');assert.equal(documents.length,1);await footer(page);await page.getByRole('link',{name:'Docs',exact:true}).click();await page.waitForURL(ui+'/docs');});
   await scenario('legacy docs route and blocked storage',{ui:'https://console.sociaro.ai',docs:'https://docs.sociaro.ai',blocked:true},async({page,documents})=>{await page.goto(ui+'/docs?theme=dark#docs/authentication');await page.waitForURL(docs+'/#docs/authentication');await footer(page);assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');assert.equal(documents.length,2);});
   await scenario('login legal and public routes',{ui:'https://console-staging.sociaro.ai',docs:'https://docs-staging.sociaro.ai',auth:false},async({page,documents})=>{for(const route of ['/','/profile','/templates','/organizations','/api-keys','/configuration','/system','/terms','/privacy']){await page.goto(ui+route);await footer(page);}await page.goto(ui+'/');await loaded(page);documents.length=0;await page.getByRole('link',{name:'Docs',exact:true}).click();await page.waitForURL(docs+'/');await footer(page);assert.equal(documents.length,1);});
   for(const pair of [['https://console-staging.sociaro.ai','https://docs-staging.sociaro.ai'],['https://self.example.test','https://self.example.test']])await scenario('public legal shell '+pair[0],{ui:pair[0],docs:pair[1],auth:false},async({page,requests})=>{
    for(const kind of ['terms','privacy']){
     await page.goto(ui+'/'+kind);await footer(page);
     const header=page.getByRole('banner'),nav=header.getByRole('navigation',{name:'Main navigation'}),title=kind==='terms'?'Terms of Use':'Privacy Policy';
     assert.equal(await header.count(),1);await page.getByRole('heading',{name:title,exact:true}).waitFor();
     assert.equal(await page.locator('.legal-text').textContent(),'Synthetic local legal fixture.');
     assert.equal(await nav.getByRole('link',{name:'Console',exact:true}).getAttribute('href'),ui+'/?theme=light');
     assert.equal(await nav.getByRole('link',{name:'Docs',exact:true}).getAttribute('href'),docs+(docs===ui?'/docs':'/')+'?theme=light');
     assert.equal(await nav.locator('[aria-current]').count(),0,'Legal pages must not mark Console or Docs current');
     let tab='Tab';await nav.getByRole('link',{name:'Console',exact:true}).focus();await page.keyboard.press(tab);
     if(!await nav.getByRole('link',{name:'Docs',exact:true}).evaluate(n=>n===document.activeElement)){tab='Alt+Tab';await nav.getByRole('link',{name:'Console',exact:true}).focus();await page.keyboard.press(tab);}
     assert.ok(await nav.getByRole('link',{name:'Docs',exact:true}).evaluate(n=>n===document.activeElement&&n.matches(':focus-visible')));
     for(const theme of ['light','dark'])for(const width of [1440,390,320]){
      await page.evaluate(t=>GatewayTheme.apply(t),theme);await page.setViewportSize({width,height:1000});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
      const geometry=await page.evaluate(()=>{const box=s=>document.querySelector(s).getBoundingClientRect(),h=box('.topbar'),m=box('main'),f=box('.site-footer'),shell=box('.shell');return {overflow:document.documentElement.scrollWidth>innerWidth,headerBottom:h.bottom,contentTop:m.top,contentBottom:m.bottom,footerTop:f.top,headerLeft:h.left,headerRight:h.right,shellLeft:shell.left,shellRight:shell.right,padding:parseFloat(getComputedStyle(document.querySelector('main')).paddingTop)};});
      assert.equal(geometry.overflow,false);assert.ok(geometry.headerBottom<=geometry.contentTop);assert.ok(geometry.contentBottom<=geometry.footerTop+1);assert.equal(geometry.headerLeft,geometry.shellLeft);assert.equal(geometry.headerRight,geometry.shellRight);assert.equal(geometry.padding,32);
      await page.screenshot({path:path.join(out,engine+'-'+new URL(ui).hostname+'-'+kind+'-'+theme+'-'+width+'.png'),fullPage:true});
     }
     await nav.getByRole('link',{name:'Docs',exact:true}).focus();await page.keyboard.press('Enter');await page.waitForURL(docs+(docs===ui?'/docs':'/'));await footer(page);
     await page.goBack();await page.waitForURL(ui+'/'+kind);await footer(page);await page.getByRole('banner').getByRole('link',{name:'Console',exact:true}).click();await page.waitForURL(ui+'/');await page.getByRole('button',{name:'Continue with email',exact:true}).waitFor();
     legalMode='default';await page.goto(ui+'/'+kind);await page.getByRole('heading',{name:'Operator setup required',exact:true}).waitFor();await footer(page);
     for(const theme of ['light','dark'])for(const width of [1440,390,320]){
      await page.evaluate(t=>GatewayTheme.apply(t),theme);await page.setViewportSize({width,height:1000});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Bundled legal content overflow');
      const gap=await page.getByRole('contentinfo').evaluate(f=>f.getBoundingClientRect().top-document.querySelector('[data-legal-default]').getBoundingClientRect().bottom);assert.ok(gap>=79,'Long legal text retains the shared 80px footer spacing');
      await page.screenshot({path:path.join(out,engine+'-'+new URL(ui).hostname+'-'+kind+'-default-'+theme+'-'+width+'.png'),fullPage:true});
     }
     legalMode='error';await page.reload();await page.getByRole('button',{name:'Retry',exact:true}).waitFor();await footer(page);assert.equal(await page.getByRole('banner').count(),1);
     legalMode='custom';await page.getByRole('button',{name:'Retry',exact:true}).click();await page.getByText('Synthetic local legal fixture.',{exact:true}).waitFor();await page.getByRole('button',{name:'Retry',exact:true}).waitFor({state:'hidden'});
     await page.evaluate(()=>GatewayTheme.apply('light'));
    }
    assert.equal(requests.filter(r=>r.path==='/ui/app.js'||r.path==='/v1/me').length,0,'Public legal navigation never loads private console code or account preferences');
   });
   await scenario('blocked storage direct and reverse theme',{ui:'https://console.sociaro.ai',docs:'https://docs.sociaro.ai',auth:false,blocked:true,mode:'dark'},async({page,documents})=>{await page.goto(ui+'/');await loaded(page);await page.evaluate(()=>GatewayTheme.apply('dark'));documents.length=0;await page.getByRole('link',{name:'Docs',exact:true}).click();await page.waitForURL(docs+'/');await loaded(page);assert.equal(documents.length,1);assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');documents.length=0;await page.getByRole('link',{name:'Console',exact:true}).click();await page.waitForURL(ui+'/');await loaded(page);assert.equal(documents.length,1);assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');});
   for(const suffix of ['/','/docs'])await scenario('remote config failure '+suffix,{ui:'https://console.sociaro.ai',docs:'https://docs.sociaro.ai'},async({page,documents})=>{failConfig=true;await page.goto(docs+suffix);await loaded(page);await page.locator('#docsContent h1').waitFor();for(const selector of ['[data-console-link]','[data-legal-link]']){for(const link of await page.locator(selector).all()){assert.equal(await link.getAttribute('href'),null);assert.equal(await link.getAttribute('aria-disabled'),'true');}}assert.equal(documents.length,1);});
   await scenario('hung remote config is bounded',{ui:'https://console.sociaro.ai',docs:'https://docs.sociaro.ai'},async({page})=>{holdConfig=true;await page.goto(docs+'/');await page.locator('#docsContent h1').waitFor();assert.equal(await page.getByRole('contentinfo').count(),1);assert.equal(await page.locator('[data-console-link]').first().getAttribute('href'),null);});
   await scenario('public auth states and footer keyboard',{ui:'https://console-staging.sociaro.ai',docs:'https://docs-staging.sociaro.ai',auth:false},async({page,documents})=>{
    await page.goto(ui+'/');await page.getByRole('button',{name:'Continue with email',exact:true}).click();await footer(page);
    await page.locator('#authForgot').click();await page.getByRole('heading',{name:'Reset your password',exact:true}).waitFor();await footer(page);
    await page.locator('#authBack').click();await page.locator('#authSwitch').click();await page.getByRole('heading',{name:'Create your account',exact:true}).waitFor();await footer(page);
    await page.goto(ui+'/#email_reset=synthetic-local-only');await page.getByRole('heading',{name:'Choose a new password',exact:true}).waitFor();await footer(page);
    setupRequired=true;await page.goto(ui+'/');await page.getByRole('heading',{name:'Create owner account',exact:true}).waitFor();await footer(page);
    for(const theme of ['light','dark'])for(const width of [1440,390,320]){await page.evaluate(t=>GatewayTheme.apply(t),theme);await page.setViewportSize({width,height:1000});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:path.join(out,engine+'-setup-'+theme+'-'+width+'.png'),fullPage:true});}
    documents.length=0;await page.getByRole('contentinfo').getByRole('link',{name:'Terms of Use',exact:true}).focus();await page.keyboard.press('Enter');await page.waitForURL(ui+'/terms');await footer(page);assert.equal(documents.length,1);
   });
   await scenario('config error same-origin fallback',{ui:'https://console.sociaro.ai',docs:'https://docs.sociaro.ai'},async({page,documents})=>{failConfig=true;await page.goto(ui+'/');await loaded(page);await page.locator('#docsNav').waitFor({state:'visible'});documents.length=0;await page.locator('#docsNav').click();await page.waitForURL(ui+'/docs');await page.locator('#docsContent h1').waitFor();assert.equal(documents.length,1);assert.equal(await page.getByRole('contentinfo').count(),1);});
  }
  console.log('PASS '+engine+': '+results.length+' scenarios; real Caddy asset policy and actual Console/Login clicks');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>{if(caddy)caddy.kill('SIGTERM');upstream.closeAllConnections();upstream.close();});
