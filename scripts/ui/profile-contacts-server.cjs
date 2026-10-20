// Invoked by TestProfileContactsBrowserWorkflow. Real API responses; localhost only.
const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
(async()=>{
 const [base,editorToken,viewerToken,company]=process.argv.slice(2);
 assert.equal(new URL(base).hostname,'127.0.0.1');
 const browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const errors=[],writes=[];let committed=0,holdNextWrite=false,releaseWrite,writeHeld;
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000}});
  await context.route('**/*',async route=>{
   const request=route.request();if(new URL(request.url()).origin!==base)return route.abort();
   // Delay dispatch of one actual PATCH to inspect pending controls; never mock its response.
   if(holdNextWrite&&new URL(request.url()).pathname==='/v1/me'&&request.method()==='PATCH'){
    holdNextWrite=false;await new Promise(resolve=>{releaseWrite=resolve;writeHeld();});
   }
   return route.continue();
  });
  await context.addCookies([{name:'media_gateway_session',value:editorToken,url:base,httpOnly:true,sameSite:'Lax'}]);
  const page=await context.newPage();page.setDefaultTimeout(8000);
  page.on('pageerror',e=>errors.push(e.message));
  page.on('request',r=>{if(new URL(r.url()).pathname==='/v1/me'&&r.method()==='PATCH')writes.push(r.postDataJSON());});
  const form=page.locator('.profile-contacts'),save=()=>form.getByRole('button',{name:'Save',exact:true}),add=()=>form.getByRole('button',{name:'Add contact',exact:true});
  const label=()=>page.getByLabel('Contact label',{exact:true}),link=()=>page.getByLabel('Contact link',{exact:true});
  const artifact=process.env.UI_SCREENSHOT_DIR;
  async function capture(name){if(artifact){fs.mkdirSync(artifact,{recursive:true});await page.screenshot({path:path.join(artifact,name+'.png'),fullPage:true});}}
  async function reload(){await page.reload();await form.waitFor();}
  async function persist(expected,hold=false,reloadAfter=true){
   const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/me'&&r.request().method()==='PATCH');
   let held;if(hold){holdNextWrite=true;held=new Promise(resolve=>writeHeld=resolve);}
   await save().click();
   if(hold){await held;try{assert.equal(await save().isEnabled(),false);assert.equal(await add().isEnabled(),false);assert.equal(await label().first().isEnabled(),false);assert.equal(await link().first().isEnabled(),false);await form.evaluate(f=>f.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})));assert.equal(writes.length,committed+1,'pending resubmission must not duplicate PATCH');}finally{releaseWrite();}}
   assert.equal((await response).status(),200);committed++;assert.equal(writes.length,committed);assert.deepEqual(writes.at(-1),{contacts:expected});
   await form.getByRole('button',{name:'Saved',exact:true}).waitFor();
   assert.deepEqual(await page.evaluate(async()=>{const r=await fetch('/v1/me');if(!r.ok)throw Error('profile read '+r.status);return (await r.json()).contacts;}),expected);
   if(!reloadAfter)return;
   await reload();assert.equal(await form.locator('tbody tr').count(),expected.length);
   for(let i=0;i<expected.length;i++){assert.equal(await label().nth(i).inputValue(),expected[i].label);assert.equal(await link().nth(i).inputValue(),expected[i].url);}
  }
  await page.goto(base+'/');await page.waitForFunction(()=>window.Workspace?.ready());await page.locator('#userMenuToggle').press('ArrowDown');await page.getByRole('menuitem',{name:'Profile',exact:true}).press('Enter');await form.waitFor();
  assert.equal(await save().isVisible(),false);
  await add().click();await label().fill('Email');await link().fill('editor@example.test');
  await capture('email-before-save');
  assert.equal(await save().isVisible(),true,'a plain email must offer Save');assert.equal(await save().isEnabled(),true);
  await persist([{label:'Email',url:'mailto:editor@example.test'}],true);
  // A second save in the same DOM must use the confirmed baseline after normalization.
  await link().fill('editor+tag@example.test');await persist([{label:'Email',url:'mailto:editor%2Btag@example.test'}],false,false);assert.equal(await link().inputValue(),'editor+tag@example.test');
  await label().fill('Edited email');await persist([{label:'Edited email',url:'mailto:editor%2Btag@example.test'}]);
  await label().fill('Email');await persist([{label:'Email',url:'mailto:editor%2Btag@example.test'}]);
  for(const [input,stored] of [['tel:+15551234567','tel:+15551234567'],['https://example.test/contact','https://example.test/contact'],['editor@example.test','mailto:editor@example.test']]){
   await link().fill(input);await persist([{label:'Email',url:stored}]);
  }
  await link().fill('editor@@example.test');assert.equal(await save().isVisible(),true);assert.equal(await save().isEnabled(),true);await save().click();await page.getByRole('alert').filter({hasText:'mailto:'}).waitFor();assert.equal(await link().inputValue(),'editor@@example.test');assert.equal(await link().getAttribute('aria-invalid'),'true');assert.equal(writes.length,committed);assert.equal(await link().evaluate(e=>e===document.activeElement),true);await link().fill('mailto:editor@example.test');
  await add().click();await label().nth(1).fill('Label only');assert.equal(await save().isVisible(),true);assert.equal(await save().isEnabled(),true);await save().click();await page.getByRole('alert').filter({hasText:'https:'}).waitFor();assert.equal(await link().nth(1).evaluate(e=>e===document.activeElement),true);assert.equal(await link().nth(1).inputValue(),'');assert.equal(await label().nth(1).inputValue(),'Label only');assert.equal(writes.length,committed);await form.getByRole('button',{name:'Remove contact',exact:true}).nth(1).click();assert.equal(await save().isVisible(),false);
  await label().fill('Email ');assert.equal(await save().isVisible(),true);assert.equal(await save().isEnabled(),true);await label().fill('Email');assert.equal(await save().isVisible(),false);
  // Editing back to the confirmed value cancels the draft without a write.
  await label().fill('Temporary');assert.equal(await save().isVisible(),true);assert.equal(await save().isEnabled(),true);await label().fill('Email');assert.equal(await save().isVisible(),false);assert.equal(writes.length,committed);
  await label().fill('Saved contact');await add().click();
  assert.equal(await save().isVisible(),true,'one unfinished row must not hide the whole form action');assert.equal(await save().isEnabled(),true);
  await save().click();await page.getByRole('alert').waitFor();assert.equal(writes.length,committed);
  assert.equal(await label().nth(1).evaluate(e=>e===document.activeElement),true);
  assert.equal(await label().first().inputValue(),'Saved contact');
  await form.getByRole('button',{name:'Remove contact',exact:true}).nth(1).click();
  await persist([{label:'Saved contact',url:'mailto:editor@example.test'}]);
  // Exercise input order, partial values, supported URI schemes and invalid feedback.
  for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});await link().fill('tel:+15551234567');await label().fill('   ');
   assert.equal(await save().isVisible(),true);assert.equal(await save().isEnabled(),true);await save().click();await page.getByRole('alert').waitFor();assert.equal(writes.length,committed);
   await label().fill('Phone');await link().fill('+15551234567');await save().click();await page.getByRole('alert').filter({hasText:'tel:'}).waitFor();assert.equal(await link().inputValue(),'+15551234567');assert.equal(writes.length,committed);
   await link().fill('javascript:alert(1)');await save().click();await page.getByRole('alert').filter({hasText:'https:'}).waitFor();assert.equal(await link().inputValue(),'javascript:alert(1)');assert.equal(await link().getAttribute('aria-invalid'),'true');assert.equal(writes.length,committed);await capture('invalid-feedback-'+width);
   await label().fill('Saved contact');await link().fill('mailto:editor@example.test');assert.equal(await save().isVisible(),false);
   await add().click();await link().nth(1).fill('https://example.test/contact');await label().nth(1).fill('Web');assert.equal(await save().isVisible(),true);assert.equal(await save().isEnabled(),true);
   await form.getByRole('button',{name:'Remove contact',exact:true}).nth(1).press('Enter');assert.equal(await save().isVisible(),false);assert.equal(writes.length,committed);
   for(const theme of ['dark','light']){await page.evaluate(t=>GatewayTheme.apply(t),theme);await label().fill('Draft');await capture('valid-draft-'+theme+'-'+width);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'page overflow');await label().fill('Saved contact');}
  }
  // Reload discards an explicitly unsaved edit; the confirmed SQL value survives.
  await label().fill('Unsaved edit');await reload();assert.equal(await label().inputValue(),'Saved contact');assert.equal(writes.length,committed);
  const viewer=await browser.newContext();await viewer.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
  await viewer.addCookies([{name:'media_gateway_session',value:viewerToken,url:base,httpOnly:true,sameSite:'Lax'}]);
  const other=await viewer.newPage();other.setDefaultTimeout(8000);other.on('pageerror',e=>errors.push(e.message));
  await other.goto(base+'/organizations');await other.getByRole('tab',{name:'Members',exact:true}).click();
  const published=other.locator('.member-contact').filter({hasText:'Contact editor'}).getByRole('link',{name:'Saved contact',exact:true});
  await published.waitFor();assert.equal(await published.getAttribute('href'),'mailto:editor@example.test');
  const members=await other.evaluate(async company=>{const r=await fetch('/v1/organizations/'+company+'/members');if(!r.ok)throw Error('member read '+r.status);return (await r.json()).data;},company);
  assert.deepEqual(members.find(m=>m.name==='Contact editor').contacts,[{label:'Saved contact',url:'mailto:editor@example.test'}]);
  assert.deepEqual(errors,[]);
  console.log('PASS: real UI → PATCH → PostgreSQL → reload → authorized member UI; invalid/mixed rows, correction, revert, unsaved reload, keyboard, themes and 1440/390/320px');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
