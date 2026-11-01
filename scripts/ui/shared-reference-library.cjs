// Synthetic localhost media only; the application adapters have their own suites.
const {chromium,webkit}=require('playwright');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const engine=process.env.UI_BROWSER_ENGINE||'chromium',root=path.resolve(__dirname,'../../web/public/ui'),out=path.resolve(process.env.UI_EVIDENCE_DIR||'.local/shared-reference-library-'+engine);
fs.mkdirSync(out,{recursive:true});
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=','base64');
const wav=Buffer.alloc(44+16000);wav.write('RIFF');wav.writeUInt32LE(wav.length-8,4);wav.write('WAVEfmt ',8);wav.writeUInt32LE(16,16);wav.writeUInt16LE(1,20);wav.writeUInt16LE(1,22);wav.writeUInt32LE(8000,24);wav.writeUInt32LE(16000,28);wav.writeUInt16LE(2,32);wav.writeUInt16LE(16,34);wav.write('data',36);wav.writeUInt32LE(16000,40);
const clip=fs.readFileSync(path.resolve(process.env.GATEWAY_TEMPLATE_CLIP_DIR||'.local/video-workflow/clips','cfr24.mp4'));
const server=http.createServer((req,res)=>{
 const name=path.basename(req.url.split('?')[0]);
 if(name==='image.png'){res.setHeader('Content-Type','image/png');return res.end(png);}
 if(name==='audio.wav'){res.setHeader('Content-Type','audio/wav');return res.end(wav);}
 if(name==='video.mp4'){res.setHeader('Content-Type','video/mp4');return res.end(clip);}
 if(req.url==='/'){res.setHeader('Content-Type','text/html');return res.end('<!doctype html><html><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"><link rel="stylesheet" href="/references.css"><body><button id="open">Open library</button><script src="/reference-library.js"></script><script>let generationKeyVersion=1;const items=[{id:"image",name:"image.png",content_type:"image/png",url:"/image.png",size_bytes:68},{id:"audio",name:"audio.wav",content_type:"audio/wav",url:"/audio.wav",size_bytes:16044},{id:"video",name:"video.mp4",content_type:"video/mp4",url:"/video.mp4",size_bytes:2000}];let selected=new Set(),deletes=0;const view=ReferenceLibrary.create({id:"testLibrary",description:"Choose references."});document.body.append(view.dialog);function render(){view.render(items,{selected:i=>selected.has(i.id),onSelect:i=>{selected.has(i.id)?selected.delete(i.id):selected.add(i.id);render()},onDelete:i=>{deletes++;items.splice(items.indexOf(i),1);render()}})}document.getElementById("open").onclick=()=>{render();view.dialog.showModal()};</script></body></html>');}
 const file=path.join(root,name);if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':'text/css');res.end(fs.readFileSync(file));
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true});const page=await browser.newPage(),errors=[],external=[];page.on('pageerror',e=>errors.push(e.message));await page.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():(external.push(r.request().url()),r.abort()));
 try{
  await page.goto(base);await page.locator('#open').click();
  for(const kind of ['video','audio']){await page.getByRole('button',{name:'Preview '+kind+(kind==='video'?'.mp4':'.wav'),exact:true}).click();const player=page.locator(`[data-id="${kind}"] [data-player]`);await player.evaluate(p=>new Promise((resolve,reject)=>{p.onerror=reject;p.onloadedmetadata=resolve;if(p.readyState>=1)resolve()}));await player.evaluate(p=>p.play());assert.equal(await player.evaluate(p=>p.paused),false);assert.equal(await page.evaluate(()=>selected.size),0);}
  const video=await page.locator('[data-id="video"] [data-player]').elementHandle();await page.getByRole('button',{name:'Use image.png in generation',exact:true}).press('Enter');assert.equal(await video.evaluate(p=>p.paused&&!p.hasAttribute('src')),true);assert.equal(await page.locator('[data-id="image"]').getAttribute('data-selected'),'true');
  await page.getByRole('button',{name:'Use image.png in generation',exact:true}).press('Space');assert.equal(await page.locator('[data-id="image"]').getAttribute('data-selected'),'false');
  await page.locator('img').dispatchEvent('error');await page.getByRole('button',{name:'Retry preview',exact:true}).click();await page.locator('img').waitFor();
  await page.getByRole('button',{name:'Delete image.png from library',exact:true}).click();await page.getByRole('button',{name:'Keep image.png',exact:true}).press('Enter');assert.equal(await page.evaluate(()=>deletes),0);
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){await page.setViewportSize({width,height:900});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);assert.equal(await page.locator('dialog').evaluate(n=>n.scrollWidth<=n.clientWidth),true);await page.locator('dialog').screenshot({path:path.join(out,theme+'-'+width+'.png')});}
  await page.getByRole('button',{name:'Preview audio.wav',exact:true}).click();const audio=await page.locator('audio').elementHandle();await page.locator('audio').evaluate(p=>p.play());await page.keyboard.press('Escape');await page.waitForFunction(p=>p.paused&&!p.hasAttribute('src'),audio);assert.equal(await audio.evaluate(p=>p.paused&&!p.hasAttribute('src')),true);await page.locator('#open').click();assert.equal(await page.locator('[data-player]').count(),0);
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);console.log('PASS shared image/audio/video previews, playback separation/disposal, keyboard, delete cancellation and both themes at 1440/390/320');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;server.close();});
