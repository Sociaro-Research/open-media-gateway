(() => {
 'use strict';
 const content=document.querySelector('#templatesContent'),status=document.querySelector('#templatesStatus'),browser=document.querySelector('#templatesBrowser'),galleryHost=document.querySelector('#templatesGallery'),selection=document.querySelector('#templateSelection'),headingActions=document.querySelector('#templateHeadingActions');
 let requestedEdit=new URLSearchParams(location.search).get('edit');
 let canEdit=false,authorRecords=[],hovered=null;
 const stopHover=()=>{const previous=hovered;hovered=null;previous?.player.pause();};
 // WebKit may omit leave events when a pointer exits native video controls.
 document.addEventListener('pointermove',event=>{if(event.pointerType==='mouse'&&hovered&&!hovered.card.contains(event.target))stopHover();},true);
 window.addEventListener('blur',stopHover);
 const reducedMotion=matchMedia('(prefers-reduced-motion: reduce)');reducedMotion.addEventListener('change',()=>{if(reducedMotion.matches)stopHover();});
 let active=false,epoch=0,controller=new AbortController(),writing=false,editing=null,adminCatalog=[],browseID='',uploading=0,createID='';
 const localDrafts=new Map(),failedMedia=new Set(),runnerDrafts=new Map();
 window.addEventListener('gateway-reference-deleted',event=>{
  const {id,scope,keyVersion}=event.detail;
  if(scope==='organization'&&keyVersion===generationKeyVersion){for(const [templateID,draft] of runnerDrafts){if(active&&view==='use'&&browseID===templateID)continue;for(const slot of Object.keys(draft.inputs))if(draft.inputs[slot]===id){delete draft.inputs[slot];delete draft.files[slot];}}return;}
  if(scope!=='template')return;
  for(const record of localDrafts.values()){for(const slot of record.draft.slots||[])if(slot.asset_id===id)slot.asset_id='';if(record.draft.cover_id===id)record.draft.cover_id='';if(record.draft.override_sound_id===id)record.draft.override_sound_id='';}
 });
 let listFingerprint='';
 let view='gallery',useUpdate=null,editorUpdate=null,activeRevision=null,validationPending=true;
 function currentRecord(record){const content=record.content||record.draft||record.published,visible=typeof record.is_published==='boolean'?record.is_published:!!record.published;return {...record,content,draft:content,is_published:visible,published:visible?content:null};}
 async function currentChanged(id,revision){if(!active||browseID!==id||view!=='use'||activeRevision!==null&&revision!==undefined&&activeRevision!==revision)return;runnerDrafts.delete(id);window.GatewayTemplateConfirmations?.invalidate(id);await use(id);if(active&&browseID===id&&view==='use'&&activeRevision!==null)say('Template updated. Review the current settings and choose references again.');}
 function replaceUse(detail,id){runnerDrafts.delete(id);window.GatewayTemplateConfirmations?.invalidate(id);const token=next();view='use';browseID=id;showBrowser();emptySelection();renderUse(detail,token,id);say('Template updated. Review the current settings and choose references again.');}
 const updates=GatewayAutoUpdate.watch({root:document.querySelector('#templatesView'),active:()=>active,load:update});
 const el=(tag,text='',cls='')=>{const n=document.createElement(tag);n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,fn,cls='secondary')=>{const b=el('button',text,cls);b.type='button';b.onclick=fn;return b;};
 const say=(text='',type='')=>FormUI.message(status,text,type);
 const templateModes=m=>(m?.reference_modes||[]).filter(x=>x.id==='references'&&(x.max_images||0)+(x.max_videos||0)+(x.max_audios||0)>0);
 const eligible=m=>['image','video'].includes(m.media_type)&&!m.no_prompt&&templateModes(m).length>0;
 function stopMedia(root){if(hovered&&root.contains(hovered.card))stopHover();for(const asset of root.querySelectorAll('video,audio')){asset.pause();asset.removeAttribute('src');asset.load();}}
 function next(){editorUpdate=null;stopHover();for(const asset of document.querySelectorAll('#templatesView video,#templatesView audio'))asset.pause();listFingerprint='';epoch++;uploading=0;writing=false;controller.abort();controller=new AbortController();return epoch;}
 const fresh=token=>active&&token===epoch;
 function fail(error,token){if(fresh(token)&&error.name!=='AbortError')say(error.message,'error');}
 function field(form,label,type='text',value=''){const l=el('label',label),input=el(type==='textarea'?'textarea':type==='select'?'select':'input');if(input.tagName==='INPUT')input.type=type;input.setAttribute('aria-label',label);if(type!=='select')input.value=value??'';l.append(input);form.append(l);return input;}
 function media(url,type,label,{hover=false}={}){
  const key=type+':'+url,card=el('div','','template-media'),asset=el(type.startsWith('video/')?'video':type.startsWith('audio/')?'audio':'img');card.dataset.updateKey='media:'+key;asset.src=url;asset.setAttribute('aria-label',label);
  if(['VIDEO','AUDIO'].includes(asset.tagName)){asset.controls=true;asset.preload='metadata';asset.playsInline=true;}else asset.alt=label;
  const unavailable=()=>{failedMedia.add(key);asset.hidden=true;if(!card.querySelector('p'))card.append(el('p','Preview unavailable.'));};asset.addEventListener('error',unavailable,{once:true});card.append(asset);
  asset.addEventListener(['VIDEO','AUDIO'].includes(asset.tagName)?'loadedmetadata':'load',()=>{failedMedia.delete(key);asset.hidden=false;card.querySelector('p')?.remove();});if(failedMedia.has(key))unavailable();
  if(hover&&asset.tagName==='VIDEO'){
   asset.muted=true;asset.loop=true;
   asset.controls=false;asset.tabIndex=0;asset.setAttribute('aria-description','Hover to preview. Press Space or Enter, or tap the video, to play or pause.');
   const play=async player=>{const host=player.closest('.template-media');host?.querySelector('[data-playback-hint]')?.remove();try{await player.play();host?.querySelector('[data-playback-hint]')?.remove();}catch(error){if(error.name==='AbortError'||!host?.isConnected)return;if(!host.querySelector('[data-playback-hint]')){const hint=el('p','Preview did not start. Focus and press Space, or tap the video, to try playback.','field-hint');hint.dataset.playbackHint='';host.append(hint);}}};
   const toggle=player=>{if(hovered?.player===player)hovered=null;if(player.paused)play(player);else player.pause();};
   asset.onkeydown=event=>{if(![' ','Enter'].includes(event.key))return;event.preventDefault();toggle(event.currentTarget);};
   let touchIntent=null;
   asset.ontouchstart=event=>{const t=event.touches;touchIntent=t.length===1?{id:t[0].identifier,x:t[0].clientX,y:t[0].clientY}:null;};
   asset.ontouchmove=event=>{const t=event.touches;if(!touchIntent||t.length!==1||Math.abs(t[0].clientX-touchIntent.x)>10||Math.abs(t[0].clientY-touchIntent.y)>10)touchIntent=null;};
   asset.ontouchcancel=()=>{touchIntent=null;};
   asset.ontouchend=event=>{const intent=touchIntent;touchIntent=null;const t=[...event.changedTouches].find(t=>t.identifier===intent?.id);if(!intent||event.touches.length||!t||Math.abs(t.clientX-intent.x)>10||Math.abs(t.clientY-intent.y)>10)return;event.preventDefault();const player=event.currentTarget,wasPlaying=!player.paused;player.closest('.template-card')?.querySelector('[data-use-template]')?.click();if(wasPlaying)player.pause();else play(player);};
   asset.onclick=event=>{if(event.pointerType!=='pen')return;event.stopPropagation();const player=event.currentTarget,wasPlaying=!player.paused;player.closest('.template-card')?.querySelector('[data-use-template]')?.click();if(wasPlaying)player.pause();else play(player);};
   card.onpointerenter=e=>{if(e.pointerType!=='mouse'||!matchMedia('(hover: hover)').matches||reducedMotion.matches)return;const current=e.currentTarget,player=current.querySelector('video');if(!player?.paused)return;stopHover();hovered={card:current,player};play(player).then(()=>{if(player.paused&&hovered?.player===player)hovered=null;});};card.onpointerleave=e=>{if(e.pointerType==='mouse'&&hovered?.card===e.currentTarget)stopHover();};card.onfocusout=e=>{if(!e.currentTarget.contains(e.relatedTarget))e.currentTarget.querySelector('video')?.pause();};
  }
  return card;
 }
 function section(name,allowed){
  active=name==='templates'&&allowed;
  if(!active){next();stopMedia(selection);stopMedia(content);stopMedia(galleryHost);}
  const panel=document.querySelector('#connectionPanel');
  if(active){document.querySelector('#templateKeyHost').append(panel);document.querySelector('#priceCard').hidden=true;}
  else{document.querySelector('#taskForm').insertBefore(panel,document.querySelector('#priceCard'));}
 }
 async function open(){
  if(editing&&systemAdministrator&&localDrafts.has(editing.id)){renderEditor(localDrafts.get(editing.id));return;}
  return gallery();
 }
 function showBrowser(){headingActions.hidden=!canEdit;browser.hidden=false;content.hidden=true;stopMedia(content);content.replaceChildren();}
 function emptySelection(){const keyHost=document.querySelector('#templateKeyHost'),access=document.querySelector('#templateKeyAccess');access.hidden=!!GenerationKey.value();if(access.hidden){if(keyHost.parentElement!==selection.parentElement)selection.after(keyHost);}else if(keyHost.parentElement!==access)access.append(keyHost);stopMedia(selection);selection.replaceChildren();selection.closest('aside').hidden=!browseID;browser.classList.toggle('has-selection',!!browseID);useUpdate=null;}
 async function gallery(){
  const id=browseID;next();view=id?'use':'gallery';editing=null;showBrowser();emptySelection();document.querySelector('#priceCard').hidden=true;
  return updates.refresh({restart:true,quiet:false});
 }
 function selectedCard(){for(const card of galleryHost.querySelectorAll('[data-template-id]')){const selected=card.dataset.templateId===browseID;card.classList.toggle('selected',selected);card.querySelector('[data-use-template]')?.setAttribute('aria-pressed',String(selected));}}
 function renderGallery(result){
  const nextGrid=el('div');
  const actions=el('div');headingActions.hidden=!canEdit;
  if(canEdit){const action=button('Create template',create,'primary compact');action.disabled=!adminCatalog.length;actions.append(action,button('Template references',()=>chooseLibrary('',true,null)));if(!adminCatalog.length){const hint=el('p','No models with a References workflow are available for templates.','field-hint');hint.id='templateAvailability';action.setAttribute('aria-describedby',hint.id);actions.append(hint);}}
  GatewayAutoUpdate.reconcile(headingActions,actions);
  const seen=new Set();
  for(const item of result.data||[]){seen.add(item.id);const card=el('article','','card template-card');card.dataset.updateKey=item.id;card.dataset.templateId=item.id;card.onclick=event=>{if(event.defaultPrevented||event.target.closest('button,a,input,select,textarea'))return;event.currentTarget.querySelector('[data-use-template]')?.click();};card.append(media(item.cover_url,item.cover_type,item.title,{hover:true}));const select=button(item.title,()=>use(item.id),'template-card-title');select.dataset.useTemplate='';select.setAttribute('aria-pressed',String(browseID===item.id));card.append(select);if(item.description)card.append(el('p',item.description));if(canEdit){const record=authorRecords.find(r=>r.id===item.id);if(record){const edit=button('Edit',event=>{event.stopPropagation();renderEditor(localDrafts.get(record.id)||record);});edit.setAttribute('aria-label','Edit '+(record.draft.title||item.title));card.append(edit);}}nextGrid.append(card);}
  if(canEdit)for(const record of authorRecords){if(seen.has(record.id))continue;const card=el('article','','card template-card template-draft-card');card.dataset.updateKey=record.id;card.append(el('h2',record.draft.title||'Untitled template'),el('p',record.published?'Published template unavailable to this key':'Not published'));const edit=button('Edit',()=>renderEditor(localDrafts.get(record.id)||record));edit.setAttribute('aria-label','Edit '+(record.draft.title||'Untitled template'));card.append(edit);nextGrid.append(card);}
  if(!nextGrid.childElementCount)nextGrid.append(el('p','No templates have been published yet.'));
  for(const old of galleryHost.children){const incoming=[...nextGrid.children].find(n=>n.dataset.updateKey===old.dataset.updateKey);if(!incoming||old.querySelector('.template-media')?.dataset.updateKey!==incoming.querySelector('.template-media')?.dataset.updateKey)stopMedia(old);}
  GatewayAutoUpdate.reconcile(galleryHost,nextGrid);selectedCard();
 }
 async function update({signal,current,quiet}){
  if(writing||uploading)return;
  const token=epoch,screen=view,id=browseID;const valid=()=>current()&&fresh(token)&&view===screen;
  if(!quiet)say('Loading templates…');
  let result=null,failure=null;
  const renderList=()=>{if(!valid()||!result||screen==='edit')return;const signature=JSON.stringify([canEdit,result,authorRecords,adminCatalog]);if(signature!==listFingerprint){listFingerprint=signature;renderGallery(result);}};
  const read=operation=>operation().catch(error=>{if(valid()&&error.name!=='AbortError'){failure=error;say(error.message,'error');}});
  // These reads have separate authorization and rendering responsibilities.
  // A slow authoring catalogue must not hold the gallery or selected detail.
  async function authoring(){
   let admin=null,allowed=false;
   if(systemAdministrator){try{admin=await api('/v1/system/templates',{signal});if(!valid())return;allowed=true;}catch(e){if(e.status!==403)throw e;}}
   if(!valid())return;
   canEdit=allowed;authorRecords=canEdit?(admin.data||[]).map(currentRecord):[];adminCatalog=canEdit?(admin.catalog||[]).map(p=>({...p,models:p.models.filter(eligible)})).filter(p=>p.models.length):[];
   if(requestedEdit&&screen!=='edit'){const id=requestedEdit;requestedEdit=null;const record=canEdit&&authorRecords.find(item=>item.id===id);if(record){renderEditor(localDrafts.get(record.id)||record);return;}say('This template is unavailable for editing.','error');}
   if(screen==='edit'){if(!canEdit){editing=null;await gallery();say('Template editing permission is no longer available.','error');}else await editorUpdate?.();return;}
   renderList();
  }
  async function listing(){
   if(screen==='edit')return;
   const incoming=GenerationKey.value()?await api('/v1/templates',{signal}):{data:[]};if(!valid())return;
   result=incoming;renderList();
   if(!GenerationKey.value()){emptySelection();if(!failure)say('Enter the same Gateway API key used in Playground to browse and generate.');}
   else if(screen!=='use'&&!failure)say('');
  }
  async function detail(){
   if(screen!=='use'||!id||!GenerationKey.value())return;
   let selectedToken=token;const keyVersion=generationKeyVersion,key=GenerationKey.value();let latestMetadata=null;
   const selected=()=>current()&&fresh(selectedToken)&&view==='use'&&browseID===id&&generationKeyVersion===keyVersion&&GenerationKey.value()===key;
   validationPending=true;window.GatewayTemplateConfirmations?.invalidate(id);window.dispatchEvent(new CustomEvent('gateway-template-validation-pending',{detail:{id}}));useUpdate?.pending?.();
   for(let attempt=0;attempt<2;attempt++){
    try{
     const metadata=await api('/v1/templates/'+encodeURIComponent(id)+'/metadata',{signal});if(!selected())return;
     if(metadata.id!==id||!Number.isInteger(metadata.revision)||metadata.revision<1||metadata.validation_status!=='pending'||!metadata.template?.title)throw Error('Template validation is unavailable. Update the server and retry.');
     latestMetadata=metadata;
     const changed=activeRevision!==null&&activeRevision!==metadata.revision;
     if(changed||attempt){runnerDrafts.delete(id);window.GatewayTemplateConfirmations?.invalidate(id);selectedToken=next();activeRevision=null;}
     if(activeRevision===null||!useUpdate?.pending)renderPending(metadata);
     activeRevision=metadata.revision;
     const incoming=await api('/v1/templates/'+encodeURIComponent(id)+'?revision='+encodeURIComponent(metadata.revision),{signal});if(!selected())return;
     if(incoming.id!==id||incoming.revision!==metadata.revision||!['ready','failed'].includes(incoming.validation_status)||!Array.isArray(incoming.catalog)||draftStamp(incoming.content||incoming.template)!==draftStamp(metadata.template))throw Error('Template validation is unavailable. Update the server and retry.');
     if(incoming.validation_status==='failed'){renderPending(metadata,incoming.asset_error||incoming.workflow_error||'This template is unavailable. Check its references and model, then retry.');return;}
     validationPending=false;
     if(useUpdate?.pending)useUpdate(incoming);else renderUse(incoming,selectedToken,id);
     if(changed||attempt)say('Template updated. Review the current settings and choose references again.');
     return;
    }catch(error){
     if(!selected()||error.name==='AbortError')return;
     if(error.status===409&&attempt===0){runnerDrafts.delete(id);window.GatewayTemplateConfirmations?.invalidate(id);continue;}
     if([403,404,410].includes(error.status))clearSelected(id);
     else {validationPending=true;if(latestMetadata){renderPending(latestMetadata,error.message);say('');return;}useUpdate?.pending?.();say(error.message,'error');}
     throw error;
    }
   }
  }
  await Promise.all([read(authoring),read(listing),read(detail)]);
 }

 async function chooseLibrary(kind,administrator,onSelect,validationContext=null,selectedID=()=>null,onClear=null,returnTarget=null){
  if(!administrator&&validationPending)return;
  const selectedTemplate=browseID;
  const token=epoch,parentSignal=controller.signal,operation=new AbortController(),signal=operation.signal,previousFocus=returnTarget||document.activeElement;
  let busy=false,uploadPending=false,offset=0,hasMore=false,items=[],sequence=0;
  const kinds=kind?[kind]:administrator&&onSelect?['image','video']:['image','video','audio'],previousSlot=previousFocus?.closest('[data-slot-id]')?.dataset.slotId;
  const compatible=item=>kinds.some(type=>(item.content_type||'').startsWith(type+'/'));
  const endpoint=administrator?'/v1/system/template-references':'/v1/references';
  const previousUpload=previousFocus?.closest('[data-reference-upload]');
  const close=()=>{view.stop();operation.abort();dialog.close();dialog.remove();const host=previousUpload?.isConnected?previousUpload:previousSlot?document.querySelector(`[data-slot-id="${CSS.escape(previousSlot)}"] [data-reference-upload]`):null;const focus=host?.querySelector('[data-upload-remove]:not([hidden]):not(:disabled)')||host?.querySelector('[data-upload-picker]:not([hidden]):not(:disabled)')||(previousFocus?.isConnected&&!previousFocus.hidden&&!previousFocus.disabled?previousFocus:null);if(focus){focus.focus();requestAnimationFrame(()=>{if(fresh(token)&&!parentSignal.aborted&&focus.isConnected&&!focus.disabled&&!focus.hidden&&(document.activeElement===document.body||document.activeElement===previousFocus))focus.focus({preventScroll:true});});}};
  const view=ReferenceLibrary.create({title:administrator?'Template references':'Reference library',description:administrator?'Shared template files. Administrators manage this library. Upload a file, then select it to assign a fixed slot.':'Choose a reference for this slot.',onClose:close});
  const {dialog,status:message,more}=view;dialog.classList.add('template-reference-library');
  function render(){view.render(items.filter(compatible),{
   selected:item=>item.id===selectedID(),busy:busy||uploadPending,onSelect:onSelect?select:null,
   onDelete:remove
  });more.hidden=!hasMore;more.disabled=busy||uploadPending;uploader.sync();}
  const uploader=ReferenceUpload.create({label:administrator?'Template reference library':'Reference library',title:'Drop a reference to add it to the library',uploadLabel:'Upload reference',kinds,signal,locked:()=>busy||!administrator&&validationPending,onBusy:value=>{uploadPending=value;render();},onFile:async(file,uploadSignal)=>{
   let item;
   if(administrator){const body=new FormData();body.append('file',file);const context=validationContext?.();if(context)body.append('validation',JSON.stringify(context));item=await api(endpoint,{method:'POST',body,signal:uploadSignal});}
   else item=await uploadReference(file,uploadSignal,validationContext?.(),text=>uploader.message(text));
   uploadSignal.throwIfAborted();await load();return item.name+' uploaded to '+(administrator?'Template references':'your library')+'. No slot assigned.';
  }});view.intro.after(uploader.root);
  async function load(append=false){if(signal.aborted)return;const stamp=++sequence;busy=true;message.textContent='Loading library…';render();
   try{const result=await api(endpoint+'?offset='+(append?offset:0),{signal});if(stamp!==sequence||!fresh(token)||signal.aborted)return;
    items=append?[...items,...(result.data||[]).filter(item=>!items.some(old=>old.id===item.id))]:(result.data||[]);offset=result.next_offset||items.length;hasMore=!!result.has_more;
    message.textContent=items.some(compatible)?'':'No matching files. Upload a reference or load more.';
   }catch(error){if(!signal.aborted)message.textContent=error.message;}finally{if(stamp===sequence){busy=false;if(!signal.aborted)render();}}
  }
  async function select(item){if(busy||uploadPending||signal.aborted||!administrator&&validationPending)return;busy=true;render();message.textContent='Checking reference…';
   try{if(item.id===selectedID()&&onClear)await onClear();else await onSelect(item,signal);if(fresh(token)&&!signal.aborted)close();}catch(error){if(!signal.aborted)message.textContent=error.message;}finally{busy=false;if(!signal.aborted)render();}
  }
  async function remove(item){if(busy||uploadPending||signal.aborted||!administrator&&validationPending)return;busy=true;render();
   try{await api(endpoint+'/'+encodeURIComponent(item.id),{method:'DELETE',signal});signal.throwIfAborted();window.ReferenceLibrary.deleted(item,administrator?'template':'organization');await load();message.textContent='Deleted '+item.name+'.';window.dispatchEvent(new Event('gateway-storage-change'));}
   catch(error){if(!signal.aborted){message.textContent=error.message;for(const use of error.templates||[]){const link=el('a',use.name+' · '+use.id);link.href=use.url;link.onclick=async event=>{event.preventDefault();try{const record=currentRecord(await api('/v1/system/templates/'+encodeURIComponent(use.id),{signal}));close();renderEditor(localDrafts.get(record.id)||record);}catch(e){if(!signal.aborted)message.textContent=e.message;}};message.append(document.createElement('br'),link);}}}
   finally{busy=false;if(!signal.aborted)render();}
  }
  if(!administrator)window.addEventListener('gateway-template-validation-pending',event=>{if(event.detail.id===selectedTemplate)close();},{signal});
  more.onclick=()=>load(true);dialog.addEventListener('cancel',event=>{event.preventDefault();close();});parentSignal.addEventListener('abort',close,{once:true});signal.addEventListener('abort',()=>parentSignal.removeEventListener('abort',close),{once:true});document.body.append(dialog);dialog.showModal();await load();
 }
 async function uploadReference(file,signal,validation,onProgress){
  await GenerationKey.ensureSession(true);await window.StorageQuota?.check();
  return ReferenceValidation.upload(file,{signal,validation,onProgress,expectedKey:GenerationKey.value()});
 }
 async function use(id){
  next();const token=epoch;activeRevision=null;view='use';editing=null;browseID=id;showBrowser();emptySelection();selection.replaceChildren(el('p','Loading selected template…'));useUpdate=detail=>{if(!fresh(token)||browseID!==id)return;if(detail)renderUse(detail,token,id);else clearSelected(id);};selectedCard();return updates.refresh({restart:true,quiet:false});
 }
 function clearSelected(id){runnerDrafts.delete(id);window.GatewayTemplateConfirmations?.invalidate(id);browseID='';activeRevision=null;controller.abort();controller=new AbortController();emptySelection();selectedCard();say('This template is unavailable. Choose another template.','error');}
 function renderPending(metadata,error=''){
  validationPending=true;stopMedia(selection);useUpdate=null;
  const d=metadata.template,card=el('div','','card template-use template-validation-pending');card.dataset.validationStatus=error?'failed':'pending';card.setAttribute('aria-busy',String(!error));card.append(el('h2',d.title));if(d.description)card.append(el('p',d.description));
  const settings=[d.options?.ratio,d.options?.resolution,d.options?.duration?d.options.duration+' s':null,d.override_sound_id?'Overridden Audio':d.options?.generate_audio?'Audio On':'Audio Off'].filter(Boolean);if(settings.length)card.append(el('p',settings.join(' · '),'field-hint'));
  const message=el('p',error||'Checking template references and model compatibility…','field-hint');message.setAttribute('role','status');card.append(message);
  const advanced=button('Advanced editing',()=>{});advanced.disabled=true;card.append(advanced);
  if(error)card.append(button('Retry validation',()=>updates.refresh({restart:true,quiet:false})));
  const generate=button('Review generation',()=>{},'primary');generate.disabled=true;
  document.querySelector('#templateKeyAccess').hidden=true;selection.replaceChildren(card,document.querySelector('#templateKeyHost'),generate);selection.closest('aside').hidden=false;browser.classList.add('has-selection');
 }
 function renderUse(view,token,id){
   if(view.validation_status!=='ready')return;
   activeRevision=view.revision;const d=view.content||view.template,form=el('form','','card template-use');form.id='templateUseForm';form.autocomplete='off';
   form.append(el('h2',d.title));if(d.description)form.append(el('p',d.description));
   const fixedRow=el('div','','template-fixed-references');fixedRow.setAttribute('aria-label','Template references');
   for(const slot of d.slots.filter(s=>s.asset_id)){
    const fixed=(view.fixed_references||[]).find(a=>a.slot_id===slot.id&&a.asset_id===slot.asset_id);
    const kind=fixed?.content_type?.split('/')[0]||slot.type;
    if(!['image','video'].includes(kind))continue;
    const cell=el('div','','template-fixed-media');
    if(!fixed?.url)cell.append(el('p','Preview unavailable.','field-hint'));
    else{const asset=el(kind==='image'?'img':'video');asset.setAttribute('aria-label',slot.label);asset.src=fixed.url;
     if(kind==='image')asset.alt=slot.label;
     else{asset.muted=true;asset.autoplay=true;asset.loop=true;asset.playsInline=true;asset.preload='auto';asset.tabIndex=-1;asset.setAttribute('aria-description','Muted reference preview');}
     asset.onerror=()=>{if(asset.tagName==='VIDEO'){asset.pause();asset.removeAttribute('src');asset.load();}asset.remove();if(!cell.querySelector('p'))cell.append(el('p','Preview unavailable.','field-hint'));};cell.append(asset);
    }
    fixedRow.append(cell);
   }
   if(fixedRow.childElementCount)form.append(fixedRow);
   const settings=[d.options?.ratio,d.options?.resolution,d.options?.duration?d.options.duration+' s':null,d.override_sound_id?'Overridden Audio':d.options?.generate_audio?'Audio On':'Audio Off'].filter(Boolean);if(settings.length)form.append(el('p',settings.join(' · '),'field-hint'));
   const cached=runnerDrafts.get(id),draft=cached?.revision===view.revision?cached:{revision:view.revision,inputs:{},files:{},model:''};runnerDrafts.set(id,draft);
   const model=field(form,'Model','select');model.required=true;
   ModelPicker.populate(model,view.catalog,{value:draft.model||(view.default_available?ModelPicker.key(d.provider,d.model):''),placeholder:'Choose a compatible model'});
   const inputs=draft.inputs,widgets=[];let pendingFocus=null,focusIntent=0;
   for(const event of ['pointerdown','keydown'])document.addEventListener(event,()=>focusIntent++,{capture:true,signal:controller.signal});
   document.addEventListener('focusin',event=>{if(pendingFocus&&event.target!==pendingFocus.node&&event.target!==document.body)focusIntent++;},{signal:controller.signal});
   let pending=0,unavailable=false,validationIssue='',validationSequence=0,bindingQueue=Promise.resolve();
   const target=()=>{const [p,m]=model.value?JSON.parse(model.value):['',''];return ReferenceValidation.target(p,m,d.options);};
   const checkInputs=async(candidate=inputs,signal=controller.signal)=>{if(!model.value)return;const [provider,modelID]=JSON.parse(model.value);try{await api(`/v1/templates/${id}/validate-media`,{method:'POST',body:JSON.stringify({revision:view.revision,provider,model:modelID,inputs:candidate}),signal});}catch(error){if(error.code==='template_conflict'&&fresh(token))await currentChanged(id,view.revision);throw error;}};
   const ready=()=>{model.disabled=validationPending;window.ControlPickers?.sync(model);advanced.disabled=validationPending||unavailable||!!view.asset_error||!!view.workflow_error||!model.value||pending>0;generate.disabled=validationPending||unavailable||!!view.asset_error||!!view.workflow_error||!!validationIssue||!model.value||pending>0||d.slots.some(s=>!s.asset_id&&!inputs[s.id]);for(const widget of widgets)widget.sync();};
   for(const slot of d.slots){if(slot.asset_id)continue;
    let slotVersion=0,slotAbort=null,previewURL='';
    const begin=()=>{slotAbort?.abort();slotAbort=new AbortController();return {version:++slotVersion,modelSequence:validationSequence,intent:ReferenceValidation.signature(target()),signal:AbortSignal.any([controller.signal,slotAbort.signal])};};
    const current=op=>fresh(token)&&op.version===slotVersion&&op.modelSequence===validationSequence&&op.intent===ReferenceValidation.signature(target());
    const bind=(item,op,file)=>{
     const work=async()=>{op.signal.throwIfAborted();if(!current(op))throw Error('Selection or model changed. Choose the file again.');await checkInputs({...inputs,[slot.id]:item.id},op.signal);op.signal.throwIfAborted();if(!current(op))throw Error('Selection or model changed. Choose the file again.');inputs[slot.id]=item.id;draft.files[slot.id]={...item,url:item.url};validationIssue='';if(previewURL)URL.revokeObjectURL(previewURL);previewURL=file?URL.createObjectURL(file):'';widget.setPreview({url:previewURL||item.url,type:item.content_type||file?.type||slot.type+'/unknown',name:item.name||file?.name||slot.label});widget.message('Assigned '+(item.name||file?.name||slot.label)+' to '+slot.label+'.');};
     const result=bindingQueue.then(work,work);bindingQueue=result.catch(()=>{});return result;
    };
    controller.signal.addEventListener('abort',()=>{slotAbort?.abort();if(previewURL)URL.revokeObjectURL(previewURL);},{once:true});
    function clear(){begin();delete inputs[slot.id];delete draft.files[slot.id];if(previewURL)URL.revokeObjectURL(previewURL);previewURL='';widget.setPreview(null);widget.message('Choose a reference for '+slot.label+'.');validationIssue='';ready();}
    window.addEventListener('gateway-reference-deleted',event=>{if(event.detail.scope==='organization'&&event.detail.keyVersion===generationKeyVersion&&inputs[slot.id]===event.detail.id)clear();},{signal:controller.signal});
    const row=el('div','','template-variable-reference');
    const widget=ReferenceUpload.create({label:slot.label,title:slot.type==='image'?'Drop here an image of the '+slot.label:'Drop here '+(slot.type==='audio'?'an audio file':'a video')+' for '+slot.label,selectedTitle:slot.label,uploadLabel:'Upload '+slot.type,kinds:[slot.type],signal:controller.signal,locked:()=>validationPending||unavailable||!model.value,
     limits:kind=>{const [p,m]=model.value?JSON.parse(model.value):[];return view.catalog.find(x=>x.id===p)?.models.find(x=>x.id===m)?.reference_media_limits?.[kind]||{};},
     onBusy:value=>{pending+=value?1:-1;ready();},
     onFile:async(file,signal)=>{const op=begin();op.signal=AbortSignal.any([op.signal,signal]);const ref=await uploadReference(file,op.signal,target(),text=>{if(current(op))widget.message(text);});await bind(ref,op,file);return 'Assigned '+file.name+' to '+slot.label+'.';},
     onRemove:clear,onLibrary:picker=>chooseLibrary(slot.type,false,async(item,signal)=>{const op=begin();op.signal=AbortSignal.any([op.signal,signal]);pending++;ready();try{await bind(item,op);}finally{pending--;if(fresh(token))ready();}},target,()=>inputs[slot.id],clear,picker.root)
    });widgets.push(widget);widget.root.setAttribute('aria-description','Required '+slot.type+' reference.');if(draft.files[slot.id]){const item=draft.files[slot.id];widget.setPreview({url:item.url,type:item.content_type,name:item.name||slot.label});widget.message('Assigned '+(item.name||slot.label)+'.');}row.append(widget.root);form.append(row);

   }
   const advanced=button('Advanced editing',async()=>{
    if(advanced.disabled)return;const sequence=validationSequence,bindings=JSON.stringify(inputs);pending++;ready();
    try{const [provider,modelID]=JSON.parse(model.value),source=await api(`/v1/templates/${id}/editing`,{method:'POST',body:JSON.stringify({revision:view.revision,provider,model:modelID,inputs:{...inputs}}),signal:controller.signal});if(!fresh(token)||sequence!==validationSequence||bindings!==JSON.stringify(inputs))return;
     // The published recipe stores replacement audio separately from provider
     // options. This same revision was just resolved; ordinary admission will
     // authorize the ID again. The shared Playground reuse path restores it.
     if(d.override_sound_id)source.options={...source.options,override_sound_id:d.override_sound_id};
     await openTemplateInPlayground(source,controller.signal);}
    catch(e){if(e.code==='template_conflict'&&fresh(token))await currentChanged(id,view.revision);else fail(e,token);}finally{pending--;if(fresh(token))ready();}
   });form.append(advanced);
   const generate=el('button','Review generation','primary');generate.type='submit';generate.setAttribute('form',form.id);model.onchange=async()=>{draft.model=model.value;const sequence=++validationSequence,selection=JSON.stringify(inputs);pending++;validationIssue='';ready();say('Checking reference compatibility…');try{await checkInputs();if(fresh(token)&&sequence===validationSequence&&selection===JSON.stringify(inputs))say('References checked.');}catch(e){if(fresh(token)&&sequence===validationSequence&&selection===JSON.stringify(inputs)){validationIssue=e.message;say(e.message,'error');}}finally{pending--;if(fresh(token))ready();}};
   const messages=[];if(view.workflow_error)messages.push(view.workflow_error);else{if(!view.default_available)messages.push('The configured default model is unavailable to this key. Choose an allowed alternative.');if(!view.catalog.some(p=>p.models.length))messages.push('No compatible models are available to this key.');}if(view.asset_error)messages.push(view.asset_error);say(messages.join(' '),messages.length?'error':'');
   form.onsubmit=e=>{e.preventDefault();if(generate.disabled||!form.reportValidity())return;const [provider,modelID]=JSON.parse(model.value);const source={prompt:d.prompt,provider,model:modelID,media_type:d.media_type,options:d.options,template_catalog:view.catalog,template:{id,revision:view.revision,inputs:{...inputs}}};const project=window.GatewayProjects?.selected();if(project)source.project_id=project;openGenerationConfirmation(source,generate);};
   useUpdate=latest=>{
    if(!latest){clearSelected(id);return;}
    if(latest&&(latest.revision!==view.revision||draftStamp(latest.content||latest.template)!==draftStamp(d))){replaceUse(latest,id);return;}
    // Preserve local inputs only while the current server content is unchanged.
    unavailable=!latest||latest.revision!==view.revision||!!latest.asset_error||!!latest.workflow_error||!latest.catalog?.some(p=>p.models.some(m=>ModelPicker.key(p.id,m.id)===model.value));
    ready();const restore=pendingFocus;pendingFocus=null;if(restore)requestAnimationFrame(()=>{if(fresh(token)&&!validationPending&&restore.intent===focusIntent&&restore.node.isConnected&&!restore.node.disabled&&document.activeElement===document.body)restore.node.focus({preventScroll:true});});say(latest?.workflow_error||(unavailable?'This template is unavailable. Choose another template.':''),unavailable?'error':'');
   };
   useUpdate.pending=()=>{const node=document.activeElement;if(!pendingFocus&&(form.contains(node)||node===generate))pendingFocus={node,intent:focusIntent};ready();};
   document.querySelector('#templateKeyAccess').hidden=true;selection.replaceChildren(form,document.querySelector('#templateKeyHost'),generate);selection.closest('aside').hidden=false;browser.classList.add('has-selection');FormUI.refresh();ready();
 }
 async function create(){if(writing||!canEdit)return;const token=epoch;writing=true;
  try{const provider=adminCatalog[0],model=provider?.models[0];if(!model){say('Choose an available model with a References workflow.','error');return;}const id=createID||(createID=crypto.randomUUID()),record=await api('/v1/system/templates',{method:'POST',body:JSON.stringify({id,version:0,content:{title:'',media_type:model.media_type,prompt:'',provider:provider.id,model:model.id,syntax:'',slots:[],options:{reference_mode:'references'},cover_id:''}}),signal:controller.signal});if(fresh(token)){createID='';renderEditor(record);}}catch(e){fail(e,token)}finally{writing=false;}
 }
 function draftStamp(d){const normalized={...d,description:d.description||'',reference_counts:d.reference_counts||{}};const stable=value=>Array.isArray(value)?value.map(stable):value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(key=>[key,stable(value[key])])):value;return JSON.stringify(stable(normalized));}
 function renderEditor(record){
  if(!canEdit||!systemAdministrator)return;record=currentRecord(record);stopMedia(selection);stopMedia(galleryHost);headingActions.hidden=true;browser.hidden=true;content.hidden=false;
  const token=next(),signal=controller.signal;view='edit';editing=record;const draft=structuredClone(record.draft),form=el('form','','card template-editor');let savedDraft=record.savedDraft||structuredClone(record.draft),baseline='',modelOptions=structuredClone(draft.options||{}),mediaChecking=false,mediaIssue='',mediaSequence=0,mediaStamp='';content.replaceChildren(form);say('');
  form.append(button('Back to templates',()=>{localDrafts.set(record.id,snapshotRecord());editing=null;gallery();}));const titleHeading=FormUI.sectionHeading(form,'Edit template'),heading=titleHeading.parentElement;
  const title=field(form,'Title','text',draft.title);title.maxLength=120;
  const description=field(form,'Short description (optional)','textarea',draft.description);description.maxLength=500;
  const model=field(form,'Default model','select');ModelPicker.populate(model,adminCatalog,{value:ModelPicker.key(draft.provider,draft.model),placeholder:'Choose a model'});
  const mode=field(form,'Reference workflow','select');
  const outputSettings=el('section','','generation-settings'),outputGrid=el('div','','settings-grid'),audioGrid=el('div','','toggle-grid'),optionHint=el('p','','field-hint');outputSettings.append(el('strong','Output settings'),outputGrid,audioGrid,optionHint);form.append(outputSettings);
  const generationFields={};const resetOutput=button('Reset output settings',()=>{for(const key of ['ratio','resolution','duration','generate_audio'])delete modelOptions[key];renderGenerationOptions();cache();checkMedia();});outputSettings.append(resetOutput);
  function renderGenerationOptions(){
   outputGrid.replaceChildren();audioGrid.replaceChildren();for(const key of Object.keys(generationFields))delete generationFields[key];const m=selected();if(!m){resetOutput.hidden=true;optionHint.textContent='Choose a model to configure output settings.';return;}
   const notes=[],reference=templateModes(m).find(x=>x.id===mode.value),ratios=reference?.ratios?.length?reference.ratios:m.ratios||[];
   for(const [key,label,choices] of [['resolution','Resolution',m.resolutions||[]],['ratio','Aspect ratio',ratios]]){
    const saved=modelOptions[key];if(!choices.length&&!saved){notes.push(label+' is not configurable for this model.');continue;}
    const group=el('div');outputGrid.append(group);const input=field(group,label,'select');input.required=true;input.append(...choices.map(value=>new Option(value,value)));const value=saved||(key==='ratio'&&m.media_type==='video'&&choices.includes('16:9')?'16:9':choices[0]);if(saved&&!choices.includes(saved))input.append(new Option('Unsupported saved value: '+saved,saved));input.value=value||'';
    const validate=()=>input.setCustomValidity(choices.includes(input.value)?'':'Choose a supported '+label.toLowerCase()+'.');validate();input.onchange=()=>{validate();cache();checkMedia();};generationFields[key]=input;
   }
   if(m.max_duration>0&&!m.duration_from_input||modelOptions.duration){const group=el('div');outputGrid.append(group);const input=field(group,ModelInfo.durationLabel(m),'number',modelOptions.duration||m.default_duration);input.required=true;input.min=m.min_duration;input.max=m.max_duration;input.step=m.duration_step||1;input.setCustomValidity(m.duration_from_input||!(m.max_duration>0)?'Duration is not configurable for this model. Choose a compatible model.':'');generationFields.duration=input;input.oninput=()=>{cache();checkMedia();};}
   else if(m.duration_from_input)notes.push('Duration follows the input media.');else notes.push('Duration is not configurable for this model.');
   if(m.supports_audio||modelOptions.generate_audio!==undefined){const label=el('label','','toggle-option'),text=el('span'),input=el('input');text.append(el('strong','Audio'),el('small',m.audio_always_on?'Always generated by this model':'Synchronized audio'));input.type='checkbox';input.setAttribute('aria-label','Audio');input.checked=modelOptions.generate_audio??!!m.audio_always_on;input.disabled=!!m.audio_always_on&&input.checked;input.setCustomValidity(!m.supports_audio?'This model does not support a generated audio setting.':m.audio_always_on&&!input.checked?'This model always generates audio.':'');input.onchange=()=>{input.disabled=!!m.audio_always_on&&input.checked;input.setCustomValidity(!m.supports_audio?'This model does not support a generated audio setting.':'');cache();checkMedia();};label.append(text,input);audioGrid.append(label);generationFields.generate_audio=input;}
   else notes.push('Generated audio is not supported by this model.');optionHint.textContent=notes.join(' ');resetOutput.hidden=!Object.values(generationFields).some(input=>!input.validity.valid);FormUI.refresh();
  }
  function generationOptions(){const result={...modelOptions};for(const [key,input] of Object.entries(generationFields))result[key]=key==='generate_audio'?input.checked:key==='duration'?Number(input.value):input.value;return result;}

  const prompt=field(form,'Prompt','textarea',draft.prompt);prompt.rows=5;
  const countFields=el('div');form.append(countFields);const counts={};
  for(const kind of ['image','video','audio']){const input=field(countFields,kind[0].toUpperCase()+kind.slice(1)+' input count','number',draft.reference_counts?.[kind]??(Math.max(0,...(draft.slots||[]).filter(s=>s.type===kind).map(s=>s.index))||''));input.min=0;input.max=64;input.step=1;counts[kind]=input;}
  countFields.append(el('p','For workflows without prompt markers, declare the ordered inputs. Leave blank to use the workflow minimum. The prompt is submitted unchanged.','field-hint'));
  const slots=el('div','','template-slots');let bindings=structuredClone(draft.slots||[]),syntax=ReferencePrompts.syntax,coverID=draft.cover_id||'';
  let overrideSoundID=draft.override_sound_id||'';
  const sound=OverrideSound.create({value:overrideSoundID,signal,load:id=>loadImage(id),locked:()=>writing||uploading>0,onBusy:busy=>{if(!fresh(token))return;uploading+=busy?1:-1;lock(uploading>0);},onChange:id=>{if(!fresh(token))return;overrideSoundID=id;cache();},upload:async(file,operationSignal)=>{const body=new FormData();body.append('file',file);return api(`/v1/system/templates/${record.id}/assets`,{method:'POST',body,signal:operationSignal});}});
  form.append(sound.root);
  let promptEditor=null,coverUpload=null;const widgets=new Set(),assetPreviews=new Map(),imageLoads=new Map(),metadataLoads=new Map(),assetNames=new Map();
  signal.addEventListener('abort',()=>{for(const item of assetPreviews.values())if(item.url)URL.revokeObjectURL(item.url);assetPreviews.clear();},{once:true});
  const assetTypes=new Map();for(const s of bindings)if(s.asset_id)assetTypes.set(s.asset_id,s.type+'/unknown');if(coverID&&!assetTypes.has(coverID))assetTypes.set(coverID,'image/unknown');
  function selected(){if(!model.value)return null;const [p,m]=JSON.parse(model.value);return adminCatalog.find(x=>x.id===p)?.models.find(x=>x.id===m);}
  function countVisibility(){const m=selected(),marker=ReferencePrompts.markers(prompt.value).length>0;countFields.hidden=!m||Boolean(marker);for(const kind of ['image','video','audio'])counts[kind].disabled=Boolean(marker)||!m?.['supports_'+kind+'_input'];}
  function modes(value=''){const choices=templateModes(selected());mode.replaceChildren(new Option('Choose References',''),...choices.map(x=>new Option(x.label,x.id)));mode.value=choices.some(x=>x.id===value)?value:'';ControlPickers.sync(mode);countVisibility();}
  modes(draft.options?.reference_mode||'');renderGenerationOptions();if(draft.options?.reference_mode!=='references'||!model.value)form.append(el('p','Saved workflow: '+(draft.options?.reference_mode||'unspecified')+'. This recipe is retained. Choose a model with References mode, then infer and review its slots before publishing.','field-hint'));model.onchange=()=>{modelOptions={};modes('references');renderGenerationOptions();cache();say('Model changed. Generation defaults reset; infer and review slots again before publishing.');checkMedia();};mode.onchange=()=>{modelOptions={};countVisibility();renderGenerationOptions();cache();say('Workflow changed. Generation defaults reset; infer and review slots again before publishing.');checkMedia();};
  function read(){const [p,m]=model.value?JSON.parse(model.value):[draft.provider,draft.model],reference_counts={};if(!countFields.hidden)for(const kind of ['image','video','audio'])if(selected()?.['supports_'+kind+'_input']&&counts[kind].value!=='')reference_counts[kind]=Number(counts[kind].value);return {...draft,title:title.value,description:description.value,prompt:prompt.value,provider:p,model:m,media_type:selected()?.media_type||draft.media_type,syntax,reference_counts,slots:bindings,options:{...generationOptions(),reference_mode:mode.value||modelOptions.reference_mode||draft.options?.reference_mode||''},cover_id:coverID,override_sound_id:overrideSoundID};}
  function snapshotRecord(){const content=read();return {...record,content,draft:content,savedDraft};}
  function workflowReady(){return !!model.value&&mode.value==='references'&&Object.values(generationFields).every(input=>input.validity.valid);}
  function cache(){FormUI.updatePrompt(prompt,selected());sound.root.hidden=selected()?.media_type!=='video'&&!overrideSoundID;sound.sync();countVisibility();infer.disabled=writing||!!uploading||!workflowReady();syncPublication();localDrafts.set(record.id,snapshotRecord());promptEditor?.refresh();for(const widget of widgets)widget.sync();FormUI.update(form);}
  const validation=()=>{const d=read();return ReferenceValidation.target(d.provider,d.model,d.options);};
  async function checkDraftMedia(d,operationSignal=signal){if(!d.provider||!d.model)return;await api(`/v1/system/templates/${record.id}/validate-media`,{method:'POST',body:JSON.stringify({draft:d}),signal:operationSignal});}
  function checkMedia(){
   const d=read(),stamp=JSON.stringify([d.provider,d.model,d.options,d.slots]);if(stamp===mediaStamp)return;
   mediaStamp=stamp;const sequence=++mediaSequence;mediaIssue='';
   if(!d.provider||!d.model){mediaChecking=false;return;}
   mediaChecking=true;syncPublication();FormUI.update(form);
   checkDraftMedia(d).then(()=>{if(fresh(token)&&sequence===mediaSequence){mediaChecking=false;mediaIssue='';syncPublication();if(status.dataset.messageType==='error')say('Fixed references checked.');FormUI.update(form);}}).catch(e=>{if(fresh(token)&&sequence===mediaSequence){mediaChecking=false;mediaIssue=e.message;say(e.message,'error');syncPublication();FormUI.update(form);}});
  }
  function intent(){return {signature:ReferenceValidation.signature(validation()),sequence:mediaSequence};}
  async function acceptAsset(slot,a,name,file,started,operationSignal){
   operationSignal.throwIfAborted();
   const current=()=>fresh(token)&&started.sequence===mediaSequence&&started.signature===ReferenceValidation.signature(validation());
   if(!current())throw Error('Model or workflow changed. Choose the reference again.');
   const candidate=read();if(slot){candidate.slots=structuredClone(candidate.slots);candidate.slots.find(x=>x.id===slot.id).asset_id=a.id;}else candidate.cover_id=a.id;
   await checkDraftMedia(candidate,operationSignal);operationSignal.throwIfAborted();
   if(!current())throw Error('Model or workflow changed. Choose the reference again.');
   bindAsset(slot,a,name,file);checkMedia();
  }
  window.addEventListener('gateway-reference-deleted',event=>{
   if(event.detail.scope!=='template'||!fresh(token))return;const id=event.detail.id;
   const preview=assetPreviews.get(id);if(preview?.url)URL.revokeObjectURL(preview.url);assetPreviews.delete(id);assetTypes.delete(id);assetNames.delete(id);
   for(const slot of bindings)if(slot.asset_id===id)slot.asset_id='';if(coverID===id)coverID='';if(overrideSoundID===id)sound.clear();
   cache();renderSlots();checkMedia();if(coverUpload)showPreview(coverUpload,coverID,'image','Cover');
  },{signal});
  function inputLimits(kind){return selected()?.reference_media_limits?.[kind]||{};}
  function rememberAsset(a,name,file){
   assetTypes.set(a.id,a.content_type);assetNames.set(a.id,name||a.id);
   if(file&&/^(image|video|audio)\//.test(a.content_type)){const old=assetPreviews.get(a.id);if(old?.url)URL.revokeObjectURL(old.url);assetPreviews.set(a.id,{url:URL.createObjectURL(file),type:a.content_type,name});}
  }
  function loadMetadata(id){
   if(assetNames.has(id))return Promise.resolve({name:assetNames.get(id),content_type:assetTypes.get(id)});
   if(!metadataLoads.has(id))metadataLoads.set(id,api(`/v1/system/templates/${encodeURIComponent(record.id)}/assets/${encodeURIComponent(id)}/metadata`,{signal}).then(a=>{if(!fresh(token))return null;assetTypes.set(id,a.content_type);assetNames.set(id,a.name||'');return a;}).finally(()=>metadataLoads.delete(id)));return metadataLoads.get(id);
  }
  async function loadImage(id){
   if(assetPreviews.has(id))return assetPreviews.get(id);
   if(!imageLoads.has(id))imageLoads.set(id,(async()=>{const path=`/v1/system/templates/${encodeURIComponent(record.id)}/assets/${encodeURIComponent(id)}?preview=1`;await loadMetadata(id);if(!fresh(token))return null;const blob=await api(path,{responseType:'blob',signal});if(!fresh(token))return null;if(!/^(image|video|audio)\//.test(blob.type))throw Error('Unsupported preview media.');const item={url:URL.createObjectURL(blob),type:blob.type,name:assetNames.get(id)};assetPreviews.set(id,item);return item;})().finally(()=>imageLoads.delete(id)));
   return imageLoads.get(id);
  }
  async function showPreview(widget,id,kind,name){
   const changed=widget.previewAssetID!==id;widget.previewAssetID=id;widget.previewFallback={kind,name};widget.setPreview(id?{...assetPreviews.get(id),type:assetTypes.get(id)||kind+'/unknown',name:assetNames.get(id)||name}:null);
   if(!widget.previewStatus){widget.previewStatus=el('p','','template-preview-status');widget.previewStatus.setAttribute('role','status');widget.root.append(widget.previewStatus);}if(changed||!id||assetPreviews.has(id))FormUI.message(widget.previewStatus,'');
   if(!id||assetPreviews.has(id))return;
   const current=()=>fresh(token)&&widget.root.isConnected&&widget.previewAssetID===id;
   try{const item=await loadImage(id);if(item&&current()){widget.setPreview({...item,name:item.name||name});FormUI.message(widget.previewStatus,'');}}
   catch(error){if(current()&&error.name!=='AbortError'){FormUI.message(widget.previewStatus,'Preview unavailable. Retrying automatically. The saved reference is still assigned.','error');}}
  }
  async function copyFile(file,slot,operationSignal){const body=new FormData();body.append('file',file);if(slot)body.append('validation',JSON.stringify(validation()));return api(`/v1/system/templates/${record.id}/assets`,{method:'POST',body,signal:operationSignal});}
  function bindAsset(slot,a,name,file){
   if(slot&&!a.content_type?.startsWith(slot.type+'/')||!slot&&!/^(image|video)\//.test(a.content_type||''))throw Error('The uploaded media does not match this reference type.');
   rememberAsset(a,name,file);if(slot)slot.asset_id=a.id;else coverID=a.id;cache();
   renderSlots();
   if(coverUpload)showPreview(coverUpload,coverID,a.content_type.startsWith('image/')?'image':'video','Cover');
   if(slot)slots.querySelector(`[data-slot-id="${CSS.escape(slot.id)}"] .reference-status`).textContent='Assigned '+name+' to '+slot.label+'.';
  }
  function uploadControl(slot){
   const kind=slot?.type,label=slot?.label||'Cover',widget=ReferenceUpload.create({label,title:slot?'Drop '+(kind==='audio'?'an audio file':'an '+kind)+' here':'Drop an image or video cover here',uploadLabel:slot?'Upload '+kind:'Upload cover',kinds:slot?[kind]:['image','video'],limits:slot?inputLimits:()=>({}),signal,locked:()=>writing||uploading>0,
    onRemove:()=>{if(slot)slot.asset_id='';else coverID='';cache();renderSlots();checkMedia();if(!slot)showPreview(coverUpload,'','image','Cover');else slots.querySelector(`[data-slot-id="${CSS.escape(slot.id)}"] :is([data-upload-remove],[data-upload-picker]):not([hidden])`)?.focus({preventScroll:true});},
    onBusy:(busy,{restoreFocus=false}={})=>{if(!fresh(token))return;uploading+=busy?1:-1;lock(uploading>0);if(!busy&&restoreFocus&&slot)slots.querySelector(`[data-slot-id="${CSS.escape(slot.id)}"] :is([data-upload-remove],[data-upload-picker]):not([hidden])`)?.focus({preventScroll:true});},
    onFile:async(file,operationSignal)=>{const started=intent(),a=await copyFile(file,slot,operationSignal);await acceptAsset(slot,a,file.name,file,started,operationSignal);return slot?'Assigned '+file.name+' to '+slot.label+'.':'Cover ready: '+file.name;},
    ...({onLibrary:picker=>chooseLibrary(kind,true,async(item,selectionSignal)=>{const started=intent(),a=await api(`/v1/system/templates/${record.id}/assets`,{method:'POST',body:JSON.stringify({library_reference_id:item.id,...(slot?{validation:validation()}:{})}),signal:selectionSignal});await acceptAsset(slot,a,item.name,null,started,selectionSignal);},slot?validation:null,()=>slot?slot.asset_id:coverID,()=>{if(slot)slot.asset_id='';else coverID='';cache();renderSlots();checkMedia();if(!slot)showPreview(coverUpload,'','image','Cover');},picker.root)})
   });widgets.add(widget);showPreview(widget,slot?.asset_id||(!slot&&coverID),kind||'image',slot?'Fixed '+label:'Cover');return widget;
  }
  function renderSlots(){
   for(const widget of widgets)if(widget!==coverUpload){widget.dispose();widgets.delete(widget);}
   slots.replaceChildren();if(!bindings.length)slots.append(el('p','No reference slots. Infer from the selected model and prompt.'));
   for(const slot of bindings){const row=el('div','','template-slot');row.dataset.slotId=slot.id;const heading=el('div','','template-slot-heading');heading.append(el('h3',slot.label),el('span',slot.type+' · '+(slot.asset_id?'Fixed reference':'Supplied by user'),'field-hint'));row.append(heading);
    const label=field(row,'Input label','text',slot.label);label.oninput=()=>{slot.label=label.value;heading.querySelector('h3').textContent=slot.label;widget.setLabel(slot.label);cache();};
    row.append(el('p',slot.asset_id?'Fixed reference. Users cannot replace it.':'Required reference. The user supplies this input when generating.','field-hint'));
    const widget=uploadControl(slot);row.append(widget.root);
    slots.append(row);
   }
   FormUI.refresh();promptEditor?.refresh();
  }
  const infer=button('Infer and review slots',async()=>{if(writing||uploading)return;lock(true);try{const d=read(),result=await api(`/v1/system/templates/${record.id}/infer`,{method:'POST',body:JSON.stringify(d),signal:controller.signal});if(!fresh(token))return;bindings=result.slots.map(s=>{const previous=bindings.find(x=>x.id===s.id&&x.role===s.role);return {...s,...(previous?{asset_id:previous.asset_id,label:previous.label}:{})};});syntax=result.syntax;cache();renderSlots();checkMedia();say('Review each slot, fixed references and the cover before publishing.');}catch(e){fail(e,token)}finally{if(fresh(token))lock(false);}});
  form.append(infer,slots);
  coverUpload=uploadControl(null);const coverSection=el('section','','template-cover');coverSection.append(el('h3','Gallery cover'),el('p','Choose an image or video. Replacing a fixed reference does not change the cover.','field-hint'),coverUpload.root);form.append(coverSection);
  form.append(el('p','Save updates this template. Changes to a published template are immediately available to users. Saving an unpublished template keeps it unpublished.','field-hint'));
  form.append(el('p','Publishing shares this prompt, fixed references and cover with authorized users of this instance. Separate covers are never generation inputs.','field-hint'));
  const actions=el('div','','form-heading-actions');heading.append(actions);
  function lock(value,editingDuringSave=false){writing=value;sound.sync();for(const input of form.querySelectorAll('input,textarea,select,button:not([data-upload-cancel])'))input.disabled=value&&!(editingDuringSave&&(input===title||input===description||input===prompt));for(const widget of widgets)widget.sync();if(!value){if(generationFields.generate_audio)generationFields.generate_audio.disabled=!!selected()?.audio_always_on&&generationFields.generate_audio.checked;countVisibility();infer.disabled=writing||!!uploading||!workflowReady();syncPublication();}FormUI.refresh();if(!value)FormUI.update(form);}
  async function save(action=''){if(action&&draftStamp(read())!==baseline||!action&&draftStamp(read())===baseline)return;if(writing||uploading||action!=='unpublish'&&(!workflowReady()||mediaChecking))return;if(action!=='unpublish'&&mediaIssue){say(mediaIssue,'error');return;}cache();const submitted=draftStamp(read()),data=action?{version:record.version,action}:{version:record.version,content:structuredClone(read())};lock(true,!action);say(action==='publish'?'Publishing…':'Saving…');
   try{const saved=currentRecord(await api('/v1/system/templates/'+record.id,{method:'PUT',body:JSON.stringify(data),signal:controller.signal}));if(!fresh(token))return;const changed=draftStamp(read())!==submitted;record={...saved};editing=record;savedDraft=structuredClone(saved.draft);baseline=draftStamp(saved.draft);if(changed){localDrafts.set(record.id,snapshotRecord());FormUI.commit(form,baseline);syncPublication();}else{localDrafts.delete(record.id);renderEditor(saved);}say(action==='publish'?'Published.':action==='unpublish'?'Unpublished. Existing jobs are unchanged.':'Saved. '+(saved.is_published?'Changes are available to users.':'This template is unpublished.'));}
   catch(e){
    if(e.status===409&&fresh(token)){
     try{const latest=await api('/v1/system/templates',{signal:controller.signal});if(!fresh(token))return;const current=latest.data?.map(currentRecord).find(item=>item.id===record.id);if(current){record.version=current.version;record.published=current.published;record.is_published=current.is_published;record.revision=current.revision;savedDraft=structuredClone(current.draft);baseline=draftStamp(savedDraft);FormUI.commit(form,baseline);localDrafts.set(record.id,snapshotRecord());form.querySelector('[data-conflict-review]')?.remove();const review=el('details');review.dataset.conflictReview='';review.open=true;review.append(el('summary','Current template — review before saving your edits again'));const fields=el('dl');for(const [name,value] of Object.entries(current.draft)){fields.append(el('dt',name.replaceAll('_',' ')),el('dd',typeof value==='object'?JSON.stringify(value,null,2):String(value??'')));}review.append(fields);form.append(review);say('Template changed. Your edits are retained. Review the current content before saving again.','error');}else say('This template is no longer available. Your edits are retained.','error');}catch(error){fail(error,token);}
    }else fail(e,token);
   }finally{if(fresh(token))lock(false);writing=false;}
  }
  const saveDraft=button('Save',()=>save(),'primary compact'),publicationLabel=el('label','Published','checkbox'),publish=el('input'),publicationHint=el('p','','field-hint');publish.type='checkbox';publish.setAttribute('aria-label','Published');publicationLabel.append(publish);actions.append(saveDraft,publicationLabel);form.append(publicationHint);
  function syncPublication(){const dirty=draftStamp(read())!==baseline;publish.checked=!!record.published;publish.disabled=writing||!!uploading||dirty||(!record.published&&(!coverID||!workflowReady()||mediaChecking||!!mediaIssue));publicationHint.textContent=dirty?'Save your changes before changing publication.':!coverID?'Choose a gallery cover before publishing.':'';saveDraft.disabled=writing||!!uploading||!workflowReady()||mediaChecking||!!mediaIssue;}
  publish.onchange=()=>{const action=publish.checked?'publish':'unpublish';publish.checked=!!record.published;save(action);};infer.disabled=writing||!!uploading||!workflowReady();
  form.onsubmit=e=>{e.preventDefault();save();};form.oninput=event=>{if(event.target!==publish)cache();};renderSlots();FormUI.watch(form,saveDraft,{read:()=>draftStamp(read()),heading:titleHeading});if(!record.savedDraft)savedDraft=structuredClone(read());baseline=draftStamp(savedDraft);FormUI.commit(form,baseline);syncPublication();FormUI.refresh();checkMedia();
  editorUpdate=()=>Promise.all([...widgets].filter(widget=>widget.root.isConnected&&widget.previewAssetID&&!assetPreviews.has(widget.previewAssetID)).map(widget=>showPreview(widget,widget.previewAssetID,widget.previewFallback.kind,widget.previewFallback.name)));
  function promptOptions(){const options={reference_mode:mode.value,image_urls:[],video_urls:[],audio_urls:[]};for(const slot of bindings)options[slot.type+'_urls'][slot.index-1]='https://template.invalid/'+slot.id;return options;}
  promptEditor=PromptReferenceEditor.attach(prompt,{model:selected,options:promptOptions,signal,validate:false,allowBlob:true,reference:(kind,index)=>{const slot=bindings.find(s=>s.type===kind&&s.index===index+1);return slot?{name:slot.label+' · '+(slot.asset_id?'Fixed reference':'Supplied by user'),url:assetPreviews.get(slot.asset_id)?.url}:null;},message:()=>{const missing=ReferencePrompts.inspect(prompt.value,selected(),promptOptions()).filter(item=>item.reason==='missing');return missing.length?'Infer and review slots to bind '+[...new Set(missing.map(item=>item.label))].join(', ')+'.':ReferencePrompts.error(prompt.value,selected(),promptOptions());}});

 }
 window.addEventListener('gateway-key-change',()=>{runnerDrafts.clear();next();editing=null;browseID='';uploading=0;stopMedia(selection);emptySelection();if(active)gallery();});
 window.GatewayTemplates={section,open,currentChanged};
})();
