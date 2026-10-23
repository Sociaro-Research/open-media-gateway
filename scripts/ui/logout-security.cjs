// Exercise the real console code with a failed session revocation and a retry.
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const {[engine]:chromium}=require('playwright');
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path');
const ui=path.resolve(__dirname,'../../web/public/ui');
let unavailable=true,logoutCalls=0;
const server=http.createServer((req,res)=>{
 if(req.url==='/auth/logout'){logoutCalls++;return res.writeHead(unavailable?503:204,{'Content-Type':'application/json'}).end(unavailable?JSON.stringify({error:{message:'retry'}}):undefined)}
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
  await openLogout();
  unavailable=true;const before=logoutCalls;
  await page.locator('#logoutButton').focus();await page.keyboard.press('Enter');
  await page.waitForFunction(()=>document.querySelector('#sessionMessage').textContent.includes('Sign out could not'));
  assert.equal(await page.locator('#sessionMessage').isVisible(),true,'logout error must be visible in every section');
  assert.equal(logoutCalls,before+1);assert.equal(await page.evaluate(()=>GenerationKey.value()),'fixture-personal-key');
  assert.equal(await page.locator('#logoutButton').isDisabled(),false);assert.equal(await page.evaluate(()=>localStorage.getItem('gateway-sign-out')),null);
  assert.equal(await page.evaluate(()=>localStorage.getItem('gateway-theme')),'dark','failed logout preserves preference');
  unavailable=false;await openLogout();await Promise.all([page.waitForEvent('load'),page.keyboard.press('Enter')]);
  assert.equal(await page.evaluate(()=>sessionStorage.getItem('gateway-generation-key')?.includes('fixture-personal-key')||false),false);
  assert.equal(await page.evaluate(()=>window.themeAtBoot),null,'successful logout clears preference before reload');
  assert.ok(await page.evaluate(()=>localStorage.getItem('gateway-sign-out')));assert.deepEqual(errors,[]);await page.close();
 }
 console.log('PASS: failed logout visibly offers retry from Playground/Profile and preserves key; successful logout clears key; desktop/mobile and keyboard.');
})().catch(error=>{console.error(error);process.exitCode=1}).finally(async()=>{await browser?.close();server.close()});
