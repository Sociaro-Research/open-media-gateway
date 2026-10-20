/* Provider libraries share the reference picker; credentials and management API
   calls stay on the gateway. Stored URLs are never used as durable asset IDs. */
(() => {
 'use strict';
 const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 const action=(text,fn)=>{const b=node('button',text,'secondary');b.type='button';b.onclick=fn;return b;};
 const safeURL=raw=>{if(typeof raw!=='string'||!raw.trim())return '';try{const u=new URL(raw,location.origin);return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password?u.href:'';}catch{return '';}};
 function mount(o){
  let source='local',providerID='',records=[],groups=[],group='',next='',offset=0,more=false,inventory=false,canImport=false,busy=false,epoch=0,poll,controller=new AbortController(),pendingSource=null;
  const pendingSelection=new Set();
  const sourceField=node('label','Reference source'),sourceSelect=node('select');sourceSelect.setAttribute('aria-label',o.kind+' reference source');sourceSelect.append(new Option('My storage','local'),new Option('BytePlus','provider'));sourceField.append(sourceSelect);
  const panel=node('div',null,'provider-reference-library'),controls=node('div',null,'control-row');
  const groupLabel=node('label','Asset group'),groupSelect=node('select');groupSelect.setAttribute('aria-label',o.kind+' asset group');groupLabel.append(groupSelect);
  const newGroup=action('New group',()=>{createForm.hidden=!createForm.hidden;FormUI.refresh();if(!createForm.hidden)nameInput.focus();});controls.append(groupLabel,newGroup);
  const createForm=node('form',null,'control-row'),nameLabel=node('label','Group name'),nameInput=node('input'),createButton=node('button','Create group','secondary');nameInput.setAttribute('aria-label',o.kind+' group name');nameInput.maxLength=64;nameInput.required=true;nameLabel.append(nameInput);createButton.type='submit';createForm.append(nameLabel,createButton);createForm.hidden=true;
  const tools=node('div',null,'reference-actions'),refresh=action('Refresh',()=>load()),existing=action('Import existing assets',()=>switchInventory(!inventory)),register=action('Register selected file',()=>registerUploaded(pendingSource));tools.append(refresh,existing,register);register.hidden=true;existing.hidden=true;
  const info=node('p','BytePlus prepares each asset before it can be used. Uploaded originals remain in My storage.','field-explanation');panel.append(controls,createForm,tools,info);panel.hidden=true;
  o.library.querySelector('p').before(sourceField);o.library.querySelector('[data-grid]').before(panel);
  const localGrid=o.library.querySelector('[data-grid]'),remoteGrid=node('div',null,'reference-library-grid');panel.append(remoteGrid);
  function base(){return '/v1/references/providers/'+encodeURIComponent(providerID);}
  function status(text,error=false){o.message(text,error);}
  function selectedGroup(){return groupSelect.value;}
  function supports(){return !!selectedModel()?.supports_asset_references;}
  function setSource(value){source=value;sourceSelect.value=value;panel.hidden=source!=='provider';localGrid.hidden=source==='provider';o.library.querySelector('#libraryDropzone p').textContent=source==='provider'?'Drop files to register them in BytePlus':'Drop files to add them to your library';o.library.querySelector('[data-upload]').textContent=source==='provider'?'Upload to BytePlus':'Upload '+(o.kind==='audio'?'audio files':o.kind+'s');if(source==='local')o.loadLocal();else load();}
  sourceSelect.onchange=()=>setSource(sourceSelect.value);
  groupSelect.onchange=()=>{group=selectedGroup();if(inventory)load();else render();};
  createForm.onsubmit=async e=>{e.preventDefault();if(!createForm.reportValidity()||busy)return;const current=epoch;busy=true;createButton.disabled=true;try{const g=await api(base()+'/groups',{method:'POST',body:JSON.stringify({name:nameInput.value.trim()}),signal:controller.signal});if(current!==epoch)return;groups.push(g);group=g.id;renderGroups();createForm.hidden=true;nameInput.value='';render();status('Group created.');}catch(e){if(current===epoch)status(e.message,true);}finally{if(current===epoch){busy=false;createButton.disabled=false;}}};
  function renderGroups(){groupSelect.replaceChildren(new Option('Choose an asset group',''),...groups.map(g=>new Option(g.name,g.id)));groupSelect.value=groups.some(g=>g.id===group)?group:'';FormUI.refresh();}
  async function loadGroups(){const data=await api(base()+'/groups'+(inventory?'?inventory=1':''),{signal:controller.signal});groups=data.data||[];canImport=!!data.can_import;existing.hidden=!canImport;renderGroups();}
  function remember(item){if(!item.uri||!item.asset_id)return;const previous=o.metadata.get(item.uri)||{};o.metadata.set(item.uri,{...previous,...item,name:item.name||item.asset_id,connection_id:providerID,provider_binding_id:inventory?previous.provider_binding_id:item.id,preview_url:safeURL(item.preview_url)});}
  async function load(append=false){
   configure();if(source!=='provider'||busy)return;const current=epoch;busy=true;refresh.disabled=true;status('Loading BytePlus assets…');
   try{await loadGroups();if(current!==epoch)return;const url=inventory?base()+'/inventory?'+new URLSearchParams({group:group,next:append?next:''}):base()+'/assets?offset='+(append?offset:0);
    const data=await api(url,{signal:controller.signal});if(current!==epoch)return;records=append?[...records,...(data.data||[])]:data.data||[];next=data.next||'';offset=data.next_offset||records.length;more=inventory?!!next:!!data.has_more;
    records.forEach(remember);for(const record of records){if(record.status==='Active'&&pendingSelection.has(record.id)){pendingSelection.delete(record.id);if(!o.urls().includes(record.uri))o.select({...record,url:record.uri});}}
    status(records.length?'':inventory?'No assets found in BytePlus.':'No BytePlus assets in this workspace. Upload a file or import an existing asset.');render();schedule();o.renderSelected();
   }catch(e){if(current===epoch&&e.name!=='AbortError')status(e.message,true);}finally{if(current===epoch){busy=false;refresh.disabled=false;}}
  }
  function schedule(){clearTimeout(poll);if(o.library.open&&source==='provider'&&records.some(a=>['Creating','Unknown','Processing'].includes(a.status)))poll=setTimeout(()=>load(),4000);}
  function render(){
   if(source!=='provider')return;remoteGrid.replaceChildren();const values=o.urls();
   for(const item of records.filter(a=>a.type===({image:'Image',video:'Video',audio:'Audio'})[o.kind]&&(!group||a.group_id===group))){
    const card=node('div',null,'reference-card');card.dataset.assetId=item.asset_id||item.id;const choose=action('',()=>inventory?importItem(item):o.select({...item,url:item.uri}));choose.className='reference-choice';choose.setAttribute('aria-label',(inventory?'Import ':'Use ')+(item.name||item.asset_id)+(inventory?' into this workspace':' in generation'));choose.setAttribute('aria-pressed',String(values.includes(item.uri)));choose.disabled=inventory?false:item.status!=='Active';
    const preview=safeURL(item.preview_url);if(preview&&o.kind==='image'){const img=node('img');img.src=preview;img.alt=item.name;img.referrerPolicy='no-referrer';img.loading='lazy';choose.append(img);}else{const placeholder=node('div',o.kind==='image'?'Image':o.kind==='video'?'Video':'Audio','reference-placeholder');choose.append(placeholder);}
    choose.append(node('span',item.name||item.asset_id,'reference-name'),node('small',item.status==='Active'?'Ready':item.status==='Unknown'?'Checking registration':item.status||'Preparing','reference-size'));card.append(choose);if(item.error)card.append(node('p',item.error,'reference-delete-warning'));
    if(!inventory){const del=action('Delete',()=>confirmDelete(card,item));del.setAttribute('aria-label','Delete '+item.name+' from BytePlus');del.classList.add('reference-delete');del.disabled=!item.asset_id&&item.status!=='Failed';card.append(del);if(item.asset_id&&!['Creating','Processing','Unknown','Deleting'].includes(item.status)){const unlink=action('Remove from workspace',()=>removeFromWorkspace(item));unlink.setAttribute('aria-label','Remove '+item.name+' from workspace');card.append(unlink);}}
    remoteGrid.append(card);
   }
   o.more.hidden=!more;o.more.disabled=false;register.hidden=!pendingSource||inventory;register.textContent=pendingSource?'Register '+pendingSource.name:'Register selected file';register.disabled=!selectedGroup();existing.textContent=inventory?'Back to workspace assets':'Import existing assets';newGroup.hidden=inventory;
  }
  async function importItem(item){
   const current=epoch;
   try{
    status('Importing asset…');const record=await api(base()+'/import',{method:'POST',body:JSON.stringify({asset_id:item.asset_id}),signal:controller.signal});
    if(current!==epoch)return;remember(record);inventory=false;await load();
    // Refresh can be interrupted by a key or connection change too.
    if(current!==epoch)return;
    if(record.status==='Active'&&!o.urls().includes(record.uri))o.select({...record,url:record.uri});
   }catch(e){if(current===epoch)status(e.message,true);}
  }
  async function switchInventory(value){inventory=value;group='';records=[];await load();}
  async function removeFromWorkspace(item){const current=epoch;try{await api(base()+'/assets/'+encodeURIComponent(item.id)+'?detach=1',{method:'DELETE',signal:controller.signal});if(current!==epoch)return;o.write(o.urls().filter(v=>v!==item.uri));o.metadata.delete(item.uri);records=records.filter(v=>v.id!==item.id);render();status('Removed from this workspace. The asset is retained in BytePlus.');}catch(e){if(current===epoch)status(e.message,true);}}
  function confirmDelete(card,item){const text=node('p','Delete this asset permanently from BytePlus? It will no longer be available for future generations.','reference-delete-warning'),buttons=node('div',null,'reference-card-actions');buttons.append(action('Cancel',render),action('Delete asset',async()=>{const current=epoch;try{await api(base()+'/assets/'+encodeURIComponent(item.id),{method:'DELETE',signal:controller.signal});if(current!==epoch)return;o.write(o.urls().filter(v=>v!==item.uri));o.metadata.delete(item.uri);records=records.filter(v=>v.id!==item.id);render();status('Deleted from BytePlus. Any original in My storage is retained.');}catch(e){if(current===epoch)status(e.message,true);}}));card.append(text,buttons);}
  async function registerUploaded(item){
   configure();if(!item)return;pendingSource=item;if(!selectedGroup()){source='provider';sourceSelect.value=source;panel.hidden=false;localGrid.hidden=true;await load();status('Choose or create an asset group, then register '+item.name+'.');render();return;}
   const current=epoch;try{status('Registering '+item.name+' with BytePlus…');const record=await api(base()+'/assets',{method:'POST',body:JSON.stringify({reference_id:item.id,group_id:selectedGroup()}),signal:controller.signal});if(current!==epoch)return;pendingSource=null;pendingSelection.add(record.id);source='provider';sourceSelect.value=source;panel.hidden=false;localGrid.hidden=true;inventory=false;await load();status(record.status==='Unknown'?'BytePlus has not confirmed registration yet. Gateway will check its outcome without creating a duplicate.':'File registered. It will become selectable when BytePlus finishes preparing it.');}catch(e){if(current===epoch)status(e.message,true);}
  }
  function configure(){
   const id=selectedProvider()?.id||'';
   if(id!==providerID){epoch++;controller.abort();controller=new AbortController();providerID=id;records=[];groups=[];group='';busy=false;createButton.disabled=false;source='local';sourceSelect.value=source;panel.hidden=true;localGrid.hidden=false;pendingSource=null;pendingSelection.clear();clearTimeout(poll);}
   sourceField.hidden=!supports();if(!supports()&&source==='provider')setSource('local');
  }
  o.library.addEventListener('close',()=>clearTimeout(poll));
  function validate(){for(const url of o.urls()){const item=o.metadata.get(url);if(url.startsWith('asset://')&&item?.connection_id&&item.connection_id!==selectedProvider()?.id){status('This registered asset belongs to another provider connection. Select an asset from the current connection.',true);return false}}return true;}
  return {configure,load,render,prepareUpload(){if(!selectedGroup()||inventory){status('Choose or create a workspace asset group before uploading to BytePlus.',true);groupSelect.focus();return false;}return true;},fileError(file){if(o.kind==='video'&&!['video/mp4','video/quicktime'].includes(file.type))return 'BytePlus accepts MP4 or MOV videos.';if(o.kind==='audio'&&(!['audio/mpeg','audio/wav','audio/wave','audio/x-wav'].includes(file.type)||file.size>15*1024*1024))return 'BytePlus accepts MP3 or WAV audio up to 15 MB.';if(o.kind==='image'&&file.size>=30*1024*1024)return 'BytePlus image assets must be smaller than 30 MB.';return '';},setUploading(value){sourceSelect.disabled=value;groupSelect.disabled=value;newGroup.disabled=value;},active:()=>source==='provider',registerUploaded,validate,decorate(card,item){if(!supports())return;const b=action('Add to BytePlus',()=>{pendingSource=item;setSource('provider');});b.setAttribute('aria-label','Register '+item.name+' in BytePlus');card.append(b);},reset(){epoch++;controller.abort();controller=new AbortController();source='local';sourceSelect.value=source;panel.hidden=true;localGrid.hidden=false;records=[];groups=[];busy=false;createButton.disabled=false;sourceSelect.disabled=false;groupSelect.disabled=false;newGroup.disabled=false;pendingSource=null;clearTimeout(poll);pendingSelection.clear();}};
 }
 window.ProviderReferenceLibrary={mount,safeURL};
})();
