// Sign out warns first when the installation has no browser sign-in method left.
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const {[engine]:browserType}=require('playwright');
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path');
const ui=path.resolve(process.env.UI_SOURCE_DIR||path.join(__dirname,'../../web/public/ui')),output=path.resolve(process.env.UI_EVIDENCE_DIR||`.local/sign-out-warning/${engine}`);
fs.mkdirSync(output,{recursive:true});
let methods,logoutCalls=0;
const server=http.createServer((req,res)=>{const p=new URL(req.url,'http://fixture.test').pathname,json=(data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
 if(p==='/auth/company-connection')return json({connected:false},404);
 if(p==='/auth/methods')return json(methods);
 if(p==='/auth/logout'){logoutCalls++;return res.writeHead(204).end();}
 if(p==='/auth/session')return res.writeHead(204).end();
 const name=path.basename(p),file=path.join(ui,name.includes('.')?name:'index.html');
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
let browser;
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));browser=await browserType.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 for(const width of [1440,390]){
  const page=await browser.newPage({viewport:{width,height:900}});page.setDefaultTimeout(15000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/v1/**',route=>{const p=new URL(route.request().url()).pathname,json=data=>route.fulfill({json:data});
   if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,version:1}});
   if(p==='/v1/me')return json({id:'self',name:'Owner',email:'owner@example.test',session_id:'fixture-session',is_system_admin:true});
   return json({data:[]});
  });
  const open=async()=>{await page.goto('http://127.0.0.1:'+server.address().port+'/');await page.locator('#userMenuToggle').waitFor();};
  const signOut=async()=>{if(!await page.locator('#logoutButton').isVisible()){await page.locator('#userMenuToggle').focus();await page.keyboard.press('ArrowDown');await page.keyboard.press('End');}await page.locator('#logoutButton').click();};
  const dialog=()=>page.getByRole('dialog',{name:'Sign out without a way back in?'});
  methods={email:false,email_delivery:false,email_confirmation:true,google:false,setup_required:false};logoutCalls=0;
  await open();await signOut();await dialog().waitFor();
  assert.match(await dialog().innerText(),/Sign-in is currently unavailable/);assert.equal(logoutCalls,0,'warning must come before the session is revoked');
  assert.equal(await page.evaluate(()=>document.activeElement?.textContent),'Stay signed in','the safe choice has focus');
  await page.screenshot({path:path.join(output,`warning-${width}.png`)});
  await dialog().getByRole('button',{name:'Stay signed in'}).click();await dialog().waitFor({state:'detached'});
  assert.equal(logoutCalls,0);assert.equal(await page.locator('#logoutButton').isDisabled(),false);
  await signOut();await dialog().waitFor();await page.keyboard.press('Escape');await dialog().waitFor({state:'detached'});assert.equal(logoutCalls,0,'Escape keeps the session');
  await signOut();await dialog().waitFor();await Promise.all([page.waitForEvent('load'),dialog().getByRole('button',{name:'Sign out anyway'}).click()]);assert.equal(logoutCalls,1,'Sign out anyway still signs out');
  for(const available of [{email:true,email_delivery:true,google:false},{email:false,google:true},{setup_required:true}]){
   methods={email_confirmation:true,setup_required:false,...available};logoutCalls=0;await open();
   await Promise.all([page.waitForEvent('load'),signOut()]);assert.equal(logoutCalls,1,'no warning when a sign-in method exists: '+JSON.stringify(available));assert.equal(await dialog().count(),0);
  }
  assert.deepEqual(errors,[]);await page.close();
 }
 console.log('PASS: Sign out warns before revoking the session when no browser sign-in method exists; Stay signed in and Escape keep it; Sign out anyway and working methods sign out directly; desktop/mobile.');
})().catch(error=>{console.error(error);process.exitCode=1}).finally(async()=>{await browser?.close();server.close()});
