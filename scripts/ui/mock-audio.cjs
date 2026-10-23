// Decode and play the exact sample returned by the free mock audio adapter.
const {chromium,webkit}=require('playwright');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const http=require('node:http');
const path=require('node:path');
const {evidencePath}=require('./evidence.cjs');
const engine=process.env.UI_BROWSER_ENGINE||'chromium';
assert.ok(['chromium','webkit'].includes(engine),'Unsupported browser engine');
const sample=fs.readFileSync(path.resolve(__dirname,'../../plugins/provider/mock/assets/sample.mp3'));
const server=http.createServer((req,res)=>{
 if(req.url==='/sample.mp3'){
  res.writeHead(200,{'Content-Type':'audio/mpeg','Content-Length':sample.length});
  return res.end(sample);
 }
 res.writeHead(200,{'Content-Type':'text/html'});
 res.end('<audio controls src="/sample.mp3"></audio><button>Play</button><script>const audio=document.querySelector("audio");audio.onended=()=>window.finished=true;document.querySelector("button").onclick=()=>audio.play().catch(e=>window.playError=e.message);</script>');
});
(async()=>{
 let browser;
 try{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  browser=await ({chromium,webkit}[engine]).launch({headless:true,...(process.env.UI_BROWSER_PATH&&engine==='chromium'?{executablePath:process.env.UI_BROWSER_PATH}:{})});
  const page=await browser.newPage();
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto('http://127.0.0.1:'+server.address().port);
  const decoded=await page.evaluate(async()=>{
   const context=new AudioContext({sampleRate:44100});
   try{
    const response=await fetch('/sample.mp3');
    const data=await response.arrayBuffer();
    const audio=await context.decodeAudioData(data.slice(0));
    let peak=0,energy=0;
    for(let channel=0;channel<audio.numberOfChannels;channel++){
     const samples=audio.getChannelData(channel);
     for(const value of samples){peak=Math.max(peak,Math.abs(value));energy+=value*value;}
    }
    return {bytes:data.byteLength,mime:response.headers.get('content-type'),duration:audio.duration,channels:audio.numberOfChannels,sampleRate:audio.sampleRate,peak,rms:Math.sqrt(energy/(audio.length*audio.numberOfChannels))};
   }finally{await context.close();}
  });
  assert.equal(decoded.bytes,24076);assert.equal(decoded.mime,'audio/mpeg');
  assert.equal(decoded.channels,2);assert.equal(decoded.sampleRate,44100);
  assert.ok(decoded.duration>0.35&&decoded.duration<0.7,JSON.stringify(decoded));
  assert.ok(decoded.peak>0.05&&decoded.rms>0.005,'Decoded sample must contain an audible nonzero signal');
  await page.getByRole('button',{name:'Play',exact:true}).click();
  await page.waitForFunction(()=>window.finished||window.playError,{},{timeout:8000});
  const playback=await page.locator('audio').evaluate(audio=>({duration:audio.duration,currentTime:audio.currentTime,ended:audio.ended,error:audio.error?.message||window.playError||null}));
  assert.equal(playback.error,null);assert.equal(playback.ended,true);
  assert.ok(playback.currentTime>0.35&&playback.duration<0.7);
  assert.deepEqual(errors,[]);
  const result={engine,decoded,playback};
  const out=evidencePath('/tmp/gateway-mock-audio/'+engine+'.json');
  fs.mkdirSync(path.dirname(out),{recursive:true});fs.writeFileSync(out,JSON.stringify(result,null,2)+'\n');
  console.log('PASS: '+JSON.stringify(result));
 }finally{if(browser)await browser.close();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
