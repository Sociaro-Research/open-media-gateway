(() => {
  const limit = 1048576, signature = [137,80,78,71,13,10,26,10];
  const renders = new WeakMap(), uploads = new Map();
  let request = async (url, options) => {
    const response = await fetch(url, {...options, credentials:'same-origin'});
    if (!response.ok) throw new Error('Provider logo is unavailable');
    return response.blob();
  };

  function image(mark, provider) {
    const initial = [...(provider.name || provider.id || '?').trim()][0]?.toUpperCase() || '?';
    const render = {}; renders.set(mark,render);
    mark.dataset.logoInitial = initial;
    mark.dataset.providerLogoUrl = provider.icon_url || '';
    mark.textContent = initial;
    if (!provider.icon_url) return;
    const icon = document.createElement('img');
    icon.alt = ''; icon.className = 'provider-logo'; icon.dataset.logoUrl = provider.icon_url;
    // Authenticated fetch also supports the console's API-key mode without
    // putting credentials in URLs or exposing private provider logos publicly.
    request(provider.icon_url, {responseType:'blob', cache:'no-store', providerLogoGeneration:new URL(provider.icon_url,location.origin).searchParams.get('generation')==='1'}).then(blob => {
      if (!mark.isConnected || renders.get(mark)!==render) return;
      const source = URL.createObjectURL(blob);
      icon.addEventListener('load', () => URL.revokeObjectURL(source), {once:true});
      icon.addEventListener('error', () => { URL.revokeObjectURL(source); if (renders.get(mark)===render) mark.textContent = initial; }, {once:true});
      icon.src = source; mark.replaceChildren(icon);
    }).catch(() => {});
  }

  function refresh(iconURL) {
    const confirmed = new URL(iconURL,location.origin);
    for (const mark of document.querySelectorAll('[data-provider-logo-url]')) {
      if (!mark.dataset.providerLogoUrl) continue;
      const shown = new URL(mark.dataset.providerLogoUrl,location.origin);
      if (shown.pathname!==confirmed.pathname) continue;
      // Retain each surface's generation/commercial authorization context.
      shown.searchParams.set('v',confirmed.searchParams.get('v'));
      image(mark,{name:mark.dataset.logoInitial,icon_url:shown.pathname+shown.search});
    }
  }

  // Canvas exposes 8-bit alpha and rounds near-opaque 16-bit samples to 255.
  // Read original alpha samples only for that case, after browser decoding.
  async function transparent16(bytes, width, height) {
    const channels = bytes[25]===6 ? 4 : bytes[25]===4 ? 2 : 0;
    if (bytes[24]!==16 || !channels) return false;
    const view = new DataView(bytes.buffer), chunks = [];
    for (let offset=8; offset+12<=bytes.length;) {
      const length=view.getUint32(offset), type=String.fromCharCode(...bytes.slice(offset+4,offset+8));
      if (offset+length+12>bytes.length) throw new Error('Invalid PNG image.');
      if (type==='IDAT') chunks.push(bytes.slice(offset+8,offset+8+length));
      offset+=length+12;
    }
    const reader=new Blob(chunks).stream().pipeThrough(new DecompressionStream('deflate')).getReader();
    const parts=[]; let size=0;
    try {
      while (true) {
        const {done,value}=await reader.read(); if (done) break;
        size+=value.length;
        if (size>width*height*channels*2+height*14+7) { await reader.cancel(); throw new Error('Invalid PNG decoded size.'); }
        parts.push(value);
      }
    } finally { reader.releaseLock(); }
    const raw=new Uint8Array(size); let position=0;
    for (const part of parts) { raw.set(part,position); position+=part.length; }
    position=0;
    const passes=bytes[28]===1?[[0,0,8,8],[4,0,8,8],[0,4,4,8],[2,0,4,4],[0,2,2,4],[1,0,2,2],[0,1,1,2]]:[[0,0,1,1]];
    const bpp=channels*2;
    for (const [x,y,dx,dy] of passes) {
      const w=Math.max(0,Math.ceil((width-x)/dx)), h=Math.max(0,Math.ceil((height-y)/dy));
      if (!w || !h) continue;
      let previous=new Uint8Array(w*bpp);
      for (let row=0; row<h; row++) {
        const filter=raw[position++], line=raw.slice(position,position+w*bpp); position+=w*bpp;
        if (line.length!==w*bpp || filter>4) throw new Error('Invalid PNG image.');
        for (let i=0; i<line.length; i++) {
          const left=i>=bpp?line[i-bpp]:0, up=previous[i], corner=i>=bpp?previous[i-bpp]:0;
          let predictor=0;
          if (filter===1) predictor=left;
          if (filter===2) predictor=up;
          if (filter===3) predictor=Math.floor((left+up)/2);
          if (filter===4) { const p=left+up-corner,a=Math.abs(p-left),b=Math.abs(p-up),c=Math.abs(p-corner); predictor=a<=b&&a<=c?left:b<=c?up:corner; }
          line[i]=(line[i]+predictor)&255;
        }
        for (let i=bpp-2; i<line.length; i+=bpp) if (line[i]!==255 || line[i+1]!==255) return true;
        previous=line;
      }
    }
    return false;
  }

  async function validate(file) {
    if (file.size > limit) throw new Error('Logo must be at most 1 MiB.');
    const bytes = new Uint8Array(await file.arrayBuffer());
    if (!/\.png$/i.test(file.name) || (file.type && file.type !== 'image/png') || !signature.every((b,i) => bytes[i] === b)) {
      throw new Error('Choose a PNG image with transparency. Other formats are unsupported.');
    }
    // Check IHDR before decoding, bounding the browser's decoded allocation.
    if (bytes.length < 33 || String.fromCharCode(...bytes.slice(12,16)) !== 'IHDR') throw new Error('Invalid PNG image. Choose a complete, valid PNG file.');
    const view = new DataView(bytes.buffer), width = view.getUint32(16), height = view.getUint32(20);
    if (!width || !height || width > 2048 || height > 2048) throw new Error('PNG dimensions must be between 1 and 2048 pixels per side.');
    const source = URL.createObjectURL(file), img = new Image();
    try {
      img.src = source;
      try { await img.decode(); } catch { throw new Error('Invalid PNG image. Choose a complete, valid PNG file.'); }
      const canvas = document.createElement('canvas'); canvas.width = width; canvas.height = height;
      const context = canvas.getContext('2d', {willReadFrequently:true}); context.drawImage(img,0,0);
      const pixels = context.getImageData(0,0,width,height).data;
      for (let i=3; i<pixels.length; i+=4) if (pixels[i]<255) return;
      if (await transparent16(bytes,width,height)) return;
      throw new Error('PNG must contain transparency. At least one pixel must be partially or fully transparent.');
    } finally { URL.revokeObjectURL(source); }
  }

  function editor(host, provider, {api, current, saved, identity}) {
    const revision = {}; host.logoRevision = revision;
    const isCurrent = () => current() && host.isConnected && host.logoRevision===revision;
    host.replaceChildren(); host.className = 'branding-asset branding-asset-inline provider-logo-editor';
    const preview = document.createElement('div'); preview.className = 'branding-image-preview';
    preview.setAttribute('aria-label', provider.name + ' logo');
    if (provider.icon_url) image(preview,provider); else preview.textContent = 'No logo';
    const details = document.createElement('div'); details.className = 'branding-asset-details';
    const title = document.createElement('h4'); title.textContent = 'Provider logo';
    const hint = document.createElement('p'); hint.className = 'muted';
    hint.textContent = 'PNG with transparency only · up to 1 MiB · maximum 2048 × 2048 px. Dark theme uses the original; light theme uses black.';
    const input = document.createElement('input'); input.type = 'file'; input.accept = 'image/png,.png';
    input.setAttribute('aria-label', 'Upload logo for ' + provider.name);
    const status = document.createElement('p'); status.setAttribute('role','status'); status.setAttribute('aria-live','polite');
    const update = state => {
      if (!isCurrent()) return;
      input.disabled = !!state && !state.done;
      if (input.disabled) host.setAttribute('aria-busy','true'); else host.removeAttribute('aria-busy');
      if (state?.iconURL) image(preview,{...provider,icon_url:state.iconURL});
      if (state) FormUI.message(status,state.message,state.type||'');
    };
    const active = uploads.get(identity);
    if (active) active.views.add(update);
    update(active);
    input.addEventListener('change', async () => {
      const file = input.files[0]; if (!file || uploads.has(identity)) return;
      const state = {message:'Checking PNG…',views:new Set([update]),done:false};
      uploads.set(identity,state);
      const notify = () => state.views.forEach(view=>view(state));
      notify();
      try {
        await validate(file);
        if (!isCurrent()) { state.message='Upload canceled before sending. Choose a file to retry.'; return; }
        state.message='Uploading logo…'; notify();
        const result = await api(`/v1/admin/providers/${encodeURIComponent(provider.id)}/logo`, {method:'PUT', headers:{'Content-Type':'image/png'}, body:file});
        saved(result.icon_url);
        state.iconURL=result.icon_url;state.message='Logo uploaded.';state.type='success';
      } catch (error) {
        state.message=`${error.message} Choose a file to retry.`;state.type='error';
      } finally {
        state.done=true;uploads.delete(identity);input.value='';notify();
      }
    });
    details.append(title,hint,input,status); host.append(preview,details);
  }
  window.ProviderLogos = {image, refresh, validate, editor, configure: options => {request = options.request;}};
})();
