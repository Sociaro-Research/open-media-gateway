// Shared post-generation audio control. Uploads use the existing media transport.
window.OverrideSound=(()=>{
 function create(config){
  const section=document.createElement('section');section.dataset.overrideSound='';
  const heading=document.createElement('h3');heading.textContent='Override Sound';section.append(heading);
  const hint=document.createElement('p');hint.className='field-hint';hint.textContent='Replace the video’s original sound. Longer audio is trimmed; shorter audio leaves silence. Video quality and duration stay unchanged.';section.append(hint);
  let value=config.value||'',url='',busy=false;
  const widget=ReferenceUpload.create({label:'Override Sound',title:'Drop an audio file here',uploadLabel:'Upload',kinds:['audio'],signal:config.signal,locked:()=>Boolean(config.locked?.()),onRemove:()=>clear(),onBusy:b=>{busy=b;sync();config.onBusy?.(b);},onFile:async(file,signal)=>{
   const result=await config.upload(file,signal);signal.throwIfAborted();
   if(!result.content_type?.startsWith('audio/'))throw Error('Choose a supported audio file.');
   if(url)URL.revokeObjectURL(url);url=URL.createObjectURL(file);value=result.id;
   widget.setPreview({name:result.name||file.name,type:result.content_type,url});config.onChange?.(value);sync();return 'Override sound ready: '+(result.name||file.name);
  }});
  function sync(){widget.sync();}
  function restore(id){clear();value=id||'';if(value)widget.setPreview({name:'Saved override sound',type:'audio/unknown'});sync();}
  function clear(){widget.dispose();widget.clearRetry();widget.message('');if(url)URL.revokeObjectURL(url);url='';value='';widget.setPreview(null);config.onChange?.('');sync();}
  section.append(widget.root);
  if(value){widget.setPreview({name:config.name||'Saved override sound',type:'audio/unknown'});if(config.load){const initial=value;Promise.resolve().then(()=>config.load(initial)).then(item=>{if(item&&value===initial&&!config.signal?.aborted)widget.setPreview(item);}).catch(()=>{if(value===initial&&!config.signal?.aborted)widget.message('Saved override sound is assigned. Preview unavailable.',true);});}}
  config.signal?.addEventListener('abort',()=>{if(url)URL.revokeObjectURL(url);},{once:true});sync();
  return {root:section,get value(){return value;},get busy(){return busy;},sync,clear,restore,dispose:()=>{widget.dispose();if(url)URL.revokeObjectURL(url);}};
 }
 return {create};
})();
