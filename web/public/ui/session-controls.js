// Descriptor-driven local session intent; provider handles and inspection stay on the host.
(() => {
 'use strict';
 const node=(tag,text='',cls='')=>Object.assign(document.createElement(tag),{textContent:text,className:cls});
 const revision=item=>item?.session?.selection_revision??0;
 const alive=item=>!!item?.session&&Number.isFinite(Date.parse(item.session.expires_at))&&Date.parse(item.session.expires_at)>Date.now();
 const ready=item=>item?.usable===true&&item.status==='succeeded'&&alive(item);
 function selectable(model,item){return ready(item)||model.session_operation?.action==='clear'&&item?.status==='failed'&&alive(item);}
 function mount(host,{model,binding,onChange=()=>{},locked=()=>false,initial=null}){
  const schema=model.session_operation;host.replaceChildren();host.className='session-controls';
  const heading=node('h3','Session action'),status=node('p','','field-hint'),fields=node('div','','session-action-fields');status.setAttribute('role','status');host.append(heading,status,fields);
  let identity='',snapshot=null,frame=null,rendering=false,points=[],recover=null,frameIndex=null,frameChoice=null;
  function field(parent,label,type='number',value=''){const wrap=node('label',label),input=document.createElement(type==='select'?'select':'input');if(type!=='select')input.type=type;input.value=value;wrap.append(input);parent.append(wrap);return input;}
  function button(parent,label,fn){const b=node('button',label,'secondary');b.type='button';b.onclick=fn;parent.append(b);return b;}
  function changed(){FormUI.refresh();if(!rendering)onChange();}
  function render(){rendering=true;try{
   fields.replaceChildren();points=[];recover=frameIndex=frameChoice=null;frame=null;
   if(!snapshot?.session)return;
   if(['add','delete'].includes(schema.action)){
    if(schema.action==='delete'){
     frameChoice=field(fields,'Marked frame','select');frameChoice.append(new Option('Choose a marked frame',''));
     for(const item of snapshot.session.selections||[])frameChoice.append(new Option('Frame '+item.frame_index,String(item.frame_index)));
     const list=node('fieldset','','session-points parameter-list'),legend=node('legend','Points to remove');list.append(legend);fields.append(list);
     const show=()=>{list.replaceChildren(legend);points=[];frame=(snapshot.session.selections||[]).find(f=>String(f.frame_index)===frameChoice.value)||null;for(const point of frame?.points||[]){const label=node('label','','checkbox-label'),check=document.createElement('input');check.type='checkbox';label.append(check,document.createTextNode('x '+point.x+' · y '+point.y));list.append(label);points.push({check,point});check.onchange=changed;}changed();};frameChoice.onchange=show;
     if(initial?.frame){frameChoice.value=String(initial.frame.frame_index);show();for(const row of points)row.check.checked=initial.frame.points?.some(p=>p.x===row.point.x&&p.y===row.point.y)||false;}
    }else{
     frameIndex=field(fields,'Frame index','number',initial?.frame?.frame_index??schema.frame_index_base);frameIndex.min=schema.frame_index_base;frameIndex.max=snapshot.session.frame_count-1+schema.frame_index_base;frameIndex.step='1';frameIndex.required=true;frameIndex.oninput=changed;
     const list=node('div','','session-points'),actions=node('div','','reference-actions');fields.append(list,actions);
     const add=button(actions,'Add point',()=>append());add.dataset.addSessionPoint='';
     function labels(){points.forEach((p,i)=>{FormUI.setLabel(p.x,'Point '+(i+1)+' x');FormUI.setLabel(p.y,'Point '+(i+1)+' y');});add.disabled=locked()||points.length>=schema.max_points;}
     function append(value={x:'',y:''},focus=true){if(points.length>=schema.max_points)return;const row=node('div','','session-point-row'),x=field(row,'Point '+(points.length+1)+' x','number',value.x),y=field(row,'Point '+(points.length+1)+' y','number',value.y),p={row,x,y};for(const input of [x,y]){input.min=0;input.max=1;input.step='any';input.required=true;input.oninput=changed;}const remove=button(row,'Remove',()=>{const i=points.indexOf(p);points.splice(i,1);row.remove();labels();(points[i]?.x||points.at(-1)?.x||add).focus();changed();});remove.setAttribute('aria-label','Remove selection point');list.append(row);points.push(p);labels();changed();if(focus)x.focus();}
     for(const point of initial?.frame?.points?.length?initial.frame.points:[{x:'',y:''}])append(point,false);
     fields.append(node('p','Normalized coordinates from 0 to 1. The server validates frame and selection limits.','field-hint'));
    }
   }
   if(schema.action==='clear'&&snapshot.status==='failed'){
    const label=node('label','','checkbox-label');recover=document.createElement('input');recover.type='checkbox';label.append(recover,document.createTextNode('Recover this failed session by clearing its selection'));recover.onchange=changed;fields.append(label);
    fields.append(node('p','This is an explicit recovery operation. A failed or uncertain mutation is never retried automatically.','field-hint'));
   }
   initial=null;FormUI.refresh();
  }finally{rendering=false;}}
  function sync(){
   const item=binding();snapshot=item;const next=item?JSON.stringify([item.id,revision(item),item.status,item.usable,item.session?.expires_at,item.session?.selections]):'';
   if(next!==identity){identity=next;render();}
   status.textContent=!item?'Choose a session from the resource library.':!alive(item)?'Session expired or its expiry is unavailable.':item.status==='failed'?'Session unavailable after a failed or uncertain operation. Only explicit recovery clear is allowed.':!ready(item)?'Session validation is pending or the resource is unavailable.':'Current session · '+(item.session.frame_count>0?item.session.frame_count+' frames':'Frame count unknown');
   for(const input of fields.querySelectorAll('input,select,button'))input.disabled=locked()||!item||!alive(item)||!selectable(model,item)||input.hasAttribute('data-add-session-point')&&points.length>=schema.max_points;
  }
  function read(){
   const item=binding();if(!item)return null;
   const result={binding_id:item.id,action:schema.action,expected_revision:revision(item)};
   if(schema.action==='add')result.frame={frame_index:Number(frameIndex?.value),points:points.map(p=>({x:Number(p.x.value),y:Number(p.y.value)}))};
   if(schema.action==='delete')result.frame={frame_index:frame?.frame_index,points:points.filter(p=>p.check.checked).map(p=>({...p.point}))};
   if(recover?.checked)result.recover=true;
   return result;
  }
  function valid(){
   const item=binding(),intent=read();if(!item||item.kind!==schema.kind||!alive(item)||!Number.isSafeInteger(revision(item))||revision(item)<0||!selectable(model,item)||item.status==='failed'&&!intent?.recover)return false;
   if(['clear','preview'].includes(schema.action))return true;
   const frame=intent?.frame,selected=item.session.selections||[],base=schema.frame_index_base;
   if(!frame||!Number.isSafeInteger(frame.frame_index)||!Number.isSafeInteger(base)||!Number.isSafeInteger(item.session.frame_count)||item.session.frame_count<=0||frame.frame_index<base||frame.frame_index>=base+item.session.frame_count||!frame.points.length||frame.points.length>schema.max_points)return false;
   if(schema.action==='add'&&(!frameIndex.validity.valid||points.some(p=>!p.x.validity.valid||!p.y.validity.valid)))return false;
   if(frame.points.some(p=>!Number.isFinite(p.x)||!Number.isFinite(p.y)||p.x<0||p.x>1||p.y<0||p.y>1)||new Set(frame.points.map(p=>JSON.stringify(p))).size!==frame.points.length)return false;
   if(schema.action==='add'&&!selected.some(f=>f.frame_index===frame.frame_index)&&selected.length>=schema.max_frames)return false;
   if(schema.action==='delete'&&!frame.points.every(p=>selected.find(f=>f.frame_index===frame.frame_index)?.points.some(q=>p.x===q.x&&p.y===q.y)))return false;
   return true;
  }
  sync();return {sync,read,valid,dispose(){host.replaceChildren();}};
 }
 window.SessionControls={mount,selectable,ready};
})();
