// Isolated report dashboard checks; no production or provider requests.
const engine=process.env.UI_BROWSER_ENGINE||'chromium'; const { [engine]:chromium}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=process.env.UI_SOURCE_DIR||path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const filename=path.basename(req.url.split('?')[0]);const file=path.join(root,filename.includes('.')?filename:'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH}),page=await browser.newPage({viewport:{width:1440,height:1100}});page.setDefaultTimeout(8000);
 const org='11111111-1111-4111-8111-111111111111',key='22222222-2222-4222-8222-222222222222',requests=[],errors=[];let empty=false,fail=false;
 page.on('pageerror',e=>errors.push(e.message));
 const metrics={profit_usd:450,pending_profit_requests:2,recognized_profit_requests:149,requests:153,successful:150,failed:1,spend_usd:2876.31,priced_requests:151,tokens:6095160,token_requests:120};
 let fixture,displayTheme;
 const summary=()=>({...metrics,profit_usd:fixture.values.reduce((a,b)=>a+b,0),average_cost_usd:fixture.values.reduce((a,b)=>a+b,0)/fixture.values.length,interval:'day',periods:fixture.values.map((value,i)=>({...metrics,from:'2026-09-'+(14+i),to:'2026-09-'+(14+i),spend_usd:value,profit_usd:value})),public_models:[],provider_models:[],providers:[]});
 await page.route('**/v1/**',async route=>{const url=new URL(route.request().url()),p=url.pathname,json=value=>route.fulfill({json:value});
  if(p==='/v1/info')return json({auth_mode:'google',console:{generation_ui:true,personal_providers:false,shared_resources:true,version:1}});
  if(p==='/v1/me')return json({id:'33333333-3333-4333-8333-333333333333',email:'tester@example.test',name:'Tester',theme:displayTheme,is_system_admin:true,session_id:'fixture'});
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
 const out=process.env.UI_EVIDENCE_DIR||'/tmp/gateway-usage-axis'; fs.mkdirSync(out,{recursive:true}); const results=[];
 try {
 for(const theme of ['dark','light']){displayTheme=theme;await page.emulateMedia({colorScheme:theme});for(const f of [
 {name:'tenths',values:[0,.025,.05,.075,.1]},
 {name:'micro',values:[0,.000001,.000002,.000003,.000004]},
 {name:'zero',values:[0,0,0]},
 {name:'negative',values:[-.1,-.075,-.05,-.025,0],profit:true},
 {name:'mixed',values:[-.04,0,.08],profit:true},
 {name:'ordinary',values:[0,25,50,75,100]},
 {name:'single-micro',values:[.000001]},
 {name:'thousands',values:[0,250,500,750,1000]},
 {name:'millions',values:[0,250000,500000,750000,1000000]},
 {name:'billions',values:[0,250000000,500000000,750000000,1000000000]},
 {name:'trillions',values:[-1000000000000,0,1000000000000],profit:true},
 {name:'awkward',values:[-.000001,.123456],profit:true},
 {name:'negative-zero',values:[-0,0,0],profit:true}
 ]) { fixture=f;
 await page.goto(base+'/system'); await page.locator('[data-system-page=usage]').click(); await page.locator('.report-metric').first().waitFor();
 if(f.profit)await page.getByRole('group',{name:'Chart metric'}).getByRole('button',{name:'Profit',exact:true}).click();
 for(const width of [1440,390,320]) {
 await page.evaluate(()=>document.activeElement?.blur());await page.setViewportSize({width,height:1100});await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
 const observed=await page.evaluate(()=>({labels:[...document.querySelectorAll('.report-spend-axis span')].map(e=>e.textContent),bars:[...document.querySelectorAll('.report-spend-bar')].map(e=>({height:e.style.height,bottom:e.style.bottom,pixels:e.getBoundingClientRect().height,trackPixels:e.parentElement.clientHeight,background:e.style.background,tooltip:e.parentElement.getAttribute('aria-label')}))}));
 const min=Math.min(...f.values,0),max=Math.max(...f.values,0)|| (min===0?1:0),scale=max-min;
 observed.bars.forEach((b,i)=>{assert.ok(Math.abs(parseFloat(b.height)-100*Math.abs(f.values[i])/scale)<.0001);assert.ok(Math.abs(parseFloat(b.bottom)-100*(Math.min(0,f.values[i])-min)/scale)<.0001);assert.ok(Math.abs(b.pixels-b.trackPixels*Math.abs(f.values[i])/scale)<.1);});
 assert.equal(new Set(observed.labels).size,5,'duplicate labels '+f.name);
 assert.ok(!observed.labels.some(v=>/^-\$0(?:\.0+)?(?:[KMBT])?$/.test(v)),'negative zero '+f.name+' '+width+' '+theme);
 if(f.name==='tenths')assert.deepEqual(observed.labels,['$0.1','$0.075','$0.05','$0.025','$0']);
 if(f.name==='zero'||f.name==='negative-zero')assert.deepEqual(observed.labels,['$1','$0.75','$0.5','$0.25','$0']);
 observed.labels.forEach((label,i)=>{const suffix=label.slice(-1),divisor=({K:1e3,M:1e6,B:1e9,T:1e12})[suffix]||1;const numeric=Number(label.replace(/[$,KMBT]/g,''))*divisor;assert.ok(Math.abs(numeric-(min+(max-min)*(1-i/4)))<scale/4*.025+1e-12,'label accuracy '+label);});
 observed.bars.forEach((bar,i)=>{const expected=new Intl.NumberFormat('en-US',{style:'currency',currency:'USD',minimumFractionDigits:2,maximumFractionDigits:6}).format(f.values[i]||0);assert.ok(bar.tooltip.includes(expected),'tooltip amount changed');});
 const box=await page.locator('.report-spend-axis').boundingBox(),scroll=await page.locator('.report-spend-scroll').boundingBox();assert.ok(box.x+box.width<scroll.x,'axis overlaps bars');assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'page overflow '+f.name+' '+width+' '+theme);
 await page.locator('.report-spend-track').first().focus();await page.keyboard.press('Tab');await page.keyboard.press('Shift+Tab');assert.ok(await page.locator('.report-chart-tooltip').first().isVisible(),'keyboard tooltip');
 results.push({engine,theme,name:f.name,width,values:f.values,rawTicks:[1,.75,.5,.25,0].map(p=>min+(max-min)*p),...observed});
 await page.locator('.report-summary-card').first().screenshot({path:out+'/'+engine+'-'+theme+'-'+f.name+'-'+width+'.png'});
 }
 }
 }assert.deepEqual(errors.filter(e=>e!=='ResizeObserver loop completed with undelivered notifications.'),[]);fs.writeFileSync(out+'/'+engine+'.json',JSON.stringify({source:root,results,errors},null,2));console.log('Browser diagnostics: '+JSON.stringify(errors));console.log(engine+': '+results.length+' cases passed geometry checks; '+out);
 } finally {await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exit(1)});
