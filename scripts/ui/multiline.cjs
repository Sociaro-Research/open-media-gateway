const {evidencePath} = require('./evidence.cjs');
// Shared multiline behavior: no production requests or provider calls.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const html=`<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"><main style="padding:24px;max-width:760px;margin:auto"><form><label for="prompt">Prompt</label><textarea id="prompt" maxlength="10000"></textarea><details><summary>Reference URLs</summary><label for="urls">Video URLs</label><textarea id="urls" rows="2"></textarea></details><label>Full generation prompt<textarea id="history" readonly></textarea></label><button type="reset">Reset</button></form><div id="dynamic"></div></main><script src="/form-ui.js"></script>`;
const server=http.createServer((req,res)=>{
  if(req.url==='/'){res.setHeader('Content-Type','text/html');return res.end(html);}
  const file=path.join(root,path.basename(req.url));if(!fs.existsSync(file))return res.writeHead(404).end();
  res.setHeader('Content-Type',file.endsWith('.css')?'text/css':'text/javascript');res.end(fs.readFileSync(file));
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const engine=process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium;
  const browser=await engine.launch({headless:true,...(engine===chromium&&process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
  const page=await browser.newPage({viewport:{width:390,height:900},isMobile:true,hasTouch:true});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const metrics=locator=>locator.evaluate(e=>{const s=getComputedStyle(e),r=e.getBoundingClientRect(),l=e.closest('.floating-field')?.querySelector('.floating-label')?.getBoundingClientRect();return {height:r.height,line:parseFloat(s.lineHeight),padding:parseFloat(s.paddingTop)+parseFloat(s.paddingBottom),scroll:e.scrollHeight,client:e.clientHeight,labelBottom:l?.bottom,top:r.top};});
  async function lines(locator,count){await page.waitForFunction(({id,count})=>{const e=document.getElementById(id),s=getComputedStyle(e);return Math.abs(e.getBoundingClientRect().height-(count*parseFloat(s.lineHeight)+parseFloat(s.paddingTop)+parseFloat(s.paddingBottom)))<=1;},{id:await locator.getAttribute('id'),count});const m=await metrics(locator);assert.ok(Math.abs(m.height-(count*m.line+m.padding))<=1,JSON.stringify(m));assert.ok(m.labelBottom<=m.top,'label overlaps scroll viewport');}
  try{
    await page.goto('http://127.0.0.1:'+server.address().port);
    const prompt=page.locator('#prompt');await page.locator('.multiline-field #prompt').waitFor();
    await lines(prompt,3);
    await prompt.fill(Array.from({length:6},(_,i)=>'Line '+i).join('\n'));await lines(prompt,6);
    await prompt.fill(Array.from({length:30},(_,i)=>'Line '+i).join('\n'));await lines(prompt,10);
    await prompt.evaluate(e=>e.scrollTop=e.scrollHeight);const scrolled=await metrics(prompt);assert.ok(scrolled.scroll>scrolled.client);assert.ok(scrolled.labelBottom<=scrolled.top);
    const label=prompt.locator('..').locator('.floating-label'),before=await label.boundingBox();await prompt.evaluate(e=>e.scrollTop=0);assert.deepEqual(await label.boundingBox(),before);
    await prompt.fill('');await prompt.focus();await page.keyboard.insertText('Typed line\n'.repeat(30));await lines(prompt,10);const scrollBefore=await prompt.evaluate(e=>{e.scrollTop=e.scrollHeight;return e.scrollTop;});await page.keyboard.insertText('Last line');assert.ok(await prompt.evaluate((e,before)=>e.scrollTop>=before,scrollBefore),'typing must preserve the scroll position');
    await prompt.fill('Short');await lines(prompt,3);
    // Existing application code also dispatches non-bubbling input events.
    await prompt.evaluate(e=>{e.value='Line\n'.repeat(7);e.dispatchEvent(new Event('input'));});await lines(prompt,8);
    await page.getByRole('button',{name:'Reset',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#prompt').value==='');await lines(prompt,3);
    await page.locator('#urls').evaluate(e=>{e.value='https://example.test/video.mp4\n'.repeat(15);});await page.locator('summary').click();
    await page.waitForFunction(()=>document.querySelector('#urls').clientHeight>=240);await lines(page.locator('#urls'),10);
    await page.evaluate(()=>{document.querySelector('#dynamic').innerHTML='<label for="newField">Notes</label><textarea id="newField">One\nTwo\nThree\nFour\nFive</textarea>';const history=document.querySelector('#history');history.value='Saved prompt\n'.repeat(14);FormUI.refresh();});
    await page.locator('.multiline-field #newField').waitFor();await lines(page.locator('#newField'),5);await lines(page.locator('#history'),10);
    await prompt.fill('A longer sentence that wraps across the available width. '.repeat(8));
    for(const width of [320,430,1440]){await page.setViewportSize({width,height:900});await page.waitForFunction(()=>document.querySelector('#prompt').getBoundingClientRect().width>0);const m=await metrics(prompt);assert.ok(m.height>=3*m.line+m.padding-1&&m.height<=10*m.line+m.padding+1);assert.ok(m.labelBottom<=m.top);}
    await page.setViewportSize({width:390,height:900});await prompt.fill('Line\n'.repeat(30));await prompt.focus();await prompt.evaluate(e=>e.scrollTop=e.scrollHeight);await page.screenshot({path:evidencePath('/tmp/gateway-multiline-')+engine.name()+'.png'});
    assert.deepEqual(errors,[]);console.log('PASS '+engine.name()+': 3–10 rows, scrolling label separation, shrinking, reset, dynamic/hidden/read-only fields and responsive wrapping');
  }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
