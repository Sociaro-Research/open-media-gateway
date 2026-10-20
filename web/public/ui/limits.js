(() => {
 'use strict';
 const fields=[
 ['key_generations_minute','Generations per key / minute'],['user_generations_minute','Generations per user / minute'],
 ['key_generations_second','Generations per key / second'],['user_generations_second','Generations per user / second'],
 ['key_reads_minute','Status and list requests per key / minute'],['user_reads_minute','Status and list requests per user / minute'],
 ['user_key_creations_minute','New keys per user / minute'],['user_active_keys','Active keys per user']
 ];
 const el=(tag,text,cls)=>{const node=document.createElement(tag);if(text!=null)node.textContent=text;if(cls)node.className=cls;return node};
 window.RequestLimits={async mount(parent,api,{organization='',current=()=>true}={}){
  const url=organization?`/v1/organizations/${organization}/limits`:'/v1/system/limits';
  const form=el('form',null,'keys-form organization-form request-limits');
  form.append(el('h2',organization?'Organization request limits':'Request limits'));
  const actions=el('div',null,'form-actions'),status=el('p'),save=el('button','Save','primary compact');status.setAttribute('role','status');save.type='submit';actions.append(status,save);form.append(actions);
  const description=el('p',organization?'Limits apply separately to each member, across all their keys in this organization. Leave a field empty to inherit its system default.':'Defaults for personal keys and all organizations. User limits combine their keys within each organization; personal keys are counted separately.','field-explanation');form.append(description);
  const controls=new Map();
  for(const [name,title] of fields){const label=el('label',title,'organization-field'),input=el('input');input.type='number';input.name=name;input.min='1';input.max='2147483647';input.step='1';input.required=!organization;label.append(input);form.append(label);controls.set(name,input)}
  parent.append(form);let policy,busy=false;
  const enable=value=>{for(const c of form.querySelectorAll('input,button'))c.disabled=!value};enable(false);
  const config=()=>policy[organization?'organization':'system'];
  const input=()=>({values:Object.fromEntries([...controls].map(([name,node])=>[name,node.value===''?null:Number(node.value)])),version:config().version});
  try{policy=await api(url);if(!current())return;for(const [name,node] of controls){node.value=config().values[name]??'';if(organization){node.dataset.floatingLabel='always';node.placeholder=String(policy.system.values[name]);node.title=`System default: ${policy.system.values[name]}`}}enable(true);FormUI.watch(form,save);FormUI.refresh()}
  catch(error){if(current())FormUI.message(status,error.message,'error');return}
  form.addEventListener('submit',async e=>{
   e.preventDefault();if(busy||!form.reportValidity())return;const payload=input();busy=true;enable(false);FormUI.message(status,'');
   try{const next=await api(url,{method:'PUT',body:JSON.stringify(payload)});if(!current())return;policy=next;enable(true);FormUI.saved(form)}
   catch(error){if(current())FormUI.message(status,error.status===409?'Another administrator changed these limits. Reopen Settings before saving.':error.message,'error')}
   finally{busy=false;enable(true);FormUI.update(form)}
  });
 }};
})();
