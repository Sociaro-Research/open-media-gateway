// TestProfileNameBrowserReconciliation: real session, API and PostgreSQL; localhost only.
const {chromium,webkit}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
(async()=>{
 const [base,token]=process.argv.slice(2);assert.equal(new URL(base).hostname,'127.0.0.1');
 const engine=process.env.UI_BROWSER_ENGINE||'chromium';
 const browser=await ({chromium,webkit}[engine]).launch({headless:true,...(engine==='chromium'&&process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 let release,held;const errors=[],writes=[];
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000}});
  await context.route('**/*',async route=>{
   if(new URL(route.request().url()).origin!==base)return route.abort();
   if(held&&route.request().method()==='PATCH'&&new URL(route.request().url()).pathname==='/v1/me'){
    const notify=held;held=null;
    const response=await route.fetch();
    await new Promise(resolve=>{release=resolve;notify(response.status());});
    return route.fulfill({response});
   }
   return route.continue();
  });
  await context.addCookies([{name:'media_gateway_session',value:token,url:base,httpOnly:true,sameSite:'Lax'}]);
  const page=await context.newPage();page.setDefaultTimeout(8000);
  page.on('pageerror',e=>errors.push(e.message));
  page.on('request',r=>{if(r.method()==='PATCH'&&new URL(r.url()).pathname==='/v1/me')writes.push(r.postDataJSON());});
  const input=()=>page.getByLabel('Display name',{exact:true});
  const form=()=>page.locator('#profileContent form').filter({has:input()});
  const save=()=>form().getByRole('button',{name:'Save',exact:true});
  const stored=async()=>{const r=await context.request.get(base+'/__test/name');assert.equal(r.status(),200);return r.json();};
  const header=()=>page.locator('#userName').innerText();
  const open=async()=>{await page.locator('#userMenuToggle').press('ArrowDown');await page.getByRole('menuitem',{name:'Profile',exact:true}).press('Enter');await input().waitFor();};
  const reopen=async()=>{await page.getByRole('button',{name:'Playground',exact:true}).click();await open();};
  async function capture(label){if(process.env.UI_SCREENSHOT_DIR){fs.mkdirSync(process.env.UI_SCREENSHOT_DIR,{recursive:true});await page.screenshot({path:path.join(process.env.UI_SCREENSHOT_DIR,engine+'-'+label+'.png'),fullPage:true});}}
  async function settle(name){await page.waitForFunction(name=>document.querySelector('#userName').textContent===name,name);assert.equal(await stored(),name);}
  async function persist(value){await input().fill(value);await save().press('Enter');await form().getByRole('button',{name:'Saved',exact:true}).waitFor();await settle(value.trim());}
  async function start(value,status=200){
   await input().fill(value);const received=new Promise(resolve=>held=resolve);await save().press('Enter');
   assert.equal(await received,status);return writes.length;
  }
  async function noDuplicate(count){
   // Native Enter submission while pending must not create another request.
   await input().press('Enter');await page.waitForTimeout(100);assert.equal(writes.length,count);
  }
  await page.goto(base+'/');await page.waitForFunction(()=>window.Workspace?.ready());await open();
  assert.equal(await input().inputValue(),'Alice');assert.equal(await stored(),'Alice');
  let count=await start('Bob');await noDuplicate(count);await reopen();
  console.log('BASELINE pending:',JSON.stringify({input:await input().inputValue(),header:await header(),sql:await stored(),saveVisible:await save().isVisible()}));
  assert.equal(await input().inputValue(),'Alice');assert.equal(await stored(),'Bob');
  release();await settle('Bob');await capture('untouched-response');
  console.log('MEASURE confirmed:',JSON.stringify({input:await input().inputValue(),header:await header(),sql:await stored(),saveVisible:await save().isVisible()}));
  assert.equal(await input().inputValue(),'Bob','untouched reopened form must adopt confirmed name');
  assert.equal(await save().isVisible(),false);
  for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});
   await persist('Alice');count=await start('  Bob  ');await reopen();await input().fill('Carol');
   assert.equal(await save().isVisible(),true);assert.equal(await save().isEnabled(),false,'reopened form locks the same pending field');await noDuplicate(count);
   release();await settle('Bob');assert.equal(await input().inputValue(),'Carol');assert.equal(await save().isEnabled(),true);assert.equal(await save().isVisible(),true);
   await capture('draft-'+width);await save().press('Enter');await form().getByRole('button',{name:'Saved',exact:true}).waitFor();await settle('Carol');
   // A deliberate edit back to the old cached value is still a draft after Bob commits.
   await persist('Alice');await start('Bob');await reopen();await input().fill('Temporary');await input().fill('Alice');
   release();await settle('Bob');assert.equal(await input().inputValue(),'Alice');assert.equal(await save().isVisible(),true);await save().press('Enter');await form().getByRole('button',{name:'Saved',exact:true}).waitFor();await settle('Alice');
   // Real server validation failure; preserve the reopened draft and retry it.
   await start('Rejected\x01',400);await reopen();await input().fill('Retry name');release();
   await form().getByRole('status').waitFor();assert.equal(await stored(),'Alice');assert.equal(await header(),'Alice');assert.equal(await input().inputValue(),'Retry name');
   assert.equal(await save().isVisible(),true);assert.equal(await save().isEnabled(),true);await save().press('Enter');await form().getByRole('button',{name:'Saved',exact:true}).waitFor();await settle('Retry name');
   // Same DOM drafts and normalized server baselines must also remain saveable.
   await start('  Normalized  ');await input().fill('Newer edit');release();await settle('Normalized');assert.equal(await input().inputValue(),'Newer edit');
   // Editing away and back after submission remains a draft even when the server trims it.
   count=await start('  Normalized again  ');await input().fill('Temporary');await input().fill('  Normalized again  ');await noDuplicate(count);
   release();await settle('Normalized again');assert.equal(await input().inputValue(),'  Normalized again  ','same-form edit-back preserves the raw draft');
   assert.equal(await save().isVisible(),true);assert.equal(await save().isEnabled(),true);await capture('same-form-edit-back-'+width);
   await input().fill('  Normalized  ');assert.equal(await save().isVisible(),true);await save().press('Enter');await form().getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(await input().inputValue(),'Normalized');
   await input().fill('Unsaved reload');await page.reload();await input().waitFor();assert.equal(await input().inputValue(),'Normalized');assert.equal(await stored(),'Normalized');assert.equal(await save().isVisible(),false);
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'overflow '+width);
  }
  count=await start('Retry after outage',500);await reopen();await input().fill('Retry after outage');await noDuplicate(count);release();
  await form().getByRole('status').filter({hasText:'Could not save profile'}).waitFor();
  assert.equal(await input().inputValue(),'Retry after outage');assert.equal(await stored(),'Normalized');assert.equal(await header(),'Normalized');
  await save().press('Enter');await form().getByRole('button',{name:'Saved',exact:true}).waitFor();await settle('Retry after outage');
  // Confirmation while Profile is closed must initialize the next opening correctly.
  await start('Closed profile');await page.getByRole('button',{name:'Playground',exact:true}).click();release();await settle('Closed profile');await open();assert.equal(await input().inputValue(),'Closed profile');
  await persist('Final name');await page.reload();await input().waitFor();assert.equal(await input().inputValue(),'Final name');assert.equal(await stored(),'Final name');
  assert.ok(writes.every(w=>Object.keys(w).length===1&&typeof w.name==='string'));assert.deepEqual(errors,[]);
  console.log('PASS: real name PATCH/SQL reconciliation, untouched/draft/reversion, failure/retry, duplicate prevention, normalization, reload, keyboard and 1440/390/320px; writes='+writes.length);
 }finally{release?.();await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
