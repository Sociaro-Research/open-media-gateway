const {evidencePath} = require('./evidence.cjs');
// Isolated profile/theme UI checks. No live account or provider requests.
const {chromium}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const file=path.join(root,path.basename(req.url.split('?')[0]).includes('.')?path.basename(req.url.split('?')[0]):'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH}),page=await browser.newPage({viewport:{width:1440,height:1000}});page.setDefaultTimeout(8000);
 const errors=[],requests=[];page.on('pageerror',e=>errors.push(e.message));let personal=false,manager=false,fail=false;
 let user={id:'33333333-3333-4333-8333-333333333333',name:'Google Name',email:'user@example.test',theme:'system',session_id:'fixture'};
 await page.route('**/v1/**',async route=>{const req=route.request(),p=new URL(req.url()).pathname,json=data=>route.fulfill({json:data});
  if(p==='/v1/me'){if(req.method()==='PATCH'){requests.push(req.postDataJSON());if(fail)return route.fulfill({status:500,json:{error:{message:'Could not save profile'}}});user={...user,...req.postDataJSON()};}return json({...user,is_client_manager:manager});}
  if(p==='/v1/info')return json({auth_mode:'google',console:{generation_ui:true,personal_providers:personal,shared_resources:true}});
  if(p==='/v1/admin/providers')return json({data:[{id:'mock',name:'Mock provider',models:[],configured:false,enabled:true}]});
  return json({data:[]});
 });
 const base='http://127.0.0.1:'+server.address().port;
 const form=key=>page.locator('#profileContent form').filter({has:page.locator('[name='+key+']')});
 const save=async key=>{await form(key).getByRole('button',{name:'Save',exact:true}).click();await form(key).getByRole('button',{name:'Saved',exact:true}).waitFor();};
 const choose=async value=>{await page.getByRole('combobox',{name:'Theme',exact:true}).click();await page.getByRole('option',{name:value,exact:true}).click();};
 const theme=()=>page.locator('html').getAttribute('data-theme');
 try{
  await page.emulateMedia({colorScheme:'light'});await page.goto(base+'/');await page.locator('#userMenuToggle').press('ArrowDown');await page.getByRole('menuitem',{name:'Profile',exact:true}).press('Enter');
  await page.getByLabel('Display name',{exact:true}).waitFor();assert.equal(new URL(page.url()).pathname,'/profile');assert.equal(await page.getByLabel('Display name',{exact:true}).inputValue(),'Google Name');assert.equal(await form('name').getByRole('button',{name:'Save',exact:true}).isVisible(),false);assert.equal(await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).count(),0);
  await page.getByLabel('Display name',{exact:true}).fill('Custom Name');const saveBox=await form('name').getByRole('button',{name:'Save',exact:true}).boundingBox();await save('name');assert.deepEqual(await form('name').getByRole('button',{name:'Saved',exact:true}).boundingBox(),saveBox);assert.equal(await page.locator('#userName').innerText(),'Custom Name');
  assert.equal(await theme(),'light');assert.equal(await page.locator('[name=theme]').inputValue(),'system');await page.emulateMedia({colorScheme:'dark'});await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');await choose('Light');await save('theme');assert.equal(await theme(),'light');await page.emulateMedia({colorScheme:'dark'});assert.equal(await theme(),'light');
  await choose('System');await save('theme');assert.equal(await theme(),'dark');await page.emulateMedia({colorScheme:'light'});await page.waitForFunction(()=>document.documentElement.dataset.theme==='light');
  await page.reload();await page.getByLabel('Display name',{exact:true}).waitFor();assert.equal(await page.getByLabel('Display name',{exact:true}).inputValue(),'Custom Name');assert.equal(await theme(),'light');
  for(const width of [1440,390,320]){await page.setViewportSize({width,height:900});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'overflow '+width);await page.getByRole('combobox',{name:'Theme',exact:true}).click();await page.screenshot({path:evidencePath('/tmp/gateway-profile-light-')+width+'.png',fullPage:true});await page.keyboard.press('Escape');}
  await choose('Dark');await save('theme');await page.screenshot({path:evidencePath('/tmp/gateway-profile-dark-320.png'),fullPage:true});
  fail=true;await page.getByLabel('Display name',{exact:true}).fill('Unsaved Name');await form('name').getByRole('button',{name:'Save',exact:true}).click();await page.getByText('Could not save profile',{exact:true}).waitFor();assert.equal(await page.locator('#userName').innerText(),'Custom Name');assert.equal(await page.getByLabel('Display name',{exact:true}).inputValue(),'Unsaved Name');fail=false;await save('name');
  personal=true;await page.reload();await page.getByLabel('Display name',{exact:true}).waitFor();await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();await page.locator('#providersView').waitFor();await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Profile',exact:true}).click();await page.getByLabel('Display name',{exact:true}).waitFor();
  manager=true;await page.reload();await page.getByLabel('Display name',{exact:true}).waitFor();assert.equal(await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).count(),0);
  assert.deepEqual(errors,[]);assert.ok(requests.every(r=>Object.keys(r).length===1));console.log('PASS: always-visible profile, names, theme persistence/system changes, failed saves, inline feedback, role/provider gating, keyboard and desktop/mobile layouts');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close()});
