(() => {
  const content = document.querySelector('#reportsContent');
  let epoch = 0;
  const node = (tag, text, className) => { const el = document.createElement(tag); if (text !== undefined) el.textContent = text; if(className)el.className=className; return el; };
  const usd = value => value == null ? 'Not yet known' : new Intl.NumberFormat('en-US', {style:'currency',currency:'USD',minimumFractionDigits:0,maximumFractionDigits:6}).format(value);
  function settlement(cost) {
    if (!cost || cost.actual_usd == null) return 'Awaiting provider bill';
    if (cost.actual_source === 'free_provider') return 'Free test provider';
    if (cost.actual_source === 'provider_failed_no_charge') return 'Rejected before generation';
    if (cost.package_units > 0) return cost.actual_usd === 0 ? 'Covered by package / free allowance' : 'Partly covered by package / free allowance; charged '+usd(cost.actual_usd);
    return cost.actual_usd === 0 ? 'Zero charge reported; reason unavailable' : 'Charged '+usd(cost.actual_usd);
  }
  function tokens(usage){
    const values=(usage?.metrics||[]).filter(m=>m.unit==='token');
    if(!values.length)return 'Not reported';
    const block=node('div');
    for(const metric of values){const label=metric.name.replaceAll('_',' ');block.append(node('small',`${label}: ${new Intl.NumberFormat('en-US').format(metric.quantity)}`));}
    return block;
  }
  async function open(shared=false,scope=null,selection=null,filters=null,view='reports') {
    const content=scope?.parent||document.querySelector('#reportsContent');
    const token = ++epoch;window.ReportDetails?.close();
    const usage=view==='usage';
    shared=!scope&&(shared||systemAdministrator);
    let organizations,organizationID,memberReport;
    content.textContent='Loading organizations…';
    try {
      organizations=scope?(scope.personal?[]:[scope.organization]):await window.GatewayOrganizations.list(shared);
      if(token!==epoch)return;
      if(!shared&&!scope?.personal&&!organizations.length){content.textContent='No organizations available for reports.';return;}
      organizationID=scope?.personal?'':selection??(scope?.organization.id||(shared?'':organizations[0].id));
      if(organizationID&&!organizations.some(o=>o.id===organizationID)){content.textContent='This organization is no longer available.';return;}
      memberReport=!shared&&['member','developer'].includes(organizations.find(o=>o.id===organizationID)?.role);
    }catch(e){if(token===epoch)content.replaceChildren(node('p',e.message,'operation-error'));return;}
    let request = 0, offset = 0, hasMore = false, hasHistory = false;
    const heading=scope?'h2':'h1';
    content.innerHTML = `<div class="report-heading console-section-heading"><${heading}>${usage?'Usage':'Reports'}</${heading}>${usage?'':'<button type="button" class="secondary" data-export disabled hidden>Download CSV</button>'}</div>
      <form class="report-filters"><div><label for="reportOrganization">Organization</label><select id="reportOrganization"></select></div><div><label for="reportFrom">From</label><input id="reportFrom" type="date" required></div><div><label for="reportTo">To</label><input id="reportTo" type="date" required></div><div><label for="reportKey">API key</label><select id="reportKey"><option value="">All keys</option></select></div><button class="secondary" type="submit">Apply</button></form>
      <p role="status"></p>${usage?'<div class="report-summary-host"></div>':'<p class="report-columns-status" data-columns-status role="status"></p><div class="docs-table-wrap" hidden><table class="docs-table report-table"><thead><tr><th class="report-columns-cell"><select multiple aria-label="Columns" id="reportColumns" hidden></select><button type="button" class="secondary report-columns-button" aria-label="Columns" title="Choose columns"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="m9 3-.7 2.3-2 .9-2.2-.9-2 3.4 1.6 1.8v2.3L2.1 15l2 3.4 2.2-.9 2 .9.7 2.6h4l.7-2.6 2-.9 2.2.9 2-3.4-1.6-2.2v-2.3l1.6-1.8-2-3.4-2.2.9-2-.9L13 3Z"/><circle cx="11" cy="12" r="3"/></svg></button></th></tr></thead><tbody></tbody></table></div><div class="report-pagination" hidden><button type="button" class="secondary" data-prev>Previous</button><span data-page></span><button type="button" class="secondary" data-next>Next</button></div>'}`;
    if(shared){const old=content.querySelector('#reportKey'),input=node('input');input.id='reportKey';input.title='Optional key UUID';old.replaceWith(input);}
    if(!usage){const field=node('div'),label=node('label','Generation ID'),input=node('input');input.id='reportGeneration';input.autocomplete='off';label.htmlFor=input.id;field.append(label,input);content.querySelector('form button').before(field);}

    if(!memberReport&&!scope?.personal){const field=node('div'),label=node('label','User ID or email'),user=node('input');user.id='reportUser';label.htmlFor=user.id;field.append(label,user);content.querySelector('form button').before(field);}
    const form=content.querySelector('form'),from=form.querySelector('#reportFrom'),to=form.querySelector('#reportTo'),status=content.querySelector('[role=status]'),body=content.querySelector('tbody'),prev=content.querySelector('[data-prev]'),next=content.querySelector('[data-next]');
    const summary=usage?ReportSummary.mount(content.querySelector('.report-summary-host')):null;
    let key=form.querySelector('#reportKey');
    if(!usage)form.querySelector('#reportGeneration').value=filters?.generationID||'';
    const organization=form.querySelector('#reportOrganization');
    if(shared){const option=node('option','All organizations');option.value='';organization.append(option);}
    for(const item of organizations){const option=node('option',item.name);option.value=item.id;organization.append(option);}
    organization.value=organizationID;organization.disabled=!!scope;if(scope)organization.closest('div').hidden=true;
    organization.addEventListener('change',()=>open(shared,scope,organization.value,{from:from.value,to:to.value,generationID:form.querySelector('#reportGeneration')?.value},view));

    const now=new Date(),today=`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;from.value=filters?.from||today.slice(0,8)+'01';to.value=filters?.to||today;
    const download=content.querySelector('[data-export]');let applied=null;
    const tableWrap=body?.closest('.docs-table-wrap');
    const columns=[
      {id:'organization',label:'Organization',available:shared,base:true,value:item=>item.organization_name||'Legacy account'},
      {id:'date',label:'Date',base:true,value:item=>{const date=node('time',new Date(item.created_at).toLocaleString());date.dateTime=item.created_at;date.title='Generation '+item.id;return date;}},
      {id:'generation_id',label:'Generation ID',value:item=>item.id},
      {id:'user',label:'User',available:!memberReport,value:item=>item.user_email||item.user_id?UserUI.identity({id:item.user_id,email:item.user_email,picture:item.user_picture}):'—'},
      {id:'api_key',label:'API key',base:true,value:item=>{const identity=node('div');identity.append(node('strong',item.api_key_name),node('small',item.api_key_id?'*'+item.api_key_id.slice(-4):'Session'));return identity;}},
      {id:'model',label:'Model',base:true,value:item=>item.public_model||item.model},
      {id:'provider',label:'Provider',value:item=>item.provider},
      {id:'status',label:'Status',base:true,value:item=>item.status},
      {id:'profit_status',label:'Profit status',base:true,available:systemAdministrator||clientManager,value:item=>({generating:'Generating',awaiting_billing:'Awaiting billing verification',awaiting_cost:'Awaiting cost',recognized:'Profit calculated',not_tracked:'Before bonus tracking',not_applicable:'Not applicable'}[item.profit_status]||'—')},
      {id:'profit',label:'Profit',base:true,available:systemAdministrator||clientManager,value:item=>usd(item.profit_usd)},
      {id:'tokens',label:'Tokens',base:true,value:item=>tokens(item.usage)},
      {id:'estimated_cost',label:'Estimated cost',available:systemAdministrator,value:item=>usd(item.cost?.expected_usd)},
      {id:'cost',label:'Cost',base:true,value:item=>usd(systemAdministrator?item.cost?.metered_usd??(['free_provider','provider_failed_no_charge'].includes(item.cost?.actual_source)?0:undefined):item.consumed_usd)},
      {id:'confirmed_cost',label:'Confirmed cost',available:systemAdministrator,value:item=>usd(item.cost?.tariff_usd??(['free_provider','provider_failed_no_charge'].includes(item.cost?.actual_source)?0:undefined))},
      {id:'settlement',label:'Settlement',available:systemAdministrator,value:item=>settlement(item.cost)},
      {id:'customer_charge',label:'Customer charge',available:systemAdministrator,value:item=>usd(item.consumed_usd)}
    ].filter(column=>column.available!==false);
    const columnPicker=content.querySelector('#reportColumns'),columnStatus=content.querySelector('[data-columns-status]'),columnCell=content.querySelector('.report-columns-cell');
    let savedColumns=window.GatewayProfile?.reportColumns()||null,rows=[];
    function visibleColumns(){return columns.filter(column=>columnPicker.querySelector(`option[value="${column.id}"]`).selected);}
    function renderRows(){
      if(usage)return;
      const visible=visibleColumns();body.closest('table').style.minWidth=(visible.reduce((width,column)=>width+({generation_id:300,user:240,date:170,settlement:220}[column.id]||130),52))+'px';content.querySelector('thead tr').replaceChildren(...visible.map(column=>node('th',column.label)),columnCell);body.replaceChildren();
      for(const item of rows){const tr=node('tr');tr.classList.add('interactive-row');tr.tabIndex=0;tr.setAttribute('aria-label','Open generation '+item.id);tr.setAttribute('aria-haspopup','dialog');const openDetails=()=>{if(token===epoch)ReportDetails.open(endpoint.split('?')[0]+'/'+encodeURIComponent(item.id),tr);};tr.onclick=e=>{if(!e.target.closest('button,a,input,select,textarea,summary'))openDetails();};tr.onkeydown=e=>{if(e.target===tr&&(e.key==='Enter'||e.key===' ')){e.preventDefault();openDetails();}};for(const column of visible){const value=column.value(item),td=node('td');td.dataset.column=column.id;if(column.id==='status'||['estimated_cost','cost','confirmed_cost','customer_charge'].includes(column.id))td.classList.add('report-value');if(column.id==='status'&&item.status==='failed')td.classList.add('operation-error');td.append(value instanceof Node?value:document.createTextNode(value??'—'));tr.append(td);}tr.append(node('td'));body.append(tr);}
    }
    if(!usage){
      const availableSaved=columns.some(column=>savedColumns?.includes(column.id));
      for(const column of columns){const option=node('option',column.label);option.value=column.id;option.selected=availableSaved?savedColumns.includes(column.id):!!column.base;columnPicker.append(option);}
      let confirmed=[...columnPicker.selectedOptions].map(option=>option.value);
      columnPicker.onchange=async()=>{
        const selected=[...columnPicker.selectedOptions].map(option=>option.value);
        if(!selected.length){for(const option of columnPicker.options)option.selected=confirmed.includes(option.value);ControlPickers.sync(columnPicker);FormUI.message(columnStatus,'Keep at least one column visible.','error');return;}
        // Preserve preferences for columns that are unavailable in this scope.
        const next=[...(savedColumns||[]).filter(id=>!columns.some(column=>column.id===id)),...selected];
        columnPicker.disabled=true;ControlPickers.sync(columnPicker);FormUI.message(columnStatus,'Saving columns…');
        try{const result=await api('/v1/me',{method:'PATCH',body:JSON.stringify({report_columns:next})});window.GatewayProfile.identify({report_columns:result.report_columns});savedColumns=result.report_columns;confirmed=selected;if(token===epoch){renderRows();FormUI.message(columnStatus,'');}}
        catch(error){if(token===epoch){for(const option of columnPicker.options)option.selected=confirmed.includes(option.value);FormUI.message(columnStatus,error.message,'error');}}
        finally{columnPicker.disabled=false;ControlPickers.sync(columnPicker);}
      };
      ControlPickers.enhance(columnPicker,{trigger:columnCell.querySelector('button')});FormUI.refresh();renderRows();
    }
    const endpoint=scope?.personal?'/v1/admin/reports?':!organizationID&&shared?'/v1/system/reports?':'/v1/organizations/'+organizationID+'/reports?';
    if(!usage)download.onclick=async()=>{if(!applied)return;download.disabled=true;FormUI.message(status,'Preparing CSV…');try{const query=new URLSearchParams(applied);query.set('format','csv');query.delete('offset');query.delete('limit');const blob=await api(endpoint+query,{responseType:'blob'});if(token!==epoch)return;const url=URL.createObjectURL(blob),link=node('a');link.href=url;link.download='generation-report.csv';link.click();FormUI.message(status,'');setTimeout(()=>URL.revokeObjectURL(url),1000);}catch(e){if(token===epoch)FormUI.message(status,e.message,'error');}finally{if(token===epoch)download.disabled=false;}};
    async function load(paging=false) {
      const current=++request;
      // Paging and CSV share the last successful filters; only Apply reads drafts.
      const query=paging?new URLSearchParams(applied):new URLSearchParams({from:from.value,to:to.value,api_key_id:key.value,limit:'50',timezone:Intl.DateTimeFormat().resolvedOptions().timeZone});
      query.set('offset',String(offset));if(!paging&&!memberReport&&!scope?.personal)query.set('user',content.querySelector('#reportUser').value);
      if(usage){summary.load(endpoint,query);return;}
      if(!paging)query.set('generation_id',form.querySelector('#reportGeneration').value.trim());
      FormUI.message(status,'Loading report…');prev.disabled=next.disabled=true;download.disabled=true;
      try {
        const result=await api(endpoint+query);if(token!==epoch||current!==request)return;
        if(result.data.length)hasHistory=true;
        if(!hasHistory){
          // A month/filter with no matches is different from an organization with no history.
          const historyEndpoint=shared&&!scope?'/v1/system/reports?':endpoint;
          const historyQuery=new URLSearchParams({from:'0001-01-01',to:'9999-12-30',limit:'1',timezone:Intl.DateTimeFormat().resolvedOptions().timeZone});
          const history=await api(historyEndpoint+historyQuery);if(token!==epoch||current!==request)return;
          hasHistory=history.data.length>0;
        }
        form.hidden=false;tableWrap.hidden=!result.data.length;download.hidden=!result.data.length;
        applied=new URLSearchParams(query);download.disabled=false;hasMore=result.has_more;body.replaceChildren();
        rows=result.data;renderRows();
        FormUI.message(status,result.data.length?'':hasHistory?'No generations match these filters.':'No generations yet.');
        content.querySelector('[data-page]').textContent=`Page ${Math.floor(offset/50)+1}`;prev.disabled=offset===0;next.disabled=!hasMore;prev.parentElement.hidden=offset===0&&!hasMore;
      } catch(error){if(token===epoch&&current===request){FormUI.message(status,`Could not load report: ${error.message}`,'error');body.replaceChildren();tableWrap.hidden=true;download.hidden=true;form.hidden=false;prev.parentElement.hidden=true;prev.disabled=next.disabled=true;}}
    }
    form.addEventListener('submit',e=>{e.preventDefault();offset=0;load();});if(!usage){prev.addEventListener('click',()=>{offset=Math.max(0,offset-50);load(true);});next.addEventListener('click',()=>{offset+=50;load(true);});}
    if(shared){await load();return;}
    if(clientManager){const input=node('input');input.id='reportKey';input.title='Optional key UUID';key.replaceWith(input);key=input;await load();return;}
    try {const result=await api('/v1/api-keys'+(organizationID?'?organization_id='+encodeURIComponent(organizationID):''));if(token!==epoch)return;for(const item of result.data.filter(item=>(item.organization_id||'')===organizationID)){const option=node('option',item.name+(item.revoked_at?' (revoked)':''));option.value=item.id;key.append(option);}}
    catch(error){if(token===epoch)FormUI.message(status,`Could not load API keys: ${error.message}`,'error');return;}
    await load();
  }
  window.GatewayReports={open,openUsage:(shared=false,scope=null)=>open(shared,scope,null,null,'usage'),close(){epoch++;window.ReportDetails?.close();content.replaceChildren();}};
})();
