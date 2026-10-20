const {evidencePath} = require('./evidence.cjs');
// Isolated fixture for the shared storage editor; no external storage requests.
const {chromium,webkit}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui');
const server=http.createServer((req,res)=>{
 const name=path.basename(req.url.split('?')[0]);
 if(name==='fixture')return res.end('<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"></head><body><main style="padding:24px;max-width:960px;margin:auto" id="host"></main><script src="/control-pickers.js"></script><script src="/form-ui.js"></script><script src="/storage-settings.js"></script></body></html>');
 const file=path.join(root,name);if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript; charset=utf-8':'text/css; charset=utf-8');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const engine=process.env.UI_BROWSER_ENGINE||'chromium';
 const browser=await (engine==='webkit'?webkit:chromium).launch({headless:true,...(process.env.UI_BROWSER_PATH?{executablePath:process.env.UI_BROWSER_PATH}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1100}});page.setDefaultTimeout(6000);const errors=[];page.on('pageerror',e=>{if(engine==='webkit'&&e.message==='ResizeObserver loop completed with undelivered notifications.')console.log('WebKit ResizeObserver notification');else errors.push(e.message);});
 try{
  await page.goto(`http://127.0.0.1:${server.address().port}/fixture`);
  await page.evaluate(()=>{
   window.storageFixture={driver:'s3',endpoint:'https://account.r2.cloudflarestorage.com',region:'auto',bucket:'files',prefix:'',path_style:true,access_key_id:'fixture-access',credentials_configured:true};window.writes=[];
   window.corsFixture=null;window.corsPending=0;
   window.api=async (url,options)=>{
    let response;
    if(options){
     const body=JSON.parse(options.body);writes.push({url,body,method:options.method});
     if(options.method==='POST'&&(corsFixture||corsPending>0)){
      if(corsPending>0)corsPending--;
      const error=new Error('Browser uploads need setup. Save the connection to let Gateway configure browser access.');error.code='storage_browser_setup_required';throw error;
     }
     if(options.method==='PUT'){
      if(corsFixture==='denied'){const error=new Error('Gateway could not update bucket CORS. Allow this key to update bucket CORS (Backblaze B2 key capabilities: readBuckets and writeBuckets), then save again');error.code='storage_configuration_invalid';throw error;}
      const {secret_access_key,...settings}=body;storageFixture={...storageFixture,...settings,credentials_configured:!!secret_access_key||storageFixture.credentials_configured};
      if(corsFixture){corsFixture=null;corsPending=1;response={...storageFixture,browser_uploads_pending:true};}
     }
    }
    return response||(!options&&url.includes('/organizations/')?{settings:storageFixture,can_manage:true,gateway_allowance_bytes:null}:storageFixture);
   };
   window.openStorage=async scope=>{const host=document.querySelector('#host');host.replaceChildren();await StorageSettings.open(host,{base:scope==='organization'?'/v1/organizations/fixture/storage':`/v1/${scope}/storage`,heading:FormUI.sectionHeading(host,'Storage'),organization:scope==='organization',allowLocal:scope!=='organization'});};
  });
  const field=name=>page.locator(`[name="${name}"]`),picker=()=>page.getByRole('combobox',{name:'Storage provider',exact:true});
  const choose=async name=>{await picker().click();await page.getByRole('option',{name,exact:true}).click();};
  const save=()=>page.getByRole('button',{name:'Save',exact:true});
  const stored=async()=>{await save().click();await page.getByRole('button',{name:'Saved',exact:true}).waitFor();};
  const credentials=async()=>{await field('access_key_id').fill('fixture-key-id');await field('secret_access_key').fill('fixture-secret');};
  const visible=async(provider,names)=>{
   const fields=await page.locator('.storage-s3-fields input:visible').evaluateAll(nodes=>nodes.map(n=>n.name).sort());assert.deepEqual(fields,names.sort(),provider+' visible inputs');
   for(const name of ['endpoint','region'])assert.equal(await field(name).isDisabled(),!names.includes(name),provider+' hidden field disabled');
   assert.equal(await field('prefix').isVisible(),false,'Optional prefix starts collapsed');
   assert.equal(await field('secret_access_key').getAttribute('autocomplete'),'off');
  };
  for(const scope of ['admin','system','organization']){
   await page.evaluate(s=>openStorage(s),scope);await picker().waitFor();
   assert.equal(await field('service').inputValue(),'r2');assert.equal(await page.getByRole('button',{name:'Save',exact:true}).isVisible(),false);
   await picker().focus();await page.keyboard.press('ArrowDown');await page.getByRole('option',{name:'Amazon S3',exact:true}).waitFor();await page.keyboard.press('Escape');assert.equal(await picker().getAttribute('aria-expanded'),'false');
   await visible('r2',['endpoint','bucket','access_key_id','secret_access_key']);
   await choose('Amazon S3');assert.equal(await field('endpoint').inputValue(),'');assert.equal(await field('region').inputValue(),'us-east-1');assert.equal(await field('path_style').isChecked(),false);
   await visible('aws',['region','bucket','access_key_id','secret_access_key']);
   await credentials();await field('region').fill('eu-central-1');await stored();
   assert.equal(await page.evaluate(()=>writes.at(-1).body.endpoint),'');assert.equal(await page.evaluate(()=>writes.at(-1).body.path_style),false);
   await page.evaluate(s=>openStorage(s),scope);assert.equal(await field('service').inputValue(),'aws');
   await choose('Backblaze B2');assert.equal(await field('region').inputValue(),'');assert.equal(await field('path_style').isChecked(),true);
   await visible('b2',['endpoint','bucket','access_key_id','secret_access_key']);
   assert.equal(await page.getByLabel('keyID',{exact:true}).count(),1);assert.equal(await page.getByLabel('applicationKey',{exact:true}).count(),1);
   assert.match(await page.locator('.storage-provider-help').innerText(),/keyName is only a label and is not needed/);
   assert.doesNotMatch(await page.locator('.storage-s3-fields').innerText(),/AWS|Access key ID|Secret access key|Use path-style/);
   assert.equal(await field('access_key_id').inputValue(),'');assert.equal(await field('secret_access_key').inputValue(),'');
   const count=await page.evaluate(()=>writes.length);
   await save().click();assert.equal(await page.evaluate(()=>writes.length),count,'Incomplete B2 form cannot save');
   await field('endpoint').fill('s3.us-west-004.backblazeb2.com');assert.equal(await field('region').inputValue(),'us-west-004');
   await credentials();await field('bucket').fill('');await save().click();assert.equal(await page.evaluate(()=>writes.length),count,'Bucket is required');await field('bucket').fill('files');
   await field('endpoint').fill('https://s3.us-east-005.backblazeb2.com');assert.equal(await field('region').inputValue(),'us-east-005');
   await field('endpoint').fill('https://s3.us-west-004.backblazeb2.com.evil.test');await save().click();assert.equal(await page.evaluate(()=>writes.length),count,'Wrong provider endpoint cannot save');
   assert.match(await field('endpoint').evaluate(n=>n.validationMessage),/Backblaze/);
   await field('endpoint').fill('s3.us-west-004.backblazeb2.com/');
   // Keyboard submission uses the same normalized/derived settings as connection checks.
   await page.getByRole('button',{name:'Test connection',exact:true}).click();await page.getByText('Connection verified.',{exact:true}).waitFor();
   const checked=await page.evaluate(()=>writes.at(-1));assert.ok(checked.url.endsWith('/check'));
   await field('endpoint').press('Enter');await page.getByRole('button',{name:'Saved',exact:true}).waitFor();
   const post=await page.evaluate(()=>writes.at(-1));assert.deepEqual(post.body,checked.body);assert.equal(post.body.driver,'s3');assert.equal(post.body.secret_access_key,'fixture-secret');assert.equal(post.body.access_key_id,'fixture-key-id');assert.equal(post.body.region,'us-west-004');assert.equal(post.body.endpoint,'https://s3.us-west-004.backblazeb2.com');assert.equal('service' in post.body,false);
   await page.evaluate(s=>openStorage(s),scope);await picker().waitFor();assert.equal(await field('service').inputValue(),'b2');
   assert.equal(await field('secret_access_key').inputValue(),'************');assert.equal(await save().isVisible(),false);
   // An unchanged saved connection can repair browser access without exposing Save.
   const setup=()=>page.getByRole('button',{name:'Set up browser uploads',exact:true});
   assert.equal(await setup().isVisible(),false);
   await page.evaluate(()=>corsFixture='missing');
   const putsBefore=await page.evaluate(()=>writes.filter(w=>w.method==='PUT').length);
   await page.getByRole('button',{name:'Test connection',exact:true}).click();await setup().waitFor();
   assert.equal(await page.evaluate(()=>writes.filter(w=>w.method==='PUT').length),putsBefore,'Test connection must not configure CORS');
   assert.equal(await save().isVisible(),false,'Repair does not require editing saved fields');
   for(const width of [1440,390,320]){
    await page.setViewportSize({width,height:1100});await setup().scrollIntoViewIfNeeded();
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Repair action overflow');
    await page.screenshot({path:evidencePath(`/tmp/gateway-storage-cors-${engine}-${scope}-${width}.png`),fullPage:true});
   }
   await page.setViewportSize({width:1440,height:1100});
   await page.evaluate(()=>corsFixture='denied');await setup().click();
   await page.getByText(/Allow this key to update bucket CORS/).waitFor();
   assert.equal(await field('secret_access_key').inputValue(),'************','Failed CORS setup preserves stored credentials');
   await page.evaluate(()=>corsFixture='missing');await page.getByRole('button',{name:'Test connection',exact:true}).click();await setup().waitFor();
   const repairPuts=await page.evaluate(()=>writes.filter(w=>w.method==='PUT').length);
   await setup().focus();await page.keyboard.press('Enter');
   await page.getByText('Settings saved. Waiting for browser uploads to become available…',{exact:true}).waitFor();
   await page.getByText('Browser uploads are ready.',{exact:true}).waitFor();
   assert.equal(await page.evaluate(()=>writes.filter(w=>w.method==='PUT').length),repairPuts+1,'Propagation polls must not write CORS repeatedly');
   assert.equal(await setup().isVisible(),false);
   assert.equal(await page.evaluate(()=>writes.filter(w=>w.method==='PUT').at(-1).body.secret_access_key),'','Repair reuses saved credentials');
   await page.getByText('Optional settings',{exact:true}).click();await field('prefix').fill('/media-'+scope+'/');await stored();assert.equal(await page.evaluate(()=>writes.at(-1).body.secret_access_key),'');assert.equal(await page.evaluate(()=>writes.at(-1).body.prefix),'media-'+scope);
   await page.evaluate(s=>openStorage(s),scope);
   await choose('Custom S3');await field('endpoint').fill('storage.example.test');await field('region').fill('region-1');
   await visible('custom',['endpoint','region','bucket','access_key_id','secret_access_key','path_style']);
   await field('path_style').focus();await page.keyboard.press('Space');assert.equal(await field('path_style').isChecked(),false);await credentials();
   await stored();assert.equal(await page.evaluate(()=>storageFixture.endpoint),'https://storage.example.test');
   await page.evaluate(s=>openStorage(s),scope);await picker().waitFor();assert.equal(await field('service').inputValue(),'custom');
   assert.equal(await field('path_style').isChecked(),false,'Saved custom addressing is preserved');
   await choose('Cloudflare R2');assert.equal(await field('region').inputValue(),'auto');await field('endpoint').fill('https://account.r2.cloudflarestorage.com');
   await credentials();await stored();assert.equal(await page.evaluate(()=>writes.at(-1).body.region),'auto');assert.equal(await page.evaluate(()=>writes.at(-1).body.path_style),true);
  }
  // Existing endpoint/credential/addressing settings are retained on bucket-only edits.
  await page.evaluate(()=>{storageFixture={...storageFixture,endpoint:'https://s3.us-west-004.backblazeb2.com',region:'us-west-004',path_style:false};return openStorage('system');});
  await field('bucket').fill('another-bucket');await stored();assert.equal(await page.evaluate(()=>writes.at(-1).body.path_style),false);assert.equal(await page.evaluate(()=>writes.at(-1).body.secret_access_key),'');
  // Required invalid cloud fields cannot block switching to managed/local storage.
  await field('endpoint').fill('invalid');await page.locator('[name=destination][value=local]').check();await stored();assert.deepEqual(await page.evaluate(()=>writes.at(-1).body),{driver:'local'});
  for(const provider of ['Amazon S3','Cloudflare R2','Backblaze B2','Custom S3']){
   await page.locator('[name=destination][value=s3]').check();await choose(provider);
   for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);await page.setViewportSize({width,height:1100});await picker().scrollIntoViewIfNeeded();await picker().click();
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'storage layout overflow');
   await page.keyboard.press('Escape');await page.screenshot({path:evidencePath(`/tmp/gateway-storage-${engine}-${provider.replaceAll(' ','-')}-${theme}-${width}.png`),fullPage:true});
   await field('bucket').focus();await page.keyboard.press('Tab');assert.equal(await field('access_key_id').evaluate(n=>n===document.activeElement),true,'Hidden fields are absent from keyboard navigation');
   }
  }
  assert.deepEqual(errors,[]);console.log(`PASS (${engine}): provider-specific visible fields in personal/system/organization scopes, credential labels, derived regions, validation, check/save/reopen, CORS repair/permissions/propagation, secret preservation, custom addressing, keyboard, both themes and 1440/390/320px layouts`);
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
