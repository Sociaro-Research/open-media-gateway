const {evidencePath} = require('./evidence.cjs');
// Shared model selector: real UI, isolated API fixtures, no paid generations.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const engine=process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium;
const root=path.resolve(__dirname,'../../web/public/ui'),output=process.env.UI_SCREENSHOT_DIR||evidencePath('/tmp/gateway-model-picker-')+engine.name();fs.mkdirSync(output,{recursive:true});
const server=http.createServer((req,res)=>{const url=new URL(req.url,'http://fixture').pathname,asset=url.match(/^\/provider-assets\/([a-z]+)\/icon\.(png|svg)$/),name=path.basename(url)||'index.html',file=asset?path.resolve(__dirname,'../../plugins/provider',asset[1],'icon.'+asset[2]):path.join(root,name);if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.html':'text/html'})[path.extname(file)]||'text/plain');res.end(fs.readFileSync(file));});
const model=(id,name,release,extra={})=>({id,display_name:name,public_id:'studio/'+id,media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5,pricing:{known:true,reference_price_usd:0},metadata:{release_date:release,description:'Published model details',...extra}});
const catalog=[{id:'mock',name:'Studio One',default_model:'old',models:[model('old','Cinema Alpha','2025-01-01',{version_date:'2026-09-01'}),model('boundary','Cinema Beta','2026-01-01'),model('new','Cinema Gamma','2026-01-02'),model('unknown','Cinema Unknown',''),model('month','Cinema Monthly','2026-02'),...['image','speech','music','audio'].map(type=>({...model(type,type+' Creator','2026-02-01'),media_type:type})),...Array.from({length:30},(_,i)=>model('extra-'+i,'Creative Video '+String(i).padStart(2,'0'),'2026-04-01'))]},{id:'other',name:'Studio Two',models:[model('new','Cinema Gamma','2026-03-10')]}];
catalog[0].icon_url='/provider-assets/sonilo/icon.png';
catalog[1].icon_url='/provider-assets/openai/icon.svg';catalog[1].icon_monochrome=true;
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await engine.launch({headless:true,...(engine===chromium?{ignoreDefaultArgs:['--hide-scrollbars']}:{}),executablePath:process.env.UI_BROWSER_PATH}),page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];
 page.on('pageerror',e=>{
  // WebKit also reports this browser scheduling notice during viewport
  // changes. Keep it visible separately from application exceptions.
  if(engine===webkit&&e.message==='ResizeObserver loop completed with undelivered notifications.'){console.warn('WebKit resize notice: '+e.message);return;}
  errors.push(e.message);
 });page.setDefaultTimeout(8000);
 await page.route('**/v1/**',route=>{const p=new URL(route.request().url()).pathname,json=x=>route.fulfill({json:x});
  if(p==='/v1/info')return json({auth_mode:'google',provider:'mock'});
  if(p==='/v1/me')return json({id:'admin',name:'Tester',session_id:'test',is_system_admin:true});
  if(p==='/v1/organizations')return json({data:[{id:'11111111-1111-4111-8111-111111111111',name:'Studio',role:'admin'}]});
  if(p==='/v1/api-keys'&&route.request().method()==='POST')return json({secret:'fixture-key'});
  if(p==='/v1/key')return json({key:{id:'fixture-key',organization_id:'11111111-1111-4111-8111-111111111111',resource_mode:'shared',scopes:['generation']},organization:{id:'11111111-1111-4111-8111-111111111111',role:'admin'},usage:{}});
  if(p==='/v1/providers')return json({data:catalog});
  if(p==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
  return json({data:[]});
 });
 const popup=page.locator('.model-select-popup'),search=()=>popup.getByRole('searchbox'),date=()=>popup.locator('input[type=date]'),names=()=>popup.getByRole('option').allTextContents();
 async function open(){await page.getByRole('combobox',{name:'Model',exact:true}).click();await search().waitFor();}
 try{
  await page.goto('http://127.0.0.1:'+server.address().port+'/');await page.locator('#createGenerationKey').click();await page.waitForFunction(()=>selectedModel()?.id==='old');await open();
  assert.deepEqual(await popup.locator('.picker-group').allTextContents(),['Studio One','Studio Two']);
  await popup.locator('.model-picker-provider-mark img').evaluateAll(images=>Promise.all(images.map(img=>img.decode())));
  assert.equal(await popup.locator('.model-picker-provider-mark img').first().evaluate(img=>img.naturalWidth),172,'original Sonilo asset is decoded');
  const heading=await popup.locator('.model-picker-provider').first().boundingBox(),mark=await popup.locator('.model-picker-provider-mark').first().boundingBox();
  assert.ok(heading.height>=64);assert.equal(mark.width,32);assert.equal(mark.height,32);
  const separation=await popup.evaluate(p=>{const headers=p.querySelectorAll('.model-picker-provider');return headers[1].getBoundingClientRect().top-headers[1].previousElementSibling.getBoundingClientRect().bottom});assert.ok(separation>=16,'provider groups have visible separation');
  assert.equal(await search().getAttribute('placeholder'),null);
  assert.equal(await search().locator('..').locator('.floating-label').textContent(),'Search by Name');
  await date().fill('2026-01-01');
  await popup.getByRole('button',{name:'Released after',exact:true}).click();
  await page.locator('.calendar-popup [data-date="2026-01-02"]').click();
  assert.equal(await date().inputValue(),'2026-01-02');assert.equal(await popup.count(),1,'calendar choice retains model popup');
  await popup.getByRole('button',{name:'Released after',exact:true}).click();await page.keyboard.press('Escape');
  assert.equal(await popup.count(),1,'Escape only closes nested calendar');assert.equal(await page.locator('.calendar-popup').count(),0);
  await popup.getByRole('button',{name:'Clear model filters'}).click();
  for(const type of ['Image','Speech','Music','Audio','Video']){
   await popup.getByRole('combobox',{name:'Content type',exact:true}).click();
   await page.getByRole('listbox',{name:'Content type',exact:true}).getByRole('option',{name:type,exact:true}).click();
   const matching=await popup.getByRole('option').allTextContents();assert.ok(matching.length>0);assert.ok(matching.every(name=>type==='Video'?/Cinema|Creative/.test(name):name.includes(type.toLowerCase())));
   assert.equal(await page.locator('#model').inputValue(),JSON.stringify(['mock','old']),'type filter must not select a model');
  }
  await search().fill('creator');assert.equal((await names()).length,0,'type and search combine');
  await popup.getByRole('button',{name:'Clear model filters'}).click();
  await search().fill('NeMa');assert.equal((await names()).length,6,'substring, case insensitive');assert.equal(await page.locator('#model').inputValue(),JSON.stringify(['mock','old']),'filter must not change selection');
  await date().fill('2026-01-01');assert.equal((await names()).length,3,'strict release cutoff excludes boundary, unknown and later version of old model');
  assert.ok((await names()).every(name=>/Gamma|Monthly/.test(name)));await search().fill('studio/new');assert.equal((await names()).length,2,'public model names searchable');
  await date().fill('2026-03-01');assert.deepEqual(await popup.locator('.picker-group').allTextContents(),['Studio Two'],'empty provider groups hidden');
  await search().fill('no-such-model');await popup.getByText('No models match these filters.').waitFor();
  await popup.getByRole('button',{name:'Clear model filters'}).click();assert.equal((await names()).length,40);
  await search().fill('Gamma');await page.keyboard.press('Escape');assert.equal(await popup.count(),0);await open();assert.equal(await search().inputValue(),'Gamma','filters persist per picker');
  const info=popup.getByRole('button',{name:'Information about Cinema Gamma'}).first();await info.hover();await page.locator('.model-info-popup').waitFor();assert.ok((await page.locator('.model-info-popup').innerText()).includes('Published model details'));await page.keyboard.press('Escape');assert.equal(await popup.count(),1);
  await search().focus();await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter');assert.deepEqual(await page.evaluate(()=>[selectedProvider().id,selectedModel().id]),['other','new'],'keyboard chooses the provider-specific model');
  for(const [width,height] of [[1440,900],[390,900],[320,900],[390,480]]){
   await page.setViewportSize({width,height});await open();if(await popup.getByRole('button',{name:'Clear model filters'}).isEnabled())await popup.getByRole('button',{name:'Clear model filters'}).click();
   await page.mouse.move(0,height-1);await page.evaluate(()=>ModelInfo.close());
   const rect=await popup.boundingBox();assert.ok(rect.x>=0&&rect.x+rect.width<=width&&rect.y>=0&&rect.y+rect.height<=height,'popup fits viewport');
   assert.ok(Math.abs(rect.height-Math.min(540,height-24))<1,'long model list fills the available popup height');
   const listRect=await popup.locator('.model-picker-options').boundingBox();assert.ok(listRect.height>=(width>600?380:height<540?170:250),'results retain enough space below the filters');
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=document.documentElement.clientWidth),'page fits even with visible scrollbars');
   const before=await search().boundingBox();await popup.locator('.model-picker-options').evaluate(n=>n.scrollTop=400);const after=await search().boundingBox();assert.equal(before.y,after.y,'filters remain visible while scrolling');
   assert.ok(await popup.locator('.model-picker-options').evaluate(n=>n.scrollTop>0),'results scroll');
   console.log(`${engine.name()} ${width}x${height}: popup ${rect.height}px, results ${listRect.height}px`);
   for(const theme of ['dark','light']){await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await page.screenshot({path:output+'/picker-'+width+'-'+height+'-'+theme+'.png'});assert.equal(await popup.locator('img.monochrome').evaluate(img=>getComputedStyle(img).filter),theme==='light'?'none':'invert(1)');}
   await popup.getByRole('button',{name:'Released after',exact:true}).click();
   const calendar=await page.locator('.calendar-popup').boundingBox();assert.ok(calendar.x>=0&&calendar.x+calendar.width<=width&&calendar.y>=0&&calendar.y+calendar.height<=height);
   await page.screenshot({path:output+'/calendar-'+width+'-'+height+'.png'});await page.keyboard.press('Escape');
   await popup.getByRole('combobox',{name:'Content type',exact:true}).click();await page.keyboard.press('Escape');assert.equal(await popup.count(),1);
   await page.keyboard.press('Escape');
  }
  // Mount the actual organization grants editor using the same model metadata.
  await page.evaluate(async catalog=>{document.querySelector('main').replaceChildren();const parent=document.querySelector('main');window.testGrants=[];const models=catalog.flatMap(p=>p.models.map((m,i)=>({id:p.id+'-'+i,provider:p.id,provider_name:p.name,provider_icon_url:p.icon_url,provider_icon_monochrome:p.icon_monochrome,model:m.id,name:m.display_name,public_id:m.public_id,enabled_by_default:false,capabilities:m})));await ModelAccess.organization(parent,async(_,options)=>options?(window.testGrants=JSON.parse(options.body).model_ids,{model_ids:window.testGrants,version:2}):({data:models,model_ids:[],version:1}),{organization:'test'});FormUI.refresh();},catalog);
  await page.getByRole('combobox',{name:'Model to enable'}).click();await search().fill('gamma');await date().fill('2026-03-01');assert.equal((await names()).length,1);await popup.getByRole('option').click();await page.getByRole('button',{name:'Add model',exact:true}).click();await page.getByRole('button',{name:'Save',exact:true}).click();assert.deepEqual(await page.evaluate(()=>window.testGrants),['other-0']);
  await page.getByRole('combobox',{name:'Model to enable'}).click();await popup.getByRole('button',{name:'Clear model filters'}).click();assert.equal(await popup.locator('img').first().getAttribute('src'),catalog[0].icon_url,'organization grants retain provider artwork');await page.keyboard.press('Escape');
  await page.evaluate(()=>{const select=document.querySelector('.organization-model-access select');ModelPicker.populate(select,[{id:'a',name:'Duplicate Studio',icon_url:'/provider-assets/sonilo/icon.png',models:[{id:'one'}]},{id:'b',name:'Duplicate Studio',icon_url:'/provider-assets/agnes/icon.png',models:[{id:'two'}]},{id:'c',name:'Very long provider connection name that must wrap on a small screen',icon_url:'/provider-assets/missing/icon.png',models:[{id:'three'}]}],{allModels:true});FormUI.refresh();});
  await page.setViewportSize({width:320,height:900});await page.getByRole('combobox',{name:'Model to enable'}).click();if(await popup.getByRole('button',{name:'Clear model filters'}).isEnabled())await popup.getByRole('button',{name:'Clear model filters'}).click();
  assert.equal(await popup.locator('.model-picker-provider').count(),3,'separate connections with equal names retain separate groups');
  await page.waitForFunction(()=>document.querySelectorAll('.model-picker-provider-mark')[2]?.children.length===0);
  assert.equal(await popup.locator('.model-picker-provider-mark').last().getAttribute('data-initial'),'V');assert.equal((await names()).length,6,'all-model choices are retained');
  await popup.locator('img').evaluateAll(images=>Promise.all(images.map(img=>img.decode())));assert.equal(await popup.locator('img').nth(1).evaluate(img=>img.naturalWidth),256,'original Agnes asset is decoded');
  assert.ok(await popup.locator('.model-picker-provider-name').last().evaluate(n=>n.getBoundingClientRect().right<=innerWidth));await page.screenshot({path:output+'/fallback-and-long-name.png'});await page.keyboard.press('Escape');
  // Exercise the actual markup editor independently of organization invitation fixtures.
  await page.setViewportSize({width:1440,height:900});
  await page.evaluate(async catalog=>{const parent=document.querySelector('main');parent.replaceChildren();window.testMarkup=null;await MarkupSettings.mount(parent,async(url,options)=>{if(url.endsWith('/catalog'))return {data:catalog};if(options){window.testMarkup=JSON.parse(options.body);return {system:{...window.testMarkup,version:2}}}return {system:{default_percent:20,exceptions:[],version:1}}});},catalog);
  await page.getByRole('button',{name:'Add exception',exact:true}).click();await page.getByRole('combobox',{name:'Model',exact:true}).click();
  await popup.locator('img').evaluateAll(images=>Promise.all(images.map(img=>img.decode())));assert.equal(await popup.locator('img').first().getAttribute('src'),catalog[0].icon_url,'markup exceptions retain provider artwork');
  assert.ok((await names()).some(name=>name.includes('All models')),'provider-wide markup choices remain available');
  await search().fill('Gamma');await date().fill('2026-03-01');await search().focus();await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter');
  await page.getByRole('spinbutton',{name:'Markup (%)',exact:true}).fill('15');await page.getByRole('button',{name:'Save',exact:true}).click();
  assert.deepEqual(await page.evaluate(()=>window.testMarkup),{default_percent:20,exceptions:[{provider:'other',model:'new',markup_percent:15}],version:1});
  assert.deepEqual(errors,[]);console.log('PASS: shared picker, source logos/fallback, separate provider groups, both themes, substring/public-name search, strict release dates, information, keyboard, persistence, responsive scroll, organization grants, markup exceptions.');
 }catch(e){await page.screenshot({path:output+'/failure.png'});throw e}finally{await browser.close();server.close()}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
