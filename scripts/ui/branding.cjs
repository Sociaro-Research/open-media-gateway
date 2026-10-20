const {evidencePath} = require('./evidence.cjs');
// Isolated appearance workflow; API writes stay in this localhost fixture.
const {chromium}=require('playwright');
const fs=require('node:fs'),http=require('node:http'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const keys=['bg','panel','panel-2','field','popup','line','switch-off','text','muted','accent','accent-dark','highlight','on-accent','on-highlight','blue','red','success','yellow','on-warning','chart-2','chart-3','chart-4','chart-5','chart-6'];
const dark=['#0a0a0a', '#141414', '#1c1c1c', '#101010', '#1c1c1c', '#303030', '#404040', '#f5f5f5', '#a3a3a3', '#e5e5e5', '#262626', '#ffffff', '#171717', '#171717', '#6ca8ff', '#f08088', '#79d6a0', '#f3c45b', '#171717', '#24a5bf', '#9767dc', '#db9950', '#4aa881', '#d86e9a'];
const light=['#fafafa', '#ffffff', '#f0f0f0', '#ffffff', '#ffffff', '#d4d4d4', '#737373', '#171717', '#525252', '#262626', '#f0f0f0', '#444444', '#ffffff', '#ffffff', '#235dba', '#b42336', '#147844', '#916000', '#ffffff', '#24a5bf', '#9767dc', '#db9950', '#4aa881', '#d86e9a'];
const defaults={name:'',description:'',contact_email:'',dark:Object.fromEntries(keys.map((k,i)=>[k,dark[i]])),light:Object.fromEntries(keys.map((k,i)=>[k,light[i]])),assets:{},site_max_width:1280,content_max_width:null,sidebar_subsections:false,version:1};
let settings=structuredClone(defaults),fail=false,puts=0,admin=true;
const server=http.createServer(async(req,res)=>{
 const p=new URL(req.url,'http://localhost').pathname,json=(data,code=200)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
 if(p==='/v1/system/branding'){
  if(!admin)return json({error:{message:'Administrator required'}},403);
  if(req.method==='PUT'){puts++;let body='';for await(const chunk of req)body+=chunk;const next=JSON.parse(body);if(fail)return json({error:{message:'Another administrator saved'}},409);assert.equal(next.version,settings.version);settings={...next,version:settings.version+1};return json(settings);}
  return json({settings,defaults,colors:keys.map(key=>({key,label:({'accent':'Primary accent','bg':'Page background','text':'Primary text'})[key]||key,group:key.startsWith('chart-')?'Charts':'Palette'}))});
 }
 if(p.startsWith('/branding/assets/')){const a=settings.assets[p.split('/').at(-1)];if(!a)return res.writeHead(404).end();res.writeHead(200,{'Content-Type':a.mime});return res.end(Buffer.from(a.data,'base64'));}
 if(p==='/site.json')return json({branding:settings});

 if(p==='/auth/methods')return json({google:true,email:false,email_delivery:false});
 if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,version:1}});
 if(p==='/v1/me')return json({id:'self',name:'Administrator',email:'admin@example.test',is_system_admin:admin});
 if(p==='/v1/system/settings')return json({generation_ui:true,personal_providers:true,version:1});
 if(p==='/v1/sales/bonuses')return json({settings:{manager_percent:5,supervisor_percent:1,version:1},people:[]});
 if(p==='/v1/system/markups')return json({system:{default_percent:0,exceptions:[],version:1},company:{default_percent:null,exceptions:[],version:0}});
 if(p==='/v1/system/email')return json({provider:'',from:'',version:1,available_providers:[]});
 if(p.startsWith('/v1/'))return json({data:[]});
 const file=path.join(root,p.startsWith('/ui/')?p.slice(4):p==='/docs'?'documentation.html':p==='/privacy'?'privacy.html':p==='/terms'?'terms.html':'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();
 let data=fs.readFileSync(file);
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');
 if(file.endsWith('.html'))res.setHeader('Content-Security-Policy',"default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data: blob:; base-uri 'none'");
 res.end(data);
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const base=`http://127.0.0.1:${server.address().port}`,browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})}),page=await browser.newPage({viewport:{width:1440,height:1000},colorScheme:'dark'}),errors=[];page.on('pageerror',e=>errors.push(e.message));fs.mkdirSync(evidencePath('/tmp/gateway-branding-ui'),{recursive:true});
 try{
  await page.goto(base+'/system');await page.getByRole('tab',{name:'Appearance',exact:true}).click();const form=page.locator('.branding-settings'),colors=page.locator('.branding-color-settings'),saveColors=async()=>{await colors.getByRole('button',{name:'Save',exact:true}).click();await colors.getByRole('button',{name:'Saved',exact:true}).waitFor();};await form.getByLabel('Site name',{exact:true}).waitFor();
  assert.equal(await form.getByRole('button',{name:'Save',exact:true}).isVisible(),false);
  assert.equal(await page.title(),'Media gateway');
  assert.equal(await page.locator('.brand img[src]').count(),0);
  assert.equal(await page.locator('link[rel=icon],link[rel=apple-touch-icon]').count(),0);
  assert.deepEqual(await form.locator('.branding-image-source').allTextContents(),Array(8).fill('No image uploaded'));

  await form.getByLabel('Site name',{exact:true}).fill('New Brand');await form.getByLabel('Contact email',{exact:true}).fill('hello@example.test');
  await colors.locator('input[name="dark_accent"]').fill('#aa11bb');assert.equal(await page.locator('.branding-preview').evaluate(n=>getComputedStyle(n).getPropertyValue('--accent').trim()),'#aa11bb');assert.equal(await page.locator('html').evaluate(n=>getComputedStyle(n).getPropertyValue('--accent').trim()),'#e5e5e5');
  const before=puts;await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(puts,before+1);assert.equal(settings.name,'New Brand');assert.equal(await page.title(),'New Brand');await saveColors();assert.equal(await page.locator('html').evaluate(n=>getComputedStyle(n).getPropertyValue('--accent').trim()),'#aa11bb');
  await page.reload();await page.getByRole('tab',{name:'Appearance',exact:true}).click();await form.getByLabel('Site name',{exact:true}).waitFor();assert.equal(await form.getByLabel('Site name',{exact:true}).inputValue(),'New Brand');
  // Invalid hex input cannot publish; discard restores the persisted baseline.
  await colors.locator('input[name="dark_accent"]').fill('bad');await colors.getByRole('button',{name:'Save',exact:true}).click();assert.equal(puts,before+2);await colors.getByRole('button',{name:'Discard'}).click();assert.equal(await colors.locator('input[name="dark_accent"]').inputValue(),'#aa11bb');
  // Upload and failed save preserve the draft, then retry succeeds.
  const svg=Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 20"><path fill="#aa11bb" d="M0 0h100v20H0z"/></svg>');
  await form.locator('input[name=asset_logo]').setInputFiles({name:'brand.svg',mimeType:'image/svg+xml',buffer:svg});await page.waitForFunction(()=>document.querySelector('.branding-preview img').src.startsWith('data:image/svg+xml'));
  fail=true;await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByText('Another administrator changed the brand. Reopen Settings before saving.').waitFor();assert.equal(await form.getByRole('button',{name:'Save',exact:true}).isVisible(),true);fail=false;await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(settings.assets.logo.data,svg.toString('base64'));
  // Both image columns contain exactly the same roles in the same order.
  assert.deepEqual(await form.locator('[data-image-theme=dark] h4').allTextContents(),['Logo','Compact logo','Favicon']);
  assert.deepEqual(await form.locator('[data-image-theme=light] h4').allTextContents(),['Logo','Compact logo','Favicon']);
  assert.deepEqual(await form.locator('.branding-shared-images h4').allTextContents(),['Home screen icon','Email logo']);
  const card=key=>form.locator(`[data-asset="${key}"]`);
  assert.equal(await card('logo_light').locator('.branding-image-source').innerText(),'Uses Dark theme · Logo');
  const lightSvg=Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 20"><path fill="#226644" d="M0 0h100v20H0z"/></svg>');
  const markSvg=Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><circle fill="#fff" cx="10" cy="10" r="8"/></svg>');
  for(const [key,buffer]of [['logo_light',lightSvg],['symbol',markSvg],['symbol_light',lightSvg],['favicon',markSvg],['favicon_light',lightSvg]]){
    await form.locator(`input[name=asset_${key}]`).setInputFiles({name:key+'.svg',mimeType:'image/svg+xml',buffer});
    await page.waitForFunction(k=>document.querySelector(`[data-asset="${k}"] .branding-image-source`).textContent==='Uploaded for this theme',key);
  }
  await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();
  await page.evaluate(()=>GatewayTheme.apply('light'));
  assert.ok((await page.locator('.brand-wordmark').getAttribute('src')).includes(lightSvg.toString('base64')));
  assert.ok((await page.locator('.brand-symbol').getAttribute('src')).includes(lightSvg.toString('base64')));
  await page.evaluate(()=>GatewayTheme.apply('dark'));
  assert.ok((await page.locator('.brand-wordmark').getAttribute('src')).includes(svg.toString('base64')));
  assert.ok((await page.locator('.brand-symbol').getAttribute('src')).includes(markSvg.toString('base64')));
  // Browser favicon selection is independent of a dark profile theme.
  await page.emulateMedia({colorScheme:'light'});
  assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');
  assert.ok((await page.locator('link[rel=icon][media="(prefers-color-scheme: light)"]').getAttribute('href')).includes(lightSvg.toString('base64')));
  assert.equal(await page.evaluate(()=>matchMedia('(prefers-color-scheme: light)').matches),true);
  await page.evaluate(()=>GatewayTheme.apply('system'));
  // Removing either theme restores the other theme symmetrically, visibly.
  await card('symbol_light').getByRole('button',{name:'Remove compact logo in light theme',exact:true}).click();
  assert.equal(await card('symbol_light').locator('.branding-image-source').innerText(),'Uses Dark theme · Compact logo');
  await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();
  await card('symbol').getByRole('button',{name:'Remove compact logo in dark theme',exact:true}).focus();await page.keyboard.press('Enter');
  assert.equal(await card('symbol').locator('.branding-image-source').innerText(),'Uses Dark theme · Favicon');
  await form.getByRole('button',{name:'Discard'}).click();
  assert.equal(await card('symbol').locator('.branding-image-source').innerText(),'Uploaded for this theme');
  // Custom SVG-only brands show the configured site name in email previews.
  assert.equal(await card('email_logo').locator('.branding-image-source').innerText(),'Uses site name');
  // Light palette and shared dropdown keyboard/Escape.
  await colors.locator('details').nth(1).evaluate(n=>n.open=true);await colors.locator('input[name="light_accent"]').fill('#226644');await colors.getByRole('switch',{name:'Light preview',exact:true}).check();await page.waitForFunction(()=>getComputedStyle(document.querySelector('.branding-preview')).getPropertyValue('--accent').trim()==='#226644');
  const picker=colors.getByRole('combobox',{name:'Color theme',exact:true});await picker.focus();await picker.press('Enter');await picker.press('Escape');assert.equal(await picker.getAttribute('aria-expanded'),'false');
  await saveColors();await page.emulateMedia({colorScheme:'light'});await page.waitForFunction(()=>getComputedStyle(document.documentElement).getPropertyValue('--accent').trim()==='#226644');
  for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'overflow at '+width);await page.screenshot({path:evidencePath(`/tmp/gateway-branding-ui/appearance-${width}.png`),fullPage:true});await page.screenshot({path:evidencePath(`/tmp/gateway-branding-ui/viewport-${width}.png`)});await form.locator('.branding-theme-columns').first().screenshot({path:evidencePath(`/tmp/gateway-branding-ui/theme-images-${width}.png`)});if(width===1440){for(const [dark,light]of [['logo','logo_light'],['symbol','symbol_light'],['favicon','favicon_light']]){const a=await card(dark).boundingBox(),b=await card(light).boundingBox();assert.ok(Math.abs(a.y-b.y)<1,`${dark} and ${light} rows are misaligned`);assert.ok(Math.abs(a.width-b.width)<1,'theme columns have different widths');}}else{const a=await card('logo').boundingBox(),b=await card('logo_light').boundingBox();assert.ok(Math.abs(a.x-b.x)<1,'stacked columns are misaligned');assert.ok(Math.abs(a.width-b.width)<1,'stacked columns have different widths');}}
  await page.setViewportSize({width:1440,height:1000});await page.emulateMedia({colorScheme:'dark'});await page.screenshot({path:evidencePath('/tmp/gateway-branding-ui/dark-desktop.png'),fullPage:true});
  for(const p of ['/docs','/privacy','/terms']){await page.goto(base+p);await page.evaluate(()=>GatewaySite.ready);assert.ok((await page.title()).includes('New Brand'));assert.equal(await page.locator('html').evaluate(n=>getComputedStyle(n).getPropertyValue('--accent').trim()),'#aa11bb');}
  await page.goto(base+'/system');await page.getByRole('tab',{name:'Appearance',exact:true}).click();await form.getByLabel('Site name',{exact:true}).waitFor();assert.equal(await card('logo_light').locator('.branding-image-source').innerText(),'Uploaded for this theme');assert.equal(await card('symbol_light').locator('.branding-image-source').innerText(),'Uses Dark theme · Compact logo');assert.equal(await page.getByRole('button',{name:/Reset/}).count(),0);for(const key of ['Site name','Site description','Contact email'])await form.getByLabel(key,{exact:true}).fill('');while(await form.locator('.branding-image-actions button:visible').count())await form.locator('.branding-image-actions button:visible').first().click();assert.equal(settings.name,'New Brand');await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(settings.name,'');assert.deepEqual(settings.assets,{});
  assert.equal(await page.locator('.brand img[src]').count(),0);
  assert.equal(await page.locator('link[rel=icon],link[rel=apple-touch-icon]').count(),0);
  assert.equal(await colors.locator('.branding-preview img').isVisible(),false);

  // Persisted width limits, full-width mode and both subsection locations.
  await page.setViewportSize({width:2560,height:1200});
  const siteWidth=form.getByLabel('Maximum site width (px)',{exact:true}),contentWidth=form.getByLabel('Maximum content width (px)',{exact:true}),tree=form.getByRole('switch',{name:'Show subsections in the left menu'});
  assert.equal(await siteWidth.inputValue(),'1280');assert.equal(await contentWidth.inputValue(),'');
  const saveLayout=async()=>{await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();};
  const tabIDs=locator=>locator.evaluateAll(tabs=>tabs.map(tab=>tab.id));
  const settingsTabs=await tabIDs(page.locator('#systemContent .system-settings-tabs [role=tab]'));
  assert.ok(settingsTabs.includes('settings-tab-system')&&settingsTabs.includes('settings-tab-appearance'),'settings tabs are missing');
  assert.equal(new Set(settingsTabs).size,settingsTabs.length,'duplicate settings tabs');
  await siteWidth.fill('959');const invalidPuts=puts;await form.getByRole('button',{name:'Save',exact:true}).click();assert.equal(puts,invalidPuts);await siteWidth.fill('1600');await contentWidth.fill('1000');await tree.check();await saveLayout();
  const geometry=async()=>({shell:await page.locator('.shell').boundingBox(),content:await page.locator('#systemContent').boundingBox(),menu:await page.locator('#systemView > aside').boundingBox()});
  let g=await geometry();assert.ok(Math.abs(g.shell.width-1600)<1);assert.ok(Math.abs(g.shell.x-480)<1);assert.ok(Math.abs(g.content.width-1000)<1);assert.ok(Math.abs(g.content.x+g.content.width/2-1280)<1,'content is not centered on screen');assert.ok(g.menu.x+g.menu.width<=g.content.x,'menu overlaps content');
  assert.deepEqual(await tabIDs(page.locator('#systemView aside [role=tab]')),settingsTabs,'sidebar must preserve all settings tabs');assert.equal(await page.locator('#systemContent [role=tablist]').count(),0);assert.equal(await page.locator('[role=tablist]').getAttribute('aria-orientation'),'vertical');
  // Nested navigation shows one selection on a vertical version of the header's straight line.
  const navParent=page.locator('[data-system-page=settings]'),markups=page.getByRole('tab',{name:'Markups',exact:true});
  const navStyle=async locator=>locator.evaluate(n=>{const s=getComputedStyle(n),line=getComputedStyle(n,'::after'),root=getComputedStyle(document.documentElement);return {color:s.color,background:s.backgroundColor,shadow:s.boxShadow,radius:s.borderRadius,bottomBorder:s.borderBottomWidth,lineColor:line.backgroundColor,lineHeight:line.height,lineWidth:line.width,lineLeft:line.left,height:n.getBoundingClientRect().height,accent:(root.getPropertyValue('--accent-text')||root.getPropertyValue('--accent')).trim(),muted:root.getPropertyValue('--muted').trim()};});
  const colorRGB=hex=>'rgb('+hex.slice(1).match(/../g).map(v=>parseInt(v,16)).join(', ')+')';
  await markups.click();
  for(const theme of ['dark','light']){
   await page.evaluate(t=>GatewayTheme.apply(t),theme);await page.mouse.move(0,0);
   const p=await navStyle(navParent),m=await navStyle(markups);
   assert.equal(p.color,colorRGB(p.muted),'parent looks selected');assert.equal(p.background,'rgba(0, 0, 0, 0)');assert.equal(p.shadow,'none');
   assert.equal(m.color,colorRGB(m.accent));assert.equal(m.background,'rgba(0, 0, 0, 0)');assert.equal(m.shadow,'none');assert.equal(m.radius,'0px');assert.equal(m.lineWidth,'2px');assert.ok(Math.abs(parseFloat(m.lineHeight)-m.height)<1,'vertical marker does not span the selected row');assert.equal(m.lineLeft,'-1px');assert.equal(m.bottomBorder,'0px');assert.equal(m.lineColor,colorRGB(m.accent));
   assert.equal(await page.locator('.subsection-nav').evaluate(n=>getComputedStyle(n).borderLeftWidth),'1px');
   const other=await navStyle(page.getByRole('tab',{name:'System',exact:true}));assert.notEqual(other.color,m.color,'two subsections look selected');
   await markups.hover();assert.equal((await navStyle(markups)).lineColor,m.lineColor,'hover changes selected vertical marker');
   await page.mouse.move(0,0);await page.locator('#systemView').screenshot({path:evidencePath(`/tmp/gateway-branding-ui/sidebar-selection-${theme}.png`)});
  }
  await page.getByRole('tab',{name:'Appearance',exact:true}).click();await form.waitFor();await page.evaluate(()=>GatewayTheme.apply('dark'));
  await siteWidth.fill('');await siteWidth.press('Tab');await saveLayout();g=await geometry();assert.ok(Math.abs(g.shell.width-2520)<1);assert.ok(Math.abs(g.shell.x-20)<1,'site lost safe left margin');assert.ok(Math.abs(2560-g.shell.x-g.shell.width-20)<1,'site lost safe right margin');assert.ok(Math.abs(g.menu.x-20)<1,'menu lost safe edge margin');assert.ok(Math.abs(g.content.x+g.content.width/2-1280)<1);assert.equal(settings.site_max_width,null);
  await page.screenshot({path:evidencePath('/tmp/gateway-branding-ui/layout-full-sidebar.png')});
  const parent=page.locator('[data-system-page=settings]');await parent.click();assert.equal(await parent.getAttribute('aria-expanded'),'false');assert.equal((await navStyle(parent)).shadow,'none','collapsed parent looks selected');await parent.press('ArrowRight');assert.equal(await parent.getAttribute('aria-expanded'),'true');assert.equal(await page.getByRole('tab',{name:'Appearance',exact:true}).evaluate(n=>n===document.activeElement),true);assert.notEqual(await page.getByRole('tab',{name:'Appearance',exact:true}).evaluate(n=>getComputedStyle(n).outlineStyle),'none','keyboard focus is invisible');await page.keyboard.press('ArrowUp');await page.getByRole('heading',{name:'System',exact:true}).waitFor();await page.keyboard.press('ArrowDown');await form.waitFor();await page.keyboard.press('Escape');assert.equal(await parent.getAttribute('aria-expanded'),'false');await parent.press('ArrowRight');
  await contentWidth.fill('640');await saveLayout();assert.equal(await form.locator('.branding-theme-columns').first().evaluate(n=>getComputedStyle(n).gridTemplateColumns.split(' ').length),1,'narrow desktop content did not adapt');
  for(const width of [1024,900,768,760,390,320]){await page.setViewportSize({width,height:1000});await page.waitForFunction(w=>document.querySelector('.system-settings-tabs').getAttribute('aria-orientation')===(w>760?'vertical':'horizontal'),width);await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth);if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)){console.log(await page.evaluate(()=>[...document.querySelectorAll('body *')].map(n=>({tag:n.tagName,id:n.id,class:n.className,text:n.textContent.slice(0,55),x:n.getBoundingClientRect().x,right:n.getBoundingClientRect().right})).filter(n=>n.right>innerWidth+1)));await page.screenshot({path:evidencePath('/tmp/gateway-layout-fail.png')});}assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'layout overflow '+width);const frame=await page.locator('.shell').boundingBox(),gutter=width>760?20:12;assert.ok(Math.abs(frame.x-gutter)<1,'left safe margin '+width);assert.ok(Math.abs(width-frame.x-frame.width-gutter)<1,'right safe margin '+width);assert.deepEqual(await tabIDs(page.locator('#systemView aside [role=tab]')),width>760?settingsTabs:[],'sidebar tabs at '+width);if(width<=760){assert.deepEqual(await tabIDs(page.locator('#systemContent .system-settings-tabs [role=tab]')),settingsTabs,'inline tabs at '+width);assert.equal(await page.locator('#systemContent [role=tablist]').getAttribute('aria-orientation'),'horizontal');}await page.screenshot({path:evidencePath(`/tmp/gateway-branding-ui/layout-${width}.png`)});}
  await page.setViewportSize({width:1920,height:1100});await page.reload();await page.getByRole('tab',{name:'Appearance',exact:true}).click();await siteWidth.waitFor();assert.equal(await siteWidth.inputValue(),'');assert.equal(await contentWidth.inputValue(),'640');assert.equal(await tree.isChecked(),true);assert.deepEqual(await tabIDs(page.locator('#systemView aside [role=tab]')),settingsTabs,'saved sidebar must preserve all settings tabs');g=await geometry();assert.ok(Math.abs(g.shell.x-20)<1,'safe margin missing on initial load');assert.ok(Math.abs(1920-g.shell.x-g.shell.width-20)<1);
  await contentWidth.fill('1280');await tree.uncheck();await saveLayout();g=await geometry();assert.ok(Math.abs(g.menu.x-20)<1,'horizontal-tab menu lost safe margin');assert.ok(Math.abs(g.content.width-1280)<1);assert.ok(Math.abs(g.content.x+g.content.width/2-960)<1,'safe gutters shifted centered content');assert.equal(await page.locator('#systemContent [role=tablist]').getAttribute('aria-orientation'),'horizontal');await page.evaluate(()=>GatewayTheme.apply('light'));await page.screenshot({path:evidencePath('/tmp/gateway-branding-ui/layout-safe-margins.png')});
  await siteWidth.fill('960');await contentWidth.fill('');await tree.uncheck();await saveLayout();g=await geometry();assert.ok(Math.abs(g.shell.width-960)<1);assert.equal(await page.locator('#systemContent [role=tablist]').getAttribute('aria-orientation'),'horizontal');assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await siteWidth.fill('1280');await saveLayout();assert.equal(settings.site_max_width,1280);assert.equal(settings.content_max_width,null);assert.equal(settings.sidebar_subsections,false);
  await page.locator('[data-system-page=administrators]').click();await page.getByRole('heading',{name:'Users',exact:true}).waitFor();await page.mouse.move(0,0);
  const single=await navStyle(page.locator('[data-system-page=administrators]'));assert.equal(single.color,colorRGB(single.accent));assert.ok(single.shadow.includes('inset'),'sections without subsections lost their side marker');assert.notEqual(single.background,'rgba(0, 0, 0, 0)');
  admin=false;await page.reload();await page.waitForFunction(()=>document.querySelector('#systemNav').hidden);assert.equal(await form.count(),0);assert.deepEqual(errors,[]);console.log('PASS: appearance draft, save/reload, validation, upload, conflict/retry, image removal, themes, public pages, keyboard, 1440/390/320px layout widths, sidebar navigation and non-admin UI.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
