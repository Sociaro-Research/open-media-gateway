(() => {
 'use strict';
 // One read at a time per view. A suspended/replaced read can never commit.
 function watch({root,active=()=>true,load,interval=15000}){
  let stopped=false,visible=false,suspended=false,timer=null,reading=null,serial=0;
  const available=()=>!stopped&&!suspended&&root.isConnected&&!document.hidden&&!root.closest('[hidden]')&&(root.tagName!=='DIALOG'||root.open)&&active();
  function cancel(){clearTimeout(timer);timer=null;serial++;if(reading){clearTimeout(reading.deadline);reading.controller.abort();reading=null;}}
  function schedule(){clearTimeout(timer);if(available())timer=setTimeout(()=>refresh(),interval);}
  function refresh({restart=false,quiet=true}={}){
   if(restart)cancel();
   if(!available())return Promise.resolve();
   if(reading&&!restart)return reading.promise;
   clearTimeout(timer);
   const controller=new AbortController(),version=++serial;
   const current=()=>version===serial&&!controller.signal.aborted&&available();
   const operation={controller,promise:null,deadline:null};reading=operation;
   operation.deadline=setTimeout(()=>{if(reading===operation){cancel();schedule();}},20000);
   operation.promise=Promise.resolve().then(()=>current()&&load({signal:controller.signal,current,quiet})).catch(()=>{}).finally(()=>{clearTimeout(operation.deadline);if(reading===operation){reading=null;schedule();}});
   return operation.promise;
  }
  function reconcile(){
   if(!root.isConnected){stop();return;}
   const next=available();if(next===visible)return;visible=next;
   if(next)refresh();else cancel();
  }
  function resume(){reconcile();if(available())refresh();}
  function hide(){suspended=true;visible=false;cancel();}
  function show(){suspended=false;resume();}
  function stop(){if(stopped)return;stopped=true;cancel();observer.disconnect();document.removeEventListener('visibilitychange',resume);window.removeEventListener('focus',resume);window.removeEventListener('pagehide',hide);window.removeEventListener('pageshow',show);}
  const observer=new MutationObserver(reconcile);observer.observe(document.body,{subtree:true,childList:true,attributes:true,attributeFilter:['hidden','open']});
  document.addEventListener('visibilitychange',resume);window.addEventListener('focus',resume);window.addEventListener('pagehide',hide);window.addEventListener('pageshow',show);
  // The caller performs its initial foreground read after mounting.
  visible=available();schedule();
  return {refresh,stop,invalidate(){cancel();schedule();}};
 }
 // Read-only results retain their nodes, native media state, disclosure state
 // and focus. Editable forms are never reconciled by background reads.
 function reconcile(parent,next){
  const focused=document.activeElement,owned=parent.contains(focused);
  const keyed=new Map([...parent.children].filter(n=>n.dataset.updateKey).map(n=>[n.dataset.updateKey,n]));
  let position=parent.firstChild;
  for(const incoming of [...next.childNodes]){
   let node=incoming.nodeType===1&&incoming.dataset.updateKey?keyed.get(incoming.dataset.updateKey):position;
   if(!node||node.nodeType!==incoming.nodeType||node.nodeName!==incoming.nodeName||(node.nodeType===1&&node.dataset.updateKey!==incoming.dataset.updateKey)){node=incoming;parent.insertBefore(node,position);}
   else{
    if(node!==position){if(parent.moveBefore)parent.moveBefore(node,position);else parent.insertBefore(node,position);}
    if(node.nodeType===3){if(node.textContent!==incoming.textContent)node.textContent=incoming.textContent;}
    else if(node.nodeType===1&&!node.classList.contains('copy-icon')){
     for(const attr of [...node.attributes])if(!incoming.hasAttribute(attr.name)&&!(node.tagName==='DETAILS'&&attr.name==='open'))node.removeAttribute(attr.name);
     for(const attr of incoming.attributes)if(!(node.tagName==='DETAILS'&&attr.name==='open')&&node.getAttribute(attr.name)!==attr.value)node.setAttribute(attr.name,attr.value);
     node.onclick=incoming.onclick;
     reconcile(node,incoming);
    }
   }
   position=node.nextSibling;
  }
  while(position){const next=position.nextSibling;position.remove();position=next;}
  if(owned&&focused.isConnected&&document.activeElement!==focused)focused.focus({preventScroll:true});
 }
 window.GatewayAutoUpdate={watch,reconcile};
})();
