// Actual docs.JSON contract plus isolated synthetic composition edge cases; no API writes.
const {chromium,webkit}=require('playwright');
const assert=require('node:assert/strict'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),crypto=require('node:crypto'),{execFileSync}=require('node:child_process');
const {evidencePath}=require('./evidence.cjs');
const repo=path.resolve(__dirname,'../..'),root=path.join(repo,'web/public/ui');
const temporary=fs.mkdtempSync(path.join(os.tmpdir(),'gateway-docs-schema-'));
let actual;
try{
 fs.writeFileSync(path.join(temporary,'main.go'),'package main\nimport("os";"github.com/Sociaro-Research/open-media-gateway/docs")\nfunc main(){data,err:=docs.JSON();if err!=nil{panic(err)};os.Stdout.Write(data)}\n');
 actual=JSON.parse(execFileSync('go',['run',path.join(temporary,'main.go')],{cwd:repo,encoding:'utf8',maxBuffer:8*1024*1024}));
}finally{fs.rmSync(temporary,{recursive:true,force:true});}
const ref=name=>({$ref:'#/components/schemas/'+name});
const edge={info:{version:'synthetic'},tags:[],paths:{},components:{schemas:{
 Base:{type:'object',description:'Referenced base description.',properties:{id:{type:'string',format:'uuid',description:'Identifier description.'},optional:{type:'boolean'}}},
 Combined:{description:'Composition description.',required:['id','own'],allOf:[ref('Base'),{required:['label'],type:'object',properties:{label:{type:'string',description:'Label description.'}}}],properties:{own:{type:'integer'}}},
 RefSibling:{...ref('Base'),description:'Sibling reference description.',required:['id']},
 Mixed:{type:'object',required:['shared'],properties:{shared:{type:['number','null'],description:'Shared description.'}},oneOf:[{type:'object',properties:{first:{type:'string'}}},{type:'object',properties:{second:{type:'boolean'}}}],anyOf:[{type:'object',properties:{third:{type:'integer'}}}]},
 Repeated:{allOf:[ref('Base'),ref('Base')]},
 Recursive:{type:'object',properties:{child:ref('Recursive')}},
 CycleA:{allOf:[ref('CycleB')]},CycleB:{allOf:[ref('CycleA')]},
 Scalar:{type:'string',allOf:[{type:'string',format:'date'},{enum:['2026-10-21']}]},
 Empty:{},
 }}};
