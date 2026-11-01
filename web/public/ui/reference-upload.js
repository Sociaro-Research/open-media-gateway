// File selection UI shared by typed template slots and cover media. Transport,
// authorization and durable bindings stay with the caller and existing API.
(() => {
 'use strict';
 let sequence=0;
 const types={image:['image/png','image/jpeg','image/webp','image/gif'],video:['video/mp4','video/quicktime','video/webm'],audio:['audio/mpeg','audio/wave','audio/wav','audio/x-wav','audio/ogg','audio/flac']};
 const equivalent=type=>['audio/wav','audio/x-wav'].includes(type)?'audio/wave':type;
 const el=(tag,text='',cls='')=>{const node=document.createElement(tag);node.textContent=text;node.className=cls;return node;};
 function create(config){
  const id='referenceUpload'+(++sequence),root=el('div','','reference-dropzone'),input=el('input'),title=el('p',config.title),actions=el('div','','reference-actions'),hint=el('small'),status=el('p','','reference-status'),preview=el('div','','reference-card template-reference-preview');
  root.setAttribute('role','group');root.setAttribute('aria-label',config.label);root.dataset.referenceUpload='';
  input.type='file';input.hidden=true;input.setAttribute('aria-label',config.label+' file');
  input.accept=config.kinds.flatMap(kind=>types[kind]).join(',');
  hint.id=id+'Hint';status.id=id+'Status';status.setAttribute('role','status');status.setAttribute('aria-live','polite');
  const button=(text,fn)=>{const node=el('button',text,'secondary');node.type='button';node.onclick=fn;return node;};
  const upload=button(config.uploadLabel,()=>{if(!busy){returnFocus=true;input.click();}});upload.dataset.uploadPicker='';upload.setAttribute('aria-label',config.uploadLabel+' for '+config.label);upload.setAttribute('aria-describedby',hint.id);
  const library=config.onLibrary?button('Choose from library',()=>{if(!busy)config.onLibrary(api);}):null;
  const cancel=button('Cancel upload',()=>operation?.abort());cancel.dataset.uploadCancel='';cancel.hidden=true;
  const retry=button('Retry upload',()=>{returnFocus=true;if(lastFile)select([lastFile]);});retry.hidden=true;
  actions.append(upload);if(library)actions.append(library);actions.append(cancel,retry);
  preview.hidden=true;root.append(preview,title,actions,hint,status,input);
  let busy=false,operation=null,lastFile=null,depth=0,returnFocus=false,assignedName='';
  const retained=()=>assignedName?' Current assignment remains '+assignedName+'.':' No new reference assigned.';
  function limits(kind){const shared={image:30,video:200,audio:50}[kind]*1024*1024,rule=config.limits?.(kind)||{};return {shared,rule};}
  function message(text='',error=false){status.textContent=text;status.classList.toggle('error',error);}
  function sync(){const locked=busy||!!config.locked?.();upload.disabled=locked;if(library)library.disabled=locked;retry.disabled=locked;cancel.disabled=!busy;cancel.hidden=!busy;root.setAttribute('aria-busy',String(busy));input.accept=config.kinds.flatMap(kind=>types[kind].filter(type=>{const allowed=limits(kind).rule.content_types;return !allowed?.length||allowed.some(item=>equivalent(item)===equivalent(type));})).join(',');hint.textContent=config.hint||config.kinds.map(kind=>{const {shared,rule}=limits(kind);return kind+' · '+(rule.max_bytes&&rule.max_bytes-1<shared?((rule.max_bytes-1)/1_000_000)+' MB':(shared/1024/1024)+' MiB')+' maximum';}).join(' · ');}
  function error(file){
   const kind=config.kinds.find(kind=>types[kind].includes(file.type));
   if(!kind)return file.name+': format '+(file.type||'unknown')+'; use a supported '+config.kinds.join(' or ')+' file ('+config.kinds.flatMap(kind=>types[kind]).join(', ')+').';
   const {shared,rule}=limits(kind);
   if(!file.size||file.size>shared||rule.max_bytes&&file.size>=rule.max_bytes)return file.name+': size '+file.size+' bytes; use a nonempty '+kind+' file '+(rule.max_bytes&&rule.max_bytes<=shared?'smaller than '+rule.max_bytes:'at most '+shared)+' bytes. Reduce the file size.';
   if(rule.content_types?.length&&!rule.content_types.some(type=>equivalent(type)===equivalent(file.type)))return file.name+': format '+file.type+'; this model accepts '+rule.content_types.join(', ')+'. Export in a supported format.';
   return '';
  }
  async function run(work,name){
   if(busy||config.locked?.()||config.signal?.aborted)return;
   const requestedFocus=returnFocus||root.contains(document.activeElement);returnFocus=false;operation=new AbortController();const abort=()=>operation?.abort();config.signal?.addEventListener('abort',abort,{once:true});
   busy=true;retry.hidden=true;message('Uploading '+name+'…');config.onBusy?.(true);sync();
   try{const result=await work(operation.signal);operation.signal.throwIfAborted();if(!config.signal?.aborted){message(typeof result==='string'?result:'Ready');lastFile=null;}}
   catch(e){if(!config.signal?.aborted){message((e.name==='AbortError'?'Upload cancelled. Choose the file again or retry.':e.message)+retained(),true);retry.hidden=!lastFile;}}
   finally{config.signal?.removeEventListener('abort',abort);operation=null;busy=false;const restoreFocus=requestedFocus||root.contains(document.activeElement);config.onBusy?.(false,{restoreFocus});sync();if(restoreFocus&&root.isConnected&&!config.signal?.aborted&&!upload.disabled)upload.focus({preventScroll:true});}
  }
  function select(files){if(busy||config.locked?.()||!files.length)return;retry.hidden=true;lastFile=null;if(files.length!==1){message('Choose one file for '+config.label+'.',true);return;}const file=files[0],problem=error(file);input.value='';if(problem){message(problem+retained(),true);return;}lastFile=file;return run(signal=>config.onFile(file,signal),file.name);}
  input.onchange=()=>select([...input.files]);
  root.onclick=e=>{if(!e.target.closest('button,input,video,audio')&&!busy&&!config.locked?.()){returnFocus=root.contains(document.activeElement);input.click();}};
  root.ondragenter=e=>{if(e.dataTransfer?.types.includes('Files')){e.preventDefault();depth++;if(!busy&&!config.locked?.())root.classList.add('dragover');}};
  root.ondragover=e=>{if(e.dataTransfer?.types.includes('Files')){e.preventDefault();e.dataTransfer.dropEffect=busy||config.locked?.()?'none':'copy';}};
  root.ondragleave=e=>{e.preventDefault();if(--depth<=0){depth=0;root.classList.remove('dragover');}};
  root.ondrop=e=>{e.preventDefault();depth=0;root.classList.remove('dragover');select([...e.dataTransfer.files]);};
  function setPreview(item){
   for(const node of preview.querySelectorAll('video,audio')){node.pause();node.removeAttribute('src');node.load();}
   assignedName=item?.name||'';preview.replaceChildren();preview.hidden=!item;if(!item)return;
   const name=el('span',item.name||config.label,'reference-name');
   if(item.url&&/^(image|video|audio)\//.test(item.type||'')){
    const kind=item.type.split('/')[0],asset=el(kind==='image'?'img':kind);asset.setAttribute('aria-label',item.name||config.label);
    if(kind==='image')asset.alt=item.name||config.label;else{asset.controls=true;asset.preload='metadata';if(kind==='video')asset.playsInline=true;}
    asset.src=item.url;asset.onerror=()=>{asset.remove();preview.prepend(el('div','Preview unavailable. The saved reference is still assigned.','reference-placeholder'));};preview.append(asset);
   }else preview.append(el('div',item.type?.split('/')[0]||'Reference','reference-placeholder'));
   preview.append(name);
  }
  function setLabel(label){config.label=label;root.setAttribute('aria-label',label);upload.setAttribute('aria-label',config.uploadLabel+' for '+label);}
  const dispose=()=>{operation?.abort();setPreview(null);};config.signal?.addEventListener('abort',dispose,{once:true});
  const api={root,input,message,sync,setPreview,run,error,setLabel,dispose};sync();return api;
 }
 window.ReferenceUpload={create};
})();
