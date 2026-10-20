const {evidencePath} = require('./evidence.cjs');
// Email authentication UI with local API fixtures; never sends actual email.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
let registrationHandler;
const server=http.createServer((req,res)=>{if(registrationHandler&&['/auth/email/registration','/auth/email/complete'].includes(req.url))return registrationHandler(req,res);const name=path.basename(req.url.split('?')[0])||'index.html',file=path.join(root,name.includes('.')?name:'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await(process.env.UI_BROWSER_ENGINE==='webkit'?webkit.launch({headless:true}):chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})}));
 const page=await browser.newPage({viewport:{width:390,height:844},colorScheme:'dark',isMobile:true,hasTouch:true});page.setDefaultTimeout(7000);
 const errors=[],posts=[];let delivery=false,signedIn=false,failLogin=true,verifyFailure=false,pendingRegistration=false;
 // Use real HTTP requests for cookie checks; WebKit interception omits Cookie.
 registrationHandler=(req,res)=>{
  const grant=pendingRegistration&&req.headers.cookie?.includes('media_gateway_registration=fixture-grant');
  res.setHeader('Content-Type','application/json');
  if(req.url.endsWith('/registration'))return res.end(JSON.stringify(grant?{pending:true,email:'user@example.test'}:{pending:false}));
  if(!grant){res.statusCode=400;return res.end(JSON.stringify({error:{message:'Missing registration grant'}}));}
  pendingRegistration=false;signedIn=true;res.setHeader('Set-Cookie','media_gateway_registration=; Path=/auth/email; HttpOnly; SameSite=Strict; Max-Age=0');res.end(JSON.stringify({message:'Your account is ready.'}));
 };
 let completeRegistration,completeVerification;
 const registrationPending=new Promise(resolve=>{completeRegistration=resolve;}),verificationPending=new Promise(resolve=>{completeVerification=resolve;});
 page.on('pageerror',e=>{if(!e.message.includes('ResizeObserver loop completed'))errors.push(e.message)});
 await page.route('**/v1/**',route=>{const p=new URL(route.request().url()).pathname;if(p==='/v1/info')return route.fulfill({json:{auth_mode:'google',console:{generation_ui:true,personal_providers:true}}});if(p==='/v1/me')return signedIn?route.fulfill({json:{id:'test-user',session_id:'session',name:'Email User',email:'user@example.test'}}):route.fulfill({status:401,json:{error:{message:'Sign in'}}});return route.fulfill({json:{data:[]}})});
 await page.route('**/auth/methods',route=>route.fulfill({json:{google:true,email:delivery,email_delivery:delivery,email_confirmation:true}}));
 await page.route('**/auth/email/**',async route=>{
  const action=new URL(route.request().url()).pathname.split('/').pop(),data=route.request().postDataJSON();posts.push({action,data});
  if(action==='registration')return route.continue();
  if(action==='register')await registrationPending;
  if(action==='complete'){assert.deepEqual(Object.keys(data),['password']);return route.continue();}
  if(action==='verify'){
   await verificationPending;
   if(verifyFailure)return route.fulfill({status:400,json:{error:{code:'invalid_link',message:'This confirmation link has expired or already been used. If you have confirmed your email, sign in. Otherwise, request a new confirmation email.'}}});
   pendingRegistration=true;return route.fulfill({headers:{'Set-Cookie':'media_gateway_registration=fixture-grant; Path=/auth/email; HttpOnly; SameSite=Strict'},json:{message:'Email confirmed. Choose your password.',email:'user@example.test'}});
  }
  if(action==='login'){if(failLogin)return route.fulfill({status:401,json:{error:{message:'Email or password is incorrect. Confirm your email before signing in.'}}});signedIn=true;return route.fulfill({json:{message:'Signed in.'}})}
  return route.fulfill({status:['register','forgot'].includes(action)?202:200,json:{message:'Check your email for the next step.',email:'user@example.test'}});
 });
 const base='http://127.0.0.1:'+server.address().port;
 const click=async id=>page.locator('#'+id).tap();
 const active=()=>page.locator('.email-auth-form:not([hidden])'),email=()=>active().locator('[name=email]'),password=()=>active().locator('[name=password]'),submit=()=>active().locator('[type=submit]'),status=()=>active().locator('[data-auth-status]');
 async function switchUnderHeading(mode){
  const hint=page.locator('#authModeSwitch');await hint.waitFor();
  assert.equal(await page.locator('.auth-card-heading #authSwitch').innerText(),mode==='login'?'Sign up':'Sign in');
  assert.equal(await page.locator('#authModeHint').innerText(),mode==='login'?'Don’t have an account?':'Already have an account?');
  const title=await page.locator('#authTitle').boundingBox(),line=await hint.boundingBox(),form=await active().boundingBox();
  assert.ok(line.y>=title.y+title.height,'Switch must follow the title');assert.ok(line.y+line.height<=form.y,'Switch must precede the fields');
 }
 async function passwordMetadata(mode){
  assert.equal(await active().getAttribute('id'),'email-'+mode+'-form');
  assert.equal(await active().getAttribute('method'),'post');assert.equal(await active().getAttribute('action'),'/auth/email/'+mode);
  assert.equal(await email().getAttribute('autocomplete'),'username');
  assert.equal(await password().getAttribute('id'),'email-'+mode+'-password');
  assert.equal(await password().getAttribute('autocomplete'),mode==='login'?'current-password':'new-password');
  assert.equal(await password().getAttribute('type'),'password');
  if(mode!=='login'){
   assert.equal(await password().getAttribute('minlength'),'15');assert.equal(await password().getAttribute('maxlength'),'128');
   assert.equal(await password().getAttribute('passwordrules'),'minlength: 15; maxlength: 128;');
  }
  assert.equal(await active().locator('.secret-input').count(),0);
  assert.equal(await password().getAttribute('data-lpignore'),null);
 }
 async function togglePassword(){
  const value='A long secret passphrase 123!',count=posts.length;
  await password().fill(value);await password().evaluate(e=>{e.focus();e.setSelectionRange(3,8)});
  let toggle=active().getByRole('button',{name:'Show password',exact:true});
  assert.equal(await toggle.getAttribute('aria-controls'),await password().getAttribute('id'));
  const fieldBox=await password().boundingBox(),buttonBox=await toggle.boundingBox();
  assert.ok(buttonBox.width>=44&&buttonBox.height>=44,'Password toggle needs a touch target');
  assert.ok(buttonBox.x>=fieldBox.x&&buttonBox.x+buttonBox.width<=fieldBox.x+fieldBox.width,'Toggle must stay inside the field');
  await toggle.tap();assert.equal(await password().getAttribute('type'),'text');assert.equal(await password().inputValue(),value);
  assert.deepEqual(await password().evaluate(e=>[e===document.activeElement,e.selectionStart,e.selectionEnd]),[true,3,8]);
  toggle=active().getByRole('button',{name:'Hide password',exact:true});await toggle.focus();await page.keyboard.press('Space');
  assert.equal(await password().getAttribute('type'),'password');assert.equal(await password().inputValue(),value);
  await page.keyboard.press('Enter');assert.equal(await password().getAttribute('type'),'text');assert.equal(posts.length,count,'Show/hide must not submit the form');
 }
 try{
  await page.goto(base);await page.locator('#googleLogin').waitFor();
  assert.equal(await page.locator('#googleLogin').innerText(),'Continue with Google');
  assert.equal(await page.locator('#emailLogin').isVisible(),false);
  assert.equal(await page.locator('.auth-card a[href="/docs"]').count(),0);assert.equal(await page.locator('#docsNav').isVisible(),true);assert.equal(await page.locator('#authModeSwitch').isVisible(),false);
  assert.ok(posts.every(p=>p.action==='registration'));
  delivery=true;await page.reload();await click('emailLogin');await switchUnderHeading('login');await passwordMetadata('login');
  await togglePassword();await click('authSwitch');await switchUnderHeading('register');
  assert.equal(await password().count(),0,'Registration must request only email');
  assert.equal(await page.locator('#email-login-password').getAttribute('type'),'password');
  assert.equal(await page.locator('#email-login-password').inputValue(),'');
  await email().fill('user@example.test');await submit().tap();
  assert.equal(await page.locator('#authSwitch').isDisabled(),true);completeRegistration();
  await active().getByText('Check your email for the next step.').waitFor();assert.deepEqual(posts.at(-1),{action:'register',data:{email:'user@example.test'}});
  await click('authSwitch');await password().fill('Wrong long password');await submit().tap();
  await page.getByText(/Email or password is incorrect/).waitFor();assert.equal(await status().evaluate(e=>getComputedStyle(e).color),'rgb(240, 128, 136)');
  await click('authForgot');assert.equal(await password().count(),0);await submit().tap();
  await active().getByText('Check your email for the next step.').waitFor();assert.deepEqual(posts.at(-1),{action:'forgot',data:{email:'user@example.test'}});
  assert.equal(await page.locator('#authModeSwitch').isVisible(),false);assert.equal(await page.locator('#authBack').innerText(),'Back to sign in');await click('authBack');await switchUnderHeading('login');
  await click('authBack');assert.equal(await page.locator('#authModeSwitch').isVisible(),false);await page.locator('#authChoices').waitFor();
  const token='a'.repeat(43);await page.goto(base+'/#email_reset='+token);await page.getByRole('heading',{name:'Choose a new password'}).waitFor();assert.ok(!page.url().includes(token));
  await passwordMetadata('reset');await togglePassword();
  await password().fill('A new secret passphrase 456!');await submit().tap();await page.getByText('Password saved. Sign in with your new password.').waitFor();
  assert.equal(posts.at(-1).action,'reset');assert.equal(posts.at(-1).data.token,token);
  await passwordMetadata('login');assert.equal(await password().inputValue(),'');assert.equal(await email().inputValue(),'user@example.test');
  // Verification runs automatically, including when delivery has since been disabled.
  delivery=false;const beforeVerify=posts.length;
  await page.goto(base+'/#email_verify='+token);await page.getByRole('heading',{name:'Confirming your email'}).waitFor();
  assert.equal(await page.locator('.email-auth-form:visible').count(),0);assert.equal(await page.locator('#authGate button:visible').count(),0);
  assert.ok(!page.url().includes(token));await page.evaluate(()=>window.EmailLogin.initialize());
  completeVerification();await page.getByRole('heading',{name:'Choose your password',exact:true}).waitFor();
  assert.deepEqual(posts.slice(beforeVerify),[{action:'verify',data:{token}}],'Confirmation must exchange only the link token once');
  assert.equal(signedIn,false,'Confirmation must not authenticate before setting a password');
  await passwordMetadata('complete');assert.equal(await email().inputValue(),'user@example.test');
  assert.ok(!(await page.evaluate(()=>document.cookie)).includes('media_gateway_registration'));
  const grant=(await page.context().cookies(base+'/auth/email')).find(c=>c.name==='media_gateway_registration');assert.ok(grant.httpOnly);assert.equal(grant.path,'/auth/email');assert.equal(grant.sameSite,'Strict');
  await togglePassword();
  // Refresh resumes the restricted registration; the email link is already consumed.
  await page.reload();await page.getByRole('heading',{name:'Choose your password',exact:true}).waitFor();
  await passwordMetadata('complete');assert.equal(await password().inputValue(),'');
  for(const width of [320,390,1440]){await page.setViewportSize({width,height:900});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:evidencePath(`/tmp/gateway-auth-setup-${process.env.UI_BROWSER_ENGINE||'chromium'}-${width}.png`)});}
  await password().fill('A confirmed owner passphrase 789!');await submit().tap();await page.locator('#authGate').waitFor({state:'hidden'});
  assert.equal(posts.filter(p=>p.action==='complete').length,1);
  assert.ok(await page.evaluate(()=>![...Object.values(localStorage),...Object.values(sessionStorage)].some(v=>v.includes('passphrase')||v.includes('a'.repeat(43)))));
  signedIn=false;verifyFailure=true;await page.goto(base+'/#email_verify='+token);await page.getByText(/This confirmation link has expired/).waitFor();
  assert.equal(await page.locator('.email-auth-form:visible').count(),0);await click('authBack');await passwordMetadata('login');
  delivery=true;await page.reload();await click('emailLogin');
  for(const width of [320,390,1440]){
   await page.setViewportSize({width,height:900});await switchUnderHeading('login');assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await page.screenshot({path:evidencePath(`/tmp/gateway-auth-signin-${process.env.UI_BROWSER_ENGINE||'chromium'}-${width}.png`)});
   await click('authSwitch');await switchUnderHeading('register');assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await page.screenshot({path:evidencePath(`/tmp/gateway-auth-signup-${process.env.UI_BROWSER_ENGINE||'chromium'}-${width}.png`)});await click('authSwitch');
  }
  failLogin=false;await email().fill('user@example.test');await password().fill('A long secret passphrase 123!');await submit().tap();await page.locator('#authGate').waitFor({state:'hidden'});
  assert.deepEqual(errors,[]);console.log('Email UI passed: stable password-manager metadata, show/hide by touch and keyboard, responsive layout, email-only signup, login/reset, token exchange, resumable password setup and sign-in only after password creation. Native password-manager popups depend on browser settings.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close()});
