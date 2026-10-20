// Offline documentation contracts: actual OpenAPI operation ID, no API writes.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const yaml=fs.readFileSync(path.resolve(__dirname,'../../docs/openapi.yaml'),'utf8');
const estimate=yaml.match(/  \/v1\/generations\/estimate:\n([\s\S]*?)(?=\n  \/|\ncomponents:)/)?.[1].match(/operationId: (\S+)/)?.[1];
assert.ok(estimate,'canonical estimate operation missing');
const spec={info:{version:'fixture'},tags:[],components:{schemas:{}},paths:{'/v1/generations/estimate':{post:{operationId:estimate,summary:'Estimate generation',tags:['Generations'],responses:{200:{description:'Estimated'}}}}}};
const server=http.createServer((req,res)=>{
 const url=new URL(req.url,'http://localhost');
 if(url.pathname==='/openapi.json'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify(spec));}
 if(url.pathname==='/site.json'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({name:'Documentation fixture'}));}
 const file=path.join(root,url.pathname==='/docs'?'documentation.html':path.basename(url.pathname));
 if(!fs.existsSync(file))return res.writeHead(404).end();
 res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.html':'text/html','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await(process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium).launch({headless:true,...(process.env.UI_BROWSER_ENGINE!=='webkit'&&process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage();const errors=[],failures=[];page.on('pageerror',e=>errors.push(e.message));
 const origin=`http://127.0.0.1:${server.address().port}`;
 await page.route('**/*',route=>new URL(route.request().url()).origin===origin?route.continue():route.abort());
 try{
  for(const width of [1440,390,320])for(const theme of ['light','dark']){
   await page.setViewportSize({width,height:1000});await page.emulateMedia({colorScheme:theme});
   await page.goto(origin+'/docs#docs/costs');await page.locator('#docsContent h1').waitFor();
   const link=page.getByRole('link',{name:'Estimate endpoint →',exact:true});
   if(await link.getAttribute('href')!==`#docs/${estimate}`)failures.push('estimate link disagrees with OpenAPI');
   await link.focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#docsContent h1')?.textContent!=='Costs & budgets');
   if(await page.locator('#docsContent h1').innerText()!=='Estimate generation')failures.push('estimate navigation renders Page not found');
   await page.goto(origin+'/docs#docs/authentication');await page.getByRole('heading',{name:'Authentication',exact:true}).waitFor();
   const row=await page.locator('#docsContent tr').filter({hasText:'Personal API key'}).innerText();
   if(!/personal workspace/i.test(row)||!/no organization/i.test(row))failures.push('personal key falsely requires organization context');
   if(!/Organization API key/.test(await page.locator('#docsContent').innerText()))failures.push('organization key context not distinguished');
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  }
  assert.deepEqual(errors,[]);assert.deepEqual([...new Set(failures)],[]);
  console.log('PASS: OpenAPI estimate navigation, personal/organization key guidance, keyboard, both themes and 1440/390/320 layouts.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
