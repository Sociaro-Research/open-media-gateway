(() => {
  'use strict';
  const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!=null)e.textContent=text;if(cls)e.className=cls;return e;};
  const normalizeEndpoint=value=>{const endpoint=value.trim().replace(/\/+$/,'');return endpoint.startsWith('//')?'https:'+endpoint:endpoint&&!endpoint.includes('://')&&!/^https?:/i.test(endpoint)?'https://'+endpoint:endpoint;};

  const serviceFor=endpoint=>{let host='';try{host=new URL(normalizeEndpoint(endpoint||'')).hostname;}catch{}return !endpoint?'aws':host.endsWith('.r2.cloudflarestorage.com')?'r2':host.endsWith('.backblazeb2.com')?'b2':/(^|\.)s3([.-][a-z0-9-]+)*\.amazonaws\.com(\.cn)?$/.test(host)?'aws':'custom';};
  const providers={
    aws:{endpoint:false,region:true,access:'Access key ID',secret:'Secret access key',help:'Copy the Bucket name and Region from Amazon S3, and the Access key ID and Secret access key from IAM.'},
    r2:{endpoint:true,region:false,access:'Access Key ID',secret:'Secret Access Key',help:'Copy the S3 API endpoint from Cloudflare R2 and the Bucket name from your bucket. Use the Access Key ID and Secret Access Key from an R2 API token with Object Read & Write permission.'},
    b2:{endpoint:true,region:false,access:'keyID',secret:'applicationKey',help:'Copy S3 Endpoint and Bucket Name from Backblaze → Buckets. Region is detected automatically. In App Keys, copy keyID and applicationKey; keyName is only a label and is not needed. Use an application key with read, write and delete access, not the master application key.'},
    custom:{endpoint:true,region:true,access:'Access key ID',secret:'Secret access key',help:'Use the S3 endpoint, signing region, bucket name and credentials supplied by your storage service.'}
  };

  // One editor for organization, personal and system storage. The caller supplies
  // the destination choices and API scope; fields and interaction stay identical.
  async function open(parent,{base,heading,organization=false,allowLocal=false,isCurrent=()=>true,legacyIDs=false}){
    const section=node('section',null,'storage-settings');parent.append(section);
    const current=()=>section.isConnected&&isCurrent();
    const message=node('p');message.setAttribute('role','status');section.append(message);
    try{
      const result=await api(base);if(!current())return;
      let saved=organization?result.settings:result;
      const managedDriver=organization?'gateway':'local';
      const allowance=organization?(result.gateway_allowance_bytes==null?'Free storage with no size limit.':`${StorageQuota.size(result.gateway_allowance_bytes)} included free.`):'';
      const choices=[...(organization||allowLocal?[[managedDriver,organization?'Gateway storage':'Local storage',allowance]]:[]),['s3','Own S3 Compatible Storage','']];
      if(organization&&!result.can_manage){
        const card=node('div',null,'storage-choice storage-choice-current'),header=node('div',null,'storage-choice-header'),copy=node('span');
        copy.append(node('strong',saved.driver==='s3'?'Own S3 Compatible Storage':'Gateway storage'));
        if(saved.driver!=='s3')copy.append(node('span',allowance));
        header.append(copy);card.append(header);section.insertBefore(card,message);return;
      }
      const form=node('form',null,'storage-settings-form'),cards=node('div',null,'storage-choices'),radios=[],inputs={};let ownStorage,busy=false;
      form.autocomplete='off';if(legacyIDs){form.id='storageForm';message.id='storageMessage';}
      const initialMode=choices.some(([value])=>value===saved.driver)?saved.driver:'s3';
      for(const [value,title,text] of choices){
        const card=node('div',null,'storage-choice'),header=node('label',null,'storage-choice-header'),radio=node('input'),copy=node('span');
        radio.type='radio';radio.name='destination';radio.value=value;radio.checked=initialMode===value;radios.push(radio);
        copy.append(node('strong',title));if(text)copy.append(node('span',text));header.append(radio,copy);card.append(header);cards.append(card);if(value==='s3')ownStorage=card;
      }
      form.append(cards);
      const fields=node('fieldset',null,'storage-s3-fields');
      const serviceLabel=node('label','Storage provider','storage-provider-field'),service=node('select');service.name='service';
      for(const [value,title] of [['aws','Amazon S3'],['r2','Cloudflare R2'],['b2','Backblaze B2'],['custom','Custom S3']])service.append(new Option(title,value));
      service.value=saved.driver==='s3'?serviceFor(saved.endpoint):'custom';if(legacyIDs)service.id='storagePreset';serviceLabel.append(service);fields.append(serviceLabel);
      const help=node('p',null,'field-explanation storage-provider-help');fields.append(help);
      const advanced=node('details',null,'storage-options'),advancedFields=node('div',null,'storage-options-fields');advanced.append(node('summary','Optional settings'),advancedFields);
      const wrappers={};
      const fieldSpecs=[['endpoint','S3 Endpoint','text','Endpoint'],['region','Region','text','Region'],['bucket','Bucket name','text','Bucket'],['access_key_id','Access key ID','text','AccessKey'],['secret_access_key','Secret access key','text','SecretKey'],['prefix','Folder prefix (optional)','text','Prefix']];
      for(const [name,label,type,id] of fieldSpecs){
        const wrap=node('label',label),input=node('input');input.name=name;input.type=type;input.value=name==='secret_access_key'?'':saved[name]||'';input.autocomplete='off';input.spellcheck=false;input.required=['region','bucket'].includes(name);
        if(legacyIDs)input.id='storage'+id;
        if(name==='endpoint')input.inputMode='url';
        if(name==='secret_access_key')FormUI.credentials.render(input,saved.credentials_configured);
        inputs[name]=input;wrappers[name]=wrap;wrap.append(input);(name==='prefix'?advancedFields:fields).append(wrap);
      }
      const pathLabel=node('label',null,'storage-checkbox'),path=node('input');path.type='checkbox';path.name='path_style';path.checked=saved.path_style??true;if(legacyIDs)path.id='storagePathStyle';pathLabel.append(path,document.createTextNode('Use path-style addressing'));fields.append(pathLabel,advanced);ownStorage.append(fields);
      const save=node('button','Save','primary compact'),check=node('button','Test connection','secondary'),actions=node('div',null,'form-actions');save.type='submit';check.type='button';if(legacyIDs){save.id='saveStorage';check.id='checkStorage';}
      const setup=node('button','Set up browser uploads','secondary');setup.type='button';setup.hidden=true;
      const cors=node('details',null,'field-explanation'),corsTitle=node('summary','Direct upload setup');
      cors.append(corsTitle,node('p',`On save, Gateway configures browser uploads for this site using your storage key and keeps existing bucket rules. The key needs permission to read and update bucket CORS if browser access is missing. The bucket stays private. Storage must support multipart uploads with SHA-256 checksums and allow listing and aborting unfinished uploads.`));
      actions.append(save);fields.append(check,setup,cors);form.append(actions,node('p','New files use the selected storage. Existing files stay where they are.','field-explanation'));section.insertBefore(form,message);
      const mode=()=>radios.find(r=>r.checked).value;
      const validateEndpoint=()=>{
        const value=normalizeEndpoint(inputs.endpoint.value);let url;try{url=new URL(value);}catch{}
        const b2Region=url?.hostname.match(/^s3\.([a-z0-9-]+)\.backblazeb2\.com$/)?.[1];
        let error='';
        if(service.value==='b2'){
          inputs.region.value=b2Region||'';
          if(value&&(!b2Region||url.protocol!=='https:'||url.pathname!=='/'||url.search||url.hash||url.username||url.password||url.port))error='Copy S3 Endpoint from Backblaze → Buckets, for example s3.us-west-004.backblazeb2.com.';
        }else if(service.value==='r2'){
          inputs.region.value='auto';
          if(value&&(!url||url.protocol!=='https:'||!url.hostname.endsWith('.r2.cloudflarestorage.com')||url.pathname!=='/'||url.search||url.hash||url.username||url.password||url.port))error='Copy the S3 API endpoint from Cloudflare R2, for example https://<account-id>.r2.cloudflarestorage.com.';
        }
        inputs.endpoint.setCustomValidity(error);
      };
      const configureProvider=()=>{
        const provider=providers[service.value];help.textContent=provider.help;
        for(const name of ['endpoint','region']){wrappers[name].hidden=!provider[name];inputs[name].required=provider[name];}
        inputs.access_key_id.required=true;
        inputs.secret_access_key.required=!saved.credentials_configured||service.value!==serviceFor(saved.endpoint);
        FormUI.setLabel(inputs.access_key_id,provider.access);FormUI.setLabel(inputs.secret_access_key,provider.secret);
        pathLabel.hidden=service.value!=='custom';
        validateEndpoint();
      };
      const update=()=>{fields.hidden=mode()!=='s3';fields.disabled=fields.hidden||busy;for(const [name,input] of Object.entries(inputs))input.disabled=wrappers[name].hidden;path.disabled=pathLabel.hidden;radios.forEach(r=>r.disabled=busy);save.disabled=check.disabled=busy;FormUI.update(form);};
      const payload=()=>mode()!=='s3'?{driver:mode()}:{driver:'s3',...Object.fromEntries(Object.entries(inputs).map(([name,e])=>[name,name==='secret_access_key'?FormUI.credentials.replacement(e):name==='endpoint'?normalizeEndpoint(e.value):e.value.trim()])),prefix:inputs.prefix.value.trim().replace(/^\/+|\/+$/g,''),path_style:path.checked};
      service.onchange=()=>{inputs.endpoint.value='';inputs.region.value=service.value==='r2'?'auto':service.value==='aws'?'us-east-1':'';path.checked=service.value!=='aws';inputs.access_key_id.value='';FormUI.credentials.render(inputs.secret_access_key,false);configureProvider();setup.hidden=true;FormUI.message(message,'');FormUI.refresh();update();};
      configureProvider();update();FormUI.watch(form,save,{heading,read:()=>JSON.stringify(payload())});FormUI.refresh();
      inputs.endpoint.addEventListener('input',()=>{validateEndpoint();FormUI.update(form);});
      form.addEventListener('change',update);form.addEventListener('input',()=>{setup.hidden=true;FormUI.message(message,'');});
      async function submit(checking){
        if(busy||!current()||!form.reportValidity())return;
        busy=true;update();FormUI.message(message,checking?'Checking connection…':'Saving…');
        try{
          const data=await api(base+(checking?'/check':''),{method:checking?'POST':'PUT',body:JSON.stringify(payload())});if(!current())return;
          if(!checking){saved={...saved,...data};for(const [name,input] of Object.entries(inputs)){if(name==='secret_access_key')FormUI.credentials.render(input,saved.credentials_configured);else input.value=saved[name]||'';}path.checked=saved.path_style??true;if(saved.driver==='s3')service.value=serviceFor(saved.endpoint);configureProvider();FormUI.saved(form);FormUI.refresh();window.dispatchEvent(new Event('gateway-storage-change'));}
          setup.hidden=true;
          if(!checking&&data.browser_uploads_pending){
            FormUI.message(message,'Settings saved. Waiting for browser uploads to become available…');
            const deadline=Date.now()+60000;let verified=false;
            while(current()&&Date.now()<deadline){
              try{await api(base+'/check',{method:'POST',body:JSON.stringify(payload()),signal:AbortSignal.timeout(Math.max(1,deadline-Date.now()))});verified=true;break;}
              catch(e){if(e.code!=='storage_browser_setup_required'&&e.name!=='TimeoutError'){throw new Error('Settings saved. '+e.message);}}
              if(current()&&Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,Math.min(3000,deadline-Date.now())));
            }
            if(current())FormUI.message(message,verified?'Browser uploads are ready.':'Settings saved. Storage is still applying browser access. Try Test connection in a minute.');
          }else FormUI.message(message,checking?'Connection verified.':'');
        }catch(e){if(current()){setup.hidden=e.code!=='storage_browser_setup_required';FormUI.message(message,e.message,'error');}}finally{busy=false;if(current())update();}
      }
      form.onsubmit=e=>{e.preventDefault();submit(false)};check.onclick=()=>submit(true);setup.onclick=()=>submit(false);
    }catch(e){if(current())FormUI.message(message,e.message,'error');}
  }
  window.StorageSettings={open};
})();
