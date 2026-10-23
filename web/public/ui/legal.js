(() => {
 'use strict';
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
 const titles={privacy:'Privacy Policy',terms:'Terms of Use'};
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
  const group=el('div',undefined,'settings-form-group');group.append(el('h3','Public legal documents'));parent.append(group);
  for(const [kind,title] of Object.entries(titles)){
   const open=el('button','Edit '+title,'secondary');open.type='button';group.append(open);
   open.onclick=async()=>{
    if(open.disabled)return;open.disabled=true;
    const dialog=el('dialog',undefined,'keys-revoke-dialog sales-dialog legal-editor'),form=el('form'),status=el('p'),actions=el('div',undefined,'dialog-actions'),save=el('button','Save','primary'),cancel=el('button','Cancel','secondary'),reset=el('button','Use default template','secondary'),text=el('textarea'),label=el('label',title),help=el('p','Plain text only; line breaks are preserved. HTML and links are displayed as text. Up to 64 KiB. Saving publishes immediately. An empty document uses the bundled template.','field-explanation');
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
     text.oninput=()=>{const tooLarge=new TextEncoder().encode(text.value).length>65536;text.setCustomValidity(tooLarge?'Document must be at most 64 KiB.':'');FormUI.message(status,text.value!==initial?'Unsaved changes. Cancel or Close discards this draft.':'');FormUI.update(form)};
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
