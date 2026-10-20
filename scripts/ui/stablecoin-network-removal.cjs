// Synthetic localhost settings only: no RPC, credentials or payment creation.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(process.env.UI_SOURCE_DIR||path.join(__dirname,'../../web/public/ui'));
const output=path.resolve(process.env.UI_SCREENSHOT_DIR||'/tmp/gateway-stablecoin-network-removal');
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
const id='66666666-6666-4666-8666-666666666666';
const wallets=['ethereum','bsc','polygon'].map((network,i)=>({network,address:'0x'+String(i+1).repeat(40),tokens:['USDT','USDC']}));
let providers=[],writes=[],failNext=false;
const server=http.createServer((req,res)=>{
 const name=path.basename(req.url.split('?')[0]);
 if(name==='fixture'){
  res.setHeader('Content-Type','text/html');
  return res.end(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><link rel="stylesheet" href="/docs.css"></head><body><main class="content" style="padding:24px;max-width:1100px;margin:auto"><div id="fixture"></div></main><script>window.fixtureAPI=async(p,o={})=>{const r=await fetch(p,{...o,headers:{'Content-Type':'application/json'}}),data=await r.json();if(!r.ok){const e=new Error(data.error.message);e.status=r.status;throw e}return data};window.VATSettingsUI={mount:async()=>{}};</script><script src="/control-pickers.js"></script><script src="/form-ui.js"></script><script src="/stablecoin-payments.js"></script><script src="/payments.js"></script></body></html>`);
 }
 if(!['styles.css','docs.css','control-pickers.js','form-ui.js','stablecoin-payments.js','payments.js'].includes(name))return res.writeHead(404).end();
 res.setHeader('Content-Type',name.endsWith('.css')?'text/css':'text/javascript');res.end(fs.readFileSync(path.join(root,name)));
});
(async()=>{
 fs.mkdirSync(output,{recursive:true});await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let browser;
 const results=[],errors=[],unexpected=[];
 try{
  browser=await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
  const page=await browser.newPage();page.setDefaultTimeout(10000);
  page.on('pageerror',e=>errors.push(e.message));
  const origin='http://127.0.0.1:'+server.address().port;
  await page.route('**/*',async route=>{
   const req=route.request(),url=new URL(req.url()),p=url.pathname;
   if(url.origin!==origin){unexpected.push(req.url());return route.abort()}
   if(p==='/v1/system/payment-providers'&&req.method()==='GET')return route.fulfill({json:{data:providers,available_providers:[{id:'stripe',name:'Stripe'},{id:'stablecoin',name:'Stablecoins'}]}});
   if(p==='/v1/system/payment-providers/'+id&&req.method()==='PUT'){
    const data=req.postDataJSON();writes.push(data);
    if(failNext){failNext=false;return route.fulfill({status:503,json:{error:{message:'Fixture save failed'}}})}
    assert.equal(data.version,providers[0].version);assert.equal(new Set(data.stablecoin.wallets.map(w=>w.network)).size,data.stablecoin.wallets.length);
    providers=[{...providers[0],...data,version:data.version+1}];return route.fulfill({json:providers[0]});
   }
   if(p.startsWith('/v1/')){unexpected.push(req.method()+' '+p);return route.abort()}
   return route.continue();
  });
  const reopen=async()=>page.evaluate(async()=>{document.querySelector('#fixture').replaceChildren();await PaymentProviderSettings.mount(document.querySelector('#fixture'),fixtureAPI,()=>true)});
  for(const theme of ['dark','light'])for(const width of [1440,390,320])for(const removeIndex of [0,1]){
   const name=theme+'-'+width+'-remove-'+removeIndex;
   if(process.env.UI_CASE_FILTER&&!name.includes(process.env.UI_CASE_FILTER))continue;
   const result={name,checks:[]};results.push(result);
   try{
    providers=[{id,kind:'stablecoin',name:'Synthetic wallets',enabled:false,live:true,fee_mode:'service',version:1,stablecoin:{wallets:structuredClone(wallets)}}];writes=[];failNext=false;
    await page.setViewportSize({width,height:1100});await page.goto(origin+'/fixture');await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await reopen();
    const card=page.locator('.stablecoin-provider-card'),rows=card.locator('tbody tr');
    // Use the shared picker with the keyboard to create the duplicate.
    await card.getByRole('combobox',{name:'Network',exact:true}).nth(1).press('Enter');await page.keyboard.press('Home');await page.keyboard.press('Enter');
    await card.getByRole('button',{name:'Save',exact:true}).press('Enter');await page.getByRole('alert').filter({hasText:'Choose each network only once.'}).waitFor();
    assert.equal(writes.length,0);result.checks.push('duplicate blocked without a write');await page.keyboard.press('Escape');
    // Removing an unrelated network must retain both duplicate errors.
    await rows.nth(2).getByRole('button',{name:'Remove',exact:true}).press('Enter');
    assert.deepEqual(await rows.locator('select[aria-label=Network]').evaluateAll(nodes=>nodes.map(n=>n.validity.customError)),[true,true]);
    const retained=await rows.nth(1-removeIndex).getByLabel('Receiving address',{exact:true}).inputValue();
    await rows.nth(removeIndex).getByRole('button',{name:'Remove',exact:true}).press('Enter');
    const validity=await card.locator('select[aria-label=Network]').evaluateAll(nodes=>nodes.map(n=>({value:n.value,valid:n.validity.valid,message:n.validationMessage})));
    result.afterRemoval=validity;
    if(validity.some(n=>!n.valid)){
     await card.getByRole('button',{name:'Save',exact:true}).press('Enter');await page.getByRole('alert').filter({hasText:'Choose each network only once.'}).waitFor();
     result.blockedWriteCount=writes.length;assert.equal(writes.length,0);
    }
    assert.ok(validity.every(n=>n.valid),'A unique remaining network must be valid after removing its duplicate: '+JSON.stringify(validity));
    result.checks.push('removal clears only resolved duplicate errors');
    assert.equal(await rows.getByLabel('Receiving address',{exact:true}).inputValue(),retained);
    assert.equal(await card.getByRole('button',{name:'Save',exact:true}).isVisible(),true);
    failNext=true;await card.getByRole('button',{name:'Save',exact:true}).press('Enter');await card.getByText('Fixture save failed',{exact:true}).waitFor();
    assert.equal(writes.length,1);assert.equal(await rows.getByLabel('Receiving address',{exact:true}).inputValue(),retained);assert.equal(providers[0].stablecoin.wallets.length,3);
    const attempt=structuredClone(writes[0]);await card.getByRole('button',{name:'Save',exact:true}).press('Enter');await card.getByRole('button',{name:'Saved',exact:true}).waitFor();
    assert.deepEqual(writes[1],attempt);assert.deepEqual(providers[0].stablecoin.wallets,[{network:'ethereum',address:retained,tokens:['USDT','USDC']}]);result.checks.push('failed save retains draft; identical retry persists remaining wallet');
    // A new edit during Saved feedback must restore Save, then survive reopening.
    await card.getByLabel('Display name',{exact:true}).fill('Re-edited '+name);await card.getByRole('button',{name:'Save',exact:true}).press('Enter');await card.getByRole('button',{name:'Saved',exact:true}).waitFor();
    assert.equal(writes.length,3);assert.equal(writes[2].version,2);await reopen();
    assert.equal(await card.getByLabel('Display name',{exact:true}).inputValue(),'Re-edited '+name);assert.equal(await rows.count(),1);assert.equal(await rows.getByLabel('Receiving address',{exact:true}).inputValue(),retained);assert.equal(await card.getByRole('button',{name:'Save',exact:true}).isVisible(),false);
    result.checks.push('re-edit during Saved and reopen match persisted fixture');
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await card.screenshot({path:path.join(output,name+'.png')});
    result.status='passed';console.log('PASS '+name);
   }catch(e){result.status='failed';result.error=e.message;await page.screenshot({path:path.join(output,name+'-failure.png'),fullPage:true});console.error('FAIL '+name+': '+e.message)}
  }
  assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[]);assert.ok(results.length>0);assert.equal(results.filter(r=>r.status==='failed').length,0);
 }finally{
  fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({engine,root,results,errors,unexpected},null,2)+'\n');await browser?.close();server.close();
 }
})().catch(e=>{console.error(e);process.exitCode=1;server.close()});
