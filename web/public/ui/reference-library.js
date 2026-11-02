(() => {
 'use strict';
 const node=(tag,text='',cls='')=>{const n=document.createElement(tag);n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,label,action)=>{const n=node('button',text,'secondary');n.type='button';n.setAttribute('aria-label',label);n.onclick=action;return n;};
 function stop(root){ReferenceUpload.stop(root);}
 // Scope, permissions, validation and durable deletion remain caller-owned.
 function create({id='',title='Reference library',description='',onClose}){
  const dialog=node('dialog','','reference-library');if(id)dialog.id=id;
  const header=node('div','','reference-library-header'),heading=node('h2',title);heading.id=(id||'reference-library-'+(++create.sequence))+'Title';dialog.setAttribute('aria-labelledby',heading.id);
  const close=()=>onClose?onClose():dialog.close();header.append(heading,button('×','Close library',close));header.lastChild.dataset.close='';header.lastChild.classList.add('icon-close');
  const intro=node('p',description),status=node('p','','reference-status'),grid=node('div','','reference-library-grid'),footer=node('div','','reference-library-footer'),more=button('Load more','Load more',()=>{});intro.hidden=!description;
  status.dataset.status='';status.setAttribute('role','status');grid.dataset.grid='';more.dataset.more='';more.hidden=true;footer.append(more);dialog.append(header,intro,status,grid,footer);
  const cards=new Map(),unobserve=ReferenceUpload.observeRow(grid);let disposed=false;
  dialog.addEventListener('click',event=>{if(event.target!==dialog)return;const r=dialog.getBoundingClientRect();if(event.clientX<r.left||event.clientX>r.right||event.clientY<r.top||event.clientY>r.bottom)close();});
  dialog.addEventListener('close',()=>{if(!dialog.open)for(const media of dialog.querySelectorAll('video,audio'))media.pause();});
  function render(items,options={}){
   if(disposed)return;
   const {selected=()=>false,onSelect,selectionLabel=item=>'Use '+item.name+' in generation',busy=false,onDelete,decorate,scope=''}=options,nodes=[],keys=new Set();
   for(const item of items){
    const key=JSON.stringify([scope,item.id,item.content_type,!!onSelect,!!onDelete]);keys.add(key);let card=cards.get(key);
    if(!card){
     card=ReferenceUpload.mediaCard(item,{library:true,onSelect:onSelect?value=>{if(!disposed)onSelect(value);}:null,selectionLabel:selectionLabel(item),onRemove:onDelete?()=>confirm(card):null,removeLabel:'Delete'});card.dataset.id=item.id;card.dataset.libraryReference=item.id;cards.set(key,card);
     decorate?.(card,item);
    }
    card.context={item,options};card.update(item,{selected:selected(item),busy,onSelect:onSelect?value=>{if(!disposed)onSelect(value);}:null,selectionLabel:selectionLabel(item),canDelete:options.canDelete?.(item)!==false,deleteReason:options.deleteReason?.(item)});
    nodes.push(card);
   }
   for(const [key,card]of cards)if(!keys.has(key)){stop(card);cards.delete(key);}
   ReferenceUpload.reconcile(grid,nodes);
  }
  function confirm(card){
   const {item,options}=card.context;if(disposed||options.busy||item.deleting||options.canDelete?.(item)===false||card.querySelector('.reference-delete-warning'))return;
   const warning=node('p','Delete this file permanently? Pending generations using it may fail.','reference-delete-warning'),actions=node('div','','reference-card-actions');
   const cancel=()=>{warning.remove();actions.remove();card.removeButton.disabled=!!card.context.options.busy;card.removeButton.focus();};
   actions.append(button('Cancel','Keep '+item.name,cancel),button('Delete file','Confirm delete '+item.name,()=>{const latest=card.context;if(disposed||latest.options.busy||latest.item.deleting||latest.options.canDelete?.(latest.item)===false)return;latest.options.onDelete?.(latest.item);}));card.removeButton.disabled=true;card.append(warning,actions);actions.firstChild.focus();
  }
  return {dialog,intro,status,grid,more,render,stop:()=>{for(const media of dialog.querySelectorAll('video,audio'))media.pause();},dispose:()=>{disposed=true;stop(dialog);unobserve();cards.clear();dialog.close();dialog.remove();}};
 }
 create.sequence=0;
 window.ReferenceLibrary={create,stop,deleted(item,scope){window.dispatchEvent(new CustomEvent('gateway-reference-deleted',{detail:{id:item.id,url:item.url,scope,keyVersion:generationKeyVersion}}));}};
})();
