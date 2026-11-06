// Real console renderers, synthetic loopback APIs. No business writes are allowed.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const {generationFixtureResponse}=require('./generation-page-fixture.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=process.env.UI_SOURCE_DIR||path.resolve(__dirname,'../../web/public/ui'),out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/table-controls/renderers-'+engine),baseline=!fs.existsSync(path.join(root,'table-ui.js'));
fs.mkdirSync(out,{recursive:true});
const hashes=()=>Object.fromEntries(fs.readdirSync(root).filter(n=>/\.(js|css|html)$/.test(n)).sort().map(n=>[n,crypto.createHash('sha256').update(fs.readFileSync(path.join(root,n))).digest('hex')]));const sourceStart=hashes();const fixtureHash=crypto.createHash('sha256').update(fs.readFileSync(__filename)).digest('hex');
const uid='11111111-1111-4111-8111-111111111111',oid='22222222-2222-4222-8222-222222222222',kid='33333333-3333-4333-8333-333333333333',date='2026-11-06T10:00:00Z';
let scenario='',empty=false,catalogRevision=1;const user={id:uid,name:'Fixture viewer',email:'viewer@example.test',theme:'dark',session_id:'fixture',is_system_admin:true,report_columns:['date','user','model','status','cost'],table_columns:{},table_wide:{}};
const company={id:oid,name:'Fixture company',role:'supermanager',balance_usd:100,available_usd:100,credit_limit_usd:0,default_weekly_limit_usd:0,storage_bytes:2048};
const provider={id:'openai--fixture',name:'Fixture connection',api_type:'openai',configured:true,version:1,default_timeout_seconds:30,models:[{id:'fixture-model',display_name:'Fixture model',media_type:'image'}],fields:[],connection:{timeout_seconds:30,manual_catalog:true,endpoints:{},extra_body:{},headers:[{name:'X-Fixture',value:'Original header',operation:''}],models:[{model:{id:'fixture-model',display_name:'Fixture model'},profile:'fixture-profile',price_usd:0.01,extra_body:{}}]}};
const summary={requests:1,successful:1,failed:0,priced_requests:1,average_cost_usd:1,tokens:10,token_requests:1,periods:[],public_models:[],provider_models:[],providers:[{name:'Fixture provider',spend_usd:1,requests:1,priced_requests:1,successful:1,failed:0,tokens:10,token_requests:1}]};
const server=http.createServer((req,res)=>{let file=path.join(root,path.basename(req.url.split('?')[0]));if(!path.extname(file))file=path.join(root,'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
const cases=['requests','organizations','client_companies','organization_members','organization_members_readonly','organization_invitations','catalog_sources','catalog_runs','catalog_warnings','audit_events','audit_revoked_keys','payment_ledger','personal_keys','organization_keys','provider_models','usage_providers','company_invitations','receiving_wallets','payment_requests','provider_headers','provider_manual_models','vat_rules','organization_roles'];
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000},hasTouch:true,reducedMotion:'reduce'});page.setDefaultTimeout(10000);
 const errors=[],calls=[],writes=[],metrics=[];page.on('pageerror',e=>errors.push({scenario,message:e.message}));
 await page.route('**/*',async route=>{
  const req=route.request(),url=new URL(req.url()),p=url.pathname,method=req.method();if(url.origin!==base)return route.abort();
  const json=value=>route.fulfill({json:generationFixtureResponse(req,value)}),rows=value=>({data:empty?[]:value,has_more:false});
  if(p==='/auth/company-connection')return route.fulfill({status:404,json:{}});if(p==='/auth/methods')return json({email:true,google:false});if(!p.startsWith('/v1/'))return route.continue();calls.push({scenario,p,method});
  if(p==='/v1/info')return json({auth_mode:'google',console:{generation_ui:true,personal_providers:true,shared_resources:scenario!=='personal_keys',version:1}});
  if(p==='/v1/me'){
   if(method==='PATCH'){const patch=req.postDataJSON();writes.push(patch);for(const [k,v]of Object.entries(patch))if(k==='table_columns'||k==='table_wide')user[k]={...user[k],...v};else user[k]=v;}
   return json(user);
  }
  if(p==='/v1/playground-key/restore')return route.fulfill({status:404,json:{error:{code:'playground_key_unavailable',message:'No synthetic generation key'}}});
  if(method!=='GET'){errors.push({scenario,message:'Unexpected business mutation '+method+' '+p});return route.fulfill({status:405,json:{error:{message:'Business writes disabled in this synthetic fixture'}}});}
  if(p==='/v1/organizations')return json({as_of:date,...rows([{...company,usage:{spend_usd:5,unknown_costs:0,last_activity_at:date}}])});
  if(p==='/v1/organizations/'+oid)return json(company);
  if(p.endsWith('/members'))return json(rows([{id:uid,email:user.email,name:'Fixture member',role:'member',weekly_limit_usd:20,weekly_committed_usd:5,total_consumed_usd:10}]));
  if(p.endsWith('/invitations'))return json(rows([{id:kid,role:'member',expires_at:'2027-01-01T00:00:00Z',created_at:date}]));
  if(p==='/v1/client-onboardings')return json(rows([{organization_id:oid,name:company.name,email:'owner@example.test',status:'sent',version:1,expires_at:'2027-01-01T00:00:00Z'}]));
  if(p.endsWith('/balance-history'))return json(rows([{id:kid,kind:'promo',amount_usd:10,created_at:date,reason:'Fixture credit'}]));
  if(p.endsWith('/payment-methods'))return json({data:[]});
  if(p.endsWith('/payments'))return json(rows([{id:kid,created_at:date,status:'pending',settled:false,amount_cents:1000,stablecoin:{amount:'10.000000',token:'USDC'}}]));
  if(p==='/v1/api-keys')return json(rows([{id:kid,user_id:uid,organization_id:url.searchParams.has('organization_id')?oid:'',name:'Fixture key',prefix:'sk_fixture',resource_mode:url.searchParams.has('organization_id')?'shared':'personal',storage_mode:'shared',scopes:['generation'],created_at:date,created_by:uid,user_email:user.email,creator_email:user.email}]));
  if(p==='/v1/api-keys/usage')return json({month:url.searchParams.get('month'),data:[]});
  if(p==='/v1/providers')return json({data:[]});
  if(p==='/v1/admin/providers/types')return json({data:[{id:'openai',name:'OpenAI compatible',profiles:[{id:'fixture-profile',display_name:'Fixture profile',media_type:'image'}]}]});
  if(p==='/v1/admin/providers')return json({data:[provider]});if(p==='/v1/admin/providers/'+provider.id)return json(provider);
  if(p==='/v1/system/model-settings')return json(rows([{id:kid,provider:provider.id,model:'fixture-model',name:'Fixture model',public_id:'fixture-model',enabled_by_default:true,version:1}]));
  if(p.endsWith('/reports'))return json(url.searchParams.get('view')==='summary'?summary:rows([{id:kid,created_at:date,organization_id:oid,organization_name:company.name,user_id:uid,user_email:user.email,api_key_name:'Fixture key',api_key_id:kid,model:'mock/image',provider:'mock',status:'succeeded',consumed_usd:1,cost:{metered_usd:1},usage:{metrics:[]}}]));
  if(p==='/v1/audit/events')return json(rows([{id:1,created_at:date,scope:'organization',actor_id:uid,actor_name:'Fixture viewer',organization_id:oid,organization_name:company.name,action:'settings',object_type:'organization',object_id:oid,object_name:company.name,outcome:'success',details:{before:{name:'Previous'},after:{name:company.name}}}]));
  if(p==='/v1/audit/revoked-keys')return json(rows([{id:kid,organization_id:oid,revoked_at:date,snapshot:{name:'Fixture revoked key',prefix:'fixture',owner_id:uid,owner_email:user.email,revoker_email:user.email,history_complete:true,spent_micros:0},current_spent_micros:0,current_pending_requests:0,current_unknown_cost_requests:0}]));
  if(p==='/v1/system/catalog/settings')return json({timezone:'UTC',email:'fixture@example.test',version:1});
  if(p==='/v1/system/catalog/schedules')return json({data:[{provider:'openai',enabled:true,time:'08:00',version:1,sources:[{id:'fixture',url:'https://fixture.invalid/catalog',parser:'document'}]}]});
  if(p==='/v1/system/catalog/runs')return json(rows([{id:'run-fixture',completed_at:date,provider:'openai',status:'completed',version:catalogRevision,changes:[],open_warnings:[],email_status:'not_needed'}]));
  if(p==='/v1/system/catalog/providers/openai')return json({warnings:[{key:'warning-fixture',model:'fixture-model',source:'fixture',reason:'Synthetic warning',first_seen:date,last_seen:date,occurrences:1}]});
  if(p==='/v1/system/billing')return json({version:1,seller:{legal_name:'Fixture company',email:'billing@example.test',address:{country:'GB',line1:'1 Test Street',city:'London'},vat_registered:false},rules:[{country:'domestic',status:'any',treatment:'standard',rate_percent:20}]});
  if(p.endsWith('/storage/usage'))return json({used_bytes:0,limit_bytes:null,applies:true});
  if(p.endsWith('/sales-permissions'))return json({can_view_all_requests:true});
  return json({data:[]});
 });
 async function mount(id,openAdvanced=true){
  scenario=id;empty=false;await page.goto(base+'/profile');await page.waitForFunction(()=>consoleInitialized&&Workspace.ready());
  await page.evaluate(async({id,company,provider,oid,openAdvanced})=>{
   GatewayOrganizations.close();GatewayKeys.close();GatewayReports.close();ModelInfo.close();
   const host=document.querySelector('#profileContent');GatewayLayout.clear(host);host.replaceChildren();host.className='configuration-content';document.querySelector('#configurationView').hidden=false;
   const current=()=>host.isConnected;window.tableQA={host,current};window.systemAdministrator=true;systemAdministrator=true;clientManager=false;
   if(id==='requests')await GatewayReports.open(false,{personal:true,parent:host});
   else if(id==='organizations')await GatewayOrganizations.open(host,true,true);
   else if(id==='client_companies'){systemAdministrator=false;clientManager=true;await GatewayOrganizations.open(host,false,true);}
   else if(id==='organization_members'||id==='organization_invitations')await GatewayOrganizations.details(host,company,false,'Members');
   else if(id==='organization_members_readonly'){systemAdministrator=false;clientManager=true;await GatewayOrganizations.details(host,{...company,role:'client_manager'},false,'Members');}
   else if(id==='catalog_sources')await CatalogSync.provider(host,'openai',api,current);
   else if(id==='catalog_runs'||id==='catalog_warnings')await CatalogSync.journal(host,api,current);
   else if(id==='audit_events'||id==='audit_revoked_keys')await AuditLog.mount(host,api,{current});
   else if(id==='payment_ledger')await BalanceHistory.mount(host,api,company,current,()=>{},true);
   else if(id==='personal_keys'||id==='organization_keys')await GatewayKeys.open(host,id==='personal_keys'?'':oid);
   else if(id==='provider_models')await ModelAccess.provider(host,provider,api,{current});
   else if(id==='usage_providers'){const summary=ReportSummary.mount(host);await summary.load('/v1/system/reports?',new URLSearchParams({from:'2026-11-01',to:'2026-11-06'}));}
   else if(id==='company_invitations')await ClientOnboarding.mount(host,{current,refresh:()=>{}});
   else if(id==='receiving_wallets')host.append(StablecoinUI.providerEditor({id:oid,kind:'stablecoin',name:'Fixture stablecoins',enabled:true,version:1,stablecoin:{wallets:[{network:'ethereum',address:'0x1111111111111111111111111111111111111111',tokens:['USDT','USDC']}]}},{api,current,providers:[],onSaved(){},onCancel(){}}));
   else if(id==='payment_requests')await StablecoinUI.requests(host,{api,base:'/v1/organizations/'+oid,current,onView(){}});
   else if(id==='provider_headers'||id==='provider_manual_models'){const view=document.querySelector('#providersView');host.append(view);view.hidden=false;view.classList.add('personal-section');providerAdminCatalog=[provider];selectedAdminProviderID=provider.id;renderProviderDetail(provider);if(openAdvanced)view.querySelector('.provider-connection-settings details')?.setAttribute('open','');}
   else if(id==='vat_rules')await VATSettingsUI.mount(host,api,current);
   else if(id==='organization_roles')host.append(OrganizationRoles.heading());
  },{id,company,provider,oid,openAdvanced});
  if(id==='audit_revoked_keys')await page.getByRole('tab',{name:'Revoked keys',exact:true}).click();
  if(id==='catalog_warnings'){await page.locator('#profileContent').getByRole('combobox',{name:'Provider',exact:true}).click();await page.getByRole('option',{name:'openai',exact:true}).click();await page.locator('#profileContent').getByRole('combobox',{name:'View',exact:true}).click();await page.getByRole('option',{name:'Open warnings',exact:true}).click();}
  if(id==='organization_roles'){const info=page.getByRole('button',{name:'Organization role permissions',exact:true});await info.focus();await info.press('F2');}
  const stable=id==='organization_members_readonly'?'organization_members':id;
  let table=page.locator('[data-console-table="'+stable+'"]');
  if(baseline){const candidates=page.locator('#profileContent table,.model-info-popup table');const first={organization_invitations:'Role',organization_members:'Member',organization_members_readonly:'Member',provider_manual_models:'Model',provider_headers:'Header'}[id];table=first?candidates.filter({has:page.locator('thead th:first-child',{hasText:first})}):candidates.first();await table.first().waitFor();await table.first().evaluate((t,id)=>t.dataset.fixtureTable=id,stable);table=page.locator('[data-fixture-table="'+stable+'"]');}
  await table.first().waitFor({state:openAdvanced?'visible':'attached'});return table.first();
 }
 async function expose(table){if(await table.evaluate(t=>!!t.closest('.model-info-popup')))await table.evaluate(t=>{t.closest('.model-info-popup').scrollTop=0;});else await table.locator('thead').scrollIntoViewIfNeeded();}
 const selected=process.env.UI_TABLE_CASES?process.env.UI_TABLE_CASES.split(','):cases;
 try{
 if(!baseline&&selected.includes('provider_headers')){
  user.table_wide.provider_headers=true;const hidden=await mount('provider_headers',false);assert.equal(await hidden.isVisible(),false);await hidden.locator('.table-controls').waitFor({state:'attached'});assert.equal(await hidden.locator('button[aria-label="Restore normal table width"]').count(),1);
  await page.locator('.provider-connection-settings details > summary').click();await page.waitForFunction(()=>!!document.querySelector('[data-console-table=provider_headers]').parentElement.style.width);await hidden.getByRole('button',{name:'Columns for provider headers',exact:true}).click();await page.getByRole('dialog',{name:'Columns for provider headers',exact:true}).waitFor();await hidden.evaluate(t=>t.closest('details').open=false);await page.getByRole('dialog',{name:'Columns for provider headers',exact:true}).waitFor({state:'detached'});user.table_wide.provider_headers=false;console.log('PASS disclosure persisted-wide and close cleanup');
 }
 for(const id of selected){
  const table=await mount(id),stable=id==='organization_members_readonly'?'organization_members':id;await expose(table);
  if(!baseline){await table.locator('.table-controls').waitFor();if(id==='organization_roles'){await table.locator('.table-controls button').first().click();await page.getByRole('dialog',{name:'Columns for organization roles',exact:true}).waitFor();await page.keyboard.press('Escape');assert.equal(await page.getByRole('dialog',{name:'Organization role permissions',exact:true}).isVisible(),true);}assert.ok((await table.locator('thead th').count())>3);
   const editable=['organization_members','catalog_sources','provider_models','receiving_wallets','provider_headers','vat_rules'].includes(id);
   if(editable)await table.evaluate(t=>{for(const n of t.querySelectorAll('input:not(:disabled),select:not(:disabled),textarea:not(:disabled)')){if(n.type==='checkbox')n.checked=!n.checked;else if(n.tagName==='SELECT'){if(n.options.length>1)n.selectedIndex=(n.selectedIndex+1)%n.options.length;}else if(n.type==='number')n.value=String((Number(n.value)||0)+1);else if(n.type==='url')n.value='https://draft.example.test/catalog';else n.value=n.value+'-draft';n.dispatchEvent(new Event('input',{bubbles:true}));n.dispatchEvent(new Event('change',{bubbles:true}));}});
   const formSnapshots=await table.evaluate(t=>[...new Set([...t.querySelectorAll('input,select,textarea')].map(n=>n.form).filter(Boolean))].map(f=>FormUI.snapshot(f)));
   const before=await table.evaluate(t=>{const fields=[...t.querySelectorAll('input,select,textarea')];window.tableQANodes=fields;return fields.map(n=>[n.name,n.value,n.checked,n.disabled,n.form?.id||'']);});
   const select=page.locator('body > select[data-table-columns="'+stable+'"]');const options=await select.locator('option').evaluateAll(nodes=>nodes.map(o=>o.value));assert.ok(options.length>1);
   const original=await select.evaluate(s=>[...s.selectedOptions].map(o=>o.value));const changed=original.length>1?original.slice(0,-1):options.filter(c=>!original.includes(c)).slice(0,1);await select.selectOption(changed,{force:true});await page.waitForFunction(id=>!document.querySelector('body > select[data-table-columns="'+id+'"]').disabled,stable);
   assert.deepEqual(await table.evaluate(t=>[...t.querySelectorAll('input,select,textarea')].map(n=>[n.name,n.value,n.checked,n.disabled,n.form?.id||''])),before);assert.equal(await table.evaluate(t=>window.tableQANodes.every((n,i)=>n===[...t.querySelectorAll('input,select,textarea')][i])),true);
   assert.equal(await table.locator('select[data-table-columns]').count(),0);assert.equal(await table.evaluate(t=>[...new Set([...t.querySelectorAll('input,select,textarea')].map(n=>n.form).filter(Boolean))].every(form=>![...form.elements].some(e=>e.matches('[data-table-columns]')))),true);
   assert.ok(user.table_columns[stable]||stable==='requests');
   assert.deepEqual(await table.evaluate(t=>[...new Set([...t.querySelectorAll('input,select,textarea')].map(n=>n.form).filter(Boolean))].map(f=>FormUI.snapshot(f))),formSnapshots,'chooser preserves dirty resource snapshots');
   if(editable){
    const field=table.locator('input:not(:disabled),select:not(:disabled),textarea:not(:disabled)').first();const column=await field.evaluate(n=>n.closest('td').dataset.column);const without=options.filter(c=>c!==column);await select.selectOption(without,{force:true});await page.waitForFunction(id=>!document.querySelector('body > select[data-table-columns="'+id+'"]').disabled,stable);assert.equal(await field.isVisible(),false,'editable column hidden');
    const prefs=JSON.stringify(user.table_columns[stable]);await table.locator('..').locator('..').getByRole('button',{name:'Show hidden fields',exact:true}).first().click();assert.equal(await field.isVisible(),true);assert.equal(JSON.stringify(user.table_columns[stable]),prefs,'recovery does not overwrite preference');
    await select.selectOption(without,{force:true});await page.waitForFunction(id=>!document.querySelector('body > select[data-table-columns="'+id+'"]').disabled,stable);await field.evaluate(n=>{n.setCustomValidity('Synthetic invalid draft');n.reportValidity();});assert.equal(await field.isVisible(),true,'invalid hidden field revealed');await field.evaluate(n=>n.setCustomValidity(''));
   }
   await select.selectOption(original,{force:true});await page.waitForFunction(id=>!document.querySelector('body > select[data-table-columns="'+id+'"]').disabled,stable);
  }
  if(!baseline&&id==='catalog_runs'){
   await table.getByRole('button',{name:'Expand table to viewport width',exact:true}).click();await page.waitForFunction(()=>!document.querySelector('body > select[data-table-columns=catalog_runs]').disabled);await table.locator('.table-controls button').first().click();const popup=page.getByRole('dialog',{name:'Columns for catalog runs',exact:true});await popup.waitFor();catalogRevision=2;await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.waitForFunction(()=>document.querySelector('[data-console-table=catalog_runs] tbody tr')?.children[3]?.textContent==='2');assert.equal(await popup.isVisible(),true,'background read keeps column menu open');assert.equal(await table.getByRole('button',{name:'Restore normal table width',exact:true}).isVisible(),true);await page.keyboard.press('Escape');await table.getByRole('button',{name:'Restore normal table width',exact:true}).click();await page.waitForFunction(()=>!document.querySelector('body > select[data-table-columns=catalog_runs]').disabled);
  }
  for(const theme of ['dark','light'])for(const width of [1440,2560,1024,768,600,390,320]){
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>GatewayProfile.identify({theme:t}),theme);await expose(table);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const normal=await table.evaluate(t=>({wrap:t.parentElement.getBoundingClientRect().toJSON(),scroll:scrollY,overflow:document.documentElement.scrollWidth>innerWidth,theme:document.documentElement.dataset.theme}));assert.equal(normal.overflow,false,id+' normal page overflow '+width);assert.equal(normal.theme,theme);await page.screenshot({path:path.join(out,id+'-'+theme+'-'+width+'-normal.png')});if(!baseline){await table.locator('.table-controls button').first().click();const popup=page.getByRole('dialog',{name:stable==='requests'?'Columns':'Columns for '+stable.replaceAll('_',' '),exact:true});await popup.waitFor();const b=await popup.boundingBox();assert.ok(b.x>=11&&b.x+b.width<=width-11&&b.y>=0&&b.y+b.height<=1001,id+' chooser bounds');await page.keyboard.press('Escape');}
   if(!baseline){await table.getByRole('button',{name:'Expand table to viewport width',exact:true}).click();await page.waitForFunction(id=>!document.querySelector('body > select[data-table-columns="'+id+'"]').disabled,stable);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));const wide=await table.evaluate(t=>({wrap:t.parentElement.getBoundingClientRect().toJSON(),scroll:scrollY,overflow:document.documentElement.scrollWidth>innerWidth,theme:document.documentElement.dataset.theme}));assert.equal(wide.overflow,false,id+' wide page overflow '+width);assert.equal(wide.theme,theme);assert.equal(await page.evaluate(()=>document.fullscreenElement),null);if(width===2560)assert.ok(wide.wrap.width>normal.wrap.width+500,id+' wide bypasses content cap');assert.ok(wide.wrap.x>=0&&wide.wrap.right<=width+1,id+' viewport bounds');metrics.push({id,stable,engine,theme,width,normal,wide});await page.screenshot({path:path.join(out,id+'-'+theme+'-'+width+'-wide.png')});await table.getByRole('button',{name:'Restore normal table width',exact:true}).click();await page.waitForFunction(id=>!document.querySelector('body > select[data-table-columns="'+id+'"]').disabled,stable);}
  }
  console.log('PASS renderer',id);fs.writeFileSync(path.join(out,'matrix.json'),JSON.stringify({metrics,writes,errors,calls,sourceStart,sourceEnd:hashes(),fixtureHash},null,2));
 }}catch(error){console.error('FAILED CASE',scenario,'last routes',calls.slice(-12));await page.screenshot({path:path.join(out,'failure-'+scenario+'.png')});throw error;}finally{await browser.close();server.close();}
 assert.deepEqual(hashes(),sourceStart,'immutable UI source during run');assert.deepEqual(errors.filter(e=>!e.message.startsWith('ResizeObserver loop')),[]);console.log('PASS renderer inventory',selected.length,engine,baseline?'baseline':'current');
})().catch(error=>{console.error(error);server.close();process.exitCode=1;});
