(() => {
  'use strict';
  let user=null;
  const fields=new Map(),pending=new Set();
  const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!=null)e.textContent=text;if(cls)e.className=cls;return e;};
  function identify(value){
    user=value.id?{...value}:{...user,...value};UserUI.remember(user);GatewayTheme.apply(user.theme||'system');window.GatewaySite?.selectPreset(user.color_preset||'');
    document.querySelector('#userName').textContent=user.name||user.email;
    document.querySelector('#userInitials').textContent=(user.name||user.email||'?').trim().split(/\s+/).map(p=>Array.from(p)[0]).slice(0,2).join('').toUpperCase();
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
      const field={form,control,save,status,edited:false};fields.set(key,field);save.disabled=pending.has(key);
      for(const event of ['input','change'])control.addEventListener(event,()=>{field.edited=true;});
      form.onsubmit=async event=>{
        event.preventDefault();if(pending.has(key)||save.disabled||!form.reportValidity())return;
        const value=control.value;field.edited=false;pending.add(key);save.disabled=true;FormUI.message(status,'');
        try{
          const result=await api('/v1/me',{method:'PATCH',body:JSON.stringify({[key]:value})});identify({[key]:result[key]});
          const current=fields.get(key);
          if(current.form.isConnected){
            // Reopened untouched controls adopt confirmation; even an edit back to
            // their old value is a draft. The server value is always the baseline.
            if(!current.edited)current.control.value=result[key];
            current.edited=current.control.value!==result[key];
            if(current===field)FormUI.saved(current.form,result[key]);else FormUI.commit(current.form,result[key]);
          }
        }
        catch(error){const current=fields.get(key);if(current.form.isConnected)FormUI.message(current.status,error.message,'error');}
        finally{pending.delete(key);const current=fields.get(key);current.save.disabled=false;FormUI.update(current.form);FormUI.refresh();}
      };
    }
    ContactLinks.editor(container,()=>user,identify);
  }
  window.GatewayProfile={identify,open,reportColumns:()=>user?.report_columns};
})();
