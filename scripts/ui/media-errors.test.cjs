const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const source=fs.readFileSync(path.resolve(process.env.UI_SOURCE_DIR||'web/public/ui','generation-artifacts.js'),'utf8');
const context={window:{}};vm.runInNewContext(source.replace('window.GenerationArtifacts={attach,fitVideo}', 'window.GenerationArtifacts={attach,fitVideo,failureMessage}'),context);
const message=context.window.GenerationArtifacts.failureMessage;
test('safe API diagnosis survives with the correct Preview/Download action',()=>{
 for(const action of ['Preview','Download','Last frame preview'])for(const [status,text] of [[401,'Sign in again.'],[403,'Ask an administrator for access.'],[503,'Storage is temporarily unavailable.']]){
  const got=message({status,message:text,isGatewayError:true,code:'safe_code',param:'safe_field'},action);assert.ok(got.startsWith(action+' unavailable. '));assert.ok(got.endsWith(text));
 }
});
test('untrusted thrown errors never become media feedback',()=>{
 const hostile='<img onerror=alert(1)> https://private.invalid/token /private/media/file';
 for(const action of ['Preview','Download'])for(const status of [undefined,401,403,429,500,502,503]){const got=message({message:hostile,status},action);assert.ok(got.startsWith(action+' unavailable.'));assert.doesNotMatch(got,/private|https:|<img|token/);if(status===401)assert.match(got,/sign in/i);if(status===403)assert.match(got,/permissions/);if(status>=500)assert.match(got,/administrator/);}
});
test('typed network, malformed and signed-link failures give bounded recovery',()=>{
 assert.match(message({code:'network_error'},'Download'),/connection/);
 assert.match(message({code:'invalid_response'},'Preview'),/unreadable response/);
 for(const status of [401,403]){const got=message({status},'Preview',true);assert.match(got,/fresh link/);assert.doesNotMatch(got,/sign in/i);}
 assert.match(message({name:'TypeError'},'Preview',true),/connection/);
 assert.match(message({status:503},'Preview',true),/HTTP 503/);
});
