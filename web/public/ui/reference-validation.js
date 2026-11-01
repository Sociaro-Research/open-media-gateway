// Shared upload/selection contract. Success is shown only after server inspection.
window.ReferenceValidation=(()=>{
 function target(provider,model,options={}){
  if(!provider||!model)return null;
  const clean=structuredClone(options);for(const key of ['image_urls','video_urls','video_url','audio_urls','audio_url','reference_voices'])delete clean[key];
  const reference_ids=ids([...(options.image_urls||[]),...(options.video_urls||(options.video_url?[options.video_url]:[])),...(options.audio_urls||(options.audio_url?[options.audio_url]:[]))]);
  return {provider,model,options:clean,reference_ids};
 }
 const signature=value=>JSON.stringify(value?{...value,reference_ids:undefined}:null);
 function ids(values){return values.map(raw=>{try{const u=new URL(raw,location.origin);if(u.origin!==location.origin)return null;return /^\/references\/[^/]+\/([^/]+)$/.exec(u.pathname)?.[1]||null;}catch{return null;}}).filter(Boolean);}
 async function check(referenceIDs,validation,options={}){
  if(!referenceIDs.length)return;
  await api('/v1/references/validate',{method:'POST',body:JSON.stringify({validation:validation?{...validation,reference_ids:validation.role==='reference_voice'?validation.reference_ids:[]}:null,reference_ids:referenceIDs}),...options});
 }
 async function upload(file,{validation=null,signal,expectedKey,onProgress=()=>{}}){
  signal.throwIfAborted();onProgress(`Preparing ${file.name}…`);
  const sha256=await hashReferenceFile(file,signal);
  const plan=await api('/v1/references/uploads',{method:'POST',body:JSON.stringify({name:file.name,content_type:file.type,size_bytes:file.size,sha256,...(validation?{validation}:{})}),signal,expectedKey});
  signal.throwIfAborted();
  if(plan.mode==='existing')return plan.reference;
  if(plan.mode==='local'){
   onProgress(`Uploading and checking ${file.name}…`);const body=new FormData();body.append('file',file);if(validation)body.append('validation',JSON.stringify(validation));
   return api('/v1/references',{method:'POST',body,signal,expectedKey});
  }
  if(plan.mode!=='s3')throw Error('Unsupported upload method. Retry the file.');
  onProgress(`Uploading ${file.name}…`);let response;
  try{response=await fetch(plan.target.url,{method:'PUT',headers:plan.target.headers,body:file,credentials:'omit',redirect:'error',signal});}
  catch(e){if(e.name==='AbortError')throw e;throw Error('Could not upload directly to storage. Check your connection and the bucket CORS settings.');}
  if(!response.ok)throw Error(`Storage rejected the upload (HTTP ${response.status}). Retry the file.`);
  signal.throwIfAborted();onProgress(`Checking ${file.name}…`);
  return api(`/v1/references/uploads/${encodeURIComponent(plan.id)}/complete`,{method:'POST',signal,expectedKey});
 }
 return {target,signature,ids,check,upload};
})();
