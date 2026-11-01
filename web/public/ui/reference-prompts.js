// Shared UI reference addresses. Recommendations never select the grammar.
// Native capabilities still validate media; prompt text is never rewritten.
(() => {
 'use strict';
 const kinds=['image','video','audio'];
 const syntax='ui-reference-v1';
 function contract(model,options={}){
  if(!model||model.no_prompt)return null;
  const mode=model.reference_modes?.find(m=>m.id===options.reference_mode),types=kinds.filter(kind=>mode?mode['max_'+kind+'s']>0:model['supports_'+kind+'_input']||model.reference_prompt_types?.includes(kind));
  return {syntax,types};
 }
 function label(_syntax,kind,index){return '@'+kind[0].toUpperCase()+kind.slice(1)+(index+1);}
 function markers(value){
  const word='Image|Picture|Video|Audio',number='[+-]?\\d+(?:\\.\\d+)?';
  const pattern=new RegExp('([<\\[{])('+word+')[ _]?('+number+')([>\\]}])|@('+word+')_?('+number+')','giu'),text=String(value||''),out=[];
  for(const m of text.matchAll(pattern)){
   const start=m.index,end=start+m[0].length,previous=[...text.slice(0,start)].at(-1)||'',following=[...text.slice(end)][0]||'';
   if(previous&&!/[\p{White_Space}('"“«:;,.!?>\]}]/u.test(previous)||following&&/[\p{L}\p{N}_>\]}@]/u.test(following))continue;
   if(m[1]&&({'<':'>','[':']','{':'}'}[m[1]]!==m[4]))continue;
   const name=(m[2]||m[5]).toLowerCase(),kind=name==='picture'?'image':name,raw=m[3]||m[6],n=Number(raw),valid=/^[1-9]\d*$/.test(raw)&&Number.isSafeInteger(n)&&n>0;
   out.push({kind,index:n-1,valid,label:m[0],start,end});
  }
  return out;
 }
 function urls(options,kind){
  if(kind==='image')return options.image_urls||[];
  return options[kind+'_urls']||(options[kind+'_url']?[options[kind+'_url']]:[]);
 }
 function offset(options,kind){return kind==='image'&&options.reference_mode==='first_frame_references'?1:0;}
 function inspect(value,model,options={}){
  const spec=contract(model,options);if(!spec)return [];
  return markers(value).map(marker=>({...marker,reason:!marker.valid?'invalid':!spec.types.includes(marker.kind)?'unsupported':!urls(options,marker.kind)[marker.index+offset(options,marker.kind)]?'missing':''}));
 }
 function error(value,model,options={}){
  const spec=contract(model,options);if(!spec)return '';
  const invalid=inspect(value,model,options).filter(m=>m.reason);
  if(!invalid.length)return '';
  const malformed=invalid.filter(m=>m.reason==='invalid');if(malformed.length)return 'Invalid reference: '+[...new Set(malformed.map(m=>m.label))].join(', ')+'. Use the displayed reference identifier and a positive whole number.';
  const unsupported=invalid.filter(m=>m.reason==='unsupported');if(unsupported.length)return 'Unsupported reference type: '+[...new Set(unsupported.map(m=>m.label))].join(', ')+'. This model accepts '+(spec.types.join(', ')||'no')+' references.';
  const unique=[...new Map(invalid.map(m=>[m.kind+':'+m.index,m])).values()].sort((a,b)=>kinds.indexOf(a.kind)-kinds.indexOf(b.kind)||a.index-b.index);
  const imageOnly=unique.every(m=>m.kind==='image'),labels=unique.map(m=>label(spec.syntax,m.kind,m.index));
  return `Missing ${imageOnly?'image references':'references'}: ${labels.join(', ')}. Add the ${imageOnly?'images':'files'} or correct the prompt before generating.`;
 }
 const api={syntax,contract,label,markers,urls,offset,inspect,error};
 if(typeof window!=='undefined')window.ReferencePrompts=api;
 if(typeof module!=='undefined'&&module.exports)module.exports=api;
})();
