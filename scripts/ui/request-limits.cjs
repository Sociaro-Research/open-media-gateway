const {evidencePath} = require('./evidence.cjs');
// Shared request-limit editor, tested without production accounts or mutations.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{
 const name=req.url.split('?')[0];
 if(name==='/'){res.setHeader('Content-Type','text/html');return res.end('<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"><main id="configurationView"><div class="organizations-content" id="fixture"></div></main><script src="/form-ui.js"></script><script src="/limits.js"></script>')}
 const file=path.join(root,name);if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'application/octet-stream');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const engine=process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium;
 const browser=await engine.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH});
 try{const page=await browser.newPage({viewport:{width:1440,height:1080}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.address().port}`);
 await page.evaluate(async()=>{
  const values={key_generations_minute:100,user_generations_minute:300,key_generations_second:20,user_generations_second:50,key_reads_minute:1200,user_reads_minute:3600,user_key_creations_minute:5,user_active_keys:20};
  window.policy={system:{values,version:1},organization:{values:Object.fromEntries(Object.keys(values).map(k=>[k,null])),version:1},effective:{...values}};window.writes=[];window.rejectSave=false;
  const api=async(url,options={})=>{if(options.method==='PUT'){if(window.rejectSave){const e=new Error('Conflict');e.status=409;throw e}const data=JSON.parse(options.body);window.writes.push({url,data});const scope=url.startsWith('/v1/system/')?'system':'organization';window.policy[scope]={...data,version:window.policy[scope].version+1}}return structuredClone(window.policy)};
  await RequestLimits.mount(document.querySelector('#fixture'),api);
  await RequestLimits.mount(document.querySelector('#fixture'),api,{organization:'test-company'});
 });
 const forms=page.locator('.request-limits'),sys=forms.nth(0),org=forms.nth(1);
 assert.equal(await forms.count(),2);assert.equal(await org.locator('input').count(),8);
 assert.equal(await org.locator('[name=user_active_keys]').inputValue(),'');assert.equal(await org.locator('[name=user_active_keys]').getAttribute('placeholder'),'20');
 await sys.locator('[name=user_active_keys]').fill('30');await sys.getByRole('button',{name:'Save',exact:true}).click();await sys.getByText('Saved',{exact:true}).waitFor();
 await org.locator('[name=user_active_keys]').fill('7');await org.getByRole('button',{name:'Save',exact:true}).click();await org.getByText('Saved',{exact:true}).waitFor();
 await org.locator('[name=user_active_keys]').fill('');await org.getByRole('button',{name:'Save',exact:true}).click();
 assert.deepEqual(await page.evaluate(()=>writes.map(w=>w.data.values.user_active_keys)),[30,7,null]);
 await page.evaluate(()=>rejectSave=true);await org.locator('[name=user_active_keys]').fill('8');await org.getByRole('button',{name:'Save',exact:true}).click();await org.getByText('Another administrator changed these limits. Reopen Settings before saving.').waitFor();assert.equal(await org.locator('[name=user_active_keys]').inputValue(),'8');
 fs.mkdirSync(evidencePath('/tmp/gateway-layout'),{recursive:true});
 for(const width of [1440,390,320]){await page.setViewportSize({width,height:1080});await page.screenshot({path:evidencePath(`/tmp/gateway-layout/request-limits-${process.env.UI_BROWSER_ENGINE||'chromium'}-${width}.png`),fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`overflow at ${width}`);
  assert.equal(await org.locator('.floating-field:not(.raised)').count(),0,'inherited placeholder overlaps its label');
  if(width<=600){const a=await org.locator('h2').boundingBox(),b=await org.locator('[role=status]').boundingBox();assert.ok(b.y>=a.y+a.height,'error overlaps heading')}
  const controls=await page.locator('.floating-field').evaluateAll(nodes=>nodes.map(n=>{const r=n.getBoundingClientRect();return {left:r.left,right:r.right,width:r.width}}));assert.ok(controls.every(r=>r.left>=0&&r.right<=width&&r.width>100),'invalid field geometry');
 }
 assert.deepEqual(errors,[]);console.log('Request limits: shared editor, save feedback, inheritance, conflicts, responsive layout passed.');
 }finally{await browser.close()}
})().then(()=>server.close(),e=>{console.error(e);server.close();process.exitCode=1});
