(() => {
  'use strict';
  const root=document.documentElement,desktop=matchMedia('(min-width:761px)'),bindings=new Set();
  let sequence=0;
  function prepare(){
    for(const content of document.querySelectorAll('#systemContent,.configuration-content,.docs-content')){
      content.classList.add('layout-content');
      if(!content.parentElement.classList.contains('layout-main')){const wrapper=document.createElement('div');wrapper.className='layout-main';content.before(wrapper);wrapper.append(content);}
    }
  }
  function apply(settings){
    const width=settings.site_max_width===undefined?1280:settings.site_max_width;
    root.style.setProperty('--site-max-width',width==null?'100%':width+'px');
    root.style.setProperty('--site-gutter','20px');
    root.style.setProperty('--content-max-width',settings.content_max_width==null?'100%':settings.content_max_width+'px');
    root.dataset.sidebarSubsections=String(!!settings.sidebar_subsections);
    for(const binding of bindings)binding.update();
  }
  function clear(scope){for(const binding of [...bindings])if(scope.contains(binding.scope))binding.destroy();}
  function bind(tabs,{scope,label,body,anchor,inline=false}={}){
    prepare();const marker=document.createComment('subsections'),id='subsections-'+(++sequence);tabs.before(marker);tabs.id=id;
    tabs.setAttribute('role','tablist');tabs.setAttribute('aria-label',label+' sections');
    let generated=null,workspace=null,expanded=true,vertical=false;
    if(!inline&&!anchor){
      const system=scope.closest('#systemContent');
      if(system)anchor=document.querySelector('[data-system-page=organizations]');
      else{
        workspace=document.querySelector('#configurationView');generated=document.createElement('aside');generated.className='configuration-sidebar workspace-subsections-sidebar';generated.hidden=true;
        const nav=document.createElement('nav');nav.className='configuration-nav';nav.setAttribute('aria-label',label+' navigation');anchor=document.createElement('button');anchor.type='button';anchor.textContent=label;anchor.className='active';nav.append(anchor);generated.append(nav);workspace.prepend(generated);
      }
    }
    const buttons=[...tabs.querySelectorAll('button')];
    buttons.forEach((button,index)=>{button.id||=id+'-'+index;button.setAttribute('role','tab');if(body){body.id||=id+'-panel';button.setAttribute('aria-controls',body.id);}});
    if(body)body.setAttribute('role','tabpanel');
    function sync(){for(const button of buttons){const active=button.getAttribute('aria-pressed')==='true';button.setAttribute('aria-selected',String(active));button.tabIndex=active?0:-1;if(active&&body)body.setAttribute('aria-labelledby',button.id);}}
    function disclosure(){tabs.hidden=vertical&&!expanded;if(vertical)anchor.setAttribute('aria-expanded',String(expanded));}
    function update(){
      const focus=tabs.contains(document.activeElement)?document.activeElement:null;
      vertical=!inline&&root.dataset.sidebarSubsections==='true'&&desktop.matches;
      if(vertical){anchor.after(tabs);anchor.classList.add('subsection-parent');anchor.setAttribute('aria-controls',id);tabs.classList.add('subsection-nav');}
      else{marker.after(tabs);anchor?.classList.remove('subsection-parent');anchor?.removeAttribute('aria-controls');anchor?.removeAttribute('aria-expanded');tabs.classList.remove('subsection-nav');}
      tabs.setAttribute('aria-orientation',vertical?'vertical':'horizontal');
      if(workspace){workspace.classList.toggle('has-sidebar-subsections',vertical);generated.hidden=!vertical;}
      disclosure();sync();if(focus&&!tabs.hidden)focus.focus();
    }
    function parentClick(event){if(!vertical)return;event.preventDefault();event.stopImmediatePropagation();expanded=!expanded;disclosure();}
    function parentKey(event){if(!vertical)return;if(event.key==='ArrowRight'){event.preventDefault();expanded=true;disclosure();buttons.find(b=>b.getAttribute('aria-selected')==='true')?.focus();}else if(event.key==='ArrowLeft'){event.preventDefault();expanded=false;disclosure();}}
    function key(event){
      if(vertical&&(event.key==='Escape'||event.key==='ArrowLeft')){event.preventDefault();expanded=false;disclosure();anchor.focus();return;}
      const previous=vertical?'ArrowUp':'ArrowLeft',next=vertical?'ArrowDown':'ArrowRight';if(![previous,next,'Home','End'].includes(event.key))return;
      event.preventDefault();const index=buttons.indexOf(document.activeElement),target=event.key==='Home'?0:event.key==='End'?buttons.length-1:(index+(event.key===previous?-1:1)+buttons.length)%buttons.length;
      buttons[target].click();buttons[target].focus();
    }
    anchor?.addEventListener('click',parentClick,true);anchor?.addEventListener('keydown',parentKey);tabs.addEventListener('keydown',key);tabs.addEventListener('click',sync);
    const binding={scope,update,sync,destroy(){marker.after(tabs);marker.remove();anchor?.classList.remove('subsection-parent');anchor?.removeAttribute('aria-controls');anchor?.removeAttribute('aria-expanded');anchor?.removeEventListener('click',parentClick,true);anchor?.removeEventListener('keydown',parentKey);tabs.removeEventListener('keydown',key);tabs.removeEventListener('click',sync);tabs.classList.remove('subsection-nav');tabs.hidden=false;if(workspace)workspace.classList.remove('has-sidebar-subsections');generated?.remove();bindings.delete(binding);}};
    bindings.add(binding);update();return binding;
  }
  desktop.addEventListener('change',()=>{for(const binding of bindings)binding.update();});
  prepare();window.GatewayLayout={apply,bind,clear,prepare};
})();
