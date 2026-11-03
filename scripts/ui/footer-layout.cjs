// Actual portal modules with synthetic HTTPS routes; no live account or provider.
const {chromium,webkit}=require('playwright'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'../../web/public'),out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/footer-layout'),engine=process.env.UI_BROWSER_ENGINE||'chromium';fs.mkdirSync(out,{recursive:true});
const ui='https://console-staging.fixture.test',docs='https://docs-staging.fixture.test',errors=[],external=[],geometry=[];
(async()=>{const browser=await({chromium,webkit}[engine]).launch({headless:true}),context=await browser.newContext({viewport:{width:1440,height:360}});
 await context.addInitScript(()=>localStorage.setItem('gateway-theme','light'));
 await context.route('**/*',async route=>{const req=route.request(),u=new URL(req.url());if(![ui,docs].includes(u.origin)){external.push(u.origin);return route.abort();}
  const json=(body,status=200)=>route.fulfill({status,json:body});
  if(u.pathname==='/site.json')return json({ui_url:ui,api_url:ui,docs_url:docs,branding:{name:'Footer fixture',version:1,dark:{},light:{}}});
  if(u.pathname==='/auth/session')return route.fulfill({status:204,body:''});
  if(u.pathname==='/auth/methods')return json({email:true,email_delivery:true,google:false});
  if(u.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,version:1}});
  if(u.pathname==='/v1/me')return json({id:'fixture',name:'Footer fixture',theme:'light',is_system_admin:true});
  if(u.pathname==='/v1/system/settings')return json({generation_ui:true,personal_providers:true,version:1});
  if(u.pathname==='/openapi.json')return json({openapi:'3.1.0',info:{title:'Synthetic API',version:'fixture'},paths:{}});
  if(u.pathname.startsWith('/legal/'))return json({text:'Synthetic legal fixture.'});
  if(u.pathname.startsWith('/v1/'))return json({data:[]});
  const name=u.pathname.startsWith('/ui/')?u.pathname.slice(1):u.origin===docs?'ui/documentation.html':'ui/index.html',file=path.join(root,name);
  if(!file.startsWith(root+path.sep)||!fs.existsSync(file))return route.fulfill({status:404,body:''});
  return route.fulfill({path:file,contentType:file.endsWith('.css')?'text/css':file.endsWith('.js')?'text/javascript':'text/html'});
 });
 const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(10000);
 try{await page.goto(ui+'/');await page.evaluate(()=>GatewaySite.ready);await page.locator('#generationView').waitFor({state:'visible'});
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.evaluate(t=>GatewayTheme.apply(t),theme);await page.setViewportSize({width,height:360});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const value=await page.evaluate(()=>{const f=document.querySelector('.site-footer').getBoundingClientRect(),content=document.querySelector('#generationView .layout').getBoundingClientRect();return {gap:f.top-content.bottom,expectedGap:parseFloat(getComputedStyle(document.querySelector('#configurationView')).paddingBottom),document:document.documentElement.scrollWidth,viewport:innerWidth};});geometry.push({theme,width,...value});
   fs.writeFileSync(path.join(out,'geometry.json'),JSON.stringify(geometry,null,2));await page.screenshot({path:path.join(out,theme+'-'+width+'.png'),fullPage:true});
   assert.ok(value.gap>=value.expectedGap-1,'Playground footer gap '+value.gap+' must retain portal section spacing '+value.expectedGap);assert.ok(value.document<=value.viewport,'Horizontal page overflow');
  }
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);console.log('PASS '+engine+': real Playground footer gap; light/dark1440/390/320, no external requests');
 }finally{fs.writeFileSync(path.join(out,'result.json'),JSON.stringify({engine,geometry,errors,external},null,2));await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
