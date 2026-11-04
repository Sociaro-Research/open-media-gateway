const {evidencePath}=require('./evidence.cjs');
const {chromium,webkit}=require('playwright'),http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../../web/public/ui'),engine=process.env.UI_BROWSER_ENGINE||'chromium',before=process.env.UI_CAPTURE_BEFORE==='1';
const out=path.resolve(process.env.UI_EVIDENCE_DIR||evidencePath('/tmp/gateway-switches'));
const variants=[
 ['publication','form-heading-actions','<label class="checkbox">Published<input type="checkbox"></label>'],
 ['provider','provider-detail-header','<label class="master-switch"><span><strong>Provider enabled</strong></span><input type="checkbox" checked></label>'],
 ['system','system-settings','<label class="system-toggle"><input type="checkbox"><span><strong>System setting</strong><small>Unique explanation retained</small></span></label>'],
 ['storage','storage-options-fields','<label class="storage-checkbox"><input type="checkbox">Use path-style addressing</label>'],
 ['permissions','keys-form','<fieldset><legend>Permissions</legend><label><input type="checkbox" checked disabled>Generation</label></fieldset>'],
 ['generation','toggle-grid','<label class="toggle-option"><span><strong>Audio</strong></span><input type="checkbox"></label>'],
 ['advanced','advanced-grid','<label class="check-option"><input type="checkbox"><span><strong>Draft</strong><small>Useful model detail retained</small></span></label>'],
 ['dynamic-provider','provider-settings-form','<label><input type="checkbox">Native provider option</label>'],
 ['connection','provider-connection-settings','<label><input type="checkbox"><strong>Use manual catalog only</strong></label>'],
 ['system-permissions','system-settings','<fieldset><label><input type="checkbox">System permission</label></fieldset>'],
 ['multi-select','control-popup','<label class="multi-select-option"><input type="checkbox"><span>Active</span></label>'],
 ['report','report-chart-switches','<label class="report-chart-switch"><span>Include totals</span><input type="checkbox"></label>'],
 ['branding','branding-preset-controls','<label class="system-toggle"><input type="checkbox"><span><strong>Default</strong></span></label>'],
 ['session','session-point-list','<label class="checkbox-label"><input type="checkbox">x 0.25 · y 0.75</label>'],
 ['sales','keys-form organization-form','<label class="system-toggle"><input type="checkbox"><span><strong>Sales permission</strong><small>Permission explanation retained</small></span></label>'],
 ['long-label','organization-settings-grid','<label class="system-toggle"><input type="checkbox"><span><strong>A long setting label that wraps on a narrow screen without covering the switch</strong></span></label>'],
];
const html=`<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"><style>.audit{max-width:780px;padding:24px;margin:auto}.audit-case{margin-bottom:20px}.audit .control-popup{position:static;max-width:100%}.audit .master-switch:has(>span){min-width:0}.audit .toggle-grid{grid-template-columns:minmax(0,1fr)}.audit-heading{font-weight:700}</style><main class="audit"><h1 class="audit-heading">Switches</h1><section id="configurationView">${variants.map(([id,cls,body])=>`<div class="audit-case ${cls}" data-case="${id}">${body}</div>`).join('')}</section><section id="systemView"><div class="keys-form"><fieldset><label><input type="checkbox"><strong>Administrative permission</strong></label></fieldset></div></section><table><tr><td class="model-access-toggle"><input type="checkbox" aria-label="Model available"></td></tr></table><label class="storage-choice-header"><input type="radio" name="storage" checked>Gateway storage</label><form id="dynamic"></form></main><script src="/form-ui.js"></script>`;
const server=http.createServer((req,res)=>{if(req.url==='/'){res.setHeader('Content-Type','text/html');return res.end(html)}const file=path.join(root,path.basename(req.url));if(!fs.existsSync(file))return res.writeHead(404).end();res.setHeader('Content-Type',file.endsWith('.css')?'text/css':'text/javascript');res.end(fs.readFileSync(file))});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await({chromium,webkit}[engine]).launch({headless:true,executablePath:process.env.UI_BROWSER_PATH});const page=await browser.newPage({viewport:{width:1440,height:1000},hasTouch:true});const errors=[],measurements=[];page.on('pageerror',e=>errors.push(e.message));fs.mkdirSync(out,{recursive:true});
 try{
  await page.goto('http://127.0.0.1:'+server.address().port);await page.getByRole('switch',{name:'System setting',exact:false}).waitFor();
  const switches=page.getByRole('switch');assert.equal(await switches.count(),18);
  const thumbColor=await page.evaluate(()=>{const n=document.createElement('span');n.style.color='var(--on-accent)';document.body.append(n);const c=getComputedStyle(n).color;n.remove();return c;});
  for(const input of await switches.all()){
   const style=await input.evaluate(n=>{const s=getComputedStyle(n),t=getComputedStyle(n,'::before');return{width:s.width,height:s.height,appearance:s.appearance,radius:s.borderRadius,thumb:t.width,color:t.backgroundColor};});
   assert.deepEqual(style,{width:'42px',height:'24px',appearance:'none',radius:'999px',thumb:'16px',color:thumbColor});
  }
  const metrics=()=>page.locator('label:has(>input[type=checkbox])').evaluateAll(labels=>labels.map(e=>{const s=getComputedStyle(e),i=e.querySelector('input'),r=i.getBoundingClientRect();return {case:e.closest('[data-case]')?.dataset.case||'other',gap:s.columnGap,weight:s.fontWeight,strong:[...e.querySelectorAll('strong')].map(x=>getComputedStyle(x).fontWeight),padding:s.paddingInlineStart,width:r.width,height:r.height,left:e.getBoundingClientRect().left,right:e.getBoundingClientRect().right};}));
  for(const theme of ['dark','light'])for(const width of [1440,390,320]){
   await page.setViewportSize({width,height:1000});await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);const items=await metrics();measurements.push({theme,width,items});
   if(!before)for(const m of items){assert.equal(m.gap,'12px',JSON.stringify(m));assert.equal(m.weight,'400',JSON.stringify(m));assert.ok(m.strong.every(w=>w==='400'),JSON.stringify(m));assert.equal(m.width,42);assert.equal(m.height,24);assert.ok(m.left>=0&&m.right<=width+1,JSON.stringify(m));if(m.case==='generation')assert.equal(m.padding,'14px');}
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:path.join(out,`${theme}-${width}.png`),fullPage:true});
  }
  if(!before){
   const system=page.getByRole('switch',{name:'System setting',exact:false});await system.focus();await system.press('Space');assert.equal(await system.isChecked(),true);await page.waitForFunction(()=>getComputedStyle(document.querySelector('[data-case=system] input'),'::before').transform==='matrix(1, 0, 0, 1, 18, 0)');assert.equal(await system.evaluate(e=>e.matches(':focus-visible')),true);await system.locator('..').click();assert.equal(await system.isChecked(),false);await system.tap();assert.equal(await system.isChecked(),true);
   const disabled=page.getByRole('switch',{name:'Generation',exact:true});assert.equal(await disabled.isDisabled(),true);assert.equal(await disabled.isChecked(),true);
   await page.evaluate(()=>{const l=document.createElement('label');l.innerHTML='<input type="checkbox"><strong>Dynamic setting</strong>';document.querySelector('#dynamic').append(l)});const dyn=page.getByRole('switch',{name:'Dynamic setting'});await dyn.check();assert.equal(await dyn.isChecked(),true);assert.equal(await dyn.locator('..').evaluate(e=>getComputedStyle(e.querySelector('strong')).fontWeight),'400');assert.equal(await dyn.locator('..').evaluate(e=>getComputedStyle(e).gap),'12px');
   assert.equal(await page.locator('.audit-heading').evaluate(e=>getComputedStyle(e).fontWeight),'700');assert.equal(await page.getByRole('radio').count(),1);await page.emulateMedia({reducedMotion:'reduce'});assert.equal(await system.evaluate(n=>getComputedStyle(n,'::before').transitionDuration),'0s');
  }
  assert.deepEqual(errors,[]);fs.writeFileSync(path.join(out,'report.json'),JSON.stringify({engine,before,variants:variants.map(v=>v[0]),measurements,errors},null,2));console.log(`PASS: ${engine}, ${before?'baseline capture':'16 shared variants + administrative/dynamic controls'}, 6 theme/width combinations; interactions and regular labels ${before?'not asserted':'passed'}.`);
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
