// Synthetic localhost fixture: no provider requests, real keys or paid generations.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {evidencePath}=require('./evidence.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',browserType=engine==='webkit'?webkit:chromium;
const out=evidencePath('/tmp/gateway-kling-connection/'+engine);fs.mkdirSync(out,{recursive:true});
const root=path.resolve(__dirname,'../../web/public/ui');
const fields=[{key:'api_key',label:'API key (current API)',secret:true,required:true},{key:'base_url',label:'API endpoint'},...['720p','1080p'].map(res=>({key:`kling4_${res}_price`,label:`4.0 ${res} override (USD/s, optional)`}))];
const models=['kling-3.0-text-to-video','kling-3.0-image-to-video','kling-4.0-text-to-video','kling-4.0-image-to-video'].map(id=>({id,display_name:id,media_type:'video',pricing:{known:true,reference_price_usd:id.includes('4.0')?2.35:0.42}}));
let checks=0,denied=false,holdCheck=false,releaseCheck;
const provider={id:'kling',api_type:'kling',name:'Kling AI',version:1,enabled:true,configured:true,credential_source:'console',fields,hints:{api_key:'••••fixture'},values:{base_url:'https://api-singapore.klingai.com',kling4_720p_price:'',kling4_1080p_price:''},connection:{manual_catalog:false,models:[],headers:[],endpoints:{}},region:'api-singapore.klingai.com',models,model_count:4,media_counts:{video:4},status:'unverified',status_message:'Public catalog loaded. API key and model access are not verified.'};
const server=http.createServer((req,res)=>{const name=req.url.startsWith('/ui/')?path.basename(req.url.split('?')[0]):'index.html';const file=path.join(root,name);if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await browserType.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[];
 page.on('pageerror',e=>{if(engine==='webkit'&&e.message==='ResizeObserver loop completed with undelivered notifications.')return;errors.push(e.message);});
 await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname;
 assert.equal(url.hostname,'127.0.0.1','fixture must not contact external services');
 if(!p.startsWith('/v1/'))return route.continue();
 const json=x=>route.fulfill({json:x});
 if(p==='/v1/me')return json({id:'admin',is_system_admin:true,name:'Admin'});
 if(p==='/v1/info')return json({provider:'mock',auth_mode:'google',console:{generation_ui:true,personal_providers:true}});
 if(p==='/v1/organizations')return json({data:[]});
 if(p==='/v1/playground-key/restore')return route.fulfill({status:404,json:{error:{code:'not_found',message:'No synthetic saved key'}}});
 if(p.endsWith('/providers/types'))return json({data:[{id:'kling',name:'Kling AI',fields,profiles:[]}]});
 if(p.endsWith('/providers/kling/check')){
 assert.equal(req.method(),'POST');checks++;
 if(holdCheck){const stale={...provider,status:'authenticated'};await new Promise(resolve=>releaseCheck=resolve);return json(stale);}
 provider.status=denied?'error':'authenticated';
 provider.status_message=denied?'Kling account permission denied; authentication and model access are unverified.':'Account query authenticated. Model access, available funds and generation output are not verified. Resource-package details were absent or malformed; package availability and balances remain unverified.';
 provider.last_checked_at='2026-10-29T16:00:00Z';return json(provider);
 }
 assert.equal(req.method(),'GET',`only explicit check may write: ${p}`);
 if(p.endsWith('/providers/kling'))return json(provider);
 if(p==='/v1/admin/providers'||p==='/v1/system/providers')return json({data:[provider]});
 return json({data:[]});
 });
 try{
 await page.goto(`http://127.0.0.1:${server.address().port}/profile`);
 await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());
 await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();
 await page.locator('#adminProviderStatus').waitFor({state:'visible'});
 assert.equal(await page.locator('#adminProviderStatus').textContent(),'Connected, Unverified');assert.equal(checks,0);
 for(const resolution of ['720p','1080p'])assert.equal(await page.locator(`[data-provider-field="kling4_${resolution}_price"]`).inputValue(),'');
 const check=page.locator('#checkProviderButton');await check.focus();await page.keyboard.press('Enter');
 await page.waitForFunction(()=>document.querySelector('#adminProviderStatus').textContent==='Connected, Verified');assert.equal(checks,1);
 assert.match(await page.locator('#providerCheckFeedback').getAttribute('class'),/success/);
 assert.match(await page.locator('#providerCheckFeedback').textContent(),/Model access, available funds and generation output are not verified/);
 assert.match(await page.locator('#providerCheckFeedback').textContent(),/Resource-package details were absent or malformed/);
 for(const theme of ['dark','light'])for(const width of [1440,390,320]){
 await page.setViewportSize({width,height:1100});await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`${theme}/${width} page overflow`);
 await page.screenshot({path:path.join(out,`${theme}-${width}.png`),fullPage:true});
 }
 await page.reload();await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());
 await page.locator('#profileContent .organization-tabs').getByRole('tab',{name:'Providers',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('#adminProviderStatus').textContent==='Connected, Verified');assert.equal(checks,1,'reads never recheck key');
 assert.match(await page.locator('#adminConnectionState').textContent(),/Resource-package details were absent or malformed/);
 denied=true;await page.locator('#checkProviderButton').click();await page.waitForFunction(()=>document.querySelector('#adminProviderStatus').textContent==='Connected, Unverified');
 assert.match(await page.locator('#providerCheckFeedback').getAttribute('class'),/error/);assert.equal(checks,2);
 // Shared renderer covers both personal and shared provider surfaces.
 const labels=await page.evaluate(()=>[
  [{enabled:true,configured:true,status:'connected'},'Connected, Unverified'],
  [{enabled:true,configured:true,status:'error'},'Connected, Unverified'],
  [{enabled:true,configured:true,status:'authenticated'},'Connected, Verified'],
  [{enabled:false,configured:true,status:'authenticated'},'Disabled, Verified'],
  [{enabled:false,configured:true,status:'error'},'Disabled, Unverified'],
  [{enabled:true,configured:false,status:'authenticated'},'Not configured, Unverified'],
  [{enabled:true,configured:true,archived:true,status:'authenticated'},'Archived, Verified']
 ].map(([p,want])=>[providerStatusLabel(p),want]));
 for(const [actual,want] of labels)assert.equal(actual,want);
 for(const transition of ['reload','scope']){
  holdCheck=true;releaseCheck=null;
  await page.locator('#checkProviderButton').click();
  await page.waitForFunction(()=>document.querySelector('#providerCheckFeedback').textContent==='Checking connection…');
  while(!releaseCheck)await new Promise(resolve=>setImmediate(resolve));
  await page.evaluate(async transition=>{if(transition==='scope')sharedConfiguration=!sharedConfiguration;await loadAdminProviders();},transition);
  releaseCheck();await page.waitForFunction(()=>!document.querySelector('#checkProviderButton').disabled);
  assert.equal(await page.locator('#adminProviderStatus').textContent(),'Connected, Unverified','late check must not restore verification');
 }
 assert.deepEqual(errors,[]);console.log(JSON.stringify({engine,checks,layouts:6,result:'passed',evidence:out}));
 }finally{await browser.close();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
