(() => {
  'use strict';
  const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  const clone=value=>JSON.parse(JSON.stringify(value));
  const imageSlots=[
    {key:'logo',role:'logo',theme:'dark',label:'Logo'},
    {key:'symbol',role:'symbol',theme:'dark',label:'Compact logo'},
    {key:'favicon',role:'favicon',theme:'dark',label:'Favicon'},
    {key:'logo_light',role:'logo',theme:'light',label:'Logo'},
    {key:'symbol_light',role:'symbol',theme:'light',label:'Compact logo'},
    {key:'favicon_light',role:'favicon',theme:'light',label:'Favicon'},
    {key:'touch_icon',label:'Home screen icon'},
    {key:'email_logo',label:'Email logo'}
  ];
  const slotByKey=new Map(imageSlots.map(slot=>[slot.key,slot]));
  const themeName=theme=>theme==='dark'?'Dark theme':'Light theme';
  const slotName=slot=>slot.theme?`${themeName(slot.theme)} · ${slot.label}`:slot.label;
  // Same ordered fallback rules as branding.Settings.ImageSource. Both theme
  // slots are peers; an inherited image always identifies its actual source.
  function resolveImage(settings,key){
    const slot=slotByKey.get(key);
    const pair=role=>slot.theme==='light'?[role+'_light',role]:[role,role+'_light'];
    let candidates=[key];
    if(slot.role==='logo')candidates=pair('logo');
    if(slot.role==='symbol')candidates=[...pair('symbol'),...pair('favicon'),...pair('logo')];
    if(slot.role==='favicon')candidates=[...pair('favicon'),...pair('symbol'),...pair('logo')];
    if(key==='touch_icon')candidates=[key,'favicon_light','favicon','symbol_light','symbol','logo_light','logo'];
    if(key==='email_logo'){
      candidates=[key,...['logo_light','logo'].filter(k=>['image/png','image/jpeg'].includes(settings.assets[k]?.mime))];
    }
    for(const source of candidates){const asset=settings.assets[source];if(asset)return {url:`data:${asset.mime};base64,${asset.data}`,mime:asset.mime,source,note:source===key?(slot.theme?'Uploaded for this theme':'Uploaded image'):'Uses '+slotName(slotByKey.get(source))};}
    if(key==='email_logo'&&settings.name)return {text:settings.name,note:'Uses site name'};
    return {note:'No image uploaded'};
  }
  const apply=settings=>window.GatewaySite.apply(settings);
  window.BrandingSettings={apply,resolveImage,async mount(parent,api,current){
    const form=el('form',undefined,'keys-form branding-settings'),heading=FormUI.sectionHeading(form,'Appearance'),status=el('p','','form-message');form.append(status);parent.append(form);
    let data;
    try{data=await api('/v1/system/branding');if(!current())return;}catch(error){if(current())FormUI.message(status,error.message,'error');return;}
    if(!data.settings){FormUI.message(status,'Appearance settings are unavailable.','error');return;}
    window.GatewayLayout.apply(data.settings);
    data.settings=window.GatewaySite.normalize(clone(data.settings));
    let draft=clone(data.settings),busy=false,pendingUploads=0,previewTheme=document.documentElement.dataset.theme||'dark',editingID=draft.default_preset;
    const appearanceKeys=['name','description','contact_email','assets','site_max_width','content_max_width','sidebar_subsections'];
    const appearanceValues=settings=>Object.fromEntries(appearanceKeys.map(key=>[key,settings[key]]));
    const selectedPreset=()=>draft.presets.find(p=>p.id===editingID)||draft.presets[0];
    function syncDefault(settings){const preset=settings.presets.find(p=>p.id===settings.default_preset);settings.dark=clone(preset.dark);settings.light=clone(preset.light);}
    const identity=el('div',undefined,'settings-grid');
    for(const [key,label,max,type]of [['name','Site name',80,'text'],['description','Site description',240,'text'],['contact_email','Contact email',254,'email']]){const wrap=el('label',label),input=el('input');input.name=key;input.type=type;input.maxLength=max;wrap.append(input);identity.append(wrap);input.addEventListener('input',()=>{draft[key]=input.value;refreshImages();renderPreview();changed();});}
    form.append(identity,el('h3','Layout'));
    const widths=el('div',undefined,'branding-layout-fields'),widthInputs=new Map();
    for(const [key,label,min]of [['site_max_width','Maximum site width (px)',960],['content_max_width','Maximum content width (px)',640]]){
      const wrap=el('label',label),input=el('input');input.type='number';input.name=key;input.min=String(min);input.max='7680';input.step='1';wrap.append(input);widths.append(wrap);widthInputs.set(key,input);
      input.addEventListener('input',()=>{draft[key]=input.value===''?null:Number(input.value);changed();});
    }
    const treeLabel=el('label',undefined,'system-toggle'),tree=el('input'),treeText=el('span');tree.type='checkbox';tree.name='sidebar_subsections';treeText.append(el('strong','Show subsections in the left menu'),el('small','On desktop, expand each section to show its subsections in the left menu. When off, subsections appear as tabs above the content. Mobile always uses tabs.'));treeLabel.append(tree,treeText);tree.addEventListener('change',()=>{draft.sidebar_subsections=tree.checked;changed();});
    form.append(widths,el('p','Leave a width empty to use all available space with safe outer margins. Widths are maximums: narrower screens shrink automatically. A limited content area stays centered when space allows; the left menu stays at the edge of the site.','field-explanation'),treeLabel,el('h3','Images by theme'),el('p','Logo is the full header image; Compact logo is the small header image; Favicon is the browser tab icon. Header images follow the site theme. Favicons follow the browser’s system theme. Upload PNG, JPEG or plain SVG, up to 512 KiB each.','field-explanation'));
    const imageThemes=el('div',undefined,'branding-theme-columns'),assetControls=new Map();
    for(const theme of ['dark','light']){const column=el('section',undefined,'branding-image-column'),title=el('h3',themeName(theme),'branding-theme-heading');column.dataset.imageTheme=theme;title.id='brand-images-'+theme;column.setAttribute('role','group');column.setAttribute('aria-labelledby',title.id);column.append(title);for(const slot of imageSlots.filter(s=>s.theme===theme))column.append(imageEditor(slot));imageThemes.append(column);}
    const shared=el('div',undefined,'branding-shared-images');for(const slot of imageSlots.filter(s=>!s.theme))shared.append(imageEditor(slot));
    form.append(imageThemes,el('h3','Shared images'),el('p','Home screen icon: one square PNG/JPEG for the phone shortcut, independent of the site theme. Email logo: one PNG/JPEG for account emails, which use a light background.','field-explanation'),shared);
    const colorForm=el('form',undefined,'keys-form branding-color-settings'),colorHeading=FormUI.sectionHeading(colorForm,'Color themes'),colorStatus=el('p','','form-message');colorForm.append(colorStatus);parent.append(colorForm);
    const selection=el('div',undefined,'control-row'),presetSelect=el('select'),selectLabel=el('label','Color theme');presetSelect.name='color_preset';selectLabel.append(presetSelect);selection.append(selectLabel);
    const presetControls=el('div',undefined,'branding-preset-controls control-row'),presetName=el('input'),nameLabel=el('label','Theme name'),defaultLabel=el('label',undefined,'system-toggle'),defaultToggle=el('input');presetName.name='preset_name';presetName.maxLength=80;presetName.required=true;nameLabel.append(presetName);defaultToggle.type='checkbox';defaultToggle.name='default_preset';defaultToggle.setAttribute('aria-label','Default theme');defaultLabel.append(defaultToggle,el('strong','Default'));
    const removePreset=el('button','Delete','secondary'),colorDiscard=el('button','Discard','secondary'),colorActions=el('div',undefined,'form-actions');removePreset.type=colorDiscard.type='button';presetControls.append(nameLabel,defaultLabel,removePreset);colorActions.append(colorDiscard);colorForm.append(selection,presetControls,colorActions);
    const presetTitle=preset=>preset.name+(preset.id===draft.default_preset?' (default)':'');
    function syncPresetTitles(){for(const option of presetSelect.options){const preset=draft.presets.find(p=>p.id===option.value);if(preset)option.textContent=presetTitle(preset);}ControlPickers.sync(presetSelect);}
    const replacementPreset=()=>draft.presets.find(p=>p.id===data.settings.default_preset&&p.id!==editingID)||draft.presets.find(p=>p.id!==editingID);
    function presetState(){defaultToggle.checked=editingID===draft.default_preset;defaultToggle.disabled=busy||draft.presets.length===1;removePreset.disabled=busy||draft.presets.length===1;removePreset.title=draft.presets.length===1?'Keep at least one color theme.':'';syncPresetTitles();}
    presetSelect.onchange=()=>{if(presetSelect.value===''){presetSelect.value=selectedPreset().id;ControlPickers.sync(presetSelect);createTheme();return;}editingID=presetSelect.value;refresh();changed();};
    presetName.oninput=()=>{selectedPreset().name=presetName.value;presetState();renderPreview();changed();};
    defaultToggle.onchange=()=>{draft.default_preset=defaultToggle.checked?editingID:replacementPreset().id;presetState();changed();};
    removePreset.onclick=()=>{if(draft.presets.length===1)return;const replacement=replacementPreset();draft.presets=draft.presets.filter(p=>p.id!==editingID);if(draft.default_preset===editingID)draft.default_preset=replacement.id;editingID=replacement.id;refresh();changed();};
    function createTheme(){
      const dialog=el('dialog',undefined,'keys-revoke-dialog system-dialog'),entry=el('form',undefined,'keys-form'),label=el('label','Theme name'),name=el('input'),message=el('p'),actions=el('div',undefined,'form-actions'),cancel=el('button','Cancel','secondary'),add=el('button','Create','primary compact');
      FormUI.sectionHeading(entry,'Create color theme');name.name='name';name.required=true;name.maxLength=80;name.setAttribute('aria-label','New theme name');label.append(name);cancel.type='button';add.type='submit';actions.append(cancel,add);entry.append(label,message,actions);dialog.append(entry);document.body.append(dialog);cancel.onclick=()=>dialog.close();dialog.addEventListener('close',()=>dialog.remove());
      entry.onsubmit=event=>{event.preventDefault();const title=name.value.trim();if(!title||draft.presets.some(p=>p.name.trim().toLowerCase()===title.toLowerCase())){FormUI.message(message,'Enter a unique theme name.','error');return;}if(draft.presets.length>=32){FormUI.message(message,'You can save up to 32 color themes.','error');return;}
        const preset=clone(selectedPreset());preset.id=crypto.randomUUID();preset.name=title;draft.presets.push(preset);editingID=preset.id;dialog.close();refresh();changed();};
      dialog.showModal();FormUI.refresh();name.focus();
    }
    function imageEditor(slot){
      const box=el('div',undefined,'branding-asset');box.dataset.asset=slot.key;
      const title=el('h4',slot.label),previewBox=el('div',undefined,'branding-image-preview'),img=el('img'),text=el('span',undefined,'branding-image-text'),source=el('p','','branding-image-source');img.alt=slotName(slot)+' preview';source.id='brand-image-source-'+slot.key;previewBox.append(img,text);
      const file=el('input');file.type='file';file.name='asset_'+slot.key;file.hidden=true;file.accept=slot.theme?'.svg,.png,.jpg,.jpeg':'image/png,image/jpeg';
      const upload=el('button','Upload image','secondary');upload.type='button';upload.setAttribute('aria-label','Upload '+slot.label.toLowerCase()+(slot.theme?' in '+slot.theme+' theme':''));upload.setAttribute('aria-describedby',source.id);upload.onclick=()=>file.click();
      const actions=el('div',undefined,'branding-image-actions'),remove=el('button','Remove upload','secondary');remove.type='button';remove.setAttribute('aria-label','Remove '+slot.label.toLowerCase()+(slot.theme?' in '+slot.theme+' theme':''));remove.onclick=()=>{delete draft.assets[slot.key];file.value='';refreshImages();renderPreview();changed();};actions.append(remove);
      file.onchange=async()=>{const selected=file.files[0];if(!selected)return;file.disabled=upload.disabled=true;pendingUploads++;save.disabled=colorSave.disabled=true;
        try{if(selected.size>512*1024)throw new Error('Image must be at most 512 KiB.');const mime=/\.svg$/i.test(selected.name)?'image/svg+xml':selected.type;if(!['image/png','image/jpeg','image/svg+xml'].includes(mime)||(!slot.theme&&mime==='image/svg+xml'))throw new Error('Choose a supported image format.');
          const encoded=await new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result.split(',')[1]);reader.onerror=()=>reject(new Error('Could not read image.'));reader.readAsDataURL(selected);});if(!current())return;draft.assets[slot.key]={mime,data:encoded};refreshImages();renderPreview();changed();FormUI.message(status,'');
        }catch(error){FormUI.message(status,error.message,'error');}finally{file.disabled=upload.disabled=busy;file.value='';pendingUploads--;save.disabled=colorSave.disabled=busy||pendingUploads>0;changed();}
      };
      box.append(title,previewBox,source,file,upload,actions);assetControls.set(slot.key,{slot,previewBox,img,text,source,remove});return box;
    }
    const previewHeader=el('div',undefined,'branding-preview-header'),previewTitle=el('h3','Preview'),themeLabel=el('label',undefined,'system-toggle branding-preview-mode'),themeToggle=el('input');themeToggle.type='checkbox';themeToggle.name='preview_light';themeToggle.checked=previewTheme==='light';themeToggle.setAttribute('aria-label','Light preview');themeLabel.append(el('span','Dark'),themeToggle,el('span','Light'));previewHeader.append(previewTitle,themeLabel);
    const preview=el('div',undefined,'branding-preview'),previewLogo=el('img'),previewName=el('h3'),previewText=el('p','Preview of panels, text, actions and feedback.'),previewAction=el('button','Primary action','primary compact');previewAction.type='button';const previewFeedback=el('div',undefined,'branding-preview-feedback');for(const [key,label]of [['success','Success'],['yellow','Warning'],['red','Error']]){const n=el('span',label);n.style.color='var(--'+key+')';previewFeedback.append(n);}preview.append(previewHeader,previewLogo,previewName,previewText,previewAction,previewFeedback);for(const checked of [false,true]){const label=el('label',undefined,'system-toggle'),switchInput=el('input'),state=el('span',checked?'On':'Off');switchInput.type='checkbox';switchInput.checked=checked;switchInput.setAttribute('aria-label','Preview switch '+(checked?'on':'off'));switchInput.onchange=()=>{state.textContent=switchInput.checked?'On':'Off';};label.append(switchInput,state);preview.append(label);}colorForm.append(preview);
    themeToggle.onchange=()=>{previewTheme=themeToggle.checked?'light':'dark';renderPreview();};
    const palettes=el('div',undefined,'branding-palettes branding-theme-columns'),paletteInputs=new Map();
    for(const theme of ['dark','light']){
      const details=el('details'),summary=el('summary',themeName(theme));details.open=true;details.append(summary);let group='';
      for(const color of data.colors){if(color.group!==group){group=color.group;details.append(el('h3',group));}const row=el('div',undefined,'branding-color control-row'),label=el('label',color.label),hex=el('input');hex.name=theme+'_'+color.key;hex.pattern='#[0-9a-fA-F]{6}';hex.maxLength=7;hex.required=true;hex.spellcheck=false;label.append(hex);const picker=el('input');picker.type='color';picker.setAttribute('aria-label',color.label+' in '+theme+' theme');
        hex.addEventListener('input',()=>{selectedPreset()[theme][color.key]=hex.value;if(/^#[0-9a-fA-F]{6}$/.test(hex.value)){picker.value=hex.value;refreshImages();renderPreview();}changed();});picker.oninput=()=>{selectedPreset()[theme][color.key]=picker.value;hex.value=picker.value;hex.dispatchEvent(new Event('input',{bubbles:true}));};row.append(label,picker);details.append(row);paletteInputs.set(theme+'_'+color.key,{hex,picker,theme,key:color.key});
      }palettes.append(details);
    }
    colorForm.append(el('h3','Colors'),palettes);
    const discard=el('button','Discard','secondary');discard.type='button';discard.onclick=()=>{Object.assign(draft,clone(appearanceValues(data.settings)));refresh();changed();FormUI.message(status,'');};
    colorDiscard.onclick=()=>{draft.presets=clone(data.settings.presets);draft.default_preset=data.settings.default_preset;if(!draft.presets.some(p=>p.id===editingID))editingID=draft.default_preset;refresh();changed();FormUI.message(colorStatus,'');};
    const actions=el('div',undefined,'form-actions');actions.append(discard);form.append(actions);
    const save=el('button','Save','primary compact');save.type='submit';form.append(save);
    const colorSave=el('button','Save','primary compact');colorSave.type='submit';colorForm.append(colorSave);
    function renderPreview(){for(const [key,value]of Object.entries(selectedPreset()[previewTheme]))if(/^#[0-9a-fA-F]{6}$/.test(value))preview.style.setProperty('--'+key,value);const image=resolveImage(draft,'logo'+(previewTheme==='light'?'_light':''));previewLogo.hidden=!image.url;if(image.url)previewLogo.src=image.url;else previewLogo.removeAttribute('src');previewLogo.alt=draft.name;previewName.textContent=draft.name;}
    function refreshImages(){for(const [key,{slot,previewBox,img,text,source,remove}]of assetControls){const resolved=resolveImage(draft,key),palette=selectedPreset()[slot.theme||'light'];for(const token of ['panel','text','line'])if(/^#[0-9a-fA-F]{6}$/.test(palette[token]))previewBox.style.setProperty('--'+token,palette[token]);img.hidden=!resolved.url;if(resolved.url&&img.getAttribute('src')!==resolved.url)img.src=resolved.url;else if(!resolved.url)img.removeAttribute('src');text.hidden=!resolved.text;text.textContent=resolved.text||'';source.textContent=resolved.note;remove.hidden=!draft.assets[key];}}
    function refresh(){for(const key of ['name','description','contact_email'])form.elements[key].value=draft[key];for(const [key,input]of widthInputs)input.value=(key==='site_max_width'&&draft[key]===undefined?1280:draft[key])??'';tree.checked=!!draft.sidebar_subsections;presetSelect.replaceChildren(...draft.presets.map(p=>new Option(presetTitle(p),p.id)),new Option('+ Create new theme',''));presetSelect.value=selectedPreset().id;presetName.value=selectedPreset().name;presetState();for(const {hex,picker,theme,key}of paletteInputs.values()){hex.value=selectedPreset()[theme][key];picker.value=/^#[0-9a-fA-F]{6}$/.test(hex.value)?hex.value:'#000000';}refreshImages();renderPreview();FormUI.refresh();ControlPickers.sync(presetSelect);}
    const read=()=>JSON.stringify(appearanceValues(draft)),colorRead=()=>JSON.stringify({presets:draft.presets,default_preset:draft.default_preset});
    function changed(){FormUI.update(form);FormUI.update(colorForm);}
    refresh();FormUI.watch(form,save,{heading,read,discard});FormUI.watch(colorForm,colorSave,{heading:colorHeading,read:colorRead,discard:colorDiscard});
    async function publish(event,colors){event.preventDefault();if(busy||pendingUploads)return;const target=colors?colorForm:form,message=colors?colorStatus:status;
      if(colors){const names=new Set();for(const preset of draft.presets){const name=preset.name.trim().toLowerCase();if(!name||preset.name!==preset.name.trim()||names.has(name)){FormUI.message(message,'Enter a unique theme name without surrounding spaces.','error');return;}names.add(name);for(const theme of ['dark','light'])for(const color of data.colors)if(!/^#[0-9a-fA-F]{6}$/.test(preset[theme][color.key])){FormUI.message(message,preset.name+': '+color.label+' must be a six-digit hex color.','error');return;}}}
      busy=true;const baseline=FormUI.snapshot(target),submitted=clone(data.settings);if(colors){submitted.presets=clone(draft.presets);submitted.default_preset=draft.default_preset;}else Object.assign(submitted,clone(appearanceValues(draft)));syncDefault(submitted);for(const owner of [form,colorForm])for(const control of owner.querySelectorAll('input,button,select'))control.disabled=true;FormUI.message(message,'');
      try{const saved=window.GatewaySite.normalize(await api('/v1/system/branding',{method:'PUT',body:JSON.stringify(submitted)}));if(!current())return;data.settings=clone(saved);draft.version=saved.version;if(colors){draft.presets=clone(saved.presets);draft.default_preset=saved.default_preset;}else Object.assign(draft,clone(appearanceValues(saved)));refresh();apply(saved);FormUI.saved(target,baseline);}
      catch(error){if(current())FormUI.message(message,error.status===409?'Another administrator changed the brand. Reopen Settings before saving.':error.message,'error');}
      finally{busy=false;for(const owner of [form,colorForm])for(const control of owner.querySelectorAll('input,button,select'))control.disabled=false;presetState();changed();FormUI.refresh();}
    }
    form.onsubmit=event=>publish(event,false);colorForm.onsubmit=event=>publish(event,true);
  }};
})();
