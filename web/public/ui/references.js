function hashReferenceFile(file,signal){
 return new Promise((resolve,reject)=>{
  signal.throwIfAborted();
  const worker=new Worker('/ui/file-hash-worker.js?v=20260915-direct-storage');
  const finish=(error,value)=>{signal.removeEventListener('abort',abort);worker.terminate();error?reject(error):resolve(value);};
  const abort=()=>finish(new DOMException('Upload cancelled','AbortError'));
  signal.addEventListener('abort',abort,{once:true});
  worker.onmessage=({data})=>finish(data.error?new Error(data.error):null,data.sha256);
  worker.onerror=()=>finish(new Error('Could not prepare the file for upload.'));
  worker.postMessage(file);
 });
}
(() => {
 const pickers = [];
 let voices={},voiceTarget=null;
 const modeField=document.querySelector('#referenceModeField'), modeSelect=document.querySelector('#referenceMode');
 let modeModel='',selectionVersion=0;
 const mode=()=>selectedModel()?.reference_modes?.find(item=>item.id===modeSelect.value);
 function configure() {
   const model=selectedModel();if(!model){if(modeModel){modeModel='';selectionVersion++;}return;}
   modeField.hidden=!model.reference_modes?.length;modeSelect.disabled=modeField.hidden;
   const modes=model.reference_modes||[], signature=JSON.stringify([selectedProvider()?.id,model]);
   if(modeModel!==signature){
     voiceTarget=null;selectionVersion++;modeModel=signature;const previous=modeSelect.value;
     modeSelect.replaceChildren(...modes.map(item=>new Option(item.label,item.id)));
     if(modes.some(item=>item.id===previous))modeSelect.value=previous;
   }
   const profile=mode();
	const ratios=profile?.ratios?.length?profile.ratios:model.ratios;
	if(ratios?.length){const ratio=document.querySelector('#ratio'),previous=ratio.value;ModelControls.ratios(ratio,model,ratios,model.ratio_may_be_inferred&&previous===''?'':ratios.includes(previous)?previous:undefined);ControlPickers.sync(ratio);}
   for(const kind of ['image','video','audio']) {
     const target=document.querySelector(kind==='image'?'#imageURLsField':`#${kind}URLField`);
     const manual=target.querySelector('.reference-manual');
     const managed=!!model.resource_input_schemas?.length;FormUI.disclosure(manual.querySelector('summary'),'Use URLs');
     let hint=manual.querySelector('[data-asset-hint]');
     if(!hint){hint=document.createElement('p');hint.className='field-hint';hint.dataset.assetHint='';manual.append(hint);}
     hint.hidden=!model.supports_asset_references||managed;
     hint.textContent='For registered BytePlus assets, paste asset:// followed by the asset ID, one per line. Use assets from the same BytePlus account and project as your provider connection.';
     target.hidden=profile?profile['max_'+(kind==='image'?'images':kind+'s')]===0:!model[`supports_${kind}_input`];
   }
   document.querySelector('#referenceModeHint').textContent=profile?.image_roles?.length ? 'Image order: '+profile.image_roles.join(' → ')+'.' : '';
 }
 modeSelect.addEventListener('change',()=>{voiceTarget=null;configure();pickers.forEach(p=>p.render());updatePrice();});
 const activeVoices=()=>{if(!mode()?.supports_reference_voice)return {};const visible=[...document.querySelector('#imageURLs').value.split('\n').slice(mode()?.voice_image_start||0),...document.querySelector('#videoURL').value.split('\n')].map(x=>x.trim()).filter(Boolean);return Object.fromEntries(Object.entries(voices).filter(([url])=>visible.includes(url)));};
 function picker(kind) {
  const image=kind==='image',plural=kind==='image'?'images':kind+'s',displayPlural=kind==='audio'?'audio files':plural;
  const maxBytes=()=>ReferenceValidation.fileLimit(selectedModel(),kind,{reference_mode:modeSelect.value}),limitText=()=>ReferenceValidation.limitLabel(selectedModel(),kind,{reference_mode:modeSelect.value});
  const accept=()=>ReferenceUpload.fileTypes(kind,selectedModel()?.reference_media_limits?.[kind]).join(',');
  if(!image){
    const container=document.querySelector(`#${kind}URLField`),input=document.querySelector(`#${kind}URL`);
    const label=container.querySelector('label'),control=input.closest('.floating-field')||input;
    label.textContent=`Or paste ${kind} URLs`;
    const heading=document.createElement('label');heading.id=`${kind}ReferencesLabel`;heading.textContent=kind==='video'?'Video references':'Audio references';
    const manual=document.createElement('details');manual.className='reference-manual';manual.innerHTML=`<summary>Use ${kind} URLs</summary>`;
    // Move the complete field, including its floating label, into manual entry.
    // The upload section's heading must stay outside that field.
    control.before(heading,manual);if(control===input)manual.append(label);manual.append(control);
    manual.insertAdjacentHTML('beforebegin',`<div id="${kind}ReferenceDropzone" class="reference-dropzone" role="group" aria-labelledby="${heading.id}"><p>Drop ${displayPlural} here</p><div class="reference-actions"><button type="button" class="secondary" id="${kind}UploadReference">Upload ${displayPlural}</button><button type="button" class="secondary" id="${kind}ChooseReferences">Choose from library</button></div><small>Up to ${limitText()} each</small></div><input id="${kind}ReferenceFiles" type="file" accept="${accept()}" multiple hidden><div id="${kind}SelectedReferences" class="selected-references"></div><p id="${kind}ReferenceStatus" class="reference-status" role="status"></p>`);
  }

  const field = document.querySelector(image?'#imageURLs':`#${kind}URL`);
  let dropzone = document.querySelector(image?'#referenceDropzone':`#${kind}ReferenceDropzone`);
  let files = document.querySelector(image?'#referenceFiles':`#${kind}ReferenceFiles`);
  let selected = document.querySelector(image?'#selectedReferences':`#${kind}SelectedReferences`);
  let status = document.querySelector(image?'#referenceStatus':`#${kind}ReferenceStatus`);
  const metadata = new Map();
  let draggedURL = null;
  const dragType = 'application/x-gateway-reference-'+kind;
  let items = [], offset = 0, hasMore = false, busy = false, loading = false, epoch = 0;
  let controller = new AbortController();
  let checkedSignature='',checking=false,validationError='',pendingSelection=null;
  const validationTarget=()=>{const model=selectedModel();const t=model?ReferenceValidation.target(selectedProvider()?.id,model.id,requestOptions(model)):null;if(t&&kind==='audio'&&voiceTarget)t.role='reference_voice';return t;};
  const selectionSignature=()=>JSON.stringify([selectionVersion,validationTarget(),urls(),field.value,kind==='audio'?activeVoices():null]);
  const candidateIDs=values=>[...new Set([...(validationTarget()?.reference_ids||[]).filter(id=>!ReferenceValidation.ids(urls()).includes(id)),...ReferenceValidation.ids(values)])];
 let remote,returnFocus=null,previousFocus=null;
  const libraryView=ReferenceLibrary.create({id:image?'referenceLibrary':`${kind}ReferenceLibrary`,description:''});
  const library=libraryView.dialog;
  libraryView.intro.insertAdjacentHTML('afterend',`<div class="reference-dropzone" id="libraryDropzone"><p>Drop ${displayPlural} to add them to your library</p><button type="button" class="secondary" data-upload>Upload ${displayPlural}</button><small>Up to ${limitText()} each</small></div>`);
  document.body.append(library);
  let sharedLibrary=false,lastRemoteSource='local',listSequence=0,listController=null;
  const sourceLabel=document.createElement('label');sourceLabel.textContent='Library';const source=document.createElement('select');source.append(new Option('My references','private'),new Option('Template references','shared'));sourceLabel.append(source);library.querySelector('p').after(sourceLabel);
  source.onchange=()=>{if(loading||busy){source.value=sharedLibrary?'shared':'private';return;}sharedLibrary=source.value==='shared';library.querySelector('[data-upload]').closest('.media-picker').hidden=sharedLibrary;load();};
  const grid = library.querySelector('[data-grid]');
  const libraryStatus = library.querySelector('[data-status]');
  const more = library.querySelector('[data-more]');
  let sparse=false,missingTarget=null,roleEntries=[];
  function rolesFor(values){const remaining=[...roleEntries];return values.map((url,index)=>{const found=remaining.findIndex(e=>e.url===url);return {url,role:found>=0?remaining.splice(found,1)[0].role:roleEntries[index]?.url===''?roleEntries[index].role:undefined};});}
  function syncRoles(values){roleEntries=rolesFor(values);}
  const readRoles=()=>Object.fromEntries(roleEntries.map((e,i)=>[kind+'_'+(i+1),ModelControls.role(selectedModel(),kind,e.role)]));
  let acceptedValues=[],lastDraft=null,urlDraftError='';
  function parseDraft(){if(field.value===lastDraft)return;lastDraft=field.value;const values=field.value.split('\n').map(s=>s.trim());const next=sparse?values:[...new Set(values.filter(Boolean))];urlDraftError='';
   for(const value of next){if(!value)continue;let valid=false;try{const u=new URL(value);valid=['http:','https:'].includes(u.protocol)&&!!u.hostname&&!u.username&&!u.password&&!/\s/.test(value)&&!/:\/\//.test(value.replace(/^https?:\/\//,''));if(u.protocol==='asset:')valid=!!selectedModel()?.supports_asset_references&&/^asset:\/\/[^\s/]+$/.test(value);}catch{}if(!valid){urlDraftError='Enter a valid URL'+(limit()===1?'.':', one per line.');return;}}
   if(next.filter(Boolean).length>limit()){urlDraftError='Use up to '+limit()+' URL'+(limit()===1?'':'s')+'.';return;}acceptedValues=next;
  }
  const urls=()=>{parseDraft();return [...acceptedValues];};
  const insert = (values,url) => {if(limit()===1)return [url];const next=[...values],hole=sparse?(missingTarget!==null&&next[missingTarget]===''?missingTarget:next.indexOf('')):-1;if(hole>=0)next[hole]=url;else next.push(url);return next;};
  const absolute = url => new URL(url, location.origin).href;
  function limit(){return mode()?.['max_'+plural] ?? (image ? selectedModel()?.max_image_inputs || Infinity : 1);}
  const pickerUI=ReferenceUpload.create({label:kind[0].toUpperCase()+kind.slice(1)+' references',kinds:[kind],multiple:()=>limit()!==1,maxCount:limit,maxBytes,limits:()=>selectedModel()?.reference_media_limits?.[kind]||{},locked:()=>busy||loading||!!pendingSelection,onFiles:upload,onLibrary:()=>{returnFocus=pickerUI.library;previousFocus=document.activeElement;library.showModal();renderLibrary();load();}});
  const oldDrop=dropzone,oldFiles=files,oldSelected=selected,oldStatus=status,heading=oldDrop.previousElementSibling;
  oldDrop.before(pickerUI.root);if(heading?.tagName==='LABEL'&&!heading.contains(field))heading.remove();
  pickerUI.drop.id=oldDrop.id;pickerUI.input.id=oldFiles.id;pickerUI.previews.id=oldSelected.id;pickerUI.status.id=oldStatus.id;pickerUI.upload.id=image?'uploadReference':kind+'UploadReference';pickerUI.library.id=image?'chooseReferences':kind+'ChooseReferences';
  oldDrop.remove();oldFiles.remove();oldSelected.remove();oldStatus.remove();dropzone=pickerUI.drop;files=pickerUI.input;selected=pickerUI.previews;status=pickerUI.status;
  const manual=field.closest('.reference-manual');pickerUI.root.append(manual);const urlLabel=manual.querySelector('label');if(urlLabel){const text=urlLabel.querySelector('.floating-label')||urlLabel;if(text===urlLabel&&text.contains(field)){for(const n of [...text.childNodes])if(n.nodeType===3)n.remove();text.prepend(document.createTextNode('URLs (one per line)'));}else text.textContent='URLs (one per line)';}FormUI.disclosure(manual.querySelector('summary'),'Use URLs');
  const libraryUI=ReferenceUpload.create({label:kind[0].toUpperCase()+kind.slice(1)+' references',kinds:[kind],multiple:true,maxBytes,limits:()=>selectedModel()?.reference_media_limits?.[kind]||{},locked:()=>busy||loading||!!pendingSelection,onFiles:upload});
  const oldLibraryDrop=library.querySelector('#libraryDropzone');oldLibraryDrop.replaceWith(libraryUI.root);libraryUI.drop.id='libraryDropzone';libraryUI.upload.dataset.upload='';
  const selectedCards=new Map();
  let pendingUploads=[];
  function message(text = '', error = false) {
    for (const target of [status, libraryStatus]) { target.textContent = text; target.classList.toggle('error', error); target.classList.toggle('sr-only',!!text&&!error&&!/saved without|cancelled|unavailable|pending|unchanged|could not|failed/i.test(text)); }
  }
  function button(text, label, action) {
    const node = document.createElement('button'); node.type = 'button'; node.className = 'secondary'; node.textContent = text;
    node.setAttribute('aria-label', label); node.addEventListener('click', action); return node;
  }
  function write(values,validated=false,entries=null) { if(!values.length)sparse=false;if(entries)roleEntries=entries.map((entry,i)=>({...entry,url:values[i]}));else syncRoles(values);acceptedValues=[...values];lastDraft=values.join('\n');urlDraftError='';field.value = lastDraft;if(validated){checkedSignature=selectionSignature();checking=false;validationError='';} field.dispatchEvent(new Event('input', {bubbles:true})); }
  function preview(card, url, name) {
    const providerPreview=metadata.get(url)?.preview_url;
    if(url.startsWith('asset://')&&providerPreview&&window.ProviderReferenceLibrary?.safeURL(providerPreview)){const n=document.createElement(image?'img':kind==='video'?'video':'div');if(image||kind==='video'){n.src=providerPreview;n.alt=name;n.referrerPolicy='no-referrer';n.preload='metadata';n.muted=true;}else n.textContent='Audio';card.append(n);return;}
    let local = false;
    try { const parsed = new URL(url); local = parsed.origin === location.origin && parsed.pathname.startsWith('/references/'); } catch {}
    const node = document.createElement(local ? (image?'img':kind==='video'?'video':'div') : 'div');
    if (local && (image||kind==='video')) { node.src = url; node.alt = name; node.loading = 'lazy'; node.preload='metadata'; node.muted=true; node.referrerPolicy = 'no-referrer'; node.draggable = false; }
    else if(local){node.className='reference-placeholder';node.textContent='Audio';}
    else { node.className = 'reference-placeholder'; node.textContent = url.startsWith('asset://') ? 'Registered asset' : `${kind} URL`; }
    card.append(node);
  }
  function syncFileInput() {
    // Voice uploads use this input while the ordinary audio field is hidden.
    if(kind==='audio')files.disabled=field.closest('#audioURLField').hidden&&!(library.open&&voiceTarget&&mode()?.supports_reference_voice);
  }
  function updateCaption(card,name,index){const roleName=mode()?.image_roles?.[image?index:-1],spec=window.ReferencePrompts?.contract(selectedModel(),{reference_mode:modeSelect.value}),offset=image&&mode()?.id==='first_frame_references'?1:0,address=spec?.types.includes(kind)&&index>=offset?ReferencePrompts.label(spec.syntax,kind,index-offset):'';card.querySelector('.reference-name').textContent=(address?address+' · ':roleName?roleName+': ':'')+name;}

  const displayedValues=()=>pendingSelection?.values||urls();
  function currentSelection(pending){return pending.epoch===epoch&&pending.key===generationKeyVersion&&pending.voice===voiceTarget&&pending.intent===ReferenceValidation.signature(validationTarget())&&pending.selection===selectionSignature();}
  function discardSelection(){if(pendingSelection){pendingSelection.controller.abort();pendingSelection=null;}}
  function pendingCards(){return pendingUploads.filter(entry=>!entry.cancelled).map(entry=>entry.card);}
  function removePending(entry){pendingUploads=pendingUploads.filter(value=>value!==entry);}
  function focusUpload(entry){
    if(!entry.restoreFocus)return;
    if(document.activeElement!==document.body&&!entry.card.contains(document.activeElement)){entry.restoreFocus=false;return;}
    const next=pendingCards().find(card=>card.isConnected)?.removeButton,upload=library.open?libraryUI.upload:pickerUI.upload,target=next||(!upload.disabled?upload:null);
    if(target){entry.restoreFocus=false;target.focus({preventScroll:true});}
  }
  function pendingFile(file){
    const entry={file,controller:new AbortController(),cancelled:false,restoreFocus:false};
    const cancel=()=>{if(entry.cancelled||!pendingUploads.includes(entry))return;entry.restoreFocus=entry.card.contains(document.activeElement);entry.cancelled=true;entry.controller.abort();removePending(entry);render();message('Upload cancelled: '+file.name);focusUpload(entry);};
    const card=ReferenceUpload.mediaCard({name:file.name,type:'pending',placeholder:' '}),spinner=document.createElement('span'),announcement=document.createElement('span');
    const action=ReferenceUpload.mediaAction('Cancel upload',event=>{event.stopPropagation();cancel();});action.classList.add('reference-selection-remove');action.setAttribute('aria-label','Cancel upload '+file.name);card.querySelector('.media-picker-media').append(action);card.removeButton=action;
    card.classList.add('reference-upload-pending');card.dataset.uploadPending='';card.removeAttribute('data-selected');card.setAttribute('aria-busy','true');
    spinner.className='reference-upload-spinner';spinner.setAttribute('aria-hidden','true');announcement.className='sr-only';announcement.setAttribute('role','status');announcement.setAttribute('aria-live','polite');announcement.textContent='Queued upload: '+file.name;
    card.preview.replaceChildren(spinner,announcement);entry.card=card;entry.announcement=announcement;return entry;
  }
  function render() {
    if(pendingSelection&&!currentSelection(pendingSelection)){discardSelection();checkedSignature='';message('Selection, model or workflow changed. Choose the file again.',true);}
    pickerUI.sync();libraryUI.sync();const label=manual.querySelector('label');if(label&&!label.contains(field))label.textContent=limit()===1?'URL':'one per line';parseDraft();if(urlDraftError)message(urlDraftError,true);
    syncFileInput();files.accept=accept();
    if(!urlDraftError&&!pendingSelection)recheck();
    const values = displayedValues();if(!pendingSelection)syncRoles(values);selected.classList.toggle('has-reference-roles',(ModelControls.schema(selectedModel(),kind)?.roles.length||0)>1);
    const occurrences=new Map();const nodes=values.map((url,index)=>{
      const info=metadata.get(url),identity=info?.id?JSON.stringify([info.scope||'organization',info.id]):url||'empty:'+index,occurrence=occurrences.get(identity)||0;occurrences.set(identity,occurrence+1);const key=identity+'#'+occurrence;let card=selectedCards.get(key);if(card){if(info)card.update({name:info.name||card.querySelector('.reference-name').textContent,type:kind+'/unknown',url:url.startsWith('asset://')?ProviderReferenceLibrary.safeURL(info.preview_url):absolute(info.url||url),version:info.version,revision:info.revision});card.currentIndex=index;card.draggable=!busy&&!pendingSelection;card.querySelector('.reference-selection-remove').disabled=busy;if(card.decoration===JSON.stringify([selectionVersion,modeSelect.value,voices[url],roleEntries[index]?.role,index,!!pendingSelection])){updateCaption(card,!url?'Choose reference':info?.name||(url.startsWith('asset://')?url.slice(8):`Reference ${index+1}`),index);return card;}card.querySelectorAll('.reference-role,.reference-voice,.reference-clear-voice').forEach(n=>n.remove());}
      const name = !url?'Choose reference':metadata.get(url)?.name || (url.startsWith('asset://') ? url.slice(8) : `Reference ${index + 1}`);
      let previewURL='';try{const u=new URL(url);if(u.origin===location.origin&&u.pathname.startsWith('/references/'))previewURL=url;}catch{}if(url.startsWith('asset://'))previewURL=ProviderReferenceLibrary.safeURL(metadata.get(url)?.preview_url)||'';
      if(!card){card=ReferenceUpload.mediaCard({url:previewURL,name,type:kind+'/unknown',placeholder:url.startsWith('asset://')?'Registered asset':kind+' URL'},{onRemove:()=>{if(busy)return;const i=card.currentIndex;if(pendingSelection)chooseValues(displayedValues().filter((_,n)=>n!==i));else write(urls().filter((_,n)=>n!==i),false,roleEntries.filter((_,n)=>n!==i));pickerUI.upload.focus();}});selectedCards.set(key,card);card.currentIndex=index;
      if(!url){const choose=button('Choose reference',`Choose ${kind} reference ${index+1}`,()=>{missingTarget=card.currentIndex;returnFocus=choose;previousFocus=document.activeElement;library.showModal();renderLibrary();load();});card.querySelector('.media-picker-media').append(choose);}
      card.removeButton.setAttribute('aria-label','Remove '+name+' from generation');
      const remove=card.removeButton;remove.disabled=busy;
      card.draggable = !busy&&!pendingSelection; card.tabIndex = 0; card.title = 'Drag to reorder. Use Alt + Left or Right Arrow with the keyboard.';
      card.addEventListener('dragstart', event => {
        if (busy || pendingSelection || event.target.closest('button,input,select')) { event.preventDefault(); return; }
        draggedURL = String(card.currentIndex); event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData(dragType, String(card.currentIndex)); card.classList.add('dragging');
      });
      card.addEventListener('dragend', () => { draggedURL = null; card.classList.remove('dragging'); document.querySelectorAll('.dragover').forEach(el => el.classList.remove('dragover')); });
      card.addEventListener('keydown', event => {
        if (!event.altKey || !['ArrowLeft','ArrowRight'].includes(event.key) || busy || pendingSelection) return;
        event.preventDefault(); reorder(card.currentIndex, card.currentIndex + (event.key === 'ArrowLeft' ? -1 : 1),true);
      });

      bindDrop(card,()=>card.currentIndex);}
      card.currentIndex=index;card.draggable=!busy&&!pendingSelection;card.decoration=JSON.stringify([selectionVersion,modeSelect.value,voices[url],roleEntries[index]?.role,index,!!pendingSelection]);
      updateCaption(card,name,index);
      const role=ModelControls.roleField(selectedModel(),kind,index+1,roleEntries[index]?.role,value=>{roleEntries[card.currentIndex].role=value;checkedSignature='';recheck();window.dispatchEvent(new Event('gateway-reference-change'));updatePrice();});if(role){role.classList.add('reference-role');if(pendingSelection)for(const control of role.querySelectorAll('input,select,button'))control.disabled=true;card.append(role);}
      if(url&&kind!=='audio'&&mode()?.supports_reference_voice&&!(image&&index<(mode()?.voice_image_start||0))){
        const voice=button(voices[url]?'Change voice reference':'Add voice reference',`Voice reference for ${name}`,()=>{voiceTarget={url};pickers[2].open(voice);});voice.disabled=!!pendingSelection;voice.classList.add('reference-voice');card.append(voice);
        if(voices[url]){const clear=button('Clear voice',`Clear voice for ${name}`,()=>{delete voices[url];render();updatePrice();});clear.disabled=!!pendingSelection;clear.classList.add('reference-clear-voice');card.append(clear);}
      }
      return card;
    });
    ReferenceUpload.reconcile(selected,[...nodes,...(!library.open?pendingCards():[])]);for(const [key,card]of selectedCards)if(!nodes.includes(card))selectedCards.delete(key);
    remote?.configure();
    if (library.open) renderLibrary();
    window.dispatchEvent(new Event('gateway-reference-change'));
  }
  async function select(item) {
    const values=displayedValues(),canonical=absolute(item.url),url=values.find(value=>{const known=metadata.get(value);return known?.id===item.id&&(known.scope||'organization')===(item.scope||'organization');})||canonical,voiceIntent=kind==='audio'?voiceTarget:null;
    if(busy)return;
    if(!voiceIntent&&values.includes(url)&&!(sparse&&values.includes(''))){chooseValues(values.filter(value=>value!==url));return;}
    if(busy)return;
    if(!voiceIntent&&limit()!==1&&values.length>=limit()&&!(sparse&&values.includes(''))){message(`This model accepts up to ${limit()} ${plural}. Remove one first.`,true);return;}
    if(!voiceIntent){metadata.set(url,item);return chooseValues(insert(values,url));}
    const token=epoch,validation=validationTarget(),intent=ReferenceValidation.signature(validation),selection=selectionSignature();
    busy=true;message(`Checking ${item.name}…`);renderLibrary();
    try{
      await ReferenceValidation.check(voiceIntent?ReferenceValidation.ids([url]):candidateIDs(insert(values,url)),validation,{signal:controller.signal});
      if(token!==epoch)return;
      if(intent!==ReferenceValidation.signature(validationTarget())||selection!==selectionSignature()){message('Selection, model or workflow changed. Choose the file again.',true);return;}
      if(voiceIntent){if(voiceTarget===voiceIntent){voices[voiceIntent.url]=url;voiceTarget=null;library.close();pickers.forEach(p=>p.render());updatePrice();}return;}
      metadata.set(url,item);write(insert(values,url),true);missingTarget=null;message();
    }catch(error){if(token===epoch&&error.name!=='AbortError')message(error.message,true);}
    finally{if(token===epoch){busy=false;render();}}
  }
  async function chooseValues(values){
    discardSelection();
    if(JSON.stringify(values)===JSON.stringify(urls())){checkedSignature='';render();message();return;}
    // Paint the intended selection immediately, but do not put uninspected
    // references in the request or enable generation while validation is pending.
    const intent=ReferenceValidation.signature(validationTarget()),validation=validationTarget(),parent=controller.signal,abort=new AbortController(),pending={values:[...values],controller:abort,epoch,key:generationKeyVersion,voice:voiceTarget,intent,selection:selectionSignature()};
    if(validation){
      const roles={...validation.options.reference_roles};for(const address of Object.keys(roles))if(address.startsWith(kind+'_'))delete roles[address];
      rolesFor(values).forEach((entry,index)=>{roles[kind+'_'+(index+1)]=entry.role;});
      const normalized=ModelControls.roles(selectedModel(),roles,Object.keys(roles));
      if(normalized)validation.options.reference_roles=normalized;else delete validation.options.reference_roles;
    }
    const cancel=()=>abort.abort();parent.addEventListener('abort',cancel,{once:true});if(parent.aborted)cancel();
    pendingSelection=pending;checkedSignature='';checking=false;message('Checking reference compatibility…');render();let failure='';
    try{
      await ReferenceValidation.check(candidateIDs(values),validation,{signal:abort.signal});
      if(pendingSelection!==pending)return;
      if(!currentSelection(pending)){failure='Selection, model or workflow changed. Choose the file again.';return;}
      pendingSelection=null;write(values,true);missingTarget=null;message();
    }catch(error){if(pendingSelection===pending&&error.name!=='AbortError')failure=error.message;}
    finally{
      parent.removeEventListener('abort',cancel);
      if(pendingSelection===pending){pendingSelection=null;render();if(failure)message(failure,true);}
    }
  }
  function recheck(){
    const stamp=selectionSignature();if(stamp===checkedSignature)return;
    checkedSignature=stamp;const previousIssue=validationError;validationError='';const referenceIDs=candidateIDs(urls()),voiceIDs=kind==='audio'&&mode()?.supports_reference_voice?ReferenceValidation.ids(Object.values(activeVoices())):[];
    if(!referenceIDs.length&&!voiceIDs.length){checking=false;if(status.textContent==='Checking reference compatibility…'||previousIssue&&status.textContent===previousIssue)message();return;}
    const token=epoch;checking=true;message('Checking reference compatibility…');
    Promise.all([ReferenceValidation.check(referenceIDs,validationTarget(),{signal:controller.signal}),...voiceIDs.map(id=>ReferenceValidation.check([id],{...validationTarget(),role:'reference_voice'},{signal:controller.signal}))]).then(()=>{
      if(token===epoch&&stamp===selectionSignature()&&!pendingSelection){checking=false;if(status.textContent==='Checking reference compatibility…')message();}
    }).catch(error=>{
      if(token===epoch&&stamp===selectionSignature()&&!pendingSelection){checking=false;if(error.name!=='AbortError'){validationError=error.message;message(validationError,true);}}
    });
  }
  function renderLibrary() {
    if(remote?.active()){remote.render();ReferenceUpload.reconcile(libraryUI.previews,pendingCards());return;}
    ReferenceUpload.reconcile(libraryUI.previews,[]);
    const values = displayedValues();
    libraryView.render(items.filter(item=>(item.content_type||'image/png').startsWith(kind+'/')).map(item=>({...item,url:absolute(item.url)})),{
      selected:item=>values.some(url=>url===item.url||metadata.get(url)?.id===item.id&&(metadata.get(url)?.scope||'organization')===(item.scope||'organization')),onSelect:select,busy,
      canDelete:()=>!pendingSelection,scope:sharedLibrary?'template':'organization',onDelete:sharedLibrary?null:deleteItem,transientNodes:pendingCards()
    });
    more.hidden = !hasMore; more.disabled = busy || loading;
  }
  async function load(append = false) {
    if(remote?.active())return remote.load(append);
    if (busy) return;
    const sequence=++listSequence,requestedShared=sharedLibrary;listController?.abort();listController=new AbortController();
    const currentEpoch = epoch; loading = true; more.disabled = true; message('Loading library…');
    pickerUI.sync();libraryUI.sync();
    try {
      const data = await api(`${requestedShared?"/v1/system/template-references":"/v1/references"}?offset=${append ? offset : 0}`, {signal:AbortSignal.any([controller.signal,listController.signal])});
      if (currentEpoch !== epoch || sequence!==listSequence) return;
      items = append ? [...items, ...(data.data || []).filter(item => !items.some(old => old.id === item.id))] : (data.data || []);
      offset = data.next_offset || items.length; hasMore = !!data.has_more;
      items.forEach(item=>{item.scope=requestedShared?'template':'organization';for(const [url,known]of metadata)if(known.id===item.id&&(known.scope||'organization')===item.scope)metadata.set(url,item);metadata.set(absolute(item.url),item);});
      message(items.length ? '' : 'Your library is empty. Upload a reference file.');
 window.StorageQuota?.refresh().catch(()=>{});
    } catch (error) { if (currentEpoch === epoch && sequence===listSequence && error.name!=='AbortError') message(`Could not load library: ${error.message}`, true); }
    finally { if (currentEpoch === epoch && sequence===listSequence) { loading = false; render(); } }
  }
  async function upload(list, replaceIndex = null) {
    if ((library.open&&sharedLibrary) || busy || loading || pendingSelection || !list.length) return;
    if(remote?.active()&&!remote.prepareUpload())return;
    const currentEpoch=epoch,modelID=selectedModel()?.id,validation=validationTarget(),intent=ReferenceValidation.signature(validation),parentSignal=controller.signal,voiceIntent=kind==='audio'?voiceTarget:null,providerUpload=!!remote?.active();
    let expectedSelection=selectionSignature(),added=0,duplicates=[],failures=[],unattached=0;
    const entries=[];
    for(const file of list){
      let error=file.size>maxBytes()?`${file.name}: size ${file.size} bytes; maximum ${maxBytes()} bytes. Reduce the file size.`:!file.size?`${file.name}: file is empty.`:!accept().split(',').includes(file.type)?`${file.name}: format ${file.type||'unknown'}; use a supported ${kind} file (${accept()}).`:'';
      if(!error&&providerUpload)error=remote.fileError(file);
      if(error){failures.push(error);continue;}entries.push(pendingFile(file));
    }
    if(!entries.length){message(failures.join(' '),true);return;}
    const abort=()=>{for(const entry of entries)entry.controller.abort();};parentSignal.addEventListener('abort',abort,{once:true});if(parentSignal.aborted)abort();
    pendingUploads=entries;busy=true;message();libraryUI.message();remote?.setUploading(true);render();
    try {
      await window.GenerationKey.ensureSession(true);
      if(currentEpoch!==epoch)return;
      const expectedKey=window.GenerationKey.value();
      for(const entry of entries){
        if(entry.cancelled)continue;
        const {file}=entry,signal=entry.controller.signal;entry.announcement.textContent='Uploading '+file.name;message('Uploading '+file.name+'…');
        try {
          signal.throwIfAborted();await window.StorageQuota?.check();signal.throwIfAborted();if(currentEpoch!==epoch)return;
          const uploadValidation={...validation,reference_ids:validationTarget()?.reference_ids?.filter(id=>replaceIndex===null||id!==ReferenceValidation.ids([urls()[replaceIndex]])[0])||[]};
          const item=await ReferenceValidation.upload(file,{validation:uploadValidation,signal,expectedKey,onProgress:text=>{if(currentEpoch===epoch&&!entry.cancelled){entry.announcement.textContent=text;message(text);}}});
          signal.throwIfAborted();if(currentEpoch!==epoch)return;
          // Upload cancellation does not undo completed storage or provider registration.
          if(providerUpload){removePending(entry);render();await remote.registerUploaded(item);added++;continue;}
          const url=absolute(item.url);metadata.set(url,item);if(item.duplicate)duplicates.push(file.name);else added++;
          if(!items.some(existing=>existing.id===item.id)){if(item.duplicate)items.push(item);else items.unshift(item);}
          const values=urls();
          if(voiceIntent){
            if(voiceTarget===voiceIntent&&expectedSelection===selectionSignature()){voices[voiceIntent.url]=url;voiceTarget=null;library.close();pickers.forEach(p=>p.render());updatePrice();}else unattached++;
            continue;
          }
          if(values.includes(url)&&!(sparse&&values.includes('')))continue;
          if(expectedSelection!==selectionSignature()||intent!==ReferenceValidation.signature(validationTarget())||modelID!==selectedModel()?.id||!selectedModel()?.[`supports_${kind}_input`]){unattached++;continue;}
          const candidate=replaceIndex!==null&&replaceIndex<values.length?values.map((v,i)=>i===replaceIndex?url:v):insert(values,url);
          await ReferenceValidation.check(candidateIDs(candidate),validation,{signal,expectedKey});signal.throwIfAborted();if(currentEpoch!==epoch)return;
          if(expectedSelection!==selectionSignature()||intent!==ReferenceValidation.signature(validationTarget())){unattached++;continue;}
          if(replaceIndex!==null&&replaceIndex<values.length){values[replaceIndex]=url;replaceIndex=null;write(values,true);}
          else if(limit()===1||values.length<limit()||sparse&&values.includes(''))write(insert(values,url),true);else unattached++;
          expectedSelection=selectionSignature();
        }catch(error){if(currentEpoch===epoch&&!entry.cancelled&&error.name!=='AbortError')failures.push(`${file.name}: ${error.message}`);}
        finally{removePending(entry);if(currentEpoch===epoch){render();focusUpload(entry);}}
        if(currentEpoch!==epoch)return;
      }
    }catch(error){if(currentEpoch===epoch&&error.name!=='AbortError')failures.push(error.message);}
    finally{
      parentSignal.removeEventListener('abort',abort);
      if(currentEpoch===epoch){
        pendingUploads=[];busy=false;remote?.setUploading(false);files.value='';window.dispatchEvent(new Event('gateway-storage-change'));
        if(library.open)await load();else render();
        if(currentEpoch!==epoch||parentSignal.aborted)return;
        message([added?(providerUpload?`${added} file${added===1?'':'s'} sent for BytePlus registration.`:`${added} file${added===1?'':'s'} saved to your library.`):'',unattached?`${unattached} saved without adding to this generation (selection or model changed, or reference limit reached).`:'',duplicates.length?`Already in your library: ${duplicates.join(', ')}. The existing file was used; no copy was created.`:'',entries.some(entry=>entry.cancelled)?'Upload cancelled.':'',...failures].filter(Boolean).join(' '),!!failures.length);
        for(const entry of entries)focusUpload(entry);
      }
    }
  }
  async function deleteItem(item) {
    if (busy || loading) return;
    const currentEpoch = epoch; busy = true; renderLibrary(); message('Deleting file…');
    try {
      await api(`/v1/references/${encodeURIComponent(item.id)}`, {method:'DELETE', signal:controller.signal});
      if (currentEpoch !== epoch) return;
      ReferenceLibrary.deleted(item,'organization');
      for(const [visual,audio] of Object.entries(voices)){if(visual===absolute(item.url)||audio===absolute(item.url))delete voices[visual];}
      metadata.delete(absolute(item.url)); items = items.filter(old => old.id !== item.id);
      write(sparse?urls().map(url=>url===absolute(item.url)?'':url):urls().filter(url=>url!==absolute(item.url))); offset = Math.max(0, offset-1); message('File deleted.');window.dispatchEvent(new Event('gateway-storage-change'));
    } catch (error) { if (currentEpoch === epoch) message(`Could not delete file: ${error.message}`, true); }
    finally { if (currentEpoch === epoch) { busy = false; render();window.dispatchEvent(new Event('gateway-storage-change')); } }
  }
  window.addEventListener('gateway-reference-deleted',event=>{
    const detail=event.detail;if(!['organization','template'].includes(detail.scope)||detail.keyVersion!==generationKeyVersion)return;
    const matches=url=>url===absolute(detail.url)||ReferenceValidation.ids([url]).includes(detail.id);
    for(const [visual,audio] of Object.entries(voices))if(matches(visual)||matches(audio))delete voices[visual];
    for(const [url,item] of metadata)if(item.id===detail.id||matches(url))metadata.delete(url);
    items=items.filter(item=>item.id!==detail.id);write(sparse?urls().map(url=>matches(url)?'':url):urls().filter(url=>!matches(url)));
  });
  function reorder(source, targetIndex,focus=false) {
    const values = urls();source=Number(source);
    if (busy || pendingSelection || !Number.isInteger(source) || source >= values.length || source < 0 || targetIndex < 0 || targetIndex >= values.length || source === targetIndex) return;
    const [value]=values.splice(source, 1);values.splice(targetIndex,0,value);const entries=[...roleEntries],[entry]=entries.splice(source,1);entries.splice(targetIndex,0,entry);write(values,false,entries);if(focus)selected.children[targetIndex]?.focus();message('Reference order updated.');
    selected.children[targetIndex]?.focus();
  }
  function bindDrop(target, index = null) {
    target.addEventListener('dragover', event => {
      if(pendingSelection)return;
      const at=typeof index==='function'?index():index;
      const internal = draggedURL !== null || event.dataTransfer.types.includes(dragType);
      if (!internal && !event.dataTransfer.types.includes('Files')) return;
      event.preventDefault(); event.stopPropagation(); event.dataTransfer.dropEffect = internal ? 'move' : at!==null&&urls()[at] ? 'none' : 'copy'; if(internal||index===null||!urls()[at])target.classList.add('dragover');
    });
    target.addEventListener('dragleave', event => { if (!target.contains(event.relatedTarget)) target.classList.remove('dragover'); });
    target.addEventListener('drop', event => {
      if(pendingSelection){event.preventDefault();event.stopPropagation();message('Wait for reference validation to finish.',true);return;}
      const at=typeof index==='function'?index():index;
      event.preventDefault(); event.stopPropagation(); target.classList.remove('dragover');
      const source = draggedURL || event.dataTransfer.getData(dragType);
      if (source) { draggedURL = null; reorder(source, at === null ? urls().length - 1 : at); return; }
      // Browser-native URL/image drags are not file uploads.
      if (event.dataTransfer.types.includes('text/uri-list') || event.dataTransfer.types.includes('text/html')) return;
      if(at!==null&&urls()[at]){message('Remove this reference before choosing another file.');return;}
      upload([...event.dataTransfer.files], at);
    });
  }
  function validate() {
    parseDraft();if(urlDraftError){message(urlDraftError,true);return false;}
    if(remote&&!remote.validate())return false;
    if (pendingSelection||checking) { message('Wait for reference validation to finish.',true);return false; }
    if(validationError){message(validationError,true);return false;}
    if (busy) { message('Wait for the reference operation to finish.', true); return false; }
    const model = selectedModel(); if (!model?.[`supports_${kind}_input`]) return true;
    const count = urls().length, minimum = mode()?.["min_"+plural] ?? (image?Math.max(model.min_image_inputs || 0, model.requires_image_input ? 1 : 0):model[`requires_${kind}_input`]?1:0);
 if(limit()===0)return true;
    if(sparse&&urls().some(url=>!url)){message('Choose a file for each empty reference, or remove the empty references before generating.',true);return false;}
    if (count < minimum) { message(`This model needs at least ${minimum} ${kind} reference${minimum === 1 ? '' : 's'}.`, true); return false; }
    if (count > limit()) { message(`This model accepts up to ${limit()} ${plural}. Remove the extra files.`, true); return false; }
    return true;
  }
  function reset() {
    discardSelection();sparse=false;missingTarget=null;roleEntries=[];remote?.reset();
    draggedURL = null; epoch++; controller.abort(); controller = new AbortController(); pendingUploads=[]; busy = false; loading = false;
    checkedSignature='';checking=false;validationError='';
    items = []; metadata.clear(); offset = 0; hasMore = false; library.close(); files.value = ''; write([]); message();
  }
  // The shared surface owns file chooser and drop handlers.
  function cancelVoice(){missingTarget=null;if(kind==='audio'){voiceTarget=null;syncFileInput();}}
  library.querySelectorAll('[data-close]').forEach(node => node.addEventListener('click', () => {cancelVoice();library.close();}));
  library.addEventListener('cancel',cancelVoice);
  // Native close events may be queued until after the picker has reopened.
  library.addEventListener('close',()=>{if(!library.open){listSequence++;listController?.abort();loading=false;pickerUI.sync();libraryUI.sync();cancelVoice();if(pendingUploads.length)render();const label=returnFocus?.getAttribute('aria-label');const trigger=returnFocus?.isConnected?returnFocus:label?[...document.querySelectorAll('button[aria-label]')].find(n=>n.getAttribute('aria-label')===label):null;const active=document.activeElement;if(trigger&&!trigger.disabled&&!trigger.closest('[hidden]')&&(active===document.body||active===previousFocus||active===returnFocus||library.contains(active)))trigger.focus();}});
  more.addEventListener('click', () => load(true));
  field.addEventListener('input', render);
  function describe(url, index) {
    if (metadata.has(url)) return metadata.get(url).name;
    try { const parsed = new URL(url); if (parsed.origin === location.origin && parsed.pathname.startsWith('/references/')) return `Library reference ${index + 1}`; } catch {}
    return url;
  }
  remote=window.ProviderReferenceLibrary?.mount({kind,library,field,metadata,more,message,urls,write,select,renderSelected:render,loadLocal:()=>load(),onSourceChange:value=>{if(value!==lastRemoteSource){lastRemoteSource=value;listSequence++;listController?.abort();loading=false;items=[];offset=0;hasMore=false;}sourceLabel.hidden=value==='provider';if(value==='provider'){sharedLibrary=false;source.value='private';libraryUI.root.hidden=false;}}});
  return {render,validate,describe,reset,roles:readRoles,restoreRoles(value){roleEntries=urls().map((url,i)=>({url,role:value?.[kind+'_'+(i+1)]}));},restoreSlots(values){sparse=!!values?.length;lastDraft=null;missingTarget=null;},values:urls,info:url=>metadata.get(url),issue:()=>urlDraftError||validationError,open(trigger){returnFocus=trigger||document.activeElement;previousFocus=document.activeElement;library.showModal();render();load();},has:url=>metadata.has(url)};
 }
 for(const kind of ["image","video","audio"])pickers.push(picker(kind));
 window.referencePicker={configure,roles(){const values=Object.assign({},...pickers.map(p=>p.roles())),addresses=Object.keys(values);return ModelControls.roles(selectedModel(),values,addresses);},restoreRoles(value){pickers.forEach(p=>p.restoreRoles(value));},values(kind){return pickers[['image','video','audio'].indexOf(kind)].values();},restoreSlots(options){pickers.forEach((p,i)=>p.restoreSlots(options?.[['image_urls','video_urls','audio_urls'][i]]));},get(kind,index){const p=pickers[['image','video','audio'].indexOf(kind)];const url=p?.values()[index];return url?{url,name:p.describe(url,index),...p.info(url)}:null;},restoreMode(options){
   const model=selectedModel(),modes=model?.reference_modes||[];
   let value=options.reference_mode;
   if(!value){
     const images=options.image_urls?.length||0,videos=options.video_urls?.length||(options.video_url?1:0);
     const audios=options.audio_urls?.length||(options.audio_url?1:0);
     value=modes.find(m=>images>=m.min_images&&images<=m.max_images&&videos>=m.min_videos&&videos<=m.max_videos&&audios>=m.min_audios&&audios<=m.max_audios&&(!m.max_visuals||images+videos<=m.max_visuals)&&images+videos>=(m.min_visuals||0)&&(!m.requires_visual||images+videos>0||!audios))?.id;

   }
   modeSelect.value=modes.some(item=>item.id===value)?value:modes[0]?.id||'';
 },voices:activeVoices,restoreVoices(value){voices=value||{};},render(){configure();pickers.forEach(p=>p.render());},validate(){const valid=pickers.every(p=>p.validate());if(!valid){const issue=pickers.map(p=>p.issue()).find(Boolean);if(issue)setMessage(issue,'error');}return valid;},describe(url,index){return (pickers.find(p=>p.has(url))||pickers[0]).describe(url,index);},reset(){voices={};voiceTarget=null;pickers.forEach(p=>p.reset());}};
 window.referencePicker.render();
})();
