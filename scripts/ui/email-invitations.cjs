const {evidencePath} = require('./evidence.cjs');
// Actual console scripts with a local mail/auth fixture. No real email or provider calls.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto'),assert=require('node:assert/strict');
const ui=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium';
const output=process.env.UI_SCREENSHOT_DIR||evidencePath(`/tmp/gateway-email-invitations-${engine}`);fs.mkdirSync(output,{recursive:true});
const records=new Map(),tokens=new Map(),grants=new Map(),invitations=new Map(),memberships=new Map(),requests=[];
const random=()=>crypto.randomBytes(32).toString('base64url');
function invite(state='active'){
 const token=random(),id=crypto.randomUUID();invitations.set(token,{organization_id:id,organization_name:`Invitation team ${invitations.size+1}`,role:'member',state});return token;
}
function org(i){return {id:i.organization_id,name:i.organization_name,role:'member',balance_usd:0,storage_bytes:0};}
const server=http.createServer(async(req,res)=>{
 try{
 const p=new URL(req.url,'http://fixture.test').pathname;
 const cookies=Object.fromEntries((req.headers.cookie||'').split(';').map(c=>c.trim().split('=')));
 const account=records.get(cookies.fixture_session);
 const json=(value,status=200,headers={})=>{res.writeHead(status,{'Content-Type':'application/json',...headers});res.end(JSON.stringify(value));};
 const failure=(message,status=400)=>json({error:{message}},status);
 if(req.method==='POST'){
  const chunks=[];for await(const c of req)chunks.push(c);const data=JSON.parse(Buffer.concat(chunks).toString()||'{}');requests.push({p,data});
  if(p==='/auth/email/register'){
   const record={email:data.email,invitation:data.invitation_token,verified:false,token:random()};records.set(data.email,record);tokens.set(record.token,record);return json({message:'Check your email.'},202);
  }
  if(p==='/auth/email/verify'){
   const record=tokens.get(data.token);if(!record)return failure('Invalid confirmation');tokens.delete(data.token);const grant=random();grants.set(grant,record);
   return json({email:record.email,invitation_token:record.invitation},200,{'Set-Cookie':`fixture_grant=${grant}; Path=/auth/email; HttpOnly; SameSite=Strict`});
  }
  if(p==='/auth/email/registration'){
   const record=grants.get(cookies.fixture_grant);return json(record?{pending:true,email:record.email,invitation_token:record.invitation}:{pending:false});
  }
  if(p==='/auth/email/complete'){
   assert.deepEqual(Object.keys(data),['password']);const record=grants.get(cookies.fixture_grant);if(!record)return failure('Missing registration');record.verified=true;grants.delete(cookies.fixture_grant);
   return json({email:record.email,invitation_token:record.invitation,message:'Your account is ready.'},200,{'Set-Cookie':[`fixture_session=${record.email}; Path=/; HttpOnly; SameSite=Lax`,'fixture_grant=; Path=/auth/email; Max-Age=0']});
  }
  if(p==='/auth/email/login'){
   const record=records.get(data.email);if(!record?.verified)return failure('Unknown account',401);
   return json({message:'Signed in.'},200,{'Set-Cookie':`fixture_session=${record.email}; Path=/; HttpOnly; SameSite=Lax`});
  }
  if(p.startsWith('/v1/invitations/')){
   if(!account?.verified)return failure('Sign in',401);const invitation=invitations.get(data.token);if(!invitation||invitation.state!=='active')return failure('This invitation is no longer available. Ask a manager for a new invitation.',409);
   const joined=memberships.get(account.email)||new Set();
   if(p.endsWith('/accept')){joined.add(invitation.organization_id);memberships.set(account.email,joined);}
   return json({...invitation,already_member:joined.has(invitation.organization_id)});
  }
  return json({data:[]});
 }
 if(p==='/auth/methods')return json({email:true,email_delivery:true,email_confirmation:true,google:false});
 if(p==='/v1/info')return json({auth_mode:'google',console:{generation_ui:true,personal_providers:true}});
 if(p==='/v1/me')return account?.verified?json({id:'11111111-1111-4111-8111-111111111111',name:'Invitation tester',email:account.email,session_id:account.email}):failure('Sign in',401);
 if(p==='/v1/organizations')return json({data:[...invitations.values()].filter(i=>memberships.get(account?.email)?.has(i.organization_id)).map(org)});
 if(p.startsWith('/v1/organizations/')){const i=[...invitations.values()].find(i=>i.organization_id===p.split('/')[3]);return json(i?org(i):{data:[]});}
 if(p.startsWith('/v1/'))return json({data:[]});
 const name=path.basename(p),file=path.join(ui,name.includes('.')?name:'index.html');if(!fs.existsSync(file)){res.writeHead(404);return res.end();}
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
 }catch(error){console.error(error);res.writeHead(500);res.end();}
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await(engine==='webkit'?webkit:chromium).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const base=`http://127.0.0.1:${server.address().port}`,contexts=[],errors=[];
 async function fresh(){const context=await browser.newContext();contexts.push(context);const page=await context.newPage();page.setDefaultTimeout(10000);page.on('pageerror',e=>{if(!e.message.includes('ResizeObserver'))errors.push(e.message);});return page;}
 const form=page=>page.locator('.email-auth-form:not([hidden])');
 const pending=page=>page.evaluate(()=>sessionStorage.getItem('gateway-pending-invite'));
 const count=p=>requests.filter(r=>r.p===p).length;
 async function requestSignup(page,email,token){
  await page.goto(base+(token?'/organizations#invite='+token:'/'));await page.locator('#emailLogin').click();await page.locator('#authSwitch').click();
  await form(page).locator('[name=email]').fill(email);await form(page).locator('[type=submit]').click();await form(page).getByText('Check your email.',{exact:true}).waitFor();
  const sent=requests.filter(r=>r.p==='/auth/email/register').at(-1);assert.equal(sent.data.invitation_token,token);assert.equal(sent.data.password,undefined);return records.get(email).token;
 }
 async function confirm(page,token){await page.goto(base+'/#email_verify='+token);await page.locator('#email-complete-form').waitFor();}
 async function complete(page){await form(page).locator('[name=password]').fill('A synthetic owner passphrase 123!');await form(page).locator('[type=submit]').click();await page.locator('#authGate').waitFor({state:'hidden'});}
 try{
  const token=invite(),first=await fresh(),email='new-owner@example.test';
  const emailed=await requestSignup(first,email,token);await first.close();
  // The confirmation browser shares neither sessionStorage nor cookies with signup.
  const confirmed=await fresh();await confirm(confirmed,emailed);assert.equal(await pending(confirmed),token);
  // A fresh tab or cleared storage can resume from the restricted server grant.
  await confirmed.evaluate(()=>sessionStorage.clear());await confirmed.reload();await confirmed.locator('#email-complete-form').waitFor();assert.equal(await pending(confirmed),token);
  assert.equal(count('/v1/invitations/accept'),0);await complete(confirmed);await confirmed.getByRole('button',{name:'Accept invitation',exact:true}).waitFor();
  assert.equal(count('/v1/invitations/accept'),0,'registration must not accept membership');assert.equal(memberships.get(email)?.size||0,0);
  for(const width of [1440,390,320]){await confirmed.setViewportSize({width,height:900});assert.ok(await confirmed.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await confirmed.screenshot({path:path.join(output,`pending-invitation-${width}.png`)});}
  await confirmed.getByRole('button',{name:'Accept invitation',exact:true}).focus();await confirmed.keyboard.press('Enter');await confirmed.waitForFunction(()=>!sessionStorage.getItem('gateway-pending-invite'));assert.equal(count('/v1/invitations/accept'),1);assert.equal(memberships.get(email).size,1);
  // Existing email accounts keep the ordinary direct-invitation flow.
  const secondToken=invite(),existing=await fresh();await existing.goto(base+'/organizations#invite='+secondToken);await existing.locator('#emailLogin').click();await form(existing).locator('[name=email]').fill(email);await form(existing).locator('[name=password]').fill('A synthetic owner passphrase 123!');await form(existing).locator('[type=submit]').click();await existing.getByRole('button',{name:'Accept invitation',exact:true}).waitFor();assert.equal(count('/v1/invitations/accept'),1);await existing.getByRole('button',{name:'Decline',exact:true}).click();assert.equal(await pending(existing),null);assert.equal(memberships.get(email).size,1);
  // Expiry/revocation can happen while registration is in progress.
  const unavailableToken=invite(),unavailable=await fresh(),unavailableEmail='expired-invite@example.test';const unavailableMail=await requestSignup(unavailable,unavailableEmail,unavailableToken);invitations.get(unavailableToken).state='revoked';const unavailableConfirmation=await fresh();await confirm(unavailableConfirmation,unavailableMail);await complete(unavailableConfirmation);await unavailableConfirmation.getByText('This invitation is no longer available. Ask a manager for a new invitation.',{exact:true}).waitFor();assert.equal(await unavailableConfirmation.getByRole('button',{name:'Accept invitation',exact:true}).count(),0);assert.equal(count('/v1/invitations/accept'),1);
  // Plain signup has no continuation and stays on the ordinary landing page.
  const ordinary=await fresh(),plainEmail='ordinary@example.test',plainToken=await requestSignup(ordinary,plainEmail);const plainConfirmation=await fresh();await confirm(plainConfirmation,plainToken);await complete(plainConfirmation);assert.equal(await pending(plainConfirmation),null);assert.equal(await plainConfirmation.getByRole('button',{name:'Accept invitation',exact:true}).count(),0);
  assert.deepEqual(errors,[]);console.log(`${engine}: invitation continuation passed across browser contexts, original-tab closure, grant resume after clearing tab storage, keyboard acceptance, existing-user login/decline, revoked invitation and ordinary signup; layouts 1440/390/320px. No real mail sent.`);
 }finally{for(const c of contexts)await c.close();await browser.close();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
