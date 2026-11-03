(() => {
 'use strict';
 const node=(tag,text='',cls='')=>{const n=document.createElement(tag);n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,label,action)=>{const n=node('button',text,'secondary');n.type='button';n.setAttribute('aria-label',label);n.onclick=action;return n;};
 function stop(root){ReferenceUpload.stop(root);}
 // Callers own authorization and durable deletion; this controller only arms a
 // compact confirmation and preserves the completed-gesture/focus boundary.
 function deleteConfirmation(card,{allowed,name,from,onConfirm}){
  const control=card.removeButton;
  function reset(){if(card.dataset.confirmDelete!=='true')return;delete card.dataset.confirmDelete;control.querySelector('.media-picker-action-label').textContent='Delete';control.setAttribute('aria-label','Delete '+name()+' from '+from);}
  control.addEventListener('blur',reset);
  // WebKit can blur the focused target on another press of that same button.
  control.addEventListener('mousedown',event=>{if(card.dataset.confirmDelete==='true')event.preventDefault();});
  function activate(){
   if(!card.isConnected||!allowed()){reset();return;}
   if(card.dataset.confirmDelete==='true'){reset();onConfirm();return;}
   card.dataset.confirmDelete='true';control.querySelector('.media-picker-action-label').textContent='Confirm Delete';control.setAttribute('aria-label','Confirm delete '+name());control.focus({preventScroll:true});
  }
  return {activate,reset};
 }
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
     card=ReferenceUpload.mediaCard(item,{library:true,onSelect:onSelect?value=>{if(!disposed)onSelect(value);}:null,selectionLabel:selectionLabel(item),onRemove:onDelete?()=>{for(const other of cards.values())if(other!==card)other.deleteConfirmation?.reset();card.deleteConfirmation.activate();}:null,removeLabel:'Delete'});card.dataset.id=item.id;card.dataset.libraryReference=item.id;cards.set(key,card);
     if(card.removeButton)card.deleteConfirmation=deleteConfirmation(card,{name:()=>card.context.item.name,from:'library',allowed:()=>!disposed&&!card.context.options.busy&&!card.context.item.deleting&&card.context.options.canDelete?.(card.context.item)!==false,onConfirm:()=>card.context.options.onDelete?.(card.context.item)});
     decorate?.(card,item);
    }
    card.context={item,options};card.update(item,{selected:selected(item),busy,onSelect:onSelect?value=>{if(!disposed)onSelect(value);}:null,selectionLabel:selectionLabel(item),canDelete:options.canDelete?.(item)!==false,deleteReason:options.deleteReason?.(item)});
    if(busy||item.deleting||options.canDelete?.(item)===false)card.deleteConfirmation?.reset();
    nodes.push(card);
   }
   for(const [key,card]of cards)if(!keys.has(key)){stop(card);cards.delete(key);}
   ReferenceUpload.reconcile(grid,nodes);
  }
  function resetConfirmations(){for(const card of cards.values())card.deleteConfirmation?.reset();}
  return {dialog,intro,status,grid,more,render,stop:()=>{resetConfirmations();for(const media of dialog.querySelectorAll('video,audio'))media.pause();},dispose:()=>{disposed=true;stop(dialog);unobserve();cards.clear();dialog.close();dialog.remove();}};
 }
 create.sequence=0;
 window.ReferenceLibrary={create,stop,deleteConfirmation,deleted(item,scope){window.dispatchEvent(new CustomEvent('gateway-reference-deleted',{detail:{id:item.id,url:item.url,scope,keyVersion:generationKeyVersion}}));}};
})();
