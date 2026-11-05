// Focused checks reuse the organization console fixture and real UI components.
const assert=require('node:assert/strict'),fs=require('node:fs');
const {evidencePath}=require('./evidence.cjs');
module.exports=async function(page,{writes,oid,account}){
 const baseline=process.env.UI_BASELINE==='1',measurements=[];
 const assignment=()=>page.locator('.organization-form').filter({has:page.getByRole('heading',{name:'Assign an existing user',exact:true})});
 const invite=()=>page.locator('.member-invitations form');
 const email=()=>assignment().getByLabel('Email or user ID',{exact:true});
 const role=()=>assignment().locator('select');
 async function geometry(){
  const nodes={field:await email().elementHandle(),picker:await role().count()?await role().locator('..').elementHandle():null,button:await invite().getByRole('button',{name:'Create invitation',exact:true}).elementHandle(),invitation:await invite().getByRole('combobox',{name:'Invitation role',exact:true}).elementHandle()};
  try{
   // Sequential boundingBox calls can straddle responsive layout/scroll events.
   // Read every peer in one browser task, keeping the row assertions strict.
   return await page.evaluate(nodes=>{
    const box=node=>{if(!node)return null;if(!node.isConnected)throw new Error('Layout control detached');const r=node.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,right:r.right,bottom:r.bottom}};
    return {field:box(nodes.field),picker:box(nodes.picker),button:box(nodes.button),invitation:box(nodes.invitation),viewport:{width:innerWidth,height:innerHeight,theme:document.documentElement.dataset.theme},scrollY};
   },nodes);
  }finally{for(const node of Object.values(nodes))if(node)await node.dispose();}
 }
 async function snapshot(name){
  for(const theme of ['dark','light']){
   await page.evaluate(theme=>GatewayTheme.apply(theme),theme);
   for(const width of [1440,390,320]){
    await page.setViewportSize({width,height:1080});
    await page.waitForFunction(width=>innerWidth===width&&innerHeight===1080&&document.documentElement.scrollWidth<=innerWidth,width);
    const sample=await geometry(),{field,picker,button,invitation,viewport}=sample;
    measurements.push({name,theme,width,...sample});
    fs.writeFileSync(evidencePath('/tmp/member-roles/geometry.json'),JSON.stringify(measurements,null,2));
    await page.screenshot({path:evidencePath('/tmp/member-roles/'+name+'-'+theme+'-'+width+'.png'),fullPage:true});
    assert.equal(viewport.width,width);assert.equal(viewport.height,1080);assert.equal(viewport.theme,theme);
    if(!baseline){
     assert.ok(picker&&field.width>=200&&picker.width>=200,'readable assignment fields');
     if(width===1440){assert.ok(Math.abs(field.y-picker.y)<1,'email and role share a row');assert.ok(Math.abs(invitation.y-button.y)<1,'invitation field and action share a row');}
     else {assert.ok(picker.y>=field.y+field.height+12,'assignment wraps in reading order');assert.ok(button.y>=invitation.y+invitation.height+12,'invitation wraps without squeezing');}
     if(width===1440){assert.ok(Math.abs(field.width-picker.width)<1,'equal assignment columns');assert.ok(Math.abs(picker.x-field.right-20)<1,'assignment column gap');assert.ok(Math.abs(button.x-invitation.right-12)<1,'invitation action gap');}
     else {assert.ok(Math.abs(picker.y-field.bottom-20)<1,'assignment row gap');assert.ok(Math.abs(button.y-invitation.bottom-12)<1,'invitation row gap');}
     assert.equal(invitation.height,58);assert.equal(button.height,58);
    }
   }
  }
  await page.setViewportSize({width:1440,height:1080});
 }
 await email().waitFor();await snapshot('members');
 if(baseline){console.log('PASS: baseline screenshots');return;}
 assert.equal(await role().inputValue(),'member');
 assert.deepEqual(await role().locator('option').evaluateAll(nodes=>nodes.map(n=>[n.value,n.textContent])),[['supermanager','Supermanager'],['admin','Manager'],['content_officer','Content Officer'],['developer','Developer'],['member','Member']]);
 assert.equal(await assignment().getByRole('button',{name:'Assign',exact:true}).isVisible(),false);
 // Enter/Space operate the shared picker; Escape returns focus without a write.
 const picker=assignment().getByRole('combobox',{name:'Role',exact:true});
 await picker.focus();await page.keyboard.press('Enter');await page.getByRole('option',{name:'Developer',exact:true}).click();
 assert.equal(await role().inputValue(),'developer');const before=writes.length;
 await picker.focus();await page.keyboard.press('Space');await page.keyboard.press('Escape');assert.equal(writes.length,before);assert.equal(await picker.evaluate(n=>n===document.activeElement),true);
 // Failure preserves email/role and allows an explicit retry; a pending write
 // cannot be submitted twice. Every role is serialized without an added limit.
 let fail=true,release,entered,requests=0;
 await page.route('**/v1/organizations/'+oid+'/members/*',async route=>{
  requests++;
  if(fail)return route.fulfill({status:403,json:{error:{message:'Role assignment denied for testing'}}});
  if(entered){entered();await new Promise(resolve=>release=resolve);}
  return route.fallback();
 });
 await email().fill(' existing@example.test ');await assignment().getByRole('button',{name:'Assign',exact:true}).click();
 await assignment().getByRole('status').filter({hasText:'Role assignment denied for testing'}).waitFor();
 assert.equal(await email().inputValue(),' existing@example.test ');assert.equal(await role().inputValue(),'developer');await snapshot('assignment-error');
 fail=false;
 for(const selected of ['developer','member','content_officer','admin','supermanager']){
  await role().selectOption(selected,{force:true});await email().fill(' existing@example.test ');
  const ready=new Promise(resolve=>entered=resolve),old=await assignment().elementHandle();
  const request=page.waitForRequest(r=>r.method()==='PUT'&&new URL(r.url()).pathname.includes('/members/'));
  await assignment().getByRole('button',{name:'Assign',exact:true}).click();await ready;
  const sent=await request;assert.equal(new URL(sent.url()).pathname,'/v1/organizations/'+oid+'/members/existing%40example.test');assert.deepEqual(sent.postDataJSON(),{role:selected});
  assert.equal(await assignment().getByRole('button',{name:'Assign',exact:true}).isDisabled(),true);
  const count=requests;await assignment().evaluate(f=>f.dispatchEvent(new Event('submit',{cancelable:true})));assert.equal(requests,count);
  release();entered=null;await page.waitForFunction(n=>!n.isConnected,old);await old.dispose();await email().waitFor();assert.equal(await role().inputValue(),'member');assert.equal(requests,count);
 }
 await page.unroute('**/v1/organizations/'+oid+'/members/*');
 await invite().locator('select').selectOption('content_officer',{force:true});
 await invite().getByRole('button',{name:'Create invitation',exact:true}).focus();
 await page.keyboard.press('Enter');await page.getByLabel('One-time invitation link',{exact:true}).waitFor();
 assert.deepEqual(writes.filter(w=>w.method==='POST'&&w.p.endsWith('/invitations')).at(-1).data,{role:'content_officer'});
 await snapshot('invitation-created');
 // Managers retain their two invitation roles and cannot directly assign users.
 for(const [actor,choices] of [['admin',['developer','member']],['supermanager',['supermanager','admin','content_officer','developer','member']]]){
  account(false,actor);
  await page.goto(new URL('/organizations',page.url()).href);await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());
  await page.getByRole('tab',{name:'Members',exact:true}).click();await invite().waitFor();
  assert.equal(await assignment().count(),0);assert.deepEqual(await invite().locator('select option').evaluateAll(nodes=>nodes.map(n=>n.value)),choices);
 }
 fs.writeFileSync(evidencePath('/tmp/member-roles/geometry.json'),JSON.stringify(measurements,null,2));
 console.log('PASS: selected roles, safe default, trimmed email, failure/retry, pending guard, invitations, restricted roles, keyboard, both themes and 1440/390/320 layouts');
};
