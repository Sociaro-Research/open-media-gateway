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
 let modeModel='';
 const mode=()=>selectedModel()?.reference_modes?.find(item=>item.id===modeSelect.value);
 function configure() {
   const model=selectedModel();if(!model)return;
   modeField.hidden=!model.reference_modes?.length;modeSelect.disabled=modeField.hidden;
   const modes=model.reference_modes||[], signature=JSON.stringify([selectedProvider()?.id,model.id,modes]);
   if(modeModel!==signature){
     voiceTarget=null;modeModel=signature;const previous=modeSelect.value;
     modeSelect.replaceChildren(...modes.map(item=>new Option(item.label,item.id)));
     if(modes.some(item=>item.id===previous))modeSelect.value=previous;
   }
   const profile=mode();
   for(const kind of ['image','video','audio']) {
     const target=document.querySelector(kind==='image'?'#imageURLsField':`#${kind}URLField`);
     const manual=target.querySelector('.reference-manual');
     manual.querySelector('summary').textContent=`Use ${kind} URLs${model.supports_asset_references?' or registered assets':''}`;
     let hint=manual.querySelector('[data-asset-hint]');
     if(!hint){hint=document.createElement('p');hint.className='field-hint';hint.dataset.assetHint='';manual.append(hint);}
     hint.hidden=!model.supports_asset_references;
     hint.textContent='For registered BytePlus assets, paste asset:// followed by the asset ID, one per line. Use assets from the same BytePlus account and project as your provider connection.';
     target.hidden=profile?profile['max_'+(kind==='image'?'images':kind+'s')]===0:!model[`supports_${kind}_input`];
   }
   document.querySelector('#referenceModeHint').textContent=profile?.image_roles?.length ? 'Image order: '+profile.image_roles.join(' → ')+'.' : '';
 }
 modeSelect.addEventListener('change',()=>{voiceTarget=null;configure();pickers.forEach(p=>p.render());updatePrice();});
 function picker(kind) {
  const image=kind==='image',plural=kind==='image'?'images':kind+'s',displayPlural=kind==='audio'?'audio files':plural;
  const maxBytes=(image?30:kind==='video'?200:50)*1024*1024;
  const accept=image?'image/png,image/jpeg,image/webp,image/gif':kind==='video'?'video/mp4,video/quicktime,video/webm': 'audio/mpeg,audio/wav,audio/x-wav,audio/ogg,audio/flac';
  if(!image){
    const container=document.querySelector(`#${kind}URLField`),input=document.querySelector(`#${kind}URL`);
    const label=container.querySelector('label'),control=input.closest('.floating-field')||input;
    label.textContent=`Or paste ${kind} URLs`;
    const heading=document.createElement('label');heading.id=`${kind}ReferencesLabel`;heading.textContent=kind==='video'?'Video references':'Audio references';
    const manual=document.createElement('details');manual.className='reference-manual';manual.innerHTML=`<summary>Use ${kind} URLs</summary>`;
    // Move the complete field, including its floating label, into manual entry.
    // The upload section's heading must stay outside that field.
    control.before(heading,manual);if(control===input)manual.append(label);manual.append(control);
    manual.insertAdjacentHTML('beforebegin',`<div id="${kind}ReferenceDropzone" class="reference-dropzone" role="group" aria-labelledby="${heading.id}"><p>Drop ${displayPlural} here</p><div class="reference-actions"><button type="button" class="secondary" id="${kind}UploadReference">Upload ${displayPlural}</button><button type="button" class="secondary" id="${kind}ChooseReferences">Choose from library</button></div><small>Up to ${maxBytes/1024/1024} MB each</small></div><input id="${kind}ReferenceFiles" type="file" accept="${accept}" multiple hidden><div id="${kind}SelectedReferences" class="selected-references"></div><p id="${kind}ReferenceStatus" class="reference-status" role="status"></p>`);
  }

  const field = document.querySelector(image?'#imageURLs':`#${kind}URL`);
  const dropzone = document.querySelector(image?'#referenceDropzone':`#${kind}ReferenceDropzone`);
  const files = document.querySelector(image?'#referenceFiles':`#${kind}ReferenceFiles`);
  const selected = document.querySelector(image?'#selectedReferences':`#${kind}SelectedReferences`);
  const status = document.querySelector(image?'#referenceStatus':`#${kind}ReferenceStatus`);
  const metadata = new Map();
  let draggedURL = null;
  const dragType = 'application/x-gateway-reference';
  let items = [], offset = 0, hasMore = false, busy = false, loading = false, epoch = 0;
  let controller = new AbortController();
 let remote;
  const library = document.createElement('dialog');
  library.className = 'reference-library'; library.id = image?'referenceLibrary':`${kind}ReferenceLibrary`;
  library.setAttribute('aria-labelledby', `${kind}ReferenceLibraryTitle`);
  library.innerHTML = `<div class="reference-library-header"><h2 id="${kind}ReferenceLibraryTitle">Reference library</h2><button type="button" class="secondary" data-close aria-label="Close library">✕</button></div>
    <p>Choose ${displayPlural} for this generation.</p>
    <div class="reference-dropzone" id="libraryDropzone"><p>Drop ${displayPlural} to add them to your library</p><button type="button" class="secondary" data-upload>Upload ${displayPlural}</button><small>Up to ${maxBytes/1024/1024} MB each</small></div>
    <p class="reference-status" role="status" data-status></p><div class="reference-library-grid" data-grid></div>
    <div class="reference-library-footer"><button type="button" class="secondary" data-more hidden>Load more</button><button type="button" class="primary" data-close>Done</button></div>`;
  document.body.append(library);
  const grid = library.querySelector('[data-grid]');
  const libraryStatus = library.querySelector('[data-status]');
  const more = library.querySelector('[data-more]');
  const urls = () => field.value.split('\n').map(s => s.trim()).filter(Boolean);
  const absolute = url => new URL(url, location.origin).href;
  const limit = () => mode()?.['max_'+plural] ?? (image ? selectedModel()?.max_image_inputs || Infinity : 1);
 const size = value => value == null ? '' : value<1024 ? `${value} B` : value<1024*1024 ? `${(value/1024).toFixed(1).replace(/\.0$/, '')} KB` : `${(value/1024/1024).toFixed(1).replace(/\.0$/, '')} MB`;
  function message(text = '', error = false) {
    for (const target of [status, libraryStatus]) { target.textContent = text; target.classList.toggle('error', error); }
  }
  function button(text, label, action) {
    const node = document.createElement('button'); node.type = 'button'; node.className = 'secondary'; node.textContent = text;
    node.setAttribute('aria-label', label); node.addEventListener('click', action); return node;
  }
  function write(values) { field.value = [...new Set(values)].join('\n'); field.dispatchEvent(new Event('input', {bubbles:true})); }
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
  function render() {
    syncFileInput();
    const values = urls();
    selected.replaceChildren(...values.map((url, index) => {
      const card = document.createElement('div'); card.className = 'reference-card';
      const name = metadata.get(url)?.name || (url.startsWith('asset://') ? url.slice(8) : `Reference ${index + 1}`);
      preview(card, url, name);
      const title = document.createElement('span'); title.className = 'reference-name'; title.textContent = `${mode()?.image_roles?.[image?index:-1] || index + 1}. ${name}`; title.title = name;
      const remove = button('×', `Remove ${name} from generation`, () => write(urls().filter((_, i) => i !== index)));
      remove.classList.add('reference-remove'); remove.disabled = busy;
      card.draggable = !busy; card.tabIndex = 0; card.title = 'Drag to reorder. Use Alt + Left or Right Arrow with the keyboard.';
      card.addEventListener('dragstart', event => {
        if (busy || event.target.closest('button')) { event.preventDefault(); return; }
        draggedURL = url; event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData(dragType, url); card.classList.add('dragging');
      });
      card.addEventListener('dragend', () => { draggedURL = null; card.classList.remove('dragging'); document.querySelectorAll('.dragover').forEach(el => el.classList.remove('dragover')); });
      card.addEventListener('keydown', event => {
        if (!event.altKey || !['ArrowLeft','ArrowRight'].includes(event.key) || busy) return;
        event.preventDefault(); reorder(url, index + (event.key === 'ArrowLeft' ? -1 : 1));
      });
      card.append(title, remove);
      if(kind!=='audio'&&mode()?.supports_reference_voice&&!(image&&index<(mode()?.voice_image_start||0))){
        const voice=button(voices[url]?'Change voice reference':'Add voice reference',`Voice reference for ${name}`,()=>{voiceTarget={url};pickers[2].open();});voice.classList.add('reference-voice');card.append(voice);
        if(voices[url]) card.append(button('Clear voice',`Clear voice for ${name}`,()=>{delete voices[url];render();updatePrice();}));
      }
      bindDrop(card, index); return card;
    }));
    remote?.configure();
    if (library.open) renderLibrary();
    window.dispatchEvent(new Event('gateway-reference-change'));
  }
  function select(item) {
    const values = urls(), url = absolute(item.url);
    if(kind==='audio'&&voiceTarget){voices[voiceTarget.url]=url;voiceTarget=null;library.close();pickers.forEach(p=>p.render());updatePrice();return;}
    if (values.includes(url)) { write(values.filter(value => value !== url)); message(); return; }
    if (values.length >= limit()) { message(`This model accepts up to ${limit()} ${plural}. Remove one first.`, true); return; }
    write([...values, url]); message();
  }
  function renderLibrary() {
    if(remote?.active()){remote.render();return;}
    const values = urls();
    grid.replaceChildren(...items.filter(item=>(item.content_type||"image/png").startsWith(kind+"/")).map(item => {
      const url = absolute(item.url), name = item.name;
      const card = document.createElement('div'); card.className = 'reference-card';
      card.dataset.id = item.id; card.dataset.selected = String(values.includes(url));
      const choose = button('', `Use ${name} in generation`, () => select(item)); choose.className = 'reference-choice';
      choose.setAttribute('aria-pressed', String(values.includes(url))); choose.disabled = busy;
      preview(choose, url, name);
      const title = document.createElement('span'); title.className = 'reference-name'; title.textContent = name; title.title = name;
      const bytes=document.createElement("small");bytes.className="reference-size";bytes.textContent=size(item.size_bytes);title.append(bytes);
      choose.append(title);
      const remove = button('Delete', `Delete ${name} from library`, () => {
        const warning = document.createElement('p'); warning.className = 'reference-delete-warning';
        warning.textContent = 'Delete this file permanently? Pending generations using it may fail.';
        const actions = document.createElement('div'); actions.className = 'reference-card-actions';
        actions.append(button('Cancel', `Keep ${name}`, renderLibrary), button('Delete file', `Confirm delete ${name}`, () => deleteItem(item)));
        remove.disabled = true; card.append(warning, actions);
      });
      remove.classList.add('reference-delete'); remove.disabled = busy; card.append(choose, remove); remote?.decorate(card,item); return card;
    }));
    more.hidden = !hasMore; more.disabled = busy || loading;
  }
  async function load(append = false) {
    if(remote?.active())return remote.load(append);
    if (loading || busy) return;
    const currentEpoch = epoch; loading = true; more.disabled = true; message('Loading library…');
    try {
      const data = await api(`/v1/references?offset=${append ? offset : 0}`, {signal:controller.signal});
      if (currentEpoch !== epoch) return;
      items = append ? [...items, ...(data.data || []).filter(item => !items.some(old => old.id === item.id))] : (data.data || []);
      offset = data.next_offset || items.length; hasMore = !!data.has_more;
      items.forEach(item => metadata.set(absolute(item.url), item));
      message(items.length ? '' : 'Your library is empty. Upload a reference file.');
 window.StorageQuota?.refresh().catch(()=>{});
    } catch (error) { if (currentEpoch === epoch) message(`Could not load library: ${error.message}`, true); }
    finally { if (currentEpoch === epoch) { loading = false; render(); } }
  }
  async function upload(list, replaceIndex = null) {
    if (busy || loading || !list.length) return;
    if(remote?.active()&&!remote.prepareUpload())return;
    const currentEpoch = epoch, modelID = selectedModel()?.id, signal = controller.signal, voiceIntent = kind==='audio'?voiceTarget:null, providerUpload=!!remote?.active();
    busy = true; remote?.setUploading(true); render();
    let added = 0, duplicates = [], failures = [], unattached = 0;
    try {
      await window.GenerationKey.ensureSession(true);
      if(currentEpoch!==epoch)return;
      const expectedKey=window.GenerationKey.value();
      for (const file of list) {
        if (file.size > maxBytes || !accept.split(',').includes(file.type)) { failures.push(`${file.name}: use a supported ${kind} file, up to ${maxBytes/1024/1024} MB`); continue; }
        if(providerUpload){const error=remote.fileError(file);if(error){failures.push(`${file.name}: ${error}`);continue;}}
        message(`Uploading ${file.name}…`);
        try {
          await window.StorageQuota?.check();
          if(currentEpoch!==epoch)return;
          const sha256=await hashReferenceFile(file,signal);
          if(currentEpoch!==epoch)return;
          const plan=await api('/v1/references/uploads',{method:'POST',body:JSON.stringify({name:file.name,content_type:file.type,size_bytes:file.size,sha256}),signal,expectedKey});
          if(currentEpoch!==epoch)return;
          let item;
          if(plan.mode==='existing')item=plan.reference;
          else if(plan.mode==='local'){
            const body=new FormData();body.append('file',file);
            item=await api('/v1/references',{method:'POST',body,signal,expectedKey});
          }else if(plan.mode==='s3'){
            let response;
            try {
              // Never send gateway credentials to storage or relay file bytes through the gateway.
              response=await fetch(plan.target.url,{method:'PUT',headers:plan.target.headers,body:file,credentials:'omit',redirect:'error',signal});
            }catch(error){if(error.name==='AbortError')throw error;throw new Error('Could not upload directly to storage. Check your connection and the bucket CORS settings.');}
            if(!response.ok)throw new Error(`Storage rejected the upload (HTTP ${response.status}).`);
            if(currentEpoch!==epoch)return;
            item=await api(`/v1/references/uploads/${encodeURIComponent(plan.id)}/complete`,{method:'POST',signal,expectedKey});
          }else throw new Error('Unknown upload method.');
          if (currentEpoch !== epoch) return;
          if(providerUpload){await remote.registerUploaded(item);added++;continue;}
          const url = absolute(item.url); metadata.set(url, item); if (item.duplicate) duplicates.push(file.name); else added++;
          const values = urls();
          // An upload belongs to the picker intent that started it, even after reopening.
          if(voiceIntent){
            if(voiceTarget===voiceIntent){voices[voiceIntent.url]=url;voiceTarget=null;library.close();pickers.forEach(p=>p.render());updatePrice();}
            else unattached++;
            continue;
          }
          if (values.includes(url)) continue;
          if (modelID !== selectedModel()?.id || !selectedModel()?.[`supports_${kind}_input`]) { unattached++; continue; }
          if (replaceIndex !== null && replaceIndex < values.length) { values[replaceIndex] = url; replaceIndex = null; write(values); }
          else if (values.length < limit()) write([...values, url]);
          else unattached++;
        } catch (error) { if (currentEpoch === epoch) failures.push(`${file.name}: ${error.message}`); }
        if (currentEpoch !== epoch) return;
      }
    } catch(error){if(currentEpoch===epoch)failures.push(error.message);} finally {
      if (currentEpoch === epoch) {
        busy = false; remote?.setUploading(false); files.value = ''; render();window.dispatchEvent(new Event('gateway-storage-change'));
        if (library.open) await load();
        message([added ? (providerUpload?`${added} file${added===1?'':'s'} sent for BytePlus registration.`:`${added} file${added === 1 ? '' : 's'} saved to your library.`) : '', unattached ? `${unattached} saved without adding to this generation (selection or model changed, or reference limit reached).` : '', duplicates.length ? `Already in your library: ${duplicates.join(', ')}. The existing file was used; no copy was created.` : '', ...failures].filter(Boolean).join(' '), !!failures.length);
      }
    }
  }
  async function deleteItem(item) {
    if (busy || loading) return;
    const currentEpoch = epoch; busy = true; renderLibrary(); message('Deleting file…');
    try {
      await api(`/v1/references/${encodeURIComponent(item.id)}`, {method:'DELETE', signal:controller.signal});
      if (currentEpoch !== epoch) return;
      for(const [visual,audio] of Object.entries(voices)){if(visual===absolute(item.url)||audio===absolute(item.url))delete voices[visual];}
      metadata.delete(absolute(item.url)); items = items.filter(old => old.id !== item.id);
      write(urls().filter(url => url !== absolute(item.url))); offset = Math.max(0, offset-1); message('File deleted.');window.dispatchEvent(new Event('gateway-storage-change'));
    } catch (error) { if (currentEpoch === epoch) message(`Could not delete file: ${error.message}`, true); }
    finally { if (currentEpoch === epoch) { busy = false; render();window.dispatchEvent(new Event('gateway-storage-change')); } }
  }
  function reorder(url, targetIndex) {
    const values = urls(), source = values.indexOf(url);
    if (busy || source < 0 || targetIndex < 0 || targetIndex >= values.length || source === targetIndex) return;
    values.splice(source, 1); values.splice(targetIndex, 0, url); write(values); message('Reference order updated.');
    selected.children[targetIndex]?.focus();
  }
  function bindDrop(target, index = null) {
    target.addEventListener('dragover', event => {
      const internal = draggedURL !== null || event.dataTransfer.types.includes(dragType);
      if (!internal && !event.dataTransfer.types.includes('Files')) return;
      event.preventDefault(); event.stopPropagation(); event.dataTransfer.dropEffect = internal ? 'move' : 'copy'; target.classList.add('dragover');
    });
    target.addEventListener('dragleave', event => { if (!target.contains(event.relatedTarget)) target.classList.remove('dragover'); });
    target.addEventListener('drop', event => {
      event.preventDefault(); event.stopPropagation(); target.classList.remove('dragover');
      const source = draggedURL || event.dataTransfer.getData(dragType);
      if (source) { draggedURL = null; reorder(source, index === null ? urls().length - 1 : index); return; }
      // Browser-native URL/image drags are not file uploads.
      if (event.dataTransfer.types.includes('text/uri-list') || event.dataTransfer.types.includes('text/html')) return;
      upload([...event.dataTransfer.files], index);
    });
  }
  function validate() {
    if(remote&&!remote.validate())return false;
    if (busy) { message('Wait for the reference operation to finish.', true); return false; }
    const model = selectedModel(); if (!model?.[`supports_${kind}_input`]) return true;
    const count = urls().length, minimum = mode()?.["min_"+plural] ?? (image?Math.max(model.min_image_inputs || 0, model.requires_image_input ? 1 : 0):model[`requires_${kind}_input`]?1:0);
 if(limit()===0)return true;
    if (count < minimum) { message(`This model needs at least ${minimum} ${kind} reference${minimum === 1 ? '' : 's'}.`, true); return false; }
    if (count > limit()) { message(`This model accepts up to ${limit()} ${plural}. Remove the extra files.`, true); return false; }
    return true;
  }
  function reset() {
    remote?.reset();
    draggedURL = null; epoch++; controller.abort(); controller = new AbortController(); busy = false; loading = false;
    items = []; metadata.clear(); offset = 0; hasMore = false; library.close(); files.value = ''; write([]); message();
  }
  bindDrop(dropzone); bindDrop(library.querySelector('#libraryDropzone'));
  document.querySelector(image?'#uploadReference':`#${kind}UploadReference`).addEventListener('click', () => { if (!busy) files.click(); });
  library.querySelector('[data-upload]').addEventListener('click', () => { if (!busy) files.click(); });
  files.addEventListener('change', () => upload([...files.files]));
  document.querySelector(image?'#chooseReferences':`#${kind}ChooseReferences`).addEventListener('click', () => { library.showModal(); renderLibrary(); load(); });
  function cancelVoice(){if(kind==='audio'){voiceTarget=null;syncFileInput();}}
  library.querySelectorAll('[data-close]').forEach(node => node.addEventListener('click', () => {cancelVoice();library.close();}));
  library.addEventListener('cancel',cancelVoice);
  // Native close events may be queued until after the picker has reopened.
  library.addEventListener('close',()=>{if(!library.open)cancelVoice();});
  more.addEventListener('click', () => load(true));
  field.addEventListener('input', render);
  function describe(url, index) {
    if (metadata.has(url)) return metadata.get(url).name;
    try { const parsed = new URL(url); if (parsed.origin === location.origin && parsed.pathname.startsWith('/references/')) return `Library reference ${index + 1}`; } catch {}
    return url;
  }
  remote=window.ProviderReferenceLibrary?.mount({kind,library,field,metadata,more,message,urls,write,select,renderSelected:render,loadLocal:()=>load()});
  return {render,validate,describe,reset,values:urls,info:url=>metadata.get(url),open(){library.showModal();render();load();},has:url=>metadata.has(url)};
 }
 for(const kind of ["image","video","audio"])pickers.push(picker(kind));
 window.referencePicker={configure,get(kind,index){const p=pickers[['image','video','audio'].indexOf(kind)];const url=p?.values()[index];return url?{url,name:p.describe(url,index),...p.info(url)}:null;},restoreMode(options){
   const model=selectedModel(),modes=model?.reference_modes||[];
   let value=options.reference_mode;
   if(!value){
     const images=options.image_urls?.length||0,videos=options.video_urls?.length||(options.video_url?1:0);
     const audios=options.audio_urls?.length||(options.audio_url?1:0);
     value=modes.find(m=>images>=m.min_images&&images<=m.max_images&&videos>=m.min_videos&&videos<=m.max_videos&&audios>=m.min_audios&&audios<=m.max_audios&&(!m.max_visuals||images+videos<=m.max_visuals)&&images+videos>=(m.min_visuals||0)&&(!m.requires_visual||images+videos>0||!audios))?.id;

   }
   modeSelect.value=modes.some(item=>item.id===value)?value:modes[0]?.id||'';
 },voices(){if(!mode()?.supports_reference_voice)return {};const visible=[...document.querySelector('#imageURLs').value.split('\n').slice(mode()?.voice_image_start||0),...document.querySelector('#videoURL').value.split('\n')];return Object.fromEntries(Object.entries(voices).filter(([url])=>visible.includes(url)));},restoreVoices(value){voices=value||{};},render(){configure();pickers.forEach(p=>p.render());},validate(){return pickers.every(p=>p.validate());},describe(url,index){return (pickers.find(p=>p.has(url))||pickers[0]).describe(url,index);},reset(){voices={};voiceTarget=null;pickers.forEach(p=>p.reset());}};
 window.referencePicker.render();
})();
