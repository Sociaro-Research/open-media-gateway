// Verify real Caddy routing and the independently served UI against an isolated API fixture.
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const {[engine]:chromium}=require('playwright');
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),{spawn}=require('node:child_process');
const root=path.resolve(__dirname,'../..'),tmp=fs.mkdtempSync(path.join(os.tmpdir(),'gateway-ui-routing-'));
const listen=server=>new Promise(resolve=>server.listen(0,'127.0.0.1',()=>resolve(server.address().port)));
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
let caddy,browser;
const brand={name:'White <Label>',description:'Independent frontend',contact_email:'support@example.test',version:3,dark:{accent:'#aa11bb'},light:{accent:'#226644'},assets:{logo:{mime:'image/svg+xml'},logo_light:{mime:'image/svg+xml'}},site_max_width:1280,content_max_width:1000,sidebar_subsections:true};
const upstream=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://localhost').pathname;
 const json=data=>{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
 if(p==='/auth/session')return res.writeHead(req.headers.cookie?.includes('test_session=alice')?204:401,{'Cache-Control':'no-store'}).end();
 if(p==='/legal/privacy'||p==='/legal/terms')return json({text:''});
 if(p==='/site.json')return json({branding:brand,api_url:'https://api.example.test',ui_url:'https://console.example.test',docs_url:''});
 if(p==='/auth/methods')return json({email:true,email_delivery:true,google:true});
 if(p==='/openapi.json')return json({openapi:'3.1.0',info:{title:'Fixture',version:'1'},tags:[{name:'Account'}],components:{schemas:{}},paths:{'/v1/me':{patch:{summary:'Update profile',security:[{sessionAuth:[]}],tags:['Account'],operationId:'update_profile',responses:{'200':{description:'Updated profile'}}}}},servers:[{url:'https://api.example.test'}]});
 if(p==='/openapi.yaml')return res.writeHead(200,{'Content-Type':'application/yaml'}).end('openapi: 3.1.0\n');
 if(p.startsWith('/branding/assets/'))return res.writeHead(200,{'Content-Type':'image/svg+xml'}).end('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 20"><rect width="100" height="20" fill="blue"/></svg>');
 if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,version:1}});
 if(p==='/v1/me')return json({id:'alice',name:'Alice',email:'alice@example.test',is_system_admin:true});
 if(p==='/v1/system/settings')return json({generation_ui:true,personal_providers:true,version:1});
 if(p.startsWith('/v1/'))return json({data:[]});
 return res.writeHead(418).end();
});
(async()=>{
 const apiPort=await listen(upstream),reservation=http.createServer(),port=await listen(reservation);await new Promise(r=>reservation.close(r));
 const env={...process.env,UI_ROOT:path.join(root,'web/public'),GATEWAY_UPSTREAM:'127.0.0.1:'+apiPort,UI_LISTEN:':'+port,DOCS_HOST:'localhost',THEME_COOKIE_DOMAIN:'example.test'};
 const log=fs.openSync(path.join(tmp,'caddy.log'),'w');
 caddy=spawn(process.env.CADDY_BIN||'caddy',['run','--config',process.env.CADDY_CONFIG||path.join(root,'web/Caddyfile'),'--adapter','caddyfile'],{env,stdio:['ignore',log,log]});fs.closeSync(log);
 const base='http://127.0.0.1:'+port;
 let ready=false;for(let i=0;i<100;i++){try{const r=await fetch(base+'/ui/site.js');if(r.status===200){ready=true;break}}catch{}if(caddy.exitCode!==null)throw Error(fs.readFileSync(path.join(tmp,'caddy.log'),'utf8'));await delay(100)}assert.ok(ready,'Caddy did not start');
 const siteResponse=await fetch(base+'/site.json');assert.equal(siteResponse.headers.get('X-Gateway-Theme-Cookie-Domain'),'example.test');
 const request=(p,code,headers={},method='GET')=>new Promise((resolve,reject)=>{
  const req=http.request(base+p,{headers,method},res=>{let body='';res.on('data',chunk=>body+=chunk);res.on('end',()=>{try{assert.equal(res.statusCode,code,p);resolve({text:async()=>body,headers:{get:name=>res.headers[name.toLowerCase()]}})}catch(error){reject(error)}})});req.on('error',reject);req.end();
 });
 for(const p of ['/','/system','/organizations','/profile','/api-keys','/configuration']){const r=await request(p,200);assert.match(await r.text(),/Sign in to get started/);assert.equal(r.headers.get('cache-control'),'no-store');assert.ok(r.headers.get('vary').split(',').map(s=>s.trim()).includes('Cookie'));}
 assert.match(await (await request('/',200,{Authorization:'Bearer fixture-key'})).text(),/Sign in to get started/);
 for(const p of ['/ui/app.js','/ui/payments.js','/provider-assets/mock/icon.png'])await request(p,401);
 for(const p of ['/ui/index.html','/ui/privacy.html','/ui/terms.html','/ui/']){await request(p,404);await request(p,404,{Cookie:'test_session=alice'});}
 for(const p of ['/ui/app.js','/ui/report-details.js'])await request(p,200,{Cookie:'test_session=alice'});
 assert.match(await (await request('/system',200,{Cookie:'test_session=alice'})).text(),/generationView/);
 for(const p of ['/privacy','/terms','/docs','/ui/site.js','/ui/login.js','/ui/styles.css','/ui/legal.js','/legal/privacy','/legal/terms'])await request(p,200);
 const legalCSS=await request('/ui/legal.css?v=20260911-console-consistency',200);assert.match(legalCSS.headers.get('content-type'),/^text\/css/);assert.equal(await legalCSS.text(),fs.readFileSync(path.join(root,'web/public/ui/legal.css'),'utf8'));
 for(const p of ['/v1/test','/auth/google/callback','/media/test','/references/test'])await request(p,p==='/v1/test'?200:418);
 const docs={Host:'localhost'};
 for(const p of ['/','/docs','/openapi.json','/site.json','/ui/docs.js','/ui/site.js'])await request(p,200,docs);
 for(const p of ['/v1/info','/auth/session','/media/test','/ui/app.js','/ui/index.html','/system','/privacy'])await request(p,404,{...docs,Cookie:'test_session=alice',Authorization:'Bearer fixture-key'});
 await request('/site.json',404,docs,'POST');
 browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_ENGINE!=='webkit'&&process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const context=await browser.newContext(),page=await context.newPage(),errors=[];page.on('pageerror',e=>{if(process.env.UI_BROWSER_ENGINE==='webkit'&&e.message==='ResizeObserver loop completed with undelivered notifications.'){console.warn('WebKit resize notice: '+e.message);return;}errors.push(e.message);});
 const screenshots=process.env.UI_SCREENSHOT_DIR||path.join(tmp,'screenshots');fs.mkdirSync(screenshots,{recursive:true});
 for(const theme of ['dark','light'])for(const width of [1440,390,320]){
  await page.setViewportSize({width,height:1000});await page.emulateMedia({colorScheme:theme});await page.goto(base+'/');await page.evaluate(()=>GatewaySite.ready);
  assert.equal(await page.title(),'Sign in · '+brand.name);assert.equal(await page.locator('html').evaluate(n=>getComputedStyle(n).getPropertyValue('--accent').trim()),brand[theme].accent);
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Login overflow '+width);
  assert.ok((await page.locator('.brand img').first().getAttribute('src')).includes(theme==='light'?'logo_light':'logo'));
  await page.getByRole('button',{name:'Continue with email',exact:true}).click();await page.getByRole('textbox',{name:'Email',exact:true}).focus();assert.ok(await page.evaluate(()=>document.activeElement?.tagName==='INPUT'));await page.keyboard.press('Tab');
  await page.screenshot({path:path.join(screenshots,`login-${theme}-${width}.png`)});
 }
 for(const theme of ['dark','light'])for(const width of [1440,390,320])for(const legal of ['/privacy','/terms']){
  assert.deepEqual(await context.cookies(),[],'Legal pages must be tested without a session');
  await page.setViewportSize({width,height:1000});await page.emulateMedia({colorScheme:theme});
  const cssResponse=page.waitForResponse(r=>new URL(r.url()).pathname==='/ui/legal.css');
  await page.goto(base+legal);await page.evaluate(()=>GatewaySite.ready);
  assert.equal((await cssResponse).status(),200,'Anonymous legal stylesheet');
  await page.getByRole('heading',{name:legal==='/privacy'?'Privacy Policy':'Terms of Use',exact:true}).waitFor();
  const layout=await page.locator('main').evaluate(n=>({padding:getComputedStyle(n).paddingTop,width:n.getBoundingClientRect().width,bodyWidth:document.body.getBoundingClientRect().width,clientWidth:document.documentElement.clientWidth}));
  assert.equal(layout.padding,'64px','Legal stylesheet applied');
  if(Math.abs(layout.width-Math.min(760,width-40))>=1)console.log('Legal viewport measurement',JSON.stringify({legal,theme,viewportWidth:width,...layout}));
  assert.ok(Math.abs(layout.width-Math.min(760,layout.bodyWidth-40))<1,'Legal content width '+JSON.stringify(layout));
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Legal page overflow '+legal+' '+width);
  await page.screenshot({path:path.join(screenshots,`${legal.slice(1)}-${theme}-${width}.png`)});
  const home=page.locator('a.back[href="/"]');assert.equal(await home.getAttribute('aria-label'),brand.name);await home.focus();assert.ok(await home.evaluate(n=>n===document.activeElement));await page.keyboard.press('Enter');
  await page.waitForURL(base+'/');await page.getByText('Sign in to get started',{exact:true}).waitFor();
 }
 await context.addCookies([{name:'test_session',value:'alice',url:base}]);
 for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.goto(base+'/');await page.evaluate(()=>GatewaySite.ready);await page.locator('#generationView').waitFor();await page.waitForFunction(()=>[...document.querySelectorAll('.brand img')].every(n=>n.complete&&n.naturalHeight));await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));const mark=await page.locator('.brand').boundingBox(),nav=await page.locator('#sectionNav').boundingBox();assert.ok(mark.x+mark.width<=nav.x+1||mark.y+mark.height<=nav.y+1,'Brand overlaps navigation '+width);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Console overflow '+width);await page.screenshot({path:path.join(screenshots,`console-${width}.png`)});}
 await page.goto('http://localhost:'+port+'/');await page.evaluate(()=>GatewaySite.ready);await page.waitForFunction(()=>document.title.includes('White <Label>'));
 await page.getByRole('heading',{name:'Build with Gateway.',exact:true}).waitFor();assert.equal(await page.locator('.operation-error').count(),0);await page.locator('#docsSearch').fill('patch');await page.locator('#docsMenu a').filter({hasText:'PATCH'}).click();await page.getByRole('heading',{name:'Update profile',exact:true}).waitFor();assert.ok((await page.locator('#docsContent').innerText()).includes('GATEWAY_COOKIE_JAR'));assert.ok(!(await page.locator('#docsContent').innerText()).includes('Bearer $GATEWAY_API_KEY'));assert.equal(await page.locator('[data-console-link]').first().getAttribute('href'),'https://console.example.test/');assert.equal(await page.locator('meta[name="gateway-url"]').getAttribute('content'),'https://api.example.test');
 await page.screenshot({path:path.join(screenshots,'docs-320.png')});assert.deepEqual(errors,[]);console.log('PASS: independent Caddy UI, anonymous styled Privacy/Terms and keyboard Home, private session gating, API/capability proxying, docs isolation, branding, both themes and 1440/390/320px layouts. Screenshots: '+screenshots);
})().catch(error=>{console.error(error);process.exitCode=1}).finally(async()=>{await browser?.close();upstream.close();if(caddy&&caddy.exitCode===null){caddy.kill('SIGTERM');await new Promise(r=>caddy.once('exit',r))}});
