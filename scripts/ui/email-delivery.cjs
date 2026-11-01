const {evidencePath} = require('./evidence.cjs');
// Local UI fixtures: administrator configuration and the public sign-in choice.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const name=path.basename(req.url.split('?')[0])||'index.html',file=path.join(root,name.includes('.')?name:'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await(process.env.UI_BROWSER_ENGINE==='webkit'?webkit.launch({headless:true}):chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})}));
 const page=await browser.newPage({viewport:{width:1440,height:1100}});page.setDefaultTimeout(7000);
 let admin=true,failSave=false,mail={provider:'',from:'',key_configured:false,enabled:false,version:1,available_providers:[{id:'resend',name:'Resend'}]},posts=[];
 let methods={google:true,email:true};let loginRows=[{provider:'google',name:'Google',enabled:true,connected:true,configured:true,client_id:'old.apps.googleusercontent.com',secret_configured:true,redirect_url:'https://gateway.test/auth/google/callback',version:1},{provider:'email',name:'Email and password',enabled:true,connected:true,configured:true,version:1}],loginWrites=[],loginConflict=false;
 let storage={driver:'s3',endpoint:'https://s3.example.test',region:'eu-test',bucket:'files',prefix:'',path_style:true,access_key_id:'fixture-access',credentials_configured:true},storagePosts=[];
 const errors=[];page.on('pageerror',e=>{if(!e.message.includes('ResizeObserver loop completed'))errors.push(e.message)});
 const policy={generation_ui:true,personal_providers:true,available_providers:[],version:1};
 await page.route('**/v1/**',route=>{const req=route.request(),p=new URL(req.url()).pathname;
  if(p==='/v1/info')return route.fulfill({json:{auth_mode:'google',console:policy}});
  if(p==='/v1/me')return admin?route.fulfill({json:{id:'self',name:'Admin',email:'admin@example.test',is_system_admin:true}}):route.fulfill({status:401,json:{error:{message:'Sign in'}}});
  if(p==='/v1/system/login-providers')return route.fulfill({json:{data:loginRows,available_providers:[{id:'google',name:'Google'},{id:'email',name:'Email and password'}],email_delivery_configured:mail.enabled}});
  if(p.startsWith('/v1/system/login-providers/')){const kind=p.split('/').at(-1),data=req.postDataJSON(),old=loginRows.find(r=>r.provider===kind);loginWrites.push(data);if(loginConflict||data.version!==old.version)return route.fulfill({status:409,json:{error:{message:'Settings changed'}}});const {client_secret,...publicData}=data,next={...old,...publicData,connected:true,configured:true,secret_configured:kind==='google',version:old.version+1};methods[kind]=next.enabled;loginRows=loginRows.map(r=>r===old?next:r);return route.fulfill({json:next});}
  if(p==='/v1/system/catalog/settings')return route.fulfill({json:{timezone:'UTC',email:'',version:1}});
  if(p==='/v1/system/storage/quota')return route.fulfill({json:{limit_bytes:null,used_bytes:0,version:1}});
  if(p==='/v1/system/settings')return route.fulfill({json:policy});
  if(p==='/v1/sales/bonuses')return route.fulfill({json:{settings:{manager_percent:5,supervisor_percent:1,version:1},people:[]}});
  if(p==='/v1/admin/storage'||p==='/v1/system/storage'){
   if(req.method()==='PUT'){const data=req.postDataJSON();storagePosts.push(data);const {secret_access_key,...settings}=data;storage={...storage,...settings};}
   return route.fulfill({json:storage});
  }
  if(p==='/v1/system/email'){
   if(req.method()==='PUT'){const data=req.postDataJSON();posts.push(data);if(failSave)return route.fulfill({status:500,json:{error:{message:'Could not save settings'}}});mail={...mail,provider:data.provider,from:data.provider?data.from:'',key_configured:!!data.provider,enabled:!!data.provider,version:mail.version+1};}
   return route.fulfill({json:mail});
  }
  return route.fulfill({json:{data:[]}});
 });
 await page.route('**/auth/methods',route=>route.fulfill({json:{google:methods.google,email:methods.email&&mail.enabled,email_delivery:mail.enabled,email_confirmation:methods.email}}));
 const base='http://127.0.0.1:'+server.address().port,form=page.locator('.email-delivery-settings');
 async function navigate(url){await page.goto(url);if(!new URL(url).pathname.endsWith('/login.html'))await page.waitForFunction(()=>consoleInitialized&&(!consoleAuthenticated||window.Workspace?.ready()));}
 const save=()=>form.getByRole('button',{name:'Save',exact:true});
 const key=()=>form.locator('input[name=key]');
 async function saved(){await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(await key().inputValue(),mail.key_configured?'************':'');}
 async function credential(input){assert.equal(await input.getAttribute('type'),'text');assert.equal(await input.getAttribute('autocomplete'),'off');assert.equal(await input.getAttribute('data-1p-ignore'),'true');assert.equal(await input.inputValue(),'************');}
 try{
  await navigate(base+'/system');await page.getByRole('tab',{name:'Email delivery',exact:true}).click();await form.waitFor();await page.waitForFunction(()=>document.querySelector('.email-delivery-settings select')?.options.length===2);
  assert.equal(await save().isVisible(),false);assert.equal(await form.locator('input[name=key]').isVisible(),false);
  await form.locator('select').selectOption('resend',{force:true});await form.locator('input[name=sender]').fill('Sociaro <login@example.test>');await form.locator('input[name=key]').fill('fixture-resend-key');
  await save().click();await saved();assert.equal(posts.at(-1).api_key,'fixture-resend-key');assert.equal(posts.at(-1).provider,'resend');
  await credential(key());assert.equal(await form.getByLabel('API key',{exact:true}).count(),1);
  await form.getByRole('button',{name:'Saved',exact:true}).waitFor({state:'hidden'});
  for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'page overflow');await form.screenshot({path:evidencePath(`/tmp/gateway-email-settings-${width}.png`)});}
  await form.locator('input[name=sender]').fill('Support <support@example.test>');await save().click();await saved();assert.equal('api_key' in posts.at(-1),false);
  await page.reload();await page.getByRole('tab',{name:'Email delivery',exact:true}).click();await form.locator('input[name=sender]').waitFor();assert.equal(await form.locator('input[name=sender]').inputValue(),'Support <support@example.test>');await credential(key());
  await key().click();await key().press('r');await key().pressSequentially('eplacement-key');assert.equal(await key().inputValue(),'replacement-key');
  failSave=true;await save().click();await form.getByText('Could not save settings',{exact:true}).waitFor();assert.equal(await key().inputValue(),'replacement-key');
  failSave=false;await save().click();await saved();assert.equal(posts.at(-1).api_key,'replacement-key');
  await key().evaluate(input=>{const clipboardData=new DataTransfer();clipboardData.setData('text/plain','pasted-replacement');input.dispatchEvent(new ClipboardEvent('paste',{bubbles:true,cancelable:true,clipboardData}));});
  assert.equal(await key().inputValue(),'pasted-replacement');await save().click();await saved();assert.equal(posts.at(-1).api_key,'pasted-replacement');
  await form.locator('input[name=sender]').fill('Another <another@example.test>');await save().click();await saved();assert.equal('api_key' in posts.at(-1),false);
  const tabs=page.getByRole('tablist',{name:'Settings sections'});
  // This workflow needs its own sections, not a frozen inventory of every setting.
  // New independent settings sections must not break email/login publication checks.
  for(const name of ['Email delivery','Login methods']){
   const tab=tabs.getByRole('tab',{name,exact:true});assert.equal(await tab.count(),1);
   assert.equal(await tab.getAttribute('aria-controls'),name==='Email delivery'?'settings-panel-email':'settings-panel-login');
  }
  await page.getByRole('tab',{name:'Login methods',exact:true}).click();const login=page.locator('.login-method-settings'),google=login.locator('[data-provider=google]'),email=login.locator('[data-provider=email]');
  const storedMail=JSON.stringify(mail);
  await email.getByRole('switch').uncheck();await page.waitForFunction(()=>!document.querySelector('[data-provider=email] input[type=checkbox]').disabled);assert.equal(methods.email,false);assert.equal(JSON.stringify(mail),storedMail);
  await credential(google.getByLabel('Client Secret',{exact:true}));
  await google.getByLabel('Client ID',{exact:true}).fill('new.apps.googleusercontent.com');
  await page.getByRole('tab',{name:'Email delivery',exact:true}).click();await form.locator('input[name=sender]').fill('Draft <draft@example.test>');
  await page.getByRole('tab',{name:'Login methods',exact:true}).click();await google.getByRole('switch').uncheck();await page.waitForFunction(()=>!document.querySelector('[data-provider=google] input[type=checkbox]').disabled);
  assert.equal(await google.getByLabel('Client ID',{exact:true}).inputValue(),'new.apps.googleusercontent.com');
  loginConflict=true;await google.getByRole('button',{name:'Save',exact:true}).click();await google.getByText('Settings changed. Reload before saving.').waitFor();assert.equal(await google.getByLabel('Client ID',{exact:true}).inputValue(),'new.apps.googleusercontent.com');loginConflict=false;
  await google.getByRole('button',{name:'Save',exact:true}).click();await google.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal('client_secret' in loginWrites.at(-1),false);
  await google.getByLabel('Client Secret',{exact:true}).fill('rotated-secret');await google.getByRole('button',{name:'Save',exact:true}).click();await page.waitForFunction(()=>document.querySelector('[data-provider=google] .secret-input').value==='************');assert.equal(loginWrites.at(-1).client_secret,'rotated-secret');
  await page.getByRole('tab',{name:'Email delivery',exact:true}).click();assert.equal(await form.locator('input[name=sender]').inputValue(),'Draft <draft@example.test>');
  await page.getByRole('tab',{name:'Login methods',exact:true}).click();assert.equal(JSON.stringify(mail),storedMail);
  for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));await page.screenshot({path:evidencePath(`/tmp/gateway-login-methods-${width}.png`),fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Login methods overflow '+width+' '+JSON.stringify(await page.evaluate(()=>[...document.querySelectorAll('body *')].filter(n=>n.getBoundingClientRect().right>innerWidth+1).map(n=>({tag:n.tagName,cls:n.className,width:n.getBoundingClientRect().width}))))); }
  await navigate(base+'/login.html');await page.locator('#authUnavailable').waitFor();assert.equal(await page.locator('#googleLogin').isVisible(),false);assert.equal(await page.locator('#emailLogin').isVisible(),false);
  await navigate(base+'/system');await page.getByRole('tab',{name:'Login methods',exact:true}).click();await email.getByRole('switch').check();await google.getByRole('switch').check();await page.waitForFunction(()=>Array.from(document.querySelectorAll('.login-provider-card input[type=checkbox]')).every(e=>!e.disabled));assert.equal(JSON.stringify(mail),storedMail);
  await navigate(base+'/login.html');await page.locator('#emailLogin').waitFor();assert.equal(await page.locator('#googleLogin').isVisible(),true);
  // An unconnected supported provider can be added without editing server files.
  loginRows=loginRows.map(r=>r.provider==='google'?{...r,connected:false,configured:false,enabled:false,client_id:'',secret_configured:false,version:1}:r);methods.google=false;
  await navigate(base+'/system');await page.getByRole('tab',{name:'Login methods',exact:true}).click();await login.getByRole('button',{name:'Add method',exact:true}).click();
  await google.getByLabel('Client ID',{exact:true}).fill('added.apps.googleusercontent.com');await google.getByLabel('Client Secret',{exact:true}).fill('new-client-secret');
  await google.getByRole('button',{name:'Save',exact:true}).click();await google.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(methods.google,false);
  await google.getByRole('switch').check();await page.waitForFunction(()=>!document.querySelector('[data-provider=google] input[type=checkbox]').disabled);assert.equal(methods.google,true);
  await navigate(base+'/system');await page.locator('[data-system-page=sharedStorage]').click();const secret=page.locator('#storageSecretKey');await secret.waitFor();await credential(secret);
  await page.locator('#storageBucket').fill('renamed-files');await page.locator('#saveStorage').click();await page.locator('#saveStorage').filter({hasText:'Saved'}).waitFor();await credential(secret);assert.equal(storagePosts.at(-1).secret_access_key,'');
  await secret.click();await secret.press('s');await secret.pressSequentially('torage-replacement');await page.locator('#saveStorage').click();await page.waitForFunction(()=>document.querySelector('#storageSecretKey').value==='************');assert.equal(storagePosts.at(-1).secret_access_key,'storage-replacement');
  await page.reload();await page.locator('[data-system-page=sharedStorage]').click();await secret.waitFor();await credential(secret);await page.locator('#storageBucket').fill('next-files');await page.locator('#saveStorage').click();await page.locator('#saveStorage').filter({hasText:'Saved'}).waitFor();assert.equal(storagePosts.at(-1).secret_access_key,'');
  admin=false;await navigate(base);await page.locator('#emailLogin').waitFor();assert.equal(await page.locator('#googleLogin').isVisible(),true);
  admin=true;await navigate(base+'/system');await page.getByRole('tab',{name:'Email delivery',exact:true}).click();await form.locator('input[name=sender]').waitFor();await form.locator('select').selectOption('',{force:true});await save().click();await saved();assert.equal(mail.enabled,false);
  await page.getByRole('tab',{name:'Login methods',exact:true}).click();await email.getByText('Email login is enabled, but it will not be shown to users until Email delivery is configured. The enabled preference is preserved.',{exact:true}).waitFor();
  assert.equal(await email.getByRole('switch').isChecked(),true);assert.equal(methods.email,true);
  for(const theme of ['dark','light']){await page.evaluate(theme=>GatewayTheme.apply(theme),theme);for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await email.screenshot({path:evidencePath(`/tmp/gateway-email-login-warning-${theme}-${width}.png`)});}}
  admin=false;for(const path of ['/?auth_error=denied','/login.html?auth_error=denied']){await navigate(base+path);await page.locator('#googleLogin').waitFor();assert.equal(await page.locator('#emailLogin').isVisible(),false);await page.getByText('Could not sign in. Please try again or contact your administrator.',{exact:true}).waitFor();}
  methods.google=false;await navigate(base);await page.locator('#authUnavailable').waitFor();assert.equal(await page.locator('#emailLogin').isVisible(),false);assert.equal(await page.locator('#googleLogin').isVisible(),false);assert.equal(await page.locator('.email-auth-form:visible').count(),0);
  const methodWrites=loginWrites.length;admin=true;await navigate(base+'/system');await page.getByRole('tab',{name:'Email delivery',exact:true}).click();await form.locator('select').selectOption('resend',{force:true});await form.locator('input[name=sender]').fill('Restored <restored@example.test>');await key().fill('fixture-restored-mail-key');await save().click();await saved();
  await page.getByRole('tab',{name:'Login methods',exact:true}).click();assert.equal(await email.getByRole('switch').isChecked(),true);assert.equal(await email.locator('.settings-warning').isVisible(),false);assert.equal(loginWrites.length,methodWrites,'restoring delivery must not rewrite login preferences');
  admin=false;await navigate(base);await page.locator('#emailLogin').waitFor();assert.equal(await page.locator('#googleLogin').isVisible(),false);
  assert.deepEqual(errors,[]);console.log('PASS: Resend and storage saved masks, key replacement/retry/preservation, non-password fields, login visibility, disable, Saved feedback and responsive layout.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close()});
