// Exercise the real console code with a failed session revocation and a retry.
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const {[engine]:chromium}=require('playwright');
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path');
const ui=path.resolve(__dirname,'../../web/public/ui');
let unavailable=true,logoutCalls=0,stallMethods=false,methods=null;const events=[];
const server=http.createServer((req,res)=>{if(req.method==='GET'&&new URL(req.url,'http://fixture.test').pathname==='/auth/company-connection'){res.writeHead(404,{'Content-Type':'application/json'});return res.end(JSON.stringify({connected:false}));}
 if(req.url==='/auth/methods'&&stallMethods){events.push(['methods',Date.now()]);return;}// never answers
 if(req.url==='/auth/methods'&&methods)return res.writeHead(200,{'Content-Type':'application/json'}).end(JSON.stringify(methods));
 if(req.url==='/auth/logout'){logoutCalls++;events.push(['logout',Date.now()]);return res.writeHead(unavailable?503:204,{'Content-Type':'application/json'}).end(unavailable?JSON.stringify({error:{message:'retry'}}):undefined)}
 if(req.url==='/auth/session')return res.writeHead(204).end();
 const name=path.basename(req.url.split('?')[0]),file=path.join(ui,name.includes('.')?name:'index.html');
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
let browser;
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 for(const width of [1440,390])for(const section of ['generation','profile']){
  const page=await browser.newPage({viewport:{width,height:1000}});page.setDefaultTimeout(15000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/v1/**',route=>{const p=new URL(route.request().url()).pathname,json=data=>route.fulfill({json:data});
   if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,version:1}});
   if(p==='/v1/me')return json({id:'self',name:'Tester',email:'tester@example.test',session_id:'fixture-session',is_system_admin:true});
   if(p==='/v1/organizations')return json({data:[]});
   if(p==='/v1/key')return json({key:{name:'Fixture',resource_mode:'personal'},usage:{actual_usd:0,reserved_usd:0}});
   return json({data:[]});
  });
  await page.addInitScript(()=>{window.themeAtBoot=localStorage.getItem('gateway-theme')});
  await page.goto('http://127.0.0.1:'+server.address().port+'/');await page.locator('#userMenuToggle').waitFor();
  // Bind a fixture generation key through the real input/validation code.
  await page.locator('#apiKey').fill('fixture-personal-key');await page.locator('#apiKey').dispatchEvent('change');
  await page.waitForFunction(()=>GenerationKey.value()==='fixture-personal-key');
  if(section==='profile'){await page.evaluate(()=>showConsoleSection('profile'));await page.locator('#profileView').waitFor()}
  const openLogout=async()=>{await page.locator('#userMenuToggle').focus();await page.keyboard.press('ArrowDown');await page.keyboard.press('End');await page.locator('#logoutButton').waitFor()};
  await page.evaluate(()=>GatewayTheme.apply('dark',{share:true}));
  const until=async check=>{for(let i=0;i<150&&!check();i++)await new Promise(r=>setTimeout(r,100));assert.ok(check());await page.waitForFunction(()=>!document.querySelector('#logoutButton').disabled);};
  if(section==='generation'){
   // No browser sign-in method left: Sign out warns before revoking the session, and cancelling returns focus to the visible menu button.
   unavailable=true;methods={email:false,email_delivery:false,email_confirmation:true,google:false,setup_required:false};const warning=page.getByRole('dialog',{name:'Sign out without a way back in?'}),calls=logoutCalls;
   for(const [index,[name,cancel]] of [['Stay signed in',()=>warning.getByRole('button',{name:'Stay signed in',exact:true}).click()],['Escape',()=>page.keyboard.press('Escape')],['Close',()=>warning.locator('.dialog-close').click()]].entries()){
    // First pass: a mouse press in the keyboard-opened menu. WebKit blurs to no target before the click, which used to close the menu and drop the click.
    await openLogout();if(index)await page.keyboard.press('Enter');else await page.locator('#logoutButton').click();await warning.waitFor();
    assert.match(await warning.innerText(),/Sign-in is currently unavailable/);assert.equal(await page.evaluate(()=>document.activeElement?.textContent),'Stay signed in','the safe choice has focus');
    await cancel();await warning.waitFor({state:'detached'});
    assert.equal(await page.evaluate(()=>document.activeElement?.id),'userMenuToggle',name+' returns focus to the user menu button');
   }
   assert.equal(logoutCalls,calls,'cancelling keeps the session');assert.equal(await page.locator('#logoutButton').isDisabled(),false);
   await openLogout();await page.keyboard.press('Enter');await warning.waitFor();await warning.getByRole('button',{name:'Sign out anyway'}).click();
   await until(()=>logoutCalls===calls+1);// the fixture's 503 keeps this page for the checks below
   for(const available of [{email:true,email_delivery:true,google:false},{email:false,google:true},{setup_required:true}]){
    methods={email_confirmation:true,setup_required:false,...available};const tries=logoutCalls;
    await openLogout();await page.keyboard.press('Enter');await until(()=>logoutCalls===tries+1);assert.equal(await warning.count(),0,'no warning when a sign-in method exists: '+JSON.stringify(available));
   }
   methods=null;
  }
  // The profile pass checks Sign out reached the stalled sign-in check first and gave up on it after about 3 s, well inside the 15 s page timeout.
  const stalledFirst=mark=>{if(!stallMethods)return;const [check,logout]=events.slice(mark,mark+2);assert.deepEqual([check?.[0],logout?.[0]],['methods','logout'],'Sign out waits for the sign-in check first');assert.ok(logout[1]-check[1]<7000,'stalled check deadline '+(logout[1]-check[1])+' ms');};
  await openLogout();
  unavailable=true;const before=logoutCalls;
  stallMethods=section==='profile';// the profile pass also proves a stalled sign-in methods check cannot hold Sign out
  let mark=events.length;await page.locator('#logoutButton').focus();await page.keyboard.press('Enter');
  await page.waitForFunction(()=>{const m=document.querySelector('#sessionMessage');return !m.hidden&&m.textContent.includes('Sign out could not')});stalledFirst(mark);
  assert.equal(await page.locator('#sessionMessage').isVisible(),true,'logout error must be visible in every section');
  assert.equal(logoutCalls,before+1);assert.equal(await page.evaluate(()=>GenerationKey.value()),'fixture-personal-key');
  assert.equal(await page.locator('#logoutButton').isDisabled(),false);assert.equal(await page.evaluate(()=>localStorage.getItem('gateway-sign-out')),null);
  assert.equal(await page.evaluate(()=>localStorage.getItem('gateway-theme')),'dark','failed logout preserves preference');
  unavailable=false;await openLogout();mark=events.length;await Promise.all([page.waitForEvent('load'),page.keyboard.press('Enter')]);
  assert.equal(logoutCalls,before+2);stalledFirst(mark);stallMethods=false;
  assert.equal(await page.evaluate(()=>sessionStorage.getItem('gateway-generation-key')?.includes('fixture-personal-key')||false),false);
  assert.equal(await page.evaluate(()=>window.themeAtBoot),null,'successful logout clears preference before reload');
  assert.ok(await page.evaluate(()=>localStorage.getItem('gateway-sign-out')));assert.deepEqual(errors,[]);await page.close();
 }
 console.log('PASS: failed logout visibly offers retry from Playground/Profile and preserves key; successful logout clears key; no-sign-in warning (mouse and keyboard, Stay/Escape/Close/Sign out anyway, none with a working method); stalled sign-in check gives up in time; desktop/mobile.');
})().catch(error=>{console.error(error);process.exitCode=1}).finally(async()=>{await browser?.close();server.close()});
