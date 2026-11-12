// Create template then Back without edits must not leave an empty Untitled template.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=path.resolve(process.env.UI_SOURCE_DIR||'web/public/ui'),output=path.resolve(process.env.UI_EVIDENCE_DIR||`.local/template-create-back/${engine}`);
fs.mkdirSync(output,{recursive:true});
const model={id:'image',display_name:'Fixture image',media_type:'image',supports_image_input:true,max_image_inputs:3,reference_modes:[{id:'references',label:'References',max_images:3}],resolutions:['1024x1024'],ratios:['1:1'],pricing:{known:true,reference_price_usd:0}};
const catalog=[{id:'mock',name:'Mock',models:[model]}];
let records,deletes,failDelete;
function reset(){records=new Map();deletes=[];failDelete=false;}
reset();
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture');let bytes=[];for await(const p of req)bytes.push(p);let body;try{body=JSON.parse(Buffer.concat(bytes).toString())}catch{}
 const json=(data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
 const error=(status,message)=>json({error:{code:'template_unavailable',message}},status);
 const one=url.pathname.match(/^\/v1\/system\/templates\/([^/]+)$/),id=one&&decodeURIComponent(one[1]);
 if(req.method==='POST'&&url.pathname==='/v1/system/templates'){const item={id:body.id,version:1,revision:0,draft:structuredClone(body.content),published:null};records.set(item.id,item);return json({...structuredClone(item),content:item.draft,is_published:false});}
 if(req.method==='DELETE'&&one){deletes.push(body);if(failDelete)return error(503,'Synthetic removal failure.');const item=records.get(id);if(!item)return error(404,'Template is unavailable.');if(body?.version!==item.version)return error(409,'Template changed.');records.delete(id);return json({...structuredClone(item),version:item.version+1,content:item.draft,published:null,is_published:false});}
 if(req.method==='PUT'&&one){const item=records.get(id);if(!item||body?.version!==item.version)return error(409,'Template changed.');item.version++;item.draft=structuredClone(body.content);return json({...structuredClone(item),content:item.draft,is_published:false});}
 if(req.method==='POST'&&url.pathname==='/v1/playground-key/restore')return error(404,'No saved fixture key.');
 if(req.method==='POST'&&(url.pathname.endsWith('/validate-media')||url.pathname==='/v1/generations/estimate'||url.pathname.endsWith('/infer')))return json({valid:true,cost:{expected_usd:0},can_generate:true});
 if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true}});
 if(url.pathname==='/v1/me')return json({id:'owner',session_id:'session-1',email:'owner@example.test',is_system_admin:true});
 if(url.pathname==='/v1/key')return json({key:{name:'Fixture key',resource_mode:'personal'}});
 if(url.pathname==='/v1/providers')return json({data:catalog});
 if(url.pathname==='/v1/system/templates')return json({data:[...records.values()],catalog});
 if(url.pathname==='/v1/templates')return json({data:[]});
 if(one)return records.has(id)?json(records.get(id)):error(404,'Template is unavailable.');
 if(url.pathname.startsWith('/v1/'))return json({data:[]});
 if(url.pathname==='/auth/company-connection')return json({connected:false});
 if(url.pathname.startsWith('/auth/'))return json({setup_required:false});
 const file=path.join(root,['/','/templates'].includes(url.pathname)?'index.html':path.basename(url.pathname));if(!fs.existsSync(file)){res.writeHead(404);return res.end();}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),page=await browser.newPage({viewport:{width:1366,height:900},reducedMotion:'reduce'}),errors=[];
 page.setDefaultTimeout(10000);page.on('pageerror',e=>{if(!e.message.startsWith('ResizeObserver loop'))errors.push(e.message);});
 const editor=()=>page.locator('.template-editor'),create=()=>page.getByRole('button',{name:'Create template',exact:true}),back=()=>editor().getByRole('button',{name:'Back to templates',exact:true});
 const untitled=()=>page.locator('#templatesGallery').getByText('Untitled template');
 async function start(){reset();await page.goto(base);await page.waitForFunction(()=>authState==='authenticated');await page.locator('#apiKey').fill('fixture-key');await page.locator('#apiKey').press('Tab');await page.waitForFunction(()=>GenerationKey.value()==='fixture-key'&&providerCatalog.length>0);await page.locator('#templatesNav').click();await create().waitFor();await create().click();await editor().waitFor();}
 try{
  await start();await back().click();await editor().waitFor({state:'detached'});await create().waitFor();
  assert.deepEqual(deletes,[{version:1}],'untouched draft is removed with its created version');assert.equal(records.size,0);assert.equal(await untitled().count(),0,'no Untitled template card remains');
  await page.screenshot({path:path.join(output,'back-untouched.png')});console.log('PASS untouched Create then Back removes the empty draft');

  await start();await editor().getByLabel('Title',{exact:true}).fill('Kept draft');await back().click();await editor().waitFor({state:'detached'});await create().waitFor();
  assert.deepEqual(deletes,[],'an edited draft is never removed');assert.equal(records.size,1);console.log('PASS edited draft is kept');

  await start();await editor().getByLabel('Title',{exact:true}).fill('Saved once');await editor().getByRole('button',{name:'Save',exact:true}).click();
  for(const deadline=Date.now()+5000;[...records.values()][0]?.version!==2;await new Promise(r=>setTimeout(r,20)))assert.ok(Date.now()<deadline,'fixture did not receive the Save');
  await page.waitForFunction(()=>[...document.querySelectorAll('.template-editor button')].filter(b=>b.textContent.trim()==='Save').every(b=>b.hidden||b.disabled||!b.getClientRects().length));await editor().getByLabel('Title',{exact:true}).fill('');await back().click();await editor().waitFor({state:'detached'});
  assert.deepEqual(deletes,[],'a saved draft is never removed, even when its fields are cleared again');assert.equal(records.size,1);console.log('PASS saved draft is kept');

  await start();failDelete=true;await back().click();await editor().waitFor({state:'detached'});await untitled().first().waitFor();
  assert.equal(deletes.length,1);assert.equal(records.size,1,'failed removal keeps the draft');console.log('PASS failed removal keeps the draft and returns to the gallery');
  assert.deepEqual(errors,[]);
 }catch(error){await page.screenshot({path:path.join(output,'failure.png')}).catch(()=>{});throw error;}
 finally{await browser.close();server.close();}
 console.log('PASS: Create template then Back without edits leaves no Untitled template; edited, saved and failed-removal drafts are kept.');
})().catch(error=>{console.error(error);process.exitCode=1;});
