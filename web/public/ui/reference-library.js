(() => {
 'use strict';
 const node=(tag,text='',cls='')=>{const n=document.createElement(tag);n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,label,action)=>{const n=node('button',text,'secondary');n.type='button';n.setAttribute('aria-label',label);n.onclick=action;return n;};
 function stop(root){ReferenceUpload.stop(root);}
 // Scope, permissions, validation and durable deletion remain caller-owned.
 function create({id='',title='Reference library',description='',onClose}){
  const dialog=node('dialog','','reference-library');if(id)dialog.id=id;
  const header=node('div','','reference-library-header'),heading=node('h2',title);heading.id=(id||'reference-library-'+(++create.sequence))+'Title';dialog.setAttribute('aria-labelledby',heading.id);
  const close=()=>{resetConfirmations();onClose?onClose():dialog.close();};header.append(heading,button('×','Close library',close));header.lastChild.dataset.close='';header.lastChild.classList.add('icon-close');
  const intro=node('p',description),status=node('p','','reference-status'),grid=node('div','','reference-library-grid'),footer=node('div','','reference-library-footer'),more=button('Load more','Load more',()=>{});intro.hidden=!description;
  status.dataset.status='';status.setAttribute('role','status');grid.dataset.grid='';more.dataset.more='';more.hidden=true;footer.append(more);dialog.append(header,intro,status,grid,footer);
  const cards=new Map(),unobserve=ReferenceUpload.observeRow(grid);let disposed=false;
  dialog.addEventListener('click',event=>{if(event.target!==dialog)return;const r=dialog.getBoundingClientRect();if(event.clientX<r.left||event.clientX>r.right||event.clientY<r.top||event.clientY>r.bottom)close();});
  dialog.addEventListener('cancel',resetConfirmations);
  dialog.addEventListener('close',()=>{resetConfirmations();if(!dialog.open)for(const media of dialog.querySelectorAll('video,audio'))media.pause();});
  function render(items,options={}){
   if(disposed)return;
   const {selected=()=>false,onSelect,selectionLabel=item=>'Use '+item.name+' in generation',busy=false,onDelete,decorate,scope=''}=options,nodes=[],keys=new Set();
   for(const item of items){
    const key=JSON.stringify([scope,item.id,item.content_type,!!onSelect,!!onDelete]);keys.add(key);let card=cards.get(key);
    if(!card){
     card=ReferenceUpload.mediaCard(item,{library:true,onSelect:onSelect?value=>{if(!disposed)onSelect(value);}:null,selectionLabel:selectionLabel(item),onRemove:onDelete?()=>confirm(card):null,removeLabel:'Delete'});card.dataset.id=item.id;card.dataset.libraryReference=item.id;cards.set(key,card);
     // WebKit may blur a focused button on its next mouse press; this is still
     // the same activation target, not a focus move to another control.
     card.removeButton?.addEventListener('mousedown',event=>{if(card.dataset.confirmDelete==='true')event.preventDefault();});
     card.removeButton?.addEventListener('blur',()=>resetConfirmation(card));
     decorate?.(card,item);
    }
    card.context={item,options};card.update(item,{selected:selected(item),busy,onSelect:onSelect?value=>{if(!disposed)onSelect(value);}:null,selectionLabel:selectionLabel(item),canDelete:options.canDelete?.(item)!==false,deleteReason:options.deleteReason?.(item)});
    if(busy||item.deleting||options.canDelete?.(item)===false)resetConfirmation(card);
    nodes.push(card);
   }
   for(const [key,card]of cards)if(!keys.has(key)){stop(card);cards.delete(key);}
   ReferenceUpload.reconcile(grid,nodes);
  }
  function resetConfirmation(card){
   if(!card.removeButton||card.dataset.confirmDelete!=='true')return;delete card.dataset.confirmDelete;
   card.removeButton.querySelector('.media-picker-action-label').textContent='Delete';
   card.removeButton.setAttribute('aria-label','Delete '+card.context.item.name+' from library');
  }
  function resetConfirmations(){for(const card of cards.values())resetConfirmation(card);}
  function confirm(card){
   const {item,options}=card.context;if(disposed||options.busy||item.deleting||options.canDelete?.(item)===false)return;
   if(card.dataset.confirmDelete==='true'){
    resetConfirmation(card);options.onDelete?.(item);return;
   }
   resetConfirmations();card.dataset.confirmDelete='true';
   card.removeButton.querySelector('.media-picker-action-label').textContent='Confirm Delete';
   card.removeButton.setAttribute('aria-label','Confirm delete '+item.name);
   card.removeButton.focus({preventScroll:true});
  }
  return {dialog,intro,status,grid,more,render,stop:()=>{resetConfirmations();for(const media of dialog.querySelectorAll('video,audio'))media.pause();},dispose:()=>{disposed=true;stop(dialog);unobserve();cards.clear();dialog.close();dialog.remove();}};
 }
 create.sequence=0;
 window.ReferenceLibrary={create,stop,deleted(item,scope){window.dispatchEvent(new CustomEvent('gateway-reference-deleted',{detail:{id:item.id,url:item.url,scope,keyVersion:generationKeyVersion}}));}};
})();
