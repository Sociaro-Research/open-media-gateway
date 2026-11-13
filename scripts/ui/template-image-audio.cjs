// Image templates have no audio, so their settings summary must not mention it.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=path.resolve(process.env.UI_SOURCE_DIR||'web/public/ui'),output=path.resolve(process.env.UI_EVIDENCE_DIR||`.local/template-image-audio/${engine}`);
fs.mkdirSync(output,{recursive:true});
const model={id:'image',display_name:'Fixture image',media_type:'image',supports_image_input:true,max_image_inputs:3,reference_modes:[{id:'references',label:'References',max_images:3}],resolutions:['1024x1024'],ratios:['1:1'],pricing:{known:true,reference_price_usd:0}};
const catalog=[{id:'mock',name:'Mock',models:[model]}],png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1sAAAAASUVORK5CYII=','base64');
const T='10000000-0000-4000-8000-000000000009',template={title:'Product shot',provider:'mock',model:'image',media_type:'image',prompt:'Use {Image_1}',syntax:'ui-reference-v1',options:{reference_mode:'references',resolution:'1024x1024',ratio:'1:1'},slots:[{id:'image:1',type:'image',index:1,role:'reference',label:'Product'}],cover_id:'cover'};
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture');for await(const _ of req);
 const json=(data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
 if(req.method==='POST'&&url.pathname==='/v1/playground-key/restore')return json({error:{message:'No saved fixture key.'}},404);
 if(req.method==='POST')return json({valid:true,cost:{expected_usd:0,customer_expected_usd:0},can_generate:true});
 if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true}});
 if(url.pathname==='/v1/me')return json({id:'user',session_id:'session-1',email:'user@example.test',is_system_admin:false});
 if(url.pathname==='/v1/key')return json({key:{name:'Fixture key',resource_mode:'personal'}});
 if(url.pathname==='/v1/providers')return json({data:catalog});
 if(url.pathname==='/v1/templates')return json({data:[{id:T,title:template.title,cover_url:'/cover.png',cover_type:'image/png'}]});
 if(url.pathname==='/v1/templates/'+T+'/metadata')return json({id:T,revision:1,template,validation_status:'pending'});
 if(url.pathname==='/v1/templates/'+T)return json({id:T,revision:1,template,validation_status:'ready',catalog,default_available:true});
 if(url.pathname==='/cover.png'||url.pathname.startsWith('/template-media/')){res.writeHead(200,{'Content-Type':'image/png'});return res.end(png);}
 if(url.pathname.startsWith('/v1/'))return json({data:[]});
 if(url.pathname==='/auth/company-connection')return json({connected:false});
 if(url.pathname.startsWith('/auth/'))return json({setup_required:false});
 const file=path.join(root,['/','/templates'].includes(url.pathname)?'index.html':path.basename(url.pathname));if(!fs.existsSync(file)){res.writeHead(404);return res.end();}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),errors=[];
 try{
  for(const width of [1366,390]){
   const page=await browser.newPage({viewport:{width,height:900},reducedMotion:'reduce'});page.setDefaultTimeout(10000);page.on('pageerror',e=>{if(!e.message.startsWith('ResizeObserver loop'))errors.push(e.message);});
   await page.goto(base);await page.waitForFunction(()=>authState==='authenticated');await page.locator('#apiKey').fill('fixture-key');await page.locator('#apiKey').press('Tab');await page.waitForFunction(()=>GenerationKey.value()==='fixture-key'&&providerCatalog.length>0);
   await page.locator('#templatesNav').click();await page.getByRole('button',{name:template.title,exact:true}).click();await page.locator('form.template-use').waitFor();
   await page.waitForFunction(()=>document.querySelector('form.template-use > .field-hint')?.textContent.includes('1:1'));
   const summary=await page.locator('form.template-use > .field-hint').innerText();
   await page.locator('form.template-use').screenshot({path:path.join(output,`image-template-${width}.png`)});
   assert.equal(summary,'1:1 · 1024x1024','image template summary has no audio part');await page.close();
  }
  assert.deepEqual(errors,[]);
 }finally{await browser.close();server.close();}
 console.log('PASS: image template settings summary omits audio at desktop and phone width.');
})().catch(error=>{console.error(error);process.exitCode=1;});
