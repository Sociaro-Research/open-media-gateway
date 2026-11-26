// Exact current GatewayKeys component; synthetic loopback API only. Key rows name permissions with the Create form's labels.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',out=path.resolve(process.env.UI_EVIDENCE_DIR||'dist/checks/key-permission-labels-'+engine);fs.mkdirSync(out,{recursive:true});
const org='11111111-1111-4111-8111-111111111111',user='22222222-2222-4222-8222-222222222222';
const key=(id,organization_id,scopes)=>({id,organization_id,user_id:user,created_by:user,name:'Fixture key',prefix:'gw_fixture',scopes,resource_mode:organization_id?'shared':'personal',created_at:'2025-01-01T00:00:00Z',expires_at:null,revoked_at:null});
const keys=[key('33333333-3333-4333-8333-333333333333',org,['generation','keys:manage','organizations:manage']),key('44444444-4444-4444-8444-444444444444','',['generation','providers:manage','storage:manage','keys:manage','system:manage','future:scope'])];
const server=http.createServer((req,res)=>{const u=new URL(req.url,'http://fixture'),json=value=>{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
 if(u.pathname==='/v1/organizations')return json({data:[{id:org,role:'supermanager'}]});
 if(u.pathname===`/v1/organizations/${org}/members`)return json({data:[{id:user,name:'Fixture user',email:'fixture@example.test',role:'supermanager'}]});
 if(u.pathname==='/v1/api-keys/usage')return json({month:u.searchParams.get('month'),data:[]});
 if(u.pathname==='/v1/api-keys')return json({data:keys});
 if(u.pathname==='/'){res.writeHead(200,{'Content-Type':'text/html'});return res.end(`<!doctype html><html data-theme="dark"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"><link rel="stylesheet" href="/docs.css"><body><main class="shell"><section id="configurationView"><div class="configuration-content"><div id="keysContent" class="organization-page"></div></div></section></main><script>var systemAdministrator=true,serviceInfo={console:{}};window.Workspace={isSelf:id=>id===${JSON.stringify(user)}};window.api=async p=>{const r=await fetch(p);return r.json();};window.openKeys=async(scope,policy)=>{serviceInfo.console=policy;await GatewayKeys.open(document.querySelector('#keysContent'),scope);FormUI.refresh();};</script><script src="/user-ui.js"></script><script src="/organization-roles.js"></script><script src="/form-ui.js"></script><script src="/control-pickers.js"></script><script src="/keys.js"></script></body></html>`);}
 const file=path.join(root,path.basename(u.pathname));if(!fs.existsSync(file)){res.writeHead(404);return res.end();}res.setHeader('Content-Type',file.endsWith('.css')?'text/css':'text/javascript');res.end(fs.readFileSync(file));
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await ({chromium,webkit}[engine]).launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:900}}),errors=[];page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(8000);
 const row=()=>page.locator('#keysContent tbody tr').first();
 async function check(name,scope,policy,expected){
  await page.evaluate(([s,p])=>openKeys(s,p),[scope,policy]);await row().waitFor();const text=await row().innerText();
  assert.ok(text.includes(expected),`${name} row should list "${expected}", got: ${text}`);assert.doesNotMatch(text,/\b(providers|storage|keys|organizations|system):manage\b/,name+' row shows a raw scope ID');
  // Every label shown in the row is the Create form's own switch label.
  for(const label of expected.split(', ').filter(l=>l!=='future:scope'))assert.equal(await page.locator('.keys-form fieldset label').filter({hasText:new RegExp('^\\s*'+label+'\\s*$')}).count(),1,label+' is a Create form label');
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:900});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${name} overflows at ${width}`);await page.screenshot({path:path.join(out,`${name}-${theme}-${width}.png`),fullPage:true});}
  await page.setViewportSize({width:1440,height:900});
 }
 try{await page.goto('http://127.0.0.1:'+server.address().port);
  await check('organization',org,{shared_resources:true,personal_providers:false},'Generation and results, Manage API keys, Manage organization');
  // An unknown future scope stays readable as its ID.
  await check('personal','',{shared_resources:false,personal_providers:true},'Generation and results, Manage providers, Manage storage, Manage API keys, Manage system, future:scope');
  assert.deepEqual(errors,[]);console.log('PASS: key rows name permissions like the Create form.');
 }catch(e){process.exitCode=1;console.error(e);}finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
