// Shared native validation semantics across background layout and view changes.
const {chromium,webkit}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const root=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium';
const out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/validation-layout/'+engine);fs.mkdirSync(out,{recursive:true});
const html=`<!doctype html><html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><body><main class="shell"><section class="panel"><h2>Contact links</h2><form id="draft"><label>Contact link<input id="contact" type="email" required aria-describedby="guidance"></label><p id="guidance" class="field-explanation">Enter your published contact address.</p><button>Save</button></form><label>Adjacent field<input id="other"></label></section></main><script src="/form-ui.js"></script><script>window.submits=0;draft.onsubmit=e=>{e.preventDefault();submits++;};</script></body></html>`;
const server=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://fixture').pathname;
 if(p==='/')return res.writeHead(200,{'Content-Type':'text/html'}).end(html);
 if(p==='/styles.css'||p==='/form-ui.js')return res.writeHead(200,{'Content-Type':p.endsWith('.css')?'text/css':'text/javascript'}).end(fs.readFileSync(path.join(root,p.slice(1))));
 res.writeHead(404).end();
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base='http://127.0.0.1:'+server.address().port;
 const browser=await ({chromium,webkit}[engine]).launch({headless:true});const errors=[],external=[],results=[];
 try{
  const context=await browser.newContext();await context.route('**/*',route=>{if(new URL(route.request().url()).origin===base)return route.continue();external.push(route.request().url());return route.abort();});
  const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});await page.goto(base);await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   const input=page.locator('#contact'),bubble=page.getByRole('alert'),form=page.locator('#draft');
   const invalidate=async()=>{await input.fill('invalid@@example.test');await form.getByRole('button',{name:'Save',exact:true}).click();await bubble.waitFor();assert.equal(await input.getAttribute('aria-invalid'),'true');};
   const cleared=async()=>{assert.equal(await input.getAttribute('aria-invalid'),null);assert.equal(await input.getAttribute('aria-describedby'),'guidance');assert.equal(await page.locator('.field-validation').count(),0);};
   await invalidate();const description=await input.getAttribute('aria-describedby'),scroll=await page.evaluate(()=>scrollY);
   // Observe the refresh atomically: WebKit may subsequently scroll its native
   // focused caret into view. That browser action is not an application scroll.
   const shift=await form.evaluate(f=>{const spacer=document.createElement('div');spacer.id='background-layout';spacer.style.height=(innerHeight+32)+'px';const anchor=document.documentElement.style.overflowAnchor;document.documentElement.style.overflowAnchor='none';f.before(spacer);scrollTo(0,0);const input=f.querySelector('input'),before=scrollY;FormUI.refresh();return {anchor,offscreen:input.getBoundingClientRect().top>innerHeight,before,after:scrollY,invalid:input.getAttribute('aria-invalid'),description:input.getAttribute('aria-describedby'),focused:input===document.activeElement,hidden:document.querySelector('.field-validation')?.hidden};});
   assert.equal(shift.offscreen,true);assert.equal(shift.invalid,'true');assert.equal(shift.description,description);assert.equal(shift.focused,true);assert.equal(shift.after,shift.before,'refresh must not scroll');assert.equal(shift.hidden,true,'offscreen bubble must be hidden');
   await page.evaluate(({anchor,y})=>{document.querySelector('#background-layout').remove();document.documentElement.style.overflowAnchor=anchor;scrollTo(0,y);FormUI.refresh();},{anchor:shift.anchor,y:scroll});
   await bubble.waitFor();assert.equal(await input.getAttribute('aria-describedby'),description);assert.equal(await input.evaluate(e=>e===document.activeElement),true);
   // The opposite viewport edge must retain the same semantics and popup ID.
   await form.evaluate(f=>{f.style.transform='translateY(-2000px)';FormUI.refresh();});
   assert.equal(await input.getAttribute('aria-invalid'),'true');assert.equal(await bubble.count(),0);
   await form.evaluate(f=>{f.style.transform='';FormUI.refresh();});await bubble.waitFor();assert.equal(await input.getAttribute('aria-describedby'),description);
   const box=await bubble.boundingBox();assert.ok(box.x>=11&&box.x+box.width<=width-11);assert.ok(box.y>=11&&box.y+box.height<=1000);
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await page.screenshot({path:path.join(out,`restored-${theme}-${width}.png`)});
   await input.fill('valid@example.test');await cleared();
   await invalidate();await page.locator('#other').focus();await cleared();
   await invalidate();await page.keyboard.press('Escape');await cleared();
   await invalidate();await form.evaluate(f=>{f.hidden=true;FormUI.refresh();});await cleared();await form.evaluate(f=>f.hidden=false);
   await invalidate();await input.evaluate(e=>{e.disabled=true;FormUI.refresh();});await cleared();await input.evaluate(e=>e.disabled=false);
   await invalidate();await form.evaluate(f=>{window.detachedValidationForm=f;f.remove();FormUI.refresh();});assert.equal(await page.locator('.field-validation').count(),0);assert.equal(await page.evaluate(()=>detachedValidationForm.querySelector('input').getAttribute('aria-invalid')),null);
   assert.equal(await page.evaluate(()=>submits),0,'invalid inputs must never submit');
   results.push({theme,width,below:true,above:true,restored:true,correction:true,focusAway:true,escape:true,hidden:true,disabled:true,detached:true});
  }
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({engine,results,errors,external},null,2));
  console.log('PASS: shared validation survives offscreen layout without focus/scroll/write changes, restores its bubble, and clears on correction, focus, Escape and inactive views in both themes at1440/390/320px');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>server.close());
