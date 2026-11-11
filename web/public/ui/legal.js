(() => {
 'use strict';
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
 const titles={privacy:'Privacy Policy',terms:'Terms of Use'};
 const invalidFooterLine=/[\p{Cc}\u2028\u2029]/u;
 function footerValue(value){
  if(!value||typeof value!=='object'||Array.isArray(value)||typeof value.copyright_holder!=='string'||!Number.isSafeInteger(value.version)||value.version<1||new TextEncoder().encode(value.copyright_holder).length>200||invalidFooterLine.test(value.copyright_holder))return null;
  return {copyright_holder:value.copyright_holder,version:value.version};
 }
 function invalidFooterResponse(){const error=Error('Invalid footer settings response');error.code='invalid_footer_response';return error}
 function mountFooter(parent,api,current){
  const form=el('form',undefined,'member-limit-form profile-field legal-footer-settings'),input=el('input'),save=el('button','Save','secondary'),status=el('small');
  input.type='text';input.setAttribute('aria-label','Copyright holder');input.maxLength=200;input.placeholder='[Your company name]';input.dataset.floatingLabel='always';input.autocomplete='organization';input.disabled=true;save.type='submit';save.hidden=true;status.role='status';form.append(input,save,status);parent.append(form);
  let data,busy=false,watched=false,loading=true;const active=()=>current()&&form.isConnected,read=()=>input.value.trim();
  const validate=()=>{
   const bytes=new TextEncoder().encode(input.value).length;
   input.setCustomValidity(bytes>200?'Copyright holder is '+bytes+' UTF-8 bytes; the limit is 200 bytes. Shorten the text before saving.':invalidFooterLine.test(input.value)?'Remove control characters and line separators; the copyright holder must be a single line.':'');FormUI.update(form);
  };input.addEventListener('input',validate);
  async function load(retain=false,uncertain=false){
   let responseValid=false;loading=true;try{const next=footerValue(await api('/v1/system/legal/footer'));if(!active())return;if(!next)throw invalidFooterResponse();responseValid=true;data=next;loading=false;if(!retain)input.value=next.copyright_holder;input.disabled=false;save.disabled=false;
    if(!watched){FormUI.watch(form,save,{read,valid:()=>!busy&&!loading&&FormUI.valid(form)});watched=true}else FormUI.commit(form,next.copyright_holder);
    window.GatewaySite?.applyLegalFooter(next);
    const checked=uncertain?(read()===next.copyright_holder?'Saved footer matches your draft. No further Save is needed.':'Saved footer settings were checked. Your draft is retained; choose Save only if you want to replace the current saved holder.'):'';
    FormUI.message(status,uncertain?checked:retain?'This footer changed elsewhere. Your draft is retained. Review it before saving again.':'',uncertain&&read()===next.copyright_holder?'success':retain?'error':'');validate();FormUI.refresh();
   }catch(e){
    if(!active())return;loading=true;input.disabled=true;save.disabled=true;const draft=retain?' Your draft is retained.':'';
    if(responseValid){FormUI.message(status,'The copyright holder loaded, but the editor could not initialize. Reload this page; if this continues, ask an Administrator to check the UI and server versions.'+draft,'error');return}
    if(e.status===401){FormUI.message(status,'Sign in again, then reopen Public legal documents to load the copyright holder.'+draft,'error');return}
    if(e.status===403){FormUI.message(status,'Administrator access is required. Ask an Administrator to restore access, then reopen Public legal documents.'+draft,'error');return}
    if(e.status===429){FormUI.message(status,'The server is limiting requests. Wait, then reopen Public legal documents.'+draft,'error');return}
    if(e.code==='invalid_footer_response'||e.status>=400&&e.status<500&&e.status!==408){FormUI.message(status,'Could not load the copyright holder. Ask an Administrator to check footer access, the server version and logs, then reload.'+draft,'error');return}
    FormUI.message(status,'Could not load the copyright holder. Retrying automatically… If this continues, ask an Administrator to check the server logs.'+draft,'error');setTimeout(()=>{if(active())void load(retain,uncertain)},1500);
   }
  }
  form.onsubmit=async event=>{event.preventDefault();if(busy||loading||!data||read()===data.copyright_holder||!form.reportValidity())return;busy=true;input.disabled=save.disabled=true;const submitted=read();let confirmed=false;FormUI.message(status,'');
   try{const next=footerValue(await api('/v1/system/legal/footer',{method:'PUT',body:JSON.stringify({copyright_holder:submitted,version:data.version})}));if(!active())return;if(!next||next.copyright_holder!==submitted||next.version!==data.version+1)throw invalidFooterResponse();confirmed=true;data=next;input.value=next.copyright_holder;window.GatewaySite?.applyLegalFooter(next);FormUI.saved(form,next.copyright_holder)}
   catch(e){if(active()){if(e.status===409)await load(true);else if(!confirmed&&(e.code==='invalid_footer_response'||!e.status||e.status===408||e.status>=500||e.status>=200&&e.status<300)){
    loading=true;FormUI.message(status,'Save result could not be confirmed. The server may have accepted the change. Check saved settings before saving again; your draft is retained.','error');
    const check=el('button','Check saved footer','secondary compact');check.type='button';check.onclick=()=>{if(!active()||busy)return;check.disabled=true;void load(true,true)};const actions=el('span',undefined,'form-actions compact');actions.append(el('span',status.textContent),check);status.replaceChildren(actions);
   }else if(confirmed){loading=true;FormUI.message(status,'The footer was saved, but this view could not be updated. Reload this page before editing again.','error')}
   else if(e.status===401)FormUI.message(status,'Sign in again before saving this footer. Your draft is retained.','error');
   else if(e.status===403)FormUI.message(status,'Administrator access is required. Ask an Administrator to restore access before saving this draft.','error');
   else if(e.status===429)FormUI.message(status,'The server is limiting requests. Wait before saving this draft again.','error');
   else if(e.status===400)FormUI.message(status,e.message,'error');
   else FormUI.message(status,'Could not save the copyright holder. Ask an Administrator to check the server version and configuration before saving again.','error')}}
   finally{busy=false;if(active()){input.disabled=save.disabled=loading;FormUI.update(form)}}
  };
  void load();FormUI.refresh();
 }
 async function publicDocument(){
  const original=document.querySelector('[data-legal-default]');if(!original)return;
  const kind=original.dataset.legalDefault,message=el('p','Loading document…'),custom=el('div',undefined,'legal-text');
  original.hidden=true;original.after(message,custom);
  async function load(){
   message.replaceChildren(document.createTextNode('Loading document…'));
   try{const response=await fetch('/legal/'+kind,{credentials:'omit',cache:'no-store'});if(!response.ok)throw Error('Could not load this document.');const data=await response.json();if(typeof data.text!=='string')throw Error('Could not load this document.');
    original.hidden=!!data.text;custom.textContent=data.text;message.hidden=true;
   }catch(e){message.textContent='Could not load this document. ';const retry=el('button','Retry','secondary');retry.onclick=load;message.append(retry);}
  }await load();
 }
 window.LegalTextUI={mount(parent,api,current=()=>true){
  const group=el('div',undefined,'settings-form-group'),documentActions=el('div',undefined,'form-actions compact');group.append(el('h3','Public legal documents'));parent.append(group);mountFooter(group,api,current);group.append(documentActions);
  for(const [kind,title] of Object.entries(titles)){
   const open=el('button','Edit '+title,'secondary');open.type='button';documentActions.append(open);
   open.onclick=async()=>{
    if(open.disabled)return;open.disabled=true;
    const dialog=el('dialog',undefined,'keys-revoke-dialog sales-dialog legal-editor'),form=el('form'),status=el('p'),actions=el('div',undefined,'dialog-actions'),save=el('button','Save','primary'),cancel=el('button','Cancel','secondary'),reset=el('button','Use default template','secondary'),text=el('textarea'),label=el('label',title),help=el('p','Plain text only; line breaks are preserved. HTML and links are displayed as text. Up to '+FileSize.limit(65536)+'. Saving publishes immediately. An empty document uses the bundled template.','field-explanation');
    let busy=false,loaded=false,initial='',version;
    const dismiss=()=>{if(!busy)dialog.close();};
    const {close}=FormUI.dialogHeading(form,title,dismiss);dialog.setAttribute('aria-label',title);
    text.id='legal-editor-text';text.rows=16;text.maxLength=65536;label.htmlFor=text.id;save.type='submit';cancel.type=reset.type='button';save.disabled=true;text.disabled=true;reset.disabled=true;
    cancel.onclick=dismiss;reset.onclick=()=>{text.value='';text.dispatchEvent(new Event('input',{bubbles:true}));text.focus();};
    actions.append(cancel,save);form.append(help,label,text,reset,status,actions);dialog.append(form);document.body.append(dialog);
    dialog.addEventListener('cancel',e=>{e.preventDefault();dismiss()});
    const leaving=e=>{if(loaded&&text.value!==initial){e.preventDefault();e.returnValue='';}};
    window.addEventListener('beforeunload',leaving);
    dialog.onclose=()=>{window.removeEventListener('beforeunload',leaving);dialog.remove();open.disabled=false;open.focus();};
    dialog.showModal();close.focus();FormUI.message(status,'Loading…');
    try{
     const data=await api('/v1/system/legal/'+kind);if(!dialog.isConnected)return;if(!current()){dialog.close();return}
     version=data.version;initial=data.text;if(!initial){const response=await fetch('/'+kind,{credentials:'omit',cache:'no-store'});if(!response.ok)throw Error('Could not load the default template.');const document=new DOMParser().parseFromString(await response.text(),'text/html'),content=document.querySelector('[data-legal-default]');if(!content)throw Error('Could not load the default template.');initial=[...content.children].map(n=>n.textContent.trim()).filter(Boolean).join('\n\n');}if(!dialog.isConnected)return;text.value=initial;loaded=true;text.disabled=false;reset.disabled=false;FormUI.message(status,'');text.focus();
     FormUI.watch(form,save,{placement:'footer',read:()=>text.value,valid:()=>!busy&&new TextEncoder().encode(text.value).length<=65536});
     text.oninput=()=>{const tooLarge=new TextEncoder().encode(text.value).length>65536;text.setCustomValidity(tooLarge?'Document must be at most '+FileSize.limit(65536)+' ('+FileSize.exact(65536)+').':'');FormUI.message(status,text.value!==initial?'Unsaved changes. Cancel or Close discards this draft.':'');FormUI.update(form)};
     FormUI.refresh();
    }catch(e){if(dialog.isConnected)FormUI.message(status,e.message,'error')}
    form.onsubmit=async e=>{
     e.preventDefault();if(busy||!loaded||text.value===initial||!form.reportValidity())return;
     busy=true;for(const control of [save,cancel,reset,close,text])control.disabled=true;FormUI.message(status,'Saving…');
     try{await api('/v1/system/legal/'+kind,{method:'PUT',body:JSON.stringify({text:text.value,version})});initial=text.value;busy=false;dialog.close()}
     catch(e){if(dialog.isConnected)FormUI.message(status,e.status===409?'This document changed elsewhere. Copy your draft, close and reopen to review the latest text.':e.message,'error')}
     finally{busy=false;if(dialog.isConnected){for(const control of [save,cancel,reset,close,text])control.disabled=false;FormUI.update(form)}}
    };
   };
  }
 }};
 publicDocument();
})();
