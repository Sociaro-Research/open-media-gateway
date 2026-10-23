(() => {
 'use strict';
 window.InvitationFeedback={async created(target,current=()=>target.isConnected){
  if(!current())return;
  target.classList.remove('settings-warning');FormUI.message(target,'Invitation created.');
  const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),5000);
  let text='Invitation created.',warning=false;
  try{
   // Use the same effective methods as the sign-in page, including Google.
   const response=await fetch('/auth/methods',{credentials:'same-origin',cache:'no-store',signal:controller.signal});
   if(!response.ok)throw Error('Sign-in configuration unavailable');
   const methods=await response.json();
   if(typeof methods.email!=='boolean'||typeof methods.google!=='boolean')throw Error('Invalid sign-in configuration');
   if(!methods.email&&!methods.google){text+=' No sign-in method is enabled. An Administrator must enable one so recipients can sign in.';warning=true;}
  }catch{
   text+=' Could not check sign-in availability. Ask an Administrator to check the site’s Login methods settings.';warning=true;
  }finally{clearTimeout(timer);}
  if(!current())return;
  target.classList.toggle('settings-warning',warning);FormUI.message(target,text);
 }};
})();
