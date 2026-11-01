/* The native textarea remains the editor and source of submitted text. A mirror
   paints reference markers only; it never rewrites prompt text or adds HTML. */
(() => {
 'use strict';
 let sequence=0;
 function attach(input,config){
 const id=config.id||'promptReference'+(++sequence),life=new AbortController();
 const listen=(target,name,fn)=>target?.addEventListener(name,fn,{signal:life.signal});
 const model=()=>config.model(),options=()=>config.options()||{},provider=()=>config.provider?.();
 const warning=document.createElement('p');warning.id=id+'Error';warning.className='form-message error';warning.setAttribute('aria-live','polite');warning.hidden=true;(config.warningAfter||input.closest('.floating-field')||input).after(warning);
 const providerHint=document.createElement('p');providerHint.className='field-hint reference-recommendation';providerHint.id=id+'Recommendation';providerHint.hidden=true;warning.before(providerHint);
 function recommendation(){const m=model(),r=m?.reference_prompt_recommendation,mode=options().reference_mode;const show=!m?.no_prompt&&r&&['example','instruction'].includes(r.kind)&&typeof r.text==='string'&&r.text.trim()&&(!r.modes?.length||r.modes.includes(mode));providerHint.hidden=!show;providerHint.textContent=show?(r.kind==='example'?'Provider API example: ':'Provider API recommendation: ')+r.text+' Prompt is sent unchanged.':'';}
 const mirror=document.createElement('div'),text=document.createElement('div');mirror.className='prompt-reference-mirror';mirror.setAttribute('aria-hidden','true');text.className='prompt-reference-text';mirror.append(text);
 let parent,tokens=[],popup=null,active=null,composing=false,scheduled=false,lastSignature='',pinnedOpen=false;
 const geometry=['fontFamily','fontSize','fontWeight','fontStyle','lineHeight','letterSpacing','textIndent','textAlign','tabSize','paddingTop','paddingBottom','paddingLeft','paddingRight','wordBreak','overflowWrap'];
 const safeURL=value=>{if(typeof value!=='string'||!value.trim())return '';try{const u=new URL(value,location.origin);return ((['http:','https:'].includes(u.protocol)&&!u.username&&!u.password)||(config.allowBlob&&u.protocol==='blob:'&&u.origin===location.origin))?u.href:'';}catch{return '';}};
 function enabled(){return !!window.ReferencePrompts?.contract(model(),options())&&!input.disabled&&!composing;}
 function hide(){if(popup){if(popup.matches(':popover-open'))popup.hidePopover();popup.remove();}popup=null;active=null;pinnedOpen=false;}
 function sync(){text.style.transform=`translate(${-input.scrollLeft}px,${-input.scrollTop}px)`;}
 function refresh(){
  scheduled=false;if(life.signal.aborted)return;recommendation();if(input.parentElement!==parent||mirror.parentElement!==input.parentElement){parent=input.parentElement;parent.classList.add('prompt-reference-container');parent.append(mirror);}
  const show=enabled();input.classList.toggle('prompt-reference-input',show);mirror.hidden=!show;if(!show){tokens=[];lastSignature='';check();hide();return;}
  const css=getComputedStyle(input);for(const name of geometry)text.style[name]=css[name];text.style.width=input.clientWidth+'px';mirror.style.left=(input.offsetLeft+input.clientLeft)+'px';mirror.style.top=(input.offsetTop+input.clientTop)+'px';mirror.style.width=input.clientWidth+'px';mirror.style.height=input.clientHeight+'px';text.style.color=css.getPropertyValue('--text')||'inherit';
  const signature=JSON.stringify([model()?.reference_prompt_syntax,model()?.reference_prompt_types,model()?.reference_prompt_recommendation,model()?.reference_modes,options().reference_mode,provider()?.id,input.value,input.clientWidth,input.clientHeight,input.offsetTop,input.offsetLeft,css.font,css.lineHeight,options(),...['image','video','audio'].flatMap(kind=>ReferencePrompts.urls(options(),kind).map((_,index)=>reference(kind,index)))]);if(signature===lastSignature){sync();return;}lastSignature=signature;
  hide();text.replaceChildren();tokens=[];const value=input.value;let last=0;
  for(const {start,end,index,label,kind,reason} of markers()){text.append(document.createTextNode(value.slice(last,start)));const span=document.createElement('span');span.className='prompt-reference-token';span.textContent=label;span.dataset.referenceIndex=String(index);span.dataset.referenceKind=kind;const item=reference(kind,index);span.classList.toggle('missing',!!reason||!item);text.append(span);tokens.push({start,end,index,kind,span});last=end;}
  text.append(document.createTextNode(value.slice(last)+(value.endsWith('\n')?'\n':'')));sync();if(active){const next=tokens.find(t=>t.start===active.start&&t.end===active.end);if(!next)hide();}
 }
 function markers(){return ReferencePrompts.inspect(input.value,model(),options());}
 function check(){
  const length=FormUI.updatePrompt(input,model()),reference=enabled()?(config.message?.()??ReferencePrompts.error(input.value,model(),options())):'',message=length.error||reference;
  input.setCustomValidity(config.validate===false?length.error:message);if(warning.textContent!==message)warning.textContent=message;warning.hidden=!message;return !message;
 }
 function requestError(request,model){return FormUI.promptState(request.prompt,model).error||ReferencePrompts.error(request.prompt,model,request.options||{});}
 function validate(){const valid=check();if(!valid){input.focus();input.reportValidity();}return valid;}
 function requestRefresh(){if(life.signal.aborted)return;check();if(!scheduled){scheduled=true;requestAnimationFrame(refresh);}}
 function reference(kind,index){
  if(config.reference)return config.reference(kind,index);
  const values=options(),offset=ReferencePrompts.offset(values,kind),url=ReferencePrompts.urls(values,kind)[index+offset];
  if(!url)return null;
  const item=window.referencePicker?.get(kind,index+offset)||{url,name:kind+' '+(index+1)};
  return item?.connection_id&&item.connection_id!==provider()?.id?null:item;
 }
 function at(x,y){const box=input.getBoundingClientRect();if(x<box.left||x>box.right||y<box.top||y>box.bottom)return null;for(const token of tokens){const range=document.createRange();range.selectNodeContents(token.span);if([...range.getClientRects()].some(r=>x>=r.left&&x<=r.right&&y>=r.top&&y<=r.bottom))return token;}return null;}
 function show(token,x,y,pinned=false){
  if(active===token&&popup){position(x,y);return;}
  hide();active=token;pinnedOpen=pinned;const item=reference(token.kind,token.index);popup=document.createElement('aside');popup.className='model-info-popup reference-preview-popup';popup.setAttribute('popover','manual');popup.setAttribute('role','tooltip');popup.id=id+'Preview';
  const title=document.createElement('strong');title.textContent=token.span.textContent;popup.append(title);
  const caption=document.createElement('p');caption.textContent=item?item.name||`${token.kind} ${token.index+1}`:`No ${token.kind} ${token.index+1} is selected.`;popup.append(caption);
  const preview=item?.url?.startsWith('asset://')?item.preview_url:item?.url;const src=safeURL(preview);
  const panel=popup;
  if(item&&src){const img=document.createElement(token.kind==='image'?'img':token.kind);if(token.kind==='image')img.alt=`Preview of image ${token.index+1}`;else{img.controls=true;img.preload='none';}img.referrerPolicy='no-referrer';img.onload=()=>{if(popup===panel)position(x,y);};img.onerror=()=>{img.remove();const error=document.createElement('p');error.textContent='Preview unavailable.';if(popup===panel)panel.append(error);};img.src=src;popup.append(img);}else if(item){const unavailable=document.createElement('p');unavailable.textContent=item.url?.startsWith('asset://')?'Open the BytePlus library to load this registered asset’s preview.':'Preview unavailable.';popup.append(unavailable);}
  if(pinned){const close=document.createElement('button');close.type='button';close.className='model-info-close icon-close';close.textContent='×';close.setAttribute('aria-label','Close reference preview');close.onclick=hide;popup.append(close);}
  document.body.append(popup);if(popup.showPopover)popup.showPopover();position(x,y);
 }
 function position(x,y){if(!popup)return;const rect=popup.getBoundingClientRect();const left=Math.max(8,Math.min(x+14,innerWidth-rect.width-8));const top=y+16+rect.height<innerHeight?y+16:Math.max(8,y-rect.height-12);popup.style.left=left+'px';popup.style.top=top+'px';}
 listen(input,'input',()=>{hide();requestRefresh();});listen(input,'scroll',()=>{sync();hide();});listen(input,'compositionstart',()=>{composing=true;refresh();});listen(input,'compositionend',()=>{composing=false;requestRefresh();});
 listen(input,'pointermove',event=>{if(event.pointerType==='touch'||pinnedOpen||!enabled())return;const token=at(event.clientX,event.clientY);input.classList.toggle('reference-hover',!!token);if(token)show(token,event.clientX,event.clientY);else hide();});
 listen(input,'pointerleave',event=>{if(event.pointerType!=='touch'&&!pinnedOpen)hide();});
 listen(input,'pointerdown',event=>{if(event.pointerType!=='touch'||!enabled())return;const token=at(event.clientX,event.clientY);if(token){event.preventDefault();if(active===token)hide();else show(token,event.clientX,event.clientY,true);}});
 listen(input,'keydown',event=>{if(event.key==='Escape'){hide();return;}if(event.altKey&&event.key==='ArrowDown'){const token=tokens.find(t=>input.selectionStart>=t.start&&input.selectionStart<=t.end);if(token){event.preventDefault();const r=token.span.getBoundingClientRect();show(token,r.left,r.bottom,true);}}});
 listen(document,'pointerdown',event=>{if(event.target!==input&&!popup?.contains(event.target))hide();});listen(window,'resize',()=>{hide();requestRefresh();});
 for(const [target,event] of config.events||[])listen(target,event,requestRefresh);
 const observer=new ResizeObserver(requestRefresh);observer.observe(input);
 const mutations=new MutationObserver(requestRefresh);mutations.observe(input,{attributes:true,attributeFilter:['style','disabled','class']});
 const structure=new MutationObserver(()=>{if(input.isConnected&&mirror.parentElement!==input.parentElement)requestRefresh();});structure.observe(input.closest('form'),{childList:true,subtree:true});
 const hint=document.createElement('span');hint.className='sr-only';hint.id=id+'Help';hint.textContent='For supported reference models, hover or tap a reference for a preview. With the caret in a reference, press Alt plus Down Arrow. Escape closes the preview.';input.after(hint);input.setAttribute('aria-describedby',[input.getAttribute('aria-describedby'),hint.id,providerHint.id,warning.id].filter(Boolean).join(' '));
 function destroy(){hide();life.abort();observer.disconnect();mutations.disconnect();structure.disconnect();mirror.remove();warning.remove();providerHint.remove();hint.remove();input.classList.remove('prompt-reference-input','reference-hover');if(config.validate!==false)input.setCustomValidity('');}
 config.signal?.addEventListener('abort',destroy,{once:true});
 const api={refresh:requestRefresh,close:hide,validate,requestError,destroy};requestRefresh();return api;
 }
 window.PromptReferenceEditor={attach};
})();
