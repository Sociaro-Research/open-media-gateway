(() => {
 'use strict';
 const originalFetch=window.fetch.bind(window);
 let connection=null,flight=null;
 const localControl=path=>/^\/v1\/system\/remote-workspace(?:[/?]|$)/.test(path);
 const customer=path=>path.startsWith('/v1/')||['/media/','/references/','/template-media/','/provider-assets/'].some(prefix=>path.startsWith(prefix));
 async function initialize(){
  if(connection)return connection;
  if(flight)return flight;
  flight=(async()=>{
   const response=await originalFetch('/auth/company-connection',{credentials:'same-origin',cache:'no-store'});
   if([401,403,404].includes(response.status))return null;
   if(!response.ok)throw Error('Could not load the Sociaro connection. Try again.');
   const data=await response.json();
   if(data.connected===false)return null;
   const record=data.connection,origin=new URL(record?.origin);
   if(data.connected!==true||record?.access!=='company-api-v1'||!record.context||typeof data.key!=='string'||!data.key||origin.protocol!=='https:'||origin.origin!==record.origin)throw Error('Invalid Sociaro connection');
   connection=Object.freeze({record:Object.freeze(record),key:data.key,origin:origin.origin});return connection;
  })().finally(()=>{flight=null;});
  return flight;
 }
 window.fetch=async(input,options)=>{
  const current=connection;if(!current)return originalFetch(input,options);
  const source=input instanceof Request?input.url:String(input),url=new URL(source,location.href);
  if(url.origin!==location.origin&&url.origin!==current.origin)return originalFetch(input,options);
  if(!customer(url.pathname))return originalFetch(input,options);
  const request=new Request(input instanceof Request?input:url.href,options),headers=new Headers(request.headers);
  if(localControl(url.pathname)&&url.origin===location.origin){headers.delete('Authorization');headers.delete('X-API-Key');return originalFetch(new Request(request,{headers,credentials:'same-origin'}));}
  const explicit=headers.get('Authorization');
  if(headers.has('X-API-Key')||explicit&&explicit!=='Bearer '+current.key)throw Error('Use the connected company key in this interface.');
  headers.set('Authorization','Bearer '+current.key);headers.set('X-Company-Context',current.record.context);
  const target=location.origin+'/auth/company-api'+url.pathname+url.search;
  const init={method:request.method,headers,credentials:'same-origin',cache:'no-store',redirect:'error',signal:request.signal};
  // Materialize a Blob, not a streaming Request body: streaming uploads require
  // HTTP/2 in Chromium and are unavailable on ordinary HTTP/1 installations.
  if(!['GET','HEAD'].includes(request.method))init.body=await request.blob();
  const forwarded=new Request(target,init);
  if(connection!==current)throw new DOMException('Company connection changed','AbortError');
  const response=await originalFetch(forwarded);
  if(connection!==current)throw new DOMException('Company connection changed','AbortError');
  return response;
 };
 window.CompanyConnection={url:value=>connection&&typeof value==='string'&&/^\/(?:media|references|template-media)\//.test(value)?new URL(value,connection.origin).href:value,initialize,active:()=>!!connection,key:()=>connection?.key||'',record:()=>connection?.record||null};
})();
