(() => {
 'use strict';
 let active=null, timer;
 const node=(tag,cls,text)=>{const n=document.createElement(tag);if(cls)n.className=cls;if(text!=null)n.textContent=text;return n;};
 const usd=v=>new Intl.NumberFormat(undefined,{style:'currency',currency:'USD',maximumFractionDigits:6}).format(Number(v));
 const date=v=>v?new Date(v+(v.length===7?'-01':'')+'T12:00:00').toLocaleDateString(undefined,{year:'numeric',month:'short',...(v.length===7?{}:{day:'numeric'})}):'Not published';
 // Resolve only advertised choices; K and p are different semantic families.
 function nearestResolution(preference,choices=[]){
  if(!choices.length)return '';if(choices.includes(preference))return preference;
  const parse=value=>{const text=String(value||'').trim().toLowerCase().replaceAll(' ',''),tier=/^([0-9]+(?:\.[0-9]+)?)([pk])$/.exec(text),dimensions=/^([0-9]+)[x*×]([0-9]+)$/.exec(text);if(tier){const x=Number(tier[1]);if(x>0&&x<=1e6)return {family:tier[2],x};}if(dimensions){const x=Number(dimensions[1]),y=Number(dimensions[2]);if(x>0&&y>0&&x<=1e6&&y<=1e6)return {family:'dimensions',x,y};}return {};};
  const source=parse(preference);if(!source.family)return choices[0];let best='',bestScore;
  const orientation=s=>Math.sign(s.x-s.y),less=(a,b)=>{for(let i=0;i<a.length;i++)if(Math.abs(a[i]-b[i])>1e-9)return a[i]<b[i];return false;};
  for(const choice of choices){const target=parse(choice);if(target.family!==source.family)continue;const score=source.family==='dimensions'?[orientation(target)===orientation(source)?0:1,Math.abs(target.x/target.y-source.x/source.y),(target.x-source.x)**2+(target.y-source.y)**2,target.x*target.y]:[0,0,Math.abs(target.x-source.x),target.x];if(!best||less(score,bestScore)){best=choice;bestScore=score;}}
  return best||choices[0];
 }
 // Format capability bounds once for both generation and template fields.
 function durationLabel(model={}){
  if(model.duration_from_input)return 'Duration (from input)';
  if(model.max_duration===0)return 'Duration (unavailable)';
  const min=Number.isFinite(model.min_duration)&&model.min_duration>0?model.min_duration:null;
  const max=Number.isFinite(model.max_duration)&&model.max_duration>0?model.max_duration:null;
  if(min!==null&&max!==null&&min>max)return 'Duration (s)';
  const range=min!==null&&max!==null?(min===max?min+' s':min+'–'+max+' s'):max!==null?'up to '+max+' s':min!==null?'from '+min+' s':'s';
  const step=Number.isFinite(model.duration_step)&&model.duration_step>1&&min!==max?'; step '+model.duration_step+' s':'';
  return 'Duration ('+range+step+')';
 }
 function close(restore=false){clearTimeout(timer);if(!active)return;const {panel,button}=active;active=null;panel.remove();button.setAttribute('aria-expanded','false');button.removeAttribute('aria-describedby');if(restore&&button.isConnected)button.focus();}
 function position(){if(!active)return;const {panel,button}=active,r=button.getBoundingClientRect();panel.style.maxHeight=Math.max(120,Math.min(560,innerHeight-24))+'px';const b=panel.getBoundingClientRect();const right=r.right+10;panel.style.left=Math.max(12,Math.min(right+b.width<innerWidth-12?right:r.left-b.width-10,innerWidth-b.width-12))+'px';panel.style.top=Math.max(12,Math.min(r.top,innerHeight-b.height-12))+'px';}
 function contains(target){return !!active&&(active.panel.contains(target)||active.button.contains(target));}
 function open(button,model,pinned=false){
  clearTimeout(timer);
  if(active?.button===button){if(pinned)active.pinned=true;return;}
  close();const m=model.metadata||{},p=model.pricing||{},panel=node('aside','model-info-popup');
  panel.id='model-information';panel.setAttribute('role','dialog');panel.setAttribute('aria-label','Model information: '+model.display_name);panel.setAttribute('popover','manual');
  const header=node('div','model-info-header');header.append(node('strong','',model.display_name));
  const dismiss=node('button','model-info-close icon-close','×');dismiss.type='button';dismiss.setAttribute('aria-label','Close model information');dismiss.onclick=()=>close(true);header.append(dismiss);panel.append(header);
  if(m.description)panel.append(node('p','model-info-description',m.description));
  const dl=node('dl','model-info-facts');
  const fact=(label,value)=>{if(value==null||value===''||(Array.isArray(value)&&!value.length))return;dl.append(node('dt','',label),node('dd','',Array.isArray(value)?value.join(', '):String(value)));};
  fact('Model ID',model.id);fact('Released',date(m.release_date));if(m.version_date)fact('Version date',date(m.version_date));fact('Current snapshot',m.snapshot);
  if(m.api_created)fact('API object created',new Date(m.api_created*1000).toLocaleString());
  fact('Publisher',m.owned_by);if(m.shutdown_date)fact('Scheduled retirement',date(m.shutdown_date));
  fact('Example estimate',p.known&&Number.isFinite(p.reference_price_usd)?usd(p.reference_price_usd):'Unavailable');fact('Calculation assumptions',p.reference_description);
  const q=p.reference_quote;
  if(q?.pricing){
   fact('Billing region',q.pricing.region);
   if(q.pricing.unit!=='multi_metric'){const tariff=usd(q.pricing.price)+' / '+q.pricing.per+' '+q.pricing.unit+(q.pricing.fixed_usd?' + '+usd(q.pricing.fixed_usd)+' input cost':'');if(q.quantity_unknown||!Number.isFinite(q.estimated_units)||!Number.isFinite(q.expected_usd)){fact('Verified tariff',tariff);fact('Estimate','Unavailable until input quantity is known.');}else fact('Calculation',q.estimated_units+' '+q.pricing.unit+' × '+tariff+' = '+usd(q.expected_usd));}
   for(const c of q.pricing.components||[])fact(c.metric.replaceAll('_',' '),usd(c.price)+' / '+c.per+' tokens');
   if(q.pricing.estimate_description)fact('Calculation',q.pricing.estimate_description);
  }
  fact('Available in this gateway',model.media_type);
  fact('Sizes / resolutions',model.resolutions);fact('Aspect ratios',model.ratios);fact('Quality',model.qualities);fact('Background',model.backgrounds);fact('Output formats',model.formats);
  if(model.max_duration)fact('Video duration',model.min_duration+'–'+model.max_duration+' seconds'+(model.duration_step?', step '+model.duration_step:'')+(model.duration_from_input?'; determined by input':''));
  fact('Reference modes',(model.reference_modes||[]).map(v=>v.label||v.id));
  if(model.supports_image_input)fact('Image references',model.max_image_inputs?'Up to '+model.max_image_inputs+(model.requires_image_input?'; required':''):'Supported');
  if(model.supports_video_input)fact('Video references',model.requires_video_input?'Required':'Supported');
  if(model.supports_audio_input)fact('Audio references',model.requires_audio_input?'Required':'Supported');
  if(model.supports_audio)fact('Generated audio',model.audio_always_on?'Always enabled':'Optional');
  fact('Voices',model.voices);
  const features=[['supports_draft','Draft mode'],['supports_camera_fixed','Fixed camera'],['supports_last_frame','Last-frame output'],['supports_negative_prompt','Negative prompt'],['supports_lyrics','Lyrics'],['supports_gender','Vocal gender']].filter(([key])=>model[key]).map(([,label])=>label);
  fact('Other gateway controls',features);
  for(const param of model.parameters||[])fact('Gateway parameter: '+(param.label||param.key),[param.type,param.description,...(param.choices||[]),param.required?'required':'',param.default!=null?'default '+param.default:'',param.min!=null?'min '+param.min:'',param.max!=null?'max '+param.max:'',param.max_length?'max length '+param.max_length:'',param.max_items?'max items '+param.max_items:''].filter(Boolean).join('; '));
  for(const f of m.facts||[])fact(f.label,f.value);
  if(m.checked_at)fact('Metadata checked',date(m.checked_at));panel.append(dl);
  const sources=node('div','model-info-sources');
  for(const s of m.sources||[]){let url;try{url=new URL(s.url);}catch{continue;}if(url.protocol!=='https:')continue;const a=node('a','',s.title);a.href=url.href;a.target='_blank';a.rel='noopener noreferrer';sources.append(a);}
  panel.append(sources);display(button,panel,pinned);
 }
 function display(button,panel,pinned){
  (button.closest('dialog')||document.body).append(panel);active={panel,button,pinned};button.setAttribute('aria-expanded','true');button.setAttribute('aria-describedby',panel.id);if(panel.showPopover)panel.showPopover();position();
  panel.onpointerenter=()=>clearTimeout(timer);panel.onpointerleave=()=>schedule();panel.onfocusin=()=>{if(active)active.pinned=true;};
 }
 function informationButton(title,render,className=''){
  const b=node('button','model-info-button','i');b.type='button';b.setAttribute('aria-label',title);b.setAttribute('aria-haspopup','dialog');b.setAttribute('aria-expanded','false');
  function show(pinned){clearTimeout(timer);if(active?.button===b){if(pinned)active.pinned=true;return;}close();const panel=node('aside','model-info-popup '+className);panel.id='role-information';panel.setAttribute('role','dialog');panel.setAttribute('aria-label',title);panel.setAttribute('popover','manual');const header=node('div','model-info-header');header.append(node('strong','',title));const dismiss=node('button','model-info-close icon-close','×');dismiss.type='button';dismiss.setAttribute('aria-label','Close '+title.toLowerCase());dismiss.onclick=()=>close(true);header.append(dismiss);panel.append(header);render(panel);display(b,panel,pinned);}
  b.onclick=e=>{e.stopPropagation();if(active?.button===b&&active.pinned)close();else show(true);};b.onpointerenter=e=>{if(e.pointerType==='mouse')show(false);};b.onpointerleave=schedule;b.onkeydown=e=>{e.stopPropagation();if(e.key==='ArrowRight'||e.key==='F2'){e.preventDefault();show(true);active.panel.querySelector('button').focus();}};return b;
 }
 function schedule(){clearTimeout(timer);if(active&&!active.pinned)timer=setTimeout(()=>close(),200);}
 function button(model){
  const b=node('button','model-info-button','i');b.type='button';b.setAttribute('aria-label','Information about '+model.display_name);b.setAttribute('aria-haspopup','dialog');b.setAttribute('aria-expanded','false');
  b.onclick=e=>{e.stopPropagation();if(active?.button===b&&active.pinned)close();else open(b,model,true);};
  b.onpointerenter=e=>{if(e.pointerType==='mouse')open(b,model);};b.onpointerleave=schedule;
  b.onkeydown=e=>{e.stopPropagation();if(e.key==='ArrowRight'||e.key==='F2'){e.preventDefault();open(b,model,true);active.panel.querySelector('button').focus();}};
  return b;
 }
 document.addEventListener('keydown',e=>{if(active&&e.key==='Escape'){e.preventDefault();e.stopImmediatePropagation();close(true);}},true);
 document.addEventListener('pointerdown',e=>{if(active&&!contains(e.target))close();},true);
 window.addEventListener('resize',position);document.addEventListener('scroll',e=>{if(active&&!active.panel.contains(e.target))position();},true);
 window.ModelInfo={nearestResolution,button,informationButton,close,contains,durationLabel,compare:(a,b)=>{const d=m=>[m.metadata?.release_date||'',m.metadata?.version_date||''].sort().at(-1);return d(b).localeCompare(d(a))||a.display_name.localeCompare(b.display_name);}};
})();
