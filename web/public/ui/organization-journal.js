(() => {
 'use strict';
 // Both Requests surfaces use the same UI; the scoped API enforces authority.
 function mount(parent,organization,api){
  return window.GatewayReports.open(false,{organization,parent,api});
 }
 window.OrganizationJournal={mount};
})();
