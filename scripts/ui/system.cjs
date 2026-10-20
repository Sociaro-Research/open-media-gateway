const {evidencePath} = require('./evidence.cjs');
// Verify administrator navigation, changes and ordinary-user restrictions without external calls.
const {chromium}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const name=req.url.split('?')[0].replace(/^\/ui\//,'');const file=path.join(root,name.includes('.')?name:'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];page.on('pageerror',e=>errors.push(e.message));let admin=true,settings={allow_inline_references:true,generation_ui:true,personal_providers:true,version:1},roles=[];
 await page.route('**/v1/**',route=>{const req=route.request(),url=new URL(req.url()),json=data=>route.fulfill({json:data});
 if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock',console:settings});
 if(url.pathname==='/v1/me')return json({id:'self',name:'Administrator',email:'admin@example.test',is_system_admin:admin});
 if(url.pathname==='/v1/admin/providers')return json({data:[{id:'bytedance',name:'BytePlus ModelArk'},{id:'alibaba',name:'Alibaba Cloud'}].map(p=>({...p,models:[],status:'not_configured'}))});
 if(url.pathname==='/v1/system/settings'){if(req.method()==='PUT'){settings={...req.postDataJSON(),version:settings.version+1};}return json(settings);}
 if(url.pathname==='/v1/sales/bonuses')return json({settings:{manager_percent:5,supervisor_percent:1,version:1},people:[]});
 if(url.pathname==='/v1/system/markups/catalog')return json({data:[]});
 if(url.pathname==='/v1/system/markups'){if(req.method()==='PUT'){if(req.postDataJSON().version!==settings.version)return route.fulfill({status:409,json:{error:{message:'Stale settings'}}});settings.markup_percent=req.postDataJSON().default_percent;settings.version++;}return json({system:{default_percent:settings.markup_percent??0,exceptions:[],version:settings.version},company:{default_percent:null,exceptions:[],version:0}})}
 if(url.pathname==='/v1/system/email')return json({provider:'',from:'',key_configured:false,enabled:false,version:1,available_providers:[{id:'resend',name:'Resend'}]});
 if(url.pathname==='/v1/system/users')return json({data:[{id:'other',name:'Other user',email:'other@example.test',is_system_admin:roles.at(-1)?.role==='system_admin',is_client_manager:roles.at(-1)?.role==='client_manager'}],has_more:false});
 if(url.pathname.endsWith('/role')){roles.push(req.postDataJSON());return route.fulfill({status:204});}
 if(url.pathname==='/v1/audit/events')return json({data:[{id:1,created_at:new Date().toISOString(),actor_name:'Server command',actor_type:'service',object_type:'user',object_id:'self',outcome:'success',action:'administrator_role',details:{email:'admin@example.test',after:true}}],has_more:false});
 return json({data:[]});
 });
 async function capture(name){fs.mkdirSync(evidencePath('/tmp/gateway-layout'),{recursive:true});for(const width of [1440,390]){await page.setViewportSize({width,height:1000});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),name+' overflow at '+width);await page.screenshot({path:evidencePath('/tmp/gateway-layout/')+name+'-'+width+'.png',fullPage:true});}await page.setViewportSize({width:1440,height:1000});}
 try{await page.goto(`http://127.0.0.1:${server.address().port}/system`);await page.getByRole('heading',{name:'Settings',exact:true}).waitFor();assert.equal(await page.locator('#systemNav').isVisible(),true);
 assert.equal(await page.locator('#sectionNav #systemNav').innerText(),'Administration');
 assert.equal(await page.locator('#systemNav').getAttribute('aria-current'),'page');
 await page.goto(`http://127.0.0.1:${server.address().port}/configuration`);await page.locator('#systemNav').click();await page.getByRole('heading',{name:'Settings',exact:true}).waitFor();
 assert.equal(await page.locator('#settings-panel-system').getByText('Available providers',{exact:true}).count(),0);
 assert.equal(await page.getByRole('button',{name:'Copy my connections to shared'}).count(),0);
 const inline=page.getByRole('switch',{name:'Allow reference files in generation requests'});
 assert.equal(await inline.isChecked(),true);
 for(const enabled of [false,true]){
  await inline.setChecked(enabled);await page.getByRole('button',{name:'Save',exact:true}).click();await page.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(settings.allow_inline_references,enabled);
  await page.reload();await inline.waitFor();assert.equal(await inline.isChecked(),enabled);
 }
 await page.getByRole('tab',{name:'Markups',exact:true}).click();await page.getByLabel('Default markup (%)',{exact:true}).fill('25.5');
 // Saving the main form must not mark an unsaved pricing draft as saved.
 await page.getByRole('tab',{name:'System',exact:true}).click();await inline.setChecked(false);
 await Promise.all([page.waitForResponse(r=>r.url().endsWith('/v1/system/settings')&&r.request().method()==='PUT'),page.locator('.system-settings').evaluate(f=>f.requestSubmit())]);
 await page.getByRole('tab',{name:'Markups',exact:true}).click();
 assert.equal(await page.locator('.markup-settings').getByRole('button',{name:'Save',exact:true}).isVisible(),true);
 await page.locator('.markup-settings').getByRole('button',{name:'Save',exact:true}).click();await page.locator('.markup-settings').getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(settings.markup_percent,25.5);await page.reload();await page.getByRole('tab',{name:'Markups',exact:true}).click();await page.getByLabel('Default markup (%)',{exact:true}).waitFor();assert.equal(await page.getByLabel('Default markup (%)',{exact:true}).inputValue(),'25.5');
 for(const width of [1440,1024,768,390,320]){
  await page.setViewportSize({width,height:1000});
  await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth);
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'header overflow at '+width);
  const brand=await page.locator('.brand').first().boundingBox(),nav=await page.locator('#sectionNav').boundingBox();
  assert.ok(brand.x+brand.width<=nav.x||brand.y+brand.height<=nav.y,'header overlaps at '+width);
  fs.mkdirSync(evidencePath('/tmp/gateway-administration-nav'),{recursive:true});await page.screenshot({path:evidencePath(`/tmp/gateway-administration-nav/${width}.png`)});
 }
 await page.setViewportSize({width:1440,height:1000});
 await page.getByRole('tab',{name:'System',exact:true}).click();await page.locator('input[name=generation]').uncheck();await page.locator('input[name=providers]').uncheck();await page.getByRole('button',{name:'Save',exact:true}).click();await page.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(settings.generation_ui,false);assert.equal(await page.locator('#generationNav').isVisible(),false);
 await page.getByRole('button',{name:'Users',exact:true}).click();await page.getByRole('heading',{name:'Users',exact:true}).waitFor();
 const role=()=>page.getByRole('combobox',{name:'Role for other@example.test'});
 await role().waitFor();assert.equal(await page.getByText('Administrators only',{exact:true}).count(),0);assert.equal(await page.getByRole('button',{name:'Apply',exact:true}).isVisible(),false);
 for(const value of ['system_admin','user','client_manager','user']){await page.locator('.role-editor select').selectOption(value,{force:true});await page.getByRole('button',{name:'Apply',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.role-editor > button')?.hidden===true);assert.equal(roles.at(-1).role,value);}
 await capture('users');
 await page.getByRole('button',{name:'Audit Log',exact:true}).click();await page.getByText('Administrator granted',{exact:true}).waitFor();await capture('activity-log');
 await page.getByRole('button',{name:'Settings',exact:true}).click();await page.getByRole('heading',{name:'Settings',exact:true}).waitFor();await page.getByRole('tab',{name:'System',exact:true}).click();await page.locator('.system-settings').waitFor();fs.mkdirSync(evidencePath('/tmp/gateway-system-ui'),{recursive:true});await page.screenshot({path:evidencePath('/tmp/gateway-system-ui/settings-desktop.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:evidencePath('/tmp/gateway-system-ui/settings-mobile.png'),fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'mobile overflow');
 admin=false;await page.reload();await page.locator('#organizationsView').waitFor();assert.equal(await page.locator('#systemNav').isVisible(),false);assert.equal(await page.locator('#generationNav').isVisible(),false);assert.equal(await page.locator('#providersNav').isVisible(),false);assert.ok(page.url().endsWith('/organizations'));settings.personal_providers=true;await page.setViewportSize({width:1440,height:1000});await page.goto(`http://127.0.0.1:${server.address().port}/profile`);await page.getByRole('tab',{name:'Providers',exact:true}).click();await page.locator('.provider-list-item').nth(1).waitFor();assert.equal(await page.locator('.provider-list-item').count(),2);
 assert.deepEqual(errors,[]);console.log('PASS: admin deep link, settings, grants, revocation, audit, mobile and non-admin restrictions.');
 }finally{await browser.close();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
