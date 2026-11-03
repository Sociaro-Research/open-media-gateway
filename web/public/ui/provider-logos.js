(() => {
  const limit = 1048576, signature = [137,80,78,71,13,10,26,10];
  const renders = new WeakMap(), uploads = new Map();
  let request = async (url, options) => {
    const response = await fetch(url, {...options, credentials:'same-origin'});
    if (!response.ok) throw new Error('Provider logo is unavailable');
    return response.blob();
  };

  // Retain authorized, decoded resources only in this tab. Versioned URLs and
  // the caller's access epoch distinguish account, key and resource contexts.
  const images=new Map(), maxEntries=32, maxBytes=8*1024*1024, maxPixels=16*1024*1024, lifetime=5*60*1000;
  const waiting=[];let activeReads=0;
  let accessContext=()=> 'session';
  const contextFor=url=>String(accessContext(url));
  function schedule(read){
    return new Promise((resolve,reject)=>{waiting.push({read,resolve,reject});drain();});
  }
  function drain(){
    while(activeReads<4&&waiting.length){
      const {read,resolve,reject}=waiting.shift();activeReads++;
      Promise.resolve().then(read).then(resolve,reject).finally(()=>{activeReads--;drain();});
    }
  }
  function release(entry,invalidated=true){
    if(images.get(entry.key)===entry)images.delete(entry.key);
    entry.invalidated=invalidated;
    if(invalidated)entry.controller.abort();
    if(entry.objectURL){const url=entry.objectURL;entry.objectURL=null;if(invalidated)URL.revokeObjectURL(url);else setTimeout(()=>URL.revokeObjectURL(url),0);}
  }
  function trim(){
    let ready=[...images.values()].filter(entry=>entry.ready),bytes=ready.reduce((n,e)=>n+e.bytes,0),pixels=ready.reduce((n,e)=>n+e.pixels,0);
    while(ready.length>maxEntries||bytes>maxBytes||pixels>maxPixels){const entry=ready.shift();bytes-=entry.bytes;pixels-=entry.pixels;release(entry,false);}
  }
  function clear(){
    for(const entry of [...images.values()])release(entry);
    for(const mark of document.querySelectorAll('[data-provider-logo-url]')){
      renders.set(mark,{});mark.textContent=mark.dataset.logoInitial||'?';mark.dataset.logoState='initial';mark.title='';
    }
  }
  function prune(){
    for(const entry of [...images.values()])if(entry.context!==contextFor(entry.source))release(entry);
    for(const mark of document.querySelectorAll('[data-provider-logo-url]')){
      if(mark.dataset.logoAccessContext!==undefined&&mark.dataset.logoAccessContext!==contextFor(mark.dataset.providerLogoUrl)){
        renders.set(mark,{});mark.textContent=mark.dataset.logoInitial||'?';mark.dataset.logoState='initial';
      }
    }
  }
  function resource(source){
    const context=contextFor(source),key=context+'\n'+new URL(source,location.origin).href;
    let entry=images.get(key);
    if(entry?.ready&&Date.now()-entry.loadedAt>lifetime){release(entry);entry=null;}
    if(entry){images.delete(key);images.set(key,entry);return entry;}
    entry={key,context,source,controller:new AbortController(),invalidated:false,ready:false};images.set(key,entry);
    entry.promise=schedule(async()=>{
      if(entry.invalidated||context!==contextFor(source))throw new DOMException('Logo context changed','AbortError');
      const blob=await request(source,{responseType:'blob',cache:'no-store',signal:entry.controller.signal,providerLogoGeneration:new URL(source,location.origin).searchParams.get('generation')==='1'});
      if(entry.invalidated||images.get(key)!==entry||context!==contextFor(source))throw new DOMException('Logo context changed','AbortError');
      entry.objectURL=URL.createObjectURL(blob);const decoded=new Image();decoded.src=entry.objectURL;await decoded.decode();
      if(entry.invalidated||images.get(key)!==entry||context!==contextFor(source))throw new DOMException('Logo context changed','AbortError');
      entry.image=decoded;entry.bytes=blob.size;entry.pixels=decoded.naturalWidth*decoded.naturalHeight;entry.loadedAt=Date.now();entry.ready=true;trim();return entry;
    }).catch(error=>{release(entry);throw error;});
    return entry;
  }
  function image(mark, provider) {
    const initial=[...(provider.name||provider.id||'?').trim()][0]?.toUpperCase()||'?',render={};renders.set(mark,render);
    mark.dataset.logoInitial=initial;mark.dataset.providerLogoUrl=provider.icon_url||provider.logoIdentity||'';
    mark.dataset.logoPresent=String(!!provider.icon_url);
    mark.dataset.logoAccessContext=contextFor(mark.dataset.providerLogoUrl);mark.title='';
    if(!provider.icon_url){mark.textContent=initial;mark.dataset.logoState='initial';return;}
    const entry=resource(provider.icon_url);
    const current=()=>renders.get(mark)===render&&!entry.invalidated&&entry.context===contextFor(provider.icon_url);
    const display=()=>{
      if(!current())return;
      const icon=entry.image.cloneNode();icon.alt='';icon.className='provider-logo';icon.dataset.logoUrl=provider.icon_url;
      icon.addEventListener('error',()=>{if(current()){release(entry);mark.textContent=initial;mark.dataset.logoState='error';mark.title='Logo unavailable. Reopen the picker to retry.';}},{once:true});
      mark.replaceChildren(icon);mark.dataset.logoState='ready';
    };
    // A warm render is synchronous: no intermediate initial or fresh blob URL.
    if(entry.ready){display();return;}
    mark.textContent=initial;mark.dataset.logoState='loading';
    entry.promise.then(()=>{if(mark.isConnected)display();}).catch(error=>{if(renders.get(mark)===render&&entry.context===contextFor(provider.icon_url)&&error.name!=='AbortError'){mark.dataset.logoState='error';mark.title='Logo unavailable. Reopen the picker to retry.';}});
  }
  function refresh(iconURL, removed=false) {
    const confirmed=new URL(iconURL,location.origin),version=confirmed.searchParams.get('v');
    for(const entry of [...images.values()]){
      const source=new URL(entry.source,location.origin);
      // Serving returns the current bytes even for an older v query. A confirmed
      // mutation must invalidate every variant, including an equal content hash.
      if(source.origin===confirmed.origin&&source.pathname===confirmed.pathname)release(entry);
    }
    for(const mark of document.querySelectorAll('[data-provider-logo-url]')){
      if(!mark.dataset.providerLogoUrl)continue;
      const shown=new URL(mark.dataset.providerLogoUrl,location.origin);
      if(shown.origin!==confirmed.origin||shown.pathname!==confirmed.pathname)continue;
      shown.searchParams.set('v',version);
      image(mark,{name:mark.dataset.logoInitial,icon_url:removed?'':shown.pathname+shown.search,logoIdentity:shown.pathname+shown.search});
    }
  }
  window.addEventListener('pagehide',clear);
  window.addEventListener('pageshow',event=>{
    if(!event.persisted)return;
    prune();
    for(const mark of document.querySelectorAll('[data-logo-present="true"]')){
      if(mark.dataset.logoAccessContext===contextFor(mark.dataset.providerLogoUrl))image(mark,{name:mark.dataset.logoInitial,icon_url:mark.dataset.providerLogoUrl});
    }
  });

  async function validate(file) {
    if (file.size > limit) throw new Error('Logo must be at most '+ReferenceUpload.formatLimit(limit)+' ('+FileSize.exact(limit)+').');
    const bytes = new Uint8Array(await file.arrayBuffer());
    if (!/\.png$/i.test(file.name) || (file.type && file.type !== 'image/png') || !signature.every((b,i) => bytes[i] === b)) {
      throw new Error('Choose a PNG image. Other formats are unsupported.');
    }
    // Check IHDR before decoding, bounding the browser's decoded allocation.
    if (bytes.length < 33 || String.fromCharCode(...bytes.slice(12,16)) !== 'IHDR') throw new Error('Invalid PNG image. Choose a complete, valid PNG file.');
    const view = new DataView(bytes.buffer), width = view.getUint32(16), height = view.getUint32(20);
    if (!width || !height || width > 2048 || height > 2048) throw new Error('PNG dimensions must be between 1 and 2048 pixels per side.');
    const source = URL.createObjectURL(file), img = new Image();
    try {
      img.src = source;
      try { await img.decode(); } catch { throw new Error('Invalid PNG image. Choose a complete, valid PNG file.'); }
    } finally { URL.revokeObjectURL(source); }
  }

  function editor(host, provider, {api, current, saved, identity}) {
    const revision = {}; host.logoRevision = revision;
    const isCurrent = () => current() && host.isConnected && host.logoRevision===revision;
    host.replaceChildren(); host.className = 'provider-logo-editor';

    let operationState, intent='upload', shown=Symbol('initial');
    const upload=ReferenceUpload.create({
      label:'Provider logo', title:'No logo. Drop a PNG here',selectedTitle:provider.name+' logo',uploadLabel:'Upload Logo',kinds:['image'],onRemove:removeLogo,
      dimensions:'2048 × 2048 px max',maxBytes:()=>limit,
      limits:()=>({content_types:['image/png']}), locked:()=>uploads.has(identity)&&uploads.get(identity)!==operationState,canContinue:isCurrent, cancelable:()=>false,
      retainedMessage:()=>provider.icon_url?' Current logo retained.':' No logo installed.',
      onBusy:busy=>{
        if (busy) {
          operationState={kind:intent,message:intent==='remove'?'Removing logo…':'Checking PNG…',views:new Set([update]),done:false};
          uploads.set(identity,operationState);
        } else {
          operationState.done=true;uploads.delete(identity);intent='upload';
          if (!operationState.iconURL && operationState.iconURL!=='' && !operationState.error) operationState.message='Upload cancelled. Choose a file to retry.';
        }
        operationState.views.forEach(view=>view(operationState));
      },
      validate:async(file,signal)=>{
        try { await validate(file);signal.throwIfAborted();if(!isCurrent())throw new DOMException('Provider changed before upload','AbortError'); }
        catch(error){await failure(error);throw error;}
      },
      onFile:async(file,signal)=>{
        operationState.message='Uploading logo…';operationState.views.forEach(view=>view(operationState));
        try {
          const result=await api(`/v1/admin/providers/${encodeURIComponent(provider.id)}/logo`,{method:'PUT',headers:{'Content-Type':'image/png'},body:file,signal});
          saved(result.icon_url);operationState.iconURL=result.icon_url;operationState.message='Logo uploaded.';return 'Logo uploaded.';
        } catch(error){await failure(error,true);throw error;}
      }
    });
    const remove=upload.root.querySelector('[data-upload-remove]');remove.setAttribute('aria-label','Remove logo for '+provider.name);
    async function failure(error,sent=false){
      operationState.error=true;
      const retry=operationState.kind==='remove'?' Use Remove logo to retry.':' Choose a file to retry.';
      operationState.message=(error.name==='AbortError'?'Operation interrupted.':error.message)+retry;
      if(sent){
        try{const latest=await api(`/v1/admin/providers/${encodeURIComponent(provider.id)}`);operationState.iconURL=latest.icon_url||'';saved(operationState.iconURL);operationState.message+=' Current server logo is shown.';}
        catch{operationState.message+=' Result not confirmed; reopen provider settings to check.';}
      }
    }
    function renderPreview(){
      if(shown===provider.icon_url)return;shown=provider.icon_url;
      upload.setPreview(provider.icon_url?{name:'Current logo',type:'image/png'}:null);
      const preview=upload.root.querySelector('.template-reference-preview');if(!preview)return;preview.classList.add('provider-logo-preview');
      if(provider.icon_url){const mark=document.createElement('span');mark.className='provider-logo-mark';image(mark,provider);preview.querySelector('.provider-logo-mark')?.remove();preview.querySelector('.media-picker-media').prepend(mark);}
    }
    function update(state){
      if(!isCurrent())return;
      if(state?.iconURL!==undefined)provider.icon_url=state.iconURL;
      renderPreview();upload.sync();remove.disabled=!!state&&!state.done;
      host.setAttribute('aria-busy',String(!!state&&!state.done));
      if(state)upload.message(state.message,!!state.error);
    }
    function removeLogo(){
      if(uploads.has(identity)||!provider.icon_url||!isCurrent())return;
      upload.clearRetry();intent='remove';upload.run(async signal=>{
        try{await api(`/v1/admin/providers/${encodeURIComponent(provider.id)}/logo`,{method:'DELETE',signal});saved('');operationState.iconURL='';operationState.message='Logo removed.';return 'Logo removed.';}
        catch(error){await failure(error,true);throw error;}
      },'logo removal');
    };
    host.append(upload.root);
    const active=uploads.get(identity);if(active)active.views.add(update);update(active);
  }
  window.ProviderLogos = {image, refresh, validate, editor, clear, prune, configure: options => {clear();request=options.request;if(options.context)accessContext=options.context;}};
})();
