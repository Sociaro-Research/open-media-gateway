// Existing portal, synthetic central company API and local-control fixtures only.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(process.env.UI_SOURCE_DIR||'web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',out=process.env.UI_EVIDENCE_DIR||'/tmp/gw282-simple-key-20261104/'+engine;
fs.mkdirSync(out,{recursive:true});
const key='synthetic-central-company-key',company='11111111-1111-4111-8111-111111111111';
const record={connected:true,access:'company-api-v1',context:'connection-one',origin:'https://console.sociaro.ai',organization_id:company,organization_name:'Central fixture company',version:1};
const model={id:'image',display_name:'Central image',media_type:'image',resolutions:['1024x1024'],ratios:['1:1'],pricing:{known:true,reference_price_usd:0},prompt_optional:false};
let online=true,bootstrap=true,submissions=[],central=[],local=[],transfers=[];
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),chunks=[];for await(const chunk of req)chunks.push(chunk);const raw=Buffer.concat(chunks),json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(v));};
 if(u.pathname==='/auth/company-connection')return json(bootstrap?{connected:true,connection:record,key}:{connected:false});
 if(u.pathname.startsWith('/auth/company-api/')){
  const p=u.pathname.slice('/auth/company-api'.length);central.push({path:p,method:req.method,query:u.search,body:raw.toString(),headers:req.headers});
  if(req.headers.authorization!=='Bearer '+key||req.headers['x-company-context']!==record.context)return json({error:{message:'Wrong company authority'}},403);
  if(!online)return json({error:{message:'Central fixture unavailable'}},503);
  if(p==='/v1/info')return json({auth_mode:'google',console:{generation_ui:true,personal_providers:false,shared_resources:true}});
  if(p==='/v1/me')return json({id:'central-owner',name:'Central Owner',email:'central@example.test',is_system_admin:false});
  if(p==='/v1/organizations')return json({data:[{id:company,name:'Central fixture company',role:'supermanager'}]});
  if(p==='/v1/key')return json({key:{id:'company-key',name:'Company connection',user_id:'central-owner',organization_id:company,resource_mode:'shared',scopes:['generation','organizations:manage','keys:manage']},organization:{id:company,available_usd:5,weekly_limit_usd:null,weekly_committed_usd:0}});
  if(p==='/v1/providers')return json({data:[{id:'fixture',name:'Central provider',default_model:'image',models:[model]}]});
  if(p==='/v1/generations'&&req.method==='GET')return json({data:[],journal_scope:'central',total:0,has_more:false,next_cursor:null});
  if(p.endsWith('/estimate'))return json({can_generate:true,cost:{expected_usd:0}});
  if(p==='/v1/generations'&&req.method==='POST'){submissions.push(JSON.parse(raw));return json({id:'fixture-task',status:'queued'},202);}
  if(p==='/v1/templates')return json({data:[{id:'template',title:'Central template',description:'Shared central template',cover_type:'image/png',cover_url:'/template-media/fixture?signature=untouched%2Bbytes'}]});
  if(p==='/v1/references'&&req.method==='POST'){transfers.push({type:req.headers['content-type'],body:raw.toString()});return json({id:'central-upload'});}
  if(p==='/v1/test-range'){res.writeHead(206,{'Content-Range':'bytes 0-2/3','Content-Type':'application/octet-stream'});return res.end('abc');}
  if(p==='/v1/test-redirect'){res.writeHead(302,{Location:'/v1/must-not-follow'});return res.end();}
  return json({data:[]});
 }
 if(u.pathname.startsWith('/v1/')){local.push({path:u.pathname,method:req.method,auth:req.headers.authorization});if(u.pathname==='/v1/system/remote-workspace')return json(record);if(u.pathname.endsWith('/recovery'))return json({data:[]});return json({error:{message:'Unexpected local API'}},503);}
 const name=['/','/templates','/organizations'].includes(u.pathname)?'index.html':path.basename(u.pathname),file=path.join(root,name);if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[],external=[];
 const expectedErrors=[];page.on('pageerror',e=>{if(engine==='webkit'&&phase==='redirect and foreign key'&&e.message.includes('/auth/company-api/v1/test-redirect due to access control checks.'))expectedErrors.push(e.message);else errors.push(e.message);});page.setDefaultTimeout(10000);await page.route(u=>u.origin!==base,r=>{const u=new URL(r.request().url());if(u.origin==='https://console.sociaro.ai'&&u.pathname==='/template-media/fixture'){assert.equal(u.search,'?signature=untouched%2Bbytes');assert.equal(r.request().headers().authorization,undefined);return r.fulfill({contentType:'image/png',body:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aT0sAAAAASUVORK5CYII=','base64')});}external.push({url:r.request().url(),headers:r.request().headers()});return r.abort();});
 let phase='boot';try{
  await page.goto(base);await page.waitForFunction(()=>document.querySelector('#model')?.options.length>0&&document.querySelector('#apiKey')?.value===window.CompanyConnection?.key());
  assert.equal(await page.locator('#apiKey').inputValue(),key);assert.equal(await page.locator('#workspaceSelect').inputValue(),company);assert.equal(await page.locator('#userName').textContent(),'Central Owner');assert.equal(local.length,0);assert.equal(await page.locator('.remote-dialog').count(),0);
  phase='ordinary generation';await page.locator('#prompt').fill('Literal central fixture prompt');await page.locator('#submitButton').click();await page.locator('#confirmDialog').waitFor({state:'visible'});await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);assert.equal(submissions.length,1);assert.equal(submissions[0].prompt,'Literal central fixture prompt');assert.equal(submissions[0].model,'fixture/image');
  phase='streaming request and signed query';const result=await page.evaluate(async()=>{const form=new FormData();form.append('file',new File(['fixture-only-bytes'],'fixture.txt',{type:'text/plain'}));const upload=await fetch('/v1/references',{method:'POST',body:form});const range=await fetch(new Request('/v1/test-range?signature=a%2Bb&v=1&v=2',{headers:{Range:'bytes=0-2'}}));return {upload:await upload.json(),status:range.status,body:await range.text()};});assert.deepEqual(result,{upload:{id:'central-upload'},status:206,body:'abc'});assert.equal(transfers.length,1);assert.ok(transfers[0].type.startsWith('multipart/form-data; boundary='));assert.ok(transfers[0].body.includes('fixture-only-bytes'));assert.equal(central.find(r=>r.path==='/v1/test-range').query,'?signature=a%2Bb&v=1&v=2');
  phase='redirect and foreign key';assert.equal(await page.evaluate(async()=>{try{await fetch('/v1/test-redirect');return false}catch{return true}}),true);assert.equal(local.length,0);const before=central.length;assert.equal(await page.evaluate(async()=>{try{await fetch('/v1/models',{headers:{Authorization:'Bearer foreign'}});return false}catch{return true}}),true);assert.equal(central.length,before);
  phase='templates and reload';await page.goto(base+'/templates');await page.getByRole('button',{name:'Central template',exact:true}).waitFor();assert.equal(await page.locator('#apiKey').inputValue(),key);assert.equal(local.length,0);
  phase='connection controls';await page.locator('#userMenuToggle').hover();await page.getByRole('menuitem',{name:'Sociaro connection'}).click();await page.getByRole('button',{name:'Disconnect',exact:true}).waitFor();assert.equal(local.length,1);assert.equal(local[0].auth,undefined);await page.keyboard.press('Escape');
  phase='responsive existing interface';for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.screenshot({path:path.join(out,theme+'-'+width+'.png'),fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));}
  phase='outage no fallback';online=false;await page.reload();await page.locator('#authRetry').waitFor({state:'visible'});assert.equal(local.length,1);assert.equal(submissions.length,1);assert.deepEqual(errors,[]);assert.equal(expectedErrors.length,engine==='webkit'?1:0);assert.deepEqual(external,[]);
  fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({engine,passed:8,errors,expectedErrors,submissions,local,paths:[...new Set(central.map(r=>r.path))]},null,2));console.log('PASS 8 company-key existing UI cases '+engine);
 }catch(e){fs.writeFileSync(path.join(out,'failure.json'),JSON.stringify({phase,error:e.message,errors,local,paths:central.map(r=>r.path)},null,2));await page.screenshot({path:path.join(out,'failure.png')}).catch(()=>{});throw e}finally{await browser.close();server.closeAllConnections();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
