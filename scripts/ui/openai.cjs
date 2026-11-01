const {useFixtureKey}=require('./fixture-key.cjs');
const {evidencePath} = require('./evidence.cjs');
// OpenAI capability UI and uploaded-PNG contract. All API calls use local fixtures.
const {chromium,webkit}=require('playwright'),browserType=process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium;
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const logo=require('./provider-logo-fixtures.cjs').valid;
const server=http.createServer((req,res)=>{
 if(req.url==='/provider-assets/fixture/openai/icon.png'){res.setHeader('Content-Type','image/png');return res.end(logo);}
 const name=path.basename(req.url.split('?')[0]);const file=path.join(root,name.includes('.')?name:'index.html');
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
const image={parameters:[{key:'style_strength',label:'Style strength',type:'number',min:0,max:1,default:0.5}],id:'gpt-image-2.5-flare',display_name:'GPT Image 2.5 Flare',media_type:'image',resolutions:['1024x1024','1536x1024'],qualities:['medium','low','high','xhigh','max'],backgrounds:['opaque','transparent','auto'],formats:['png','jpeg','webp'],supports_image_input:true,max_image_inputs:16,no_seed:true,no_watermark:true,pricing:{known:true,reference_price_usd:0.01317}};
const speech={id:'gpt-4o-mini-tts',display_name:'GPT-4o mini TTS',media_type:'speech',no_seed:true,no_watermark:true,no_language:true,supports_voice:true,requires_voice:true,voices:['alloy','coral'],formats:['mp3','wav'],pricing:{known:true}};
const legacy={...speech,id:'tts-1',display_name:'TTS-1',no_instructions:true};
const provider={fields:[{key:'api_key',label:'API key',secret:true,required:true},{key:'base_url',label:'API endpoint'}],values:{base_url:'https://api.openai.com/v1'},hints:{api_key:'••••test'},icon_url:'/provider-assets/fixture/openai/icon.png',icon_monochrome:true,id:'openai',name:'OpenAI',live:true,configured:true,credentials_configured:true,enabled:true,status:'connected',status_message:'Key verified.',default_model:image.id,models:[image,speech,legacy],model_count:3,media_counts:{image:1,speech:2},supports_billing_credentials:false,base_url:'https://api.openai.com/v1',region:'api.openai.com',credential_source:'console',credential_hint:'••••test'};
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await browserType.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[],estimates=[];
 page.on('pageerror',e=>{errors.push(e.message);console.error(e.message);});
 await page.route('**/v1/**',async route=>{
  const p=new URL(route.request().url()).pathname,json=body=>route.fulfill({json:body});
  if(p==='/v1/info')return json({provider:'openai',auth_mode:'google',console:{generation_ui:true,personal_providers:true,allowed_providers:['openai','mock','alibaba','bytedance']}});
  if(p==='/v1/me')return json({id:'fixture-user',name:'Test developer',email:'test@example.test',is_system_admin:true});
  if(p==='/v1/api-keys'&&route.request().method()==='POST')return json({name:'OpenAI fixture',secret:'fixture-generation-key'});
  if(p==='/v1/api-keys'||p==='/v1/organizations'||(p==='/v1/generations' && route.request().method()==='GET')||p==='/v1/references')return json({data:[]});
  if(p==='/v1/key')return json({key:{name:'OpenAI fixture'},usage:{remaining_usd:null}});
  if(p==='/v1/providers'||p==='/v1/admin/providers')return json({data:[provider]});
  if(p==='/v1/admin/providers/openai')return json(provider);
  if(p==='/v1/generations/estimate'){estimates.push(route.request().postDataJSON());return json({cost:{expected_usd:0.02},can_generate:true});}
  return json({});
 });
 try{
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  await useFixtureKey(page,'fixture-generation-key');
  await page.waitForFunction(()=>selectedModel()?.id==='gpt-image-2.5-flare');
  await page.locator('#prompt').fill('A painted lake.');
  await page.locator('.advanced-settings summary').click();
  await page.locator('#quality').selectOption('high');
  await page.locator('#background').selectOption('transparent');
  await page.waitForFunction(()=>document.querySelector('#priceValue').textContent.includes('0.02'));
  assert.equal(await page.locator('#seed').isVisible(),false);
  assert.equal(await page.locator('#watermarkOption').isVisible(),false);
  const submitted=await page.evaluate(()=>requestOptions(selectedModel()));
  assert.equal(submitted.parameters.style_strength,0.5);assert.equal(submitted.quality,'high');assert.equal(submitted.background,'transparent');assert.equal(submitted.format,'png');
  fs.mkdirSync(evidencePath('/tmp/gateway-openai-ui'),{recursive:true});
  await page.screenshot({path:evidencePath('/tmp/gateway-openai-ui/image.png'),fullPage:true});

  await page.locator('#model').selectOption(JSON.stringify(['openai','gpt-4o-mini-tts']));
  assert.equal(await page.locator('#qualityField').isVisible(),false);
  assert.equal(await page.locator('#languageField').isVisible(),false);
  assert.equal(await page.locator('#instructionsField').isVisible(),true);
  await page.locator('#instructions').fill('Whisper');
  const speechBody=await page.evaluate(()=>generationRequest(estimateRequest()));
  assert.equal(speechBody.model,'openai/gpt-4o-mini-tts');
  assert.equal(speechBody.instructions,'Whisper');
  assert.ok(typeof speechBody.input==='string' && speechBody.voice);
  assert.equal(speechBody.prompt,undefined);assert.equal(speechBody.provider,undefined);
  assert.equal(speechBody.options.voice,undefined);assert.equal(speechBody.options.instructions,undefined);

  await page.locator('#model').selectOption(JSON.stringify(['openai','tts-1']));
  assert.equal(await page.locator('#instructionsField').isVisible(),false);
  const tts=await page.evaluate(()=>requestOptions(selectedModel()));
  assert.ok(!('parameters' in tts));assert.ok(!('instructions' in tts));assert.ok(!('quality' in tts));assert.ok(!('background' in tts));assert.ok(!('language' in tts));
  await page.goto(`http://127.0.0.1:${server.address().port}/profile`);
  await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();
  await page.locator('[data-provider-id="openai"]').click();
  assert.equal(await page.locator('[data-provider-field=api_key]').count(),1);assert.equal(await page.locator('[data-provider-field=billing_access_key_id]').count(),0);
  const key=page.locator('[data-provider-field=api_key]');
  assert.equal(await key.getAttribute('type'),'text');assert.equal(await key.getAttribute('autocomplete'),'off');assert.equal(await key.inputValue(),provider.hints.api_key);
  await key.fill('replacement-key');
  assert.equal(await page.evaluate(()=>FormUI.credentials.replacement(document.querySelector('[data-provider-field=api_key]'))),'replacement-key');
  const img=page.locator('[data-provider-id="openai"] .provider-list-mark img');
  assert.equal(await img.getAttribute('data-logo-url'),'/provider-assets/fixture/openai/icon.png');
  await img.evaluate(i=>i.decode());
  const dims=await img.boundingBox();assert.equal(dims.width,38);assert.equal(dims.height,38);assert.equal(await page.locator('[data-provider-id="openai"] strong').innerText(),'OpenAI');
  await page.screenshot({path:evidencePath('/tmp/gateway-openai-ui/connection.png'),fullPage:true});
  await page.setViewportSize({width:390,height:844});
  await page.screenshot({path:evidencePath('/tmp/gateway-openai-ui/mobile.png'),fullPage:true});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'mobile overflow');
  assert.deepEqual(errors,[]);
  console.log('PASS: OpenAI image controls, TTS capabilities, credential form, uploaded transparent logo, desktop and mobile.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
