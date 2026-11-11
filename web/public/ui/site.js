(() => {
  'use strict';
  // Shared console/docs header: measure intrinsic content without resize feedback.
  function bindHeader(header){
    const nav=header.querySelector('nav'),brand=header.querySelector('.brand'),account=header.querySelector('.topbar-actions');
    if(!nav||!brand)return;
    nav.classList.add('section-nav');
    const wordmark=brand.querySelector('.brand-wordmark'),symbol=brand.querySelector('.brand-symbol');
    const layout=()=>{
      const visible=[...nav.children].filter(n=>!n.hidden&&getComputedStyle(n).display!=='none');
      const style=getComputedStyle(header),navStyle=getComputedStyle(nav);
      const navWidth=visible.reduce((sum,n)=>sum+n.getBoundingClientRect().width,0)+Math.max(0,visible.length-1)*(parseFloat(navStyle.columnGap)||0);
      const maximum=parseFloat(style.getPropertyValue('--brand-full-width'))||190;
      const imageWidth=image=>image?.naturalHeight?Math.min(maximum,image.naturalWidth/image.naturalHeight*parseFloat(getComputedStyle(image).height)):40;
      const controls=navWidth+(account?.getBoundingClientRect().width||0)+2*(parseFloat(style.columnGap)||0);
      const compact=imageWidth(wordmark)+controls>header.clientWidth;
      header.classList.toggle('compact-brand',compact);
      header.classList.toggle('wrapped',(compact?imageWidth(symbol):imageWidth(wordmark))+controls>header.clientWidth);
    };
    let frame;const schedule=()=>{if(!frame)frame=requestAnimationFrame(()=>{frame=undefined;layout();});};
    const observer=new ResizeObserver(schedule);[header,brand,nav,account].filter(Boolean).forEach(n=>observer.observe(n));
    new MutationObserver(schedule).observe(nav,{subtree:true,childList:true,characterData:true,attributes:true,attributeFilter:['hidden']});
    [wordmark,symbol].filter(Boolean).forEach(n=>n.addEventListener('load',schedule));
    document.fonts.ready.then(schedule);window.addEventListener('resize',schedule);schedule();
  }
  document.querySelectorAll('.topbar').forEach(bindHeader);
  // A single footer survives authentication, section changes and delayed site reads.
  const footer=document.createElement('footer');footer.className='site-footer';
  const copyright=document.createElement('span');copyright.className='site-footer-copyright';footer.append(copyright);let localFooter=false;
  function applyLegalFooter(settings){copyright.textContent='© '+new Date().getFullYear()+' '+(typeof settings?.copyright_holder==='string'&&settings.copyright_holder.trim()?settings.copyright_holder:'[Your company name]');}
  applyLegalFooter();
  const legal=document.createElement('nav');legal.setAttribute('aria-label','Legal');footer.append(legal);
  for(const [label,kind]of [['Terms of Use','terms'],['Privacy Policy','privacy']]){
    const link=document.createElement('a');link.textContent=label;link.setAttribute('aria-disabled','true');link.dataset.legalLink=kind;legal.append(link);
  }
  document.body.classList.add('site-page');(document.querySelector('.shell')||document.body).append(footer);
  let appearance,siteConfiguration;
  const systemOnly=['system','docs'].includes(document.documentElement.dataset.themeMode);
  let presetPreference='';
  if(!systemOnly){try{presetPreference=localStorage.getItem('gateway-color-preset')||'';}catch{}}
  const colorRoles={'accent-text':'accent','chart-1':'accent','switch-thumb':'on-accent','switch-thumb-on':'on-accent','switch-on-border':'accent'};
  const dangerDefaults={dark:{danger:'#f08088','on-danger':'#171717'},light:{danger:'#b42336','on-danger':'#ffffff'}};
  function paletteColors(palette,mode='dark'){const result={...palette};for(const [role,source]of Object.entries(colorRoles))if(!result[role])result[role]=result[source];for(const [role,value]of Object.entries(dangerDefaults[mode==='light'?'light':'dark']))if(!Object.prototype.hasOwnProperty.call(result,role))result[role]=value;return result;}
  function normalize(settings){const value={...settings};value.presets=settings.presets?.length?settings.presets.map(p=>({...p,dark:paletteColors(p.dark,'dark'),light:paletteColors(p.light,'light')})):[{id:'original',name:'Original',dark:paletteColors(settings.dark,'dark'),light:paletteColors(settings.light,'light')}];value.default_preset=settings.default_preset||value.presets[0].id;return value;}
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
  function targetDocs(site,hash=location.hash){
    const url=new URL(site.docs_url?'/' : '/docs',site.docs_url||site.ui_url||location.origin);
    if(hash.startsWith('#docs/'))url.hash=hash;
    return window.GatewayTheme?.docsURL(url.href)||url.href;
  }
  function targetConsole(site){if(!site.ui_url)return null;const url=new URL('/',site.ui_url);return window.GatewayTheme?.docsURL(url.href)||url.href;}
  function updateLinks(site){
    document.querySelectorAll('[data-console-link]').forEach(n=>{const url=targetConsole(site);if(url){n.href=url;n.removeAttribute('aria-disabled');}else{n.removeAttribute('href');n.setAttribute('aria-disabled','true');}});
    document.querySelectorAll('[data-docs-link]').forEach(n=>n.href=targetDocs(site));
    footer.querySelectorAll('[data-legal-link]').forEach(n=>{
      // A separate docs page cannot infer its Console host while config is unavailable.
      if(!site.ui_url)return;
      n.href=new URL('/'+n.dataset.legalLink,site.ui_url||location.origin).href;n.removeAttribute('aria-disabled');
    });
  }
  // Bound the shared promise too: reference text must not wait forever on config.
  const controller=new AbortController(),deadline=setTimeout(()=>controller.abort(),8000);
  const ready=fetch('/site.json',{credentials:'omit',signal:controller.signal}).then(async r=>{
    if(!r.ok)throw Error('Site configuration unavailable');
    const site=await r.json();window.GatewayTheme?.configure(site,r.headers.get('X-Gateway-Theme-Cookie-Domain')||'');if(site.branding)apply(site.branding);if(!localFooter)applyLegalFooter(site.legal);
    const meta=document.querySelector('meta[name="gateway-url"]');if(meta)meta.content=site.api_url||location.origin;
    // Compatibility for old bookmarks only. New links already target the docs origin.
    if(site.docs_url&&['/docs','/docs/'].includes(location.pathname)&&new URL(site.docs_url).origin!==location.origin)location.replace(targetDocs(site));
    siteConfiguration=site;updateLinks(site);if(window.GatewaySite)window.GatewaySite.current=site;return site;
  }).catch(()=>{const site={api_url:location.origin,ui_url:document.documentElement.dataset.themeMode==='docs'?'':location.origin};siteConfiguration=site;updateLinks(site);return site;}).finally(()=>clearTimeout(deadline));
  async function docsURL(hash=location.hash){return targetDocs(await ready,hash);}
  async function consoleURL(){return targetConsole(await ready);}
  async function openDocs(hash=location.hash,current=()=>true){const target=await docsURL(hash);if(current())location.assign(target);}
  let linkNavigation=0;
  for(const link of document.querySelectorAll('[data-docs-link],[data-console-link]'))link.addEventListener('click',async event=>{
    if(event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey||link.target==='_blank')return;
    event.preventDefault();const intent=++linkNavigation;const target=link.hasAttribute('data-docs-link')?await docsURL():await consoleURL();if(target&&intent===linkNavigation)location.assign(target);
  });
  new MutationObserver(()=>{if(appearance)apply(appearance);if(siteConfiguration)updateLinks(siteConfiguration);}).observe(document.documentElement,{attributes:true,attributeFilter:['data-theme']});
  window.GatewaySite={ready,docsURL,consoleURL,openDocs,apply,applyLegalFooter:settings=>{localFooter=true;applyLegalFooter(settings);},normalize,paletteColors,selectPreset,presets:()=>appearance?.presets||[],defaultPreset:()=>appearance?.default_preset||''};
})();
