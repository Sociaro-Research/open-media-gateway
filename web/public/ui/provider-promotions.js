(() => {
 'use strict';
 const guide='https://github.com/Sociaro-Research/open-media-gateway/blob/main/docs/provider-development.md';
 const prefix='gateway-provider-promotions-v1:';
 const dismissed=new Set();let actor='',canConnect=false,root;
 const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
 const key=id=>prefix+encodeURIComponent(actor)+':'+id;
 function hidden(id){try{return dismissed.has(key(id))||localStorage.getItem(key(id))==='1';}catch{return dismissed.has(key(id));}}
 function dismiss(id,card){
  dismissed.add(key(id));try{localStorage.setItem(key(id),'1');}catch{/* Keep dismissal usable when storage is unavailable. */}
  card.hidden=true;root.hidden=![...root.children].some(n=>!n.hidden);
  const next=[...root.querySelectorAll('a,button')].find(n=>!n.closest('article').hidden&&!n.disabled);
  if(next)next.focus();else{const heading=document.querySelector('#providersView h1');if(heading){heading.tabIndex=-1;heading.focus();}}
 }
 function card(id,title,copy,action){
  const area=node('article',undefined,'card provider-promotion');area.dataset.promotion=id;
  const heading=node('h2',title),close=node('button','×','icon-close');heading.id='provider-promotion-'+id;
  area.setAttribute('aria-labelledby',heading.id);close.type='button';close.setAttribute('aria-label','Dismiss '+title);close.onclick=()=>dismiss(id,area);
  area.append(heading,close,node('p',copy),action);return area;
 }
 function render(options={}){
  canConnect=!!options.canConnect;const hero=document.querySelector('#providersView .providers-hero');if(!hero)return;
  if(!root){root=node('div',undefined,'provider-promotions');root.setAttribute('aria-label','Provider options');hero.after(root);
   const link=node('a','Provider integration guide','secondary');link.href=guide;link.target='_blank';link.rel='noopener noreferrer';
   const connect=node('button','Connect to Sociaro','primary compact');connect.type='button';
   const message=node('p',undefined,'form-message');message.setAttribute('role','status');
   const available=()=>canConnect&&!service.hidden&&!root.hidden&&!document.querySelector('#providersView')?.hidden;
   connect.onclick=async()=>{if(!available()||connect.disabled)return;const user=actor,navigation=consoleNavigation,load=providerLoadSequence,current=()=>available()&&actor===user&&consoleNavigation===navigation&&providerLoadSequence===load;connect.disabled=true;message.textContent='';try{await window.SetupSelector.open({selection:'sociaro',current});}catch(error){if(current())FormUI.message(message,error.message,'error');}finally{connect.disabled=false;}};
   const contribution=card('contribute','Missing a provider?','Add it to our open-source project with a pull request.',link);
   const service=card('sociaro','Skip the provider setup','Connect to Sociaro for ready-to-use providers, exclusive models and pricing.',connect);service.append(message);
   root.append(contribution,service);
  }
  for(const item of root.children)item.hidden=hidden(item.dataset.promotion)||(item.dataset.promotion==='sociaro'&&!canConnect);
  root.hidden=![...root.children].some(n=>!n.hidden);
 }
 window.ProviderPromotions={identify(user){actor=user?.id||'local';if(root)render({canConnect:false});},render};
})();
