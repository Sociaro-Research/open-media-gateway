const {evidencePath} = require('./evidence.cjs');
// Local fixture: exercises the real provider screen and model-access editor.
const {chromium}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const ui=path.resolve(__dirname,'../../web/public/ui'),output=evidencePath('/tmp/gateway-model-access-ui');fs.mkdirSync(output,{recursive:true});
const server=http.createServer((req,res)=>{const pathname=new URL(req.url,'http://fixture').pathname;const name=pathname.startsWith('/ui/')||pathname.endsWith('.svg')?path.basename(pathname):'index.html';const f=path.join(ui,name);if(!fs.existsSync(f))return res.writeHead(404).end();res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':name.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(f));});
const ids=['11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222'];
let models=[{id:ids[0],provider:'mock',model:'video',name:'Studio Video',public_id:'mock/video',enabled_by_default:true,version:1},{id:ids[1],provider:'mock',model:'image',name:'Studio Image',public_id:'studio-image',enabled_by_default:true,version:1}];
let credentials={api_key:'fixture-original',billing_access_key_id:'fixture-billing',billing_access_key_secret:'fixture-secret'},providerWrites=[];
const provider=()=>({id:'mock',name:'Mock',description:'',enabled:true,configured:true,status:'connected',fields:[{key:'api_key',label:'API key',secret:true},{key:'billing_access_key_id',label:'Billing Access Key ID',secret:true,group:'billing'},{key:'billing_access_key_secret',label:'Billing Secret',secret:true,group:'billing'}],values:{},hints:Object.fromEntries(Object.entries(credentials).filter(([,v])=>v).map(([k,v])=>[k,'••••'+v.slice(-4)])),models:models.map(m=>({id:m.model,public_id:m.public_id,display_name:m.name,media_type:m.model==='video'?'video':'image',resolutions:['720p'],ratios:['16:9'],default_duration:5,pricing:{known:true,reference_price_usd:0}})),model_count:2,media_counts:{video:1,image:1}});
let grants=[],version=1,writes=0,checkFails=false;
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/v1/**',async route=>{
  const req=route.request(),p=new URL(req.url()).pathname,json=x=>route.fulfill({json:x});
  if(p==='/v1/me')return json({id:'admin',is_system_admin:true,name:'Administrator'});
  if(p==='/v1/info')return json({provider:'mock',auth_mode:'google',console:{generation_ui:true,personal_providers:true,allowed_providers:['mock']}});
  if(p==='/v1/providers'||p==='/v1/admin/providers'||p==='/v1/system/providers')return json({data:[provider()]});
  if(p.endsWith('/providers/mock/check'))return checkFails?route.fulfill({status:503,json:{error:{message:'Fixture connection error'}}}):json(provider());
  if(p.endsWith('/providers/mock')){if(req.method()==='PUT'){const body=req.postDataJSON();providerWrites.push(body);for(const key of Object.keys(credentials))if(key in body)credentials[key]=body[key];}return json(provider());}
  if(p==='/v1/system/model-settings')return json({data:models});
  if(p.startsWith('/v1/system/model-settings/')){
   const body=req.postDataJSON(),model=models.find(m=>p.endsWith('/'+m.id));
   if(body.version!==model.version||models.some(m=>m.id!==model.id&&m.public_id===body.public_id))return route.fulfill({status:409,json:{error:{message:'Conflict'}}});
   Object.assign(model,body,{version:model.version+1});writes++;return json(model);
  }
  if(p.endsWith('/model-access')){if(req.method()==='PUT'){const b=req.postDataJSON();assert.equal(b.version,version);grants=b.model_ids;version++;return json({model_ids:grants,version})}return json({data:models,model_ids:grants,version})}
  if(p==='/v1/api-keys'&&req.method()==='POST')return json({secret:'fixture-key',name:'Fixture'});
  if(p==='/v1/key')return json({key:{name:'Fixture'},usage:{}});
  return json({data:[]});
 });
 try{
  await page.emulateMedia({colorScheme:'dark'});
  await page.goto(`http://127.0.0.1:${server.address().port}/profile`);
  await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();
  await page.locator('[data-provider-id="mock"]').click();
  const row=page.locator('.model-access-row[data-model="video"]');await row.getByLabel('Public API name').waitFor();
  const checkButton=page.locator('#checkProviderButton'),checkFeedback=page.locator('#providerCheckFeedback');
  for(const theme of ['light','dark']){
   await page.evaluate(t=>GatewayTheme.apply(t),theme);
   for(const width of [1440,390,320]){
    await page.setViewportSize({width,height:1100});await checkButton.scrollIntoViewIfNeeded();await checkButton.focus();await page.keyboard.press('Enter');
    await checkFeedback.getByText('Connection verified.',{exact:true}).waitFor();
    const buttonBox=await checkButton.boundingBox(),feedbackBox=await checkFeedback.boundingBox();
    assert.ok(feedbackBox.x>=buttonBox.x+buttonBox.width,'connection feedback appears right of its button');
    assert.equal(await checkFeedback.evaluate(e=>getComputedStyle(e).color),await page.evaluate(()=>{const probe=document.createElement('span');probe.style.color='var(--success)';document.body.append(probe);const color=getComputedStyle(probe).color;probe.remove();return color;}));
    assert.equal(await page.locator('#providerMessage').innerText(),'');
    await page.screenshot({path:output+'/connection-'+theme+'-'+width+'.png'});
   }
  }
  checkFails=true;await checkButton.click();await checkFeedback.getByText('Connection check failed: Fixture connection error',{exact:true}).waitFor();assert.equal(await checkFeedback.getAttribute('class'),'provider-check-feedback error');checkFails=false;
  await page.setViewportSize({width:1440,height:1100});
  const fields=page.locator('.provider-dynamic-fields');
  assert.equal(await fields.locator('small,.field-explanation').count(),0);
  assert.equal(await fields.getByText('Not set',{exact:true}).count(),0);
  assert.equal(await fields.locator('.floating-field').count(),3);
  assert.equal(await fields.getByLabel('Billing Access Key ID (optional)',{exact:true}).inputValue(),'••••ling');
  assert.equal(await fields.getByLabel('Billing Secret (optional)',{exact:true}).inputValue(),'••••cret');
  assert.equal(await fields.getByLabel('API key',{exact:true}).inputValue(),'••••inal');
  for(const theme of ['light','dark']){
   await page.evaluate(t=>GatewayTheme.apply(t),theme);
   for(const width of [1440,390,320]){
    await page.setViewportSize({width,height:1100});await fields.scrollIntoViewIfNeeded();
    await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'credential fields do not overflow');
    for(const field of await fields.locator('.floating-field').all()){
     const box=await field.boundingBox(),label=await field.locator('.floating-label').boundingBox();
     assert.ok(label.y>=box.y&&label.y+label.height<=box.y+box.height,'label stays inside field');
     assert.equal(Math.round(box.height),58);
    }
    await fields.screenshot({path:output+'/credentials-'+theme+'-'+width+'.png'});
   }
  }
  await page.setViewportSize({width:1440,height:1100});
  assert.equal(await page.getByRole('table',{name:'Provider model settings'}).count(),1);
  assert.equal(await row.evaluate(n=>n.tagName),'TR');
  const save=row.locator('button[type=submit]');
  assert.equal(await save.isVisible(),false);
  await row.hover();assert.equal(await save.isVisible(),false,'hover never reveals unchanged Save');
  const rowSize=await row.boundingBox();
  await row.getByLabel('Public API name').fill('draft');assert.equal(await save.isVisible(),true);
  await page.mouse.move(0,0);assert.equal(await save.isVisible(),true,'dirty Save remains visible without hover');
  await row.getByLabel('Public API name').fill('mock/video');assert.equal(await save.isVisible(),false,'reverting hides Save');
  assert.equal((await row.boundingBox()).height,rowSize.height,'Save does not move the row');
  assert.equal(await page.locator('.model-access-table th').last().innerText(),'');
  assert.equal(await row.locator('td').first().evaluate(n=>getComputedStyle(n).borderBottomWidth),'0px');
  assert.equal(await page.locator('.model-access-table th').first().evaluate(n=>getComputedStyle(n).backgroundColor),await page.evaluate(()=>{const probe=document.createElement('span');probe.style.backgroundColor='var(--table-header)';document.body.append(probe);const color=getComputedStyle(probe).backgroundColor;probe.remove();return color;}));
  await row.getByLabel('Public API name').fill('cinema-fast');await row.getByLabel('Available by default').uncheck();const beforeSave=await save.boundingBox();await row.getByRole('button',{name:'Save',exact:true}).click();
  await row.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(await save.boundingBox(),beforeSave,'Saved stays exactly in the Save position');assert.equal(await save.evaluate(n=>getComputedStyle(n).color),'rgb(121, 214, 160)');assert.equal(await row.locator('[role=status]').textContent(),'');await page.screenshot({path:output+'/saved-in-place.png'});await save.waitFor({state:'hidden',timeout:5000});assert.equal(models[0].public_id,'cinema-fast');assert.equal(models[0].enabled_by_default,false);
  await page.reload();await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();await row.getByLabel('Public API name').waitFor();assert.equal(await row.getByLabel('Public API name').inputValue(),'cinema-fast');assert.equal(await row.getByLabel('Available by default').isChecked(),false);
  await row.getByLabel('Public API name').fill('studio-image');await row.getByRole('button',{name:'Save',exact:true}).click();await row.getByRole('status').filter({hasText:'Name already in use'}).waitFor();assert.equal(writes,1);assert.equal(await save.isVisible(),true,'failed save retains the action');
  await row.getByLabel('Public API name').fill('cinema-fast');
  await page.getByRole('searchbox',{name:'Search provider models'}).fill('image');assert.equal(await row.isVisible(),false);await page.getByRole('searchbox',{name:'Search provider models'}).fill('');
  // The submission builder must use the configured public ID, not reconstruct it.
  const name=await page.evaluate(p=>{providerCatalog=[p];return generationRequest({provider:'mock',model:'video',media_type:'video',prompt:'x',options:{duration:5}}).model},provider());assert.equal(name,'cinema-fast');
  const providerSave=page.locator('#saveProviderButton');
  await page.locator('#provider-field-api_key').fill('fixture-replacement');await providerSave.scrollIntoViewIfNeeded();const providerBox=await providerSave.boundingBox();await providerSave.click();
  await page.locator('#saveProviderButton.save-confirmation').waitFor();assert.equal(await providerSave.innerText(),'Saved');assert.deepEqual(await providerSave.boundingBox(),providerBox);
  // Editing during Saved restores the original nested button contents immediately.
  await page.locator('#provider-field-api_key').fill('another-fixture-key');assert.equal(await providerSave.locator('span').first().innerText(),'Save');await providerSave.click();await page.locator('#saveProviderButton.save-confirmation').waitFor();
  assert.equal('billing_access_key_id' in providerWrites.at(-1),false,'unchanged masked billing key is omitted');
  assert.equal('billing_access_key_secret' in providerWrites.at(-1),false);
  await page.locator('#provider-field-api_key').fill('new-account');
  for(const key of ['billing_access_key_id','billing_access_key_secret']){await page.locator('#provider-field-'+key).focus();await page.keyboard.press('Backspace');}
  await providerSave.click();await page.locator('#saveProviderButton.save-confirmation').waitFor();
  assert.equal(providerWrites.at(-1).api_key,'new-account');assert.equal(providerWrites.at(-1).billing_access_key_id,'');assert.equal(providerWrites.at(-1).billing_access_key_secret,'');
  await page.reload();await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();await row.getByLabel('Public API name').waitFor();
  assert.equal(await page.locator('#provider-field-billing_access_key_id').inputValue(),'');assert.equal(await page.locator('#provider-field-billing_access_key_secret').inputValue(),'');
  for(const key of ['billing_access_key_id','billing_access_key_secret'])await page.locator('#provider-field-'+key).fill('fixture-restored');
  await providerSave.click();await page.locator('#saveProviderButton.save-confirmation').waitFor();
  for(const key of Object.keys(credentials))await page.locator('#provider-field-'+key).fill('');
  await providerSave.click();await page.locator('#saveProviderButton.save-confirmation').waitFor();
  for(const key of Object.keys(credentials))assert.equal(providerWrites.at(-1)[key],'','explicit deletion is sent');
  await page.reload();await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();await row.getByLabel('Public API name').waitFor();
  for(const key of Object.keys(credentials))assert.equal(await page.locator('#provider-field-'+key).inputValue(),'','deleted credential stays empty after reload');
  await providerSave.waitFor({state:'hidden',timeout:5000});
  await page.screenshot({path:output+'/provider-desktop.png',fullPage:true});
  await page.locator('.provider-panel').filter({has:page.locator('.model-access-table')}).screenshot({path:output+'/model-table-desktop.png'});
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:output+'/provider-mobile.png',fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'provider mobile overflow');
  await row.getByLabel('Public API name').fill('cinema-mobile');await save.scrollIntoViewIfNeeded();await save.focus();const mobileSaveBox=await save.boundingBox();await page.keyboard.press('Enter');
  await row.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(await save.boundingBox(),mobileSaveBox,'keyboard save keeps mobile geometry');await page.screenshot({path:output+'/saved-mobile.png'});await save.waitFor({state:'hidden',timeout:5000});
  await row.getByLabel('Public API name').fill('cinema-fast');await save.click();await row.getByRole('button',{name:'Saved',exact:true}).waitFor();
  await page.locator('.model-access-scroll').evaluate(n=>n.scrollLeft=n.scrollWidth);
  await page.locator('.provider-panel').filter({has:page.locator('.model-access-table')}).screenshot({path:output+'/model-table-mobile.png'});
  await page.evaluate(async()=>{document.querySelector('#configurationView').hidden=true;const host=document.createElement('section');host.id='model-grant-test';host.style='padding:24px;max-width:960px;margin:auto';document.querySelector('main').append(host);await ModelAccess.organization(host,api,{organization:'33333333-3333-4333-8333-333333333333',current:()=>true})});
  const editor=page.locator('#model-grant-test');await page.evaluate(()=>FormUI.refresh());await editor.getByRole('combobox').click();await page.getByRole('option',{name:'cinema-fast',exact:true}).click();await editor.getByRole('button',{name:'Add model',exact:true}).click();await editor.getByRole('button',{name:'Save',exact:true}).click();await editor.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(grants,[ids[0]]);
  await page.screenshot({path:output+'/organization-mobile.png',fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'organization mobile overflow');
  await page.setViewportSize({width:1440,height:1100});await page.screenshot({path:output+'/organization-desktop.png',fullPage:true});
  await editor.getByRole('button',{name:'Remove',exact:true}).click();await editor.getByRole('button',{name:'Save',exact:true}).click();await editor.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.deepEqual(grants,[]);
  models=models.map(m=>({...m,name:'Shared model'}));
  await page.evaluate(async provider=>{const host=document.createElement('section');host.id='model-label-test';host.style='padding:24px';document.querySelector('main').append(host);await ModelAccess.provider(host,provider,api,{current:()=>true});},provider());
  const namedRows=page.locator('#model-label-test .model-access-row');
  const displayNames=await namedRows.locator('.model-access-identity strong').allTextContents();
  assert.equal(new Set(displayNames).size,2);assert.ok(displayNames.every(n=>n.startsWith('Shared model · ')));
  assert.deepEqual(await namedRows.evaluateAll(rows=>rows.map(row=>[row.dataset.model,row.querySelector('input[aria-label="Public API name"]').value])),models.map(m=>[m.model,m.public_id]));
  assert.deepEqual(errors,[]);console.log('PASS: real provider screen, rename, disable, reload, name conflict, search, public API name, organization grants/revocation, desktop and mobile.');
 }finally{await browser.close();server.close()}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
