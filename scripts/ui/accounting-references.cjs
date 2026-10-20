const {evidencePath} = require('./evidence.cjs');
// Exercise account reports and model-specific media selection without paid calls.
const {chromium}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{const file=path.join(root,req.url==='/'?'index.html':path.basename(req.url.split('?')[0]));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 const profile=(id,label,i,v,a)=>({id,label,min_images:0,max_images:i,min_videos:0,max_videos:v,min_audios:0,max_audios:a});
 const modes=[{...profile('first_last_frames','First and last frames',2,0,0),min_images:2,image_roles:['First frame','Last frame']},profile('references','References',9,3,3)];
 const model={id:'dreamina-seedance-2-0-260128',display_name:'Seedance 2.0',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:15,default_duration:5,supports_image_input:true,supports_audio_input:true,supports_video_input:true,max_image_inputs:9,reference_modes:modes,pricing:{known:true,reference_price_usd:0}};
 const wan={...model,id:'wan2.7-r2v',display_name:'Wan 2.7',supports_audio_input:false,reference_modes:[{...profile('references','References',5,5,0),supports_reference_voice:true}]};
 const provider={id:'mock',name:'Test provider',credentials_configured:true,default_model:model.id,models:[model,wan]};
 const library=[{id:'i1',name:'first.png',content_type:'image/png',size_bytes:32768},{id:'i2',name:'last.png',content_type:'image/png',size_bytes:65536},{id:'v1',name:'motion.mp4',content_type:'video/mp4',size_bytes:1572864},{id:'a1',name:'voice.mp3',content_type:'audio/mpeg',size_bytes:2048}].map(item=>({...item,url:'/references/test/'+item.id}));
 const organization={id:'11111111-1111-4111-8111-111111111111',name:'Test organization',role:'admin',balance_usd:0,available_usd:0,storage_bytes:2500,weekly_limit_usd:null,weekly_committed_usd:0};
 let reportQueries=[],posts=[];
 await page.route('**/v1/**',route=>{const req=route.request(),url=new URL(req.url()),json=data=>route.fulfill({json:data});
  if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock'});
  if(url.pathname==='/v1/me')return json({id:'test',session_id:'session',name:'Test account',is_system_admin:true,report_columns:['date','api_key','model','status','tokens','estimated_cost','cost','confirmed_cost','settlement']});
  if(url.pathname==='/v1/organizations/11111111-1111-4111-8111-111111111111')return json(organization);
  if(url.pathname==='/v1/organizations')return json({data:[organization]});
 if(url.pathname==='/v1/api-keys')return req.method()==='POST'?json({secret:'fixture-key'}):json({data:[{id:'11111111-1111-4111-8111-111111111111',name:'Production bot',organization_id:'11111111-1111-4111-8111-111111111111'}]});
  if(url.pathname==='/v1/key')return json({key:{name:'Reference fixture',organization_id:'11111111-1111-4111-8111-111111111111',resource_mode:'shared'},organization,usage:{}});
  if(url.pathname==='/v1/providers')return json({data:[provider]});
  if((url.pathname==='/v1/generations' && route.request().method()==='GET'))return json({data:[]});
  if(url.pathname==='/v1/references')return json({data:library,has_more:false});
  if(url.pathname==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
  if(url.pathname==='/v1/generations'){posts.push(req.postDataJSON());return json({...posts.at(-1),id:'task',status:'queued',created_at:Math.floor(Date.now()/1000)});}
  if(url.pathname==='/v1/organizations/11111111-1111-4111-8111-111111111111/reports'){reportQueries.push(url.searchParams);return json({data:[{id:'task',created_at:'2026-09-07T10:00:00Z',api_key_id:'11111111-1111-4111-8111-111111111111',api_key_name:'Production bot',provider:'bytedance',model:'seedream-4-5',status:'succeeded',consumed_usd:0.04,cost:{expected_usd:0.03,metered_usd:0.039,tariff_usd:0.04,actual_usd:0,package_units:1}},{id:"pending-bill",created_at:"2026-09-07T11:00:00Z",api_key_name:"Production bot",provider:"bytedance",model:"seedream-4-5",status:"succeeded",cost:{expected_usd:0.05,metered_usd:0}}],has_more:false});}
  return json({data:[]});
 });
 try{
 await page.goto(`http://127.0.0.1:${server.address().port}`);await page.waitForFunction(()=>consoleInitialized&&window.Workspace?.ready());await page.locator('#createGenerationKey').click();await page.waitForFunction(()=>document.querySelector('#model').value.includes('seedance'));
 await page.locator('#generationKeyBudget').getByText('Test organization · Shared providers · Organization storage',{exact:true}).waitFor();
 assert.equal(await page.locator('#generationKeyBudget').getByText('Organization allowance unavailable.',{exact:true}).count(),0);
 await page.locator('#chooseReferences').click();await page.getByRole('button',{name:'Use first.png in generation'}).click();await page.getByRole('button',{name:'Use last.png in generation'}).click();
 assert.match(await page.locator('#referenceLibrary .reference-card').first().innerText(),/32 KB/);
 await page.getByRole('button',{name:'Done',exact:true}).click();assert.match(await page.locator('#selectedReferences').innerText(),/First frame/);assert.match(await page.locator('#selectedReferences').innerText(),/Last frame/);
 assert.equal(await page.locator('#videoURLField').isVisible(),false);
 await page.locator('#referenceMode').selectOption('references');await page.locator('#videoChooseReferences').click();
 await page.locator('#videoReferenceLibrary .reference-card').waitFor();assert.equal(await page.locator('#videoReferenceLibrary .reference-card').count(),1);assert.match(await page.locator('#videoReferenceLibrary .reference-card').innerText(),/1.5 MB/);
 await page.getByRole('button',{name:'Use motion.mp4 in generation'}).click();await page.locator('#videoReferenceLibrary').getByRole('button',{name:'Done',exact:true}).click();
 await page.locator('#audioChooseReferences').click();await page.locator('#audioReferenceLibrary .reference-card').waitFor();assert.equal(await page.locator('#audioReferenceLibrary .reference-card').count(),1);await page.getByRole('button',{name:'Use voice.mp3 in generation'}).click();await page.locator('#audioReferenceLibrary').getByRole('button',{name:'Done',exact:true}).click();
 await page.locator('#prompt').fill('A scene with image, video and audio references');await page.locator('#submitButton').click();await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);
 assert.equal(posts.length,1);assert.equal(posts[0].options.reference_mode,'references');assert.equal(posts[0].options.video_urls.length,1);assert.equal(posts[0].options.audio_urls.length,1);assert.equal(posts[0].input_reference.length,2);
 await page.locator('#model').selectOption(JSON.stringify([provider.id,wan.id]));
 await page.locator('#prompt').fill('A character speaking with the selected voice');
 await page.getByRole('button',{name:'Voice reference for first.png',exact:true}).click();
 await page.getByRole('button',{name:'Use voice.mp3 in generation'}).click();
 await page.locator('#submitButton').click();await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);
 assert.equal(posts.length,2);assert.equal(Object.keys(posts[1].options.reference_voices).length,1);assert.ok(Object.values(posts[1].options.reference_voices)[0].endsWith('/a1'));
 await page.locator('#workspaceOpen').click();await page.locator('.organization-tabs').getByRole('tab',{name:'Billing',exact:true}).click();await page.locator('.report-table tbody tr').first().waitFor();
 assert.deepEqual(await page.locator('.report-table thead th:not(.report-columns-cell)').allTextContents(),['Date','API key','Model','Status','Tokens','Estimated cost','Cost','Confirmed cost','Settlement']);assert.deepEqual((await page.locator('.report-table tbody tr').first().locator('td').allTextContents()).slice(5,8),['$0.03','$0.039','$0.04']);assert.match(await page.locator('.report-table').innerText(),/Covered by package/);assert.deepEqual((await page.locator('.report-table tbody tr').nth(1).locator('td').allTextContents()).slice(5,8),['$0.05','$0','Not yet known']);assert.equal(await page.locator('.report-value').first().evaluate(el=>getComputedStyle(el).whiteSpace),'nowrap');
 // Identical fixture rows must not let the old table satisfy the post-filter wait.
 const previousReportRow=await page.locator('.report-table tbody tr').first().elementHandle();
 await page.locator('#reportFrom').fill('2026-08-01');await page.locator('#reportTo').fill('2026-08-31');await page.locator('#reportKey').selectOption('11111111-1111-4111-8111-111111111111',{force:true});const filtered=page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/reports')&&new URL(r.url()).searchParams.get('api_key_id')==='11111111-1111-4111-8111-111111111111');await page.getByRole('button',{name:'Apply'}).click();await (await filtered).finished();await page.waitForFunction(row=>!row.isConnected,previousReportRow);await previousReportRow.dispose();await page.locator('.report-table tbody tr').first().waitFor();
 assert.equal(reportQueries.at(-1).get('from'),'2026-08-01');assert.equal(reportQueries.at(-1).get('to'),'2026-08-31');assert.equal(reportQueries.at(-1).get('api_key_id'),'11111111-1111-4111-8111-111111111111');
 fs.mkdirSync(evidencePath('/tmp/gateway-accounting-ui'),{recursive:true});await page.screenshot({path:evidencePath('/tmp/gateway-accounting-ui/reports-desktop.png')});
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:evidencePath('/tmp/gateway-accounting-ui/reports-mobile.png')});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'mobile page overflow');
 assert.deepEqual(errors,[]);console.log('PASS: reference modes, image/video/audio library, file sizes, generation payload and filtered account reports.');
 }finally{await browser.close();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
