const {evidencePath} = require('./evidence.cjs');
// Catalogue administration uses real UI files and isolated HTTP fixtures.
const {chromium,webkit}=require('playwright');
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
if(!['chromium','webkit'].includes(engine))throw new Error('Unsupported browser engine: '+engine);
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui'),out=evidencePath('/tmp/gateway-catalog-ui');fs.mkdirSync(out,{recursive:true});
const server=http.createServer((req,res)=>{const name=path.basename(req.url.split('?')[0]),file=path.join(root,name.includes('.')?name:'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH&&engine==='chromium'?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1100}});page.setDefaultTimeout(8000);
 const errors=[],writes=[],date='2026-10-09T03:15:00Z',hash='a'.repeat(64),previousHash='b'.repeat(64);
 let conflict=false,empty=false,emailRuns=null,runsError=false,settings={timezone:'UTC',email:'',version:1},schedule={provider:'bytedance',enabled:true,time:'03:15',version:1,sources:[{id:'pricing',url:'https://docs.example.test/prices',parser:'byteplus-pricing'}],last_check:date,next_check:'2026-10-10T03:15:00Z'};
 const model={id:'dreamina-seedance-2-5-260628',display_name:'Seedance 2.5',media_type:'video',resolutions:['480p','720p'],ratios:['16:9'],min_duration:4,max_duration:15,default_duration:5};
 const provider={id:'bytedance',api_type:'bytedance',name:'BytePlus ModelArk',configured:true,credentials_configured:true,enabled:true,status:'connected',models:[model],model_count:1,fields:[{key:'api_key',label:'API key',secret:true,required:true}],values:{base_url:'https://ark.ap-southeast.bytepluses.com/api/v3'},hints:{api_key:'************'}};
 const warning={key:'old',model:model.id,source:'pricing',reason:'Capabilities require adapter review <script>unsafe()</script>',first_seen:date,last_seen:date,occurrences:4,evidence:{hash,previous_hash:previousHash}};
 const run={id:'run',provider:'bytedance',status:'warning',version:2,completed_at:date,changes:[{model:model.id,field:'release_date',after:'2026-06'}],new_warnings:[],open_warnings:[warning],resolved_warnings:[],sources:[{source:schedule.sources[0],hash}],email_status:'sent'};
 const state={version:2,models:{[model.id]:{id:model.id,metadata:{release_date:'2026-06'}}},warnings:[warning],observations:{pricing:{hash,source:schedule.sources[0]}}};
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/v1/**',async route=>{
  const req=route.request(),url=new URL(req.url()),p=url.pathname,json=value=>route.fulfill({json:value});
  if(p==='/v1/info')return json({auth_mode:'google',console:{generation_ui:true,personal_providers:true,version:1}});
  if(p==='/v1/me')return json({id:'fixture-admin',is_system_admin:true,name:'Administrator',email:'admin@example.test'});
  if(p==='/v1/system/catalog/settings'){
   if(req.method()==='PUT'){const body=req.postDataJSON();writes.push(body);if(conflict)return route.fulfill({status:409,json:{error:{message:'Settings changed. Reload before saving.'}}});settings={...body,version:settings.version+1};}
   return json(settings);
  }
  if(p==='/v1/system/catalog/schedules')return json({data:[schedule]});
  if(p==='/v1/system/catalog/schedules/bytedance/check'){writes.push({queued:true});return json({queued:true});}
  if(p==='/v1/system/catalog/schedules/bytedance'){const body=req.postDataJSON();writes.push(body);schedule={...body,version:schedule.version+1};return json(schedule);}
  if(p==='/v1/system/catalog/runs'&&runsError)return route.fulfill({status:503,json:{error:{message:'Catalogue journal unavailable.'}}});
  if(p==='/v1/system/catalog/runs')return json({data:empty?[]:(emailRuns||[run]),has_more:url.searchParams.get('offset')==='0'});
  if(p==='/v1/system/catalog/providers/bytedance')return json(state);
  if(p==='/v1/system/catalog/providers/bytedance/versions')return json({data:[state],has_more:false});
  if(p.startsWith('/v1/system/catalog/documents/'))return json({hash:p.split('/').at(-1),text:'Published source <script>unsafe()</script> '+hash});
  if(p==='/v1/system/settings')return json({generation_ui:true,personal_providers:true,version:1});
  if(p==='/v1/admin/providers'||p==='/v1/system/providers'||p==='/v1/providers')return json({data:[provider]});
  if(p==='/v1/admin/providers/bytedance'||p==='/v1/system/providers/bytedance')return json(provider);
  return json({data:[]});
 });
 const base='http://127.0.0.1:'+server.address().port;
 async function screenshots(name){for(const width of [1440,390,320]){await page.setViewportSize({width,height:1000});for(const theme of ['dark','light']){await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));if(name==='email-statuses')await page.locator('#systemContent tbody tr td:nth-child(7)').first().scrollIntoViewIfNeeded();await page.screenshot({path:`${out}/${name}-${width}-${theme}.png`,fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),`${name} overflows at ${width}px ${theme}: `+JSON.stringify(await page.evaluate(()=>[...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right>innerWidth+1&&e.getBoundingClientRect().width).map(e=>({tag:e.tagName,cls:e.className,width:e.getBoundingClientRect().width})).slice(0,20))));}}}
 try{
  await page.goto(base+'/system');await page.getByRole('tab',{name:'Model catalogue',exact:true}).click();
  const form=page.locator('.catalog-sync-settings');await form.getByLabel('Report email',{exact:true}).waitFor();
  await form.locator('select[name=timezone] option').first().waitFor({state:'attached'});
  assert.equal(await form.getByRole('button',{name:'Save',exact:true}).isVisible(),false);
  const zones=await form.locator('select[name=timezone] option').evaluateAll(options=>options.map(option=>({value:option.value,label:option.textContent})));
  assert.ok(zones.length<100,'Timezone choices must group equivalent cities, not list hundreds of cities');
  const offsets=zones.map(zone=>{const match=/^UTC([+-])(\d{2}):(\d{2})(?: \(.+\))?$/.exec(zone.label);assert.ok(match,'Offset must lead every timezone label: '+zone.label);return (match[1]==='-'?-1:1)*(Number(match[2])*60+Number(match[3]));});
  assert.deepEqual(offsets,[...offsets].sort((a,b)=>a-b),'UTC offsets must be sorted numerically');
  for(let offset=-720;offset<=840;offset+=60)assert.ok(offsets.includes(offset),'Missing whole-hour UTC offset '+offset);
  for(const offset of [-570,-210,210,270,330,345,390,525,570,630,765])assert.ok(offsets.includes(offset),'Missing fractional UTC offset '+offset);
  const zoneLabel='UTC+02:00 (Athens, Nicosia, Bucharest)',trigger=form.getByRole('combobox',{name:'Default timezone'});
  await trigger.click();let popup=page.getByRole('listbox',{name:'Default timezone'});await popup.waitFor();await page.keyboard.press('Home');assert.match(await popup.locator('.highlighted').textContent(),/^UTC-12:00/);await page.keyboard.press('End');assert.match(await popup.locator('.highlighted').textContent(),/^UTC\+14:00/);await page.keyboard.press('Escape');assert.equal(await form.locator('select[name=timezone]').inputValue(),'UTC');
  await trigger.click();await page.getByRole('listbox',{name:'Default timezone'}).getByRole('option',{name:zoneLabel,exact:true}).click();await form.getByLabel('Report email',{exact:true}).fill('reports@example.test');
  await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();
  assert.deepEqual(writes.at(-1),{timezone:'Europe/Athens',email:'reports@example.test',version:1});
  await form.getByLabel('Report email',{exact:true}).fill('draft@example.test');conflict=true;await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByText('Settings changed. Reload before saving.').waitFor();assert.equal(await form.getByLabel('Report email',{exact:true}).inputValue(),'draft@example.test');conflict=false;
  await screenshots('settings');await page.setViewportSize({width:1440,height:1100});await trigger.click();await screenshots('timezone-picker');await page.keyboard.press('Escape');await page.setViewportSize({width:1440,height:1100});
  // An existing equivalent IANA identifier must survive reload and unrelated saves.
  settings.timezone='Asia/Nicosia';await page.reload();await page.getByRole('tab',{name:'Model catalogue',exact:true}).click();await form.getByRole('combobox',{name:'Default timezone'}).filter({hasText:zoneLabel}).waitFor();assert.equal(await form.locator('select[name=timezone]').inputValue(),'Asia/Nicosia');assert.equal(await form.getByRole('button',{name:'Save',exact:true}).isVisible(),false);
  await form.getByLabel('Report email',{exact:true}).fill('reports-updated@example.test');await form.getByRole('button',{name:'Save',exact:true}).click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(writes.at(-1).timezone,'Asia/Nicosia');
  const content=page.locator('#systemContent'),catalogNav=page.locator('[data-system-page=catalog]');
  async function journalOnly(){await content.getByRole('button',{name:'Details',exact:true}).waitFor();assert.deepEqual(await content.locator('h1').allTextContents(),['Model catalogue'],'Journal must replace the previous administration page');assert.equal(await content.locator('.system-settings-panels').count(),0);assert.equal(await content.locator('.report-filters').count(),1);assert.equal(await catalogNav.getAttribute('aria-current'),'page');}
  await catalogNav.click();await journalOnly();
  // Reopening with the keyboard must not append a second journal.
  await catalogNav.focus();await page.keyboard.press('Enter');await journalOnly();
  // Exercise the actual Email column; details and exports must retain the API enum.
  const emailCases=[
   ['not_needed','Not required'],['pending','Awaiting delivery'],['sent','Sent'],
   ['retry','Retry scheduled'],['failed','Delivery failed'],['disabled','No recipient configured'],
   ['future_status','Unknown status: future_status'],['__proto__','Unknown status: __proto__'],
   ['<img src=x onerror="window.emailStatusInjected=true">','Unknown status: <img src=x onerror="window.emailStatusInjected=true">'],
   ['', 'Not available'],[null,'Not available'],[undefined,'Not available']
  ];
  emailRuns=emailCases.map(([email_status],i)=>({...run,id:'email-'+i,email_status}));
  await content.getByRole('button',{name:'Refresh',exact:true}).click();
  const emailCells=content.locator('tbody tr td:nth-child(7)');
  await page.waitForFunction(count=>document.querySelectorAll('#systemContent tbody tr').length===count,emailCases.length);
  assert.deepEqual(await emailCells.allTextContents(),emailCases.map(([,label])=>label));
  assert.equal(await content.locator('tbody img, tbody script').count(),0);
  assert.equal(await page.evaluate(()=>window.emailStatusInjected),undefined);
  assert.deepEqual(await content.locator('tbody tr td:nth-child(3)').allTextContents(),emailCases.map(()=>run.status));
  const exported=page.waitForEvent('download');await content.getByRole('button',{name:'Export report',exact:true}).click();
  assert.deepEqual(JSON.parse(fs.readFileSync(await (await exported).path(),'utf8')).data,JSON.parse(JSON.stringify(emailRuns)));
  await content.getByRole('button',{name:'Details',exact:true}).first().focus();await page.keyboard.press('Enter');
  await page.locator('dialog[open]').waitFor();assert.equal(JSON.parse(await page.locator('dialog[open] pre').textContent()).email_status,'not_needed');
  await page.keyboard.press('Escape');await page.locator('dialog[open]').waitFor({state:'hidden'});
  await screenshots('email-statuses');await page.setViewportSize({width:1440,height:1100});
  runsError=true;await content.getByRole('button',{name:'Refresh',exact:true}).click();await content.getByText('Catalogue journal unavailable.',{exact:true}).waitFor();
  runsError=false;emailRuns=null;await content.getByRole('button',{name:'Refresh',exact:true}).click();
  await page.waitForFunction(()=>document.querySelectorAll('#systemContent tbody tr').length===1);
  await screenshots('journal');await page.setViewportSize({width:1440,height:1100});
  await page.locator('[data-system-page=administrators]').click();await content.getByRole('heading',{name:'Users',exact:true}).waitFor();await catalogNav.click();await journalOnly();
  await page.locator('[data-system-page=sharedProviders]').click();await page.locator('[data-provider-id=bytedance]').click();
  const connection=page.locator('.catalog-provider-settings');await connection.getByLabel('Daily check time',{exact:true}).waitFor();await connection.locator('select[aria-label="Source parser"]').waitFor({state:'attached'});
  assert.equal(await connection.locator('select').count(),1);assert.match(await connection.locator('.catalog-schedule-status').textContent(),/UTC\+02:00 \(Athens, Nicosia, Bucharest\)/);
  await connection.getByLabel('Daily check time',{exact:true}).fill('04:20');
  await connection.getByRole('button',{name:'Add source',exact:true}).click();await connection.getByLabel('Source ID',{exact:true}).last().fill('models');await connection.getByLabel('Source URL',{exact:true}).last().fill('https://docs.example.test/models');
  const sourceParser=connection.locator('select[aria-label="Source parser"]').last();assert.ok((await sourceParser.locator('option').allTextContents()).includes('alibaba-image-models'));await sourceParser.selectOption('alibaba-retirements',{force:true});await connection.getByRole('button',{name:'Save',exact:true}).click();await connection.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(schedule.sources[1].parser,'alibaba-retirements');
  assert.equal(schedule.time,'04:20');assert.equal(schedule.sources.length,2);assert.equal('timezone' in schedule,false);
  await connection.getByRole('button',{name:'Check now',exact:true}).click();await connection.getByText('Check queued. Results appear in Model catalogue.').waitFor();assert.equal(writes.at(-1).queued,true);
  await screenshots('provider');await page.setViewportSize({width:1440,height:1100});
  await catalogNav.click();await journalOnly();assert.equal(await page.locator('#providersView').evaluate(node=>node.hidden&&!node.closest('#systemContent')),true,'Leaving shared providers must restore its embedded page');
  await content.getByRole('button',{name:'Details',exact:true}).focus();await page.keyboard.press('Enter');await page.locator('dialog[open]').waitFor();assert.equal(await page.locator('dialog script').count(),0);
  await page.locator('dialog[open]').getByRole('button',{name:'Source snapshots',exact:true}).click();await page.getByRole('heading',{name:'Source snapshots',exact:true}).waitFor();await page.keyboard.press('Escape');await page.getByRole('heading',{name:'Source snapshots',exact:true}).waitFor({state:'hidden'});await page.keyboard.press('Escape');await page.locator('dialog[open]').waitFor({state:'hidden'});
  const download=page.waitForEvent('download');await content.getByRole('button',{name:'Export report',exact:true}).click();assert.equal((await download).suggestedFilename(),'catalog-report.json');
  await content.locator('select[aria-label=Provider]').selectOption('bytedance',{force:true});await content.locator('select[aria-label=View]').selectOption('warnings',{force:true});await content.getByText(warning.reason,{exact:true}).waitFor();assert.equal(await content.locator('script').count(),0);
  await screenshots('warnings');await page.setViewportSize({width:1440,height:1100});
  await content.locator('select[aria-label=View]').selectOption('versions',{force:true});await content.locator('tbody tr').first().hover();await content.getByRole('button',{name:'Details',exact:true}).click();await page.getByRole('heading',{name:'Catalogue version 2'}).waitFor();await page.keyboard.press('Escape');
  empty=true;await content.locator('select[aria-label=View]').selectOption('runs',{force:true});await content.getByText('No records yet.',{exact:true}).waitFor();
  // WebKit also emits this layout diagnostic on the unmodified catalogue suite.
  // Report it explicitly; every other page error still fails this regression.
  const layoutWarnings=engine==='webkit'?errors.filter(message=>message==='ResizeObserver loop completed with undelivered notifications.'):[];
  if(layoutWarnings.length)console.warn('KNOWN BASELINE WebKit layout diagnostics: '+layoutWarnings.length);
  assert.deepEqual(errors.filter(message=>!layoutWarnings.includes(message)),[]);console.log('PASS ('+engine+'): readable email states and safe unknown/missing values, raw details/exports, common timezone/email CAS, provider time and source edits, queued checks, history/warnings/versions, safe evidence/snapshots, JSON export, keyboard/Escape, both themes at 1440/390/320px.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
