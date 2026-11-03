// Actual portal modules with synthetic HTTPS routes; no live account or provider.
const {chromium,webkit}=require('playwright'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'../../web/public'),out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/footer-layout'),engine=process.env.UI_BROWSER_ENGINE||'chromium';fs.mkdirSync(out,{recursive:true});
let ui='https://console-staging.fixture.test',docs='https://docs-staging.fixture.test';const errors=[],external=[],geometry=[],footerGeometry=[];
(async()=>{const browser=await({chromium,webkit}[engine]).launch({headless:true}),context=await browser.newContext({viewport:{width:1440,height:360}});
 await context.addInitScript(()=>localStorage.setItem('gateway-theme','light'));
 await context.route('**/*',async route=>{const req=route.request(),u=new URL(req.url());if(![ui,docs].includes(u.origin)){external.push(u.origin);return route.abort();}
  const json=(body,status=200)=>route.fulfill({status,json:body,headers:{'Access-Control-Allow-Origin':docs}});
  if(u.pathname==='/site.json')return json({ui_url:ui,api_url:ui,docs_url:docs,branding:{name:'Footer fixture',version:1,dark:{accent:'#c7e888','accent-text':'#c7e888'},light:{accent:'#536b00','accent-text':'#536b00'}}});
  if(u.pathname==='/auth/session')return route.fulfill({status:204,body:''});
  if(u.pathname==='/auth/methods')return json({email:true,email_delivery:true,google:false});
  if(u.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,version:1}});
  if(u.pathname==='/v1/me')return json({id:'fixture',name:'Footer fixture',theme:'light',is_system_admin:true});
  if(u.pathname==='/v1/system/settings')return json({generation_ui:true,personal_providers:true,version:1});
  if(u.pathname==='/openapi.json')return json({openapi:'3.1.0',info:{title:'Synthetic API',version:'fixture'},tags:[],paths:{},components:{schemas:{}}});
  if(u.pathname.startsWith('/legal/'))return json({text:'Synthetic legal fixture.'});
  if(u.pathname.startsWith('/v1/'))return json({data:[]});
  const name=u.pathname.startsWith('/ui/')?u.pathname.slice(1):((u.origin===docs&&docs!==ui)||u.pathname==='/docs')?'ui/documentation.html':'ui/index.html',file=path.join(root,name);
  if(!file.startsWith(root+path.sep)||!fs.existsSync(file))return route.fulfill({status:404,body:''});
  return route.fulfill({path:file,contentType:file.endsWith('.css')?'text/css':file.endsWith('.js')?'text/javascript':'text/html'});
 });
 const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(10000);
 let nativeTab='Tab';await page.setContent('<input id=tabStart><a id=tabLink href=\"#native\">Native link</a><input id=tabEnd>');await page.locator('#tabStart').focus();await page.keyboard.press(nativeTab);if(!await page.locator('#tabLink').evaluate(n=>n===document.activeElement)){nativeTab='Alt+Tab';await page.locator('#tabStart').focus();await page.keyboard.press(nativeTab);assert.equal(await page.locator('#tabLink').evaluate(n=>n===document.activeElement),true,'Native full-control keyboard shortcut reaches an ordinary link');}
 try{await page.goto(ui+'/');await page.evaluate(()=>GatewaySite.ready);await page.locator('#generationView').waitFor({state:'visible'});
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.evaluate(t=>GatewayTheme.apply(t),theme);await page.setViewportSize({width,height:360});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const value=await page.evaluate(()=>{const f=document.querySelector('.site-footer').getBoundingClientRect(),content=document.querySelector('#generationView .layout').getBoundingClientRect();return {gap:f.top-content.bottom,expectedGap:parseFloat(getComputedStyle(document.querySelector('#configurationView')).paddingBottom),document:document.documentElement.scrollWidth,viewport:innerWidth};});geometry.push({theme,width,...value});
   fs.writeFileSync(path.join(out,'geometry.json'),JSON.stringify(geometry,null,2));await page.screenshot({path:path.join(out,theme+'-'+width+'.png'),fullPage:true});
   assert.ok(value.gap>=value.expectedGap-1,'Playground footer gap '+value.gap+' must retain portal section spacing '+value.expectedGap);assert.ok(value.document<=value.viewport,'Horizontal page overflow');
  }
  for(const pair of [['https://console-staging.fixture.test','https://docs-staging.fixture.test'],['https://console.fixture.test','https://docs.fixture.test'],['https://self.fixture.test','https://self.fixture.test']]){
   [ui,docs]=pair;
   for(const surface of ['console','docs']){
    await page.goto(surface==='docs'?(docs===ui?docs+'/docs':docs+'/'):ui+'/');await page.evaluate(()=>GatewaySite.ready);if(surface==='docs'){await page.locator('#docsContent h1').waitFor();assert.equal(await page.getByText(/Could not load API reference/).count(),0);}const footer=page.getByRole('contentinfo');assert.equal(await footer.count(),1);
    const copyright=footer.getByText('© 2026 Sociaro Baltic OÜ',{exact:true});assert.equal(await copyright.count(),1);assert.equal(await copyright.evaluate(n=>n.tagName==='SPAN'&&n.tabIndex===-1&&!n.closest('a,button')),true);
    const terms=footer.getByRole('link',{name:'Terms of Use',exact:true}),privacy=footer.getByRole('link',{name:'Privacy Policy',exact:true});assert.equal(await terms.getAttribute('href'),ui+'/terms');assert.equal(await privacy.getAttribute('href'),ui+'/privacy');assert.equal(await footer.getByRole('link').count(),2);
    for(const theme of ['light','dark'])for(const width of [1440,390,320]){
     await page.evaluate(t=>GatewayTheme.apply(t),theme);await page.setViewportSize({width,height:700});await footer.scrollIntoViewIfNeeded();await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));await page.mouse.move(0,0);
     const normal=await terms.evaluate(n=>getComputedStyle(n).textDecorationLine);assert.equal(normal,'none');await terms.hover();
     const hover=await terms.evaluate(n=>{const probe=document.createElement('span');probe.style.color='var(--accent-text,var(--accent))';probe.style.backgroundColor='var(--bg)';document.body.append(probe);const color=getComputedStyle(probe).color,background=getComputedStyle(probe).backgroundColor;probe.remove();return {background,color:getComputedStyle(n).color,expected:color,decoration:getComputedStyle(n).textDecorationLine};});assert.equal(hover.color,hover.expected);assert.equal(hover.decoration,'none');const luminance=color=>color.match(/[0-9.]+/g).slice(0,3).map(Number).map(x=>{x/=255;return x<=.04045?x/12.92:((x+.055)/1.055)**2.4;}).reduce((sum,x,i)=>sum+x*[.2126,.7152,.0722][i],0),fg=luminance(hover.color),bg=luminance(hover.background);assert.ok((Math.max(fg,bg)+.05)/(Math.min(fg,bg)+.05)>=4.5,'Configured footer foreground contrast');
     await terms.focus();await page.keyboard.press(nativeTab);assert.equal(await privacy.evaluate(n=>n===document.activeElement&&n.matches(':focus-visible')),true);const focus=await privacy.evaluate(n=>({outline:getComputedStyle(n).outlineStyle,width:parseFloat(getComputedStyle(n).outlineWidth),decoration:getComputedStyle(n).textDecorationLine}));assert.notEqual(focus.outline,'none');assert.ok(focus.width>=2);assert.equal(focus.decoration,'none');
     const bounds=await footer.evaluate(f=>{const box=n=>{const r=n.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom};};return {footer:box(f),copyright:box(f.querySelector('.site-footer-copyright')),links:box(f.querySelector('nav')),document:document.documentElement.scrollWidth,viewport:innerWidth};});assert.ok(Math.abs(bounds.copyright.left-bounds.footer.left)<=1);assert.ok(Math.abs(bounds.links.right-bounds.footer.right)<=1);assert.ok(bounds.copyright.right+1<=bounds.links.left||bounds.copyright.bottom<=bounds.links.top,'Footer groups overlap');assert.ok(bounds.document<=bounds.viewport);footerGeometry.push({surface,ui,theme,width,...bounds,hover,focus});
     await page.screenshot({path:path.join(out,new URL(ui).hostname+'-'+surface+'-'+theme+'-'+width+'.png'),fullPage:true});
    }
   }
  }
  fs.writeFileSync(path.join(out,'footer-geometry.json'),JSON.stringify(footerGeometry,null,2));
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);console.log('PASS '+engine+': real Playground footer gap; light/dark1440/390/320, shared copyright, configured primary hover, no underline, keyboard focus, console/docs and same-origin targets; no external requests');
 }finally{fs.writeFileSync(path.join(out,'result.json'),JSON.stringify({engine,nativeTab,geometry,footerGeometry,errors,external},null,2));await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
