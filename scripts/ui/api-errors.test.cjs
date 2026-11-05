// Execute the actual shared helper without bootstrapping unrelated portal views.
const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(process.env.UI_SOURCE_DIR||'web/public/ui'),app=fs.readFileSync(path.join(root,'app.js'),'utf8');
const source=app.slice(app.indexOf('async function api('),app.indexOf('\nProviderLogos.configure('));
function fixture(fetch){
 const state={key:'synthetic-key',forgot:[],cleared:0,requests:[]};
 const context=vm.createContext({AbortController,DOMException,FormData,sharedConfiguration:false,authMode:'google',generationKeyVersion:0,
  window:{GenerationKey:{ensureSession:async()=>{},value:()=>state.key,forget:message=>state.forgot.push(message)}},
  ProviderLogos:{clear:()=>state.cleared++},fetch:async(...args)=>{state.requests.push(args);return fetch(...args);}});
 vm.runInContext(source+';this.api=api',context);return {...state,state,context,api:context.api};
}
const json=(status,body)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});
const hostile='<html>private-token https://private.example/media/secret?signature=secret /private/media/file</html>';
test('network failures are safe, actionable and never replay a mutation',async()=>{
 for(const method of ['GET','POST','PATCH','DELETE']){
  const f=fixture(async()=>{throw TypeError(hostile)});
  await assert.rejects(f.api('/v1/system/settings',{method,body:method==='GET'?undefined:'{}',headers:{'Idempotency-Key':'original'}}),e=>{
   assert.equal(e.code,'network_error');assert.equal(e.isGatewayError,true);assert.match(e.message,/connection/);assert.doesNotMatch(e.message,/private|signature|https:|<html>/);
   if(method!=='GET')assert.match(e.message,/original task|saved state/);return true;
  });assert.equal(f.state.requests.length,1);assert.equal(f.state.requests[0][1].headers['Idempotency-Key'],'original');
 }
});
test('non-JSON HTTP errors retain status and appropriate recovery without body text',async()=>{
 for(const status of [400,401,403,404,408,429,500,502,503,504])for(const body of [hostile,'','null','[]']){
  const f=fixture(async()=>new Response(body,{status}));
  await assert.rejects(f.api('/v1/system/settings'),e=>{assert.equal(e.status,status);assert.equal(e.isGatewayError,true);assert.doesNotMatch(e.message,/private|signature|https:|<html>/);assert.ok(e.message.length>20);if(status===401)assert.match(e.message,/sign in/i);if(status===403)assert.match(e.message,/permissions/);if(status>=500)assert.match(e.message,/administrator/);return true;});
 }
});
test('safe JSON diagnosis and typed metadata survive unchanged, including templates',async()=>{
 const detail={message:'Correct options.duration: use 5–15 seconds.',code:'invalid_request',param:'options.duration',templates:[{id:'safe'}]};
 const f=fixture(async()=>json(400,{error:detail}));await assert.rejects(f.api('/v1/generations/estimate',{method:'POST'}),e=>{assert.equal(e.message,detail.message);assert.equal(e.code,detail.code);assert.equal(e.param,detail.param);assert.equal(JSON.stringify(e.templates),JSON.stringify(detail.templates));assert.equal(e.status,400);return true;});
 for(const status of [401,403,503]){const f=fixture(async()=>json(status,{error:{message:'Safe server diagnosis',code:'safe_code',param:null}}));await assert.rejects(f.api('/v1/system/settings'),e=>e.message==='Safe server diagnosis'&&e.code==='safe_code'&&e.param===null);}
});
test('malformed success and body transport failure do not become empty success',async()=>{
 for(const failure of [new SyntaxError(hostile),new TypeError(hostile)]){const f=fixture(async()=>({ok:true,status:200,json:async()=>{throw failure}}));await assert.rejects(f.api('/v1/system/settings',{method:'PATCH'}),e=>{assert.equal(e.code,failure.name==='SyntaxError'?'invalid_response':'network_error');assert.match(e.message,/saved state/);assert.doesNotMatch(e.message,/private|signature/);return true;});}
});
test('body transport failures preserve already confirmed HTTP auth/server status',async()=>{
 for(const status of [401,403,502,503]){const f=fixture(async()=>({ok:false,status,json:async()=>{throw TypeError(hostile)}}));await assert.rejects(f.api('/v1/system/settings'),e=>{assert.equal(e.status,status);assert.doesNotMatch(e.message,/private|https:|<html>/);if(status===401)assert.match(e.message,/sign in/i);if(status===403)assert.match(e.message,/permissions/);if(status>=500)assert.match(e.message,/administrator/);return true;});assert.equal(f.state.cleared,status<500?1:0);}
 const f=fixture(async()=>{throw Error(hostile)});await assert.rejects(f.api('/v1/system/settings'),e=>e.code==='request_failed'&&!e.message.includes('private'));
 const company='Use the connected company key in this interface.';const c=fixture(async()=>{throw Error(company)});await assert.rejects(c.api('/v1/system/settings'),e=>e.message===company&&e.code==='company_key_required');
});
test('sign-in preflight failures are safe and do not send the requested mutation',async()=>{
 const safe='Your sign-in session ended or changed. Refresh and sign in again.';
 for(const failure of [TypeError(hostile),SyntaxError(hostile),Error(hostile),Error(safe),new DOMException('Canceled','AbortError')]){
  const f=fixture(async()=>json(200,{}));f.context.window.GenerationKey.ensureSession=async()=>{throw failure};
  await assert.rejects(f.api('/v1/generations',{method:'POST'}),e=>{if(failure.name==='AbortError')return e===failure;if(failure.message===safe)return e.message===safe&&e.status===401;assert.match(e.message,/requested action was not sent/);assert.doesNotMatch(e.message,/private|https:|<html>/);return true;});assert.equal(f.state.requests.length,0);
 }
 const f=fixture(async()=>json(200,{}));f.context.window.GenerationKey.ensureSession=async()=>{f.context.generationKeyVersion++;throw TypeError(hostile)};await assert.rejects(f.api('/v1/generations'),{name:'AbortError'});
});
test('aborts and context changes stay AbortError before and during fetch/body reads',async()=>{
 const abort=new DOMException('Canceled','AbortError');const f=fixture(async()=>{throw abort});await assert.rejects(f.api('/v1/system/settings'),e=>e===abort);
 const controller=new AbortController();controller.abort();const early=fixture(async()=>json(200,{}));await assert.rejects(early.api('/v1/system/settings',{signal:controller.signal}),{name:'AbortError'});assert.equal(early.state.requests.length,0);
 for(const stage of ['fetch','body'])for(const failure of [false,true]){
  const f=fixture(async()=>{if(stage==='fetch'){f.context.generationKeyVersion++;if(failure)throw TypeError(hostile);}return {ok:true,status:200,json:async()=>{if(stage==='body'){f.context.generationKeyVersion++;if(failure)throw SyntaxError(hostile);}return {};}};});
  await assert.rejects(f.api('/v1/generations'),{name:'AbortError'});
 }
 const during=new AbortController(),body=fixture(async()=>({ok:true,status:200,json:async()=>{during.abort();throw abort}}));await assert.rejects(body.api('/v1/system/settings',{signal:during.signal}),{name:'AbortError'});
});
test('auth credentials, key forgetting, logo invalidation, success and blobs retain contracts',async()=>{
 for(const status of [401,403]){const f=fixture(async()=>json(status,{error:{message:'Safe auth error'}}));await assert.rejects(f.api('/v1/generations'),{message:'Safe auth error'});assert.equal(f.state.cleared,1);assert.equal(f.state.forgot.length,status===401?1:0);assert.equal(f.state.requests[0][1].credentials,'omit');assert.equal(f.state.requests[0][1].headers.Authorization,'Bearer synthetic-key');}
 const f=fixture(async()=>json(200,{ok:true}));assert.equal((await f.api('/v1/system/settings')).ok,true);assert.equal(f.state.requests[0][1].credentials,'same-origin');assert.equal(f.state.requests[0][1].headers.Authorization,undefined);
 const blob=new Blob(['synthetic']);const b=fixture(async()=>({ok:true,status:200,blob:async()=>blob}));assert.equal(await b.api('/v1/generations/synthetic/content',{responseType:'blob'}),blob);
 for(const [status,method] of [[204,'DELETE'],[205,'POST'],[200,'HEAD']]){const empty=fixture(async()=>({ok:true,status,json:async()=>{throw SyntaxError('empty')}}));assert.equal(JSON.stringify(await empty.api('/v1/system/settings',{method})),'{}');}
});
