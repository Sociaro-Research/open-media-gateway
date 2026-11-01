const {chromium,webkit}=require('playwright'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {evidencePath}=require('./evidence.cjs');
(async()=>{
 const engine=process.env.UI_BROWSER_ENGINE||'chromium',browser=await({chromium,webkit}[engine]).launch({headless:true});
 try{
  const page=await browser.newPage();await page.setContent('<div id="list"></div><div id="detail"></div>');
  await page.addScriptTag({path:path.resolve(__dirname,'../../web/public/ui/auto-update.js')});
  const result=await page.evaluate(()=>{
   const list=document.querySelector('#list'),detail=document.querySelector('#detail');
   function rows(order){const next=document.createElement('div');for(const id of order){const row=document.createElement('article'),button=document.createElement('button');row.dataset.updateKey=id;button.textContent=id;button.onclick=event=>{event.currentTarget.closest('article').dataset.clicked=id;};row.append(button);next.append(row);}return next;}
   GatewayAutoUpdate.reconcile(list,rows(['a','b']));const focused=list.lastChild.firstChild;focused.focus();GatewayAutoUpdate.reconcile(list,rows(['b','a']));focused.click();
   const retained=focused.isConnected&&document.activeElement===focused&&focused.closest('article').dataset.clicked==='b';
   detail.innerHTML='<details open><summary>Retained data</summary><pre>old</pre></details><video controls></video>';
   const disclosure=detail.firstChild,player=detail.lastChild;player.currentTime=4;
   const next=document.createElement('div');next.innerHTML='<details><summary>Retained data</summary><pre>new</pre></details><video controls></video>';
   GatewayAutoUpdate.reconcile(detail,next);
   return {order:[...list.children].map(n=>n.dataset.updateKey),retained,disclosure:disclosure.isConnected&&disclosure.open,text:disclosure.querySelector('pre').textContent,media:detail.lastChild===player&&player.currentTime===4};
  });
  assert.deepEqual(result,{order:['b','a'],retained:true,disclosure:true,text:'new',media:true});
  fs.writeFileSync(evidencePath(`/tmp/gateway-automatic-update-dom/${engine}.json`),JSON.stringify({engine,result},null,2)+'\n');console.log('PASS: keyed reorder keeps focus and live callbacks; automatic updates retain disclosure and media state');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
