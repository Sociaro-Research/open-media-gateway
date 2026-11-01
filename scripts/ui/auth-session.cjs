// Real console bootstrap with gated localhost responses; no external services.
const {evidencePath}=require('./evidence.cjs');
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',browserType=require('playwright')[engine];
const ui=process.env.UI_SOURCE_DIR||path.resolve(__dirname,'../../web/public/ui');
const output=evidencePath('/tmp/gateway-auth-session/');fs.mkdirSync(output,{recursive:true});
let state, browser;
const user={id:'self',name:'Fixture User',email:'fixture@example.test',session_id:'fixture-session',is_system_admin:true};
const server=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://localhost').pathname;
 const json=(body,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(body));};
 if(p==='/site.json')return json({});
 if(p==='/v1/info'){
  state.infoCalls++;
  if(state.infoError)return json({},state.infoStatus||503);
  return json({auth_mode:state.apiKey?'api_key':'google',console:{generation_ui:true,personal_providers:true,version:1}});
 }
 if(p==='/v1/me'){
  state.meCalls++;
  if(state.hold){state.pending.push(()=>json(state.body,state.status));return;}
  if(state.network){req.socket.destroy();return;}
  return json(state.body,state.status);
 }
 if(p==='/auth/methods'){state.methodCalls++;return json({email:true,google:true,email_delivery:true});}
 if(p==='/auth/email/registration')return json({pending:false});
 if(p==='/auth/email/login'){state.status=200;state.body=user;return json({});}
 if(p==='/auth/logout'){state.logoutCalls++;if(state.logoutError)return json({},503);state.status=401;state.body={};return res.writeHead(204).end();}
 if(p==='/v1/organizations')return json({data:[]},state.workspaceError?503:200);
 if(p.startsWith('/v1/'))return json({data:[]});
 const file=path.join(ui,p.startsWith('/ui/')?path.basename(p):'index.html');
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
function reset(){state={status:200,body:user,hold:false,pending:[],infoCalls:0,meCalls:0,methodCalls:0,logoutCalls:0};}
const results=[];
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 browser=await browserType.launch({headless:true,...(engine!=='webkit'&&process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 for(const width of [1440,390,320])for(const mode of ['dark','light','system']){
  reset();const context=await browser.newContext({viewport:{width,height:1000},colorScheme:mode==='system'?'light':mode});
  await context.addInitScript(mode=>{
   if(mode!=='system')localStorage.setItem('gateway-theme',mode);
   window.loginFrames=0;window.protectedBeforeCheck=0;
   const visible=id=>{const n=document.getElementById(id);return n&&n.getClientRects().length>0&&getComputedStyle(n).visibility!=='hidden';};
   const sample=()=>{if(visible('authGate'))window.loginFrames++;if(visible('userMenu')&&visible('authCheck'))window.protectedBeforeCheck++;requestAnimationFrame(sample);};requestAnimationFrame(sample);
  },mode);
  const page=await context.newPage();page.setDefaultTimeout(10000);const errors=[];page.on('pageerror',e=>{if(!e.message.startsWith('ResizeObserver loop'))errors.push(e.message);});
  const hidden=async()=>{assert.equal(await page.locator('#authGate').isVisible(),false);assert.equal(await page.locator('#userMenu').isVisible(),false);assert.equal(await page.locator('#generationView').isVisible(),false);assert.equal(await page.locator('#configurationView').isVisible(),false);};
  const frame=()=>page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
  const loading=async(paint=true)=>{await page.locator('#authCheck').waitFor();await hidden();assert.equal(await page.locator('#authCheckStatus').getAttribute('aria-busy'),'true');assert.equal(await page.locator('#authRetry').isVisible(),false);if(paint)await frame();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);};
  const signedIn=async()=>{await page.locator('#userMenu').waitFor();await page.locator('#generationView').waitFor();assert.equal(await page.locator('#authGate').isVisible(),false);assert.equal(await page.locator('#authCheck').isVisible(),false);};
  const failed=async()=>{await page.getByRole('button',{name:'Try again',exact:true}).waitFor();await hidden();assert.equal(await page.locator('#authCheckStatus').getAttribute('aria-busy'),'false');};
  const release=()=>{state.hold=false;const pending=state.pending.splice(0);for(const resolve of pending)resolve();};
  const pending=async()=>{await page.waitForRequest(r=>new URL(r.url()).pathname==='/v1/me');};
  try{
   // Freeze the application script: the HTML itself must be safe at first paint.
   let resume;const gate=new Promise(r=>resume=r);await page.route('**/ui/app.js*',async route=>{await gate;await route.continue();});
   state.hold=true;await page.goto(base+'/',{waitUntil:'commit'});await page.locator('#authGate').waitFor({state:'attached'});
   const initiallyHidden=await page.locator('#authGate').evaluate(n=>n.hidden);
   if(!initiallyHidden)resume();
   assert.equal(initiallyHidden,true,'Login must be hidden in initial HTML');await loading(false);
   const requested=pending();resume();await requested;await loading();
   await page.screenshot({path:path.join(output,`${engine}-${mode}-${width}-first-paint.png`)});assert.equal(state.methodCalls,0,'Valid sessions do not wait for login methods');
   if(mode==='system'){await page.emulateMedia({colorScheme:'dark'});await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');}
   // Repeat route changes while the session response is held.
   await page.evaluate(()=>{showConsoleSection('profile');showConsoleSection('generation');});await loading();
   release();await signedIn();await frame();assert.equal(await page.evaluate(()=>window.loginFrames),0,'No login frame during restoration');
   assert.equal(await page.evaluate(()=>window.protectedBeforeCheck),0);
   assert.equal(state.meCalls,1);assert.equal(state.methodCalls,0);
   state.hold=true;const reloading=pending();await page.reload();await reloading;await loading();release();await signedIn();
   // Ordinary navigation does not restart the check or flash login.
   const calls=state.meCalls;await page.evaluate(()=>showConsoleSection('profile'));await page.locator('#profileView').waitFor();await page.evaluate(()=>showConsoleSection('generation'));await signedIn();assert.equal(state.meCalls,calls);
   // Server failures keep both login and protected content closed; keyboard retry.
   state.status=503;await page.reload();await failed();await page.screenshot({path:path.join(output,`${engine}-${mode}-${width}-error.png`)});
   state.status=200;state.hold=true;const retrying=pending();await page.getByRole('button',{name:'Try again',exact:true}).focus();await page.keyboard.press('Enter');await retrying;await loading();release();await signedIn();assert.equal(await page.locator('#userMenuToggle').evaluate(n=>n===document.activeElement),true);
   // Transport, forbidden and malformed responses must not pretend the user signed out.
   state.network=true;await page.reload();await failed();state.network=false;
   state.status=403;await page.reload();await failed();state.status=200;
   for(const malformed of [{},null,false]){state.body=malformed;await page.reload();await failed();}state.body=user;
   state.infoError=true;const meBefore=state.meCalls;await page.reload();await failed();assert.equal(state.meCalls,meBefore,'Unknown auth mode cannot authorize API-key access');state.infoError=false;
   state.infoError=true;state.infoStatus=401;await page.reload();await page.locator('#authGate').waitFor();assert.equal(await page.locator('#authCheck').isVisible(),false);state.infoError=false;
   // Confirmed absent/expired session opens sign-in; email sign-in reloads safely.
   state.status=401;await page.reload();await page.locator('#authGate').waitFor();assert.equal(await page.locator('#userMenu').isVisible(),false);assert.equal(await page.locator('#authCheck').isVisible(),false);
   await page.getByRole('button',{name:'Continue with email',exact:true}).click();const form=page.locator('#email-login-form');await form.getByLabel('Email',{exact:true}).fill('fixture@example.test');await form.getByLabel('Password',{exact:true}).fill('fixture passphrase');await form.getByRole('button',{name:'Sign in',exact:true}).click();await signedIn();
   // Failed logout preserves the session; retry revokes it and shows sign-in.
   const openLogout=async()=>{await page.locator('#userMenuToggle').focus();await page.keyboard.press('ArrowDown');const ids=await page.locator('#userDropdown [role=menuitem]').evaluateAll(nodes=>nodes.filter(n=>n.getClientRects().length&&!n.disabled).map(n=>n.id));const logout=ids.indexOf('logoutButton');assert.ok(logout>=0);await page.keyboard.press('End');assert.equal(await page.locator('#userDropdown').evaluate(n=>n.contains(document.activeElement)&&document.activeElement.id),ids.at(-1));await page.keyboard.press('Home');for(let i=0;i<logout;i++)await page.keyboard.press('ArrowDown');await page.locator('#logoutButton').waitFor();assert.equal(await page.locator('#logoutButton').evaluate(n=>n===document.activeElement),true);};
   state.logoutError=true;await openLogout();await page.keyboard.press('Enter');await page.locator('#sessionMessage').waitFor();assert.equal(await page.locator('#userMenu').isVisible(),true);assert.equal(await page.locator('#authGate').isVisible(),false);
   state.logoutError=false;await openLogout();await page.keyboard.press('Enter');await page.locator('#authGate').waitFor();assert.equal(state.logoutCalls,2);assert.equal(await page.locator('#userMenu').isVisible(),false);
   // A rejected session remembers only a safe destination for the OAuth return.
   await page.goto(base+'/profile?secret=discard#providers');await page.locator('#authGate').waitFor();assert.equal(await page.evaluate(()=>JSON.parse(sessionStorage.getItem('gateway-login-return')).path),'/profile#providers');
   state.status=200;state.body=user;await page.goto(base+'/');await page.locator('#profileView').waitFor();assert.equal(new URL(page.url()).pathname,'/profile');
   // Downstream workspace errors never turn a valid session into a login form.
   state.workspaceError=true;await page.goto(base+'/');await signedIn();await page.locator('#sessionMessage').waitFor();state.workspaceError=false;
   if(width===1440&&mode==='dark'){
    state.hold=true;const stalled=pending();await page.reload();await stalled;await loading();await page.getByRole('button',{name:'Try again',exact:true}).waitFor({timeout:20000});await failed();release();await frame();await failed();
    state.apiKey=true;await page.reload();await page.locator('#generationView').waitFor();assert.equal(await page.locator('#authGate').isVisible(),false);assert.equal(await page.locator('#userMenu').isVisible(),false);
   }
   assert.deepEqual(errors,[]);results.push({engine,width,mode,result:'passed'});console.log(`PASS: ${engine} ${mode} ${width}px`);
  }catch(error){ console.error('Fixture state',JSON.stringify({url:page.url(),state:{...state,pending:state.pending.length},dom:await page.evaluate(()=>({path:location.href,return:sessionStorage.getItem('gateway-login-return'),section:typeof currentConsoleSection==='undefined'?'missing':currentConsoleSection,auth:typeof authState==='undefined'?'missing':authState}))}));throw error; }finally{release();await context.close();}
 }
 fs.writeFileSync(path.join(output,`${engine}-results.json`),JSON.stringify(results,null,2));
 console.log('PASS: first paint, delayed/reloaded session, navigation, errors/retry/timeout, login/logout, return paths, workspace failure, API-key mode, keyboard, themes and responsive layouts.');
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(async()=>{await browser?.close();await new Promise(r=>server.close(r));});
