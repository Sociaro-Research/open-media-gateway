(() => {
  'use strict';
  let user=null, identityEpoch=0;
  const flights=new Map();
  const preferenceMaps=new Set(['table_columns','table_wide']);
  const context=()=>({epoch:identityEpoch,id:user?.id,session:user?.session_id,connection:window.CompanyConnection?.record?.()||null});
  const sameContext=c=>c.epoch===identityEpoch&&c.id===user?.id&&c.session===user?.session_id&&c.connection===(window.CompanyConnection?.record?.()||null);
  const fields=new Map(),pending=new Map();
  const pendingHere=key=>{const held=pending.get(key);if(held&&!sameContext(held))pending.delete(key);return pending.has(key);};
  const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!=null)e.textContent=text;if(cls)e.className=cls;return e;};
  function identify(value){
    const changed=value.id&&(value.id!==user?.id||('session_id' in value&&value.session_id!==user?.session_id));
    if(changed){identityEpoch++;flights.clear();fields.clear();pending.clear();user=null;}
    const authoritative=!!value.id,next=authoritative?{...value}:{...user,...value};
    for(const key of preferenceMaps)if(!authoritative&&value[key]){
      next[key]={...user?.[key],...value[key]};
      for(const [table,setting] of Object.entries(value[key]))if(setting===null)delete next[key][table];
    }
    user=next;UserUI.remember(user);GatewayTheme.apply(user.theme||'system',{share:true});window.GatewaySite?.selectPreset(user.color_preset||'');
    const name=document.querySelector('#userName'),initials=document.querySelector('#userInitials');
    if(name)name.textContent=user.name||user.email||'';
    if(initials)initials.textContent=(user.name||user.email||'?').trim().split(/\s+/).map(p=>Array.from(p)[0]).slice(0,2).join('').toUpperCase();
    window.dispatchEvent(new CustomEvent('gateway-profile-changed'));
  }
  async function save(input,request=window.api){
    const captured=context(),tokens=new Set();
    for(const [key,value]of Object.entries(input))for(const subkey of preferenceMaps.has(key)?Object.keys(value):['']){
      tokens.add(key+'/'+subkey);
    }
    const predecessors=[...tokens.keys()].map(key=>flights.get(key)).filter(held=>held&&sameContext(held.captured)).map(held=>held.completion);
    let release;const completion=new Promise(resolve=>{release=resolve;});
    for(const key of tokens.keys())flights.set(key,{captured,completion});
    try{
    await Promise.all(predecessors);
    if(!sameContext(captured))throw new DOMException('Profile session changed','AbortError');
    const result=await request('/v1/me',{method:'PATCH',body:JSON.stringify(input)});
    if(!sameContext(captured)||(result.id&&captured.id&&result.id!==captured.id)||(result.session_id&&captured.session&&result.session_id!==captured.session))throw new DOMException('Profile session changed','AbortError');
    const patch={};
    // A PATCH returns a full user, potentially older than another concurrent
    // confirmation. Adopt only the fields/table entries this intent changed.
    for(const [key,value]of Object.entries(input)){
      if(preferenceMaps.has(key)){
        const entries={};for(const table of Object.keys(value))entries[table]=result[key]?.[table]??null;
        if(Object.keys(entries).length)patch[key]=entries;
      }else patch[key]=result[key];
    }
    identify(patch);return result;
    }finally{release();for(const key of tokens.keys())if(flights.get(key)?.completion===completion)flights.delete(key);}
  }
  async function open(parent){
    await window.GatewaySite?.ready;
    if(!parent.isConnected)return;
    const container=node('div',null,'profile-settings');parent.append(container);
    let appearanceSection;
    for(const [title,key,label] of [['Personal details','name','Display name'],['Appearance','theme','Theme'],['Appearance','color_preset','Color theme']]){
      const section=key==='color_preset'?appearanceSection:node('section',null,'profile-section');if(key!=='color_preset'){section.append(node('h2',title));container.append(section);}if(key==='theme')appearanceSection=section;
      const form=node('form',null,(key==='name'?'member-limit-form':'role-editor')+' profile-field'),control=node(key==='name'?'input':'select');
      control.name=key;control.setAttribute('aria-label',label);
      if(key==='theme')for(const [id,name] of [['dark','Dark'],['light','Light'],['system','System']])control.append(new Option(name,id));
      else if(key==='color_preset'){control.append(new Option('Site default',''));for(const preset of window.GatewaySite?.presets()||[])control.append(new Option(preset.name,preset.id));}
      else{control.required=true;control.maxLength=100;control.autocomplete='name';}
      control.value=user?.[key]||(key==='theme'?'system':'');
      if(key==='color_preset'&&control.selectedIndex<0)control.value='';
      const save=node('button','Save','secondary'),status=node('p');save.type='submit';
      form.append(control,save,status);section.append(form);
      FormUI.refresh();FormUI.watch(form,save,{read:()=>control.value,valid:()=>key==='color_preset'||!!control.value.trim()});
      const field={form,control,save,status,edited:false};fields.set(key,field);save.disabled=pendingHere(key);
      for(const event of ['input','change'])control.addEventListener(event,()=>{field.edited=true;});
      form.onsubmit=async event=>{
        event.preventDefault();if(pendingHere(key)||save.disabled||!form.reportValidity())return;
        const captured=context(),value=control.value;field.edited=false;pending.set(key,captured);save.disabled=true;FormUI.message(status,'');
        try{
          const result=await GatewayProfile.save({[key]:value});
          if(!sameContext(captured))return;
          const current=fields.get(key);
          if(current.form.isConnected){
            // Reopened untouched controls adopt confirmation; even an edit back to
            // their old value is a draft. The server value is always the baseline.
            if(!current.edited)current.control.value=result[key];
            current.edited=current.control.value!==result[key];
            if(current===field)FormUI.saved(current.form,result[key]);else FormUI.commit(current.form,result[key]);
          }
        }
        catch(error){if(!sameContext(captured))return;const current=fields.get(key);if(current.form.isConnected)FormUI.message(current.status,error.message,'error');}
        finally{if(sameContext(captured)){pending.delete(key);const current=fields.get(key);current.save.disabled=false;FormUI.update(current.form);FormUI.refresh();}}
      };
    }
    ContactLinks.editor(container,()=>user,identify);
  }
  window.GatewayProfile={identify,open,save,context,sameContext,reportColumns:()=>user?.report_columns,tableColumns:id=>user?.table_columns?.[id],tableWide:id=>!!user?.table_wide?.[id]};
})();