for(let i=0;i<16;i++)edge.components.schemas['Deep'+i]=i===15?{type:'string'}:{type:'object',properties:{next:ref('Deep'+(i+1))}};
let spec=actual;
const server=http.createServer((req,res)=>{
 if(req.method!=='GET')return res.writeHead(405).end();
 const url=new URL(req.url,'http://localhost');
 if(url.pathname==='/openapi.json'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify(spec));}
 if(url.pathname==='/site.json'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({name:'Documentation fixture'}));}
 const file=url.pathname==='/ui/docs.js'&&process.env.UI_DOCS_SCRIPT?process.env.UI_DOCS_SCRIPT:path.join(root,url.pathname==='/docs'?'documentation.html':path.basename(url.pathname));
 if(!fs.existsSync(file)||!fs.statSync(file).isFile())return res.writeHead(404).end();
 res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.html':'text/html','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));
});
let browser;
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const engine=process.env.UI_BROWSER_ENGINE==='webkit'?'webkit':'chromium';
 browser=await(engine==='webkit'?webkit:chromium).launch({headless:true,...(engine==='chromium'&&process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage(),errors=[],external=[],results=[];
 page.on('pageerror',e=>{if(engine==='webkit'&&e.message==='ResizeObserver loop completed with undelivered notifications.'){console.warn('WebKit resize notice: '+e.message);return;}errors.push(e.message);});
 const origin=`http://127.0.0.1:${server.address().port}`,content=page.locator('#docsContent');
 await page.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin){external.push(route.request().url());return route.abort();}return route.continue();});
 const directory=evidencePath('/tmp/gateway-documentation-schemas/'+engine+'/');fs.mkdirSync(directory,{recursive:true});
 const row=(scope,name)=>scope.locator(`.docs-field:has(> .docs-field-name > code:text-is("${name}"))`);
 const open=async name=>{await page.goto(origin+'/docs?fixture='+(spec===actual?'actual':'edge')+'#docs/'+name);const title=name.startsWith('schema-')?name.slice(7):Object.values(actual.paths).flatMap(item=>Object.values(item)).find(op=>op.operationId===name).summary;await content.getByRole('heading',{name:title,exact:true,level:1}).waitFor();};
 const expand=async()=>page.locator('#docsContent details').evaluateAll(nodes=>nodes.forEach(n=>n.open=true));
 const metrics=async scope=>{
  for(const [name,field]of Object.entries(actual.components.schemas.ReportMetrics.properties)){
   const entry=row(scope,name).first();assert.equal(await entry.count(),1,'Missing metric '+name);
   assert.match(await entry.locator(':scope > .docs-field-name > span').innerText(),new RegExp(field.type));
   assert.equal(await entry.locator(':scope > .docs-field-name > em').filter({hasText:'required'}).count(),actual.components.schemas.ReportMetrics.required.includes(name)?1:0,'required '+name);
   if(field.description)assert.ok((await entry.locator(':scope > p').allTextContents()).includes(field.description),'description '+name);
  }
 };
 const group=async scope=>{await metrics(scope);for(const name of ['name','provider'])assert.equal(await row(scope,name).count(),1);assert.ok(await scope.getByRole('link',{name:'ReportMetrics',exact:true}).count()>0);};
 const summary=async scope=>{
  await metrics(scope);
  for(const name of ['providers','provider_models','public_models'])await group(row(scope,name).first());
  const periods=row(scope,'periods').first();await metrics(periods);for(const name of ['from','to'])assert.match(await row(periods,name).locator(':scope > .docs-field-name > span').innerText(),/string · date/);
  assert.match(await row(scope,'average_cost_usd').locator(':scope > .docs-field-name > span').innerText(),/number \| null/);
  assert.ok(!(await scope.innerText()).includes('Any JSON value'));
 };
 const routes=['/v1/admin/reports','/v1/system/reports','/v1/organizations/{org}/reports'].map(p=>actual.paths[p].get.operationId);
 for(const width of [1440,390,320])for(const theme of ['light','dark']){
  await page.setViewportSize({width,height:1000});await page.emulateMedia({colorScheme:theme});spec=actual;
  await open('schema-ReportGroup');await expand();await group(content);assert.ok(!(await content.innerText()).includes('Any JSON value'));
  const metricsLink=content.getByRole('link',{name:'ReportMetrics',exact:true}).first();await metricsLink.focus();await page.keyboard.press('Enter');await page.getByRole('heading',{name:'ReportMetrics',exact:true}).waitFor();await metrics(content);
  await open('schema-ReportSummary');await expand();await summary(content);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'ReportSummary overflow');
  await page.screenshot({path:path.join(directory,`summary-${theme}-${width}.png`)});
  for(const route of routes){
   await open(route);const response=content.locator('.docs-response').first();
   const variant=response.locator('summary').filter({hasText:/^ReportSummary$/}).first();await variant.focus();await page.keyboard.press('Enter');
   assert.ok(await variant.evaluate(n=>n.parentElement.open),'Keyboard expands response composition');
   await expand();await summary(response.locator('details').filter({has:page.locator(':scope > summary').filter({hasText:/^ReportSummary$/})}).first());
   assert.ok((await response.innerText()).includes('has_more'),'Other oneOf branch retained');assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Report response overflow '+route);
  }
  for(const name of ['Organization','ProviderAssetBinding','SystemSettings','TaskCost']){await open('schema-'+name);await expand();assert.ok((await content.innerText()).includes('All of the following schemas apply:'),name);assert.ok(await content.locator('.docs-field').count()>0);}
  spec=edge;
  await open('schema-Combined');await expand();
  for(const name of ['id','label','own'])assert.equal(await row(content,name).locator(':scope > .docs-field-name > em').filter({hasText:'required'}).count(),1,'allOf required '+name);
  for(const text of ['Composition description.','Referenced base description.','Identifier description.','Label description.'])assert.ok((await content.innerText()).includes(text));
  assert.equal(await row(content,'optional').locator(':scope > .docs-field-name > em').count(),0);
  await open('schema-RefSibling');assert.ok((await content.innerText()).includes('Referenced base description.'));assert.ok((await content.innerText()).includes('Sibling reference description.'));assert.ok(!(await content.innerText()).includes('Any JSON value'));assert.equal(await row(content,'id').locator(':scope > .docs-field-name > em').count(),1);
  await open('schema-Mixed');await expand();for(const name of ['shared','first','second','third'])assert.equal(await row(content,name).count(),1);assert.match(await row(content,'shared').locator(':scope > .docs-field-name > span').innerText(),/number \| null/);
  await open('schema-Repeated');assert.equal(await row(content,'id').count(),2);assert.ok(!(await content.innerText()).includes('Recursive schema'));
  for(const name of ['Recursive','CycleA','Deep0']){await open('schema-'+name);await expand();assert.match(await content.innerText(),/Recursive schema|Nested schema/);assert.ok(await content.getByRole('link').count()>0);assert.ok(await content.locator('*').count()<200,'Bounded recursion');}
  await open('schema-Scalar');assert.ok((await content.locator(':scope > p').allTextContents()).includes('string'),'Composition sibling type');assert.match(await content.innerText(),/string · date/);assert.match(await content.innerText(),/Values: 2026-10-21/);assert.ok(!(await content.innerText()).includes('Any JSON value'));
  await open('schema-Empty');assert.ok((await content.innerText()).includes('Any JSON value'));
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Horizontal overflow');results.push({width,theme,result:'passed'});
 }
 assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
 fs.writeFileSync(path.join(directory,'results.json'),JSON.stringify({engine,results,openapiSHA256:crypto.createHash('sha256').update(JSON.stringify(actual)).digest('hex'),routes},null,2)+'\n');
 console.log('PASS: full OpenAPI ReportGroup/ReportSummary, three report responses, required/types/descriptions/references, nested compositions, oneOf/anyOf, cycles/depth, keyboard, both themes and 1440/390/320px. Evidence: '+directory);
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(async()=>{await browser?.close();server.close();});
