// Bounded cursor responses for existing synthetic project fixtures. Their
// arrays define fixture order; real timestamp/UUID ordering is covered in Go
// and journal-pagination.cjs. Never use this helper with live application data.
const {createHash}=require('node:crypto');
module.exports.generationPage=(url,all)=>{
 const limit=Number(url.searchParams.get('limit')||50);
 const decode=value=>value?JSON.parse(Buffer.from(value,'base64url').toString()).id:null;
 const before=decode(url.searchParams.get('cursor')),through=decode(url.searchParams.get('through'));
 const start=before?all.findIndex(t=>t.id===before)+1:Number(url.searchParams.get('offset')||0);
 const end=through?all.findIndex(t=>t.id===through)+1:all.length;
 let range=all.slice(start,end>0?end:all.length);const ids=url.searchParams.get('ids')?.split(',');if(ids)range=range.filter(t=>ids.includes(t.id));const data=structuredClone(range.slice(0,limit)).map(t=>({...t,journal_revision:createHash('sha256').update(JSON.stringify(t)).digest('base64url')}));
 return {journal_scope:'fixture-scope',data:url.searchParams.get('view')==='versions'?data.map(t=>({id:t.id,journal_revision:t.journal_revision})):data,...(url.searchParams.get('include_total')==='false'?{}:{total:all.length}),has_more:range.length>limit,next_offset:start+data.length,next_cursor:data.length?Buffer.from(JSON.stringify({id:data.at(-1).id})).toString('base64url'):null};
};

// Adapt only legacy synthetic list fixtures. Preserve real page fixtures,
// error responses and every non-journal/mutation response unchanged.
module.exports.generationFixtureResponse=(request,body,status=200)=>{
 const method=typeof request.method==='function'?request.method():request.method;
 const raw=typeof request.url==='function'?request.url():request.url;
 const url=new URL(raw,'http://fixture');
 if(status!==200||method!=='GET'||url.pathname!=='/v1/generations'||!Array.isArray(body?.data)||body.error||['total','journal_scope','next_cursor','next_offset','has_more'].some(key=>Object.hasOwn(body,key)))return body;
 return {...body,...module.exports.generationPage(url,body.data)};
};
