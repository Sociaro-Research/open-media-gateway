(() => {
 'use strict';
 const labels={supermanager:'Supermanager',admin:'Manager',content_officer:'Content Officer',developer:'Developer',member:'Member'};
 const full=role=>['supermanager','system_admin'].includes(role);
 const manage=role=>full(role)||role==='admin';
 const statistics=role=>manage(role)||['content_officer','client_manager'].includes(role);
 const issue=(role,self,target)=>full(role)||(role==='admin'&&(self||['developer','member'].includes(target)))||(role==='developer'&&(self||target==='member'));
 const assign=(role,target)=>full(role)?Object.keys(labels):role==='admin'&&['developer','member'].includes(target)?['developer','member']:[];
 function heading(){const span=document.createElement('span');span.className='role-information-heading';span.append(document.createTextNode('Role assignment'),ModelInfo.informationButton('Organization role permissions',panel=>{
  const hint=document.createElement('p');hint.textContent='Scroll horizontally to compare all roles on smaller screens.';panel.append(hint);
  const wrap=document.createElement('div');wrap.className='docs-table-wrap';const table=document.createElement('table');table.className='docs-table';const head=document.createElement('thead'),body=document.createElement('tbody');
  const rows=[['Function',...Object.values(labels)],['Organization settings and member limits','All','All¹','—','—','—'],['Invite and remove participants','Anyone²','Developer, Member','—','—','—'],['Assign roles','Any role²','Developer, Member','—','—','—'],['Issue and revoke API keys','For everyone','Self, Developer, Member','—','Self, Member','—'],['Generate with an issued key','Yes','Yes','—','Yes','Yes'],['Statistics and spending','Entire organization','Entire organization','Entire organization','Own only','Own only'],['Request logs','All, with content','All, without content','All, with content','Own, with content','Own, with content'],['Delete generations and files','Any','—','—','Own only','Own only']];
  rows.forEach((values,index)=>{const row=document.createElement('tr');values.forEach(value=>{const cell=document.createElement(index?'td':'th');cell.textContent=value;if(!index)cell.scope='col';row.append(cell);});(index?body:head).append(row);});table.append(head,body);wrap.append(table);panel.append(wrap);
  for(const text of ['¹ Managers cannot change roles, limits or keys of Supermanagers and Content Officers. System settings, funding and credit limits require an Administrator.','² An Administrator appoints the first Supermanager. Existing Supermanagers can appoint more; the last cannot be removed or demoted.','Content includes prompts, input materials and generated results. Full access covers retained data only. Keys act as their assigned recipient; the issuer is recorded separately.']){const p=document.createElement('p');p.textContent=text;panel.append(p);}
 },'role-permissions-popup'));return span;}
 window.OrganizationRoles={labels,label:role=>labels[role]||role,full,manage,statistics,issue,assign,heading};
})();
