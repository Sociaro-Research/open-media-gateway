// Verify the deployable origin guard uses actual peer addresses, not spoofable headers.
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),{spawn}=require('node:child_process');
const tmp=fs.mkdtempSync(path.join(os.tmpdir(),'gateway-origin-security-'));
const reserve=async()=>{const server=http.createServer();await new Promise(r=>server.listen(0,'127.0.0.1',r));const port=server.address().port;await new Promise(r=>server.close(r));return port};
const delay=ms=>new Promise(r=>setTimeout(r,ms));let caddy;
(async()=>{
 const control=await reserve(),protectedPort=await reserve();assert.notEqual(control,protectedPort);
 const config=path.join(tmp,'Caddyfile'),snippet=path.resolve(__dirname,'../../deploy/cloudflare-origin.caddy');
 fs.writeFileSync(config,`{\n admin off\n auto_https off\n}\nimport "${snippet}"\n:${control} {\n respond "fixture" 200\n}\n:${protectedPort} {\n import cloudflare_origin_only\n respond "fixture" 200\n}\n`);
 const log=fs.openSync(path.join(tmp,'caddy.log'),'w');caddy=spawn(process.env.CADDY_BIN||'caddy',['run','--config',config,'--adapter','caddyfile'],{stdio:['ignore',log,log]});fs.closeSync(log);
 let ready=false;for(let i=0;i<100;i++){try{if((await fetch('http://127.0.0.1:'+control)).status===200){ready=true;break}}catch{}if(caddy.exitCode!==null)throw Error(fs.readFileSync(path.join(tmp,'caddy.log'),'utf8'));await delay(50)}assert.ok(ready,'Caddy did not start');
 for(const headers of [{},{'X-Forwarded-For':'173.245.48.1','CF-Connecting-IP':'173.245.48.1','X-Real-IP':'173.245.48.1','Forwarded':'for=173.245.48.1'}]){
  const response=await fetch('http://127.0.0.1:'+protectedPort,{headers});assert.equal(response.status,403,'untrusted peer bypassed origin guard');
 }
 console.log('PASS: origin guard rejects direct requests and forged Cloudflare/forwarding headers; control endpoint is healthy.');
})().catch(error=>{console.error(error);process.exitCode=1}).finally(async()=>{if(caddy&&caddy.exitCode===null){caddy.kill('SIGTERM');await new Promise(r=>caddy.once('exit',r))}fs.rmSync(tmp,{recursive:true,force:true})});
