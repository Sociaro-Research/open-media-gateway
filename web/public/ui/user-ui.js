(() => {
  'use strict';
  const people=new Map();
  const name=user=>user.name||user.email||user.id||'Unknown user';
  function remember(user){
    if(!user)return {};
    const key=user.id||user.email,previous=people.get(key)||people.get(user.email)||{};
    // Reuse only authorized photo metadata. Never replace a historical label
    // or infer a hidden person's name/email from another screen's response.
    const person={...user};if(person.picture===undefined)person.picture=previous.picture;
    const photo={picture:person.picture};if(key)people.set(key,photo);if(user.email)people.set(user.email,photo);
    return person;
  }
  function avatar(user){
    user=remember(user);const wrap=document.createElement('span'),initials=document.createElement('span');
    wrap.className='user-avatar';wrap.setAttribute('aria-hidden','true');
    initials.textContent=name(user).trim().split(/\s+/).slice(0,2).map(part=>Array.from(part)[0]||'').join('').toUpperCase();wrap.append(initials);
    if(user.picture){try{const url=new URL(user.picture,location.href);if(['http:','https:'].includes(url.protocol)){const image=document.createElement('img');image.alt='';image.draggable=false;image.referrerPolicy='no-referrer';image.hidden=true;image.onload=()=>{initials.hidden=true;image.hidden=false;};image.onerror=()=>image.remove();image.src=url.href;wrap.append(image);}}catch{}}
    return wrap;
  }
  function identity(user,{label,secondary=false}={}){
    user=remember(user);const wrap=document.createElement('span'),text=document.createElement('span'),primary=document.createElement('span');
    wrap.className='user-reference';text.className='user-reference-text';primary.textContent=label||name(user);text.append(primary);
    if(secondary&&user.email&&user.email!==primary.textContent){const email=document.createElement('small');email.textContent=user.email;text.append(email);}
    wrap.append(avatar(user),text);return wrap;
  }
  function option(user,label=name(user)){
    user=remember(user);const item=new Option(label,user.id||user.email||'');item.userIdentity=user;return item;
  }
  window.UserUI={avatar,identity,option,remember,name};
})();
