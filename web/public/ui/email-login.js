(() => {
  const get=id=>document.getElementById(id),host=get('emailAuthForms');
  let form,email,password,submit,status,verificationStarted=false;
  const fragment=new URLSearchParams(location.hash.slice(1));
  let mode=fragment.has('email_verify')?'verify':fragment.has('email_reset')?'reset':'login',token=fragment.get('email_'+mode)||'',delivery=false,busy=false;
  // Tokens stay in memory, never in storage, requests' query strings or referrers.
  if(token)history.replaceState(null,'',location.pathname+location.search);
  let pendingAction=!!token;
  window.addEventListener('hashchange',()=>{if(/^#email_(verify|reset)=/.test(location.hash))location.reload();});
  const messages=(text,type='')=>{status.hidden=!text;FormUI.message(status,text,type);};
  const titles={setup:'Create owner account',login:'Sign in with email',register:'Create your account',forgot:'Reset your password',complete:'Choose your password',reset:'Choose a new password'};
  const labels={setup:'Create owner account',login:'Sign in',register:'Send confirmation link',forgot:'Send reset link',complete:'Create account',reset:'Save password'};
  // Each authentication action owns a stable form and stable field metadata.
  // Share the markup, but never turn a sign-in password field into a new one.
  const forms={};
  for(const action of Object.keys(titles)){
    const next=get('emailAuthTemplate').content.firstElementChild.cloneNode(true);
    next.id='email-'+action+'-form';next.name='email-'+action;next.action=action==='setup'?'/auth/setup':'/auth/email/'+action;
    const address=next.elements.email,secret=next.elements.password,hint=next.querySelector('[data-auth-hint]');
    address.id='email-'+action+'-username';secret.id='email-'+action+'-password';hint.id='email-'+action+'-hint';
    secret.setAttribute('aria-describedby',hint.id);
    const newPassword=['setup','complete','reset'].includes(action);
    secret.autocomplete=newPassword?'new-password':'current-password';secret.minLength=newPassword?15:1;
    if(newPassword)secret.setAttribute('passwordrules','minlength: 15; maxlength: 128;');
    hint.hidden=!newPassword;
    if(['register','forgot'].includes(action))next.querySelector('[data-auth-password]').remove();
    if(['complete','reset'].includes(action)){
      next.querySelector('[data-auth-email]').hidden=true;address.required=false;
    }
    if(action==='complete')next.prepend(next.querySelector('[data-auth-status]'));
    if(action==='setup'){
      const fields=[['code','Setup code','password','off',43],['name','Name','text','name',100]];
      for(const [name,label,type,autocomplete,max]of fields){
        const row=document.createElement('div'),caption=document.createElement('label'),input=document.createElement('input');
        caption.append(document.createTextNode(label));input.name=name;input.id='installation-setup-'+name;input.type=type;input.autocomplete=autocomplete;input.maxLength=max;input.required=true;input.spellcheck=false;
        if(name==='code'){input.minLength=43;input.autocapitalize='none';input.setAttribute('autocorrect','off');}
        caption.append(input);row.append(caption);next.insertBefore(row,next.querySelector('[data-auth-email]'));
      }
      const help=document.createElement('p');help.className='auth-hint';help.textContent='Use the setup code supplied by your server installer. You can configure email delivery after signing in.';next.prepend(help);
    }
    next.querySelector('[type=submit]').textContent=labels[action];
    next.addEventListener('submit',submitForm);forms[action]=next;host.append(next);
  }
  function render(next=mode){
    mode=next;
    get('emailVerification').hidden=true;
    const previousEmail=email?.value||'';
    for(const item of Object.values(forms)){
      item.hidden=true;
      const secret=item.elements.password;if(secret){secret.value='';FormUI.passwords.hide(secret);}
    }
    form=forms[mode];email=form.elements.email;password=form.elements.password;submit=form.querySelector('[type=submit]');status=form.querySelector('[data-auth-status]');
    if(previousEmail)email.value=previousEmail;
    get('authChoices').hidden=true;host.hidden=false;form.hidden=false;get('authNavigation').hidden=false;get('authTitle').textContent=titles[mode];
    messages('');
    get('authModeSwitch').hidden=!delivery||!['login','register'].includes(mode);
    get('authModeHint').textContent=mode==='login'?'Don’t have an account?':'Already have an account?';
    get('authSwitch').textContent=mode==='login'?'Sign up':'Sign in';
    get('authBack').textContent=mode==='forgot'?'Back to sign in':'Back';
    get('authForgot').hidden=mode!=='login'||!delivery;submit.textContent=labels[mode];
    if(mode==='setup')get('authNavigation').hidden=true;
    submit.disabled=['register','forgot'].includes(mode)&&!delivery;
    if(submit.disabled)messages('Email registration and password recovery are not available yet. Please try again later.');
    FormUI.refresh();
  }
  function restoreInvitation(body){
    if(body.invitation_token){sessionStorage.setItem('gateway-pending-invite',body.invitation_token);}
  }
  async function request(action,data){
    const res=await fetch(action==='setup'?'/auth/setup':'/auth/email/'+action,{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});
    let body;try{body=await res.json();}catch{throw new Error('Could not reach the sign-in service. Please try again.');}if(!res.ok)throw new Error(body.error?.message||'Could not complete this request. Please try again.');return body;
  }
  get('emailLogin').onclick=()=>{render('login');email.focus();};
  get('authSwitch').onclick=()=>render(mode==='login'?'register':'login');
  get('authForgot').onclick=()=>render('forgot');
  get('authBack').onclick=()=>{if(mode==='forgot'||mode==='verify'){token='';render('login');return;}token='';if(password){password.value='';FormUI.passwords.hide(password);}form.hidden=true;host.hidden=true;get('authNavigation').hidden=true;get('authModeSwitch').hidden=true;get('authChoices').hidden=false;get('authTitle').textContent='Sign in to get started';};
  const navigation=()=>[get('authSwitch'),...get('authNavigation').querySelectorAll('button')];
  async function confirmEmail(){
    if(verificationStarted)return;verificationStarted=true;
    get('authChoices').hidden=true;host.hidden=true;get('authModeSwitch').hidden=true;
    get('authTitle').textContent='Confirming your email';get('authNavigation').hidden=true;
    status=get('emailVerification');messages('Confirming your email…');
    try{
      const body=await request('verify',{token});restoreInvitation(body);token='';showPasswordSetup(body.email);
    }catch(error){
      get('authTitle').textContent='Email confirmation';messages(error.message,'error');
      get('authNavigation').hidden=false;get('authForgot').hidden=true;get('authBack').textContent='Back to sign in';
    }
  }
  function showPasswordSetup(address){
    pendingAction=true;render('complete');email.value=address;messages('Email confirmed: '+address,'success');FormUI.refresh();
  }
  async function submitForm(event){
    event.preventDefault();if(busy||event.currentTarget!==form)return;busy=true;submit.disabled=true;form.setAttribute('aria-busy','true');
    const submittedForm=form,action=mode,data={};if(!['complete','reset'].includes(action))data.email=email.value;if(password)data.password=password.value;if(token)data.token=token;
    if(action==='setup'){data.code=form.elements.code.value;data.name=form.elements.name.value;}
    if(['register','forgot'].includes(action)){
      const invitation=sessionStorage.getItem('gateway-pending-invite');if(invitation)data.invitation_token=invitation;
    }
    navigation().forEach(b=>b.disabled=true);messages('');
    try{
      const body=await request(action,data);restoreInvitation(body);
      if(['setup','login','complete'].includes(action)){form.hidden=true;password.value='';if(form.elements.code)form.elements.code.value='';location.reload();return;}
      if(['complete','reset'].includes(action)){token='';email.value=body.email;render('login');messages('Password saved. Sign in with your new password.','success');}
      else{if(action==='register')submit.textContent='Resend confirmation';messages(body.message,'success');}
    }catch(error){messages(error.message,'error');}
    finally{busy=false;submittedForm.removeAttribute('aria-busy');submit.disabled=['register','forgot'].includes(mode)&&!delivery;navigation().forEach(b=>b.disabled=false);}
  }
  window.EmailLogin={hasAction:()=>pendingAction,initialize:async()=>{
    let emailAllowed=true;
    const unavailable=get('authUnavailable');
    try{const res=await fetch('/auth/methods',{credentials:'same-origin'});if(!res.ok)throw new Error();const methods=await res.json();delivery=!!methods.email_delivery;emailAllowed=methods.email_confirmation!==false;
      if(methods.setup_required){pendingAction=true;token='';get('authError').hidden=true;render('setup');return;}
      get('googleLogin').hidden=!methods.google;get('emailLogin').hidden=!methods.email;
      unavailable.hidden=!!(methods.google||methods.email);unavailable.textContent='Sign-in is currently unavailable. Contact your administrator.';
    }catch{get('googleLogin').hidden=get('emailLogin').hidden=true;unavailable.hidden=false;unavailable.textContent='Could not load sign-in methods. Please reload the page.';return;}
    if(!emailAllowed){if(token){unavailable.hidden=false;unavailable.textContent='Email sign-in is currently disabled.';}return;}
    // Existing links remain usable without email delivery when email sign-in is enabled.
    if(token){if(mode==='verify')void confirmEmail();else render();}
    else if(!verificationStarted&&!pendingAction){
      try{const state=await request('registration',{});if(state.pending){restoreInvitation(state);showPasswordSetup(state.email);}}
      catch{ /* A pending registration must not prevent ordinary sign-in. */ }
    }
  }};
})();
