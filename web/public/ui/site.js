(() => {
  'use strict';
  let appearance;
  const systemOnly=['system','docs'].includes(document.documentElement.dataset.themeMode);
  let presetPreference='';
  if(!systemOnly){try{presetPreference=localStorage.getItem('gateway-color-preset')||'';}catch{}}
  const colorRoles={'accent-text':'accent','chart-1':'accent','switch-thumb':'on-accent','switch-thumb-on':'on-accent','switch-on-border':'accent'};
  function paletteColors(palette){const result={...palette};for(const [role,source]of Object.entries(colorRoles))if(!result[role])result[role]=result[source];return result;}
  function normalize(settings){const value={...settings};value.presets=settings.presets?.length?settings.presets.map(p=>({...p,dark:paletteColors(p.dark),light:paletteColors(p.light)})):[{id:'original',name:'Original',dark:paletteColors(settings.dark),light:paletteColors(settings.light)}];value.default_preset=settings.default_preset||value.presets[0].id;return value;}
  function selectPreset(id){presetPreference=systemOnly?'':id||'';if(!systemOnly){try{localStorage.setItem('gateway-color-preset',presetPreference);}catch{}}if(appearance)apply(appearance);}
  const pageTitle=document.querySelector('title')?.dataset.pageTitle||'';
  // Resolve only uploaded assets. Empty installations never request a bundled logo.
  function resolveImage(settings,key){
    const role=key.replace(/_light$/,''),light=key.endsWith('_light'),pair=r=>light?[r+'_light',r]:[r,r+'_light'];
    let keys=pair(role);
    if(role==='symbol')keys=[...pair('symbol'),...pair('favicon'),...pair('logo')];
    if(role==='favicon')keys=[...pair('favicon'),...pair('symbol'),...pair('logo')];
    if(key==='touch_icon')keys=[key,'favicon_light','favicon','symbol_light','symbol','logo_light','logo'];
    const found=keys.find(k=>settings.assets?.[k]);
    if(!found)return null;
    const asset=settings.assets[found];
    return {url:asset.data?'data:'+asset.mime+';base64,'+asset.data:'/branding/assets/'+found+'?v='+settings.version,mime:asset.mime};
  }
  function showImage(img,image,name){
    img.alt=name||'';
    const url=image?.url;
    if(img.getAttribute('src')===url)return;
    img.hidden=true;
    img.onload=null;img.onerror=null;
    if(!url){img.removeAttribute('src');return;}
    // Keep empty space until the configured image is decoded, including on theme changes.
    img.onload=()=>{if(img.getAttribute('src')===url)img.hidden=false;};
    img.onerror=()=>{if(img.getAttribute('src')===url)img.hidden=true;};
    img.src=url;img.removeAttribute('width');img.removeAttribute('height');
    if(img.complete&&img.naturalWidth)img.hidden=false;
  }
  function apply(settings){
    settings=normalize(settings);appearance=settings;
    const root=document.documentElement,theme=root.dataset.theme==='light'?'light':'dark';
    const preset=settings.presets.find(p=>p.id===(!systemOnly&&presetPreference))||settings.presets.find(p=>p.id===settings.default_preset)||settings.presets[0];
    root.dataset.colorPreset=preset.id;
    for(const [key,value] of Object.entries(preset[theme]||{}))root.style.setProperty('--'+key,value);
    const width=settings.site_max_width===undefined?1280:settings.site_max_width;
    root.style.setProperty('--site-max-width',width==null?'100%':width+'px');
    root.style.setProperty('--site-gutter','20px');
    root.style.setProperty('--content-max-width',settings.content_max_width==null?'100%':settings.content_max_width+'px');
    window.GatewayLayout?.apply(settings);
    root.dataset.sidebarSubsections=String(!!settings.sidebar_subsections);
    document.title=(pageTitle?pageTitle+' · ':'')+(settings.name||'Media gateway');
    root.dataset.siteName=settings.name||'';
    document.querySelectorAll('span[data-site-name]').forEach(n=>n.textContent=settings.name||'This service');
    document.querySelectorAll('.brand').forEach(n=>{
      n.setAttribute('aria-label',settings.name||'Home');
      n.querySelectorAll('img').forEach(img=>showImage(img,resolveImage(settings,(img.classList.contains('brand-symbol')?'symbol':'logo')+(theme==='light'?'_light':'')),settings.name));
    });
    document.querySelectorAll('link[rel="icon"],link[rel="apple-touch-icon"]').forEach(n=>n.remove());
    for(const t of ['dark','light']){
      const image=resolveImage(settings,'favicon'+(t==='light'?'_light':''));
      if(!image)continue;
      const icon=document.createElement('link');icon.rel='icon';icon.href=image.url;icon.type=image.mime;icon.media='(prefers-color-scheme: '+t+')';document.head.append(icon);
    }
    const image=resolveImage(settings,'touch_icon');
    if(image){const touch=document.createElement('link');touch.rel='apple-touch-icon';touch.href=image.url;document.head.append(touch);}
    for(const [key,value]of [['description',settings.description],['application-name',settings.name]]){
      let meta=document.querySelector('meta[name="'+key+'"]');if(!meta){meta=document.createElement('meta');meta.name=key;document.head.append(meta);}meta.content=value||'';
    }
    document.querySelectorAll('[data-contact-details]').forEach(n=>n.hidden=!settings.contact_email);
    document.querySelectorAll('[data-contact-email]').forEach(n=>{
      n.textContent=settings.contact_email||'';
      if(settings.contact_email)n.href='mailto:'+settings.contact_email;else n.removeAttribute('href');
    });
  }
  const ready=fetch('/site.json',{credentials:'omit'}).then(async r=>{if(!r.ok)throw Error('Site configuration unavailable');const site=await r.json();window.GatewayTheme?.configure(site,r.headers.get('X-Gateway-Theme-Cookie-Domain')||'');if(site.branding)apply(site.branding);
    const meta=document.querySelector('meta[name="gateway-url"]');if(meta)meta.content=site.api_url||location.origin;
    if(site.docs_url&&['/docs','/docs/'].includes(location.pathname)&&site.docs_url!==location.origin)location.replace(window.GatewayTheme?.docsURL(site.docs_url+'/'+location.hash)||site.docs_url+'/'+location.hash);
    document.querySelectorAll('[data-console-link]').forEach(n=>n.href=(site.ui_url||'')+'/');
    if(window.GatewaySite)window.GatewaySite.current=site;
    return site;
  }).catch(()=>({api_url:location.origin,ui_url:location.origin}));
  new MutationObserver(()=>{if(appearance)apply(appearance);}).observe(document.documentElement,{attributes:true,attributeFilter:['data-theme']});
  window.GatewaySite={ready,apply,normalize,paletteColors,selectPreset,presets:()=>appearance?.presets||[],defaultPreset:()=>appearance?.default_preset||''};
})();
