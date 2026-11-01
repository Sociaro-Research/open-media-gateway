// Descriptor-driven controls shared by Playground and Template authoring.
(() => {
 'use strict';
 const node=(tag,text='',className='')=>Object.assign(document.createElement(tag),{textContent:text,className});
 function schema(model,kind){return (model?.reference_role_schemas||[]).find(s=>s.kind===kind&&Array.isArray(s.roles)&&s.roles.includes(s.default));}
 function role(model,kind,value){const s=schema(model,kind);return s?.roles.includes(value)?value:s?.default;}
 function roles(model,values,addresses){const result={};for(const address of addresses){const kind=address.split('_')[0],s=schema(model,kind),value=role(model,kind,values?.[address]);if(s&&value!==s.default)result[address]=value;}return Object.keys(result).length?result:undefined;}
 function roleField(model,kind,index,value,onChange){
  const s=schema(model,kind);if(!s||s.roles.length<2)return null;
  const label=node('label',`${kind[0].toUpperCase()+kind.slice(1)} ${index} role`),input=document.createElement('select');
  for(const value of s.roles)input.append(new Option(value.replaceAll('_',' ').replace(/^./,s=>s.toUpperCase()),value));input.value=role(model,kind,value);input.onchange=()=>onChange(input.value);label.append(input);return label;
 }
 function parameters(host,model,values={},onChange=()=>{}){
  host.replaceChildren();host.classList.add('model-parameters');const controls=[];host.hidden=!(model.parameters||[]).length;
  for(const field of model.parameters||[]){
   const value=values[field.key]??field.default;
   if(field.type==='text_list'){
    const group=node('fieldset','','parameter-list'),heading=node('legend',field.label),rows=node('div','','parameter-list-rows'),actions=node('div','','reference-actions'),add=node('button','Add item','secondary'),status=node('p','','field-hint');add.type='button';add.setAttribute('aria-label','Add '+field.label+' item');status.setAttribute('role','status');actions.append(add);group.append(heading,rows,actions,status);host.append(group);
    const inputs=[];const declared=Number.isInteger(field.max_items)&&field.max_items>0&&field.max_items<=64;
    function valid(){return declared&&(!field.required||inputs.length>0)&&inputs.length<=field.max_items&&inputs.every(input=>input.validity.valid);}
    function sync(){heading.textContent=field.label+(declared?` (${inputs.length} / ${field.max_items})`:'');add.disabled=!declared||inputs.length>=field.max_items;status.textContent=!declared?'This list is unavailable. Reload the model settings.':field.required&&!inputs.length?'Add at least one item.':'';inputs.forEach((input,index)=>{FormUI.setLabel(input,field.label+' '+(index+1));input.setAttribute('aria-label',field.label+' '+(index+1));input.setCustomValidity(field.max_length>0&&Array.from(input.value).length>field.max_length?'Use at most '+field.max_length.toLocaleString('en-US')+' characters per item.':'');});}
    function append(value='',focus=false){const row=node('label',field.label+' '+(inputs.length+1)),input=document.createElement('input'),remove=node('button','Remove','secondary parameter-remove');input.type='text';input.value=value;remove.type='button';remove.setAttribute('aria-label','Remove '+field.label+' item');row.append(input);rows.append(row);inputs.push(input);FormUI.refresh();input.closest('.floating-field').append(remove);input.addEventListener('input',()=>{sync();onChange();});remove.onclick=()=>{const index=inputs.indexOf(input);inputs.splice(index,1);row.remove();sync();(inputs[index]||inputs.at(-1)||add).focus();onChange();};sync();if(focus)input.focus();}
    if(Array.isArray(value)&&value.every(v=>typeof v==='string'))for(const text of value)append(text);
    add.onclick=()=>{if(declared&&inputs.length<field.max_items&&!add.disabled){append('',true);onChange();}};sync();controls.push({key:field.key,read:()=>inputs.map(input=>input.value),valid,sync});continue;
   }
   const label=node('label',field.label),input=document.createElement(field.type==='select'?'select':'input');input.id=host.id==='modelParameters'?'model-param-'+field.key:host.id?host.id+'-'+field.key:'';
   if(field.type==='select'){for(const value of field.choices||[])input.append(new Option(value,value));}else input.type=field.type==='boolean'?'checkbox':['number','integer'].includes(field.type)?'number':'text';
   if(field.min!=null)input.min=field.min;if(field.max!=null)input.max=field.max;input.step=field.type==='integer'?'1':'any';if(input.type!=='checkbox')input.required=!!field.required;
   if(input.type==='checkbox')input.checked=value===true;else input.value=value??'';
   const sync=()=>input.setCustomValidity(field.max_length>0&&Array.from(input.value).length>field.max_length?'Use at most '+field.max_length.toLocaleString('en-US')+' characters.':'');input.oninput=()=>{sync();onChange();};input.onchange=()=>{sync();onChange();};label.append(input);host.append(label);sync();controls.push({key:field.key,read:()=>input.type==='checkbox'?input.checked:input.value===''?undefined:['number','integer'].includes(field.type)?Number(input.value):input.value,valid:()=>input.validity.valid});
  }
  FormUI.refresh();return {sync(){controls.forEach(c=>c.sync?.());},read(){return Object.fromEntries(controls.map(c=>[c.key,c.read()]).filter(([,v])=>v!==undefined));},valid(){return controls.every(c=>c.valid());}};
 }
 function inferredDuration(field,input,model,value,onChange){
  const previous=field.parentElement.closest('.inferred-duration-group');if(previous){previous.before(field);previous.remove();}let select=null;
  if(model.duration_may_be_inferred&&!model.duration_from_input){const label=node('label','Duration'),host=node('div');host.dataset.durationMode='';select=document.createElement('select');select.setAttribute('aria-label','Duration');select.append(new Option('Auto (infer)','auto'),new Option('Set seconds','custom'));label.append(select);host.append(label);const group=node('div','','inferred-duration-group');field.before(group);group.append(host,field);select.onchange=()=>{sync();onChange();};}
  function sync(){if(select){field.hidden=select.value==='auto';input.disabled=field.hidden;}FormUI.refresh();}
  function restore(value){if(select)select.value=value===undefined||value===null||value===0?'auto':'custom';if(value>0)input.value=value;sync();}
  restore(value);return {restore,sync,read:()=>select?.value==='auto'?undefined:Number(input.value)};
 }
 function ratios(input,model,choices,value){input.dataset.inference=String(!!model.ratio_may_be_inferred);input.replaceChildren(...(model.ratio_may_be_inferred?[new Option('Auto (infer)','')]:[]),...choices.map(v=>new Option(v,v)));if(value!==undefined&&value!==''&&!choices.includes(value))input.append(new Option('Unsupported saved value: '+value,value));input.required=!model.ratio_may_be_inferred;input.value=value!==undefined&&[...input.options].some(o=>o.value===value)?value:model.ratio_may_be_inferred?'':choices[0]||'';window.ControlPickers?.sync(input);}
 window.ModelControls={schema,role,roles,roleField,parameters,inferredDuration,ratios};
})();
