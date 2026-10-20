const {evidencePath} = require('./evidence.cjs');
// Render actual Go-generated email bodies. No external requests or mail delivery.
const {chromium,webkit}=require('playwright'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),{pathToFileURL}=require('node:url');
(async()=>{
 const root=process.env.GATEWAY_EMAIL_PREVIEW_DIR||evidencePath('/tmp/gateway-email-preview');
 const engine=process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium;
 const browser=await engine.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH});
 try{
  const page=await browser.newPage();await page.route('https://**/*',r=>r.abort());
  for(const purpose of ['verify','reset']){
   await page.goto(pathToFileURL(path.join(root,purpose+'.html')).href);
   for(const width of [680,390,320]){
    await page.setViewportSize({width,height:960});await page.locator('img').evaluateAll(imgs=>Promise.all(imgs.map(img=>img.decode())));
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${purpose} overflows ${width}`);
    assert.equal(await page.locator('h1').count(),1);
    const links=await page.locator('a').evaluateAll(nodes=>nodes.map(n=>n.href));assert.equal(links.length,2);assert.equal(links[0],links[1]);
    const button=await page.locator('a').first().boundingBox();assert.ok(button.height>=44&&button.x>=0&&button.x+button.width<=width,'CTA clipped or too small');
    await page.screenshot({path:path.join(root,`${purpose}-${process.env.UI_BROWSER_ENGINE||'chromium'}-${width}.png`),fullPage:true});
   }
   // Essential layout and actions remain intact when the client strips <style>.
   await page.locator('style').evaluateAll(nodes=>nodes.forEach(n=>n.remove()));
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'inline fallback overflows');
   await page.locator('img').evaluateAll(imgs=>imgs.forEach(img=>img.style.display='none'));assert.ok(await page.locator('h1').isVisible());assert.ok(await page.locator('a').first().isVisible());
  }
  console.log('Account emails: both purposes, 680/390/320 px, optional uploaded logo, CTA/fallback URLs, stripped-style and blocked-image fallbacks passed.');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
