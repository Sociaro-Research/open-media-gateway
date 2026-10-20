(() => {
 'use strict';
 let active=null;
 const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const names={profit_usd:"Profit (USD)",profit_status:"Profit status",profit_recognized_at:"Profit recognized",billing_required:"Billing verification required",identity_historical:"Identity captured at request time",identity_snapshot:"Original identity",provider_name:"Provider name",id:'Generation ID',api_key_id:'API key',api_key_name:'API key name',public_model:'Public model',provider_model:'Provider model',user_id:'User ID',user_email:'User',organization_id:'Organization ID',organization_name:'Organization',created_at:'Created',updated_at:'Updated',completed_at:'Completed',stored_at:'Stored',started_at:'Processing started',poll_errors:'Polling errors',content_access:'Content access',request_logging:'Request logging',result_size_bytes:'Result size (bytes)',result_sha256:'Result SHA-256',result_content_type:'Result content type',result_deleted_at:'Result deleted',has_stored_result:'Stored result',consumed_usd:'Customer charge',expected_usd:'Estimated cost',metered_usd:'Metered cost',tariff_usd:'Confirmed cost',actual_usd:'Provider charged',generate_audio:'Generate audio',return_last_frame:'Return last frame',camera_fixed:'Fixed camera',reference_mode:'Reference mode',provider_task_id:'Provider task ID'};
 const label=key=>names[key]||key.replaceAll('_',' ').replace(/^./,c=>c.toUpperCase());
 const usd=value=>new Intl.NumberFormat('en-US',{style:'currency',currency:'USD',maximumFractionDigits:6}).format(value);
 function link(value,text=value){try{const url=new URL(value,location.origin);if(!['https:','http:'].includes(url.protocol))return null;const a=node('a',text);a.href=url.href;a.target='_blank';a.rel='noopener noreferrer';return a;}catch{return null;}}
 function valueNode(value,key=''){
  if(value==null)return node('span','Not recorded');
  if(['user_email','actor_email','owner_email','creator_email','revoker_email'].includes(key)&&value)return UserUI.identity({email:String(value)});if(['actor_name','user_name'].includes(key)&&value)return UserUI.identity({name:String(value)});
  if(Array.isArray(value)){if(!value.length)return node('span','None');const list=node('ul');value.forEach(item=>{const li=node('li');li.append(valueNode(item,key));list.append(li);});return list;}
  if(typeof value==='object')return facts(value);
  if(typeof value==='boolean')return node('span',value?'Yes':'No');
  if(key.endsWith('_at')&&value){const date=typeof value==='number'?new Date(value*1000):new Date(value);if(!Number.isNaN(date.getTime())){const time=node('time',date.toLocaleString());time.dateTime=date.toISOString();return time;}}
  if(key.endsWith('_usd')&&typeof value==='number')return node('span',usd(value));
  if(key==='api_key_id')return node('span','*'+String(value).slice(-4));
  if(typeof value==='string'&&(value.startsWith('https://')||value.startsWith('http://')||value.startsWith('/')))return link(value)||node('span',value);
  return node('span',String(value));
 }
 function facts(values){const dl=node('dl',null,'detail-facts');for(const [key,value] of Object.entries(values)){if(key.endsWith('_picture'))continue;const item=node('div');item.append(node('dt',label(key)),node('dd'));item.lastChild.append(key==='user_email'&&value?UserUI.identity({id:values.user_id,email:value,picture:values.user_picture}):key==='actor_name'&&value?UserUI.identity({id:values.actor_id,name:value,email:values.actor_email,picture:values.actor_picture}):valueNode(value,key));dl.append(item);}return dl;}
 function mediaKind(contentType,mediaType){if(contentType){if(contentType.startsWith('image/'))return 'image';if(contentType.startsWith('video/'))return 'video';if(contentType.startsWith('audio/'))return 'audio';return 'file';}return ['speech','music','audio'].includes(mediaType)?'audio':['image','video'].includes(mediaType)?mediaType:'file';}
 function mediaCard(url,title,kind,{download,poster}={}){
  const open=link(url,'Open '+title.toLowerCase()+' ↗');if(!open)return null;
  const card=node('article',null,'detail-media-card'),heading=node('div',null,'detail-media-heading'),preview=node('div',null,'detail-media-preview'),actions=node('div',null,'detail-links');
  heading.append(node('h4',title),FormUI.copyButton(()=>open.href,'Copy '+title.toLowerCase()+' link',()=>FormUI.message(message,'Could not copy the link. Try again.','error')));
  const message=node('p',null,'form-message');message.setAttribute('role','status');
  const unavailable=()=>preview.replaceChildren(node('p','Preview unavailable. Use Open to view the file.','detail-media-unavailable'));
  if(kind==='image'){
   const image=node('img');image.alt=title;image.loading='lazy';image.referrerPolicy='no-referrer';image.onerror=unavailable;image.src=open.href;
   const view=link(url,'');view.setAttribute('aria-label','Open '+title.toLowerCase());view.append(image);preview.append(view);
  }else if(kind==='video'||kind==='audio'){
   const player=node(kind);player.controls=true;player.playsInline=true;player.preload='metadata';player.referrerPolicy='no-referrer';player.onerror=unavailable;if(kind==='video'&&poster){const frame=link(poster);if(frame)player.poster=frame.href;}player.src=open.href;player.setAttribute('aria-label',title);preview.append(player);
  }else preview.append(node('p','File','detail-media-unavailable'));
  actions.append(open);if(download){const save=link(download,'Download result');if(save){save.removeAttribute('target');save.download='';actions.append(save);}}
  card.append(heading,preview,actions,message);return card;
 }
 function references(inputs){
  const grid=node('div',null,'detail-media-grid');
  function add(value,title,kind){if(Array.isArray(value)){value.forEach((item,i)=>add(item,title+' '+(i+1),kind));}else if(value&&typeof value==='object'){for(const [key,item]of Object.entries(value))add(item,title+' · '+label(key),kind);}else if(typeof value==='string'){const card=mediaCard(value,title,kind);if(card)grid.append(card);}}
  for(const [key,value]of Object.entries(inputs)){const kind=key.includes('video')?'video':key.includes('audio')||key==='reference_voices'?'audio':'image';add(value,key==='reference_voices'?'Voice reference':kind[0].toUpperCase()+kind.slice(1)+' reference',kind);}
  return grid.children.length?grid:facts(inputs);
 }
 function close(){if(active)active.dialog.close();}
 function render(parent,data){
  parent.replaceChildren();const report=data.report||{},generation=data.generation||{},request=data.request;
  const section=(title,contents)=>{const s=node('section',null,'detail-section');s.append(node('h3',title),contents);parent.append(s);};
  if(report.identity_historical===false)parent.append(node('p','Historical labels were not recorded for this request. Names shown are current labels.','field-explanation'));
  if(data.content_access===false)parent.append(node('p','Your role can view accounting and status, but not prompts, input materials or generated content.','settings-warning'));
  else if(data.request_logging==='minimized')parent.append(node('p','Request content is not retained because minimal request logging is enabled. Prompts, instructions and reference links are unavailable.','settings-warning'));
  const grouped=new Set(['cost','usage','options','prompt','input','instructions','image','input_reference','voice','response_format','output_format','quality','background','size','seconds','content_url','download_url','last_frame_url','status_url','can_delete','error','webhook_url']);
  const overview={...Object.fromEntries(Object.entries(generation).filter(([key])=>!grouped.has(key))),...Object.fromEntries(Object.entries(report).filter(([key])=>!['cost','usage','status'].includes(key)))};
  overview.request_logging=data.request_logging;if(data.processing)Object.assign(overview,data.processing);
  section('Generation',facts(overview));
  if(data.content_access!==false){
   const prompt=request?.prompt??generation.prompt??generation.input;
   if(prompt!=null)section(generation.media_type==='speech'?'Input text':'Prompt',node('div',prompt||'Empty prompt','detail-text'));
   const options=request?.options||generation.options||{};
   for(const key of ['instructions','negative_prompt','lyrics']){const text=options[key]??generation[key];if(text)section(label(key),node('div',text,'detail-text'));}
   const refKeys=['image_urls','video_urls','audio_urls','video_url','audio_url','reference_voices'];
   const settings={...Object.fromEntries(Object.entries(options).filter(([key])=>!refKeys.includes(key)&&!['instructions','negative_prompt','lyrics'].includes(key)))};
   for(const key of ['voice','response_format','output_format','quality','background','size','seconds'])if(generation[key]!=null&&settings[key]==null)settings[key]=generation[key];
   if(Object.keys(settings).length)section('Saved settings',facts(settings));
   const inputs={...Object.fromEntries(refKeys.filter(key=>options[key]!=null&&options[key]!==''&&(!Array.isArray(options[key])||options[key].length)).map(key=>[key,options[key]]))};
   if(!request){for(const key of ['image','input_reference'])if(generation[key])inputs[key]=generation[key];}
   if(Object.keys(inputs).length)section('References',references(inputs));
   const results=node('div',null,'detail-media-grid');
   if(data.links?.content_url){const card=mediaCard(data.links.content_url,'Result',mediaKind(generation.result_content_type,generation.media_type),{download:data.links.download_url,poster:data.links.last_frame_url});if(card)results.append(card);}
   if(data.links?.last_frame_url){const card=mediaCard(data.links.last_frame_url,'Last frame','image');if(card)results.append(card);}
   if(!data.links?.content_url&&data.links?.download_url){const card=mediaCard(data.links.download_url,'Result','file',{download:data.links.download_url});if(card)results.append(card);}
   if(!results.children.length)results.append(node('p',generation.result_deleted_at?'Result files have been deleted.':['completed','succeeded'].includes(generation.status)?'No stored result is available.':'No result is available yet.'));
   section('Results',results);
   if(generation.error)section('Error',valueNode(generation.error));
   if(request?.webhook_url)section('Webhook',valueNode(request.webhook_url));
  }
  if(generation.usage||report.usage)section('Usage',facts(generation.usage||report.usage));
  if(report.cost||generation.cost)section('Cost',facts(report.cost||generation.cost));
  const all=node('details',null,'detail-record');all.append(node('summary','All retained data'),node('pre',JSON.stringify(data,null,2)));parent.append(all);
 }
 function open(endpoint,trigger){
  close();const dialog=node('dialog',null,'keys-revoke-dialog detail-dialog'),title=FormUI.sectionHeading(dialog,'Generation details'),header=title.parentElement,actions=node('div',null,'detail-actions'),refresh=node('button','Refresh','secondary'),dismiss=node('button','Close','secondary'),status=node('p'),body=node('div');
  header.classList.add('detail-heading');dialog.setAttribute('aria-label','Generation details');refresh.type=dismiss.type='button';actions.append(refresh,dismiss);header.append(actions);dialog.append(status,body);status.setAttribute('role','status');
  const state={dialog,serial:0};active=state;
  async function load(){const serial=++state.serial;refresh.disabled=true;FormUI.message(status,'Loading generation…');body.replaceChildren();try{const data=await api(endpoint);if(active!==state||!dialog.open||serial!==state.serial)return;render(body,data);FormUI.message(status,'');}catch(error){if(active===state&&dialog.open)FormUI.message(status,'Could not load generation: '+error.message,'error');}finally{if(active===state)refresh.disabled=false;}}
  refresh.onclick=load;dismiss.onclick=()=>dialog.close();dialog.onclose=()=>{state.serial++;const restore=active===state;if(restore)active=null;dialog.remove();if(restore&&trigger?.isConnected)trigger.focus();};
  document.body.append(dialog);dialog.showModal();dismiss.focus();load();
 }
 window.ReportDetails={open,close};
})();
