/* The native textarea remains the editor and source of submitted text. A mirror
   paints reference markers only; it never rewrites prompt text or adds HTML. */
(() => {
 'use strict';
 const input=document.querySelector('#prompt');if(!input)return;
 const warning=document.createElement('p');warning.id='promptReferenceError';warning.className='form-message error';warning.setAttribute('aria-live','polite');warning.hidden=true;input.closest('form').querySelector('.field-meta').after(warning);
 const mirror=document.createElement('div'),text=document.createElement('div');mirror.className='prompt-reference-mirror';mirror.setAttribute('aria-hidden','true');text.className='prompt-reference-text';mirror.append(text);
 let parent,tokens=[],popup=null,active=null,composing=false,scheduled=false,lastSignature='',pinnedOpen=false;
 const geometry=['fontFamily','fontSize','fontWeight','fontStyle','lineHeight','letterSpacing','textIndent','textAlign','tabSize','paddingTop','paddingBottom','paddingLeft','paddingRight','wordBreak','overflowWrap'];
 const safeURL=value=>{if(typeof value!=='string'||!value.trim())return '';try{const u=new URL(value,location.origin);return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password?u.href:'';}catch{return '';}};
 function enabled(){const syntax=selectedModel()?.reference_prompt_syntax;return (syntax==='numbered'||syntax==='picture'&&document.querySelector('#referenceMode')?.value==='references')&&!input.disabled&&!composing;}
 function hide(){if(popup){if(popup.matches(':popover-open'))popup.hidePopover();popup.remove();}popup=null;active=null;pinnedOpen=false;}
 function sync(){text.style.transform=`translate(${-input.scrollLeft}px,${-input.scrollTop}px)`;}
 function refresh(){
  scheduled=false;if(input.parentElement!==parent){parent=input.parentElement;parent.classList.add('prompt-reference-container');parent.append(mirror);}
  const show=enabled();input.classList.toggle('prompt-reference-input',show);mirror.hidden=!show;if(!show){tokens=[];lastSignature='';check();hide();return;}
  const css=getComputedStyle(input);for(const name of geometry)text.style[name]=css[name];text.style.width=input.clientWidth+'px';mirror.style.left=(input.offsetLeft+input.clientLeft)+'px';mirror.style.top=(input.offsetTop+input.clientTop)+'px';mirror.style.width=input.clientWidth+'px';mirror.style.height=input.clientHeight+'px';text.style.color=css.getPropertyValue('--text')||'inherit';
  const signature=JSON.stringify([selectedModel()?.reference_prompt_syntax,document.querySelector('#referenceMode')?.value,selectedProvider()?.id,input.value,input.clientWidth,input.clientHeight,input.offsetTop,input.offsetLeft,css.font,css.lineHeight,document.querySelector('#imageURLs')?.value,...Array.from({length:document.querySelector('#imageURLs')?.value.split('\n').filter(Boolean).length||0},(_,i)=>reference(i))]);if(signature===lastSignature){sync();return;}lastSignature=signature;
  hide();text.replaceChildren();tokens=[];const value=input.value;let last=0;
  for(const {start,end,index,label} of markers()){text.append(document.createTextNode(value.slice(last,start)));const span=document.createElement('span');span.className='prompt-reference-token';span.textContent=label;span.dataset.referenceIndex=String(index);const item=reference(index);span.classList.toggle('missing',!item);text.append(span);tokens.push({start,end,index,span});last=end;}
  text.append(document.createTextNode(value.slice(last)+(value.endsWith('\n')?'\n':'')));sync();if(active){const next=tokens.find(t=>t.start===active.start&&t.end===active.end);if(!next)hide();}
 }
 function markers(){
  const pattern=selectedModel()?.reference_prompt_syntax==='picture'?/(^|[\s([{'"“«:;,.!?])(<picture[ \t]+([1-9]\d*)>)/giu:/(^|[\s([{'"“«:;,.!?])(@?image[ \t]*([1-9]\d*))(?![\p{L}\p{N}_])/giu;
  return Array.from(input.value.matchAll(pattern),m=>({start:m.index+m[1].length,end:m.index+m[1].length+m[2].length,index:Number(m[3])-1,label:m[2]}));
 }
 function check(){
  const missing=enabled()?[...new Set(markers().filter(m=>!reference(m.index)).map(m=>m.index))].sort((a,b)=>a-b):[];
  const labels=missing.map(i=>selectedModel()?.reference_prompt_syntax==='picture'?`<Picture ${i+1}>`:`Image ${i+1}`);
  const message=labels.length?`Missing image references: ${labels.join(', ')}. Add the images or correct the prompt before generating.`:'';
  input.setCustomValidity(message);if(warning.textContent!==message)warning.textContent=message;warning.hidden=!message;
  return !message;
 }
 function validate(){const valid=check();if(!valid){input.focus();input.reportValidity();}return valid;}
 function requestRefresh(){check();if(!scheduled){scheduled=true;requestAnimationFrame(refresh);}}
 function reference(index){
  // Wan's first-frame-and-references mode numbers reference images separately.
  const mode=document.querySelector('#referenceMode')?.value;const offset=mode==='first_frame_references'?1:0;
  const item=window.referencePicker?.get('image',index+offset)||null;return item?.connection_id&&item.connection_id!==selectedProvider()?.id?null:item;
 }
 function at(x,y){const box=input.getBoundingClientRect();if(x<box.left||x>box.right||y<box.top||y>box.bottom)return null;for(const token of tokens){const range=document.createRange();range.selectNodeContents(token.span);if([...range.getClientRects()].some(r=>x>=r.left&&x<=r.right&&y>=r.top&&y<=r.bottom))return token;}return null;}
 function show(token,x,y,pinned=false){
  if(active===token&&popup){position(x,y);return;}
  hide();active=token;pinnedOpen=pinned;const item=reference(token.index);popup=document.createElement('aside');popup.className='model-info-popup reference-preview-popup';popup.setAttribute('popover','manual');popup.setAttribute('role','tooltip');popup.id='promptReferencePreview';
  const title=document.createElement('strong');title.textContent=token.span.textContent;popup.append(title);
  const caption=document.createElement('p');caption.textContent=item?item.name||`Image ${token.index+1}`:`No image ${token.index+1} is selected.`;popup.append(caption);
  const preview=item?.url?.startsWith('asset://')?item.preview_url:item?.url;const src=safeURL(preview);
  const panel=popup;
  if(item&&src){const img=document.createElement('img');img.alt=`Preview of image ${token.index+1}`;img.referrerPolicy='no-referrer';img.onload=()=>{if(popup===panel)position(x,y);};img.onerror=()=>{img.remove();const error=document.createElement('p');error.textContent='Preview unavailable.';if(popup===panel)panel.append(error);};img.src=src;popup.append(img);}else if(item){const unavailable=document.createElement('p');unavailable.textContent=item.url?.startsWith('asset://')?'Open the BytePlus library to load this registered asset’s preview.':'Preview unavailable.';popup.append(unavailable);}
  if(pinned){const close=document.createElement('button');close.type='button';close.className='model-info-close';close.textContent='×';close.setAttribute('aria-label','Close reference preview');close.onclick=hide;popup.append(close);}
  document.body.append(popup);if(popup.showPopover)popup.showPopover();position(x,y);
 }
 function position(x,y){if(!popup)return;const rect=popup.getBoundingClientRect();const left=Math.max(8,Math.min(x+14,innerWidth-rect.width-8));const top=y+16+rect.height<innerHeight?y+16:Math.max(8,y-rect.height-12);popup.style.left=left+'px';popup.style.top=top+'px';}
 input.addEventListener('input',()=>{hide();requestRefresh();});input.addEventListener('scroll',()=>{sync();hide();});input.addEventListener('compositionstart',()=>{composing=true;refresh();});input.addEventListener('compositionend',()=>{composing=false;requestRefresh();});
 input.addEventListener('pointermove',event=>{if(event.pointerType==='touch'||pinnedOpen||!enabled())return;const token=at(event.clientX,event.clientY);input.classList.toggle('reference-hover',!!token);if(token)show(token,event.clientX,event.clientY);else hide();});
 input.addEventListener('pointerleave',event=>{if(event.pointerType!=='touch'&&!pinnedOpen)hide();});
 input.addEventListener('pointerdown',event=>{if(event.pointerType!=='touch'||!enabled())return;const token=at(event.clientX,event.clientY);if(token){event.preventDefault();if(active===token)hide();else show(token,event.clientX,event.clientY,true);}});
 input.addEventListener('keydown',event=>{if(event.key==='Escape'){hide();return;}if(event.altKey&&event.key==='ArrowDown'){const token=tokens.find(t=>input.selectionStart>=t.start&&input.selectionStart<=t.end);if(token){event.preventDefault();const r=token.span.getBoundingClientRect();show(token,r.left,r.bottom,true);}}});
 document.addEventListener('pointerdown',event=>{if(event.target!==input&&!popup?.contains(event.target))hide();});window.addEventListener('resize',()=>{hide();requestRefresh();});
 window.addEventListener('gateway-reference-change',requestRefresh);document.querySelector('#referenceMode')?.addEventListener('change',requestRefresh);
 const observer=new ResizeObserver(requestRefresh);observer.observe(input);
 new MutationObserver(requestRefresh).observe(input,{attributes:true,attributeFilter:['style','disabled','class']});
 const hint=document.createElement('span');hint.className='sr-only';hint.id='promptReferenceHelp';hint.textContent='For numbered reference models, hover or tap image references for a preview. With the caret in a reference, press Alt plus Down Arrow. Escape closes the preview.';input.after(hint);input.setAttribute('aria-describedby',[input.getAttribute('aria-describedby'),hint.id,warning.id].filter(Boolean).join(' '));
 window.PromptReferences={refresh:requestRefresh,close:hide,validate};requestRefresh();
})();
