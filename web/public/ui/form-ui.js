(() => {
  'use strict';
  const watches=new WeakMap();
  function sectionHeading(parent,title,level='h2'){
    const row=document.createElement('div'),heading=document.createElement(level);
    row.className='form-heading console-section-heading';heading.textContent=title;row.append(heading);parent.append(row);return heading;
  }
  function dialogHeading(parent,title,onClose){
    const heading=sectionHeading(parent,title),close=document.createElement('button');
    heading.parentElement.classList.add('dialog-heading');close.className='model-info-close dialog-close icon-close';close.type='button';close.textContent='×';close.setAttribute('aria-label','Close '+title.toLowerCase());close.onclick=onClose;heading.parentElement.append(close);return {heading,close};
  }
  function disclosure(summary,text){if(!summary||summary.dataset.disclosureLabel===text)return;summary.dataset.disclosureLabel=text;const label=document.createElement('span'),chevron=document.createElement('span');label.textContent=text;chevron.className='select-chevron';chevron.setAttribute('aria-hidden','true');summary.replaceChildren(label,chevron);}
  function message(target,text='',type=''){target.dataset.messageType=type;target.textContent=text;target.setAttribute('role','status');}

  // API credentials are not login passwords. All credential editors share the
  // saved mask and omit it from updates; the server never returns saved secrets.
  const credentialInputs=new WeakSet(),credentialMask='************';
  function setupCredential(input){
    if(credentialInputs.has(input))return;
    credentialInputs.add(input);input.type='text';input.classList.add('secret-input');
    input.autocomplete='off';input.spellcheck=false;
    input.setAttribute('autocapitalize','off');input.setAttribute('autocorrect','off');
    for(const attr of ['data-1p-ignore','data-lpignore','data-bwignore'])input.setAttribute(attr,'true');
    const selectMask=()=>{if(input.dataset.savedCredential==='true')input.select();};
    input.addEventListener('focus',selectMask);input.addEventListener('click',selectMask);
    const replaceMask=text=>{
      input.value=text;input.dataset.savedCredential='false';input.setSelectionRange(text.length,text.length);
      input.dispatchEvent(new Event('input',{bubbles:true}));
    };
    input.addEventListener('paste',event=>{
      if(input.dataset.savedCredential!=='true'||!event.clipboardData)return;
      event.preventDefault();replaceMask(event.clipboardData.getData('text/plain'));
    });
    let pendingEdit=null;
    input.addEventListener('beforeinput',event=>{
      if(input.dataset.savedCredential!=='true')return;
      const text=event.inputType.startsWith('delete')?'':event.data??event.dataTransfer?.getData('text/plain');
      // Replace the whole display mask explicitly. WebKit may retain the old
      // edit range even when the selection is changed during beforeinput.
      if(typeof text!=='string'){input.select();return;}
      if(!event.cancelable){pendingEdit=text;return;}
      event.preventDefault();replaceMask(text);
    });
    input.addEventListener('input',()=>{if(pendingEdit!==null){input.value=pendingEdit;pendingEdit=null;}input.dataset.savedCredential='false';if(input.form)update(input.form);});
  }
  function renderCredential(input,configured,hint=''){
    setupCredential(input);input.value=configured?(hint||credentialMask):'';
    input.dataset.credentialDisplay=input.value;
    input.dataset.savedCredential=configured?'true':'false';sync(input);
  }
  // Undefined preserves a saved secret; an empty string explicitly clears it.
  function credentialUpdate(input){const value=input.value.trim();return input.dataset.savedCredential==='true'||value===credentialMask||(value!==''&&value===input.dataset.credentialDisplay)?undefined:value;}
  function credentialReplacement(input){return credentialUpdate(input)??'';}

  const values=form=>JSON.stringify([...form.elements].filter(e=>e.matches('input,select,textarea')&&!['button','submit'].includes(e.type)).map(e=>[e.type,e.type==='checkbox'||e.type==='radio'?e.checked:e.value]));
  function clearSaved(state){
    const feedback=state.feedback;if(!feedback)return;
    clearTimeout(feedback.timer);
    state.button.replaceChildren(...feedback.contents);state.button.style.setProperty('min-width',feedback.minWidth,feedback.minWidthPriority);state.button.style.setProperty('width',feedback.width,feedback.widthPriority);
    state.button.classList.remove('save-confirmation');state.button.disabled=false;state.feedback=null;
  }
  function update(form){
    const state=watches.get(form);if(!state)return;
    const unchanged=state.initial===state.read();
    if(state.discard)state.discard.hidden=unchanged;
    if(state.feedback&&!unchanged)clearSaved(state);
    if(state.placement==='footer'){state.button.hidden=state.hideWhenClean&&unchanged;state.button.disabled=!!state.feedback||unchanged||!state.valid();}
    else state.button.hidden=state.feedback?false:unchanged||!state.valid();
    if(state.feedback&&!state.button.disabled)state.button.disabled=true;
  }
  function saved(form,baseline=snapshot(form)){
    const state=watches.get(form);if(!state)return;
    clearSaved(state);const width=state.button.getBoundingClientRect().width;
    commit(form,baseline);
    // Edits made while the request was pending still need to be saved.
    if(state.initial!==state.read())return;
    const button=state.button;
    state.feedback={contents:[...button.childNodes],minWidth:button.style.minWidth,minWidthPriority:button.style.getPropertyPriority('min-width'),width:button.style.width,widthPriority:button.style.getPropertyPriority('width')};
    button.style.setProperty('min-width',width+'px','important');button.style.setProperty('width',width+'px','important');button.textContent='Saved';
    button.classList.add('save-confirmation');button.setAttribute('aria-live','polite');
    state.feedback.timer=setTimeout(()=>{clearSaved(state);update(form);},3000);
    update(form);
  }
  function watch(form,button,{read=()=>values(form),valid=()=>true,heading,discard,placement='heading',hideWhenClean=false}={}){const previous=watches.get(form);if(previous)clearSaved(previous);if(placement==='heading')placeAction(form,button,heading);if(discard)placeDiscard(button,discard);watches.set(form,{button,discard,read,valid,placement,hideWhenClean,initial:read()});update(form);}
  function placeDiscard(button,discard){
    const row=button.closest('.form-heading');if(!row)return;
    const old=discard.parentElement;let actions=button.closest('.form-heading-actions');
    if(!actions){actions=document.createElement('div');actions.className='form-heading-actions';button.before(actions);}
    discard.classList.add('stable-action');actions.append(discard,button);
    if(old!==actions&&old.classList.contains('form-actions')&&!old.children.length)old.remove();
  }
  function placeAction(form,button,heading){
    if(form.matches('.role-editor,.member-limit-form'))return;
    button.classList.add('stable-action');
    heading ||= form.querySelector(':scope > h2,:scope > h3');if(!heading)return;
    if(button.closest('.form-heading'))return;
    let row=heading.parentElement;if(!row.classList.contains('form-heading')){row=document.createElement('div');row.className='form-heading';heading.before(row);row.append(heading);}
    const old=button.parentElement;if(old.classList.contains('form-actions')){const message=old.querySelector('[role=status]');if(message)row.append(message);}form.id||=`settings-form-${++serial}`;button.setAttribute('form',form.id);row.append(button);
    if(old.classList.contains('form-actions')&&!old.children.length)old.remove();
  }
  // Passive readiness checks must not dispatch invalid events or move focus.
  function valid(form){return [...form.elements].every(control=>!control.willValidate||control.validity.valid);}
  function snapshot(form){return watches.get(form)?.read();}
  function commit(form,saved=snapshot(form)){const state=watches.get(form);if(state){state.initial=saved;update(form);}}
  const supported=e=>e.matches('input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=file]):not([type=range]):not([type=button]):not([type=submit]):not([type=color]),textarea,select');
  let serial=0,scheduled=false;
  function enhance(control){
    if(control.type==='checkbox'){control.setAttribute('role','switch');return;}
    if(control.hasAttribute('data-plain-control')||control.classList.contains('picker-source')||control.closest('.floating-field')||!supported(control))return;
    let label=control.labels?.[0];
    if(!label){const name=control.dataset.fieldLabel||control.getAttribute('aria-label');if(!name)return;label=document.createElement('label');label.textContent=name;control.id||=`floating-${++serial}`;label.htmlFor=control.id;control.before(label);}
    const nested=label.contains(control),wrap=document.createElement('span');wrap.className='floating-field';
    if(nested){const caption=document.createElement('span');caption.className='floating-label';
      const text=[...label.childNodes].filter(n=>n.nodeType===Node.TEXT_NODE);caption.textContent=text.map(n=>n.textContent).join('').trim();text.forEach(n=>n.remove());
      if(!caption.textContent)return;control.before(wrap);wrap.append(control,caption);
    }else{control.before(wrap);wrap.append(control,label);label.classList.remove('sr-only');label.classList.add('floating-label');}
    sync(control);
  }
  function sync(control){const wrap=control.closest('.floating-field');if(!wrap)return;const raised=control.dataset.floatingLabel==='always'||control.tagName==='TEXTAREA'||(control.tagName==='SELECT'&&!!control.selectedOptions[0]?.textContent.trim())||control.value!==''||control===document.activeElement||['date','datetime-local','month','time'].includes(control.type);wrap.classList.toggle('raised',raised);}
  const multiline=new WeakMap(),multilineNodes=new Set(),multilineMeasure=document.createElement('textarea');
  multilineMeasure.setAttribute('data-plain-control','');multilineMeasure.setAttribute('aria-hidden','true');multilineMeasure.tabIndex=-1;
  multilineMeasure.style.cssText='position:fixed;left:-10000px;top:0;height:0;min-height:0;max-height:none;visibility:hidden;pointer-events:none;overflow:hidden;box-sizing:border-box;';
  function sizeMultiline(control){
    if(control.tagName!=='TEXTAREA'||control===multilineMeasure)return;
    control.classList.add('multiline-input');control.closest('.floating-field')?.classList.add('multiline-field');
    if(!multiline.has(control)){multiline.set(control,{});multilineNodes.add(control);multilineObserver.observe(control);}
    const state=multiline.get(control),width=control.getBoundingClientRect().width;
    if(!width){state.key=null;return;}
    const style=getComputedStyle(control),line=parseFloat(style.lineHeight)||parseFloat(style.fontSize)*1.5;
    const minRows=Math.max(1,Number(style.getPropertyValue('--textarea-min-rows'))||3);
    const maxRows=Math.max(minRows,Number(style.getPropertyValue('--textarea-max-rows'))||10);
    const padding=parseFloat(style.paddingTop)+parseFloat(style.paddingBottom),border=parseFloat(style.borderTopWidth)+parseFloat(style.borderBottomWidth);
    const key=JSON.stringify([control.value,width,line,padding,border,minRows,maxRows]);if(state.key===key)return;
    // Measure separately: collapsing the live textarea would move its caret/scroll.
    if(!multilineMeasure.isConnected)document.body.append(multilineMeasure);
    for(const name of ['fontFamily','fontSize','fontWeight','fontStyle','lineHeight','letterSpacing','wordSpacing','textIndent','textTransform','tabSize','whiteSpace','overflowWrap','wordBreak','direction','padding','border'])multilineMeasure.style[name]=style[name];
    multilineMeasure.style.width=(control.clientWidth+parseFloat(style.borderLeftWidth)+parseFloat(style.borderRightWidth))+'px';
    multilineMeasure.wrap=control.wrap;multilineMeasure.value=control.value;
    const height=Math.ceil(Math.max(minRows*line+padding+border,Math.min(multilineMeasure.scrollHeight+border,maxRows*line+padding+border)))+'px';
    if(control.style.height!==height)control.style.height=height;
    state.key=key;
  }
  // Resize on the next frame so changing height cannot re-enter observer delivery.
  const multilineObserver=new ResizeObserver(()=>schedule());
  const copyIcon='<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/></svg>';
  function copyButton(read,label,onFailure){
    const button=document.createElement('button');button.type='button';button.className='copy-icon';button.innerHTML=copyIcon;button.title=label;button.setAttribute('aria-label',label);
    const status=document.createElement('span');status.className='sr-only';status.setAttribute('role','status');button.append(status);let timer;
    button.onclick=async()=>{clearTimeout(timer);try{await navigator.clipboard.writeText(read());button.classList.add('copied');button.querySelector('svg').innerHTML='<path d="m5 12 4 4L19 6"/>';button.title='Copied';status.textContent='Copied';}catch{onFailure?.();button.title='Select and copy the value manually';status.textContent='Could not copy. Select and copy the value manually.';}timer=setTimeout(()=>{button.classList.remove('copied');button.querySelector('svg').replaceWith(new DOMParser().parseFromString(copyIcon,'image/svg+xml').documentElement);button.title=label;status.textContent='';},1800);};return button;
  }
  function enhanceCopy(control){
    if(!control.readOnly||!control.matches('input,textarea'))return;const wrap=control.closest('.floating-field');if(!wrap||wrap.classList.contains('has-copy'))return;
    wrap.classList.add('has-copy');const label=control.getAttribute('aria-label')||wrap.querySelector('.floating-label')?.textContent||'value';wrap.append(copyButton(()=>control.value,'Copy '+label,()=>{control.focus();control.select();}));
  }
  const passwordControls=new WeakMap();
  const eye='<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>';
  const eyeHidden='<path d="m3 3 18 18M10.6 5.1 12 5c6.5 0 10 7 10 7a20 20 0 0 1-3.1 3.8M6.2 6.2A21 21 0 0 0 2 12s3.5 7 10 7a11 11 0 0 0 5.8-1.8M9.9 9.9a3 3 0 0 0 4.2 4.2"/>';
  function enhancePassword(control){
    if(control.classList.contains('secret-input'))return;
    const known=passwordControls.get(control);if(known){known.button.disabled=control.disabled;return;}
    if(control.type!=='password')return;
    const wrap=control.closest('.floating-field');if(!wrap)return;
    wrap.classList.add('password-field');
    const button=document.createElement('button');button.type='button';button.className='password-toggle';
    control.id||=`password-${++serial}`;
    button.setAttribute('aria-controls',control.id);button.disabled=control.disabled;
    function setVisible(visible){
      control.type=visible?'text':'password';button.title=visible?'Hide password':'Show password';
      button.setAttribute('aria-label',button.title);
      button.innerHTML='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'+(visible?eyeHidden:eye)+'</svg>';
    }
    // Keep input focus without cancelling touch activation in WebKit.
    button.addEventListener('mousedown',event=>event.preventDefault());
    button.onclick=()=>{
      const focused=document.activeElement===control,start=control.selectionStart,end=control.selectionEnd;
      setVisible(control.type==='password');
      if(focused)control.focus({preventScroll:true});
      if(start!==null)control.setSelectionRange(start,end);
    };
    passwordControls.set(control,{button,setVisible});setVisible(false);wrap.append(button);
  }
  function hidePassword(control){const state=passwordControls.get(control);if(state)state.setVisible(false);else control.type='password';}
  // Keep native constraint validation and submission blocking, replacing only its bubble.
  let validation, pendingInvalid;
  function validationText(control){
    const v=control.validity;
    if(v.customError)return control.validationMessage;
    if(v.valueMissing)return control.tagName==='SELECT'?'Select an option.':'This field is required.';
    if(v.typeMismatch)return control.type==='email'?'Enter a valid email address.':'Enter a valid URL.';
    if(v.rangeUnderflow)return `Enter a value of ${control.min} or more.`;
    if(v.rangeOverflow)return `Enter a value of ${control.max} or less.`;
    if(v.badInput)return 'Enter a number.';
    return control.validationMessage;
  }
  function clearValidation(){
    if(!validation)return;
    for(const [target,invalid,description] of validation.attributes){
      invalid===null?target.removeAttribute('aria-invalid'):target.setAttribute('aria-invalid',invalid);
      description===null?target.removeAttribute('aria-describedby'):target.setAttribute('aria-describedby',description);
      target.classList.remove('field-invalid');
    }
    validation.popup.remove();validation=null;
  }
  function positionValidation(){
    if(!validation)return;const {control,target,popup}=validation;
    if(!control.isConnected||!control.willValidate||control.validity.valid||!target.getClientRects().length){clearValidation();return;}
    const box=target.getBoundingClientRect();
    if(box.bottom<0||box.top>innerHeight){clearValidation();return;}
    popup.style.left=Math.max(12,Math.min(box.left,innerWidth-popup.offsetWidth-12))+'px';
    popup.style.top=Math.max(12,box.bottom+8+popup.offsetHeight<=innerHeight-12?box.bottom+8:box.top-popup.offsetHeight-8)+'px';
  }
  function showValidation(control){
    clearValidation();if(!control.isConnected||!control.willValidate||control.validity.valid)return;
    const target=control.closest('.floating-field')?.querySelector('.select-trigger')||control;
    const popup=document.createElement('div');popup.className='field-validation';popup.id=`field-validation-${++serial}`;popup.setAttribute('role','alert');popup.textContent=validationText(control);
    const attributes=[...new Set([control,target])].map(e=>[e,e.getAttribute('aria-invalid'),e.getAttribute('aria-describedby')]);
    for(const [e,,description] of attributes){e.classList.add('field-invalid');e.setAttribute('aria-invalid','true');e.setAttribute('aria-describedby',[description,popup.id].filter(Boolean).join(' '));}
    target.focus();target.scrollIntoView({block:'nearest'});document.body.append(popup);validation={control,target,popup,attributes};positionValidation();
  }
  document.addEventListener('invalid',event=>{
    event.preventDefault();if(pendingInvalid)return;pendingInvalid=event.target;
    setTimeout(()=>{const control=pendingInvalid;pendingInvalid=null;showValidation(control);},0);
  },true);
  for(const event of ['input','change'])document.addEventListener(event,e=>{if(validation?.control!==e.target)return;if(e.target.validity.valid)clearValidation();else{validation.popup.textContent=validationText(e.target);positionValidation();}});
  document.addEventListener('focusin',e=>{if(validation&&e.target!==validation.target)clearValidation();});
  document.addEventListener('keydown',e=>{if(e.key==='Escape')clearValidation();});
  window.addEventListener('scroll',positionValidation,true);window.addEventListener('resize',positionValidation);
  function refresh(){scheduled=false;for(const control of multilineNodes){if(!control.isConnected){multilineObserver.unobserve(control);multilineNodes.delete(control);multiline.delete(control);}}document.querySelectorAll('input,select,textarea').forEach(control=>{if(control.classList.contains('secret-input'))setupCredential(control);enhance(control);sync(control);enhanceCopy(control);enhancePassword(control);sizeMultiline(control);window.ControlPickers?.enhance(control);});positionValidation();}
  function schedule(){if(!scheduled){scheduled=true;requestAnimationFrame(refresh);}}
  for(const event of ['input','change','focusin','focusout'])document.addEventListener(event,e=>{if(e.target.matches('input,select,textarea')){sync(e.target);sizeMultiline(e.target);if(e.target.form)update(e.target.form);}},true);
  document.addEventListener('reset',event=>{event.target.querySelectorAll('.password-field input').forEach(hidePassword);setTimeout(refresh,0);},true);
  window.addEventListener('resize',schedule);document.fonts?.ready.then(schedule);
  const promptHints=new WeakMap();
  function promptState(value,model){
    const policy=!model?.manual?model?.prompt_policy:null,reviewed=Number.isSafeInteger(policy?.max_characters)&&policy.max_characters>0;
    const count=Array.from(String(value||'')).length,declared=Number.isSafeInteger(model?.max_prompt_characters)&&model.max_prompt_characters>0?model.max_prompt_characters:null,known=declared?reviewed?Math.min(policy.max_characters,declared):declared:reviewed?policy.max_characters:null;
    const number=n=>n.toLocaleString('en-US'),label=model?.no_prompt?'Prompt':known?count?'Prompt ('+number(count)+' / '+number(known)+' characters)':'Prompt ('+number(known)+' characters max)':count?'Prompt ('+number(count)+' characters)':'Prompt';
    const characters=Array.from(String(value||'').trim()),maximum=known||10000,ascii=characters.filter(c=>c.codePointAt(0)<128).length,other=characters.length-ascii;
    let error=!model?.no_prompt&&characters.length>maximum?'Prompt must not exceed '+number(maximum)+' characters.':'';
    const weighted=reviewed&&Number.isSafeInteger(policy.ascii_budget)&&Number.isSafeInteger(policy.non_ascii_budget)&&policy.ascii_budget>0&&policy.non_ascii_budget>0;
    if(!error&&!model?.no_prompt&&weighted&&ascii*policy.non_ascii_budget+other*policy.ascii_budget>policy.ascii_budget*policy.non_ascii_budget)error='The mixed ASCII/non-ASCII prompt exceeds this model’s Gateway budget.';
    const budget=weighted?'Gateway budget: ASCII / '+number(policy.ascii_budget)+' + other characters / '+number(policy.non_ascii_budget)+' ≤ 1.':'';
    return {count,limit:known,label,error,budget};
  }
  function updatePrompt(control,model){
    control.removeAttribute('maxlength');const previous=control.dataset.promptLimitError||'',state=promptState(control.value,model);
    let hint=promptHints.get(control);if(state.budget&&!hint){hint=document.createElement('p');hint.className='field-hint prompt-policy';const owner=control.closest('label')||control.closest('.floating-field')||control,group=document.createElement('div');group.className='prompt-policy-group';owner.before(group);group.append(owner,hint);promptHints.set(control,hint);}if(hint){if(hint.textContent!==state.budget)hint.textContent=state.budget;const hidden=!state.budget||!!model?.no_prompt;if(hint.hidden!==hidden)hint.hidden=hidden;}
    control.dataset.promptLimitError=state.error;setLabel(control,state.label);control.setAttribute('aria-description',(model?.prompt_optional?'Optional. ':'')+state.label+(state.budget?' '+state.budget:''));
    if(state.error||previous&&control.validationMessage===previous)control.setCustomValidity(state.error);
    return state;
  }
  function setLabel(control,text){enhance(control);const label=control.closest('.floating-field')?.querySelector('.floating-label');if(label&&label.textContent!==text)label.textContent=text;}
  window.FormUI={sectionHeading,dialogHeading,disclosure,message,valid,watch,commit,snapshot,saved,update,refresh,setLabel,promptState,updatePrompt,copyButton,placeAction,credentials:{setup:setupCredential,render:renderCredential,replacement:credentialReplacement,updateValue:credentialUpdate},passwords:{hide:hidePassword}};
  new MutationObserver(schedule).observe(document.body,{childList:true,subtree:true,attributes:true,attributeFilter:['disabled']});refresh();
})();
