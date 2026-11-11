// Real shared UI with controlled async API outcomes; synthetic loopback only.
const {[process.env.UI_BROWSER_ENGINE||'chromium']:engine}=require('playwright');
const assert=require('node:assert/strict'),crypto=require('node:crypto'),fs=require('node:fs'),http=require('node:http'),path=require('node:path');
const root=path.resolve(process.env.UI_SOURCE_DIR||path.join(__dirname,'../../web/public/ui')),out=process.env.UI_EVIDENCE_DIR||'/tmp/gateway-legal-footer-state';
const assets=new Map(['styles.css','form-ui.js','file-size.js','legal.js'].map(name=>[name,fs.readFileSync(path.join(root,name))]));
const document='<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><link rel="stylesheet" href="/styles.css"></head><body><main id="fixture"></main><script src="/form-ui.js"></script><script src="/file-size.js"></script><script src="/legal.js"></script><script>'+
`window.footerFixture={gets:0,puts:0,completed:0,holder:'Initial holder',version:1,getStatus:200,putStatus:200,holdGet:false,holdPut:false,pending:[],applied:[]};
let epoch=0;const fixture=document.querySelector('#fixture'),state=window.footerFixture;
window.GatewaySite={applyLegalFooter:value=>{if(state.failRender)throw Error('SYNTHETIC_PRIVATE_RENDER_ERROR');state.applied.push({...value})}};
const api=async(url,options={})=>{
 if(url!=='/v1/system/legal/footer')throw Error('Unexpected fixture request');
 const put=options.method==='PUT';put?state.puts++:state.gets++;
 const status=put?state.putStatus:state.getStatus;
 if(put&&(status===200||state.acceptOnError)&&state.acceptPut!==false){state.holder=JSON.parse(options.body).copyright_holder;state.version++}
 const reply=put?'putReply':'getReply',value=Object.hasOwn(state,reply)?state[reply]:{copyright_holder:state.holder,version:state.version};
 const finish=()=>{state.completed++;if(status!==200){const e=Error('SYNTHETIC_PRIVATE_ERROR');e.status=status;throw e}return value};
 if(put?state.holdPut:state.holdGet)return new Promise((resolve,reject)=>state.pending.push(()=>{try{resolve(finish())}catch(e){reject(e)}}));
 return finish();
};
state.mount=()=>{const token=++epoch;fixture.replaceChildren();LegalTextUI.mount(fixture,api,()=>token===epoch)};
state.leave=()=>{epoch++;fixture.replaceChildren(document.createTextNode('Next view'))};
state.release=()=>state.pending.shift()();
</script></body></html>`;
const server=http.createServer((req,res)=>{
 if(req.url==='/fixture'){res.setHeader('Content-Type','text/html');return res.end(document)}
 const name=path.basename(req.url.split('?')[0]),data=assets.get(name);
 if(!data)return res.writeHead(404).end();
 res.setHeader('Content-Type',name.endsWith('.css')?'text/css':'text/javascript');res.end(data);
});
(async()=>{
 fs.mkdirSync(out,{recursive:true});await new Promise(r=>server.listen(0,'127.0.0.1',r));
 fs.writeFileSync(path.join(out,'source.json'),JSON.stringify({root,harness_sha256:crypto.createHash('sha256').update(fs.readFileSync(__filename)).digest('hex'),assets:Object.fromEntries([...assets].map(([name,data])=>[name,crypto.createHash('sha256').update(data).digest('hex')]))},null,2));
 const browser=await engine.launch({headless:true,executablePath:process.env.UI_BROWSER_PATH}),page=await browser.newPage({viewport:{width:1000,height:900}}),errors=[],results=[];
 const base='http://127.0.0.1:'+server.address().port;
 await page.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
 page.setDefaultTimeout(6000);page.on('pageerror',e=>errors.push(e.message));
 const form=page.locator('.legal-footer-settings'),input=form.getByLabel('Copyright holder',{exact:true}),save=form.getByRole('button',{name:'Save',exact:true});
 const ready=()=>page.waitForFunction(()=>{const input=document.querySelector('.legal-footer-settings input');return !!input&&!input.disabled});
 const frames=()=>page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 const visual=async name=>{
  if(!process.env.UI_FOOTER_VISUAL)return;
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:900});await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);await frames();
   await page.locator('.settings-form-group').screenshot({path:path.join(out,name+'-'+theme+'-'+width+'.png')});
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'footer state overflow '+name+' '+theme+' '+width);
  }
  await page.setViewportSize({width:1000,height:900});await frames();
 };
 const run=async(name,check)=>{
  await page.goto(base+'/fixture');
  try{await check();results.push({name,result:'pass'})}
  catch(e){await page.screenshot({path:path.join(out,name+'.png'),fullPage:true});results.push({name,result:'fail',error:String(e.stack||e)});console.error(name,e)}
  finally{fs.writeFileSync(path.join(out,'results.json'),JSON.stringify(results,null,2))}
 };
 try{
  for(const status of [401,403])await run('terminal-'+status,async()=>{
   await page.evaluate(status=>{footerFixture.getStatus=status;footerFixture.mount()},status);
   await page.waitForFunction(()=>footerFixture.gets===1);await new Promise(r=>setTimeout(r,1700));
   assert.equal(await page.evaluate(()=>footerFixture.gets),1,'permanent auth failures must not auto-retry');
   assert.equal(await input.isDisabled(),true);assert.equal(await page.evaluate(()=>footerFixture.puts),0);
   const message=await form.innerText();assert.match(message,status===401?/sign in again/i:/Administrator/);assert.ok(!message.includes('SYNTHETIC_PRIVATE_ERROR'));
   if(status===403)await visual('permission-denied');
  });
  await run('loaded-editor-failure',async()=>{
   await page.evaluate(()=>{footerFixture.failRender=true;footerFixture.mount()});
   await page.waitForFunction(()=>footerFixture.gets===1);await new Promise(r=>setTimeout(r,1700));
   assert.equal(await page.evaluate(()=>footerFixture.gets),1);assert.match(await form.innerText(),/editor could not initialize/);assert.ok(!(await form.innerText()).includes('SYNTHETIC_PRIVATE_RENDER_ERROR'));
   assert.equal(await input.isDisabled(),true);assert.equal(await page.evaluate(()=>footerFixture.puts),0);
  });
  await run('transient-recovery',async()=>{
   await page.evaluate(()=>{footerFixture.getStatus=503;footerFixture.mount()});
   await form.getByText(/Retrying automatically/).waitFor();
   await page.evaluate(()=>{footerFixture.getStatus=200});await ready();
   assert.equal(await input.inputValue(),'Initial holder');assert.equal(await page.evaluate(()=>footerFixture.gets),2);assert.equal(await page.evaluate(()=>footerFixture.puts),0);
  });
  for(const [name,reply]of [['null',null],['wrong-shape',{copyright_holder:42,version:1,message:'SYNTHETIC_PRIVATE_ERROR'}]])await run('malformed-get-'+name,async()=>{
   await page.evaluate(reply=>{footerFixture.getReply=reply;footerFixture.mount()},reply);
   await page.waitForFunction(()=>footerFixture.gets===1);await new Promise(r=>setTimeout(r,1700));
   assert.equal(await page.evaluate(()=>footerFixture.gets),1,'malformed GET responses require an operator check, not repeated reads');
   assert.match(await form.innerText(),/server version and logs|server version and logs,|server version.*logs/);
   assert.ok(!(await form.innerText()).includes('SYNTHETIC_PRIVATE_ERROR'));assert.equal(await input.isDisabled(),true);assert.equal(await page.evaluate(()=>footerFixture.puts),0);
  });
  for(const [name,separator]of [['line-separator','\u2028'],['paragraph-separator','\u2029']])await run(name,async()=>{
   await page.evaluate(()=>footerFixture.mount());await ready();
   await input.fill('First'+separator+'Second');
   assert.equal(await input.evaluate(node=>node.checkValidity()),false,'single-line input must reject Unicode separators');
   await form.evaluate(node=>node.requestSubmit());await frames();assert.equal(await page.evaluate(()=>footerFixture.puts),0);
  });
  await run('failed-conflict-refresh',async()=>{
   await page.evaluate(()=>footerFixture.mount());await ready();
   await input.fill('My retained draft');await page.evaluate(()=>{footerFixture.putStatus=409;footerFixture.getStatus=503});await save.click();
   await form.getByText(/Retrying automatically/).waitFor();
   assert.equal(await input.inputValue(),'My retained draft');assert.equal(await page.evaluate(()=>footerFixture.puts),1);
   await page.evaluate(()=>{footerFixture.getStatus=200;footerFixture.holder='Changed elsewhere';footerFixture.version=2});await ready();
   assert.equal(await input.inputValue(),'My retained draft');assert.equal(await page.evaluate(()=>footerFixture.puts),1,'refresh must not automatically PUT');
   await page.evaluate(()=>{footerFixture.putStatus=200});await save.click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();
   assert.equal(await page.evaluate(()=>footerFixture.puts),2,'only explicit Save retries the mutation');
  });
  for(const status of [401,403])await run('save-permission-'+status,async()=>{
   await page.evaluate(()=>footerFixture.mount());await ready();await input.fill('My permission draft');await page.evaluate(status=>{footerFixture.putStatus=status},status);await save.click();await frames();
   assert.equal(await input.inputValue(),'My permission draft');assert.match(await form.innerText(),status===401?/Sign in again/:/Administrator access/);assert.ok(!(await form.innerText()).includes('SYNTHETIC_PRIVATE_ERROR'));assert.equal(await page.evaluate(()=>footerFixture.puts),1);assert.equal(await page.evaluate(()=>footerFixture.gets),1);
  });
  for(const tc of [
   {name:'null',reply:null,accepted:true},
   {name:'wrong-shape',reply:{copyright_holder:42,version:2,message:'SYNTHETIC_PRIVATE_ERROR'},accepted:false},
   {name:'wrong-value',reply:{copyright_holder:'Different holder',version:2},accepted:true},
   {name:'wrong-version',reply:{copyright_holder:'My uncertain draft',version:9},accepted:true},
   {name:'network',status:0,accepted:true},
   {name:'server-failure',status:503,accepted:true}
  ])await run('unconfirmed-put-'+tc.name,async()=>{
   await page.evaluate(()=>footerFixture.mount());await ready();await input.fill('My uncertain draft');
   await page.evaluate(tc=>{if(Object.hasOwn(tc,'reply'))footerFixture.putReply=tc.reply;footerFixture.putStatus=tc.status??200;footerFixture.acceptPut=tc.accepted;footerFixture.acceptOnError=tc.status!==undefined},tc);await save.click();await frames();
   assert.equal(await input.inputValue(),'My uncertain draft','an unconfirmed response must not replace the draft');
   assert.match(await form.innerText(),/could not be confirmed.*may have accepted/s);assert.ok(!(await form.innerText()).includes('SYNTHETIC_PRIVATE_ERROR'));
   assert.equal(await form.locator('button[type=submit]').isDisabled(),true);assert.equal(await page.evaluate(()=>footerFixture.puts),1);assert.equal(await page.evaluate(()=>footerFixture.applied.length),1);
   if(tc.name==='null')await visual('unconfirmed-save');
   if(tc.name==='null')await page.evaluate(()=>{footerFixture.getStatus=503});
   await form.getByRole('button',{name:'Check saved footer',exact:true}).click();
   if(tc.name==='null'){await form.getByText(/Retrying automatically/).waitFor();await page.evaluate(()=>{footerFixture.getStatus=200})}
   await ready();assert.equal(await input.inputValue(),'My uncertain draft');assert.equal(await page.evaluate(()=>footerFixture.puts),1,'checking saved state must never PUT');
   if(tc.accepted){assert.match(await form.innerText(),/matches your draft/);await form.evaluate(node=>node.requestSubmit());await frames();assert.equal(await page.evaluate(()=>footerFixture.puts),1)}
   else{await page.evaluate(()=>{delete footerFixture.putReply;footerFixture.acceptPut=true});await save.click();await form.getByRole('button',{name:'Saved',exact:true}).waitFor();assert.equal(await page.evaluate(()=>footerFixture.puts),2)}
  });
  await run('saved-view-failure',async()=>{
   await page.evaluate(()=>footerFixture.mount());await ready();await input.fill('Confirmed saved draft');await page.evaluate(()=>{footerFixture.failRender=true});await save.click();await frames();
   assert.equal(await page.evaluate(()=>footerFixture.holder),'Confirmed saved draft');assert.match(await form.innerText(),/footer was saved.*view could not be updated/s);assert.ok(!(await form.innerText()).includes('SYNTHETIC_PRIVATE_RENDER_ERROR'));assert.equal(await form.locator('button[type=submit]').isDisabled(),true);assert.equal(await page.evaluate(()=>footerFixture.puts),1);
  });
  for(const status of [200,403])await run('late-get-'+status,async()=>{
   await page.evaluate(status=>{footerFixture.holdGet=true;footerFixture.getStatus=status;footerFixture.mount()},status);
   await page.waitForFunction(()=>footerFixture.pending.length===1);await page.evaluate(()=>{footerFixture.leave();footerFixture.release()});await frames();
   assert.equal(await page.locator('#fixture').innerText(),'Next view');assert.equal(await page.evaluate(()=>footerFixture.applied.length),0);assert.equal(await page.evaluate(()=>footerFixture.puts),0);
  });
  for(const status of [200,409])await run('late-put-'+status,async()=>{
   await page.evaluate(()=>footerFixture.mount());await ready();await input.fill('Old navigation draft');
   await page.evaluate(status=>{footerFixture.holdPut=true;footerFixture.putStatus=status},status);await save.click();await page.waitForFunction(()=>footerFixture.pending.length===1);
   await page.evaluate(()=>{footerFixture.leave();footerFixture.release()});await frames();
   assert.equal(await page.locator('#fixture').innerText(),'Next view');assert.equal(await page.evaluate(()=>footerFixture.applied.length),1,'late mutation reply must not update a different view');assert.equal(await page.evaluate(()=>footerFixture.gets),1,'late conflict must not refetch for a removed view');
  });
  assert.deepEqual(errors,[]);
  assert.equal(results.filter(r=>r.result==='fail').length,0,'footer state regressions failed; see results.json');
  console.log('PASS legal footer state: terminal auth, malformed GET, unconfirmed PUT with explicit saved-state check, transient GET recovery, Unicode separators, failed conflict refresh without automatic PUT and late navigation replies.');
 }finally{await browser.close();server.close()}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
