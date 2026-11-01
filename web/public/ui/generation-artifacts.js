// Archived result links are granted by the existing authorized media-access API.
(() => {
 'use strict';
 const node=(tag,text='',cls='')=>Object.assign(document.createElement(tag),{textContent:text,className:cls});
 async function attach(article,task,{api,context,bytes}){
  const controller=new AbortController(),identity=context(),current=()=>article.isConnected&&!controller.signal.aborted&&identity===context();article.mediaController=controller;
  const result=article.querySelector('.task-result'),groups=new Map(),entries=[];result.classList.add('generation-outputs');
  for(const artifact of task.artifacts||[]){
   if(!/^[1-9][0-9]*$/.test(artifact.id))continue;
   const group=artifact.group_id||'artifact:'+artifact.id;if(!groups.has(group))groups.set(group,groups.size+1);
   const card=node('section','','generation-output'),title=node('h4',`Output ${groups.get(group)}${artifact.format?' · '+artifact.format.toUpperCase():''}`),status=node('p','','media-note'),actions=node('div','','media-actions');status.setAttribute('role','status');card.append(title);
   const type=artifact.content_type||'',kind=artifact.media_type,textual=['text','json'].includes(kind)||type==='text/plain'||type==='application/json',attachment=kind==='binary'||type==='application/octet-stream';let player=null;
   if(textual&&!attachment){player=node('div','','artifact-text');player.setAttribute('role','region');player.setAttribute('aria-label',title.textContent+' text');player.tabIndex=0;}
   else if(type.startsWith('image/')||kind==='image'){player=document.createElement('img');player.alt=title.textContent;}
   else if(type.startsWith('audio/')||['audio','music','speech'].includes(kind)){player=document.createElement('audio');player.controls=true;player.preload='metadata';}
   else if(type.startsWith('video/')||kind==='video'){player=document.createElement('video');player.controls=true;player.preload='metadata';player.playsInline=true;}
   if(player){player.className=textual?'artifact-text':'task-media';card.append(player);}
   const facts=[kind,artifact.size_bytes>0?bytes(artifact.size_bytes):'',artifact.duration>0?artifact.duration+' s':''].filter(Boolean).join(' · ');status.textContent=facts;card.append(status,actions);result.append(card);
   const buttons=[],feedback=text=>{status.textContent=[facts,text].filter(Boolean).join(' · ');};let pending=false,textRead=null,textSequence=0;
   const access=()=>api('/v1/generations/'+encodeURIComponent(task.id)+'/media-access',{signal:controller.signal,cache:'no-store'});
   const links=bundle=>(bundle.artifacts||[]).find(value=>value.id===artifact.id);
   const clear=()=>{textSequence++;textRead?.abort();if(textual&&player)player.textContent='';if(player){player.pause?.();player.removeAttribute('src');player.removeAttribute('poster');player.load?.();}};
   const unavailable=error=>{if(error.status===404)entries.forEach(entry=>entry.gone());else feedback('Preview unavailable. Try again.');};
   function action(text,fn){const button=node('button',text,'secondary');button.type='button';button.onclick=fn;actions.append(button);buttons.push(button);return button;}
   function lock(value){pending=value;buttons.forEach(button=>button.disabled=value);}
   let lastFrameAction=null;const primaryVideo=player?.tagName==='VIDEO'&&!entries.some(e=>e.video);
   async function openLink(field,download){
    if(pending||!current())return;const tab=download?null:window.open('about:blank','_blank');if(!download&&!tab){feedback('Allow pop-ups to open saved media.');return;}if(tab){tab.opener=null;const meta=document.createElement('meta');meta.name='referrer';meta.content='no-referrer';tab.document.head.append(meta);}lock(true);
    try{const bundle=await access();if(!current()){tab?.close();return;}const url=field==='last_frame_url'?bundle.last_frame_url:links(bundle)?.[field];if(!url){tab?.close();if(field==='last_frame_url'){player?.removeAttribute('poster');if(lastFrameAction)lastFrameAction.hidden=true;feedback('Last frame is unavailable.');}else markGone();return;}const link=(tab?.document||document).createElement('a');link.href=url;link.rel='noopener noreferrer';if(download)link.download=task.id+'-'+artifact.id;(tab?.document.body||document.body).append(link);link.click();if(download)link.remove();feedback('');}
    catch(error){tab?.close();if(current()&&error.name!=='AbortError')unavailable(error);}finally{if(current())lock(false);}
   }
   for(const [label,field,download]of [...(attachment?[]:[['Open ↗','preview_url',false]]),['Download ↓','download_url',true]])action(label,()=>openLink(field,download));
   const retry=action('Retry preview',async()=>{if(pending||!current())return;lock(true);try{const bundle=await access();if(current())show(bundle);}catch(error){if(current()&&error.name!=='AbortError')unavailable(error);}finally{if(current())lock(false);}});retry.hidden=true;
   function markGone(){clear();retry.hidden=false;if(lastFrameAction)lastFrameAction.hidden=true;feedback('Saved output is no longer available.');}
   function show(bundle){const link=links(bundle);if(attachment){retry.hidden=true;if(link?.download_url)feedback('Attachment only');else feedback('Saved attachment is no longer available.');return;}if(!link?.preview_url){markGone();return;}if(primaryVideo){if(bundle.last_frame_url){player.poster=bundle.last_frame_url;if(!lastFrameAction)lastFrameAction=action('Last frame ↗',()=>openLink('last_frame_url',false));lastFrameAction.hidden=false;}else{player.removeAttribute('poster');if(lastFrameAction)lastFrameAction.hidden=true;}}if(textual){readText(link.preview_url);return;}if(player)player.src=link.preview_url;retry.hidden=true;feedback('');}

   async function readText(url){
    textRead?.abort();textRead=new AbortController();const version=++textSequence;player.textContent='';feedback('Loading text…');
    try{const response=await fetch(url,{credentials:'omit',redirect:'error',referrerPolicy:'no-referrer',signal:AbortSignal.any([controller.signal,textRead.signal])});if(!response.ok)throw Object.assign(Error('Text preview unavailable'),{status:response.status});const reader=response.body.getReader(),decoder=new TextDecoder();let value='',size=0,truncated=false;while(true){const part=await reader.read();if(part.done)break;const remaining=65536-size;if(part.value.length>remaining){value+=decoder.decode(part.value.subarray(0,remaining));truncated=true;await reader.cancel();break;}size+=part.value.length;value+=decoder.decode(part.value,{stream:true});}value+=decoder.decode();if(!current()||version!==textSequence)return;player.textContent=value;retry.hidden=true;feedback(truncated?'Preview truncated. Download the full output.':'');}
    catch(error){if(current()&&version===textSequence&&error.name!=='AbortError'){retry.hidden=false;unavailable(error);}}
   }
   if(player)player.onerror=()=>{if(current()){retry.hidden=false;feedback('Preview unavailable. Retry preview.');}};
   entries.push({show,lock,clear,gone:markGone,video:player?.tagName==='VIDEO',fail(error){retry.hidden=false;unavailable(error);}});
  }
  if(!entries.length){result.textContent='Saved outputs are unavailable.';return;}
  entries.forEach(entry=>entry.lock(true));
  try{const bundle=await api('/v1/generations/'+encodeURIComponent(task.id)+'/media-access',{signal:controller.signal,cache:'no-store'});if(current())entries.forEach(entry=>entry.show(bundle));}
  catch(error){if(current()&&error.name!=='AbortError'){if(error.status===404)entries.forEach(entry=>entry.gone());else entries.forEach(entry=>entry.fail(error));}}
  finally{if(current())entries.forEach(entry=>entry.lock(false));}
  controller.signal.addEventListener('abort',()=>{for(const entry of entries)entry.clear?.();for(const player of result.querySelectorAll('.task-media')){player.pause?.();player.removeAttribute('src');player.removeAttribute('poster');player.load?.();}},{once:true});
 }
 window.GenerationArtifacts={attach};
})();
