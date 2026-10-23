const {evidencePath} = require('./evidence.cjs');
// Static frontend regression: never paint/request a repository logo before settings arrive.
const {chromium}=require('playwright');
const fs=require('node:fs'),http=require('node:http'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const files={'/system':'index.html','/login':'login.html','/docs':'documentation.html','/privacy':'privacy.html','/terms':'terms.html'};
const svg=Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 30"><rect fill="#226644" width="120" height="30"/></svg>');
const site={branding:{name:'Configured Brand',description:'Configured description',contact_email:'support@example.test',dark:{accent:'#226644'},light:{accent:'#226644'},assets:{logo:{mime:'image/svg+xml',data:null}},version:12}};
const server=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://localhost').pathname;
 if(p.startsWith('/branding/assets/')){res.writeHead(200,{'Content-Type':'image/svg+xml'});return res.end(svg);}
 if(p==='/legal/privacy'||p==='/legal/terms'){res.writeHead(200,{'Content-Type':'application/json'});return res.end(JSON.stringify({text:''}));}
 if(p==='/auth/methods'){res.writeHead(200,{'Content-Type':'application/json'});return res.end(JSON.stringify({google:true,email:false,email_delivery:false}));}
 if(p.startsWith('/v1/')){res.writeHead(200,{'Content-Type':'application/json'});return res.end(JSON.stringify(p==='/v1/me'?{id:'fixture',email:'user@example.test',is_system_admin:true}:p==='/v1/info'?{auth_mode:'google',console:{generation_ui:true}}:{data:[]}));}
 const name=files[p]||(p.startsWith('/ui/')?p.slice(4):null),file=name&&path.join(root,name);
 if(!file||!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');
 res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 fs.mkdirSync(evidencePath('/tmp/gateway-branding-ui'),{recursive:true});
 try{
  for(const theme of ['dark','light'])for(const [url]of Object.entries(files)){
   const page=await browser.newPage({colorScheme:theme,viewport:{width:1440,height:1000}});
   const requests=[],errors=[];page.on('request',r=>requests.push(r.url()));page.on('pageerror',e=>errors.push(e.message));
   let resolveConfig,release,resolveGate;
   const gate=new Promise(r=>resolveGate=r);
   const pending=new Promise(r=>resolveConfig=r);
   await page.route('**/site.json',async route=>{release=resolveGate;resolveConfig();await gate;await route.fulfill({json:site});});
   // The route stays pending while we inspect the first static paint.
   await page.goto(base+url,{waitUntil:'domcontentloaded'});await pending;
   assert.equal(await page.locator('.brand img[src]').count(),0,url+' requested an image before config');
   assert.equal(await page.locator('.brand img:visible').count(),0);
   assert.equal(await page.locator('link[rel=icon],link[rel=apple-touch-icon]').count(),0);
   assert.ok(!requests.some(u=>u.includes('/branding/assets/')));
   await release();await page.evaluate(()=>GatewaySite.ready);
   await page.waitForFunction(()=>[...document.querySelectorAll('.brand img')].every(n=>!n.hidden&&n.complete&&n.naturalWidth>0));
   const sources=await page.locator('.brand img').evaluateAll(nodes=>nodes.map(n=>n.getAttribute('src')));
   assert.ok(sources.every(u=>u==='/branding/assets/logo?v=12'));
   assert.ok((await page.title()).includes('Configured Brand'),url+' title='+await page.title()+' errors='+JSON.stringify(errors));
   assert.ok(!requests.some(u=>/sociaro-(logo|symbol)/i.test(u)));
   if(url==='/privacy'||url==='/terms'){
    await page.locator('[data-legal-default]').waitFor();
    assert.ok((await page.locator('[data-legal-default]').innerText()).includes('[CONTACT EMAIL]'),'Operator template contact placeholder must remain explicit');
    assert.ok(!(await page.locator('body').innerText()).includes('Sociaro'));
   }
   await page.evaluate(()=>GatewayTheme.apply('light'));
   await page.waitForFunction(()=>document.querySelector('.brand img').naturalWidth>0&&!document.querySelector('.brand img').hidden);
   for(const width of url==='/system'?[390,320]:[390]){
    await page.setViewportSize({width,height:900});
    await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth);
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'overflow '+url+' '+width);
   }
   // Clearing saved assets cannot leave a stale src or favicon.
   await page.evaluate(s=>GatewaySite.apply({...s.branding,name:'',contact_email:'',assets:{}}),site);
   assert.equal(await page.locator('.brand img[src]').count(),0);
   assert.equal(await page.locator('.brand img:visible').count(),0);
   assert.equal(await page.locator('link[rel=icon],link[rel=apple-touch-icon]').count(),0);
   if(url==='/privacy'||url==='/terms')assert.ok((await page.locator('[data-legal-default]').innerText()).includes('[CONTACT EMAIL]'),'Brand reset must not rewrite legal text');
   assert.deepEqual(errors,[]);
   await page.close();
  }
  // Configuration failure keeps a fresh load unbranded.
  const page=await browser.newPage();
  await page.route('**/site.json',r=>r.fulfill({status:503,body:'unavailable'}));
  await page.goto(base+'/login');await page.evaluate(()=>GatewaySite.ready);
  assert.equal(await page.locator('.brand img[src]').count(),0);
  assert.equal(await page.locator('link[rel=icon],link[rel=apple-touch-icon]').count(),0);
  // A failed uploaded image must not expose a broken-image fallback.
  await page.route('**/branding/assets/**',r=>r.fulfill({status:404,body:''}));
  await page.evaluate(s=>GatewaySite.apply(s.branding),site);
  await page.waitForFunction(()=>document.querySelector('.brand img').complete);
  assert.equal(await page.locator('.brand img:visible').count(),0);
  await page.screenshot({path:evidencePath('/tmp/gateway-branding-ui/empty-brand-login.png')});
  await page.close();
  console.log('PASS: delayed settings on five pages in both themes; uploaded-only images; no old asset requests; empty/reset/failed settings and images; 320/390px layouts.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
