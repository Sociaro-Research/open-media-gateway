(() => {
 'use strict';
 const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(cls)n.className=cls;return n;};
 function validURL(value){
  if(!value||/[\s\x00-\x1f\x7f]/u.test(value)||/%0[ad]/i.test(value))return false;
  try{const u=new URL(value);return !u.username&&!u.password&&(['http:','https:'].includes(u.protocol)?!!u.hostname:['mailto:','tel:'].includes(u.protocol)&&!!u.pathname);}catch{return false;}
 }
 // Only unambiguous email addresses gain a scheme; never guess web/user links.
 function contactURL(value){
  const email=document.createElement('input');email.type='email';email.value=value;
  if(value.includes('@')&&!value.includes(':')&&email.validity.valid){const at=value.lastIndexOf('@');return 'mailto:'+encodeURIComponent(value.slice(0,at))+'@'+value.slice(at+1);}
  return value;
 }
 function links(contacts){
  const list=el('ul',null,'contact-links');
  for(const c of contacts||[]){if(!validURL(c.url))continue;const item=el('li'),a=el('a',c.label,'secondary contact-link');a.href=c.url;a.target='_blank';a.rel='noopener noreferrer';a.referrerPolicy='no-referrer';item.append(a);list.append(item);}
  return list;
 }
 function editor(parent,getUser,saveUser){
  const form=el('form',null,'profile-section profile-contacts'),heading=FormUI.sectionHeading(form,'Contact links'),save=el('button','Save','secondary'),status=el('p'),help=el('p','Optional. These links are shared with your company’s client manager and people who can view company members. Add a label and an email address or a full link, for example https://t.me/name, mailto:name@example.com or tel:+15551234567. Choose Save to publish your changes.','field-explanation');save.type='submit';heading.parentElement.append(save);
  const wrap=el('div',null,'docs-table-wrap'),table=el('table',null,'docs-table contact-editor'),thead=el('thead'),head=el('tr'),tbody=el('tbody');
  for(const title of ['Label','Link','']){const th=el('th',title);th.scope='col';head.append(th);}thead.append(head);table.append(thead,tbody);wrap.append(table);
  const add=el('button','Add contact','secondary');add.type='button';form.append(help,wrap,add,status);parent.append(form);
  const values=()=>[...tbody.rows].map(row=>({label:row.querySelector('[name=contact_label]').value,url:row.querySelector('[name=contact_url]').value}));
  let busy=false;
  function refresh(){add.disabled=busy||tbody.rows.length>=20;FormUI.refresh();FormUI.update(form);}
  function row(value={label:'',url:''},focus=false){
   const tr=el('tr'),label=el('input'),url=el('input'),remove=el('button','Remove','secondary');label.name='contact_label';url.name='contact_url';label.required=url.required=true;label.maxLength=80;url.maxLength=2048;label.setAttribute('aria-label','Contact label');url.setAttribute('aria-label','Contact link');url.autocomplete='url';url.spellcheck=false;label.value=value.label;url.value=value.url;
   remove.type='button';remove.setAttribute('aria-label','Remove contact');remove.onclick=()=>{const next=tr.nextElementSibling?.querySelector('input')||tr.previousElementSibling?.querySelector('input')||add;tr.remove();refresh();next.focus();};
   for(const input of [label,url]){const td=el('td');td.append(input);tr.append(td);}const actions=el('td',null,'table-row-actions');actions.append(remove);tr.append(actions);tbody.append(tr);
   const validate=()=>{label.setCustomValidity(label.value.trim()?'':'Enter a contact label.');url.setCustomValidity(validURL(contactURL(url.value.trim()))?'':'Enter an email address or a full https:, http:, mailto: or tel: link.');};
   for(const input of [label,url])input.addEventListener('input',validate);validate();
   if(focus){refresh();label.focus();}
  }
  for(const value of getUser()?.contacts||[])row(value);
  add.onclick=()=>{if(!add.disabled)row(undefined,true);};
  FormUI.watch(form,save,{read:()=>JSON.stringify(values())});refresh();
  form.onsubmit=async event=>{
   event.preventDefault();if(busy||save.disabled||!form.reportValidity())return;
   const snapshot=FormUI.snapshot(form),contacts=values().map(c=>({label:c.label.trim(),url:contactURL(c.url.trim())}));busy=true;save.disabled=true;FormUI.message(status,'');for(const control of form.elements)control.disabled=true;
   try{const result=await api('/v1/me',{method:'PATCH',body:JSON.stringify({contacts})});saveUser({contacts:result.contacts||[]});FormUI.saved(form,snapshot);}
   catch(error){FormUI.message(status,error.message,'error');}
   finally{busy=false;for(const control of form.elements)control.disabled=false;refresh();}
  };
 }
 async function support(parent,api,organization,current){
  FormUI.sectionHeading(parent,'Support');const status=el('p','Loading…');status.setAttribute('role','status');parent.append(status);
  async function load(){status.textContent='Loading…';try{const data=await api('/v1/organizations/'+organization.id+'/support');if(!current())return;status.remove();
   const card=el('section',null,'support-card'),person=data.client_manager;
   if(!person){card.append(el('h3','Your support team'),el('p','A client manager has not been assigned to your company yet. Ask your company administrator for help.','field-explanation'));parent.append(card);return;}
   card.append(el('p','Your client manager','support-eyebrow'),UserUI.identity(person));
   const contacts=links(person.contacts);if(contacts.children.length)card.append(el('p','Have a question? Your client manager is here to help. Choose any of the contact options below to get in touch.','field-explanation'),contacts);else card.append(el('p','Your client manager has not added contact links yet. Ask your company administrator to help you get in touch.','field-explanation'));parent.append(card);
  }catch(error){if(!current())return;FormUI.message(status,error.message,'error');const retry=el('button','Retry','secondary');retry.type='button';retry.onclick=()=>{retry.remove();load();};parent.append(retry);}}
  await load();
 }
 window.ContactLinks={editor,links,support};
})();
