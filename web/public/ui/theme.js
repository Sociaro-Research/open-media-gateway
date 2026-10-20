(() => {
  'use strict';
  const systemOnly=document.documentElement.dataset.themeMode==='system';
  const media=matchMedia('(prefers-color-scheme: light)');
  let preference='system';
  function render(){document.documentElement.dataset.theme=preference==='system'?(media.matches?'light':'dark'):preference;}
  function apply(value){preference=!systemOnly&&['dark','light','system'].includes(value)?value:'system';render();if(!systemOnly){try{localStorage.setItem('gateway-theme',preference)}catch{}}}
  if(!systemOnly){try{preference=localStorage.getItem('gateway-theme')||'system'}catch{}}
  apply(preference);media.addEventListener('change',render);
  window.GatewayTheme={apply};
})();
