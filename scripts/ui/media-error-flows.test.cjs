// Deterministic DOM boundary fixture. Browser acceptance remains a separate gate.
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const root=path.resolve(process.env.UI_SOURCE_DIR||'web/public/ui');
const app=fs.readFileSync(path.join(root,'app.js'),'utf8'),helper=app.slice(app.indexOf('async function api('),app.indexOf('\nProviderLogos.configure('));
const media=fs.readFileSync(path.join(root,'generation-artifacts.js'),'utf8');
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}};
const settle=async predicate=>{for(let i=0;i<100;i++){if(predicate())return;await new Promise(setImmediate);}assert.fail('Expected asynchronous renderer state did not settle');};
const json=(status,body)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});
function fixture(kind='image'){
 const state={requests:[],clicks:[],tabs:[],grant:0};
 class Element extends EventTarget{
  constructor(tag){super();this.tagName=tag.toUpperCase();this.children=[];this.attributes={};this.className='';this.hidden=false;this.disabled=false;this._text='';this.style={setProperty(){},removeProperty(){}};this.classList={add:value=>{this.className+=' '+value}};}
  get textContent(){return this._text+this.children.map(n=>n.textContent).join('');}set textContent(value){this._text=String(value);this.children=[];}
  append(...nodes){for(const n of nodes){n.parent=this;this.children.push(n);}}
  remove(){if(this.parent)this.parent.children=this.parent.children.filter(n=>n!==this);this.parent=null;}
  get isConnected(){return this.connected||!!this.parent?.isConnected;}
  setAttribute(name,value){this.attributes[name]=String(value);}removeAttribute(name){delete this.attributes[name];}hasAttribute(name){return Object.hasOwn(this.attributes,name);}
  set src(value){this.setAttribute('src',value);}get src(){return this.attributes.src||'';}set poster(value){this.setAttribute('poster',value);}get poster(){return this.attributes.poster||'';}
  querySelectorAll(selector){return this.children.flatMap(n=>[...(selector.startsWith('.')?n.className.split(' ').includes(selector.slice(1)):n.tagName===selector.toUpperCase())?[n]:[],...n.querySelectorAll(selector)]);}
  querySelector(selector){return this.querySelectorAll(selector)[0]||null;}
  click(){state.clicks.push({tag:this.tagName,href:this.href,download:this.download});}pause(){this.paused=true;}load(){}
 }
 const document={createElement:tag=>new Element(tag),head:new Element('head'),body:new Element('body')};document.body.connected=true;
 const article=new Element('article'),result=new Element('div');result.className='task-result';article.append(result);document.body.append(article);
 state.bundle=()=>({artifacts:[{id:'1',preview_url:'/signed/media?grant='+ ++state.grant+'&variant=%2F',download_url:'/signed/media?grant='+state.grant+'&download=1'}]});
 state.access=async()=>json(200,state.bundle());state.signed=async()=>new Response('Safe synthetic text');
 const context=vm.createContext({document,window:{GenerationKey:{ensureSession:async()=>{},value:()=> 'synthetic-key',forget(){}},open:()=>{const tab={document,closed:false,close(){this.closed=true}};state.tabs.push(tab);return tab;}},ProviderLogos:{clear(){}},sharedConfiguration:false,authMode:'google',generationKeyVersion:0,AbortController,AbortSignal,DOMException,FormData,TextDecoder,
  fetch:async(url,options)=>{state.requests.push({url,options});return url.startsWith('/signed/')?state.signed(url,options):state.access(url,options);}});
 vm.runInContext(helper+';this.api=api',context);vm.runInContext(media,context);
 const task={id:'synthetic',artifacts:[{id:'1',media_type:kind,content_type:kind==='text'?'text/plain':kind==='binary'?'application/octet-stream':'image/png'}]};
 const attach=()=>context.window.GenerationArtifacts.attach(article,task,{api:context.api,context:()=>context.generationKeyVersion,bytes:String});
 const note=()=>result.querySelector('.media-note').textContent,button=label=>result.querySelectorAll('button').find(n=>n.textContent===label),player=()=>result.querySelector(kind==='text'?'.artifact-text':kind==='binary'?'.absent':'img');
 return {state,context,article,result,attach,note,button,player};
}
test('initial auth/server diagnoses remain specific and explicit preview retry recovers',async()=>{
 for(const [status,text] of [[401,'Sign in again.'],[403,'Ask an administrator for access.'],[503,'Storage is temporarily unavailable.']]){
  const f=fixture();f.state.access=async()=>json(status,{error:{message:text,code:'safe_code',param:null}});await f.attach();assert.match(f.note(),new RegExp('Preview unavailable\\. '+text));assert.equal(f.button('Retry preview').hidden,false);
  f.state.access=async()=>json(200,f.state.bundle());await f.button('Retry preview').onclick();assert.equal(f.note(),'image');assert.match(f.player().src,/variant=%2F$/);
 }
});
test('Open and Download preserve safe diagnoses, close failed popup and retain current player',async()=>{
 for(const label of ['Open ↗','Download ↓'])for(const status of [401,403,503]){
  const f=fixture();await f.attach();const src=f.player().src;f.state.access=async()=>json(status,{error:{message:'Safe specific diagnosis',code:'safe_code'}});
  await f.button(label).onclick();assert.match(f.note(),new RegExp((label.startsWith('Download')?'Download':'Preview')+' unavailable\\. Safe specific diagnosis'));assert.equal(f.player().src,src);assert.equal(f.button(label).disabled,false);assert.ok(f.state.tabs.every(tab=>tab.closed));
 }
});
test('late text success cannot erase Download error and successful Download retains Preview error',async()=>{
 const f=fixture('text'),gate=deferred();f.state.signed=()=>gate.promise;await f.attach();assert.match(f.note(),/Loading text/);
 f.state.access=async()=>json(503,{error:{message:'Download diagnosis'}});await f.button('Download ↓').onclick();assert.match(f.note(),/Download unavailable\. Download diagnosis/);
 gate.resolve(new Response('Safe synthetic text'));await settle(()=>f.player().textContent==='Safe synthetic text');assert.match(f.note(),/Download diagnosis/);
 f.state.access=async()=>json(200,f.state.bundle());await f.button('Download ↓').onclick();assert.doesNotMatch(f.note(),/Download diagnosis/);
 const expired=fixture('text');expired.state.signed=async()=>new Response('',{status:403});await expired.attach();await settle(()=>expired.note().includes('fresh link'));assert.doesNotMatch(expired.note(),/Sign in/);
 await expired.button('Download ↓').onclick();assert.match(expired.note(),/fresh link/);expired.state.signed=async()=>new Response('Recovered');await expired.button('Retry preview').onclick();await settle(()=>expired.player().textContent==='Recovered');assert.equal(expired.note(),'text');
});
test('duplicate actions, disposed or changed contexts never apply a late access response',async()=>{
 for(const interrupt of ['abort','context','remove'])for(const success of [true,false]){
  const f=fixture();await f.attach();const gate=deferred();f.state.access=()=>gate.promise;const before=f.state.requests.length,pending=f.button('Download ↓').onclick();await settle(()=>f.state.requests.length===before+1);await f.button('Download ↓').onclick();assert.equal(f.state.requests.length,before+1);
  if(interrupt==='abort')f.article.mediaController.abort();if(interrupt==='context')f.context.generationKeyVersion++;if(interrupt==='remove'){f.article.mediaController.abort();f.article.remove();}
  gate.resolve(success?json(200,f.state.bundle()):json(503,{error:{message:'Late error must not render'}}));await pending;assert.equal(f.state.clicks.length,0);assert.doesNotMatch(f.note(),/Late error/);if(interrupt!=='context')assert.equal(f.player().src,'');
 }
});
test('missing and malformed access responses, native preview failure and binary downloads stay safe',async()=>{
 for(const body of [null,[],{artifacts:[null]},{artifacts:{}}]){const f=fixture();f.state.access=async()=>json(200,body);await f.attach();assert.match(f.note(),/unreadable response/);assert.doesNotMatch(f.note(),/connection/);}
 const f=fixture();await f.attach();f.state.access=async()=>json(404,{error:{message:'Not found'}});await f.button('Download ↓').onclick();assert.match(f.note(),/no longer available/);assert.equal(f.player().src,'');
 f.state.access=async()=>json(200,f.state.bundle());await f.button('Retry preview').onclick();f.player().onerror();assert.match(f.note(),/Retry preview/);await f.button('Retry preview').onclick();assert.equal(f.note(),'image');
 const binary=fixture('binary');await binary.attach();assert.equal(binary.button('Open ↗'),undefined);assert.equal(binary.button('Retry preview').hidden,true);await binary.button('Download ↓').onclick();assert.match(binary.state.clicks[0].href,/download=1$/);
});
test('hostile non-JSON/transport content stays hidden, JSON diagnosis uses plain text',async()=>{
 const raw='<img onerror=alert(1)> https://private.invalid/token /private/media/path';
 for(const failure of [()=>new Response(raw,{status:502}),()=>{throw TypeError(raw)}]){const f=fixture();await f.attach();f.state.access=failure;await f.button('Download ↓').onclick();assert.match(f.note(),/Download unavailable/);assert.doesNotMatch(f.note(),/private|<img|alert/);}
 const f=fixture();await f.attach();f.state.access=async()=>json(503,{error:{message:'<em>Safe literal JSON diagnosis</em>'}});await f.button('Download ↓').onclick();assert.match(f.note(),/<em>Safe literal JSON diagnosis<\/em>/);assert.equal(f.result.querySelectorAll('em').length,0);
});
