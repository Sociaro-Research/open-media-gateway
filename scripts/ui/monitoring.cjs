// Exercise the real UI proxy's monitoring boundary against disposable fixtures.
// Optionally render a real provisioned Grafana via GRAFANA_TEST_UPSTREAM.
const {chromium}=require('playwright');
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),{spawn}=require('node:child_process');
const root=path.resolve(__dirname,'../..'),tmp=fs.mkdtempSync(path.join(os.tmpdir(),'gateway-monitoring-ui-'));
const listen=server=>new Promise(resolve=>server.listen(0,'127.0.0.1',()=>resolve(server.address().port)));
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
let caddy,browser,revoked=false,unavailable=false,forwarded=[];
const api=http.createServer((req,res)=>{
 const p=new URL(req.url,'http://localhost').pathname;
 const json=data=>res.writeHead(200,{'Content-Type':'application/json'}).end(JSON.stringify(data));
 const admin=req.headers.cookie?.includes('test_session=alice');
 if(p==='/auth/monitoring'){
  assert.equal(req.headers.authorization,undefined,'API credential reached auth preflight');
  const signed=admin||req.headers.cookie?.includes('test_session=bob');
  return res.writeHead(unavailable?503:!signed?401:admin&&!revoked?204:403,{'Cache-Control':'no-store',...(admin&&!revoked&&!unavailable?{'X-WEBAUTH-USER':'gateway-alice'}:{})}).end();
 }
 if(p==='/auth/session')return res.writeHead(admin?204:401).end();
 if(p==='/site.json')return json({branding:{name:'Gateway',assets:{}},api_url:'',ui_url:'',docs_url:''});
 if(p==='/auth/methods')return json({email:true,email_delivery:true,google:true});
 if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true}});
 if(p==='/v1/me')return json({id:'alice',name:'Alice',email:'alice@example.test',is_system_admin:!revoked});
 if(p==='/v1/system/settings')return json({generation_ui:true,personal_providers:true,shared_resources:false,version:1});
 if(p.startsWith('/v1/'))return json({data:[]});
 res.writeHead(404).end();
});
const grafana=http.createServer((req,res)=>{
 forwarded.push(req.headers);
 res.writeHead(200,{'Content-Type':'text/html','Content-Security-Policy':"default-src 'self'; script-src 'nonce-fixture'; style-src 'self'"});
 res.end('<!doctype html><html><title>Gateway overview</title><h1>Gateway overview</h1><p>Service availability</p><a href="/system">Gateway administration</a></html>');
});
(async()=>{
 const apiPort=await listen(api),grafanaPort=await listen(grafana),reservation=http.createServer();
 let port=Number(process.env.GRAFANA_TEST_UI_PORT)||await listen(reservation);
 if(reservation.listening)await new Promise(resolve=>reservation.close(resolve));
 const base='http://127.0.0.1:'+port,real=!!process.env.GRAFANA_TEST_UPSTREAM;
 const env={...process.env,UI_ROOT:path.join(root,'web/public'),GATEWAY_UPSTREAM:'127.0.0.1:'+apiPort,GRAFANA_UPSTREAM:process.env.GRAFANA_TEST_UPSTREAM||'127.0.0.1:'+grafanaPort,UI_LISTEN:':'+port,DOCS_HOST:'docs.localhost'};
 const fd=fs.openSync(path.join(tmp,'caddy.log'),'w');
 caddy=spawn(process.env.CADDY_BIN||'caddy',['run','--config',path.join(root,'web/Caddyfile'),'--adapter','caddyfile'],{env,stdio:['ignore',fd,fd]});fs.closeSync(fd);
 for(let i=0;;i++){try{if((await fetch(base+'/ui/site.js')).ok)break}catch{}if(i>100||caddy.exitCode!==null)throw Error(fs.readFileSync(path.join(tmp,'caddy.log'),'utf8'));await delay(50)}
 async function request(p,status,headers={},method='GET',body){
  return new Promise((resolve,reject)=>{
   const req=http.request(base+p,{headers,method},res=>{let data='';res.on('data',chunk=>data+=chunk);res.on('end',()=>{try{assert.equal(res.statusCode,status,p+' '+data.slice(0,500));resolve({headers:new Headers(res.headers),text:async()=>data,json:async()=>JSON.parse(data)})}catch(error){reject(error)}})});req.on('error',reject);req.end(body);
  });
 }
 const admin={Cookie:'test_session=alice; media_gateway_session=must-not-reach-grafana',Authorization:'Bearer must-not-reach-grafana','X-WEBAUTH-USER':'forged'};
 assert.match(await(await request('/monitoring/',200)).text(),/Sign in to get started/);
 for(const p of ['/monitoring/api/user','/monitoring/d/gateway-overview/gateway-overview','/monitoring/public/build/app.js']){
  await request(p,401);await request(p,401,{'X-WEBAUTH-USER':'gateway-alice',Authorization:'Bearer fixture-key'});await request(p,403,{Cookie:'test_session=bob'});await request(p,404,{Host:'docs.localhost',...admin});
 }
 const entry=await request('/monitoring/',302,admin);assert.equal(entry.headers.get('location'),'/monitoring/d/gateway-overview/gateway-overview');
 const result=await request('/monitoring/d/gateway-overview/gateway-overview',200,admin);
 assert.ok(result.headers.get('content-security-policy').includes('nonce-'),'Grafana policy was overwritten');
 if(!real){assert.ok(forwarded.length);for(const headers of forwarded){assert.equal(headers['x-webauth-user'],'gateway-alice');assert.equal(headers.authorization,undefined);assert.equal(headers.cookie,undefined)}}
 revoked=true;await request('/monitoring/api/user',403,admin);revoked=false;
 unavailable=true;await request('/monitoring/api/user',503,admin);unavailable=false;
 assert.ok((await request('/ui/site.js',200)).headers.get('content-security-policy').includes("script-src 'self'"),'Console policy changed');
 browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const context=await browser.newContext(),page=await context.newPage(),errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 const screenshots=process.env.UI_SCREENSHOT_DIR||path.join(tmp,'screenshots');fs.mkdirSync(screenshots,{recursive:true});
 for(const theme of ['dark','light'])for(const theme of ['dark','light'])for(const width of [1440,390,320]){
  if(real)await request('/monitoring/api/user/preferences',200,{...admin,Origin:base,'Content-Type':'application/json'},'PUT',JSON.stringify({theme}));
  await page.setViewportSize({width,height:1000});await page.emulateMedia({colorScheme:theme});
  await page.goto(base+'/monitoring/');await page.getByRole('button',{name:'Continue with email',exact:true}).waitFor();
  assert.equal(await page.evaluate(()=>JSON.parse(sessionStorage.getItem('gateway-login-return')).path),'/monitoring/');
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Login overflow '+width);
  await page.getByRole('button',{name:'Continue with email',exact:true}).focus();await page.keyboard.press('Enter');await page.getByRole('textbox',{name:'Email',exact:true}).focus();await page.keyboard.press('Tab');
  assert.equal(await page.evaluate(()=>document.activeElement.type),'password');
  await page.screenshot({path:path.join(screenshots,`monitoring-login-${theme}-${width}.png`)});
 }
 await context.addCookies([{name:'test_session',value:'alice',url:base}]);
 await page.goto(base+'/');await page.waitForURL('**/monitoring/d/gateway-overview/gateway-overview');
 if(real){
  const details=await(await request('/monitoring/api/dashboards/uid/gateway-overview',200,admin)).json();assert.equal(details.dashboard.uid,'gateway-overview');assert.equal(details.meta.canSave,false);
  const user=await(await request('/monitoring/api/user',200,admin)).json();assert.equal(user.login,'gateway-alice');assert.equal(user.isGrafanaAdmin,false);
  await request('/monitoring/api/dashboards/db',403,{...admin,Origin:base,'Content-Type':'application/json'},'POST',JSON.stringify({dashboard:{title:'Must not save'},overwrite:false}));
  let now=Date.now();const queries=[{refId:'A',expr:'up',instant:true,range:false,datasource:{type:'prometheus',uid:'gateway-prometheus'},intervalMs:15000,maxDataPoints:100}];
  // Container health precedes Prometheus's first scrape. Wait for actual data,
  // keeping query errors fatal rather than masking a broken datasource.
  let hasUp=false;const deadline=Date.now()+60000;
  do{
   const current=Date.now(),query=await(await request('/monitoring/api/ds/query',200,{...admin,Origin:base,'Content-Type':'application/json'},'POST',JSON.stringify({from:String(current-300000),to:String(current),queries}))).json();
   assert.ok(!query.results.A.error,'Prometheus query failed: '+query.results.A.error);
   hasUp=query.results.A.frames.some(frame=>frame.data.values.some(values=>values.includes(1)));
   if(hasUp)break;await delay(500);
  }while(Date.now()<deadline);
  assert.ok(hasUp,'Grafana returned no UP samples within 60 seconds');
  now=Date.now();
  const expressions=details.dashboard.panels.flatMap(panel=>panel.targets||[]).map(target=>target.expr.replaceAll('$__rate_interval','5m').replaceAll('$route','.*'));
  const batch=expressions.map((expr,index)=>({...queries[0],refId:'Q'+index,expr}));
  const checked=await(await request('/monitoring/api/ds/query',200,{...admin,Origin:base,'Content-Type':'application/json'},'POST',JSON.stringify({from:String(now-300000),to:String(now),queries:batch}))).json();
  for(const [ref,result]of Object.entries(checked.results)){assert.ok(!result.error,ref+': '+result.error);assert.ok(!result.status||result.status===200,ref+': '+result.status)}
  assert.equal(Object.keys(checked.results).length,expressions.length,'Some dashboard queries were not evaluated');
 }
 for(const theme of ['dark','light'])for(const width of [1440,390,320]){
  if(real)await request('/monitoring/api/user/preferences',200,{...admin,Origin:base,'Content-Type':'application/json'},'PUT',JSON.stringify({theme}));
  await page.setViewportSize({width,height:1000});await page.goto(base+'/monitoring/');
  await page.getByText('Service availability',{exact:true}).first().waitFor({timeout:30000});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Dashboard overflow '+width);
  await page.keyboard.press('Tab');assert.ok(await page.evaluate(()=>document.activeElement!==document.body),'Dashboard lacks keyboard focus');
  if(real)await delay(3000);
  await page.screenshot({path:path.join(screenshots,`monitoring-dashboard-${theme}-${width}.png`)});
 }
 await page.goto(base+'/system');await page.locator('#systemNav').click();
 const action=page.getByRole('button',{name:'Monitoring ↗',exact:true});await action.waitFor();await action.focus();await page.keyboard.press('Enter');await page.waitForURL('**/monitoring/d/gateway-overview/gateway-overview');
 assert.deepEqual(errors.filter(message=>!message.includes('ResizeObserver')),[]);
 fs.writeFileSync(path.join(screenshots,'monitoring-summary.json'),JSON.stringify({proxy_boundary:'passed',login_return:'passed',administrator_navigation:'passed',desktop_mobile_keyboard:'passed',real_grafana:real?'passed':'unexecuted',screenshots},null,2)+'\n');
 console.log('Monitoring UI passed; real Grafana: '+real+'; screenshots: '+screenshots);
})().catch(error=>{console.error(error);process.exitCode=1}).finally(async()=>{
 await browser?.close();api.close();grafana.close();
 if(caddy&&caddy.exitCode===null){caddy.kill('SIGTERM');await new Promise(resolve=>caddy.once('exit',resolve))}
 fs.rmSync(tmp,{recursive:true,force:true});
});
