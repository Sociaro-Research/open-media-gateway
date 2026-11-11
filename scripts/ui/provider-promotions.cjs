// Actual portal with fully intercepted HTTPS fixtures; no services or paid/live calls.
const {chromium,webkit}=require('playwright'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'../../web/public'),origin='https://provider-promotions.fixture.test',engine=process.env.UI_BROWSER_ENGINE||'chromium';
const out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/provider-promotions-'+engine),errors=[],external=[],calls=[],checks=[],geometry=[];
let user={id:'fixture-admin',name:'Fixture admin',is_system_admin:true},settings={generation_ui:true,personal_providers:true,installation_role:'ordinary',version:1},releaseSettings;
const pass=message=>{checks.push(message);console.log('PASS',message);};
(async()=>{fs.mkdirSync(out,{recursive:true});const browser=await({chromium,webkit}[engine]).launch({headless:true}),context=await browser.newContext({viewport:{width:1440,height:900}});
 await context.route('**/*',async route=>{const request=route.request(),url=new URL(request.url());if(url.origin!==origin){external.push(request.url());return route.abort();}
  const json=value=>route.fulfill({json:value});
  if(url.pathname.startsWith('/v1/'))calls.push({path:url.pathname,method:request.method()});
  if(url.pathname==='/site.json')return json({ui_url:origin,api_url:origin,docs_url:origin,branding:{name:'Gateway',version:1}});
  if(url.pathname==='/auth/company-connection')return route.fulfill({status:404,json:{connected:false}});
  if(url.pathname==='/auth/session')return route.fulfill({status:204,body:''});
  if(url.pathname==='/auth/methods')return json({email:true,email_delivery:true});
  if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:settings});
  if(url.pathname==='/v1/me')return json(user);
  if(url.pathname==='/v1/system/settings'){if(releaseSettings)await new Promise(r=>releaseSettings=r);return json(settings);}
  if(url.pathname==='/v1/admin/providers')return json({data:[{id:'mock',api_type:'mock',name:'Fixture provider',enabled:true,configured:true,fields:[],models:[],version:1,status:'authenticated',credential_source:'none'}]});
  if(url.pathname.startsWith('/v1/'))return json({data:[]});
  const relative=url.pathname.startsWith('/ui/')?url.pathname.slice(1):'ui/index.html',file=path.join(root,relative);
  if(!file.startsWith(root+path.sep)||!fs.existsSync(file))return route.fulfill({status:404,body:''});
  return route.fulfill({path:file,contentType:file.endsWith('.css')?'text/css':file.endsWith('.js')?'text/javascript':'text/html'});
 });
 const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(10000);
 const cards=()=>page.locator('.provider-promotion:visible'),connect=()=>page.locator('[data-promotion=sociaro]').getByRole('button',{name:'Connect to Sociaro',exact:true}),close=id=>page.locator('[data-promotion='+id+'] .icon-close');
 async function load(){await page.goto(origin+'/profile');await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();await page.locator('#providersView').waitFor({state:'visible'});}
 try{
  await load();assert.equal(await cards().count(),2);await page.evaluate(()=>{window.connectionCalls=[];RemoteWorkspace.open=async options=>connectionCalls.push(options);});
  const guide=page.getByRole('link',{name:'Provider integration guide'});assert.equal(await guide.getAttribute('href'),'https://github.com/Sociaro-Research/open-media-gateway/blob/main/docs/provider-development.md');assert.equal(await guide.getAttribute('target'),'_blank');assert.equal(await guide.getAttribute('rel'),'noopener noreferrer');pass('real provider page shows two cards and the canonical safe contribution link');
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.evaluate(t=>GatewayTheme.apply(t),theme);await page.setViewportSize({width,height:900});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const value=await page.locator('.provider-promotions').evaluate(root=>({document:document.documentElement.scrollWidth,width:innerWidth,cards:[...root.children].map(card=>{const r=card.getBoundingClientRect(),c=card.querySelector('.icon-close').getBoundingClientRect(),h=card.querySelector('h2').getBoundingClientRect();return {left:r.left,right:r.right,closeWidth:c.width,closeHeight:c.height,closeRight:c.right,headingRight:h.right};})}));
   assert.ok(value.document<=value.width+1,'page has no horizontal overflow');for(const box of value.cards){assert.ok(box.left>=0&&box.right<=width);assert.ok(box.closeWidth>=44&&box.closeHeight>=44);assert.ok(box.closeRight<=box.right);}
   await page.keyboard.press('Tab');await close('contribute').focus();assert.equal(await close('contribute').evaluate(n=>n.matches(':focus-visible')),true);await page.screenshot({path:path.join(out,theme+'-'+width+'.png'),fullPage:true});geometry.push({theme,width,...value});
  }pass('both themes at 1440/390/320, shared cards, 44px dismiss targets and keyboard focus');
  await close('contribute').focus();await page.keyboard.press('Enter');assert.equal(await cards().count(),1);assert.equal(await page.locator('[data-promotion=sociaro] .icon-close').evaluate(n=>n===document.activeElement),true);
  await page.evaluate(()=>ProviderConnections.setup());assert.equal(await cards().count(),1);await load();assert.equal(await cards().count(),1);pass('independent dismissal persists across rerenders and reload without stealing provider state');
  await connect().click();const dialog=page.locator('.setup-dialog[open]');await dialog.waitFor();assert.equal(await dialog.locator('input[value=sociaro]').isChecked(),true);assert.equal(calls.filter(c=>c.method!=='GET'&&c.path!=='/v1/playground-key/restore').length,0);
  await page.keyboard.press('Escape');assert.equal(await dialog.count(),0);await connect().click();await dialog.waitFor();await page.evaluate(()=>{window.connectionCalls=[];RemoteWorkspace.open=async options=>connectionCalls.push(options);});await dialog.getByRole('button',{name:'Connect to Sociaro',exact:true}).click();assert.deepEqual(await page.evaluate(()=>connectionCalls),[{connect:true}]);assert.equal(await dialog.count(),0);pass('Sociaro action preselects existing third mode; Escape is harmless; only confirmation invokes existing connection flow');
  await close('sociaro').focus();await page.keyboard.press('Space');assert.equal(await cards().count(),0);assert.equal(await page.locator('#providersView h1').evaluate(n=>n===document.activeElement),true);await load();assert.equal(await cards().count(),0);pass('second dismissal independent, persisted and restores focus to provider heading');
  user={...user,id:'another-admin'};await load();assert.equal(await cards().count(),2);pass('another account does not inherit the first account dismissals');
  await page.evaluate(()=>{applySystemAccess({...serviceInfo.console,installation_role:'central'},true);});assert.equal(await page.locator('[data-promotion=sociaro]').isHidden(),true);settings={...settings,installation_role:'ordinary'};user={...user,id:'non-admin',is_system_admin:false};await load();assert.equal(await cards().count(),1);assert.equal(await page.locator('[data-promotion=sociaro]').isHidden(),true);pass('central and non-administrator scopes never offer an unavailable installation action');
  user={...user,id:'storage-blocked',is_system_admin:true};await context.addInitScript(()=>{const get=Storage.prototype.getItem,set=Storage.prototype.setItem;Storage.prototype.getItem=function(k){if(k.startsWith('gateway-provider-promotions-'))throw Error('Storage disabled');return get.call(this,k);};Storage.prototype.setItem=function(k,v){if(k.startsWith('gateway-provider-promotions-'))throw Error('Storage disabled');return set.call(this,k,v);};});await load();await close('contribute').click();await page.evaluate(()=>ProviderConnections.setup());assert.equal(await cards().count(),1);pass('storage denial preserves in-page dismissal without breaking provider UI');
  releaseSettings=true;await connect().click();await page.waitForFunction(()=>document.querySelector('[data-promotion=sociaro] .primary').disabled);await close('sociaro').click();const dismissedRequest=releaseSettings;releaseSettings=null;dismissedRequest();await page.waitForFunction(()=>!document.querySelector('[data-promotion=sociaro] .primary').disabled);assert.equal(await page.locator('.setup-dialog[open]').count(),0);pass('dismissing a banner also cancels its pending setup dialog');
  user={...user,id:'navigate-away'};await load();
  // Delay a settings read, navigate away, then release it: no stale dialog appears.
  releaseSettings=true;await connect().click();await page.waitForFunction(()=>document.querySelector('[data-promotion=sociaro] .primary').disabled);await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Storage',exact:true}).click();await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();await page.locator('#providersView').waitFor({state:'visible'});const release=releaseSettings;releaseSettings=null;release();await page.waitForFunction(()=>!document.querySelector('[data-promotion=sociaro] .primary').disabled);assert.equal(await page.locator('.setup-dialog[open]').count(),0);pass('interrupted opening does not revive a dialog after leaving and returning to Providers');
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);assert.equal(calls.filter(c=>c.method!=='GET'&&c.path!=='/v1/playground-key/restore').length,0);
 }finally{fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,checks,geometry,errors,external,calls},null,2));await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
