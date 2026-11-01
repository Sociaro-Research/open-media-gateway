const {evidencePath} = require('./evidence.cjs');
// First-owner browser flow with a local HTTP fixture; no email/provider calls.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
let required=true,posts=[],reject=true;
const server=http.createServer((req,res)=>{
 if(req.url==='/auth/methods'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({setup_required:required,email:false,google:false,email_delivery:false,email_confirmation:!required}));}
 if(req.url==='/auth/setup'){
  let raw='';req.on('data',b=>raw+=b);req.on('end',()=>{const data=JSON.parse(raw);posts.push(data);res.setHeader('Content-Type','application/json');
   if(reject){res.statusCode=403;return res.end(JSON.stringify({error:{message:'The setup code is incorrect.'}}));}
   required=false;res.statusCode=201;res.setHeader('Set-Cookie','media_gateway_session=fixture-session; Path=/; HttpOnly; SameSite=Lax');res.end(JSON.stringify({message:'Your owner account is ready.'}));});return;
 }
 if(req.url==='/auth/email/registration'){res.setHeader('Content-Type','application/json');return res.end('{"pending":false}');}
 if(req.url==='/'&&req.headers.cookie?.includes('fixture-session')){res.setHeader('Content-Type','text/html');return res.end('<h1>Administration</h1>');}
 if(req.url==='/site.json'){res.setHeader('Content-Type','application/json');return res.end('{}');}
 const name=path.basename(req.url.split('?')[0])||'login.html',file=path.join(root,name);
 if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await(process.env.UI_BROWSER_ENGINE==='webkit'?webkit.launch({headless:true}):chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})}));
 const page=await browser.newPage({viewport:{width:1440,height:1000},colorScheme:'dark'});page.setDefaultTimeout(7000);
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 const base='http://127.0.0.1:'+server.address().port,form=page.locator('#email-setup-form'),password=form.locator('[name=password]');
 try{
  await page.goto(base);await page.getByRole('heading',{name:'Create owner account'}).waitFor();
  assert.equal(await page.locator('#authChoices').isVisible(),false);assert.equal(await page.locator('#authNavigation').isVisible(),false);
  assert.equal(await form.getAttribute('action'),'/auth/setup');assert.equal(await password.getAttribute('autocomplete'),'new-password');
  assert.equal(await password.getAttribute('minlength'),'15');assert.equal(await form.locator('[name=code]').getAttribute('autocomplete'),'off');
  await form.getByLabel('Setup code',{exact:true}).fill('x'.repeat(43));await form.getByLabel('Name',{exact:true}).fill('Instance Owner');
  await form.getByLabel('Email',{exact:true}).fill('owner@example.test');await password.fill('An owner passphrase for the local fixture');
  for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Setup overflow '+width);
   await page.screenshot({path:evidencePath(`/tmp/gateway-owner-setup-${process.env.UI_BROWSER_ENGINE||'chromium'}-${width}.png`),fullPage:true});
  }
  await password.focus();await page.keyboard.press('Tab');await page.keyboard.press('Enter');
  // Password visibility is controlled by the same keyboard-accessible component.
  const toggle=form.getByRole('button',{name:/^(Show|Hide) password$/}).last();assert.ok(await toggle.count());
  await form.getByRole('button',{name:'Create owner account',exact:true}).click();await form.getByText('The setup code is incorrect.').waitFor();
  assert.equal(await form.getByLabel('Name',{exact:true}).inputValue(),'Instance Owner');assert.equal(required,true);
  assert.equal(posts.length,1);assert.deepEqual(Object.keys(posts[0]).sort(),['code','email','name','password']);
  assert.ok(await page.evaluate(()=>![...Object.values(localStorage),...Object.values(sessionStorage)].some(v=>v.includes('passphrase')||v.includes('x'.repeat(43)))));
  reject=false;await form.getByRole('button',{name:'Create owner account',exact:true}).click();await page.getByRole('heading',{name:'Administration'}).waitFor();
  const session=(await page.context().cookies()).find(c=>c.name==='media_gateway_session');assert.ok(session?.httpOnly);assert.equal(posts.length,2);
  // Setup creates a session without mail; later ordinary sign-in needs a configured method.
  await page.context().clearCookies();await page.goto(base);await page.locator('#authUnavailable').waitFor();assert.equal(await page.getByRole('button',{name:'Continue with email'}).isVisible(),false);assert.equal(await page.locator('#googleLogin').isVisible(),false);assert.equal(await page.locator('.email-auth-form:visible').count(),0);
  assert.equal(await page.locator('#email-setup-form').isVisible(),false);assert.deepEqual(errors,[]);
  console.log('PASS: first-owner setup, shared responsive fields, keyboard controls, incorrect-code retry, session reload, no secret storage and a truthful unavailable-login state until delivery or Google is configured.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close()});
