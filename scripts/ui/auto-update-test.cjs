const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const source=fs.readFileSync(path.resolve(__dirname,'../../web/public/ui/auto-update.js'),'utf8');
function fixture(load){
 const document=new EventTarget(),window=new EventTarget(),timers=new Map();let sequence=0,observe;
 document.body={};document.hidden=false;
 const root={isConnected:true,tagName:'DIV',hidden:false,closest(){return this.hidden?this:null;}};
 class MutationObserver{constructor(fn){observe=fn;}observe(){}disconnect(){}}
 vm.runInNewContext(source,{window,document,MutationObserver,AbortController,setTimeout:fn=>{timers.set(++sequence,fn);return sequence;},clearTimeout:id=>timers.delete(id)});
 const updates=window.GatewayAutoUpdate.watch({root,load});
 return {document,window,root,updates,timers,mutate:()=>observe(),tick(){const callbacks=[...timers.values()];timers.clear();callbacks.forEach(fn=>fn());}};
}
const settle=async()=>{for(let n=0;n<12;n++)await Promise.resolve();};
test('deduplicates reads and invalidates stale completion after context replacement',async()=>{
 const pending=[],committed=[];const f=fixture(async request=>{await new Promise(resolve=>pending.push({request,resolve}));if(request.current())committed.push(pending.length);});
 const first=f.updates.refresh();await settle();assert.equal(pending.length,1);
 assert.equal(f.updates.refresh(),first);f.window.dispatchEvent(new Event('focus'));await settle();assert.equal(pending.length,1);
 f.updates.refresh({restart:true});await settle();assert.equal(pending.length,2);assert.equal(pending[0].request.signal.aborted,true);
 pending[0].resolve();await settle();assert.deepEqual(committed,[]);pending[1].resolve();await settle();assert.deepEqual(committed,[2]);f.updates.stop();
});
test('pauses hidden reads, resumes immediately and retries after transient failure',async()=>{
 const seen=[];const f=fixture(async request=>{seen.push(request);if(seen.length===1)throw Error('temporary');});
 await f.updates.refresh();assert.equal(seen.length,1);f.tick();await settle();assert.equal(seen.length,2);
 f.document.hidden=true;f.document.dispatchEvent(new Event('visibilitychange'));f.tick();await settle();assert.equal(seen.length,2);assert.equal(seen[1].current(),false);
 f.document.hidden=false;f.document.dispatchEvent(new Event('visibilitychange'));await settle();assert.equal(seen.length,3);f.updates.stop();
});
test('navigation aborts reads and stops polling; back-forward cache resumes',async()=>{
 const requests=[];const f=fixture(async request=>{requests.push(request);});await f.updates.refresh();
 f.window.dispatchEvent(new Event('pagehide'));assert.equal(requests[0].current(),false);f.tick();await settle();assert.equal(requests.length,1);
 f.window.dispatchEvent(new Event('pageshow'));await settle();assert.equal(requests.length,2);
 f.root.hidden=true;f.mutate();assert.equal(requests[1].current(),false);f.tick();await settle();assert.equal(requests.length,2);
 f.root.hidden=false;f.mutate();await settle();assert.equal(requests.length,3);
 f.root.isConnected=false;f.mutate();f.window.dispatchEvent(new Event('focus'));f.tick();await settle();assert.equal(requests.length,3);assert.equal(f.timers.size,0);
});

test('a hung read is cancelled and retried without accepting its eventual result',async()=>{
 const pending=[];let commits=0;const f=fixture(async request=>{await new Promise(resolve=>pending.push({request,resolve}));if(request.current())commits++;});
 f.updates.refresh();await settle();f.tick();await settle();assert.equal(pending[0].request.signal.aborted,true);f.tick();await settle();assert.equal(pending.length,2);pending[0].resolve();await settle();assert.equal(commits,0);pending[1].resolve();await settle();assert.equal(commits,1);f.updates.stop();
});
