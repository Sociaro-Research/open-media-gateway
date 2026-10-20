// Called by TestStoredSVGIsSandboxed with its real API fixture URL and signed route.
const {chromium}=require('playwright');
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),{spawn}=require('node:child_process');
const root=path.resolve(__dirname,'../..'),tmp=fs.mkdtempSync(path.join(os.tmpdir(),'gateway-media-security-'));
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
let caddy,browser;
(async()=>{
 const upstream=new URL(process.argv[2]),route=process.argv[3];assert.ok(route?.startsWith('/media/'));
 const reservation=http.createServer();await new Promise(r=>reservation.listen(0,'127.0.0.1',r));const port=reservation.address().port;await new Promise(r=>reservation.close(r));
 const log=fs.openSync(path.join(tmp,'caddy.log'),'w');
 caddy=spawn(process.env.CADDY_BIN||'caddy',['run','--config',path.join(root,'web/Caddyfile'),'--adapter','caddyfile'],{env:{...process.env,UI_ROOT:path.join(root,'web/public'),GATEWAY_UPSTREAM:upstream.host,UI_LISTEN:':'+port,DOCS_HOST:'docs.invalid'},stdio:['ignore',log,log]});fs.closeSync(log);
 const base='http://127.0.0.1:'+port;
 let ready=false;for(let i=0;i<100;i++){try{if((await fetch(base+'/ui/site.js')).ok){ready=true;break}}catch{}if(caddy.exitCode!==null)throw Error(fs.readFileSync(path.join(tmp,'caddy.log'),'utf8'));await delay(50)}assert.ok(ready,'Caddy did not start');
 browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage();let probes=0;page.on('request',r=>{if(r.url().includes('/security-script-ran'))probes++});
 for(const url of [upstream.origin+route,base+route]){
  const response=await page.goto(url);assert.equal(response.status(),200);assert.match(response.headers()['content-security-policy'],/sandbox/);
  await page.waitForTimeout(200);assert.equal(await page.locator('svg').getAttribute('data-script-ran'),null,'SVG script ran');
  assert.equal(probes,0,'SVG made a script-initiated request');
 }
 console.log('PASS: real API and Caddy proxy retain SVG sandbox; script and fetch are blocked in Chromium.');
})().catch(error=>{console.error(error);process.exitCode=1}).finally(async()=>{await browser?.close();if(caddy&&caddy.exitCode===null){caddy.kill('SIGTERM');await new Promise(r=>caddy.once('exit',r))}fs.rmSync(tmp,{recursive:true,force:true})});
