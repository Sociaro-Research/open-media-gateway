// Exercise the deployable access-log filter without using real credentials.
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),{spawn}=require('node:child_process');
const tmp=fs.mkdtempSync(path.join(os.tmpdir(),'gateway-log-security-'));
const delay=ms=>new Promise(r=>setTimeout(r,ms));let caddy;
(async()=>{
 const server=http.createServer();await new Promise(r=>server.listen(0,'127.0.0.1',r));const port=server.address().port;await new Promise(r=>server.close(r));
 const config=path.join(tmp,'Caddyfile'),access=path.join(tmp,'access.json'),diagnostics=path.join(tmp,'caddy.log');
 // Keep the exact filter; only redirect its output to this private fixture file.
 const snippet=fs.readFileSync(path.resolve(__dirname,'../../deploy/access-log-redaction.caddy'),'utf8').replace('output stdout',`output file "${access}"`);
 fs.writeFileSync(config,`{\n admin off\n auto_https off\n}\n${snippet}\n:${port} {\n import redacted_access_log\n header Set-Cookie "fixture-session=response-secret; HttpOnly"\n respond "fixture" 200\n}\n`);
 const fd=fs.openSync(diagnostics,'w');caddy=spawn(process.env.CADDY_BIN||'caddy',['run','--config',config,'--adapter','caddyfile'],{stdio:['ignore',fd,fd]});fs.closeSync(fd);
 let ready=false;for(let i=0;i<100;i++){try{if((await fetch('http://127.0.0.1:'+port)).status===200){ready=true;break}}catch{}if(caddy.exitCode!==null)throw Error(fs.readFileSync(diagnostics,'utf8'));await delay(50)}assert.ok(ready,'Caddy did not start');
 const params=new URLSearchParams({visible:'retained',signature:'signed-secret',token:'token-secret',code:'code-secret',state:'state-secret',api_key:'query-key-secret',access_token:'access-secret','X-Amz-Signature':'aws-secret'});
 const response=await fetch(`http://127.0.0.1:${port}/fixture?${params}`,{headers:{'X-API-Key':'header-key-secret',Authorization:'Bearer authorization-secret','Proxy-Authorization':'Basic proxy-secret',Cookie:'session=cookie-secret'}});
 assert.equal(response.status,200);assert.equal(response.headers.get('set-cookie'),'fixture-session=response-secret; HttpOnly','filter must preserve the actual response');
 let entries=[];for(let i=0;i<100;i++){if(fs.existsSync(access)){entries=fs.readFileSync(access,'utf8').trim().split('\n').filter(Boolean).map(JSON.parse);if(entries.some(e=>e.request?.uri?.startsWith('/fixture?')))break}await delay(25)}
 const entry=entries.find(e=>e.request?.uri?.startsWith('/fixture?'));assert.ok(entry,'request missing from access log');assert.equal(entry.status,200);
 assert.equal(new URL(entry.request.uri,'http://fixture').searchParams.get('visible'),'retained','ordinary query metadata must remain');
 for(const key of Object.keys(Object.fromEntries(params)))if(key!=='visible')assert.equal(new URL(entry.request.uri,'http://fixture').searchParams.has(key),false,key+' persisted');
 for(const key of ['X-Api-Key','Authorization','Proxy-Authorization','Cookie'])assert.equal(entry.request.headers[key],undefined,key+' persisted');assert.equal(entry.resp_headers['Set-Cookie'],undefined);
 assert.ok(!JSON.stringify(entries).includes('-secret'),'credential marker persisted in access logs');
 console.log('PASS: API keys, auth/session headers, signed links and OAuth query secrets are absent from access logs; status, ordinary metadata and real response remain intact.');
})().catch(error=>{console.error(error);process.exitCode=1}).finally(async()=>{if(caddy&&caddy.exitCode===null){caddy.kill('SIGTERM');await new Promise(r=>caddy.once('exit',r))}fs.rmSync(tmp,{recursive:true,force:true})});
