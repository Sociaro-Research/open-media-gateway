// Synthetic loopback fixture APIs; real UI scripts, no live accounts or services.
const { [process.env.UI_BROWSER_ENGINE||'chromium']:engine }=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui'),out=process.env.UI_EVIDENCE_DIR||'/tmp/gateway-legal-documents';
let saved={privacy:{text:'',version:1},terms:{text:'',version:1}},failure=0,delay=0,writes=0;
const server=http.createServer(async(req,res)=>{
 const url=req.url.split('?')[0],json=(status,data)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data))};
 const match=url.match(/^\/(v1\/system\/)?legal\/(privacy|terms)$/);
 if(match){const admin=!!match[1],kind=match[2];if(failure)return json(failure,{error:{message:'Fixture failure'}});
  if(req.method==='PUT'){let raw='';for await(const c of req)raw+=c;writes++;await new Promise(r=>setTimeout(r,delay));const data=JSON.parse(raw);saved[kind]={text:data.text,version:saved[kind].version+1}}
  return json(200,admin?saved[kind]:{text:saved[kind].text});
 }
 if(url==='/editor')return res.end('<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><link rel="stylesheet" href="/ui/styles.css"></head><body><main id="editor"></main><script src="/ui/form-ui.js"></script><script src="/ui/legal.js"></script><script>LegalTextUI.mount(document.querySelector("main"),async(path,options)=>{const r=await fetch(path,options),data=await r.json();if(!r.ok){const e=Error(data.error.message);e.status=r.status;throw e}return data});</script></body></html>');
 const name=url==='/privacy'?'privacy.html':url==='/terms'?'terms.html':path.basename(url),file=path.join(root,name);
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 fs.mkdirSync(out,{recursive:true});await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await engine.launch({headless:true}),page=await browser.newPage(),base='http://127.0.0.1:'+server.address().port,errors=[];
 page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(8000);
 try{
 for(const theme of ['dark','light'])for(const width of [1440,390,320]){
  await page.setViewportSize({width,height:1000});await page.emulateMedia({colorScheme:theme});saved={privacy:{text:'',version:1},terms:{text:'',version:1}};
  await page.goto(base+'/editor');await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
  await page.getByRole('button',{name:'Edit Privacy Policy'}).click();const modal=page.getByRole('dialog'),input=modal.locator('textarea'),save=modal.getByRole('button',{name:'Save',exact:true});
  await input.waitFor();await page.waitForFunction(()=>!document.querySelector('textarea').disabled);
  assert.ok((await input.inputValue()).length>100);assert.ok(await save.isDisabled());
  await input.fill('Unsaved draft');await page.keyboard.press('Escape');await page.locator('dialog').waitFor({state:'detached'});assert.equal(saved.privacy.text,'');assert.equal(await page.locator('dialog').count(),0);
  await page.getByRole('button',{name:'Edit Privacy Policy'}).click();await page.waitForFunction(()=>!document.querySelector('textarea').disabled);
  const attack='<img src=x onerror="window.pwned=true">\n\n<script>window.pwned=true</script>';
  await input.fill(attack);delay=300;const before=writes;await save.click();await page.keyboard.press('Escape');assert.ok(await modal.isVisible());assert.ok(await save.isDisabled());await page.locator('dialog').waitFor({state:'detached'});assert.equal(writes,before+1);delay=0;
  await page.goto(base+'/privacy');await page.locator('.legal-text').filter({hasText:'<img'}).waitFor();assert.equal(await page.locator('.legal-text').textContent(),attack);assert.equal(await page.locator('.legal-text img,.legal-text script').count(),0);assert.equal(await page.evaluate(()=>window.pwned),undefined);assert.ok(await page.locator('[data-legal-default]').isHidden());
  await page.goto(base+'/editor');await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await page.getByRole('button',{name:'Edit Privacy Policy'}).click();await page.waitForFunction(()=>!document.querySelector('textarea').disabled);assert.equal(await input.inputValue(),attack);
  await input.fill('Changed');failure=409;await save.click();await modal.getByText(/changed elsewhere/).waitFor();assert.equal(await input.inputValue(),'Changed');failure=0;
  await modal.screenshot({path:path.join(out,(process.env.UI_BROWSER_ENGINE||'chromium')+'-'+theme+'-'+width+'.png')});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await modal.getByRole('button',{name:'Cancel',exact:true}).click();
  await page.getByRole('button',{name:'Edit Terms of Use'}).click();await page.waitForFunction(()=>!document.querySelector('textarea').disabled);await input.fill('Terms override');await save.click();await page.locator('dialog').waitFor({state:'detached'});assert.equal(saved.privacy.text,attack);assert.equal(saved.terms.text,'Terms override');
  await page.getByRole('button',{name:'Edit Privacy Policy'}).click();await page.waitForFunction(()=>!document.querySelector('textarea').disabled);
  await modal.getByRole('button',{name:'Use default template'}).click();await save.click();await page.locator('dialog').waitFor({state:'detached'});await page.goto(base+'/privacy');await page.locator('[data-legal-default]').waitFor({state:'visible'});assert.equal(saved.privacy.text,'');
  failure=503;await page.reload();await page.getByRole('button',{name:'Retry'}).waitFor();assert.ok(await page.locator('[data-legal-default]').isHidden());failure=0;await page.getByRole('button',{name:'Retry'}).click();await page.locator('[data-legal-default]').waitFor({state:'visible'});
 }
 assert.deepEqual(errors.filter(x=>x!=='ResizeObserver loop completed with undelivered notifications.'),[]);
 console.log('PASS: six theme/viewport variants; default, edit, save, cancel/Escape, pending save, conflict, reset, independent documents, literal XSS payload, public read failure/retry.');
 }finally{await browser.close();server.close()}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
