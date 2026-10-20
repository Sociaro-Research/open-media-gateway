(() => {
 'use strict';
 window.InvoiceUI={expired:p=>!p.settled&&(p.status==='expired'||(p.expires_at&&new Date(p.expires_at)<=new Date())),available:p=>p.status!=='cancelled'&&(!!p.invoice?.pdf_url||!InvoiceUI.expired(p)),payment(p,base,{api,status,current,onPaid}){
 const b=document.createElement('button');b.type='button';b.className='secondary';b.textContent=p.invoice_mode==='local'?'Pay invoice':'Continue payment';b.onclick=async()=>{if(b.disabled)return;b.disabled=true;try{const result=await api(base+'/payments/'+p.id+'/checkout',{method:'POST'});if(!current())return;if(result.settled){FormUI.message(status,'This invoice has already been paid.');if(onPaid)await onPaid()}else if(result.checkout_url){location.assign(result.checkout_url)}else{FormUI.message(status,'This payment cannot be opened right now. Try again later.','error')}}catch(e){if(current())FormUI.message(status,e.message,'error')}finally{b.disabled=false}};return b;
 },link(p,base,{api,status,current}){
  const local=p.invoice_mode==='local',a=document.createElement(local?'button':'a');a.textContent='Invoice';a.className=local?'secondary invoice-download':'invoice-download';a.title=p.invoice?.number?'Download invoice '+p.invoice.number:'Generate and download invoice PDF';
  if(!local){a.href=p.invoice.pdf_url;a.target='_blank';a.rel='noopener noreferrer';return a}
  a.type='button';a.onclick=async()=>{if(a.disabled)return;a.disabled=true;try{const blob=await api(base+'/payments/'+p.id+'/invoice',{responseType:'blob'});if(!current())return;const url=URL.createObjectURL(blob),download=document.createElement('a');download.href=url;download.download=(p.invoice?.number||'invoice-'+p.id)+'.pdf';document.body.append(download);download.click();download.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);FormUI.message(status,'')}catch(e){if(current())FormUI.message(status,e.message,'error')}finally{a.disabled=false}};return a;
 }};
})();
