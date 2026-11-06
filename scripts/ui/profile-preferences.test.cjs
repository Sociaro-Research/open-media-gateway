const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
function fixture(){
 const calls=[],state={cache:null,connection:null};
 const ctx={document:{querySelector:()=>null},UserUI:{remember:value=>state.cache=JSON.parse(JSON.stringify(value))},GatewayTheme:{apply(){}},CompanyConnection:{record:()=>state.connection},CustomEvent:class{},dispatchEvent(){},DOMException,console};ctx.window=ctx;
 vm.runInNewContext(fs.readFileSync(path.resolve(__dirname,'../../web/public/ui/profile.js'),'utf8'),ctx);
 const profile=ctx.GatewayProfile,request=(url,options)=>new Promise((resolve,reject)=>calls.push({url,input:JSON.parse(options.body),resolve,reject}));
 profile.identify({id:'one',session_id:'session-one',theme:'dark',table_columns:{organizations:['organization']}});
 return {profile,request,calls,state};
}
const tick=()=>new Promise(setImmediate),plain=value=>JSON.parse(JSON.stringify(value));
test('per-key FIFO adopts A success even if queued B fails; unrelated stale response fields do not replace cache',async()=>{
 const f=fixture(),{profile:p,request:r,calls:c}=f;
 const a=p.save({table_columns:{organizations:['balance']}},r),b=p.save({table_columns:{organizations:['credit_limit']}},r),theme=p.save({theme:'light'},r);
 await tick();assert.equal(c.length,2);c.find(x=>x.input.theme).resolve({id:'one',session_id:'session-one',theme:'light',table_columns:{organizations:['organization']}});await theme;
 c.find(x=>x.input.table_columns).resolve({id:'one',session_id:'session-one',theme:'dark',table_columns:{organizations:['balance']}});await a;await tick();assert.deepEqual(plain(p.tableColumns('organizations')),['balance']);assert.equal(f.state.cache.theme,'light');assert.equal(c.length,3);
 const rejected=assert.rejects(b,/Synthetic failure/);c[2].reject(Error('Synthetic failure'));await rejected;assert.deepEqual(plain(p.tableColumns('organizations')),['balance']);assert.equal(f.state.cache.theme,'light');
});
test('different tables and width/columns merge independently despite full stale profile responses',async()=>{
 const {profile:p,request:r,calls:c}=fixture();
 const a=p.save({table_columns:{organization_members:['role']}},r),b=p.save({table_columns:{audit_events:['action']}},r),w=p.save({table_wide:{organization_members:true}},r);
 await tick();c[2].resolve({table_wide:{organization_members:true},table_columns:{organizations:['organization']}});await w;c[1].resolve({table_columns:{audit_events:['action']}});await b;c[0].resolve({table_columns:{organization_members:['role']},table_wide:{}});await a;
 assert.deepEqual(plain(p.tableColumns('audit_events')),['action']);assert.deepEqual(plain(p.tableColumns('organization_members')),['role']);assert.equal(p.tableWide('organization_members'),true);
});
test('delayed confirmations are fenced by profile identity, session and connection',async()=>{
 for(const change of ['identity','session','connection']){
  const f=fixture(),promise=f.profile.save({table_columns:{organizations:['balance']}},f.request);await tick();const rejected=assert.rejects(promise,e=>e.name==='AbortError');
  if(change==='identity')f.profile.identify({id:'two',session_id:'session-two',theme:'light'});
  else if(change==='session')f.profile.identify({id:'one',session_id:'new-session',theme:'light'});
  else f.state.connection={id:'new-connection'};
  f.calls[0].resolve({id:'one',session_id:'session-one',table_columns:{organizations:['balance']}});await rejected;
  assert.notDeepEqual(plain(f.profile.tableColumns('organizations')||[]),['balance']);
 }
});

test('authoritative profile reload clears reset preferences while partial confirmations merge per table',()=>{
 const {profile:p}=fixture();p.identify({table_columns:{audit_events:['actor']}});assert.deepEqual(plain(p.tableColumns('organizations')),['organization']);p.identify({id:'one',session_id:'session-one',theme:'dark',table_columns:{}});assert.equal(p.tableColumns('organizations'),undefined);assert.equal(p.tableColumns('audit_events'),undefined);p.identify({table_columns:{organizations:['balance']}});p.identify({id:'one',session_id:'session-one',theme:'dark'});assert.equal(p.tableColumns('organizations'),undefined);
});

test('a new connection does not queue behind an old connection write for the same field',async()=>{
 const f=fixture(),a=f.profile.save({theme:'light'},f.request);await tick();const aborted=assert.rejects(a,e=>e.name==='AbortError');f.state.connection={id:'new'};const b=f.profile.save({theme:'dark'},f.request);await tick();assert.equal(f.calls.length,2,'new connection request starts independently');f.calls[1].resolve({theme:'dark'});await b;f.calls[0].resolve({theme:'light'});await aborted;assert.equal(f.state.cache.theme,'dark');
});
