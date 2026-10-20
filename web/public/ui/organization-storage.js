(() => {
  'use strict';
  async function open(parent,id){
    const section=document.createElement('section');section.className='organization-storage';
    const heading=FormUI.sectionHeading(section,'File Storage');
    const description=document.createElement('p');description.className='storage-description';description.textContent='References and generated files are shared with everyone in the organization.';
    section.append(description);parent.append(section);
    await StorageSettings.open(section,{base:'/v1/organizations/'+encodeURIComponent(id)+'/storage',heading,organization:true});
  }
  window.OrganizationStorage={open};
})();
