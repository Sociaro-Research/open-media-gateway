// Descriptor-driven controls shared by Playground and Template authoring.
(() => {
 'use strict';
 const node=(tag,text='',className='')=>Object.assign(document.createElement(tag),{textContent:text,className});
 // Adapt an editor draft to advertised capabilities. API admission stays strict.
 function reconcile(model,values={}){
  const result={},video=model.media_type==='video',visual=video||model.media_type==='image';
  const choice=(key,choices,fallback=choices?.[0])=>{if(choices?.length)result[key]=choices.includes(values[key])?values[key]:fallback;};
  const modes=model.reference_modes||[],mode=modes.find(m=>m.id===values.reference_mode)||modes[0];
  if(mode)result.reference_mode=mode.id;
  if(visual){
   // Use the same advertised-resolution policy as template output adaptation.
   if(model.resolutions?.length)result.resolution=ModelInfo.nearestResolution(values.resolution,model.resolutions);
   const ratios=mode?.ratios?.length?mode.ratios:model.ratios;
   choice('ratio',ratios,model.ratio_may_be_inferred?'':video&&ratios?.includes('16:9')?'16:9':ratios?.[0]);
   if(result.ratio==='')delete result.ratio;
  }
  if(model.max_duration>0&&!model.duration_from_input){
   const targetHasVideo=model.supports_video_input&&(!mode||mode.max_videos!==0)&&values.video_urls?.some(Boolean);
   const min=model.min_duration||0,max=Math.min(model.max_duration,targetHasVideo&&mode?.max_duration_with_video>0?mode.max_duration_with_video:model.max_duration),step=model.duration_step||1;
   const valid=v=>Number.isInteger(v)&&v>=min&&v<=max&&(v-min)%step===0;
   if(valid(values.duration))result.duration=values.duration;
   else if(!model.duration_may_be_inferred||values.duration!=null){
    const fallback=model.default_duration;
    if(valid(fallback))result.duration=fallback;
    else if(max>=min)result.duration=min;
   }
  }
  for(const [key,supported] of [['watermark',model.media_type!=='speech'&&!model.no_watermark],['generate_audio',model.supports_audio&&!model.audio_always_on],['camera_fixed',model.supports_camera_fixed],['draft',model.supports_draft&&(!model.draft_resolutions?.length||model.draft_resolutions.includes(result.resolution))],['return_last_frame',model.supports_last_frame]])if(supported)result[key]=values[key]===true;
  if(!model.no_seed&&Number.isSafeInteger(values.seed)&&values.seed>=0&&values.seed<=(model.max_seed||(video?4294967295:2147483647)))result.seed=values.seed;
  for(const [key,supported] of [['negative_prompt',model.supports_negative_prompt],['instructions',model.media_type==='speech'&&!model.no_instructions],['lyrics',model.supports_lyrics]])if(supported&&typeof values[key]==='string')result[key]=values[key];
  for(const [key,choices] of [['quality',model.qualities],['background',model.backgrounds],['format',model.formats]])choice(key,choices);
  if(model.supports_gender)result.gender=['female','male'].includes(values.gender)?values.gender:'female';
  if(model.supports_voice){
   const choices=voiceChoices(model);
   if(Object.hasOwn(model,'voice_choices')){
    const voice=choices.find(c=>c.id===values.voice)||choices.find(c=>c.is_default===true);
    if(voice){result.voice=voice.id;result.language=voice.languages.includes(values.language)?values.language:voice.default_language;}
   }else{result.voice=model.voices?.length?(model.voices.includes(values.voice)?values.voice:model.voices[0]):values.voice||'';if(!model.no_language&&model.media_type==='speech')result.language=values.language||'Auto';}
  }else if(!model.no_language&&model.media_type==='speech')result.language=values.language||'Auto';
  if(model.max_outputs>1)result.output_count=Number.isInteger(values.output_count)&&values.output_count>=1&&values.output_count<=model.max_outputs?values.output_count:1;
  const parameters={};
  for(const field of model.parameters||[]){
   const value=values.parameters?.[field.key];let valid=false;
   if(field.type==='boolean')valid=typeof value==='boolean';
   else if(['number','integer'].includes(field.type))valid=typeof value==='number'&&Number.isFinite(value)&&(field.type!=='integer'||Number.isInteger(value))&&(field.min==null||value>=field.min)&&(field.max==null||value<=field.max);
   else if(field.type==='text_list')valid=Array.isArray(value)&&field.max_items>0&&field.max_items<=64&&value.length<=field.max_items&&(!field.required||value.length>0)&&value.every(v=>typeof v==='string'&&(!field.max_length||Array.from(v).length<=field.max_length));
   else valid=typeof value==='string'&&(!field.required||value!=='')&&(!field.max_length||Array.from(value).length<=field.max_length)&&(field.type!=='select'||field.choices?.includes(value));
   if(valid)parameters[field.key]=structuredClone(value);else if(field.default!=null)parameters[field.key]=structuredClone(field.default);
  }
  if(Object.keys(parameters).length)result.parameters=parameters;
  return result;
 }
 function schema(model,kind){return (model?.reference_role_schemas||[]).find(s=>s.kind===kind&&Array.isArray(s.roles)&&s.roles.includes(s.default));}
 function role(model,kind,value){const s=schema(model,kind);return s?.roles.includes(value)?value:s?.default;}
 function roles(model,values,addresses){const result={};for(const address of addresses){const kind=address.split('_')[0],s=schema(model,kind),value=role(model,kind,values?.[address]);if(s&&value!==s.default)result[address]=value;}return Object.keys(result).length?result:undefined;}
 function roleField(model,kind,index,value,onChange){
  const s=schema(model,kind);if(!s||s.roles.length<2)return null;
  const label=node('label',`${kind[0].toUpperCase()+kind.slice(1)} ${index} role`),input=document.createElement('select');
  for(const value of s.roles)input.append(new Option(value.replaceAll('_',' ').replace(/^./,s=>s.toUpperCase()),value));input.value=role(model,kind,value);input.onchange=()=>onChange(input.value);label.append(input);return label;
 }
 function parameters(host,model,values={},onChange=()=>{},outputCount=1){
  host.replaceChildren();host.classList.add('model-parameters');const controls=[];host.hidden=!(model.parameters||[]).length&&!(model.max_outputs>1);let count=null;if(Number.isSafeInteger(model.max_outputs)&&model.max_outputs>1){const label=node('label','Outputs (1–'+model.max_outputs+')');count=document.createElement('input');count.type='number';count.min=1;count.max=model.max_outputs;count.step=1;count.required=true;count.value=outputCount??1;label.append(count);host.append(label);count.oninput=count.onchange=()=>onChange();}
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
   const label=node('label',field.label);let constrained=null,input=document.createElement(field.type==='select'?'select':'input');input.id=host.id==='modelParameters'?'model-param-'+field.key:host.id?host.id+'-'+field.key:'';
   if(field.type==='select'){for(const value of field.choices||[])input.append(new Option(value,value));}else input.type=field.type==='boolean'?'checkbox':['number','integer'].includes(field.type)?'number':'text';
   if(field.min!=null)input.min=field.min;if(field.max!=null)input.max=field.max;input.step=field.type==='integer'?'1':'any';if(input.type!=='checkbox')input.required=!!field.required;
   if(input.type==='checkbox')input.checked=value===true;else input.value=value??'';
   const sync=()=>input.setCustomValidity(constrained&&!constrained.has(input.value)?'Choose a currently available value.':field.max_length>0&&Array.from(input.value).length>field.max_length?'Use at most '+field.max_length.toLocaleString('en-US')+' characters.':'');input.oninput=()=>{sync();onChange();};input.onchange=()=>{sync();onChange();};label.append(input);host.append(label);sync();controls.push({key:field.key,read:()=>input.type==='checkbox'?input.checked:input.value===''?undefined:['number','integer'].includes(field.type)?Number(input.value):input.value,valid:()=>input.validity.valid,setChoices(items){const stamp=JSON.stringify(items);if(input.dataset.choiceStamp===stamp)return;const previous=input.value,next=input.tagName==='SELECT'?input:document.createElement('select');next.replaceChildren();next.id=input.id;next.required=!!field.required;next.disabled=input.disabled;next.dataset.choiceStamp=stamp;next.append(new Option('Choose '+field.label.toLowerCase(),''));constrained=new Set(items.map(item=>String(item.value)));for(const item of items)next.append(new Option(item.label,String(item.value)));if(previous&&!constrained.has(previous))next.append(new Option('Unavailable selection: '+previous,previous));next.value=previous;if(input!==next)input.replaceWith(next);input=next;input.onchange=()=>{sync();onChange();};sync();FormUI.refresh();}});
  }
  FormUI.refresh();return {count:()=>count?Number(count.value):undefined,setChoices(key,items){controls.find(c=>c.key===key)?.setChoices?.(items);},sync(){controls.forEach(c=>c.sync?.());},read(){return Object.fromEntries(controls.map(c=>[c.key,c.read()]).filter(([,v])=>v!==undefined));},valid(){return (!count||count.validity.valid)&&controls.every(c=>c.valid());}};
 }
 function inferredDuration(field,input,model,value,onChange){
  const previous=field.parentElement.closest('.inferred-duration-group');if(previous){previous.before(field);previous.remove();}let select=null;
  if(model.duration_may_be_inferred&&!model.duration_from_input){const label=node('label','Duration'),host=node('div');host.dataset.durationMode='';select=document.createElement('select');select.setAttribute('aria-label','Duration');select.append(new Option('Auto (infer)','auto'),new Option('Set seconds','custom'));label.append(select);host.append(label);const group=node('div','','inferred-duration-group');field.before(group);group.append(host,field);select.onchange=()=>{sync();onChange();};}
  function sync(){if(select){field.hidden=select.value==='auto';input.disabled=field.hidden;}FormUI.refresh();}
  function restore(value){if(select)select.value=value===undefined||value===null||value===0?'auto':'custom';if(value>0)input.value=value;sync();}
  restore(value);return {restore,sync,read:()=>select?.value==='auto'?undefined:Number(input.value)};
 }
 function ratios(input,model,choices,value){input.dataset.inference=String(!!model.ratio_may_be_inferred);input.replaceChildren(...(model.ratio_may_be_inferred?[new Option('Auto (infer)','')]:[]),...choices.map(v=>new Option(v,v)));if(value!==undefined&&value!==''&&!choices.includes(value))input.append(new Option('Unsupported saved value: '+value,value));input.required=!model.ratio_may_be_inferred;input.value=value!==undefined&&[...input.options].some(o=>o.value===value)?value:model.ratio_may_be_inferred?'':choices[0]||'';window.ControlPickers?.sync(input);}
 function resourceParameters(editor,bindings){if(!editor)return;const faces=bindings.filter(item=>item.kind==='face_session'&&item.usable===true&&Date.parse(item.session?.expires_at)>Date.now()).flatMap(item=>item.session?.faces||[]);editor.setChoices('face_index',faces.filter(face=>Number.isSafeInteger(face.index)&&face.index>0).map(face=>({value:face.index,label:'Face '+face.index+' · '+face.start_ms+'–'+face.end_ms+' ms'})));}
 const legacyLanguageValues=new WeakMap();
 function voiceChoices(model){return Array.isArray(model?.voice_choices)?model.voice_choices.filter(c=>typeof c.id==='string'&&c.id&&Array.isArray(c.languages)&&c.languages.length&&c.languages.every(l=>typeof l==='string'&&l)&&c.languages.includes(c.default_language)):[];}
 function voiceRequestError(model,options={}){if(!model||!Object.hasOwn(model,'voice_choices'))return '';const choices=voiceChoices(model);if(!choices.length)return 'Voice/language metadata is unavailable.';if(!options.voice&&options.resource_references?.length){return model.languages?.includes(options.language)?'':'Choose a declared language for the managed voice.';}return choices.some(c=>c.id===options.voice&&c.languages.includes(options.language))?'':'Choose a reviewed voice and one of its declared languages.';}
 function voiceLanguage(model,{voice,language,legacy,managed=()=>false,initial}){
  const declared=Object.hasOwn(model,'voice_choices'),choices=voiceChoices(model);if(legacy.some(c=>c.value===language.value))legacyLanguageValues.set(language,language.value);let preferred=language.value,lastManaged=!!managed();
  const display=value=>{try{return new Intl.DisplayNames('en',{type:'language'}).of(value)||value;}catch{return value;}};
  function fill(values,value,message=''){language.replaceChildren(...(message?[new Option(message,'',true,true)]:[]),...values.map(v=>new Option(display(v),v)));language.value=values.includes(value)?value:'';language.setCustomValidity(message||(!values.includes(language.value)?'Choose an available language.':''));window.ControlPickers?.sync(language);FormUI.refresh();}
  function allowed(){if(managed())return (model.languages||[]).filter(l=>typeof l==='string'&&l);return choices.find(c=>c.id===voice.value)?.languages||[];}
  function validate(){if(!declared)return true;const known=managed()||choices.some(c=>c.id===voice.value);voice.required=!!model.requires_voice&&!managed();voice.setCustomValidity(known?'':'Choose a reviewed voice.');const values=allowed();language.required=true;language.setCustomValidity(values.includes(language.value)?'':'Choose a declared language for this voice.');return (managed()||voice.validity.valid)&&language.validity.valid&&!!values.length;}
  function normalize(){if(!declared)return;const values=allowed(),choice=choices.find(c=>c.id===voice.value),value=values.includes(language.value)?language.value:values.includes(preferred)?preferred:managed()?'':choice?.default_language;fill(values,value,values.length&&!value?'Choose a language':!values.length?'Voice/language choices unavailable':'');if(values.includes(language.value))preferred=language.value;validate();}
  if(declared){voice.list?.replaceChildren(...choices.map(c=>new Option(c.id,c.id)));const defaults=choices.filter(c=>c.is_default===true);voice.value=choices.find(c=>c.id===initial?.voice)?.id||(defaults.length===1?defaults[0].id:'');preferred=initial?.language??choices.find(c=>c.id===voice.value)?.default_language??'';language.value='';normalize();}
  else{voice.setCustomValidity('');voice.required=false;language.required=false;language.setCustomValidity('');const previous=language.value;language.replaceChildren(...legacy.map(c=>new Option(c.label,c.value)));language.value=legacy.some(c=>c.value===previous)?previous:legacyLanguageValues.get(language)||legacy[0]?.value||'';window.ControlPickers?.sync(language);}
  return {valid:validate,voiceChanged(){normalize();},languageChanged(){if(declared&&allowed().includes(language.value))preferred=language.value;if(!declared&&legacy.some(c=>c.value===language.value))legacyLanguageValues.set(language,language.value);validate();},resourcesChanged(){const next=!!managed();if(next!==lastManaged){lastManaged=next;normalize();}else validate();},restore(){if(!declared){if(legacy.some(c=>c.value===language.value))legacyLanguageValues.set(language,language.value);return;}const saved=language.value,values=allowed();fill(values,saved,values.includes(saved)?'':'Saved language is unavailable; choose a current language.');if(values.includes(saved))preferred=saved;validate();}};
 }
 window.ModelControls={reconcile,voiceLanguage,voiceRequestError,voiceChoices,resourceParameters,schema,role,roles,roleField,parameters,inferredDuration,ratios};
})();
