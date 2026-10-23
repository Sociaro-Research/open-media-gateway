// Local intercepted HTTPS fixtures; no real hosts, credentials or providers.
const {chromium,webkit}=require('playwright'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium';
const out=process.env.UI_EVIDENCE_DIR||'/tmp/gateway-documentation-theme';fs.mkdirSync(out,{recursive:true});
(async()=>{
 const browser=await ({chromium,webkit}[engine]).launch({headless:true});const results=[];
 async function scenario(name,ui,docs,domain,run,{blocked=false}={}){
  const context=await browser.newContext({colorScheme:'light'}),errors=[],requests=[];
  if(blocked)await context.addInitScript(()=>{Object.defineProperty(window,'localStorage',{get(){throw new DOMException('Storage blocked','SecurityError')}});Object.defineProperty(Document.prototype,'cookie',{get(){return ''},set(){throw new DOMException('Cookies blocked','SecurityError')}})});
  await context.route('**/*',async route=>{
   const url=new URL(route.request().url());requests.push({url:url.href,cookie:route.request().headers().cookie||''});
   assert.ok([ui,docs].includes(url.origin),'unexpected origin '+url.origin);
   if(url.pathname==='/site.json')return route.fulfill({headers:{'Content-Type':'application/json','X-Gateway-Theme-Cookie-Domain':domain},body:JSON.stringify({ui_url:ui,docs_url:docs===ui?'':docs})});
   if(url.pathname==='/openapi.json')return route.fulfill({json:{openapi:'3.1.0',info:{title:'Local fixture',version:'test'},paths:{},tags:[],components:{schemas:{}}}});
   const isDocs=url.pathname==='/docs'||url.origin===docs&&docs!==ui;
   if(url.pathname.startsWith('/ui/')){const file=path.join(root,path.basename(url.pathname));return route.fulfill({path:file,contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});}
   if(isDocs)return route.fulfill({path:path.join(root,'documentation.html'),contentType:'text/html'});
   return route.fulfill({contentType:'text/html',body:'<!doctype html><html><head><script src="/ui/theme.js"></script></head><body><a id="docs" href="/docs">Docs</a><script src="/ui/site.js"></script></body></html>'});
  });
  const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
  const load=async(p,url)=>{await p.goto(url);await p.waitForFunction(()=>window.GatewaySite);await p.evaluate(()=>GatewaySite.ready)};
  const theme=async(p,want)=>{await p.waitForFunction(v=>document.documentElement.dataset.theme===v,want)};
  try{await run({context,page,load,theme,ui,docs});assert.deepEqual(errors,[]);assert.ok(!requests.some(r=>/\/v1\/|\/auth\//.test(r.url)));results.push({name,passed:true,requests:requests.length});}finally{await context.close()}
 }
 try{
 await scenario('same origin saved preference and logout','https://app.example.test','https://app.example.test','',async({context,page,load,theme,ui})=>{
  await load(page,ui+'/');await page.evaluate(()=>GatewayTheme.apply('dark',{share:true}));const doc=await context.newPage();await load(doc,ui+'/docs');await theme(doc,'dark');await page.evaluate(()=>GatewayTheme.clear());await doc.evaluate(()=>dispatchEvent(new Event('focus')));await theme(doc,'light');await doc.emulateMedia({colorScheme:'dark'});await theme(doc,'dark');
 });
 await scenario('sibling cookie and direct docs','https://app.example.test','https://docs.example.test','example.test',async({context,page,load,theme,ui,docs})=>{
  await load(page,ui+'/');assert.equal((await context.cookies()).length,0);await page.evaluate(()=>GatewayTheme.apply('dark',{share:true}));const cookies=await context.cookies();assert.equal(cookies.length,1);assert.equal(cookies[0].name,'gateway-docs-theme');assert.equal(cookies[0].value,'dark');assert.equal(cookies[0].domain,'.example.test');assert.equal(cookies[0].secure,true);assert.equal(cookies[0].sameSite,'Lax');
  await context.addCookies([{name:'fixture-session',value:'local-only',url:ui,secure:true,httpOnly:true,sameSite:'Lax'}]);
  const doc=await context.newPage();await load(doc,docs+'/');await theme(doc,'dark');assert.equal((await context.cookies(docs)).some(c=>c.name==='fixture-session'),false);
  for(const width of [1440,390,320]){await doc.setViewportSize({width,height:900});await doc.screenshot({path:path.join(out,engine+'-shared-dark-'+width+'.png')});}
  await page.evaluate(()=>GatewayTheme.apply('system',{share:true}));await doc.evaluate(()=>dispatchEvent(new Event('focus')));await theme(doc,'light');await doc.emulateMedia({colorScheme:'dark'});await theme(doc,'dark');
  await page.evaluate(()=>GatewayTheme.apply('light',{share:true}));await doc.evaluate(()=>dispatchEvent(new Event('focus')));await theme(doc,'light');await page.evaluate(()=>GatewayTheme.clear());await doc.evaluate(()=>dispatchEvent(new Event('focus')));await theme(doc,'dark');assert.equal((await context.cookies()).filter(c=>c.name==='gateway-docs-theme').length,0);assert.equal((await context.cookies()).find(c=>c.name==='fixture-session').domain,'app.example.test');
 });
 for(const blocked of [false,true])await scenario('different domains, blocked='+blocked,'https://app.example.test','https://reference.other.test','',async({context,page,load,theme,ui,docs})=>{
  await load(page,ui+'/');await page.evaluate(()=>GatewayTheme.apply('dark',{share:true}));const target=await page.evaluate(()=>GatewayTheme.docsURL('/docs#docs/intro'));await load(page,target);await page.waitForURL(docs+'/#docs/intro');await theme(page,'dark');assert.ok(!page.url().includes('theme='));assert.equal((await context.cookies()).length,0);
  const direct=await context.newPage();await load(direct,docs+'/');await theme(direct,'light');await load(direct,docs+'/?theme=invalid');await theme(direct,'light');await load(direct,docs+'/?theme=system');await direct.emulateMedia({colorScheme:'dark'});await theme(direct,'dark');
 },{blocked});
 await scenario('sibling transfer when storage blocked','https://app.example.test','https://docs.example.test','example.test',async({page,load,theme,ui,docs})=>{
  await load(page,ui+'/');await page.evaluate(()=>GatewayTheme.apply('dark',{share:true}));await load(page,await page.evaluate(()=>GatewayTheme.docsURL('/docs')));await page.waitForURL(docs+'/');await theme(page,'dark');
 },{blocked:true});
 for(const domain of ['other.test','test','example.test; Secure','https://example.test','.example.test'])await scenario('reject domain '+domain,'https://app.example.test','https://docs.example.test',domain,async({context,page,load,ui})=>{await load(page,ui+'/');await page.evaluate(()=>GatewayTheme.apply('dark',{share:true}));assert.equal((await context.cookies()).length,0)});
 await scenario('public suffix rejected by browser','https://app.co.uk','https://docs.co.uk','co.uk',async({context,page,load,ui})=>{await load(page,ui+'/');await page.evaluate(()=>GatewayTheme.apply('dark',{share:true}));assert.equal((await context.cookies()).length,0)});
 await scenario('invalid stored values','https://app.example.test','https://app.example.test','',async({page,load,theme,ui})=>{await load(page,ui+'/');await page.evaluate(()=>localStorage.setItem('gateway-theme','invalid'));await load(page,ui+'/docs');await theme(page,'light')});
 await scenario('invalid shared cookie','https://app.example.test','https://docs.example.test','example.test',async({context,page,load,theme,docs})=>{await context.addCookies([{name:'gateway-docs-theme',value:'invalid',domain:'.example.test',path:'/',secure:true,sameSite:'Lax'}]);await load(page,docs+'/');await theme(page,'light')});
 fs.writeFileSync(path.join(out,engine+'.json'),JSON.stringify(results,null,2));console.log(engine+': '+results.length+' scenarios passed');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
