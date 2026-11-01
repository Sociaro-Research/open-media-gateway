(() => {
 'use strict';
 const node=(tag,text='',cls='')=>{const n=document.createElement(tag);n.textContent=text;if(cls)n.className=cls;return n;};
 const button=(text,label,action)=>{const n=node('button',text,'secondary');n.type='button';n.setAttribute('aria-label',label);n.onclick=action;return n;};
 const size=value=>value==null?'':value<1024?`${value} B`:value<1024*1024?`${(value/1024).toFixed(1).replace(/\.0$/,'')} KB`:`${(value/1024/1024).toFixed(1).replace(/\.0$/,'')} MB`;
 function stop(root){for(const media of root.querySelectorAll('video,audio')){media.pause();media.removeAttribute('src');media.load();}}
 // Transport, authorization and slot validation belong to the caller. This is
 // the single Playground/library presentation, shared by every reference scope.
 function create({id='',title='Reference library',description='',onClose}){
  const dialog=node('dialog','','reference-library');if(id)dialog.id=id;
  const header=node('div','','reference-library-header'),heading=node('h2',title);
  heading.id=(id||'reference-library-'+(++create.sequence))+'Title';dialog.setAttribute('aria-labelledby',heading.id);
  header.append(heading,button('✕','Close library',()=>onClose?onClose():dialog.close()));header.lastChild.dataset.close='';
  const intro=node('p',description),status=node('p','','reference-status'),grid=node('div','','reference-library-grid'),footer=node('div','','reference-library-footer'),more=button('Load more','Load more',()=>{});
  status.dataset.status='';status.setAttribute('role','status');grid.dataset.grid='';more.dataset.more='';more.hidden=true;footer.append(more);dialog.append(header,intro,status,grid,footer);
  dialog.addEventListener('close',()=>{if(!dialog.open)stop(dialog);});
  function render(items,{selected=()=>false,onSelect,selectionLabel=item=>'Use '+item.name+' in generation',busy=false,onDelete,decorate}={}){
   stop(grid);
   grid.replaceChildren(...items.map(item=>{
    const card=node('div','','reference-card'),chosen=!!selected(item),name=item.name;
    card.dataset.id=item.id;card.dataset.libraryReference=item.id;card.dataset.selected=String(chosen);
    const choose=onSelect?button('',selectionLabel(item),()=>onSelect(item)):node('div');choose.className='reference-choice';if(onSelect){choose.setAttribute('aria-pressed',String(chosen));choose.disabled=busy||!!item.deleting;}
    const kind=(item.content_type||'image/png').split('/')[0],visual=node(kind==='image'?'img':kind==='video'?'video':'div');
    if(kind==='image'||kind==='video'){visual.src=item.url;visual.setAttribute('aria-label',name);visual.draggable=false;visual.referrerPolicy='no-referrer';if(kind==='image'){visual.alt=name;visual.loading='lazy';}else{visual.preload='metadata';visual.muted=true;visual.playsInline=true;}
     visual.onerror=()=>{visual.removeAttribute('src');visual.hidden=true;if(!choose.querySelector('.reference-placeholder'))choose.prepend(node('span','Preview unavailable.','reference-placeholder'));};
    }else{visual.className='reference-placeholder';visual.textContent='Audio';}
    const label=node('span','','reference-name');label.title=name;label.append(node('span',name),node('small',size(item.size_bytes),'reference-size'));choose.append(visual,label);card.append(choose);
    // Native playback is outside the selection button, so playing media never
    // assigns a slot or nests interactive controls inside another control.
    if(kind==='video'||kind==='audio'){
     const play=button('▶','Play '+name,event=>{event.stopPropagation();
      let player=card.querySelector('[data-player]');
      if(!player){player=node(kind);player.dataset.player='';player.src=item.url;player.controls=true;player.preload='metadata';player.playsInline=true;player.tabIndex=0;player.setAttribute('aria-label','Preview '+name);
       const recover=()=>{const focused=document.activeElement===player;player.pause();player.removeAttribute('src');player.load();player.remove();card.classList.remove('reference-media-playing');play.hidden=false;play.textContent='↻';play.setAttribute('aria-label','Retry preview '+name);play.title='Retry preview '+name;if(focused&&dialog.open)play.focus();};player.onerror=recover;player.recover=recover;card.append(player);
      }
      card.classList.add('reference-media-playing');play.hidden=true;player.focus({preventScroll:true});player.play().catch(error=>{if(error.name!=='AbortError'&&player.isConnected)player.recover();});
     });play.innerHTML='<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path fill="currentColor" d="M8 5v14l11-7z"/></svg>';play.classList.add('reference-play');play.title='Play '+name;play.disabled=busy||!!item.deleting;card.append(play);
    }else{const retry=button('Retry preview','Retry preview',()=>{choose.querySelector('.reference-placeholder')?.remove();visual.hidden=false;visual.src=item.url;retry.hidden=true;});retry.classList.add('reference-play');retry.textContent='↻';retry.title='Retry preview';retry.hidden=true;visual.addEventListener('error',()=>{retry.hidden=false;});card.append(retry);}
    if(onDelete){const remove=button('Delete','Delete '+name+' from library',()=>{if(card.querySelector('.reference-delete-warning'))return;const warning=node('p','Delete this file permanently? Pending generations using it may fail.','reference-delete-warning'),actions=node('div','','reference-card-actions');actions.append(button('Cancel','Keep '+name,()=>{warning.remove();actions.remove();remove.disabled=busy;remove.focus();}),button('Delete file','Confirm delete '+name,()=>onDelete(item)));remove.disabled=true;card.append(warning,actions);});remove.classList.add('reference-delete');remove.disabled=busy;card.append(remove);}
    if(item.deleting)card.append(node('p','Deletion pending. An administrator can retry deletion.','reference-status'));
    decorate?.(card,item);return card;
   }));
  }
  return {dialog,intro,status,grid,more,render,stop:()=>stop(dialog)};
 }
 create.sequence=0;
 window.ReferenceLibrary={create,deleted(item,scope){window.dispatchEvent(new CustomEvent('gateway-reference-deleted',{detail:{id:item.id,url:item.url,scope,keyVersion:generationKeyVersion}}));}};
})();
