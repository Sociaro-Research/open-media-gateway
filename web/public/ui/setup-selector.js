(() => {
 'use strict';
 let actor=null,dialog=null,busy=false,opening=0;
 const sections=new Set();
 const central=s=>s.installation_role==='central';
 const needsCompanySetup=s=>!!s.setup_pending||!s.shared_resources||s.personal_providers;
 function reflectSection(section,state){section.summary.textContent=profile(state);section.button.textContent=central(state)?'Configure company service':'Change Mode';section.button.hidden=central(state)&&!needsCompanySetup(state);}
 function published(state,previous){for(const section of sections){if(!section.summary.isConnected){sections.delete(section);continue;}reflectSection(section,state);if(section.current())section.onSaved?.(state,previous);}}
 const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
 const profile=s=>central(s)?'Central server · Service for companies':s.shared_resources?'Service for companies':(s.personal_providers?'For myself':'Custom: personal and shared access disabled');
 const scenario=s=>s.shared_resources?'operator':s.personal_providers?'personal':'';
 function dismissal(user=actor){return 'gateway-setup-dismissed-'+user.id+':'+(user.session_id||'session');}
 function dismissed(){try{return !!sessionStorage.getItem(dismissal());}catch{return false;}}
 async function open({current=()=>true,onboarding=false}={}){
  if(!actor?.is_system_admin||busy||dialog?.open)return;
  const user=actor,ticket=++opening;let settings=await api('/v1/system/settings');
  if(ticket!==opening||actor!==user||!actor?.is_system_admin||busy||!current())return;
  const isCentral=central(settings);
  dialog?.remove();const modal=node('dialog',undefined,'keys-revoke-dialog sales-dialog setup-dialog');dialog=modal;const form=node('form'),status=node('p');status.setAttribute('role','status');
  function dismiss(){if(busy)return;try{sessionStorage.setItem(dismissal(user),'1');}catch{}modal.close();}
  modal.append(form);document.body.append(modal);const {heading,close}=FormUI.dialogHeading(form,isCentral?'Configure company service':'Choose how to use this installation',dismiss);heading.id='setup-dialog-title';modal.setAttribute('aria-labelledby',heading.id);
  const active=()=>actor===user&&dialog===modal&&modal.open&&current();let reconciling=false,retry;
  modal.addEventListener('close',()=>clearTimeout(retry));
  const choices=node('fieldset');choices.setAttribute('aria-label','Setup');
  for(const [value,title,description]of [
   ['personal','For myself','Use your own provider accounts and personal storage. No company billing is required.'],
   ['operator','Service for companies','Configure shared providers and storage, then company membership, funding and limits.'],
   ['sociaro','Sociaro','Unlock ready-to-use providers, exclusive pricing, and the latest AI models, including those not publicly available. Connect to Sociaro and start generating instantly, with no manual setup required.']
  ]){if(isCentral&&value!=='operator')continue;const label=node('label',undefined,'storage-choice-header setup-option'),radio=node('input');radio.type='radio';radio.name='scenario';radio.value=value;const copy=node('span');copy.append(node('strong',title),node('small',description));label.append(radio,copy);choices.append(label);}
  const actions=node('div',undefined,'dialog-actions'),skip=node('button','Skip for now','secondary'),apply=node('button','Apply','primary compact');skip.type='button';skip.hidden=!onboarding||isCentral;apply.type='submit';skip.onclick=dismiss;actions.append(skip,apply);

  form.append(choices,status,actions);
  function draft(){return JSON.stringify(form.elements.scenario.value);}
  function baseline(){return JSON.stringify((onboarding&&settings.setup_pending)||settings.mode_selection_required||isCentral&&needsCompanySetup(settings)?'':scenario(settings));}
  function sync(){for(const radio of choices.querySelectorAll('input'))radio.checked=radio.value===(isCentral?'operator':scenario(settings));FormUI.watch(form,apply,{placement:'footer',hideWhenClean:true,read:draft,valid:()=>!!form.elements.scenario.value&&!busy&&!reconciling});FormUI.commit(form,baseline());update();}
  function update(){const value=form.elements.scenario.value;apply.textContent=value==='sociaro'?'Connect to Sociaro':'Apply';for(const control of [choices])control.disabled=busy||reconciling;for(const control of [skip,close])control.disabled=busy;FormUI.update(form);

  }
  choices.onchange=update;
  async function reconcile(){try{const next=await api('/v1/system/settings');if(!active())return;settings=next;reconciling=false;sync();published(next);FormUI.message(status,'Settings changed. Current configuration has been loaded. Choose and review your changes before applying again.','error');}catch{if(!active())return;FormUI.message(status,'Could not load the changed settings. Retrying automatically…','error');retry=setTimeout(reconcile,1500);}}
  modal.addEventListener('cancel',e=>{e.preventDefault();dismiss();});
  form.onsubmit=async e=>{e.preventDefault();const value=form.elements.scenario.value;if(!active()||busy||apply.disabled||!value)return;
   if(value==='sociaro'){dismiss();await window.RemoteWorkspace.open({connect:true});return;}
   busy=true;update();FormUI.message(status,'Applying…');modal.setAttribute('aria-busy','true');
   try{const previous=settings,next=await api('/v1/system/setup-profile',{method:'POST',body:JSON.stringify({profile:value,version:settings.version})});if(!active())return;settings=next;if(typeof consolePolicy==='undefined'||!consolePolicy.version||next.version>=consolePolicy.version)window.applySystemAccess(next,true);published(next,previous);dismissAfterSave();}
   catch(error){if(!active())return;FormUI.message(status,error.message,'error');if(error.status===409){reconciling=true;void reconcile();}}
   finally{busy=false;modal.removeAttribute('aria-busy');update();}
  };
  function dismissAfterSave(){busy=false;dismiss();}
  sync();modal.showModal();FormUI.refresh();(isCentral?(apply.disabled?close:apply):(choices.querySelector('input:checked')||choices.querySelector('input'))).focus();
 }
 window.SetupSelector={
  async initialize(user){opening++;if(actor!==user)dialog?.close();actor=user;window.RemoteWorkspace?.setActor(user);if(!user?.is_system_admin)return;try{const settings=await api('/v1/system/settings');if(actor===user&&settings.setup_pending&&!dismissed())await open({onboarding:true});}catch(error){console.warn('Setup guide is available from Settings.');}},
  mount(parent,state,onSaved,current=()=>true){const block=node('section',undefined,'setup-section'),summary=node('p',profile(state),'field-explanation');block.append(node('h2',central(state)?'Company service':'Installation Mode'),summary);const button=node('button','Change Mode','secondary');const section={summary,button,onSaved,current};sections.add(section);reflectSection(section,state);button.type='button';button.onclick=()=>open({current}).catch(e=>{if(current())FormUI.message(message,e.message,'error');});const message=node('p');message.setAttribute('role','status');block.append(button,message);parent.append(block);return button;},
  open,reflect:published
 };
})();
