(() => {
  'use strict';
  try{
    const raw=sessionStorage.getItem('gateway-login-return');
    sessionStorage.removeItem('gateway-login-return');
    if(!raw||location.pathname!=='/'||location.search||location.hash)return;
    const saved=JSON.parse(raw),target=new URL(saved.path,location.origin);
    if(target.origin!==location.origin||!['/system','/organizations','/api-keys','/configuration','/profile','/monitoring/'].includes(target.pathname)||Date.now()-saved.time>30*60*1000||!Number.isFinite(saved.time))return;
    if(target.pathname==='/monitoring/'){location.replace('/monitoring/');return;}
    history.replaceState(null,'',target.pathname+target.search+target.hash);
  }catch{ /* Sign-in still succeeds if tab storage is unavailable. */ }
})();
