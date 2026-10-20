// Hash incrementally off the UI thread; memory use is bounded independently of file size.
importScripts('/ui/sha256-vendor.js?v=0.11.1');
self.onmessage=async({data:file})=>{
 try{
  const hash=self.sha256.create(),chunkSize=1024*1024;
  for(let offset=0;offset<file.size;offset+=chunkSize)hash.update(await file.slice(offset,offset+chunkSize).arrayBuffer());
  self.postMessage({sha256:hash.hex()});
 }catch(error){self.postMessage({error:error.message||'Could not read the file.'});}
};
