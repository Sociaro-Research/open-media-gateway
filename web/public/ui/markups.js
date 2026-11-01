(() => {
  'use strict';
  const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
  const option=(value,text)=>{const n=el('option',text);n.value=value;return n;};
  window.MarkupSettings={async mount(parent,api,{organization='',current=()=>true,onSystemSaved=()=>{}}={}){
    const company=!!organization,url=company?`/v1/organizations/${organization}/markups`:'/v1/system/markups';
    const form=el('form',null,'keys-form markup-settings');
    form.innerHTML=`<h2>Markups</h2>
      <div class="form-actions"><p role="status"></p><button type="submit" class="primary compact">Save</button></div>
      <div class="markup-layout"><div class="markup-base"><label>${company?'Organization markup (%)':'Default markup (%)'}<input name="default_percent" type="number" min="0" max="1000" step="0.01" ${company?'':'required'}></label><p class="field-explanation"></p></div>
      <section class="markup-exceptions"><div class="form-heading markup-list-heading"><h3>Exceptions</h3><button class="secondary" type="button" data-add>Add exception</button></div><div class="markup-table-wrap" role="region" aria-label="Markup exceptions" tabindex="0"><table class="docs-table markup-table"><thead><tr><th scope="col">Model</th><th scope="col">Markup (%)</th><th scope="col"></th></tr></thead><tbody class="markup-rows"><tr data-empty><td colspan="3">No exceptions</td></tr></tbody></table></div></section></div>`;
    parent.append(form);
    const save=form.querySelector('[type=submit]'),status=form.querySelector('[role=status]'),base=form.elements.default_percent,rows=form.querySelector('.markup-rows'),add=form.querySelector('[data-add]'),hint=form.querySelector('.field-explanation');
    let policy,catalog=[],config,busy=false;
    const items=[];
    save.disabled=add.disabled=base.disabled=true;
    const input=()=>({default_percent:base.value===''?null:Number(base.value),exceptions:items.map(i=>{const [provider,model]=JSON.parse(i.choice.value);return {provider,...(model?{model}:{}),markup_percent:i.rate.value===''?null:Number(i.rate.value)}}),version:config.version});
    function update(){
      const seen=new Set();
      for(const item of items){
        const [provider]=JSON.parse(item.choice.value);const key=item.choice.value;item.choice.setCustomValidity(!provider?'Choose a provider or model.':seen.has(key)?'An exception for this provider and model already exists.':'');seen.add(key);
      }
      hint.textContent=company&&base.value===''?`Inherits system rules. System default: ${policy.system.default_percent??0}%.`:'';
      rows.querySelector('[data-empty]').hidden=items.length>0;
      add.disabled=busy||items.length>=500;FormUI.update(form);
    }
    function row(rule={provider:catalog[0]?.id||'',markup_percent:null}){
      const wrap=el('tr',null,'markup-rule control-row'),choice=el('select'),rate=el('input'),remove=el('button','Remove','secondary');
      choice.required=true;rate.type='number';rate.min='0';rate.max='1000';rate.step='0.01';rate.required=true;rate.value=rule.markup_percent??'';
      const choices=catalog.map(p=>({...p,models:[...(p.models||[])]}));
      // Preserve saved exceptions for retired models and disconnected providers.
      let provider=choices.find(p=>p.id===rule.provider);
      if(!provider&&rule.provider){provider={id:rule.provider,name:rule.provider,models:[]};choices.push(provider)}
      if(rule.model&&!provider.models.some(m=>m.id===rule.model))provider.models.push({id:rule.model,display_name:rule.model});
      ModelPicker.populate(choice,choices,{value:ModelPicker.key(rule.provider,rule.model||''),allModels:true});
      if(!choice.options.length)choice.append(option(ModelPicker.key(''),'No providers available'));
      const item={choice,rate,wrap};items.push(item);
      for(const [text,control] of [['Model',choice],['Markup (%)',rate]]){const cell=el('td');control.setAttribute('aria-label',text);cell.append(control);wrap.append(cell);}
      remove.type='button';remove.setAttribute('aria-label','Remove exception');const actions=el('td',null,'table-row-actions');actions.append(remove);wrap.append(actions);rows.append(wrap);
      choice.addEventListener('change',update);rate.addEventListener('input',update);
      remove.addEventListener('click',()=>{if(busy)return;items.splice(items.indexOf(item),1);wrap.remove();update()});
      update();return item;
    }
    try{
      const results=await Promise.all([api(url),api(company?url+'/catalog':'/v1/system/markups/catalog')]);if(!current())return;
      policy=results[0];catalog=results[1].data||[];config=policy[company?'company':'system'];
      base.value=config.default_percent??'';
      for(const rule of config.exceptions||[])row(rule);
      save.disabled=add.disabled=base.disabled=false;
      FormUI.watch(form,save,{read:()=>{const {version,...values}=input();return JSON.stringify(values)}});update();FormUI.refresh();
    }catch(error){if(current())FormUI.message(status,error.message,'error');return;}
    base.addEventListener('input',update);
    add.addEventListener('click',()=>{const item=row();FormUI.refresh();item.rate.focus();update()});
    form.addEventListener('submit',async event=>{
      event.preventDefault();update();if(busy||!form.reportValidity())return;busy=true;
      const payload=input();for(const control of form.querySelectorAll('input,select,button'))control.disabled=true;
      FormUI.message(status,'');
      try{
        const next=await api(url,{method:'PUT',body:JSON.stringify(payload)});if(!current())return;
        policy=next;config=next[company?'company':'system'];if(!company)onSystemSaved(config.version);
        // Unlock before committing the shared Saved feedback state.
        for(const control of form.querySelectorAll('input,select,button'))control.disabled=false;
        FormUI.saved(form);
      }catch(error){if(current())FormUI.message(status,error.status===409?'Another administrator changed these settings. Reopen Settings before saving.':error.message,'error');}
      finally{busy=false;for(const control of form.querySelectorAll('input,select,button'))control.disabled=false;update();FormUI.refresh();}
    });
    return {setSystemVersion(version,previous){if(!company&&config.version===previous)config.version=version;}};
  }};
})();
