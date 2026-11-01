(() => {
  'use strict';
  const workspaceCode=location.hash.match(/^#workspace_authorize=([A-Z2-7]{13})$/)?.[1];if(workspaceCode){try{sessionStorage.setItem('gateway-workspace-authorization',JSON.stringify({code:workspaceCode,time:Date.now()}));}catch{}history.replaceState(null,'',location.pathname+location.search);}

  // Keep invitation behavior when only the sign-in shell is loaded.
  if(location.pathname==='/organizations'&&location.hash.startsWith('#invite=')){
    try{sessionStorage.setItem('gateway-pending-invite',location.hash.slice(8));}catch{}
    history.replaceState(null,'',location.pathname+location.search);
  }
  // Google returns to /. Remember only known console destinations, never email
  // verification/reset tokens or arbitrary redirect URLs.
  if(['/system','/organizations','/api-keys','/configuration','/profile','/monitoring','/monitoring/','/monitoring/login'].includes(location.pathname)){
    const query=new URLSearchParams(location.search),safe=new URLSearchParams();
    for(const key of ['payment','organization','canceled'])if(query.has(key))safe.set(key,query.get(key));
    const fragment=['#providers','#storage','#keys','#reports'].includes(location.hash)?location.hash:'';
    const path=location.pathname.startsWith('/monitoring')?'/monitoring/':location.pathname+(safe.size?'?'+safe:'')+fragment;
    try{sessionStorage.setItem('gateway-login-return',JSON.stringify({path,time:Date.now()}));}catch{}
  }
  document.querySelector('#authError').hidden=!new URLSearchParams(location.search).has('auth_error');
  window.EmailLogin.initialize();
})();
