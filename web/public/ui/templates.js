(() => {
 'use strict';
 const content=document.querySelector('#templatesContent'),status=document.querySelector('#templatesStatus'),browser=document.querySelector('#templatesBrowser'),galleryHost=document.querySelector('#templatesGallery'),selection=document.querySelector('#templateSelection');
 let requestedEdit=new URLSearchParams(location.search).get('edit');
 let canEdit=false,authorRecords=[],hovered=null;
 const stopHover=()=>{const previous=hovered;hovered=null;previous?.player.pause();};
 // WebKit may omit leave events when a pointer exits native video controls.
 document.addEventListener('pointermove',event=>{if(event.pointerType==='mouse'&&hovered&&!hovered.card.contains(event.target))stopHover();},true);
 window.addEventListener('blur',stopHover);
 const reducedMotion=matchMedia('(prefers-reduced-motion: reduce)');reducedMotion.addEventListener('change',()=>{if(reducedMotion.matches)stopHover();});
 let active=false,epoch=0,controller=new AbortController(),writing=false,editing=null,adminCatalog=[],browseID='',uploading=0,createID='';
 const localDrafts=new Map(),failedMedia=new Set();
 let listFingerprint='';
 let view='gallery',useUpdate=null,editorUpdate=null;
 const updates=GatewayAutoUpdate.watch({root:document.querySelector('#templatesView'),active:()=>active,load:update});
 const el=(tag,text='',cls='')=>{const n=document.createElement(tag);n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,fn,cls='secondary')=>{const b=el('button',text,cls);b.type='button';b.onclick=fn;return b;};
 const assetAction=(action,name,fn)=>{const b=button(action,fn);b.setAttribute('aria-label',action+' '+name);return b;};
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
   asset.ontouchend=event=>{const intent=touchIntent;touchIntent=null;const t=[...event.changedTouches].find(t=>t.identifier===intent?.id);if(!intent||event.touches.length||!t||Math.abs(t.clientX-intent.x)>10||Math.abs(t.clientY-intent.y)>10)return;event.preventDefault();toggle(event.currentTarget);};
   asset.onpointerup=event=>{if(event.pointerType==='pen')toggle(event.currentTarget);};
   card.onpointerenter=e=>{if(e.pointerType!=='mouse'||!matchMedia('(hover: hover)').matches||reducedMotion.matches)return;const current=e.currentTarget,player=current.querySelector('video');if(!player?.paused)return;stopHover();hovered={card:current,player};play(player).then(()=>{if(player.paused&&hovered?.player===player)hovered=null;});};card.onpointerleave=e=>{if(e.pointerType==='mouse'&&hovered?.card===e.currentTarget)stopHover();};card.onfocusout=e=>{if(!e.currentTarget.contains(e.relatedTarget))e.currentTarget.querySelector('video')?.pause();};
  }
  return card;
 }
 function section(name,allowed){
  active=name==='templates'&&allowed;
  if(!active){next();stopMedia(selection);stopMedia(content);stopMedia(galleryHost);browseID='';}
  const panel=document.querySelector('#connectionPanel');
  if(active){document.querySelector('#templateKeyHost').append(panel);document.querySelector('#priceCard').hidden=true;}
  else{document.querySelector('#taskForm').insertBefore(panel,document.querySelector('#submitButton'));}
 }
 async function open(){
  if(editing&&systemAdministrator&&localDrafts.has(editing.id)){renderEditor(localDrafts.get(editing.id));return;}
  return gallery();
 }
 function showBrowser(){browser.hidden=false;content.hidden=true;stopMedia(content);content.replaceChildren();}
 function emptySelection(){stopMedia(selection);selection.replaceChildren(el('p',GenerationKey.value()?'Select a template from the gallery.':'Enter a Gateway API key to select and use a template.'));useUpdate=null;}
 async function gallery(){
  const id=browseID;next();view=id?'use':'gallery';editing=null;showBrowser();emptySelection();document.querySelector('#priceCard').hidden=true;
  return updates.refresh({restart:true,quiet:false});
 }
 function selectedCard(){for(const card of galleryHost.querySelectorAll('[data-template-id]')){const selected=card.dataset.templateId===browseID;card.classList.toggle('selected',selected);card.querySelector('[data-use-template]')?.setAttribute('aria-pressed',String(selected));}}
 function renderGallery(result){
  const nextGrid=el('div');
  if(canEdit){const card=el('article','','card template-create-card');card.dataset.updateKey='create';const action=button('Create template',create,'primary compact');action.disabled=!adminCatalog.length;card.append(action,button('Template references',()=>chooseLibrary('',true,null)));if(!adminCatalog.length)card.append(el('p','No models with a References workflow are available for templates.'));nextGrid.append(card);}
  const seen=new Set();
  for(const item of result.data||[]){seen.add(item.id);const card=el('article','','card template-card');card.dataset.updateKey=item.id;card.dataset.templateId=item.id;card.append(media(item.cover_url,item.cover_type,item.title,{hover:true}));const select=button(item.title,()=>use(item.id),'template-card-title');select.dataset.useTemplate='';select.setAttribute('aria-pressed',String(browseID===item.id));card.append(select);if(item.description)card.append(el('p',item.description));if(canEdit){const record=authorRecords.find(r=>r.id===item.id);if(record){const edit=button('Edit',()=>renderEditor(localDrafts.get(record.id)||record));edit.setAttribute('aria-label','Edit '+(record.draft.title||item.title));card.append(edit);}}nextGrid.append(card);}
  if(canEdit)for(const record of authorRecords){if(seen.has(record.id))continue;const card=el('article','','card template-card template-draft-card');card.dataset.updateKey=record.id;card.append(el('h2',record.draft.title||'Untitled draft'),el('p',record.published?'Published template unavailable to this key':'Draft · not published'));const edit=button('Edit',()=>renderEditor(localDrafts.get(record.id)||record));edit.setAttribute('aria-label','Edit '+(record.draft.title||'Untitled draft'));card.append(edit);nextGrid.append(card);}
  if(!nextGrid.childElementCount)nextGrid.append(el('p','No templates have been published yet.'));
  for(const old of galleryHost.children){const incoming=[...nextGrid.children].find(n=>n.dataset.updateKey===old.dataset.updateKey);if(!incoming||old.querySelector('.template-media')?.dataset.updateKey!==incoming.querySelector('.template-media')?.dataset.updateKey)stopMedia(old);}
  GatewayAutoUpdate.reconcile(galleryHost,nextGrid);selectedCard();
 }
 async function update({signal,current,quiet}){
  if(writing||uploading)return;
  const token=epoch,screen=view,id=browseID;const valid=()=>current()&&fresh(token)&&view===screen;
  if(!quiet)say('Loading templates…');
  try{
   let admin=null,allowed=false;
   if(systemAdministrator){try{admin=await api('/v1/system/templates',{signal});if(!valid())return;allowed=true;}catch(e){if(e.status!==403)throw e;}}
   if(!valid())return;
   canEdit=allowed;authorRecords=canEdit?(admin.data||[]):[];adminCatalog=canEdit?(admin.catalog||[]).map(p=>({...p,models:p.models.filter(eligible)})).filter(p=>p.models.length):[];
   if(requestedEdit&&screen!=='edit'){const id=requestedEdit;requestedEdit=null;const record=canEdit&&authorRecords.find(item=>item.id===id);if(record){renderEditor(localDrafts.get(record.id)||record);return;}say('This template is unavailable for editing.','error');}
   if(screen==='edit'){if(!canEdit){editing=null;await gallery();say('Template editing permission is no longer available.','error');}else await editorUpdate?.();return;}
   const result=GenerationKey.value()?await api('/v1/templates',{signal}):{data:[]};if(!valid())return;
   const signature=JSON.stringify([canEdit,result,authorRecords,adminCatalog]);if(signature!==listFingerprint){listFingerprint=signature;renderGallery(result);}
   if(!GenerationKey.value()){emptySelection();say('Enter the same Gateway API key used in Playground to browse and generate.');return;}
   if(screen==='use'&&id){try{const detail=await api('/v1/templates/'+encodeURIComponent(id),{signal});if(valid()){if(useUpdate)useUpdate(detail);else{renderUse(detail,token,id);}}}catch(e){if(valid()&&e.name!=='AbortError'){if([403,404,410].includes(e.status))useUpdate?.(null);say(e.message,'error');}}}
   else say('');
  }catch(error){if(valid()&&error.name!=='AbortError')say(error.message,'error');}
 }
 async function chooseLibrary(kind,administrator,onSelect,validationContext=null){
 let shared=administrator;
  const token=epoch,parentSignal=controller.signal,operation=new AbortController(),signal=operation.signal,dialog=el('dialog','','reference-library template-reference-library'),grid=el('div','','reference-library-grid'),message=el('p','','reference-status'),previousFocus=document.activeElement,urls=new Set();let busy=false,uploadPending=false,offset=0,librarySequence=0;
  const close=()=>{stopMedia(dialog);operation.abort();for(const url of urls)URL.revokeObjectURL(url);urls.clear();dialog.close();dialog.remove();if(previousFocus?.isConnected)previousFocus.focus();};
  FormUI.dialogHeading(dialog,administrator?'Template references':'Reference library',close);dialog.append(el('p',administrator?'Shared with everyone on this instance. Administrators manage these files. Upload files here, then choose a file to assign it to a fixed slot. Uploading alone does not assign it.':'Choose from your own reference library.','field-hint'));
  if(!administrator){const source=field(dialog,'Library','select');source.append(new Option('My references','private'),new Option('Template references','shared'));source.onchange=()=>{shared=source.value==='shared';load();};}
  let uploader;
  if(administrator){uploader=ReferenceUpload.create({label:'Template reference library',title:'Drop a reference into the shared template library',uploadLabel:'Upload reference',kinds:['image','video','audio'],signal,locked:()=>busy,onBusy:value=>{uploadPending=value;for(const b of grid.querySelectorAll('button'))b.disabled=busy||value;more.disabled=busy||value;},onFile:async(file,uploadSignal)=>{const body=new FormData();body.append('file',file);const context=validationContext?.();if(context)body.append('validation',JSON.stringify(context));const item=await api('/v1/system/template-references',{method:'POST',body,signal:uploadSignal});uploadSignal.throwIfAborted();await load();return item.name+' uploaded to Template references. No slot assigned.';}});dialog.append(uploader.root);}
  dialog.append(message,grid);message.setAttribute('role','status');
  const more=button('Load more',()=>load(true));dialog.append(more);document.body.append(dialog);
  dialog.addEventListener('cancel',e=>{e.preventDefault();close();});parentSignal.addEventListener('abort',close,{once:true});signal.addEventListener('abort',()=>parentSignal.removeEventListener('abort',close),{once:true});dialog.showModal();
  async function load(append=false){if(signal.aborted)return;const sequence=++librarySequence;busy=true;more.disabled=true;message.textContent='Loading library…';
   try{const result=await api((shared?'/v1/system/template-references':'/v1/references')+'?offset='+(append?offset:0),{signal});if(sequence!==librarySequence||!fresh(token)||!dialog.open||signal.aborted)return;if(!append){stopMedia(grid);grid.replaceChildren();for(const url of urls)URL.revokeObjectURL(url);urls.clear();}
    for(const item of result.data||[]){if(kind&&!item.content_type.startsWith(kind+'/'))continue;const card=el('div','','reference-card'),name=el('span',item.name,'reference-name');card.dataset.libraryReference=item.id;
     const preview=el('div','','template-library-preview');card.append(preview);
     const show=url=>{const kind=item.content_type.split('/')[0],asset=el(kind==='image'?'img':kind);if(!['image','video','audio'].includes(kind))return;asset.setAttribute('aria-label',item.name);if(kind==='image')asset.alt=item.name;else{asset.controls=true;asset.preload='metadata';asset.playsInline=true;}asset.onerror=()=>{if(card.isConnected&&!signal.aborted)preview.replaceChildren(el('p','Preview unavailable.'),button('Retry preview',loadPreview));};asset.src=url;preview.replaceChildren(asset);};
     const loadPreview=async()=>{preview.replaceChildren(el('p','Loading preview…'));try{show(item.url);}catch{if(card.isConnected)preview.replaceChildren(button('Retry preview',loadPreview));}};
     if(item.content_type.startsWith('image/'))loadPreview();else preview.append(button('Preview '+item.name,loadPreview));
     card.append(name,el('small',item.content_type+' · '+new Intl.NumberFormat().format(item.size_bytes)+' bytes'));
     if(item.deleting)card.append(el('p','Deletion pending. An administrator can retry deletion.'));
     if(onSelect&&!item.deleting)card.append(button('Select '+item.name,async()=>{if(busy||uploadPending||signal.aborted)return;busy=true;for(const b of grid.querySelectorAll('button'))b.disabled=true;message.textContent=administrator?'Assigning a fixed template reference…':'Selecting reference…';try{await onSelect(item,signal);if(fresh(token)&&!signal.aborted)close();}catch(e){if(fresh(token)&&!signal.aborted){message.textContent=e.message;for(const b of grid.querySelectorAll('button'))b.disabled=false;}}finally{busy=false;uploader?.sync();}},'reference-choice'));
     if(administrator&&!item.deleting){card.append(assetAction('Rename',item.name,()=>{const rename=el('dialog'),form=el('form'),status=el('p');FormUI.dialogHeading(rename,'Rename shared file',()=>rename.close());const name=field(form,'Filename','text',item.name);name.required=true;name.maxLength=255;const save=button('Save',async()=>{if(!form.reportValidity())return;save.disabled=true;try{await api('/v1/system/template-references/'+encodeURIComponent(item.id),{method:'PATCH',body:JSON.stringify({name:name.value}),signal});rename.close();await load();}catch(error){status.textContent=error.message;}finally{save.disabled=false;}});form.onsubmit=event=>{event.preventDefault();if(!save.disabled)save.click();};const abortRename=()=>rename.close();signal.addEventListener('abort',abortRename,{once:true});rename.addEventListener('close',()=>signal.removeEventListener('abort',abortRename),{once:true});form.append(save,status);rename.append(form);document.body.append(rename);rename.addEventListener('close',()=>rename.remove(),{once:true});rename.showModal();name.focus();}));}
     if(administrator&&!onSelect)card.append(assetAction('Delete',item.name,async()=>{if(busy||uploadPending||signal.aborted)return;busy=true;more.disabled=true;try{await api('/v1/system/template-references/'+encodeURIComponent(item.id),{method:'DELETE',signal});await load();message.textContent='Deleted '+item.name+'.';}catch(e){if(!signal.aborted){message.textContent=e.message;for(const use of e.templates||[]){const link=el('a',use.name+' · '+use.id);link.href=use.url;link.onclick=async event=>{event.preventDefault();try{const record=await api('/v1/system/templates/'+encodeURIComponent(use.id),{signal});close();renderEditor(localDrafts.get(record.id)||record);}catch(error){message.textContent=error.message;}};message.append(document.createElement('br'),link);}}}finally{busy=false;more.disabled=uploadPending;for(const b of grid.querySelectorAll('button'))b.disabled=uploadPending;uploader?.sync();}}));
     grid.append(card);
    }
    offset=result.next_offset||0;more.hidden=!result.has_more;message.textContent=grid.childElementCount?'':kind?'No matching files. Upload a reference or load more.':'No template references yet. Upload a file to the shared library.';
   }catch(e){if(sequence===librarySequence&&fresh(token)&&!signal.aborted)message.textContent=e.message;}finally{if(sequence!==librarySequence)return;busy=false;more.disabled=uploadPending;for(const b of grid.querySelectorAll('button'))b.disabled=uploadPending;uploader?.sync();}
  }
  await load();
 }
 async function uploadReference(file,signal,validation,onProgress){
  await GenerationKey.ensureSession(true);await window.StorageQuota?.check();
  return ReferenceValidation.upload(file,{signal,validation,onProgress,expectedKey:GenerationKey.value()});
 }
 async function use(id){
  next();view='use';editing=null;browseID=id;showBrowser();emptySelection();selection.replaceChildren(el('p','Loading selected template…'));selectedCard();return updates.refresh({restart:true,quiet:false});
 }
 function renderUse(view,token,id){
   const d=view.template,form=el('form','','card template-use');form.autocomplete='off';
   form.append(button('Clear selection',()=>{browseID='';gallery();}),el('h2',d.title));if(d.description)form.append(el('p',d.description));
   const details=el('details'),summary=el('summary','View prompt'),prompt=el('p',d.prompt,'template-prompt');details.append(summary,prompt);form.append(details);
   const model=field(form,'Model','select');model.required=true;
   ModelPicker.populate(model,view.catalog,{value:view.default_available?ModelPicker.key(d.provider,d.model):'',placeholder:'Choose a compatible model'});
   const inputs={};let pending=0,unavailable=false,validationIssue='',validationSequence=0,bindingQueue=Promise.resolve();
   const target=()=>{const [p,m]=model.value?JSON.parse(model.value):['',''];return ReferenceValidation.target(p,m,d.options);};
   const checkInputs=async(candidate=inputs,signal=controller.signal)=>{if(!model.value)return;const [provider,modelID]=JSON.parse(model.value);await api(`/v1/templates/${id}/validate-media`,{method:'POST',body:JSON.stringify({revision:view.revision,provider,model:modelID,inputs:candidate}),signal});};
   const ready=()=>{generate.disabled=unavailable||!!view.asset_error||!!view.workflow_error||!!validationIssue||!model.value||pending>0||d.slots.some(s=>!s.asset_id&&!inputs[s.id]);};
   for(const slot of d.slots){if(slot.asset_id){
    const fixed=(view.fixed_references||[]).find(a=>a.slot_id===slot.id&&a.asset_id===slot.asset_id),row=el('div','','template-fixed-reference');
    row.append(el('h3',slot.label+' · fixed'),el('p',fixed?.name||'Fixed '+slot.type,'field-hint'));
    if(fixed?.url)row.append(media(fixed.url,fixed.content_type,fixed.name||slot.label));else row.append(el('p','Fixed reference preview unavailable.'));
    form.append(row);continue;
   }
    let slotVersion=0,slotAbort=null,previewURL='';
    const begin=()=>{slotAbort?.abort();slotAbort=new AbortController();return {version:++slotVersion,modelSequence:validationSequence,intent:ReferenceValidation.signature(target()),signal:AbortSignal.any([controller.signal,slotAbort.signal])};};
    const current=op=>fresh(token)&&op.version===slotVersion&&op.modelSequence===validationSequence&&op.intent===ReferenceValidation.signature(target());
    const bind=(item,op,file)=>{
     const work=async()=>{op.signal.throwIfAborted();if(!current(op))throw Error('Selection or model changed. Choose the file again.');await checkInputs({...inputs,[slot.id]:item.id},op.signal);op.signal.throwIfAborted();if(!current(op))throw Error('Selection or model changed. Choose the file again.');inputs[slot.id]=item.id;validationIssue='';if(previewURL)URL.revokeObjectURL(previewURL);previewURL=file?URL.createObjectURL(file):'';widget.setPreview({url:previewURL||item.url,type:item.content_type||file?.type||slot.type+'/unknown',name:item.name||file?.name||slot.label});widget.message('Assigned '+(item.name||file?.name||slot.label)+' to '+slot.label+'.');};
     const result=bindingQueue.then(work,work);bindingQueue=result.catch(()=>{});return result;
    };
    controller.signal.addEventListener('abort',()=>{slotAbort?.abort();if(previewURL)URL.revokeObjectURL(previewURL);},{once:true});
    const row=el('div','','template-variable-reference');row.append(el('h3',slot.label+' · required '+slot.type));
    const widget=ReferenceUpload.create({label:slot.label,title:'Drop a '+slot.type+' here',uploadLabel:'Upload '+slot.type,kinds:[slot.type],signal:controller.signal,locked:()=>unavailable||!model.value,
     limits:kind=>{const [p,m]=model.value?JSON.parse(model.value):[];return view.catalog.find(x=>x.id===p)?.models.find(x=>x.id===m)?.reference_media_limits?.[kind]||{};},
     onBusy:value=>{pending+=value?1:-1;ready();},
     onFile:async(file,signal)=>{const op=begin();op.signal=AbortSignal.any([op.signal,signal]);const ref=await uploadReference(file,op.signal,target(),text=>{if(current(op))widget.message(text);});await bind(ref,op,file);return 'Assigned '+file.name+' to '+slot.label+'.';},
     onLibrary:()=>chooseLibrary(slot.type,false,async(item,signal)=>{const op=begin();op.signal=AbortSignal.any([op.signal,signal]);pending++;ready();try{await bind(item,op);}finally{pending--;if(fresh(token))ready();}})
    });row.append(widget.root);form.append(row);
   }
   const generate=el('button','Review generation','primary');generate.type='submit';form.append(generate);model.onchange=async()=>{const sequence=++validationSequence,selection=JSON.stringify(inputs);pending++;validationIssue='';ready();say('Checking reference compatibility…');try{await checkInputs();if(fresh(token)&&sequence===validationSequence&&selection===JSON.stringify(inputs))say('References checked.');}catch(e){if(fresh(token)&&sequence===validationSequence&&selection===JSON.stringify(inputs)){validationIssue=e.message;say(e.message,'error');}}finally{pending--;if(fresh(token))ready();}};
   const messages=[];if(view.workflow_error)messages.push(view.workflow_error);else{if(!view.default_available)messages.push('The configured default model is unavailable to this key. Choose an allowed alternative.');if(!view.catalog.some(p=>p.models.length))messages.push('No compatible models are available to this key.');}if(view.asset_error)messages.push(view.asset_error);say(messages.join(' '),messages.length?'error':'');
   form.onsubmit=e=>{e.preventDefault();if(generate.disabled||!form.reportValidity())return;const [provider,modelID]=JSON.parse(model.value);const source={prompt:d.prompt,provider,model:modelID,media_type:d.media_type,options:d.options,template_catalog:view.catalog,template:{id,revision:view.revision,inputs:{...inputs}}};const project=window.GatewayProjects?.selected();if(project)source.project_id=project;openGenerationConfirmation(source,generate);};
   useUpdate=latest=>{
    // Keep selected files, model, disclosure and the immutable reviewed recipe.
    unavailable=!latest||latest.revision!==view.revision||!!latest.asset_error||!!latest.workflow_error||!latest.catalog?.some(p=>p.models.some(m=>ModelPicker.key(p.id,m.id)===model.value));
    ready();say(latest?.workflow_error||(unavailable?'This template or its availability changed. Return to the gallery to review the current version.':''),unavailable?'error':'');
   };
   selection.replaceChildren(form);FormUI.refresh();ready();
 }
 async function create(){if(writing||!canEdit)return;const token=epoch;writing=true;
  try{const provider=adminCatalog[0],model=provider?.models[0];if(!model){say('Choose an available model with a References workflow.','error');return;}const id=createID||(createID=crypto.randomUUID()),record=await api('/v1/system/templates',{method:'POST',body:JSON.stringify({id,version:0,draft:{title:'',media_type:model.media_type,prompt:'',provider:provider.id,model:model.id,syntax:'',slots:[],options:{reference_mode:'references'},cover_id:''}}),signal:controller.signal});if(fresh(token)){createID='';renderEditor(record);}}catch(e){fail(e,token)}finally{writing=false;}
 }
 function renderEditor(record){
  if(!canEdit||!systemAdministrator)return;stopMedia(selection);stopMedia(galleryHost);browser.hidden=true;content.hidden=false;
  const token=next(),signal=controller.signal;view='edit';editing=record;const draft=structuredClone(record.draft),form=el('form','','card template-editor');let modelOptions=structuredClone(draft.options||{}),mediaChecking=false,mediaIssue='',mediaSequence=0,mediaStamp='';content.replaceChildren(form);say('');
  form.append(button('Back to templates',()=>{localDrafts.set(record.id,{...record,draft:read()});editing=null;gallery();}));const titleHeading=FormUI.sectionHeading(form,record.published?'Edit template · published revision '+record.revision:'Edit draft'),heading=titleHeading.parentElement;
  const title=field(form,'Title','text',draft.title);title.maxLength=120;
  const description=field(form,'Short description (optional)','textarea',draft.description);description.maxLength=500;
  const model=field(form,'Default model','select');ModelPicker.populate(model,adminCatalog,{value:ModelPicker.key(draft.provider,draft.model),placeholder:'Choose a model'});
  const mode=field(form,'Reference workflow','select');
  const prompt=field(form,'Prompt','textarea',draft.prompt);prompt.maxLength=10000;prompt.rows=5;
  const countFields=el('div');form.append(countFields);const counts={};
  for(const kind of ['image','video','audio']){const input=field(countFields,kind[0].toUpperCase()+kind.slice(1)+' input count','number',draft.reference_counts?.[kind]??(Math.max(0,...(draft.slots||[]).filter(s=>s.type===kind).map(s=>s.index))||''));input.min=0;input.max=64;input.step=1;counts[kind]=input;}
  countFields.append(el('p','For workflows without prompt markers, declare the ordered inputs. Leave blank to use the workflow minimum. The prompt is submitted unchanged.','field-hint'));
  const slots=el('div','','template-slots');let bindings=structuredClone(draft.slots||[]),syntax=ReferencePrompts.syntax,coverID=draft.cover_id||'';
  const cover=field(form,'Gallery cover','select');form.append(el('p','The cover is only the gallery preview. Replacing a fixed reference does not change a separately selected cover.','field-hint'));
  let promptEditor=null,coverUpload=null;const widgets=new Set(),assetPreviews=new Map(),imageLoads=new Map(),metadataLoads=new Map(),assetNames=new Map(),previewFailures=new Set();
  signal.addEventListener('abort',()=>{for(const item of assetPreviews.values())if(item.url)URL.revokeObjectURL(item.url);assetPreviews.clear();},{once:true});
  const assetTypes=new Map();for(const s of bindings)if(s.asset_id)assetTypes.set(s.asset_id,s.type+'/unknown');if(coverID&&!assetTypes.has(coverID))assetTypes.set(coverID,'image/unknown');
  function selected(){if(!model.value)return null;const [p,m]=JSON.parse(model.value);return adminCatalog.find(x=>x.id===p)?.models.find(x=>x.id===m);}
  function countVisibility(){const m=selected(),marker=ReferencePrompts.markers(prompt.value).length>0;countFields.hidden=!m||Boolean(marker);for(const kind of ['image','video','audio'])counts[kind].disabled=Boolean(marker)||!m?.['supports_'+kind+'_input'];}
  function modes(value=''){const choices=templateModes(selected());mode.replaceChildren(new Option('Choose References',''),...choices.map(x=>new Option(x.label,x.id)));mode.value=choices.some(x=>x.id===value)?value:'';ControlPickers.sync(mode);countVisibility();}
  modes(draft.options?.reference_mode||'');if(draft.options?.reference_mode!=='references'||!model.value)form.append(el('p','Saved workflow: '+(draft.options?.reference_mode||'unspecified')+'. This recipe is retained. Choose a model with References mode, then infer and review its slots before publishing.','field-hint'));model.onchange=()=>{modelOptions={};modes('references');cache();say('Model changed. Generation defaults reset; infer and review slots again before publishing.');checkMedia();};mode.onchange=()=>{modelOptions={};countVisibility();cache();say('Workflow changed. Generation defaults reset; infer and review slots again before publishing.');checkMedia();};
  function read(){const [p,m]=model.value?JSON.parse(model.value):[draft.provider,draft.model],reference_counts={};if(!countFields.hidden)for(const kind of ['image','video','audio'])if(selected()?.['supports_'+kind+'_input']&&counts[kind].value!=='')reference_counts[kind]=Number(counts[kind].value);return {...draft,title:title.value,description:description.value,prompt:prompt.value,provider:p,model:m,media_type:selected()?.media_type||draft.media_type,syntax,reference_counts,slots:bindings,options:{...modelOptions,reference_mode:mode.value||modelOptions.reference_mode||draft.options?.reference_mode||''},cover_id:coverID};}
  function workflowReady(){return !!model.value&&mode.value==='references';}
  function cache(){countVisibility();infer.disabled=!workflowReady();publish.disabled=!workflowReady()||mediaChecking||!!mediaIssue;localDrafts.set(record.id,{...record,draft:read()});promptEditor?.refresh();for(const widget of widgets)widget.sync();FormUI.update(form);}
  const validation=()=>{const d=read();return ReferenceValidation.target(d.provider,d.model,d.options);};
  async function checkDraftMedia(d,operationSignal=signal){if(!d.provider||!d.model)return;await api(`/v1/system/templates/${record.id}/validate-media`,{method:'POST',body:JSON.stringify({draft:d}),signal:operationSignal});}
  function checkMedia(){
   const d=read(),stamp=JSON.stringify([d.provider,d.model,d.options,d.slots]);if(stamp===mediaStamp)return;
   mediaStamp=stamp;const sequence=++mediaSequence;mediaIssue='';
   if(!d.provider||!d.model){mediaChecking=false;return;}
   mediaChecking=true;publish.disabled=true;FormUI.update(form);
   checkDraftMedia(d).then(()=>{if(fresh(token)&&sequence===mediaSequence){mediaChecking=false;mediaIssue='';publish.disabled=writing||!!uploading||!workflowReady();if(status.dataset.messageType==='error')say('Fixed references checked.');FormUI.update(form);}}).catch(e=>{if(fresh(token)&&sequence===mediaSequence){mediaChecking=false;mediaIssue=e.message;say(e.message,'error');publish.disabled=true;FormUI.update(form);}});
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
  function covers(){cover.replaceChildren(new Option('Choose a fixed image/video or separate cover',''));for(const [id,type] of assetTypes){if(type.startsWith('image/')||type.startsWith('video/')){const slot=bindings.find(s=>s.asset_id===id);cover.append(new Option((slot?'Fixed · '+slot.label:'Separate cover')+(assetNames.has(id)?assetNames.get(id)?' · '+assetNames.get(id):'':' · '+(previewFailures.has(id)?'Filename unavailable':'Loading filename…')),id));}}cover.value=coverID;ControlPickers.sync(cover);}
  covers();cover.onchange=()=>{coverID=cover.value;if(coverUpload)showPreview(coverUpload,coverID,'image','Cover');cache();};
  function inputLimits(kind){return selected()?.reference_media_limits?.[kind]||{};}
  function rememberAsset(a,name,file){
   assetTypes.set(a.id,a.content_type);assetNames.set(a.id,name||a.id);
   if(file&&/^(image|video|audio)\//.test(a.content_type)){const old=assetPreviews.get(a.id);if(old?.url)URL.revokeObjectURL(old.url);assetPreviews.set(a.id,{url:URL.createObjectURL(file),type:a.content_type,name});}
  }
  function loadMetadata(id){
   if(assetNames.has(id))return Promise.resolve({name:assetNames.get(id),content_type:assetTypes.get(id)});
   if(!metadataLoads.has(id))metadataLoads.set(id,api(`/v1/system/templates/${encodeURIComponent(record.id)}/assets/${encodeURIComponent(id)}/metadata`,{signal}).then(a=>{if(!fresh(token))return null;assetTypes.set(id,a.content_type);assetNames.set(id,a.name||'');covers();return a;}).finally(()=>metadataLoads.delete(id)));return metadataLoads.get(id);
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
   try{const item=await loadImage(id);if(item&&current()){previewFailures.delete(id);widget.setPreview({...item,name:item.name||name});FormUI.message(widget.previewStatus,'');covers();}}
   catch(error){if(current()&&error.name!=='AbortError'){previewFailures.add(id);FormUI.message(widget.previewStatus,'Preview unavailable. Retrying automatically. The saved reference is still assigned.','error');covers();}}
  }
  async function copyFile(file,slot,operationSignal){const body=new FormData();body.append('file',file);if(slot)body.append('validation',JSON.stringify(validation()));return api(`/v1/system/templates/${record.id}/assets`,{method:'POST',body,signal:operationSignal});}
  function bindAsset(slot,a,name,file){
   if(slot&&!a.content_type?.startsWith(slot.type+'/')||!slot&&!/^(image|video)\//.test(a.content_type||''))throw Error('The uploaded media does not match this reference type.');
   rememberAsset(a,name,file);if(slot)slot.asset_id=a.id;else coverID=a.id;covers();cache();
   renderSlots();
   if(coverUpload)showPreview(coverUpload,coverID,a.content_type.startsWith('image/')?'image':'video','Cover');
   if(slot)slots.querySelector(`[data-slot-id="${CSS.escape(slot.id)}"] .reference-status`).textContent='Assigned '+name+' to '+slot.label+'.';
  }
  function uploadControl(slot){
   const kind=slot?.type,label=slot?.label||'Cover',widget=ReferenceUpload.create({label,title:slot?(slot.asset_id?'Replace the fixed '+kind:'Drop '+(kind==='audio'?'an audio file':'an '+kind)+' here'):'Drop a separate cover here',uploadLabel:slot?(slot.asset_id?'Replace '+kind:'Upload '+kind):'Upload cover',kinds:slot?[kind]:['image','video'],limits:slot?inputLimits:()=>({}),signal,locked:()=>writing||uploading>0,
    onBusy:(busy,{restoreFocus=false}={})=>{if(!fresh(token))return;uploading+=busy?1:-1;lock(uploading>0);if(!busy&&restoreFocus&&slot)slots.querySelector(`[data-slot-id="${CSS.escape(slot.id)}"] [data-upload-picker]`)?.focus({preventScroll:true});},
    onFile:async(file,operationSignal)=>{const started=intent(),a=await copyFile(file,slot,operationSignal);await acceptAsset(slot,a,file.name,file,started,operationSignal);return slot?'Assigned '+file.name+' to '+slot.label+'.':'Cover ready: '+file.name;},
    ...({onLibrary:()=>chooseLibrary(kind,true,async(item,selectionSignal)=>{const started=intent(),a=await api(`/v1/system/templates/${record.id}/assets`,{method:'POST',body:JSON.stringify({library_reference_id:item.id,...(slot?{validation:validation()}:{})}),signal:selectionSignal});await acceptAsset(slot,a,item.name,null,started,selectionSignal);},slot?validation:null)})
   });widgets.add(widget);showPreview(widget,slot?.asset_id||(!slot&&coverID),kind||'image',slot?'Fixed '+label:'Cover');return widget;
  }
  function renderSlots(){
   for(const widget of widgets)if(widget!==coverUpload){widget.dispose();widgets.delete(widget);}
   slots.replaceChildren();if(!bindings.length)slots.append(el('p','No reference slots. Infer from the selected model and prompt.'));
   for(const slot of bindings){const row=el('div','','template-slot');row.dataset.slotId=slot.id;const heading=el('div','','template-slot-heading');heading.append(el('h3',slot.label),el('span',slot.type+' · '+(slot.asset_id?'Fixed reference':'Supplied by user'),'field-hint'));row.append(heading);
    const label=field(row,'Input label','text',slot.label);label.oninput=()=>{slot.label=label.value;heading.querySelector('h3').textContent=slot.label;widget.setLabel(slot.label);cache();};
    row.append(el('p',slot.asset_id?'Fixed reference. Users cannot replace it.':'Required reference. The user supplies this input when generating.','field-hint'));
    const widget=uploadControl(slot);row.append(widget.root);
    if(slot.asset_id)row.append(button('Leave for user',()=>{slot.asset_id='';covers();cache();renderSlots();checkMedia();}));slots.append(row);
   }
   FormUI.refresh();promptEditor?.refresh();
  }
  const infer=button('Infer and review slots',async()=>{lock(true);try{const d=read(),result=await api(`/v1/system/templates/${record.id}/infer`,{method:'POST',body:JSON.stringify(d),signal:controller.signal});if(!fresh(token))return;bindings=result.slots.map(s=>{const previous=bindings.find(x=>x.id===s.id&&x.role===s.role);return {...s,...(previous?{asset_id:previous.asset_id,label:previous.label}:{})};});syntax=result.syntax;cache();renderSlots();checkMedia();say('Review each slot, fixed references and the cover before publishing.');}catch(e){fail(e,token)}finally{if(fresh(token))lock(false);}});
  form.append(infer,slots);
  coverUpload=uploadControl(null);form.append(coverUpload.root);
  form.append(el('p','Saving a draft does not update the published template. Publish to make the reviewed references available to users.','field-hint'));
  form.append(el('p','Publishing shares this prompt, fixed references and cover with authorized users of this instance. Separate covers are never generation inputs.','field-hint'));
  const actions=el('div','','form-heading-actions');heading.append(actions);
  function lock(value){writing=value;for(const input of form.querySelectorAll('input,textarea,select,button:not([data-upload-cancel])'))input.disabled=value;for(const widget of widgets)widget.sync();if(!value){countVisibility();infer.disabled=!workflowReady();publish.disabled=!workflowReady()||mediaChecking||!!mediaIssue;}FormUI.refresh();if(!value)FormUI.update(form);}
  async function save(action=''){if(writing||uploading||action!=='unpublish'&&(!workflowReady()||mediaChecking))return;if(action!=='unpublish'&&mediaIssue){say(mediaIssue,'error');return;}cache();const data={version:record.version,draft:read(),action};lock(true);say(action==='publish'?'Publishing…':'Saving…');
   try{const saved=await api('/v1/system/templates/'+record.id,{method:'PUT',body:JSON.stringify(data),signal:controller.signal});if(!fresh(token))return;localDrafts.delete(record.id);renderEditor(saved);say(action==='publish'?'Published.':action==='unpublish'?'Unpublished. Existing jobs are unchanged.':'Draft saved. '+(saved.published?'Users still see published revision '+saved.revision+'. Publish to apply these changes.':'Publish when ready to make this template available.'));}
   catch(e){
    if(e.status===409&&fresh(token)){
     try{const latest=await api('/v1/system/templates',{signal:controller.signal});if(!fresh(token))return;const current=latest.data?.find(item=>item.id===record.id);if(current){record.version=current.version;record.published=current.published;record.revision=current.revision;localDrafts.set(record.id,{...record,draft:read()});form.querySelector('[data-conflict-review]')?.remove();const review=el('details');review.dataset.conflictReview='';review.open=true;review.append(el('summary','Current saved template — review before saving your draft again'));const fields=el('dl');for(const [name,value] of Object.entries(current.draft)){fields.append(el('dt',name.replaceAll('_',' ')),el('dd',typeof value==='object'?JSON.stringify(value,null,2):String(value??'')));}review.append(fields);form.append(review);say('Template changed. Your draft is retained. Current published revision: '+(current.revision||'none')+'. Review before saving again.','error');}else say('This template is no longer available. Your draft is retained.','error');}catch(error){fail(error,token);}
    }else fail(e,token);
   }finally{if(fresh(token))lock(false);writing=false;}
  }
  const saveDraft=button('Save draft',()=>save(),'primary compact'),publish=button('Publish',()=>save('publish'));actions.append(saveDraft,publish);infer.disabled=publish.disabled=!workflowReady();if(record.published)actions.append(button('Unpublish',()=>save('unpublish')));
  form.onsubmit=e=>{e.preventDefault();save();};form.oninput=cache;renderSlots();FormUI.watch(form,saveDraft,{read:()=>JSON.stringify(read()),valid:()=>!uploading&&workflowReady()&&!mediaChecking&&!mediaIssue,heading:titleHeading});FormUI.refresh();checkMedia();
  editorUpdate=()=>Promise.all([...widgets].filter(widget=>widget.root.isConnected&&widget.previewAssetID&&!assetPreviews.has(widget.previewAssetID)).map(widget=>showPreview(widget,widget.previewAssetID,widget.previewFallback.kind,widget.previewFallback.name)));
  function promptOptions(){const options={reference_mode:mode.value,image_urls:[],video_urls:[],audio_urls:[]};for(const slot of bindings)options[slot.type+'_urls'][slot.index-1]='https://template.invalid/'+slot.id;return options;}
  promptEditor=PromptReferenceEditor.attach(prompt,{model:selected,options:promptOptions,signal,validate:false,allowBlob:true,reference:(kind,index)=>{const slot=bindings.find(s=>s.type===kind&&s.index===index+1);return slot?{name:slot.label+' · '+(slot.asset_id?'Fixed reference':'Supplied by user'),url:assetPreviews.get(slot.asset_id)?.url}:null;},message:()=>{const missing=ReferencePrompts.inspect(prompt.value,selected(),promptOptions()).filter(item=>item.reason==='missing');return missing.length?'Infer and review slots to bind '+[...new Set(missing.map(item=>item.label))].join(', ')+'.':ReferencePrompts.error(prompt.value,selected(),promptOptions());}});

 }
 window.addEventListener('gateway-key-change',()=>{next();editing=null;browseID='';uploading=0;stopMedia(selection);emptySelection();if(active)gallery();});
 window.GatewayTemplates={section,open};
})();
