const {evidencePath} = require('./evidence.cjs');
// Reference picker smoke tests. Uses only mocked providers and temporary images.
const {chromium,webkit} = require('playwright');
const browserEngine=process.env.UI_BROWSER_ENGINE==='webkit'?webkit:chromium;
const http = require('node:http');
const https = require('node:https');
const os = require('node:os');
const {execFileSync} = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const ui = path.resolve(__dirname, '../../web/public/ui');
// Independent PNG fixture; reference tests must not depend on site branding.
const image = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64');
const output = process.env.UI_SCREENSHOT_DIR || evidencePath('/tmp/media-gateway-references-ui');
fs.mkdirSync(output, {recursive:true});
const csp=fs.readFileSync(path.join(ui,'../../Caddyfile'),'utf8').match(/header @consolePolicy Content-Security-Policy "([^"\n]+)"/)[1];
const server = http.createServer((req, res) => {
  if(req.url==='/')res.setHeader('Content-Security-Policy',csp);
  if (req.url.startsWith('/references/')) { res.setHeader('Content-Type','image/png'); return res.end(image); }
  const name = req.url === '/' ? 'index.html' : path.basename(req.url.split('?')[0]);
  const file = path.join(ui,name);
  if (!fs.existsSync(file)) return res.writeHead(404).end();
  res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.png':'image/png','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream');
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser = await browserEngine.launch({headless:true,...(process.env.UI_BROWSER_PATH ? {executablePath:process.env.UI_BROWSER_PATH} : {})});
  const page = await browser.newPage({viewport:{width:1440,height:1100},ignoreHTTPSErrors:true});
  const errors=[];page.on('pageerror',e=>{if(process.env.UI_BROWSER_ENGINE==='webkit'&&e.message==='ResizeObserver loop completed with undelivered notifications.'){console.warn('WebKit resize notice: '+e.message);return;}errors.push(e.message);});
  const models=[{id:'video',display_name:'Video with references',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:2,max_duration:10,default_duration:5,supports_image_input:true,max_image_inputs:2,pricing:{known:true,reference_price_usd:0}},
    {id:'image',display_name:'Image with references',media_type:'image',resolutions:['1024*1024'],supports_image_input:true,max_image_inputs:1,requires_image_input:true,pricing:{known:true,reference_price_usd:0}}];
  models.push({id:'assets',display_name:'Seedance registered assets',media_type:'video',resolutions:['720p'],ratios:['16:9'],min_duration:4,max_duration:15,default_duration:5,supports_image_input:true,max_image_inputs:9,supports_video_input:true,supports_audio_input:true,supports_asset_references:true,pricing:{known:true,reference_price_usd:0},reference_modes:[{id:'references',label:'References',min_images:0,max_images:9,min_videos:0,max_videos:3,min_audios:0,max_audios:3},{id:'first_last_frames',label:'First and last frames',min_images:2,max_images:2,min_videos:0,max_videos:0,min_audios:0,max_audios:0,image_roles:['First frame','Last frame']}]});
  const provider={id:'mock',name:'Mock',live:false,credentials_configured:true,default_model:'video',models};
  let library=[],counter=0,uploadCount=0,posts=[],failDelete=false,failUpload=false,delayUpload=0,direct=false,directPuts=0,completions=0,preparations=0,blockDirect=false,headerSigned=false;
  const tlsDir=fs.mkdtempSync(path.join(os.tmpdir(),'gateway-direct-ui-'));
  execFileSync('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',path.join(tlsDir,'key.pem'),'-out',path.join(tlsDir,'cert.pem'),'-days','1','-subj','/CN=localhost'],{stdio:'ignore'});
  const storageServer=https.createServer({key:fs.readFileSync(path.join(tlsDir,'key.pem')),cert:fs.readFileSync(path.join(tlsDir,'cert.pem'))},(req,res)=>{
    res.setHeader('Access-Control-Allow-Origin','*');res.setHeader('Access-Control-Allow-Methods','PUT');res.setHeader('Access-Control-Allow-Headers','content-type,x-amz-content-sha256,x-amz-date,authorization');
    if(req.method==='OPTIONS'){res.writeHead(204);return res.end();}
    directPuts++;assert.equal(req.method,'PUT');assert.equal(req.headers['x-api-key'],undefined);assert.equal(req.headers.authorization,headerSigned?'AWS4-HMAC-SHA256 storage-upload-fixture':undefined);assert.equal(req.headers.cookie,undefined);
    assert.equal(Number(req.headers['content-length']),image.length);
    const chunks=[];req.on('data',chunk=>chunks.push(chunk));req.on('end',()=>{
      assert.deepEqual(Buffer.concat(chunks),image);
      if(blockDirect){req.socket.destroy();return;}res.writeHead(200);res.end();
    });
  });
  await new Promise(resolve=>storageServer.listen(0,'127.0.0.1',resolve));
  const directURL=`https://127.0.0.1:${storageServer.address().port}/file?signature=test`;
  await page.route('**/v1/**',async route=>{
    const request=route.request(), url=new URL(request.url());
    const json=value=>route.fulfill({json:value});
    if(url.pathname==='/v1/info')return json({auth_mode:'google',provider:'mock'});
    if(url.pathname==='/v1/me')return json({id:'test',session_id:'test-session',name:'Reference test',email:'test@example.test'});
    if(url.pathname==='/v1/organizations')return json({data:[{id:'11111111-1111-4111-8111-111111111111',name:'Test organization',role:'admin',balance_usd:0,available_usd:0,storage_bytes:2500}]});
 if(url.pathname==='/v1/api-keys'&&request.method()==='POST')return json({secret:'fixture-key'});
    if(url.pathname==='/v1/key')return json({key:{id:'fixture-key',name:'Reference test',organization_id:'11111111-1111-4111-8111-111111111111',resource_mode:'shared',scopes:['generation']},usage:{consumed_usd:0},organization:{id:'11111111-1111-4111-8111-111111111111',name:'Test organization',role:'admin',balance_usd:10,credit_limit_usd:0}});
    if(url.pathname==='/v1/providers')return json({data:[provider]});
    if((url.pathname==='/v1/generations' && route.request().method()==='GET'))return json({data:[]});
    if(url.pathname==='/v1/generations/estimate')return json({cost:{expected_usd:0},can_generate:true});
    if(url.pathname==='/v1/generations'){posts.push(request.postDataJSON());return json({...posts.at(-1),provider:posts.at(-1).model.split('/')[0],media_type:'video',id:'task-1',status:'queued',created_at:Math.floor(Date.now()/1000)});}
    if(url.pathname==='/v1/references/uploads'){
      preparations++;
      const data=request.postDataJSON();assert.equal(data.sha256,crypto.createHash('sha256').update(image).digest('hex'));assert.equal(data.size_bytes,image.length);
      return json(direct?{id:'direct-upload',mode:'s3',target:{url:directURL,headers:{'x-amz-content-sha256':data.sha256,...(headerSigned?{Authorization:'AWS4-HMAC-SHA256 storage-upload-fixture','X-Amz-Date':'20261018T000000Z'}:{})}}}:{mode:'local'});
    }
    if(url.pathname==='/v1/references/uploads/direct-upload/complete'){
      completions++;assert.equal(request.postDataBuffer(),null,'completion must not contain file bytes');
      const item={id:String(++counter),name:'Direct.png',url:`/references/test/${counter}?signature=test`,size_bytes:image.length,content_type:'image/png'};
      library.unshift(item);return json(item);
    }
    if(url.pathname==='/v1/references' && request.method()==='POST'){
      uploadCount++;assert.match(request.headers()['content-type'],/^multipart\/form-data; boundary=/);
      assert.ok(request.postDataBuffer().includes(Buffer.from('name="file"')));
      if(delayUpload)await new Promise(resolve=>setTimeout(resolve,delayUpload));
      if(failUpload)return route.fulfill({status:503,json:{error:{message:'Storage unavailable'}}});
      const item={id:String(++counter),name:`Image ${counter}.png`,url:`/references/test/${counter}?signature=test`,size_bytes:image.length,content_type:'image/png'};
      library.unshift(item);return json(item);
    }
    if(url.pathname==='/v1/references'){
      const offset=Number(url.searchParams.get('offset') || 0),data=library.slice(offset,offset+2);
      return json({data,has_more:offset+data.length<library.length,next_offset:offset+data.length});
    }
    if(url.pathname.startsWith('/v1/references/') && request.method()==='DELETE'){
      if(failDelete)return route.fulfill({status:503,json:{error:{message:'Delete denied'}}});
      library=library.filter(item=>item.id!==url.pathname.split('/').pop());return route.fulfill({status:204});
    }
    return json({});
  });
  const waitMessage=text=>page.waitForFunction(text=>document.querySelector('#referenceStatus').textContent.includes(text),text);
  async function drop(selector,name='dropped.png'){
    const transfer=await page.evaluateHandle(({bytes,name})=>{const d=new DataTransfer();d.items.add(new File([new Uint8Array(bytes)],name,{type:'image/png'}));return d;},{bytes:[...image],name});
    await page.locator(selector).dispatchEvent('drop',{dataTransfer:transfer});await transfer.dispose();
  }
  try {
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.locator('#createGenerationKey').click();
    await page.waitForFunction(()=>selectedModel()?.id==='video');
    await page.locator('#chooseReferences').click();await waitMessage('empty');
    await page.getByRole('button',{name:'Done',exact:true}).click();
    await drop('#referenceDropzone');await waitMessage('1 file saved');
    assert.equal(await page.locator('#selectedReferences .reference-card').count(),1);
    const first=await page.locator('#imageURLs').inputValue();assert.match(first,/^http:\/\/127\.0\.0\.1:\d+\/references\//);
    await page.locator('#referenceFiles').setInputFiles({name:'selected.png',mimeType:'image/png',buffer:image});await waitMessage('1 file saved');
    await page.waitForFunction(()=>document.querySelectorAll('#selectedReferences .reference-card').length===2);
    const beforeReorder=uploadCount;
    await page.locator('#selectedReferences .reference-card').nth(1).dragTo(page.locator('#selectedReferences .reference-card').first());
    assert.equal(uploadCount,beforeReorder,'Reordering must not upload a duplicate');
    assert.equal((await page.locator('#imageURLs').inputValue()).split('\n')[1],first);
    await drop('#selectedReferences .reference-card:first-child','replacement.png');
    await waitMessage('1 file saved');await page.waitForFunction(()=>document.querySelector('#imageURLs').value.includes('/3?'));
    assert.equal(await page.locator('#selectedReferences .reference-card').count(),2);
    await page.locator('#chooseReferences').click();await page.waitForFunction(()=>document.querySelectorAll('#referenceLibrary .reference-card').length===2);
    await page.getByRole('button',{name:'Load more',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('#referenceLibrary .reference-card').length===3);
    await page.getByRole('button',{name:'Use Image 2.png in generation',exact:true}).click();await waitMessage('up to 2');
    await page.screenshot({path:path.join(output,'library-desktop.png')});
    failDelete=true;
    await page.getByRole('button',{name:'Delete Image 1.png from library',exact:true}).click();
    await page.getByRole('button',{name:'Confirm delete Image 1.png',exact:true}).click();await waitMessage('Could not delete');
    assert.equal(await page.locator('#referenceLibrary .reference-card').count(),3);
    failDelete=false;
    await page.getByRole('button',{name:'Delete Image 1.png from library',exact:true}).click();
    await page.getByRole('button',{name:'Confirm delete Image 1.png',exact:true}).click();await waitMessage('File deleted');
    assert.ok(!(await page.locator('#imageURLs').inputValue()).includes(first));
    await page.getByRole('button',{name:'Use Image 2.png in generation',exact:true}).click();
    await page.getByRole('button',{name:'Done',exact:true}).click();
    await page.locator('#prompt').fill('A short free test generation');
    await page.locator('#submitButton').click();await page.locator('#confirmDialog').waitFor({state:'visible'});
    const summary=await page.locator('#confirmFields').innerText();assert.ok(summary.includes('Image 3.png'));assert.ok(!summary.includes('signature='));
    await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);
    assert.equal(posts.length,1);assert.equal(posts[0].input_reference.length,2);assert.equal(posts[0].model.split('/')[0],'mock');
    await page.locator('#model').selectOption(JSON.stringify(['mock','image']));
    await page.locator('#submitButton').click();await waitMessage('up to 1');
    await page.locator('#selectedReferences .reference-remove').first().click();
    await page.locator('#selectedReferences .reference-remove').first().click();
    await page.locator('#submitButton').click();await waitMessage('at least 1');
    const uploadsBefore=uploadCount;
    await page.locator('#referenceFiles').setInputFiles({name:'bad.svg',mimeType:'image/svg+xml',buffer:Buffer.from('<svg/>')});await waitMessage('supported image');assert.equal(uploadCount,uploadsBefore);
    failUpload=true;await drop('#referenceDropzone');await waitMessage('Storage unavailable');assert.equal(await page.locator('#selectedReferences .reference-card').count(),0);failUpload=false;
    delayUpload=600;await drop('#referenceDropzone');await page.locator('#submitButton').click();await waitMessage('Wait for');await waitMessage('1 file saved');delayUpload=0;
    await page.setViewportSize({width:390,height:844});
    await page.locator('#chooseReferences').click();await page.waitForFunction(()=>!document.querySelector('#referenceStatus').textContent.includes('Loading'));
    await page.screenshot({path:path.join(output,'library-mobile.png')});
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'page overflows on mobile');
    assert.ok(await page.locator('#referenceLibrary').evaluate(node=>node.scrollWidth<=node.clientWidth),'dialog overflows on mobile');
    await page.keyboard.press('Escape');assert.equal(await page.locator('#referenceLibrary').isVisible(),false);
    await page.screenshot({path:path.join(output,'picker-mobile.png')});
    direct=true;
    const proxiedBefore=uploadCount;
    await drop('#referenceDropzone','direct.png');await waitMessage('1 file saved');
    assert.equal(directPuts,1);assert.equal(completions,1);assert.equal(uploadCount,proxiedBefore,'S3 bytes went through the gateway');
    headerSigned=true;await page.evaluate(()=>document.querySelector('#referenceStatus').textContent='');await drop('#referenceDropzone','b2-direct.png');await waitMessage('1 file saved');
    assert.equal(directPuts,2);assert.equal(completions,2);assert.equal(uploadCount,proxiedBefore,'Header-signed B2 bytes went through the gateway');
    blockDirect=true;await drop('#referenceDropzone','failed-direct.png');await waitMessage('bucket CORS');
    assert.equal(uploadCount,proxiedBefore,'failed direct upload fell back to proxy');assert.equal(completions,2);blockDirect=false;
    // Switching workspace while the quota check awaits must prevent preparation.
    const beforeCancelled=preparations;
    await page.evaluate(()=>{window.savedQuota=window.StorageQuota.check;window.StorageQuota.check=()=>new Promise(resolve=>{window.releaseQuota=resolve;});});
    await drop('#referenceDropzone','cancelled.png');await page.waitForFunction(()=>!!window.releaseQuota);
    await page.evaluate(()=>{window.referencePicker.reset();window.StorageQuota.check=window.savedQuota;window.releaseQuota();});
    await page.waitForTimeout(100);
    assert.equal(preparations,beforeCancelled,'upload crossed workspace reset');
    // The worker hashes large files in bounded chunks and can be cancelled.
    const hash=await page.evaluate(async()=>hashReferenceFile(new Blob([new Uint8Array(3*1024*1024+13)]),new AbortController().signal));
    assert.equal(hash,crypto.createHash('sha256').update(Buffer.alloc(3*1024*1024+13)).digest('hex'));
    assert.equal(await page.evaluate(async()=>{const c=new AbortController();const p=hashReferenceFile(new Blob([new Uint8Array(10*1024*1024)]),c.signal);c.abort();try{await p;return false;}catch(e){return e.name==='AbortError';}}),true);
    await page.reload();await page.waitForFunction(()=>selectedModel()?.id==='video');
    await page.locator('#chooseReferences').click();await page.waitForFunction(()=>document.querySelectorAll('#referenceLibrary .reference-card').length>0);
    assert.ok(library.length>0);
    await page.keyboard.press('Escape');
    await page.locator('#model').selectOption(JSON.stringify(['mock','assets']));
    const assetImage='asset://asset-example-character',assetVideo='asset://asset-example-video',assetAudio='asset://asset-example-audio';
    for(const [kind,value] of [['image',`https://example.test/scene.png\n${assetImage}`],['video',assetVideo],['audio',assetAudio]]){
      const container=page.locator(kind==='image'?'#imageURLsField':`#${kind}URLField`);
      await container.locator('summary').click();
      assert.match(await container.locator('summary').innerText(),/registered assets/);
      assert.equal(await container.locator('[data-asset-hint]').isVisible(),true);
      await page.locator(kind==='image'?'#imageURLs':`#${kind}URL`).fill(value);
    }
    await page.locator('#selectedReferences .reference-card').nth(1).focus();
    await page.keyboard.press('Alt+ArrowLeft');
    assert.equal((await page.locator('#imageURLs').inputValue()).split('\n')[0],assetImage);
    assert.match(await page.locator('#selectedReferences .reference-card').first().innerText(),/Registered asset/);
    await page.locator('#prompt').fill('Use the registered character with the scene, video and audio references.');
    for(const width of [1440,390,320]){
      await page.setViewportSize({width,height:1000});
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`asset picker overflows at ${width}px`);
      await page.locator('#imageURLsField').scrollIntoViewIfNeeded();
      await page.screenshot({path:path.join(output,`registered-assets-${width}.png`)});
    }
    await page.locator('#submitButton').click();await page.locator('#confirmDialog').waitFor({state:'visible'});
    assert.match(await page.locator('#confirmFields').innerText(),/asset:\/\/asset-example-character/);
    await page.locator('#confirmSubmit').click();await page.waitForFunction(()=>!document.querySelector('#confirmDialog').open);
    assert.equal(posts.length,2);
    assert.deepEqual(posts[1].input_reference,[assetImage,'https://example.test/scene.png']);
    assert.deepEqual(posts[1].options.video_urls,[assetVideo]);assert.deepEqual(posts[1].options.audio_urls,[assetAudio]);
    assert.equal(posts[1].options.reference_mode,'references');
    await page.locator('#referenceMode').selectOption('first_last_frames');
    assert.match(await page.locator('#selectedReferences .reference-card').first().innerText(),/First frame/);
    assert.match(await page.locator('#selectedReferences .reference-card').nth(1).innerText(),/Last frame/);
    await page.locator('#model').selectOption(JSON.stringify(['mock','image']));
    assert.equal(await page.locator('#imageURLsField [data-asset-hint]').isVisible(),false);
    assert.equal(await page.locator('#imageURLsField summary').innerText(),'Use image URLs');
    assert.deepEqual(errors,[]);
    console.log('Registered assets passed: mixed image/video/audio references, unchanged request URIs, keyboard reorder, frame roles, capability hints and 1440/390/320px layout.');
    console.log('Reference UI passed: direct S3 PUT without gateway credentials, no proxy fallback, chunked hashing, workspace cancellation, CSP, upload, drop, replace, reorder, library, pagination, limits, deletion, failures, generation, persistence and mobile.');
  } finally {await browser.close();server.close();storageServer.close();fs.rmSync(tlsDir,{recursive:true,force:true});}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
