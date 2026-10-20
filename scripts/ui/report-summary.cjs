const {evidencePath} = require('./evidence.cjs');
// Isolated report dashboard checks; no production or provider requests.
const {chromium}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const filename=path.basename(req.url.split('?')[0]);const file=path.join(root,filename.includes('.')?filename:'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH}),page=await browser.newPage({viewport:{width:1440,height:1100}});page.setDefaultTimeout(8000);
 const org='11111111-1111-4111-8111-111111111111',key='22222222-2222-4222-8222-222222222222',requests=[],errors=[];let empty=false,fail=false;
 page.on('pageerror',e=>errors.push(e.message));
 const metrics={profit_usd:450,pending_profit_requests:2,recognized_profit_requests:149,requests:153,successful:150,failed:1,spend_usd:2876.31,priced_requests:151,tokens:6095160,token_requests:120};
 const summary=interval=>({...metrics,average_cost_usd:19.04841,interval,periods:Array.from({length:interval==='day'?8:interval==='week'?2:1},(_,i)=>({...metrics,from:'2026-09-'+String(14+i).padStart(2,'0'),to:'2026-09-'+String(interval==='day'?14+i:21).padStart(2,'0'),profit_usd:i===0?-5:65,spend_usd:[221,310,493,421,1120,108,199,4.31][i]})),public_models:Array.from({length:51},(_,i)=>({...metrics,name:'public/model-'+i,spend_usd:2800/(i+1)})),provider_models:[{...metrics,name:'vendor-original',provider:'bytedance'}],providers:[{...metrics,name:'bytedance'},{...metrics,name:'alibaba',spend_usd:63.14},{...metrics,name:'mock',spend_usd:0,priced_requests:153},{...metrics,name:'',spend_usd:1}]});
 await page.route('**/v1/**',async route=>{const url=new URL(route.request().url()),p=url.pathname,json=value=>route.fulfill({json:value});
  if(p==='/v1/info')return json({auth_mode:'google',console:{generation_ui:true,personal_providers:false,shared_resources:true,version:1}});
  if(p==='/v1/me')return json({id:'33333333-3333-4333-8333-333333333333',email:'tester@example.test',name:'Tester',theme:'light',is_system_admin:true,session_id:'fixture'});
  if(p==='/v1/organizations')return json({data:[{id:org,name:'Sunday Games',role:'admin'}]});
  if(p==='/v1/system/settings')return json({generation_ui:true,shared_resources:true,version:1});
  if(p.endsWith('/limits'))return json({system:{values:{},version:1}});
  if(p.endsWith('/reports')){
   requests.push(url);
   if(url.searchParams.get('view')==='summary'){
    if(fail)return route.fulfill({status:503,json:{error:{message:'Temporarily unavailable'}}});
    if(empty)return json({requests:0,successful:0,failed:0,spend_usd:0,priced_requests:0,tokens:0,token_requests:0,average_cost_usd:null,interval:url.searchParams.get('interval'),periods:[],public_models:[],provider_models:[],providers:[]});
    return json(summary(url.searchParams.get('interval')));
   }
   return json({data:empty?[]:[{id:'task',created_at:'2026-09-16T12:00:00Z',provider:'bytedance',model:'vendor-original',public_model:'public/model-0',status:'succeeded',profit_status:'recognized',profit_usd:0.5,consumed_usd:5,api_key_name:'Test key',organization_name:'Sunday Games'}],has_more:!empty});
  }
  return json({data:[]});
 });
 const base='http://127.0.0.1:'+server.address().port;
 const ready=()=>page.locator('.report-metric').first().waitFor();
 async function capture(name){for(const width of [1440,390,320]){await page.setViewportSize({width,height:1100});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'overflow '+width+' '+JSON.stringify(await page.evaluate(()=>[...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right>innerWidth+1&&!e.closest('.docs-table-wrap,.report-spend-scroll')).map(e=>[e.tagName,e.className,e.getBoundingClientRect().width]).slice(0,12))));await page.screenshot({path:evidencePath('/tmp/gateway-report-')+name+'-'+width+'.png',fullPage:true});}}
 try{
  await page.goto(base+'/system');await page.locator('[data-system-page=usage]').click();await ready();assert.equal(await page.locator('.report-metric').filter({hasText:'Total requests'}).locator('strong').innerText(),'153');assert.equal(await page.locator('.report-model-bars li').count(),5);
  const filters=await page.locator('.report-filters').boundingBox(),summaryBox=await page.locator('.report-summary').boundingBox();assert.ok(filters.y<summaryBox.y,'Usage filters precede charts');assert.equal(await page.locator('.report-table').count(),0);assert.equal(requests.filter(u=>u.searchParams.get('view')!=='summary').length,0,'Usage fetched request rows');
  await page.getByRole('group',{name:'Chart metric'}).getByRole('button',{name:'Profit',exact:true}).click();await page.getByRole('heading',{name:'Daily Profit',exact:true}).waitFor();assert.match(await page.locator('.report-metric').filter({hasText:'Profit'}).innerText(),/450/);assert.ok(await page.locator('.report-spend-bar').first().evaluate(e=>e.style.background.includes('--red')));await capture('profit');
  await page.getByRole('group',{name:'Chart metric'}).getByRole('button',{name:'Spend',exact:true}).click();
  await page.screenshot({path:evidencePath('/tmp/gateway-report-daily-1440.png'),fullPage:true});

  await page.getByRole('group',{name:'Spend interval'}).getByRole('button',{name:'Week',exact:true}).focus();await page.keyboard.press('Enter');await ready();assert.equal(await page.locator('.report-spend-item').count(),2);
  await page.getByRole('group',{name:'Spend interval'}).getByRole('button',{name:'Month',exact:true}).click();await ready();assert.equal(await page.locator('.report-spend-item').count(),1);
  await page.getByRole('combobox',{name:'Show top',exact:true}).click();await page.getByRole('option',{name:'50',exact:true}).click();assert.equal(await page.locator('.report-model-bars li').count(),50);await page.getByRole('combobox',{name:'Model names',exact:true}).focus();await page.keyboard.press('ArrowDown');await page.getByRole('option',{name:'Provider model',exact:true}).click();await page.locator('.report-model-name').getByText('bytedance/vendor-original',{exact:true}).waitFor();
  assert.equal(await page.locator('.report-provider-layout tbody tr').count(),2);await page.getByRole('switch',{name:'Show zero spend'}).check();await page.getByRole('switch',{name:'Show unknown'}).check();assert.equal(await page.locator('.report-provider-layout tbody tr').count(),4);
  await capture('light');await page.evaluate(()=>GatewayTheme.apply('dark'));await capture('dark');
  await page.setViewportSize({width:1440,height:1100});await page.locator('#reportKey').fill(key);await page.locator('#reportUser').fill('someone@example.test');await page.locator('.report-filters').getByRole('button',{name:'Apply',exact:true}).click();await ready();const last=requests.filter(u=>u.searchParams.get('view')==='summary').at(-1);assert.equal(last.searchParams.get('api_key_id'),key);assert.equal(last.searchParams.get('user'),'someone@example.test');assert.equal(last.searchParams.has('offset'),false);assert.equal(last.searchParams.has('limit'),false);
  fail=true;await page.getByRole('group',{name:'Spend interval'}).getByRole('button',{name:'Day',exact:true}).click();await page.getByText('Could not load summary: Temporarily unavailable',{exact:true}).waitFor();assert.equal(await page.locator('.report-spend-item').count(),0);assert.equal(await page.locator('.report-table').count(),0);
  fail=false;empty=true;await page.getByRole('group',{name:'Spend interval'}).getByRole('button',{name:'Week',exact:true}).click();await ready();assert.equal(await page.locator('.report-metric strong').first().innerText(),'0');assert.equal(await page.locator('.report-filters').isVisible(),true);
  empty=false;const summaryCalls=requests.filter(u=>u.searchParams.get('view')==='summary').length;
  await page.locator('[data-system-page=reports]').click();await page.locator('.report-table tbody tr').first().waitFor();
  await page.getByText('Profit calculated',{exact:true}).waitFor();
  assert.equal(await page.locator('.report-summary').count(),0,'Reports contains charts');assert.equal(await page.getByRole('heading',{name:'Reports',exact:true}).count(),1);
  assert.equal(requests.filter(u=>u.searchParams.get('view')==='summary').length,summaryCalls,'Reports fetched aggregates');
  const tableFilters=await page.locator('.report-filters').boundingBox(),tableBox=await page.locator('.report-table').boundingBox();assert.ok(tableFilters.y<tableBox.y,'Reports filters precede table');
  await page.getByRole('button',{name:'Next',exact:true}).click();await page.getByText('Page 2',{exact:true}).waitFor();assert.equal(requests.filter(u=>u.searchParams.get('view')==='summary').length,summaryCalls,'pagination fetched aggregates');
  await page.locator('#reportKey').fill(key);await page.locator('#reportUser').fill('someone@example.test');await page.locator('.report-filters').getByRole('button',{name:'Apply',exact:true}).click();
  await page.getByText('Page 1',{exact:true}).waitFor();const rowCall=requests.filter(u=>u.searchParams.get('view')!=='summary').at(-1);assert.equal(rowCall.searchParams.get('api_key_id'),key);assert.equal(rowCall.searchParams.get('user'),'someone@example.test');
  await capture('reports-light');
  await page.evaluate(()=>GatewayTheme.apply('light'));await page.locator('[data-system-page=usage]').click();await ready();assert.equal(await page.locator('.report-table').count(),0);
  assert.deepEqual(errors,[]);console.log('PASS: Usage charts and Reports table separated, independent filters/requests, pagination, provider toggles, empty/error states, keyboard, light/dark and mobile layouts');
 }finally{await browser.close();server.close()}
})().catch(e=>{console.error(e);process.exitCode=1;server.close()});
