// Production Appearance UI; synthetic assets and loopback-only API writes.
const {chromium,webkit}=require('playwright'),assert=require('node:assert/strict'),fs=require('node:fs'),http=require('node:http'),path=require('node:path'),crypto=require('node:crypto');
const root=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',baseline=process.env.UI_BASELINE==='1',sharedOnly=process.env.UI_SHARED_ONLY==='1',out=path.resolve(process.env.UI_EVIDENCE_DIR||'dist/checks/gw294-'+engine);
const palette={bg:'#163248',panel:'#89456f','panel-2':'#224466',field:'#203040',popup:'#263748',line:'#777777',text:'#eeeeee',muted:'#aaaaaa',accent:'#bbdd22',highlight:'#ccff33','on-accent':'#112233','on-highlight':'#112233',red:'#cc3344'};
const defaults={name:'Preview fixture',description:'',contact_email:'',dark:{...palette},light:{...palette,bg:'#eaf0e1',text:'#151515'},assets:{},site_max_width:1280,content_max_width:null,sidebar_subsections:true,version:1};
let settings=structuredClone(defaults),writes=[],failSave=false;
const server=http.createServer(async(req,res)=>{
 const p=new URL(req.url,'http://fixture').pathname,json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
 if(p==='/v1/system/branding'){if(req.method==='PUT'){let raw='';for await(const c of req)raw+=c;const body=JSON.parse(raw);writes.push(body);if(failSave)return json({error:{message:'Fixture conflict'}},409);settings={...body,version:settings.version+1};return json(settings);}return json({settings,defaults,colors:Object.keys(palette).map(key=>({key,label:key,group:'Palette'}))});}
 if(p==='/shared-preview-matrix'){res.setHeader('Content-Type','text/html');return res.end('<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/ui/styles.css"><link rel="stylesheet" href="/ui/references.css"><link rel="stylesheet" href="/ui/templates.css"><body><script src="/ui/file-size.js"></script><script src="/ui/reference-upload.js"></script>');}
 if(p==='/site.json')return json({branding:settings});
 if(p==='/auth/company-connection')return json({connected:false});
 if(p==='/auth/methods')return json({google:true,email:false,email_delivery:false});
 if(p==='/v1/me')return json({id:'fixture-admin',name:'Fixture Administrator',email:'admin@example.test',is_system_admin:true});
 if(p==='/v1/info')return json({auth_mode:'google',provider:'mock',console:{generation_ui:true,personal_providers:true,version:1}});
 if(p==='/v1/system/settings')return json({generation_ui:true,personal_providers:true,version:1});
 if(p==='/v1/system/markups')return json({system:{default_percent:0,exceptions:[],version:1},company:{default_percent:null,exceptions:[],version:0}});
 if(p==='/v1/system/email')return json({provider:'',from:'',version:1,available_providers:[]});
 if(p.startsWith('/branding/assets/')){const asset=settings.assets[p.split('/').at(-1)];if(!asset)return res.writeHead(404).end();res.setHeader('Content-Type',asset.mime);return res.end(Buffer.from(asset.data,'base64'));}
 if(p.startsWith('/v1/'))return json({data:[]});
 const file=path.join(root,p.startsWith('/ui/')?p.slice(4):'index.html');if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 fs.mkdirSync(out,{recursive:true});await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port,browser=await({chromium,webkit}[engine]).launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[],external=[],metrics=[],groups=[];
 page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));await page.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():(external.push(r.request().url()),r.abort()));
 const svg=(w,h)=>({mime:'image/svg+xml',data:Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="'+w+'" height="'+h+'" viewBox="0 0 '+w+' '+h+'"><path fill="white" d="M0 0H'+w+'V'+h+'H0z" opacity=".15"/><path fill="#bbdd00" d="M'+w*.1+' '+h*.1+'h'+w*.3+'v'+h*.8+'h-'+w*.3+'z"/><path fill="white" d="M'+w*.5+' '+h*.25+'h'+w*.4+'v'+h*.5+'h-'+w*.4+'z"/></svg>').toString('base64')});
 const card=key=>page.locator('.branding-settings [data-asset="'+key+'"]'),form=()=>page.locator('.branding-settings');
 async function open(){await page.goto(base+'/system');await page.getByRole('tab',{name:'Appearance',exact:true}).click({timeout:10000}).catch(async error=>{console.error(JSON.stringify({errors,body:(await page.locator('body').innerText()).slice(0,1800)}));throw error;});await form().getByLabel('Site name',{exact:true}).waitFor();}
 async function loaded(){await form().locator('[data-asset] img:visible').evaluateAll(images=>Promise.all(images.map(i=>i.decode().catch(()=>{}))));await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));}
 async function geometry(){return form().locator('[data-image-theme] [data-asset]').evaluateAll(nodes=>nodes.map(n=>{
  const img=[...n.querySelectorAll('img')].find(i=>i.getClientRects().length),frame=img?.closest('.media-picker-media')||img?.parentElement,box=e=>e?.getBoundingClientRect().toJSON();
  return {key:n.dataset.asset,img:box(img),frame:box(frame),card:box(img?.closest('.media-picker-card')||frame),natural:img?[img.naturalWidth,img.naturalHeight]:[],src:img?.getAttribute('src'),background:frame?getComputedStyle(frame).backgroundColor:null,fit:img?getComputedStyle(img).objectFit:null,source:n.querySelector('.branding-image-source').textContent,remove:[...n.querySelectorAll('[data-upload-remove]')].some(b=>b.getClientRects().length),field:box(n.querySelector('.media-picker'))};
 }));}
 try{
 const shapes=[['square',256,256,'image/png'],['wide-transparent',840,140,'image/svg+xml'],['portrait',120,320,'image/jpeg'],['tiny',8,8,'image/png'],['large',2400,1600,'image/png']];
 const cases=sharedOnly?[]:baseline?shapes.slice(0,1):shapes;
 for(const [name,w,h,mime]of cases){
  let asset=svg(w,h);
  if(mime!=='image/svg+xml'){asset=await page.evaluate(({w,h,mime})=>{const c=document.createElement('canvas');c.width=w;c.height=h;const g=c.getContext('2d');if(mime==='image/jpeg'){g.fillStyle='#fff';g.fillRect(0,0,w,h);}g.fillStyle='#bbdd00';g.fillRect(w*.1,h*.1,w*.35,h*.8);g.fillStyle='#fff';g.fillRect(w*.5,h*.2,w*.4,h*.6);return {mime,data:c.toDataURL(mime).split(',')[1]};},{w,h,mime});}
  settings={...structuredClone(defaults),assets:{logo:asset,symbol:asset,favicon:asset}};await open();await loaded();
  for(const theme of ['light','dark'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1100});await page.evaluate(t=>GatewayTheme.apply(t),theme);await loaded();
   const g=await geometry();metrics.push({name,theme,width,g});
   await form().locator('.branding-theme-columns').screenshot({path:path.join(out,name+'-'+theme+'-'+width+'.png')});
   if(!baseline){
    for(const x of g){assert.equal(x.fit,'contain');assert.deepEqual(x.natural,[w,h]);assert.ok(Math.abs(x.frame.width/x.frame.height-w/h)<.025,JSON.stringify(x));assert.ok(Math.abs(x.img.width-x.frame.width)<1&&Math.abs(x.img.height-x.frame.height)<1,'image/frame mismatch '+JSON.stringify(x));assert.equal(x.background,x.key.endsWith('_light')?'rgb(255, 255, 255)':'rgb(0, 0, 0)',x.key);assert.ok(x.card.width<=x.field.width+1);assert.equal(x.remove,!x.key.endsWith('_light'));}
    for(const role of ['logo','symbol','favicon']){const own=g.find(x=>x.key===role),fallback=g.find(x=>x.key===role+'_light');assert.equal(own.src,fallback.src);assert.ok(Math.abs(own.frame.width-fallback.frame.width)<1&&Math.abs(own.frame.height-fallback.frame.height)<1,'uploaded/inherited mismatch '+JSON.stringify({own,fallback}));}
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'page overflow');
   }
  }
  groups.push(name+'-six-theme-widths');
 }
 if(!baseline){
  if(!sharedOnly){
  await page.setViewportSize({width:1440,height:1100});const asset=svg(600,120);settings={...structuredClone(defaults),assets:{logo:asset,symbol:svg(180,180),favicon:svg(120,240)}};await open();await loaded();
  const saved=JSON.stringify(settings.assets),beforeWrites=writes.length;await form().getByLabel('Site name',{exact:true}).fill('Unrelated draft');
  await card('logo').getByRole('button',{name:'Remove logo in dark theme',exact:true}).focus();await page.keyboard.press('Enter');assert.equal(await card('logo').locator('[data-upload-remove]').isVisible(),false);assert.equal(writes.length,beforeWrites);await form().getByRole('button',{name:'Discard',exact:true}).click();await loaded();assert.equal(JSON.stringify(settings.assets),saved);assert.equal(await card('logo').locator('.branding-image-source').textContent(),'Uploaded for this theme');groups.push('remove-discard-keeps-data');
  await card('logo_light').locator('input[type=file]').setInputFiles({name:'replacement.svg',mimeType:'image/svg+xml',buffer:Buffer.from(svg(100,300).data,'base64')});await page.waitForFunction(()=>document.querySelector('[data-asset=logo_light] .branding-image-source').textContent==='Uploaded for this theme');await loaded();assert.equal((await geometry()).find(x=>x.key==='logo_light').natural[1],300);assert.equal(writes.length,beforeWrites);
  failSave=true;await form().getByRole('button',{name:'Save',exact:true}).click();await form().getByText('Another administrator changed the brand. Reopen Settings before saving.').waitFor();assert.equal((await geometry()).find(x=>x.key==='logo_light').natural[1],300);failSave=false;await form().getByRole('button',{name:'Save',exact:true}).click();await form().getByRole('button',{name:'Saved',exact:true}).waitFor();await open();await loaded();assert.equal((await geometry()).find(x=>x.key==='logo_light').natural[1],300);groups.push('upload-conflict-retry-reload');
  await card('logo_light').getByRole('button',{name:'Remove logo in light theme',exact:true}).click();await loaded();const pair=await geometry();assert.equal(pair.find(x=>x.key==='logo').src,pair.find(x=>x.key==='logo_light').src);assert.equal(await card('logo_light').locator('[data-upload-remove]').isVisible(),false);groups.push('removal-restores-fallback');
  settings={...structuredClone(defaults),assets:{logo:{mime:'image/png',data:Buffer.from('broken fixture').toString('base64')}}};await open();await page.locator('[data-asset=logo] .media-picker-preview-error:visible').waitFor();await page.locator('[data-asset=logo_light] .media-picker-preview-error:visible').waitFor();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await card('logo_light').screenshot({path:path.join(out,'failed-fallback.png')});groups.push('image-failure-safe-frame');
  }
  await page.goto(base+'/shared-preview-matrix');

  // Production shared preview primitive under the actual caller layout selectors.
  // This supplements, rather than replaces, the separate full caller workflows.
  const sharedContexts=[['Playground','generationView',''],['Template runner','templatesView','template-variable-reference'],['Template authoring','templatesView','template-editor'],['Appearance','systemView','branding-asset'],['Provider logo','configurationView','provider-logo-editor'],['Invoice logo','systemView',''],['Resource source','generationView','resource-source-input'],['First/last frame','generationView','reference-input']];
  for(const [name,w,h]of [['square',240,240],['wide',720,120],['portrait',120,320]]){
   const asset=svg(w,h);
   await page.evaluate(({contexts,asset})=>{
    document.body.replaceChildren();const host=document.createElement('main');host.id='previewMatrix';host.style.cssText='padding:20px;width:100%;min-width:0;max-width:1100px;margin:auto';document.body.append(host);
    window.previewMatrixWidgets=contexts.map(([label,id,cls],i)=>{
     const outer=document.createElement('div');outer.id=id;outer.className=cls;outer.dataset.context=label;host.append(outer);
     const widget=ReferenceUpload.create({label,kinds:['image'],onFile:()=>{},onRemove:()=>widget.setPreview(null)});if(label==='Invoice logo')widget.root.classList.add('branding-asset','branding-asset-inline');outer.append(widget.root);widget.setPreview({name:'Selected '+label+' image with a long descriptive file name.svg',type:asset.mime,url:'data:'+asset.mime+';base64,'+asset.data});return widget;
    });
   },{contexts:sharedContexts,asset});
   for(const theme of ['light','dark'])for(const width of [1440,390,320]){
    await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    // WebKit preserves lazy loading below the viewport; expose each actual card
    // before asserting decoded geometry rather than treating deferred loading as failure.
    for(const image of await page.locator('#previewMatrix img').all()){await image.scrollIntoViewIfNeeded();await image.evaluate(i=>i.decode());}
    await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
    await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth,null,{timeout:2000}).catch(()=>{});
    const g=await page.locator('#previewMatrix [data-context]').evaluateAll(nodes=>nodes.map(n=>{const img=n.querySelector('img'),frame=n.querySelector('.media-picker-media'),card=n.querySelector('.media-picker-card'),row=n.querySelector('.media-picker-previews'),box=e=>e.getBoundingClientRect().toJSON();return {context:n.dataset.context,img:box(img),frame:box(frame),card:box(card),row:box(row),natural:[img.naturalWidth,img.naturalHeight],fit:getComputedStyle(img).objectFit};}));
    for(const x of g){assert.deepEqual(x.natural,[w,h]);assert.equal(x.fit,'contain');assert.ok(Math.abs(x.frame.width/x.frame.height-w/h)<.025,JSON.stringify(x));assert.ok(x.card.width<=x.row.width+1,JSON.stringify(x));assert.ok(Math.abs(x.img.width-x.frame.width)<1&&Math.abs(x.img.height-x.frame.height)<1,JSON.stringify(x));}
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,JSON.stringify(await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,invoice:[...document.querySelector('[data-context=\"Invoice logo\"]').querySelectorAll('.media-picker,.media-picker-previews')].map(n=>({cls:n.className,rect:n.getBoundingClientRect().toJSON(),display:getComputedStyle(n).display,cols:getComputedStyle(n).gridTemplateColumns,align:getComputedStyle(n).alignItems,justify:getComputedStyle(n).justifyItems,width:getComputedStyle(n).width,style:n.getAttribute('style')})),offenders:[...document.querySelectorAll('#previewMatrix *')].filter(n=>n.getBoundingClientRect().right>innerWidth+1).map(n=>({context:n.closest('[data-context]')?.dataset.context,tag:n.tagName,cls:n.className,width:n.getBoundingClientRect().width,right:n.getBoundingClientRect().right})).slice(0,12)}))));
    metrics.push({shared:name,theme,width,g});await page.locator('#previewMatrix').screenshot({path:path.join(out,'shared-'+name+'-'+theme+'-'+width+'.png')});
   }
   groups.push('eight-shared-contexts-'+name);
  }


 }
 assert.deepEqual(errors,[]);assert.deepEqual(external,[]);fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,baseline,source:Object.fromEntries(['branding.js','styles.css','references.css','reference-upload.js'].map(p=>[p,crypto.createHash('sha256').update(fs.readFileSync(path.join(root,p))).digest('hex')])),groups,metrics,writes:writes.length,errors,external},null,2));console.log(JSON.stringify({engine,baseline,groups:groups.length,errors,external}));
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
