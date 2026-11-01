const {evidencePath} = require('./evidence.cjs');
// Full generation cards: retained data, role restrictions, results, focus and responsive layout.
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const image=Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="480" height="300"><rect width="480" height="300" fill="#174bc2"/><circle cx="240" cy="150" r="90" fill="#77a3ff"/></svg>');
// Synthetic one-second H.264 clip and PCM audio for real browser metadata checks.
const video=Buffer.from('AAAAIGZ0eXBpc29tAAACAGlzb21pc28yYXZjMW1wNDEAAAMWbW9vdgAAAGxtdmhkAAAAAAAAAAAAAAAAAAAD6AAAA+gAAQAAAQAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAgAAAkB0cmFrAAAAXHRraGQAAAADAAAAAAAAAAAAAAABAAAAAAAAA+gAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAACAAAAAgAAAAAAAkZWR0cwAAABxlbHN0AAAAAAAAAAEAAAPoAAAAAAABAAAAAAG4bWRpYQAAACBtZGhkAAAAAAAAAAAAAAAAAABAAAAAQABVxAAAAAAALWhkbHIAAAAAAAAAAHZpZGUAAAAAAAAAAAAAAABWaWRlb0hhbmRsZXIAAAABY21pbmYAAAAUdm1oZAAAAAEAAAAAAAAAAAAAACRkaW5mAAAAHGRyZWYAAAAAAAAAAQAAAAx1cmwgAAAAAQAAASNzdGJsAAAAv3N0c2QAAAAAAAAAAQAAAK9hdmMxAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAACAAIABIAAAASAAAAAAAAAABFUxhdmM2Mi4yOC4xMDAgbGlieDI2NAAAAAAAAAAAAAAAGP//AAAANWF2Y0MBZAAK/+EAGGdkAAqs2UlsBEAAAAMAQAAAAwCDxIllgAEABmjr48siwP34+AAAAAAQcGFzcAAAAAEAAAABAAAAFGJ0cnQAAAAAAAAWgAAAAAAAAAAYc3R0cwAAAAAAAAABAAAAAQAAQAAAAAAcc3RzYwAAAAAAAAABAAAAAQAAAAEAAAABAAAAFHN0c3oAAAAAAAAC0AAAAAEAAAAUc3RjbwAAAAAAAAABAAADRgAAAGJ1ZHRhAAAAWm1ldGEAAAAAAAAAIWhkbHIAAAAAAAAAAG1kaXJhcHBsAAAAAAAAAAAAAAAALWlsc3QAAAAlqXRvbwAAAB1kYXRhAAAAAQAAAABMYXZmNjIuMTIuMTAwAAAACGZyZWUAAALYbWRhdAAAAq0GBf//qdxF6b3m2Ui3lizYINkj7u94MjY0IC0gY29yZSAxNjUgcjMyMjIgYjM1NjA1YSAtIEguMjY0L01QRUctNCBBVkMgY29kZWMgLSBDb3B5bGVmdCAyMDAzLTIwMjUgLSBodHRwOi8vd3d3LnZpZGVvbGFuLm9yZy94MjY0Lmh0bWwgLSBvcHRpb25zOiBjYWJhYz0xIHJlZj0zIGRlYmxvY2s9MTowOjAgYW5hbHlzZT0weDM6MHgxMTMgbWU9aGV4IHN1Ym1lPTcgcHN5PTEgcHN5X3JkPTEuMDA6MC4wMCBtaXhlZF9yZWY9MSBtZV9yYW5nZT0xNiBjaHJvbWFfbWU9MSB0cmVsbGlzPTEgOHg4ZGN0PTEgY3FtPTAgZGVhZHpvbmU9MjEsMTEgZmFzdF9wc2tpcD0xIGNocm9tYV9xcF9vZmZzZXQ9LTIgdGhyZWFkcz0xIGxvb2thaGVhZF90aHJlYWRzPTEgc2xpY2VkX3RocmVhZHM9MCBucj0wIGRlY2ltYXRlPTEgaW50ZXJsYWNlZD0wIGJsdXJheV9jb21wYXQ9MCBjb25zdHJhaW5lZF9pbnRyYT0wIGJmcmFtZXM9MyBiX3B5cmFtaWQ9MiBiX2FkYXB0PTEgYl9iaWFzPTAgZGlyZWN0PTEgd2VpZ2h0Yj0xIG9wZW5fZ29wPTAgd2VpZ2h0cD0yIGtleWludD0yNTAga2V5aW50X21pbj0xIHNjZW5lY3V0PTQwIGludHJhX3JlZnJlc2g9MCByY19sb29rYWhlYWQ9NDAgcmM9Y3JmIG1idHJlZT0xIGNyZj0yMy4wIHFjb21wPTAuNjAgcXBtaW49MCBxcG1heD02OSBxcHN0ZXA9NCBpcF9yYXRpbz0xLjQwIGFxPTE6MS4wMACAAAAAG2WIhAAV//7sz34FNyA8i/WUCHS1bnXje6EP/w==','base64');
const audio=Buffer.alloc(44+16000);audio.write('RIFF',0);audio.writeUInt32LE(audio.length-8,4);audio.write('WAVEfmt ',8);audio.writeUInt32LE(16,16);audio.writeUInt16LE(1,20);audio.writeUInt16LE(1,22);audio.writeUInt32LE(8000,24);audio.writeUInt32LE(16000,28);audio.writeUInt16LE(2,32);audio.writeUInt16LE(16,34);audio.write('data',36);audio.writeUInt32LE(16000,40);
const server=http.createServer((req,res)=>{if(req.url.includes('/reports/')&&(req.url.includes('/content')||req.url.endsWith('/last-frame'))){res.setHeader('Content-Type','image/svg+xml');if(req.url.includes('download=1'))res.setHeader('Content-Disposition','attachment; filename="result.png"');return res.end(image);}let file=path.join(root,path.basename(req.url.split('?')[0]));if(!path.extname(file))file=path.join(root,'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await(process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium).launch({headless:true,executablePath:process.env.UI_BROWSER_PATH});
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],notices=[],requested=[];page.on('pageerror',e=>{if(process.env.UI_BROWSER_ENGINE==='webkit'&&e.message==='ResizeObserver loop completed with undelivered notifications.')notices.push(e.message);else errors.push(e.message);});page.setDefaultTimeout(10000);
 if(process.env.UI_BROWSER_ENGINE!=='webkit')await page.context().grantPermissions(['clipboard-read','clipboard-write']);
 const org='11111111-1111-4111-8111-111111111111',id='abcdefab-1111-4111-8111-abcdefabcdef';
 const prompt='Full prompt with every line preserved.\n<script>window.injected=true</script>\n'+('Long prompt paragraph. '.repeat(120));
 const reference='https://example.test/references/'+('long-name-'.repeat(40))+'image.png';
 let mode='full',delay=0,version=1;
 const row={id,created_at:'2026-10-01T12:00:00Z',organization_id:org,organization_name:'Example organization',user_email:'alice@example.test',api_key_id:'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaabcd',api_key_name:'Production app',model:'mock-image',provider:'mock',status:'succeeded',usage:{input_tokens:12,output_tokens:25},cost:{expected_usd:0.1,metered_usd:0.1}};
 const detail=base=>{
  if(mode==='restricted')return {report:row,generation:{id,status:'completed',model:'mock-image',media_type:'image'},content_access:false,request_logging:'full'};
  const data={report:row,generation:{id,status:'completed',model:'mock/mock-image',provider_model:'mock-image',provider:'mock',media_type:'image',created_at:1790856000,updated_at:1790856001,usage:row.usage,result_content_type:'image/png',result_size_bytes:1024,result_sha256:'a'.repeat(64)},request:{prompt,options:{resolution:'1024x1024',ratio:'1:1',duration:0,seed:42,generate_audio:false,instructions:'Exact instruction\nSecond line',negative_prompt:'Avoid blur',image_urls:[reference],video_url:'https://example.test/reference.mp4',audio_url:'https://example.test/reference.wav',reference_voices:{speaker:'https://example.test/voice.wav'},parameters:{strength:0.7,nested:{new_setting:'Future custom setting '+version},unsafe:'javascript:alert(1)'}},webhook_url:'https://example.test/webhook'},processing:{started_at:'2026-10-01T12:00:00Z',poll_errors:0},links:{content_url:base+'/content',download_url:base+'/content?download=1',last_frame_url:base+'/last-frame'},content_access:true,request_logging:'full'};
  if(mode==='minimized'){data.request_logging='minimized';data.request={options:{resolution:'1024x1024',ratio:'1:1',duration:0,generate_audio:false}};}
  if(mode==='deleted'){data.links={};data.generation.result_deleted_at='2026-10-02T12:00:00Z';}
  if(mode==='failed'){data.links={};data.generation.status='failed';data.generation.error={code:'generation_failed',message:'The provider could not process this request'};}
  if(mode==='broken')data.links.content_url+='?missing=1';
  if(mode==='video'){data.generation.result_content_type='video/mp4';data.links.content_url+='?video=1';}
  if(mode==='audio'){data.generation.result_content_type='audio/wav';data.links.content_url+='?audio=1';}
  if(mode==='file')data.generation.result_content_type='application/pdf';
  return data;
 };
 await page.context().route('https://example.test/**',route=>route.request().url()===reference?route.fulfill({contentType:'image/svg+xml',body:image}):route.request().url().endsWith('.mp4')?route.fulfill({contentType:'video/mp4',body:video}):route.fulfill({contentType:'audio/wav',body:audio}));
 await page.context().route('**/v1/**',async route=>{
  const url=new URL(route.request().url()),p=url.pathname,json=data=>route.fulfill({json:data});
  if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,shared_resources:true,version:1}});
  if(p==='/v1/me')return json({id:'alice',name:'Report card tester',email:'alice@example.test',is_system_admin:true});
  if(p==='/v1/organizations')return json({data:[{id:org,name:'Example organization',role:'supermanager'}]});
  if(p.endsWith('/reports'))return json({data:[row]});
  if(p.endsWith('/reports/'+id)){requested.push(p);const data=detail(p),fail=mode==='error',wait=delay;if(wait)await new Promise(r=>setTimeout(r,wait));return fail?route.fulfill({status:503,json:{error:{message:'Temporary failure'}}}):json(data);}
  if(p.endsWith('/content')||p.endsWith('/last-frame'))return url.searchParams.has('video')?route.fulfill({contentType:'video/mp4',body:video}):url.searchParams.has('audio')?route.fulfill({contentType:'audio/wav',body:audio}):url.searchParams.has('missing')?route.fulfill({status:404,body:'Deleted file'}):route.fulfill({contentType:'image/svg+xml',headers:url.searchParams.has('download')?{'Content-Disposition':'attachment; filename="result.png"'}:{},body:image});
  return json({data:[]});
 });
 const base='http://127.0.0.1:'+server.address().port;
 const dialog=()=>page.getByRole('dialog',{name:'Generation details',exact:true});
 const ready=()=>dialog().getByRole('heading',{name:'Generation',exact:true}).waitFor();
 const open=async()=>{await page.locator('#reportsContent tbody tr').first().click();await ready();};
 const close=async()=>{await page.keyboard.press('Escape');await dialog().waitFor({state:'detached'});assert.equal(await page.locator('#reportsContent tbody tr').first().evaluate(el=>el===document.activeElement),true);};
 try{
  await page.goto(base+'/profile');await page.waitForFunction(()=>consoleInitialized&&consoleAuthenticated&&Workspace.ready()&&document.querySelector('#userName')?.textContent==='Report card tester');
  await page.evaluate(async()=>{currentConsoleSection='reports';GatewayOrganizations.close();document.querySelector('#organizationsContent').replaceChildren();document.querySelector('#profileView').hidden=true;document.querySelector('#generationView').hidden=true;document.querySelector('#configurationView').hidden=false;document.querySelector('#organizationsView').hidden=true;document.querySelector('#reportsView').hidden=false;await GatewayReports.open();});
  await page.waitForFunction(()=>document.querySelector('#reportsContent [role=status]')?.textContent===''&&!!document.querySelector('#reportsContent tbody tr'));
  await open();assert.equal(requested.at(-1),'/v1/system/reports/'+id);assert.equal(await dialog().locator('.detail-text').first().textContent(),prompt);assert.equal(await page.evaluate(()=>!!window.injected),false);
  assert.match(await dialog().innerText(),/Future custom setting 1/);assert.match(await dialog().innerText(),/Exact instruction\nSecond line/);
  const referenceImage=dialog().getByRole('img',{name:'Image reference 1',exact:true});await referenceImage.scrollIntoViewIfNeeded();await referenceImage.evaluate(el=>el.decode());assert.equal(await referenceImage.getAttribute('src'),reference);assert.equal(await dialog().locator('a[href^="javascript:"]').count(),0);assert.equal(await dialog().getByRole('link',{name:reference,exact:true}).count(),0);
  await dialog().getByRole('button',{name:'Copy image reference 1 link',exact:true}).click();if(process.env.UI_BROWSER_ENGINE!=='webkit')assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),reference);
  const resultImage=dialog().getByRole('img',{name:'Result',exact:true});await resultImage.scrollIntoViewIfNeeded();await resultImage.evaluate(el=>el.decode());await dialog().getByRole('button',{name:'Copy result link',exact:true}).click();if(process.env.UI_BROWSER_ENGINE!=='webkit')assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),base+'/v1/system/reports/'+id+'/content');
  const download=page.waitForEvent('download');await dialog().getByRole('link',{name:'Download result',exact:true}).click();assert.equal((await download).suggestedFilename(),'result.png');
  await dialog().locator('.detail-record').evaluate(el=>el.open=true);await dialog().getByRole('button',{name:'Close generation details',exact:true}).focus();const bodyNode=await dialog().locator('.detail-text').first().elementHandle();version=2;await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.waitForFunction(()=>document.querySelector('.detail-record pre')?.textContent.includes('Future custom setting 2'));assert.equal(await dialog().locator('.detail-record').evaluate(el=>el.open),true);assert.equal(await dialog().getByRole('button',{name:'Close generation details',exact:true}).evaluate(el=>el===document.activeElement),true);assert.equal(await bodyNode.evaluate(el=>el.isConnected),true);assert.equal(await dialog().getByRole('button',{name:/Refresh|Reload/}).count(),0);
  fs.mkdirSync(evidencePath('/tmp/gateway-report-details-ui'),{recursive:true});
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await page.setViewportSize({width,height:1000});await dialog().evaluate(el=>el.scrollTop=0);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const box=await dialog().boundingBox();assert.ok(box.x>=0&&box.x+box.width<=width);assert.ok(await dialog().evaluate(el=>el.scrollWidth<=el.clientWidth));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await page.screenshot({path:evidencePath(`/tmp/gateway-report-details-ui/${theme}-${width}.png`),fullPage:true});
  }
  await dialog().getByRole('heading',{name:'References',exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:evidencePath('/tmp/gateway-report-details-ui/media-mobile.png')});
  await page.setViewportSize({width:1440,height:1000});await dialog().getByRole('heading',{name:'References',exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:evidencePath('/tmp/gateway-report-details-ui/media-desktop.png')});
  await close();await page.setViewportSize({width:1440,height:1000});
  await page.locator('#reportsContent tbody tr').first().focus();await page.keyboard.press('Enter');await ready();assert.equal(await dialog().getByRole('button',{name:'Close generation details',exact:true}).evaluate(el=>el===document.activeElement),true);await page.keyboard.press('Tab');assert.equal(await page.evaluate(()=>document.activeElement.closest('dialog')!==null),true);await close();
  await page.keyboard.press('Space');await ready();await close();
  for(const next of ['video','audio','file','broken']){
   mode=next;await open();await dialog().getByRole('heading',{name:'Results',exact:true}).scrollIntoViewIfNeeded();
   if(next==='video'||next==='audio'){const player=dialog().locator('.detail-media-card').filter({has:page.getByRole('heading',{name:'Result',exact:true})}).locator(next);await player.scrollIntoViewIfNeeded();await player.evaluate(el=>el.readyState>=1?true:new Promise((resolve,reject)=>{el.addEventListener('loadedmetadata',()=>resolve(true),{once:true});el.addEventListener('error',()=>reject(new Error('Result preview failed')),{once:true});}));}
   if(next==='file')assert.equal(await dialog().getByRole('img',{name:'Result',exact:true}).count(),0);
   if(next==='broken')await dialog().locator('.detail-media-card').filter({has:page.getByRole('heading',{name:'Result',exact:true})}).getByText('Preview unavailable. Use Open to view the file.',{exact:true}).waitFor();
   assert.equal(await dialog().getByRole('button',{name:'Copy result link',exact:true}).isVisible(),true);assert.equal(await dialog().getByRole('link',{name:'Open result ↗',exact:true}).isVisible(),true);await close();
  }
  for(const next of ['restricted','minimized','deleted','failed']){
   mode=next;await open();const text=await dialog().innerText();
   if(next==='restricted'){assert.match(text,/Your role can view accounting/);assert.doesNotMatch(text,/Full prompt/);assert.equal(await dialog().getByRole('link').count(),0);}
   if(next==='minimized'){assert.match(text,/minimal request logging/);assert.doesNotMatch(text,/Full prompt|reference.mp4/);assert.match(text,/1024x1024/);}
   if(next==='deleted'){assert.match(text,/Result files have been deleted/);assert.equal(await dialog().getByRole('link',{name:'Download result',exact:true}).count(),0);}
   if(next==='failed')assert.match(text,/The provider could not process/);
   await close();
  }
  mode='error';await page.locator('#reportsContent tbody tr').first().click();await dialog().getByText('Could not load generation: Temporary failure',{exact:true}).waitFor();mode='full';await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await ready();await close();
  await page.locator('#reportOrganization').selectOption(org,{force:true});await page.waitForFunction(()=>document.querySelector('#reportsContent [role=status]')?.textContent===''&&!!document.querySelector('#reportsContent tbody tr'));await open();assert.equal(requested.at(-1),'/v1/organizations/'+org+'/reports/'+id);await close();
  delay=300;await page.locator('#reportsContent tbody tr').first().click();await page.keyboard.press('Escape');await page.waitForTimeout(400);assert.equal(await dialog().count(),0);delay=0;
  await page.evaluate(id=>ReportDetails.open('/v1/admin/reports/'+id),id);await ready();assert.equal(requested.at(-1),'/v1/admin/reports/'+id);await dialog().getByText('All retained data',{exact:true}).click();assert.match(await dialog().locator('pre').textContent(),/Future custom setting/);await dialog().getByRole('button',{name:'Close generation details',exact:true}).click();
  assert.deepEqual(errors,[]);if(notices.length)console.log('WebKit layout notices:',notices);console.log('PASS: full prompt/settings/references/results, system/organization/personal routes, permissions and retention, deleted/failed/error/retry states, stale closure, keyboard/focus, dark/light desktop and 320/390px.');
 }catch(error){console.error('Browser errors:',errors);await page.screenshot({path:evidencePath('/tmp/gateway-report-details-failure.png'),fullPage:true});throw error;}finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
