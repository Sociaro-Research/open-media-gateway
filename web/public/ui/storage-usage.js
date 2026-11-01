(() => {
 const warning='Shared storage limit exceeded. Delete references or stored generation results before uploading files or starting another generation.';
 let current=null,sequence=0;
 function size(bytes){return FileSize.format(bytes)||'Unknown';}
 function render(){
  document.querySelectorAll('.reference-dropzone').forEach(zone=>{let note=zone.querySelector('[data-storage-quota]');if(!note){note=document.createElement('p');note.dataset.storageQuota='';note.className='reference-status error';zone.append(note);}note.textContent=current?.exceeded?warning:'';note.hidden=!current?.exceeded;zone.querySelectorAll('[data-upload],#uploadReference,#videoUploadReference,#audioUploadReference').forEach(button=>{button.disabled=!!current?.exceeded;});});
 }
 async function refresh(){const token=++sequence;if(!window.GenerationKey?.value()){current=null;render();return null;}const value=await api('/v1/storage/usage');if(token!==sequence)return null;current=value;render();return value;}
 async function check(){const value=await refresh();if(value?.exceeded)throw new Error(warning);if(value?.applies&&value.limit_bytes!=null&&value.unknown_objects>0)throw new Error('Storage usage is being verified. Try again shortly.');}
 window.StorageQuota={refresh,check,size};
 window.addEventListener('gateway-key-change',()=>refresh().catch(()=>{}));
 window.addEventListener('gateway-storage-change',()=>refresh().catch(()=>{}));
})();
