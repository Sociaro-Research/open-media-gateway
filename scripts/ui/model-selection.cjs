const {useFixtureKey}=require('./fixture-key.cjs');
const {evidencePath} = require('./evidence.cjs');
// Content-first model selection across providers. All API responses are fixtures.
const {chromium, webkit} = require('playwright');
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const ui = path.resolve(__dirname, '../../web/public/ui');
const output = process.env.UI_SCREENSHOT_DIR || evidencePath('/tmp/media-gateway-model-selection-ui');
fs.mkdirSync(output, {recursive:true});
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : path.basename(req.url.split('?')[0]);
  const file = path.join(ui,name);
  if (!fs.existsSync(file)) return res.writeHead(404).end();
  res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.png':'image/png','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser = await (process.env.UI_BROWSER_ENGINE === 'webkit' ? webkit.launch({headless:true}) : chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH ? {executablePath:process.env.UI_BROWSER_PATH} : {})}));
  const page = await browser.newPage({viewport:{width:1440,height:1100}});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const video={id:'shared-id',display_name:'Video A',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5,pricing:{known:true,reference_price_usd:0}};
  let catalog=[
    {id:'mock',name:'BytePlus',default_model:'shared-id',models:[video,{id:'image',display_name:'Image A',media_type:'image',resolutions:['1024*1024']}]},
    {id:'other',name:'Alibaba',models:[{...video,display_name:'Video B',resolutions:['1080p'],default_duration:8},{id:'speech',display_name:'Speech B',media_type:'speech',supports_voice:true,voices:['Serena']}]},
    {id:'empty',name:'Empty provider',models:[]}
  ];
  const estimates=[],posts=[];
  await page.route('**/v1/**',async route=>{
    const request=route.request(), url=new URL(request.url());
    const json=value=>route.fulfill({json:value});
    if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock'});
    if(url.pathname==='/v1/me')return json({id:'test',session_id:'test-session',name:'Reference test',email:'test@example.test'});
    if(url.pathname==='/v1/organizations')return json({data:[{id:'11111111-1111-4111-8111-111111111111',name:'Test organization',role:'admin',balance_usd:0,available_usd:0,storage_bytes:2500}]});
 if(url.pathname==='/v1/api-keys'&&request.method()==='POST')return json({secret:'fixture-key'});
    if(url.pathname==='/v1/key')return json({key:{id:'fixture-key',name:'Reference test',organization_id:'11111111-1111-4111-8111-111111111111',resource_mode:'shared',scopes:['generation']},usage:{consumed_usd:0},organization:{id:'11111111-1111-4111-8111-111111111111',name:'Test organization',role:'admin',balance_usd:10,credit_limit_usd:0}});
    if(url.pathname==='/v1/providers')return json({data:catalog});
    if((url.pathname==='/v1/generations' && route.request().method()==='GET')||url.pathname==='/v1/references')return json({data:[]});
    if(url.pathname==='/v1/generations/estimate'){estimates.push(request.postDataJSON());return json({cost:{expected_usd:0},can_generate:true});}
    if(url.pathname==='/v1/generations'){posts.push(request.postDataJSON());return json({...posts.at(-1),provider:posts.at(-1).model.split('/')[0],media_type:'video',id:'task-1',status:'queued',created_at:Math.floor(Date.now()/1000)});}
    return json({});
  });
  try {
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    assert.equal(await page.locator('#generationFields').isVisible(),false);
    assert.equal(await page.locator('#provider').count(),0);
    await useFixtureKey(page,'fixture-key');
    await page.waitForFunction(()=>selectedModel()?.id==='shared-id');
    assert.equal(await page.locator('#mediaType').count(),0);
    assert.deepEqual(await page.locator('#model optgroup').evaluateAll(es=>es.map(e=>[e.label,e.children.length])),[['Alibaba',2],['BytePlus',2]]);
    const choose=async(label,option)=>{await page.getByRole('combobox',{name:label,exact:true}).click();await page.getByRole('option',{name:option}).click();};
    await page.getByRole('combobox',{name:'Model',exact:true}).click();
    assert.deepEqual(await page.locator('.picker-group').allTextContents(),['Alibaba','BytePlus']);
    await page.screenshot({path:path.join(output,'grouped-models-desktop.png')});
    await page.getByRole('option',{name:/Video B/}).click();
    assert.equal(await page.locator('#resolution').inputValue(),'1080p');
    assert.equal(await page.locator('#duration').inputValue(),'8');
    await page.locator('#prompt').fill('A free fixture generation');
    await page.waitForFunction(()=>document.querySelector('#priceValue').textContent==='Free');
    assert.equal(estimates.at(-1).provider,undefined);assert.equal(estimates.at(-1).model,'other/shared-id');
    await page.locator('#submitButton').click();
    assert.match(await page.locator('#confirmFields').innerText(),/Alibaba/);
    await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);
    assert.equal(posts[0].provider,undefined);assert.equal(posts[0].model,'other/shared-id');
    await choose('Model',/Image A/);
    assert.equal(await page.locator('#durationField').isVisible(),false);
    await choose('Model',/Speech B/);
    assert.equal(await page.locator('#voiceField').isVisible(),true);
    await page.evaluate(()=>reuseTask({provider:'other',model:'shared-id',media_type:'video',prompt:'Reuse',options:{resolution:'1080p',duration:7}}));
    assert.deepEqual(await page.evaluate(()=>[selectedProvider().id,selectedModel().id]),['other','shared-id']);
    assert.equal(await page.evaluate(()=>selectedModel().media_type),'video');
    assert.equal(await page.locator('#duration').inputValue(),'7');
    await choose('Model',/Video A/);
    assert.equal(await page.locator('#resolution').inputValue(),'720p');
    for(const width of [390,320]){
      await page.setViewportSize({width,height:900});
      await page.getByRole('combobox',{name:'Model',exact:true}).click();
      assert.deepEqual(await page.locator('.picker-group').allTextContents(),['Alibaba','BytePlus']);
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
      await page.screenshot({path:path.join(output,`grouped-models-${width}.png`)});
      await page.getByRole('option',{name:/Video B/}).click();
      assert.equal(await page.evaluate(()=>selectedProvider().id),'other');
    }
    catalog=[];await page.evaluate(()=>loadProviders());
    assert.equal(await page.locator('#model').isDisabled(),true);
    assert.equal(await page.locator('#generationSettings').isVisible(),false);
    assert.equal(await page.locator('#submitButton').isDisabled(),true);
    assert.equal(await page.locator('#priceCard').isVisible(),false);
    assert.deepEqual(errors.filter(e=>!e.includes('ResizeObserver loop completed')),[]);
    console.log('Grouped model selection passed: media types, provider groups, duplicate model IDs, capabilities, estimates, submission, reuse, narrow screens and empty catalog.');
  } finally {await browser.close();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
