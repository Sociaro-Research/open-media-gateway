(() => {
  'use strict';
  const docs=document.documentElement.dataset.themeMode==='docs';
  const systemOnly=document.documentElement.dataset.themeMode==='system';
  const media=matchMedia('(prefers-color-scheme: light)');
  const valid=value=>['dark','light','system'].includes(value);
  const cookieName='gateway-docs-theme';
  let preference='system',cookieDomain='',shared=false,sameOrigin=false,transferred=null;
  const stored=()=>{try{return localStorage.getItem('gateway-theme')}catch{return null}};
  function render(){document.documentElement.dataset.theme=preference==='system'?(media.matches?'light':'dark'):preference;}
  function cookieValue(){try{const values=document.cookie.split(';').map(v=>v.trim()).filter(v=>v.startsWith(cookieName+'='));return values.length===1?values[0].slice(cookieName.length+1):null}catch{return null}}
  function writeCookie(clear=false){if(!cookieDomain)return;try{document.cookie=cookieName+'='+(clear?'':preference)+'; Domain='+cookieDomain+'; Path=/; Secure; SameSite=Lax'+(clear?'; Max-Age=0':'')}catch{}}
  function apply(value,{share=false}={}){
    preference=!systemOnly&&valid(value)?value:'system';render();
    if(!docs&&!systemOnly){try{localStorage.setItem('gateway-theme',preference)}catch{}if(share)shared=true;if(shared)writeCookie();}
  }
  function refresh(){if(!docs)return;const value=cookieDomain?(cookieValue()||transferred):sameOrigin?(stored()||transferred):transferred;preference=valid(value)?value:'system';render();}
  function configure(site,domain=''){
    cookieDomain='';
    try{
      const ui=new URL(site.ui_url||location.origin),target=new URL(site.docs_url||ui.origin);
      sameOrigin=ui.origin===location.origin;
      // Explicit opt-in only. Both origins must be HTTPS and inside the scope.
      // The browser additionally rejects public-suffix cookie domains.
      const within=host=>host===domain||host.endsWith('.'+domain);
      if(/^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(domain)&&ui.protocol==='https:'&&target.protocol==='https:'&&within(ui.hostname)&&within(target.hostname)&&(location.origin===ui.origin||location.origin===target.origin))cookieDomain=domain;
    }catch{}
    if(shared)writeCookie();refresh();if(cookieDomain?valid(cookieValue()):sameOrigin&&valid(stored()))transferred=null;
  }
  function docsURL(value){const url=new URL(value,location.href);url.searchParams.set('theme',preference);return url.href;}
  function clear(){shared=false;writeCookie(true);preference='system';try{localStorage.removeItem('gateway-theme')}catch{}render();}
  if(docs){const url=new URL(location.href);const value=url.searchParams.get('theme');transferred=valid(value)?value:null;if(url.searchParams.has('theme')){url.searchParams.delete('theme');history.replaceState(null,'',url.pathname+url.search+url.hash);}apply(transferred);}
  else apply(stored());
  media.addEventListener('change',render);
  window.addEventListener('focus',refresh);window.addEventListener('pageshow',refresh);window.addEventListener('storage',refresh);
  window.GatewayTheme={apply,configure,docsURL,clear};
})();
