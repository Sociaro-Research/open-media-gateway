// Measure the full wordmark independently of the current logo, so resizing
// switches full logo → symbol → wrapped navigation without oscillation.
{
  const header=document.querySelector('.topbar'), nav=document.querySelector('#sectionNav');
  const brand=header.querySelector('.brand'), account=header.querySelector('.topbar-actions');
  const wordmark=brand.querySelector('.brand-wordmark'),symbol=brand.querySelector('.brand-symbol');
  const layoutHeader=()=>{
    const items=[...nav.children].filter(item=>!item.hidden&&getComputedStyle(item).display!=='none');
    const navWidth=items.reduce((width,item)=>width+item.getBoundingClientRect().width,0)+Math.max(0,items.length-1)*parseFloat(getComputedStyle(nav).columnGap);
    const gap=parseFloat(getComputedStyle(header).columnGap);
    const maximum=parseFloat(getComputedStyle(header).getPropertyValue('--brand-full-width'));
    const imageWidth=(image,fallback)=>image.naturalHeight?Math.min(maximum,image.naturalWidth/image.naturalHeight*parseFloat(getComputedStyle(image).height)):fallback;
    const fullWidth=imageWidth(wordmark,40),symbolWidth=imageWidth(symbol,40);
    const controlsWidth=navWidth+account.getBoundingClientRect().width+gap*2;
    const compact=fullWidth+controlsWidth>header.clientWidth;
    header.classList.toggle('compact-brand',compact);
    header.classList.toggle('wrapped',(compact?symbolWidth:fullWidth)+controlsWidth>header.clientWidth);
  };
  // The logo link must retain its intrinsic width for measuring available space.
  const observer=new ResizeObserver(layoutHeader);
  [header,brand,nav,account].forEach(element=>observer.observe(element));
  new MutationObserver(layoutHeader).observe(nav,{subtree:true,childList:true,characterData:true,attributes:true,attributeFilter:['hidden']});
  [wordmark,symbol].forEach(image=>image.addEventListener('load',layoutHeader));
  document.fonts.ready.then(layoutHeader);
}

const expandedTaskParameters = new Set();
const elements = {
  form: document.querySelector('#taskForm'),
  prompt: document.querySelector('#prompt'),
  promptCount: document.querySelector('#promptCount'),
  model: document.querySelector('#model'),
  resolution: document.querySelector('#resolution'),
  ratio: document.querySelector('#ratio'),
  duration: document.querySelector('#duration'),
  durationHint: document.querySelector('#durationHint'),
  generateAudio: document.querySelector('#generateAudio'),
  watermark: document.querySelector('#watermark'),
  cameraFixed: document.querySelector('#cameraFixed'),
  draft: document.querySelector('#draft'),
  seed: document.querySelector('#seed'),
  returnLastFrame: document.querySelector('#returnLastFrame'),
  audioOption: document.querySelector('#audioOption'),
  watermarkOption: document.querySelector('#watermarkOption'),
  cameraOption: document.querySelector('#cameraOption'),
  draftOption: document.querySelector('#draftOption'),
  lastFrameOption: document.querySelector('#lastFrameOption'),
  priceCard: document.querySelector('#priceCard'),
  priceValue: document.querySelector('#priceValue'),
  priceDetails: document.querySelector('#priceDetails'),
  apiKey: document.querySelector('#apiKey'),
  connectionState: document.querySelector('#connectionState'),
  submit: document.querySelector('#submitButton'),
  message: document.querySelector('#formMessage'),
  taskList: document.querySelector('#taskList'),
  template: document.querySelector('#taskTemplate'),
  submitLabel: document.querySelector('#submitLabel'),
  visualSettings: document.querySelector('#visualSettings'),
  resolutionField: document.querySelector('#resolutionField'),
  ratioField: document.querySelector('#ratioField'),
  durationField: document.querySelector('#durationField'),
  imageURLs: document.querySelector('#imageURLs'),
  imageURLsField: document.querySelector('#imageURLsField'),
  videoURL: document.querySelector('#videoURL'),
  videoURLField: document.querySelector('#videoURLField'),
  audioURL: document.querySelector('#audioURL'),
  audioURLField: document.querySelector('#audioURLField'),
  negativePrompt: document.querySelector('#negativePrompt'),
  negativePromptField: document.querySelector('#negativePromptField'),
  voice: document.querySelector('#voice'),
  voiceOptions: document.querySelector('#voiceOptions'),
  voiceField: document.querySelector('#voiceField'),
  language: document.querySelector('#language'),
  languageField: document.querySelector('#languageField'),
  instructions: document.querySelector('#instructions'),
  instructionsField: document.querySelector('#instructionsField'),
  lyrics: document.querySelector('#lyrics'),
  lyricsField: document.querySelector('#lyricsField'),
  gender: document.querySelector('#gender'),
  genderField: document.querySelector('#genderField'),
  quality: document.querySelector("#quality"),
  qualityField: document.querySelector("#qualityField"),
  background: document.querySelector("#background"),
  backgroundField: document.querySelector("#backgroundField"),
  format: document.querySelector('#format'),
  formatField: document.querySelector('#formatField'),
  generationView: document.querySelector('#generationView'),
  providersView: document.querySelector('#providersView'),
  generationNav: document.querySelector('#generationNav'),
  providersNav: document.querySelector('#providersNav'),
  providerAdminCount: document.querySelector('#providerAdminCount'),
  providerAdminList: document.querySelector('#providerAdminList'),
  providerEmpty: document.querySelector('#providerEmpty'),
  providerDetailContent: document.querySelector('#providerDetailContent'),
  adminProviderName: document.querySelector('#adminProviderName'),
  adminProviderStatus: document.querySelector('#adminProviderStatus'),
  adminProviderDescription: document.querySelector('#adminProviderDescription'),
  adminProviderEnabled: document.querySelector('#adminProviderEnabled'),
  adminConnectionState: document.querySelector('#adminConnectionState'),
  adminCredentialSource: document.querySelector('#adminCredentialSource'),
  adminRegion: document.querySelector('#adminRegion'),
  adminModelCount: document.querySelector('#adminModelCount'),
  providerForm: document.querySelector('#providerForm'),
  saveProvider: document.querySelector('#saveProviderButton'),
  checkProvider: document.querySelector('#checkProviderButton'),
  providerCheckFeedback: document.querySelector('#providerCheckFeedback'),
  providerMessage: document.querySelector('#providerMessage'),
  adminMediaCounts: document.querySelector('#adminMediaCounts'),
  adminModelList: document.querySelector('#adminModelList'),
  authGate: document.querySelector('#authGate'),
  authError: document.querySelector('#authError'),
  sectionNav: document.querySelector('#sectionNav'),
  userMenu: document.querySelector('#userMenu'),
  userPicture: document.querySelector('#userPicture'),
  userName: document.querySelector('#userName'),
  userEmail: document.querySelector('#userEmail'),
  logout: document.querySelector('#logoutButton'),
  sessionMessage: document.querySelector('#sessionMessage'),
  connectionPanel: document.querySelector('#connectionPanel')
};

let serviceInfo = {provider: '', model: '', live: false};
let providerCatalog = [];
let providerAdminCatalog = [];
let selectedAdminProviderID = '';
let renderedTasksFingerprint = '';
let taskListRequest = null;
let authMode = 'pending';
let latestTasks = [];
const taskCompletionUntil = new Map();
let pendingSubmission = null;
let submitting = false;
const dialog = document.querySelector('#confirmDialog');
const confirmButton = document.querySelector('#confirmSubmit');
const cancelButton = document.querySelector('#cancelConfirm');
const closeButton = document.querySelector('#closeConfirm');
const confirmStatus = document.querySelector('#confirmStatus');
const statusLabels = {queued: 'Queued', in_progress: 'Running', completed: 'Completed', failed: 'Failed'};
const promptHistory = JSON.parse(localStorage.getItem('media-gateway-prompts') || '{}');
// Remove the legacy persistent credential; API keys now stay in page memory.
localStorage.removeItem('media-gateway-api-key');
let consoleAuthenticated = false;
let currentConsoleSection = location.pathname === '/profile' ? 'profile' : location.pathname === '/system' ? 'system' : location.pathname === '/docs' || location.hash.startsWith('#docs/') ? 'docs' : location.pathname === '/api-keys' ? 'keys' : 'generation';
let consoleNavigation=0;
let systemAdministrator=false;
let clientManager=false;
let clientManagerRole='';
let sharedConfiguration=false;
let consoleInitialized=false;
let consolePolicy={generation_ui:true,personal_providers:true};
window.applySystemAccess=function(settings,admin=systemAdministrator){
 consolePolicy={...consolePolicy,...settings};serviceInfo.console=consolePolicy;systemAdministrator=admin;
 document.querySelector('#systemNav').hidden=!admin;
 document.querySelector('#createGenerationKey').disabled=!admin&&!consolePolicy.personal_providers&&!consolePolicy.shared_resources;
 if(window.Workspace?.ready())Workspace.refresh().catch(()=>{});
 elements.generationNav.hidden=clientManager||!consolePolicy.generation_ui;elements.providersNav.hidden=clientManager||(!consolePolicy.personal_providers&&!admin);
 document.querySelector('#storageNav').hidden=clientManager||(!consolePolicy.personal_providers&&!admin);
 document.querySelector('#keysNav').hidden=clientManager;
 if(clientManager){document.querySelector('#createGenerationKey').disabled=true;lastConfigurationSection='organizations';}
 if((currentConsoleSection==='generation'&&!consolePolicy.generation_ui)||(['providers','storage'].includes(currentConsoleSection)&&!consolePolicy.personal_providers&&!admin)||(currentConsoleSection==='system'&&!admin))showConsoleSection(consoleAuthenticated?'keys':'docs');
};
const configurationSections = ['providers', 'storage', 'keys', 'reports', 'organizations'];
if (location.pathname === '/configuration') currentConsoleSection = configurationSections.includes(location.hash.slice(1)) ? location.hash.slice(1) : 'providers';
if(location.pathname==='/organizations'||sessionStorage.getItem('gateway-pending-invite'))currentConsoleSection='organizations';
let lastConfigurationSection = configurationSections.includes(currentConsoleSection) ? currentConsoleSection : 'providers';
const compactConfiguration = window.matchMedia('(max-width: 700px)');
const configurationMenu = document.querySelector('#configurationMenu');
configurationMenu.open = !compactConfiguration.matches;
compactConfiguration.addEventListener('change', event => { configurationMenu.open = !event.matches; });


function setMessage(text, type = '') {
  elements.message.textContent = text;
  elements.message.className = `form-message ${type}`;
}

function modelSelectionValue(provider, model) {
  // Model IDs can be shared by different providers.
  return ModelPicker.key(provider, model);
}

function selectedProvider() {
  return providerCatalog.find(item => item.id === elements.model.selectedOptions[0]?.dataset.provider);
}

function selectedModel() {
  const provider = selectedProvider();
  return provider?.models.find(item => modelSelectionValue(provider.id, item.id) === elements.model.value);
}

function taskName(task) {
  const labels = {video: 'Video', image: 'Image', speech: 'Speech', music: 'Music', audio: 'Audio'};
  return task.prompt || promptHistory[task.id] || task.options?.lyrics || `${labels[task.media_type] || 'Media'} generation`;
}

function formatDate(value) {
  return new Intl.DateTimeFormat('en-US', {
    day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit', second: '2-digit'
  }).format(new Date(value));
}

function taskSettings(task) {
  const options = task.options || {};
  const parts = [task.media_type || 'video', task.provider, task.model];
  if (options.resolution) parts.push(options.resolution);
  if (options.duration) parts.push(`${options.duration} s`);
  if (options.ratio) parts.push(options.ratio);
  if (options.generate_audio) parts.push('with audio');
  if (options.draft) parts.push('draft');
  return parts.join(' · ');
}

function formatBytes(value) {
  if (!Number.isFinite(value) || value < 1) return '';
  if (value < 1024 * 1024) return `${Math.ceil(value / 1024)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

function usageSummary(usage, provider) {
	if (!usage) return '';
	const metricLabels = {total_tokens: 'tokens', completion_tokens: 'output tokens', input_tokens: 'input tokens', output_tokens: 'output tokens', image_count: 'images', video_count: 'video', video_duration: 's video', output_video_duration: 's output', audio_duration: 's audio', characters: 'characters'};
	const preferred = ['total_tokens', 'completion_tokens', 'image_count', 'video_duration', 'output_video_duration', 'audio_duration', 'characters'];
	const metrics = usage.metrics || [];
	const selected = preferred.map(name => metrics.find(metric => metric.name === name)).filter(Boolean);
	const parts = (selected.length ? selected : metrics.slice(0, 3)).map(metric => `${Number(metric.quantity).toLocaleString('en-US')} ${metricLabels[metric.name] || metric.unit}`);
	if (Number.isFinite(usage.estimated_cost)) parts.push(`≈ ${usage.estimated_cost.toLocaleString('en-US', {style: 'currency', currency: usage.currency || 'USD', minimumFractionDigits: 0, maximumFractionDigits: 2})}`);
	if (!Number.isFinite(usage.estimated_cost)) parts.push(providerCatalog.find(item=>item.id===provider)?.live===false ? 'Free' : 'Cost unknown');
	return parts.length ? `Provider usage: ${parts.join(' · ')}${usage.cost_status === 'estimated' ? ' · estimated cost' : ''}` : 'Provider usage details available.';
}

async function attachStoredMedia(article, task) {
  const controller = new AbortController();
  article.mediaController = controller;
  const result = article.querySelector('.task-result');
  const contentType = task.result_content_type || '';
  let player;
  if (contentType.startsWith('image/') || task.media_type === 'image') {
    player = document.createElement('img');
    player.alt = 'Generated media';
  } else {
    player = document.createElement(contentType.startsWith('audio/') || ['speech', 'music', 'audio'].includes(task.media_type) ? 'audio' : 'video');
    player.controls = true;
    player.preload = 'metadata';
    if (player.tagName === 'VIDEO') player.playsInline = true;
  }
  player.className = 'task-media';


  const note = document.createElement('div');
  note.className = 'media-note';
  note.textContent = `Saved${task.result_size_bytes ? ` · ${formatBytes(task.result_size_bytes)}` : ''}`;
  const actions = document.createElement('div');
  actions.className = 'media-actions';
  actions.textContent = 'Preparing preview…';
  result.append(player, note, actions);

  try {
    const access = await api(`/v1/generations/${task.id}/media-access`, {signal: controller.signal});
    if (!article.isConnected || controller.signal.aborted) return;
    player.src = access.preview_url;
    if (player.tagName === 'VIDEO' && (access.last_frame_url || task.last_frame_url)) player.poster = access.last_frame_url || task.last_frame_url;
    actions.replaceChildren();
    const open = document.createElement('a');
    open.href = access.preview_url;
    open.target = '_blank';
    open.rel = 'noopener noreferrer';
    open.textContent = 'Open ↗';
    const download = document.createElement('a');
    download.href = access.download_url;
    download.download = task.id;
    download.textContent = 'Download ↓';
    actions.append(open, download);
    if (access.last_frame_url) {
      const frame = document.createElement('a');
      frame.href = access.last_frame_url; frame.target = '_blank'; frame.rel = 'noopener noreferrer'; frame.textContent = 'Last frame ↗';
      actions.append(frame);
    }
  } catch (error) {
    if (error.name === 'AbortError') return;
    player.remove();
    actions.textContent = `Could not open saved media: ${error.message}`;
    actions.classList.add('error');
  }
}

function disposeTaskCard(article) {
  article.mediaController?.abort();
  for (const player of article.querySelectorAll('.task-media')) {
    player.removeAttribute('src');
    if (player.tagName === 'VIDEO' || player.tagName === 'AUDIO') player.load();
  }
  article.remove();
}

function displayGeneration(task) {
 const options={...task.options};
 if(task.seconds) options.duration=Number(task.seconds);
 if(task.size) options.resolution=task.size.includes('x') && task.media_type==='image' ? task.size.replace('x','*') : task.size;
 if(task.media_type==='video' && task.size?.includes('x')) {
  const [w,h]=task.size.split('x').map(Number);
  options.resolution=Math.min(w,h)+'p';
 }
 if(task.image || task.input_reference) options.image_urls=task.image || task.input_reference;
 for(const name of ['voice','instructions','quality','background']) if(task[name]) options[name]=task[name];
 if(task.response_format || task.output_format) options.format=task.response_format || task.output_format;
 if(options.video_urls?.length) options.video_url=options.video_urls[0];
 if(options.audio_urls?.length) options.audio_url=options.audio_urls[0];
 return {...task, model:task.provider_model || task.model.slice(task.provider.length+1), prompt:task.media_type==='speech'?task.input:task.prompt, options};
}
function renderTasks(tasks) {
  const previous = new Map(latestTasks.map(task => [task.id, task.status]));
  const existing = new Map([...elements.taskList.querySelectorAll('.task')].map(article => [article.dataset.taskId, article]));
  const fingerprints = new Map(tasks.map(task => [task.id, JSON.stringify(task)]));
  const visibleIDs = new Set(tasks.map(task => task.id));
  for (const id of taskCompletionUntil.keys()) if (!visibleIDs.has(id)) taskCompletionUntil.delete(id);
  for (const id of expandedTaskParameters) if (!visibleIDs.has(id)) expandedTaskParameters.delete(id);
  latestTasks = tasks;
  tasks=tasks.map(displayGeneration);
  if (!tasks.length) {
    existing.forEach(disposeTaskCard);
    elements.taskList.innerHTML = '<div class="empty-state"><span>◎</span><strong>No tasks yet</strong><p>Your generations will appear here.</p></div>';
    return;
  }
  const storedMedia = [];
  const finishingProgress = [];
  const articles = tasks.map(task => {
	const saved = existing.get(task.id);
	if (saved?.taskFingerprint === fingerprints.get(task.id)) return saved;
	const article = elements.template.content.firstElementChild.cloneNode(true);
	article.taskFingerprint = fingerprints.get(task.id);
	article.dataset.status = task.status;
    const progress = article.querySelector('.progress');
    if (['completed','failed'].includes(task.status)) {
      const wasActive = ['queued','in_progress'].includes(previous.get(task.id));
      if (wasActive) taskCompletionUntil.set(task.id, Date.now() + 1300);
      const remaining = (taskCompletionUntil.get(task.id) || 0) - Date.now();
      if (remaining <= 0) progress.remove();
      else {
        if (wasActive) {
          const bar = progress.querySelector('span');
          bar.style.width = previous.get(task.id) === 'in_progress' ? '58%' : '12%';
          finishingProgress.push(bar);
        }
        // 300 ms to fill, then one second at the terminal color. Re-renders
        // retain the deadline instead of restarting the completion animation.
        setTimeout(() => progress.remove(), remaining);
      }
    } else taskCompletionUntil.delete(task.id);
	article.querySelector('.task-title').textContent = taskName(task);
	article.dataset.taskId = task.id;
    const remove=article.querySelector('.delete-result');remove.hidden=task.can_delete===false||!(task.stored_at||task.has_stored_result)||!!task.result_deleted_at||!['completed','failed'].includes(task.status);remove.addEventListener('click',()=>confirmResultDeletion(task));
    article.querySelector('.reuse-task').addEventListener('click', () => reuseTask(task));
    article.querySelector('.task-id').value=task.id;
    const parameters = [['Media type', mediaTypeLabel(task.media_type)], ['Provider', task.provider], ['Model', task.model], ...taskParameterRows(task.options || {})];
    if (task.api_key_id) parameters.push(['Gateway API key', task.api_key_id]);
    fillSummary(article.querySelector('.task-parameters dl'), parameters);
    article.querySelector('.task-prompt').value = taskName(task);
    const details = article.querySelector('.task-parameters'), summary = details.querySelector('summary');
    details.open = expandedTaskParameters.has(task.id);
    const updateSummary = () => { summary.textContent = details.open ? 'Parameters' : 'Parameters: '+taskSettings(task); };
    details.addEventListener('toggle', () => { if(details.open) expandedTaskParameters.add(task.id); else expandedTaskParameters.delete(task.id); updateSummary(); });
    updateSummary();
	article.querySelector('.task-time').textContent = formatDate(task.created_at * 1000);
	const badge = article.querySelector('.status-badge');
    badge.textContent = statusLabels[task.status] || task.status;
    badge.classList.add(task.status);
	const result = article.querySelector('.task-result');
 if(task.result_deleted_at) result.textContent='Stored result files deleted.';
	if (!task.result_deleted_at && task.status === 'completed' && task.content_url) {
	  if (task.stored_at) {
		storedMedia.push({article, task});
	  } else {
		const unavailable = new URL(task.content_url,location.origin).hostname.endsWith('.invalid');
		result.textContent = unavailable ? 'This legacy task has no media file.' : '';
		if (!unavailable) {
		const link = document.createElement('a');
        link.href = task.content_url;
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
        link.textContent = 'Open result ↗';
        result.append(link);
		if (task.last_frame_url) {
		  result.append(document.createTextNode(' · '));
		  const frameLink = document.createElement('a');
		  frameLink.href = task.last_frame_url;
		  frameLink.target = '_blank';
		  frameLink.rel = 'noopener noreferrer';
		  frameLink.textContent = 'Last frame ↗';
		  result.append(frameLink);
		}
		}
	  }
	} else if (task.status === 'failed') {
      const failure=document.createElement('p');failure.textContent=task.error?.message||'Generation failed.';result.append(failure);
      result.classList.add('error');
    }
 const cost=document.createElement('div');cost.className='task-cost';
 const completed=['completed','failed'].includes(task.status);
 const amount=completed ? (task.cost?.customer_tariff_usd ?? task.cost?.customer_metered_usd ?? task.cost?.tariff_usd ?? task.cost?.metered_usd ?? (['free_provider','provider_failed_no_charge'].includes(task.cost?.actual_source) ? 0 : undefined)) : undefined;
 const price=document.createElement('span');price.textContent=amount!==undefined ? 'Cost '+formatUSD(amount) : 'Estimated cost '+(task.cost?.expected_usd!=null ? formatUSD(task.cost.customer_expected_usd ?? task.cost.expected_usd) : 'not available');
 const timer=document.createElement('span');timer.className='task-elapsed';timer.dataset.createdAt=new Date(task.created_at * 1000).toISOString();timer.dataset.finishedAt=['completed','failed'].includes(task.status)?new Date(task.updated_at * 1000).toISOString():'';updateTimer(timer);
 cost.append(price,timer);result.append(cost);
	return article;
  });
  // Keep unchanged cards attached: moving/recreating media elements interrupts
  // playback and repeats storage authorization on every unrelated task update.
  const retained = new Set(articles);
  for (const child of [...elements.taskList.children]) if (!retained.has(child)) disposeTaskCard(child);
  articles.forEach((article, index) => {
    const current = elements.taskList.children[index];
    if (current !== article) elements.taskList.insertBefore(article, current || null);
  });
  for (const bar of finishingProgress) {
    bar.getBoundingClientRect();
    bar.style.removeProperty('width');
  }
  storedMedia.forEach(({article, task}) => attachStoredMedia(article, task));
}

async function api(path, options = {}) {
 if(sharedConfiguration){path=path.replace(/^\/v1\/admin\/(providers|storage)(?=[/?]|$)/,'/v1/system/$1');}

  const management = path.startsWith('/v1/sales/') || /^\/v1\/(client-onboardings(?:[/?]|$)|organizations(?:[/?]|$)|invitations\/|audit\/|shared\/|system\/|admin\/|api-keys(?:[/?]|$)|me(?:[/?]|$))/.test(path);
  const useKey = !management || authMode === 'api_key';
  if (useKey) await window.GenerationKey.ensureSession(options.method === 'POST');
  const key = useKey ? window.GenerationKey.value() : '';
  if (useKey && !key) {
    const error = new Error('Enter a Gateway API key in the budget section below.'); error.status = 401; throw error;
  }
  if (options.expectedKey !== undefined && options.expectedKey !== key) throw new DOMException('API key changed', 'AbortError');
  options.signal?.throwIfAborted();
  const response = await fetch(path, {
    ...options, credentials: useKey ? 'omit' : 'same-origin',
    headers: {...(useKey ? {Authorization: 'Bearer ' + key} : {}), ...(options.body && !(options.body instanceof FormData) ? {'Content-Type':'application/json'} : {}), ...(options.headers || {})}
  });
  if (useKey && key !== window.GenerationKey.value()) throw new DOMException('API key changed', 'AbortError');
  if (useKey && response.status === 401) window.GenerationKey.forget('This key is invalid, expired or revoked. Paste a valid key or generate a new one.');
  const body = options.responseType==='blob'&&response.ok ? await response.blob() : await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error?.message || `HTTP ${response.status}`);
    error.status = response.status;
    error.code = body.error?.code;
    throw error;
  }
  return body;
}

function setProviderMessage(text = '', type = '') {
  elements.providerMessage.textContent = text;
  elements.providerMessage.className = `provider-message ${type}`;
}

function setProviderCheckFeedback(text = '', type = '') {
  elements.providerCheckFeedback.textContent = text;
  elements.providerCheckFeedback.className = `provider-check-feedback ${type}`;
}

function providerStatusLabel(provider) {
  if (!provider.configured) return 'Not configured';
  if (provider.status === 'error') return 'Failed';
  if (provider.status === 'unverified') return 'Not verified';
  if (!provider.enabled) return 'Disabled';
  return 'Connected';
}

function renderProviderAdminList() {
  elements.providerAdminCount.textContent = providerAdminCatalog.length;
  const buttons = providerAdminCatalog.map(provider => {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = `provider-list-item ${provider.id === selectedAdminProviderID ? 'active' : ''}`;
    button.dataset.providerId = provider.id;
    const mark = document.createElement('span');
    mark.className = `provider-list-mark ${provider.status === 'error' ? 'error' : provider.enabled && provider.configured ? 'ok' : ''}`;
    mark.setAttribute('aria-hidden', 'true');
    const iconSource = provider.icon_url;
    if (iconSource) {
      const icon = document.createElement('img');
      icon.src = iconSource;
      icon.alt = '';
      icon.classList.toggle('monochrome',!!provider.icon_monochrome);
      mark.append(icon);
    } else {
      mark.textContent = provider.name.slice(0, 1).toUpperCase();
    }
    const copy = document.createElement('span');
    const name = document.createElement('strong');
    name.textContent = provider.name;
    const status = document.createElement('small');
    status.textContent = providerStatusLabel(provider);
    copy.append(name, status);
    const arrow = document.createElement('span');
    arrow.className = 'provider-list-arrow';
    arrow.textContent = '›';
    button.append(mark, copy, arrow);
    button.addEventListener('click', () => selectAdminProvider(provider.id));
    return button;
  });
  elements.providerAdminList.replaceChildren(...buttons);
}

function renderProviderDetail(provider) {
  elements.providerEmpty.hidden = true;
  elements.providerDetailContent.hidden = false;
  elements.adminProviderName.textContent = provider.name;
  elements.adminProviderDescription.textContent = '';elements.adminProviderDescription.hidden=true;
  elements.adminProviderStatus.textContent = providerStatusLabel(provider);
  elements.adminProviderStatus.className = `provider-status ${provider.status}`;
  elements.adminProviderEnabled.checked = provider.enabled;
  elements.adminProviderEnabled.disabled = false;
  elements.adminProviderEnabled.setAttribute('aria-label', provider.name + ' enabled');
  FormUI.message(elements.adminConnectionState,provider.status_message || providerStatusLabel(provider),provider.status==='error'?'error':'');
  elements.adminCredentialSource.textContent = ({environment: 'Environment → Postgres', console: 'Console → Postgres', none: 'Not set'})[provider.credential_source] || provider.credential_source;
  elements.adminRegion.textContent = provider.region || 'Local';
  elements.adminModelCount.textContent = provider.model_count || 0;
  renderProviderFields(provider);
  delete elements.providerForm.connectionDraft;
  window.ProviderConnections?.render(provider);
  if(systemAdministrator&&sharedConfiguration)window.CatalogSync?.provider(elements.providerDetailContent,provider.id,api,()=>selectedAdminProviderID===provider.id&&sharedConfiguration);
  else elements.providerDetailContent.querySelector('.catalog-provider-settings')?.remove();
  (elements.saveProvider.querySelector('span')||elements.saveProvider).textContent = 'Save';
  elements.checkProvider.hidden = !provider.configured || !(provider.fields||[]).length;
  FormUI.watch(elements.providerForm,elements.saveProvider,{heading:document.querySelector('.provider-title-line'),read:()=>JSON.stringify([...elements.providerForm.querySelectorAll('input,select,textarea')].map(e=>[e.type,e.type==='checkbox'?e.checked:e.value]))+window.ProviderConnections?.snapshot(elements.providerForm)});

  const counts = Object.entries(provider.media_counts || {}).sort(([a], [b]) => a.localeCompare(b)).map(([type, count]) => {
    const chip = document.createElement('span');
    chip.textContent = `${mediaTypeLabel(type)} · ${count}`;
    return chip;
  });
  elements.adminMediaCounts.replaceChildren(...(counts.length ? counts : [Object.assign(document.createElement('span'), {textContent: 'Connect to view the model catalog'})]));

  const models = (provider.models || []).map(model => {
    const row = document.createElement('div');
    row.className = 'provider-model-row';
    const copy = document.createElement('div');
    const name = document.createElement('strong');
    name.textContent = model.display_name || model.id;
    const id = document.createElement('small');
    id.textContent = model.id;
    copy.append(name, id);
    const type = document.createElement('span');
    type.textContent = mediaTypeLabel(model.media_type);
    row.append(copy, type);
    return row;
  });
  elements.adminModelList.replaceChildren(...(models.length ? models : [Object.assign(document.createElement('p'), {className: 'provider-placeholder', textContent: 'No models available.'})]));
 if(systemAdministrator) window.ModelAccess.provider(elements.adminModelList,provider,api,{current:()=>selectedAdminProviderID===provider.id});
}

function selectAdminProvider(id) {
  selectedAdminProviderID = id;
  renderProviderAdminList();
  const provider = providerAdminCatalog.find(item => item.id === id);
  if (provider) renderProviderDetail(provider);
  setProviderMessage();
  setProviderCheckFeedback();
}

let providerLoadSequence=0;
async function loadAdminProviders(showErrors = true) {
 window.ProviderConnections?.setup();
 const sequence=++providerLoadSequence,scope=sharedConfiguration;
  try {
    const response = await api('/v1/admin/providers');
    if(sequence!==providerLoadSequence||scope!==sharedConfiguration)return;
    providerAdminCatalog = response.data || [];
    if (!providerAdminCatalog.some(item => item.id === selectedAdminProviderID)) {
      selectedAdminProviderID = providerAdminCatalog[0]?.id || '';
    }
    renderProviderAdminList();
    const provider = providerAdminCatalog.find(item => item.id === selectedAdminProviderID);
    if (provider) renderProviderDetail(provider);
    else {
      elements.providerDetailContent.hidden = true;
      elements.providerEmpty.hidden = false;
      elements.providerEmpty.querySelector('strong').textContent = 'No providers available';
      elements.providerEmpty.querySelector('p').textContent = 'No providers are enabled for your account.';
    }
  } catch (error) {
    elements.providerAdminList.innerHTML = '<div class="provider-loading">Could not load settings.</div>';
    if (showErrors) setProviderMessage(error.status === 401 ? (authMode === 'google' ? 'Your session expired. Sign in again.' : 'Check your Gateway API key on the Playground page.') : error.message, 'error');
  }
}

async function updateAdminProvider(payload, successMessage, showSaved = false) {
  const id = selectedAdminProviderID;
  if (!id) return;
  // Model actions live outside the form but mutate the same submitted draft.
  const controls = [...elements.providerForm.querySelectorAll('input,select,textarea,button'), ...document.querySelectorAll('#manualModelSettings button'), elements.adminProviderEnabled];
  const disabled = controls.map(control => control.disabled);
  controls.forEach(control => { control.disabled = true; });
  elements.saveProvider.disabled = true;
  elements.checkProvider.disabled = true;
  setProviderMessage('');
  setProviderCheckFeedback();
  try {
    await api(`/v1/admin/providers/${id}`, {method: 'PUT', body: JSON.stringify(payload)});
    setProviderMessage(showSaved ? '' : successMessage, 'success');
    await loadAdminProviders(false);
    if (showSaved && selectedAdminProviderID === id) FormUI.saved(elements.providerForm);
    await loadProviders();
  } catch (error) {
    setProviderMessage(`Could not save: ${error.message}`, 'error');
    if (!showSaved) await loadAdminProviders(false);
  } finally {
    controls.forEach((control, index) => { if (control.isConnected) control.disabled = disabled[index]; });
    elements.saveProvider.disabled = false;
    elements.checkProvider.disabled = false;
    FormUI.update(elements.providerForm);
  }
}

function showConsoleSection(name) {
  const navigation=++consoleNavigation;
  if(name==='docs'){location.assign('/docs'+(location.hash.startsWith('#docs/')?location.hash:''));return;}
  window.Workspace?.restore();
  if(name!=='system')window.GatewaySystem?.close();
  let page={keys:'API keys',reports:'Billing',providers:'Providers',storage:'Storage'}[name];
  if(name==='configuration'||configurationSections.includes(name))name='organizations';
  if(clientManager&&!['organizations','docs','profile'].includes(name))name='organizations';
  if(name==='system'&&consoleInitialized&&!systemAdministrator)name='organizations';
  if(name==='generation'&&!consolePolicy.generation_ui)name='organizations';
  GatewayLayout.clear(document.querySelector('#configurationView'));
  currentConsoleSection=name;
  const allowed=authMode==='api_key'||consoleAuthenticated;
  elements.authGate.hidden=name==='docs'||allowed;
  document.querySelector('#configurationView').hidden=!['organizations','profile'].includes(name)||!allowed;
  for(const section of ['generation','providers','storage','keys','reports','docs','system','organizations','profile']){
    document.querySelector('#'+section+'View').hidden=section!==name||(section!=='docs'&&!allowed);
    const nav=document.querySelector('#'+section+'Nav');if(!nav)continue;nav.classList.toggle('active',section===name);if(section===name)nav.setAttribute('aria-current','page');else nav.removeAttribute('aria-current');
  }
  document.querySelector('#workspacePicker').classList.toggle('active',name==='organizations'&&!window.Workspace?.current()?.personal);
  document.querySelector('#userMenuToggle').classList.toggle('active',name==='profile'||(name==='organizations'&&!!window.Workspace?.current()?.personal));
  window.GatewayKeys?.close();window.GatewayReports?.close();
  if(name!=='system')window.GatewayOrganizations?.close();
  if(name==='system'&&allowed)window.GatewaySystem?.open();
  if(name==='profile'&&allowed)window.Workspace?.openProfile(document.querySelector('#profileContent'));
  if(name==='organizations'&&allowed){
    if(window.Workspace?.ready())window.Workspace.open(Workspace.current()?.personal&&page==='Billing'?'Reports':page).catch(e=>{if(navigation!==consoleNavigation)return;document.querySelector('#organizationsContent').replaceChildren(Object.assign(document.createElement('p'),{className:'operation-error',textContent:e.message}))});
    else window.GatewayOrganizations?.open();
  }
  if(name!=='generation') taskListRequest?.controller.abort();
  if(name==='generation'&&allowed&&window.GenerationKey.value()){refreshGenerationBudget();updatePrice();loadTasks();}
  if(name==='docs'){if(!location.hash.startsWith('#docs/'))history.replaceState(null,'','/docs');window.GatewayDocs?.open();}
  else if(name==='system')history.replaceState(null,'','/system');
  else if(name==='profile')history.replaceState(null,'','/profile');
  else if(name==='organizations')history.replaceState(null,'','/organizations');
  else history.replaceState(null,'','/');
}

async function loadTasks(showErrors = false) {
  if (submitting || !window.GenerationKey.value()) return;
  if (taskListRequest && !taskListRequest.controller.signal.aborted) return taskListRequest.promise;
  const request = {controller: new AbortController(), promise: null};
  taskListRequest = request;
  request.promise = (async () => { try {
    const response = await api('/v1/generations?limit=50', {signal: request.controller.signal});
    if (request.controller.signal.aborted) return;
    if (submitting) return;
    const tasks = response.data || [];
    const fingerprint = JSON.stringify(tasks);
    if (fingerprint !== renderedTasksFingerprint) {
      renderedTasksFingerprint = fingerprint;
      renderTasks(tasks);
    }
  } catch (error) {
    if (error.name === 'AbortError') return;
    if (showErrors || error.status === 401) setMessage(error.status === 401 ? 'Check your API key.' : `Could not load the journal: ${error.message}`, 'error');
  } finally {
    if (taskListRequest === request) taskListRequest = null;
  } })();
  return request.promise;
}

async function loadInfo() {
  try {
    const response = await fetch('/v1/info');
    if (!response.ok) throw new Error();
    serviceInfo = await response.json();
  } catch {
    setMessage('Configuration unavailable.', 'error');
  }
}

function modelOptionLabel(model) {
  if (model.pricing?.known) {
    if (model.pricing.reference_price_usd === 0) return `${model.display_name} — free`;
    return `${model.display_name} — ≈ ${new Intl.NumberFormat(undefined, {style:"currency",currency:"USD",maximumFractionDigits:5}).format(model.pricing.reference_price_usd)}`;
  }
  return model.display_name;
}

function fillSelect(select, values, preferred) {
  select.replaceChildren(...values.map(value => new Option(value, value, false, value === preferred)));
}

function updateModelUI() {
  const provider = selectedProvider();
  const model = selectedModel();
  document.querySelector('#generationSettings').hidden = !provider || !model;
  elements.priceCard.hidden = !model;
  if (!provider || !model) { clearTimeout(quoteTimer); ++quoteSequence; return; }
  const isVideo = model.media_type === 'video';
  const isImage = model.media_type === 'image';
  const isSpeech = model.media_type === 'speech';
  FormUI.setLabel(elements.prompt,model.prompt_optional ? 'Prompt (optional)' : 'Prompt');
  elements.prompt.closest('.floating-field').hidden = !!model.no_prompt;
  elements.promptCount.parentElement.hidden = !!model.no_prompt;
  elements.prompt.disabled = !!model.no_prompt;
  elements.resolutionField.hidden = (!isVideo && !isImage) || !model.resolutions?.length;
  elements.ratioField.hidden = (!isVideo && !isImage) || !model.ratios?.length;
  elements.durationField.hidden = !(model.max_duration > 0) || !!model.duration_from_input;
  elements.resolution.disabled = elements.resolutionField.hidden;
  elements.ratio.disabled = elements.ratioField.hidden;
  elements.duration.disabled = elements.durationField.hidden;
  if (model.resolutions?.length) fillSelect(elements.resolution, model.resolutions, model.resolutions[0]);
  if (model.ratios?.length) fillSelect(elements.ratio, model.ratios, isVideo && model.ratios.includes('16:9') ? '16:9' : model.ratios[0]);
  if (!elements.durationField.hidden) {
    elements.duration.min = model.min_duration;
    elements.duration.step = model.duration_step || 1;
    elements.duration.max = model.max_duration;
    elements.duration.value = model.default_duration;
    elements.durationHint.textContent = model.duration_step ? `${model.min_duration}–${model.max_duration} seconds, in steps of ${model.duration_step}` : `${model.min_duration}–${model.max_duration} seconds`;
  }
  elements.imageURLsField.hidden = !model.supports_image_input;
  elements.videoURLField.hidden = !model.supports_video_input;
  document.querySelector('#inputDurationHint').hidden = !model.duration_from_input;
  elements.audioURLField.hidden = !model.supports_audio_input;
  elements.negativePromptField.hidden = !model.supports_negative_prompt;
  elements.voiceField.hidden = !model.supports_voice;
  elements.languageField.hidden = !isSpeech || !!model.no_language;
  elements.instructionsField.hidden = !isSpeech || !!model.no_instructions;
  elements.lyricsField.hidden = !model.supports_lyrics;
  elements.genderField.hidden = !model.supports_gender;
  renderModelParameters(model);
  elements.qualityField.hidden = !model.qualities?.length;
  elements.backgroundField.hidden = !model.backgrounds?.length;
  if (model.qualities?.length) fillSelect(elements.quality, model.qualities, model.qualities[0]);
  if (model.backgrounds?.length) fillSelect(elements.background, model.backgrounds, model.backgrounds[0]);
  elements.formatField.hidden = !model.formats?.length;
  elements.voiceOptions.replaceChildren(...(model.voices || []).map(value => new Option(value, value)));
  elements.voice.value = model.voices?.[0] || '';
  if (model.formats?.length) fillSelect(elements.format, model.formats, model.formats[0]);
  elements.watermarkOption.hidden = isSpeech || !!model.no_watermark;
  elements.seed.closest('div').hidden = !!model.no_seed;
  elements.seed.max = model.max_seed || (isVideo ? 4294967295 : 2147483647);
  document.querySelectorAll('#commonMediaFields > div').forEach(field => {
    field.querySelectorAll('input, select, textarea').forEach(input => { input.disabled = field.hidden; });
  });
  [elements.generateAudio, elements.cameraFixed, elements.draft, elements.returnLastFrame]
    .forEach(element => { element.checked = false; });
  updateDynamicCapabilities();
  window.referencePicker?.render();
  updatePrice();
}

function updateDynamicCapabilities() {
  const model = selectedModel();
  if (!model) return;
  elements.audioOption.hidden = !model.supports_audio || !!model.audio_always_on;
  elements.cameraOption.hidden = !model.supports_camera_fixed;
  const draftSupported = model.supports_draft && (!model.draft_resolutions?.length || model.draft_resolutions.includes(elements.resolution.value));
  elements.draftOption.hidden = !draftSupported;
  elements.lastFrameOption.hidden = !model.supports_last_frame;
  if (!model.supports_audio) elements.generateAudio.checked = false;
  if (!model.supports_camera_fixed) elements.cameraFixed.checked = false;
  if (!draftSupported) elements.draft.checked = false;
  if (!model.supports_last_frame) elements.returnLastFrame.checked = false;
}

function mediaTypeLabel(value) {
  return ({video: 'Video', image: 'Image', speech: 'Speech', music: 'Music', audio: 'Audio'})[value] || value;
}

function updateModelSelection(preferred = elements.model.value) {
  ModelPicker.populate(elements.model, providerCatalog, {value: preferred, label: modelOptionLabel});
  const options = [...elements.model.options];
  const defaultProvider = providerCatalog.find(provider => provider.id === serviceInfo.provider);
  const defaultValue = defaultProvider && modelSelectionValue(defaultProvider.id, defaultProvider.default_model);
  elements.model.value = options.find(option => option.value === preferred)?.value
    || options.find(option => option.value === defaultValue)?.value || options[0]?.value || '';
  elements.model.disabled = options.length === 0;
  if (!options.length) elements.model.append(new Option('No models available', ''));
  elements.submit.disabled = !options.length;
  updateModelUI();
}

function updateCatalogUI() {
  elements.submitLabel.textContent = 'Generate';
  updateModelSelection();
}

let quoteSequence = 0;
let quoteTimer;
function estimateRequest() {
 const provider=selectedProvider(), model=selectedModel();
 if(!provider || !model) return null;
 return {prompt:model.no_prompt ? '' : elements.prompt.value.trim(), provider:provider.id, model:model.id, media_type:model.media_type, options:requestOptions(model)};
}
function generationRequest(request) {
 const {provider, model, media_type, prompt, options: source, ...extra}=request;
 const options={...source};
 const publicID=providerCatalog.find(p=>p.id===provider)?.models.find(m=>m.id===model)?.public_id || provider+'/'+model;
 const body={...extra, model:publicID, options};
 if(media_type==='speech') {
  body.input=prompt;
  for(const [from,to] of [['voice','voice'],['instructions','instructions'],['format','response_format']]) {
   if(options[from]) body[to]=options[from];
   delete options[from];
  }
 } else body.prompt=prompt;
 if(media_type==='video' && options.duration) body.seconds=String(options.duration);
 if(media_type==='video') delete options.duration;
 if(media_type==='image') {
  for(const [from,to] of [['quality','quality'],['background','background'],['format','output_format']]) {
   if(options[from]) body[to]=options[from];
   delete options[from];
  }
 }
 if(['image','video'].includes(media_type) && options.resolution) {body.size=options.resolution.replace('*','x');delete options.resolution;}
 if(['image','video'].includes(media_type) && options.image_urls?.length) {
  body[media_type==='image'?'image':'input_reference']=options.image_urls;
  }
 delete options.image_urls;
 for(const [singular,plural] of [['video_url','video_urls'],['audio_url','audio_urls']]) {
  if(options[singular]) options[plural]=[options[singular]];
  delete options[singular];
 }
 return body;
}
async function fetchEstimate(request) {
 return api('/v1/generations/estimate',{method:'POST',body:JSON.stringify(generationRequest(request))});
}

function formatUSD(value) {
  if (value == null || !Number.isFinite(Number(value))) return "—";
  return Number(value).toLocaleString('en-US', {style:'currency', currency:'USD', minimumFractionDigits:0, maximumFractionDigits:2});
}

function formatPrice(value) {
  if (value === 0) return 'Free';
  return `≈ ${formatUSD(value)}`;
}

function updatePrice() {
 clearTimeout(quoteTimer);
 const sequence=++quoteSequence;
 const request=estimateRequest(); if(!request) return;
 elements.priceValue.textContent='Calculating…';
 FormUI.message(elements.priceDetails,'');
 quoteTimer=setTimeout(async()=>{
  try {
   const quote=await fetchEstimate(request);
   if(sequence!==quoteSequence) return;
   elements.priceValue.textContent=formatPrice((quote.cost.customer_expected_usd ?? quote.cost.expected_usd));
   elements.priceCard.classList.toggle('unknown',!quote.can_generate);
   FormUI.message(elements.priceDetails,quote.can_generate ? (quote.cost.pricing?.estimate_description || 'Final generation cost may differ.') : `Exceeds the available organization or weekly allowance (${formatUSD(quote.remaining_usd)}).`,quote.can_generate?'':'error');
  } catch(error) {
   if(sequence!==quoteSequence) return;
   const hasInput=request.prompt || selectedModel()?.prompt_optional || selectedModel()?.no_prompt;
   elements.priceValue.textContent=hasInput ? 'Estimate unavailable' : 'Enter a prompt';
   FormUI.message(elements.priceDetails,hasInput ? error.message : '',hasInput?'error':'');
   elements.priceCard.classList.add('unknown');
  }
 },400);
}

let budgetSequence = 0;
async function refreshGenerationBudget() {
 const sequence = ++budgetSequence, panel = document.querySelector('#generationKeyBudget');
 const key = window.GenerationKey.value();
 panel.hidden = !key;
 if (!key) { panel.replaceChildren(); return; }
 if (panel.dataset.keyState !== 'loaded') panel.textContent = 'Loading key…';
 const node = (tag, text, className) => { const el = document.createElement(tag); el.textContent = text; if (className) el.className = className; return el; };
 const usd = formatUSD;
 try {
  const result = await api('/v1/key');
  if (sequence !== budgetSequence || key !== window.GenerationKey.value()) return;
  panel.replaceChildren(); panel.dataset.keyState = 'loaded';
  if (!result.key) { panel.append(node('p', 'Service key')); return; }
  panel.append(node('strong', result.key.name, 'generation-key-name'));
  const o=result.organization;
  panel.append(node('p',`${o?.name || (result.key.organization_id?'Organization unavailable':'Profile')} · ${result.key.resource_mode==='shared'?'Shared':'Personal'} providers · ${result.key.resource_mode==='shared'?'Organization':'Personal'} storage`));
  if (result.key.resource_mode!=='shared') {panel.append(node('p','Generation is billed to your provider account.'));return;}
  if (!o) {panel.append(node('p','Organization allowance unavailable.'));return;}
  const stats=node('dl','','generation-budget-stats');
  for(const [label,value] of [['This week',usd(o.weekly_committed_usd)],['Weekly limit',o.weekly_limit_usd==null?'Unlimited':usd(o.weekly_limit_usd)]]){
    const item=node('div','');item.append(node('dt',label),node('dd',value));stats.append(item);
  }
  panel.append(stats,node('p',weeklyResetText(),'generation-budget-month'));
  const weeklyRemaining=o.weekly_limit_usd==null?Infinity:o.weekly_limit_usd-o.weekly_committed_usd;
  if(o.available_usd<weeklyRemaining)panel.append(node('p',`Organization funds available: ${usd(o.available_usd)}.`, 'generation-budget-warning'));
  panel.classList.toggle('over-budget', Math.min(o.available_usd,weeklyRemaining)<0);

 } catch (error) {
  if (sequence !== budgetSequence || key !== window.GenerationKey.value()) return;
  panel.dataset.keyState = ''; panel.replaceChildren(node('p','Budget unavailable. Retry by refreshing the page.','operation-error'));
 }
}

async function loadProviders() {
  if (!window.GenerationKey.value()) return;
  try {
    const response = await api('/v1/providers');
    providerCatalog = response.data || [];
    updateCatalogUI();
  } catch (error) {
    if (error.name === 'AbortError') return;
    providerCatalog = []; updateCatalogUI();
    elements.model.replaceChildren(new Option('Could not load models', ''));
    setMessage(error.status === 401 ? 'Enter a valid Gateway API key above.' : `Could not load providers: ${error.message}`, 'error');
  }
}

function requestOptions(model) {
  const options = {};
  const parameters={};for(const field of model.parameters||[]){const input=document.getElementById('model-param-'+field.key);if(!input)continue;if(input.type==='checkbox')parameters[field.key]=input.checked;else if(input.value!=='')parameters[field.key]=['number','integer'].includes(field.type)?Number(input.value):input.value;}if(Object.keys(parameters).length)options.parameters=parameters;

  if (model.media_type !== 'speech' && !model.no_watermark) options.watermark = elements.watermark.checked;
  if (['video', 'image'].includes(model.media_type) && model.resolutions?.length) options.resolution = elements.resolution.value;
  if (['video', 'image'].includes(model.media_type) && model.ratios?.length) options.ratio = elements.ratio.value;
  if (model.max_duration > 0 && !model.duration_from_input) {
    options.duration = Number(elements.duration.value);
  }
  if (model.supports_audio) options.generate_audio = model.audio_always_on || elements.generateAudio.checked;
  if (model.supports_camera_fixed) options.camera_fixed = elements.cameraFixed.checked;
  if (model.supports_draft) options.draft = elements.draft.checked;
  if (model.supports_last_frame) options.return_last_frame = elements.returnLastFrame.checked;
  if (!model.no_seed && elements.seed.value !== '') options.seed = Number(elements.seed.value);
  if (model.supports_negative_prompt && elements.negativePrompt.value.trim()) options.negative_prompt = elements.negativePrompt.value.trim();
  if (!document.querySelector("#referenceModeField").hidden) options.reference_mode=document.querySelector("#referenceMode").value;
  if (model.supports_image_input && !elements.imageURLsField.hidden) options.image_urls = elements.imageURLs.value.split('\n').map(value => value.trim()).filter(Boolean);
  if (model.supports_video_input && !elements.videoURLField.hidden && elements.videoURL.value.trim()) options.video_urls = elements.videoURL.value.split('\n').map(v=>v.trim()).filter(Boolean);
  if (model.supports_audio_input && !elements.audioURLField.hidden && elements.audioURL.value.trim()) options.audio_urls = elements.audioURL.value.split('\n').map(v=>v.trim()).filter(Boolean);
  if (model.supports_voice) options.voice = elements.voice.value;
  if (model.media_type === 'speech') {
    if (!model.no_language) options.language = elements.language.value;
    if (!model.no_instructions && elements.instructions.value.trim()) options.instructions = elements.instructions.value.trim();
  }
  if (model.supports_lyrics && elements.lyrics.value.trim()) options.lyrics = elements.lyrics.value.trim();
  if (model.supports_gender) options.gender = elements.gender.value;
  if (model.qualities?.length) options.quality = elements.quality.value;
  if (model.backgrounds?.length) options.background = elements.background.value;
  if (model.formats?.length) options.format = elements.format.value;
  const voices=window.referencePicker?.voices();if(voices&&Object.keys(voices).length)options.reference_voices=voices;
  return options;
}

elements.prompt.addEventListener('input', () => {
  elements.promptCount.textContent = `${elements.prompt.value.length.toLocaleString('en-US')} / 10,000`;
});
window.addEventListener('gateway-key-change', () => {
  taskListRequest?.controller.abort();
  elements.taskList.querySelectorAll('.task').forEach(disposeTaskCard);
  providerCatalog = []; latestTasks = []; taskCompletionUntil.clear(); renderedTasksFingerprint = ''; pendingSubmission = null;
  window.referencePicker?.reset();
  elements.taskList.replaceChildren(); elements.model.replaceChildren(new Option('Enter an API key first', ''));
  elements.model.disabled = true;
  clearTimeout(quoteTimer); ++quoteSequence;
  elements.priceValue.textContent = 'Key required'; elements.priceDetails.textContent = '';
  const ready = !!window.GenerationKey.value(); elements.submit.disabled = !ready;
  document.querySelector('#generationFields').hidden = !ready;
  document.querySelector('#generationSettings').hidden = true;
  elements.priceCard.hidden = !ready; elements.submit.hidden = !ready;
  document.querySelector('#generationKeyBudget').dataset.keyState = '';
  refreshGenerationBudget();
  if (ready) { setMessage(''); loadProviders(); loadTasks(true); }
});
elements.generationNav.addEventListener('click', () => showConsoleSection('generation'));
document.querySelector('#systemNav').addEventListener('click',()=>showConsoleSection('system'));
document.querySelector('#organizationsNav').addEventListener('click',()=>showConsoleSection('organizations'));
document.querySelector('#configurationNav').addEventListener('click', () => showConsoleSection('configuration'));
elements.providersNav.addEventListener('click', () => showConsoleSection('providers'));
document.querySelector('#reportsNav').addEventListener('click', () => showConsoleSection('reports'));
document.querySelector('#keysNav').addEventListener('click', () => showConsoleSection('keys'));
document.querySelector('#storageNav').addEventListener('click', () => showConsoleSection('storage'));
document.querySelector('#docsNav').addEventListener('click', () => showConsoleSection('docs'));
elements.logout.addEventListener('click', async () => {
	elements.logout.disabled = true;
	elements.sessionMessage.hidden = true;
	try {
		const response = await fetch('/auth/logout', {method: 'POST', credentials: 'same-origin'});
		if (!response.ok) throw new Error('Sign out could not be completed. Please try again.');
		window.GenerationKey.signOut();
		window.location.reload();
	} catch (error) {
		FormUI.message(elements.sessionMessage, error.message || 'Sign out could not be completed. Please try again.', 'error');
		elements.sessionMessage.hidden = false;
	} finally {
		elements.logout.disabled = false;
	}
});
elements.adminProviderEnabled.addEventListener('change', () => {
  const provider = providerAdminCatalog.find(item => item.id === selectedAdminProviderID);
  if (!provider?.configured) {
    setProviderMessage('Add an API key and save to apply this availability setting.');
    return;
  }
  updateAdminProvider(
    {enabled: elements.adminProviderEnabled.checked},
    elements.adminProviderEnabled.checked ? 'Provider enabled and available in Playground.' : 'Provider disabled and hidden from Playground.'
  );
});
elements.providerForm.addEventListener('submit', event => {
  event.preventDefault();
  const provider = providerAdminCatalog.find(item => item.id === selectedAdminProviderID);
  if (!provider) return;
  const payload = {enabled: elements.adminProviderEnabled.checked};
  for(const field of provider.fields||[]){const input=elements.providerForm.querySelector(`[data-provider-field="${field.key}"]`);if(!input)continue;const value=field.secret?FormUI.credentials.updateValue(input):input.value.trim();if(value!==undefined)payload[field.key]=value;}
  try{Object.assign(payload,window.ProviderConnections?.payload(elements.providerForm),{version:provider.version});}catch(error){setProviderMessage(error.message,'error');return;}
  updateAdminProvider(payload, '', true);
});
elements.checkProvider.addEventListener('click', async () => {
  if (!selectedAdminProviderID) return;
  const id = selectedAdminProviderID;
  elements.checkProvider.disabled = true;
  setProviderCheckFeedback('Checking connection…');
  try {
    const provider = await api(`/v1/admin/providers/${id}/check`, {method: 'POST'});
    providerAdminCatalog = providerAdminCatalog.map(item => item.id === provider.id ? provider : item);
    renderProviderAdminList();
    if (selectedAdminProviderID === id) {
      renderProviderDetail(provider);
      setProviderCheckFeedback(provider.status_message || (provider.status === 'connected' ? 'Connection verified.' : 'Connection status updated.'), provider.status === 'error' ? 'error' : provider.status === 'connected' ? 'success' : '');
    }
  } catch (error) {
    if (selectedAdminProviderID === id) setProviderCheckFeedback(`Connection check failed: ${error.message}`, 'error');
  } finally {
    elements.checkProvider.disabled = false;
  }
});
elements.model.addEventListener('change', updateModelUI);
[elements.quality, elements.background, elements.ratio, elements.duration, elements.generateAudio, elements.watermark, elements.cameraFixed, elements.draft, elements.returnLastFrame]
  .forEach(element => element.addEventListener('change', updatePrice));
elements.resolution.addEventListener('change', () => { updateDynamicCapabilities(); updatePrice(); });
elements.duration.addEventListener('input', updatePrice);

const optionFields = {
  quality: 'quality', background: 'background',
  resolution: 'resolution', ratio: 'ratio', duration: 'duration', generate_audio: 'generateAudio',
  watermark: 'watermark', camera_fixed: 'cameraFixed', draft: 'draft', seed: 'seed',
  reference_mode: 'referenceMode', video_urls:'videoURL',audio_urls:'audioURL', return_last_frame: 'returnLastFrame', negative_prompt: 'negativePrompt', image_urls: 'imageURLs',
  video_url: 'videoURL', audio_url: 'audioURL', voice: 'voice', language: 'language',
  instructions: 'instructions', lyrics: 'lyrics', gender: 'gender', format: 'format'
};
const optionLabels = {
 parameters: 'Additional parameters',
  quality: 'Quality', background: 'Background',
  resolution: 'Resolution', ratio: 'Aspect ratio', duration: 'Duration', generate_audio: 'Audio',
  watermark: 'Watermark', camera_fixed: 'Fixed camera', draft: 'Draft', seed: 'Seed',
  reference_voices:'Reference voices',reference_mode:'Reference mode',video_urls:'Video references',audio_urls:'Audio references', return_last_frame: 'Return last frame', negative_prompt: 'Negative prompt', image_urls: 'Image references',
  video_url: 'Video reference', audio_url: 'Audio reference', voice: 'Voice', language: 'Language',
  instructions: 'Voice instructions', lyrics: 'Lyrics', gender: 'Vocals', format: 'File format'
};
function optionDefaults(model) {
  return {quality: model.qualities?.[0], background: model.backgrounds?.[0], resolution: model.resolutions?.[0], ratio: model.ratios?.includes('16:9') ? '16:9' : model.ratios?.[0],
    duration: model.default_duration, voice: model.voices?.[0], language: 'Auto', gender: 'female', format: model.formats?.[0],
    generate_audio: false, watermark: false, camera_fixed: false, draft: false, return_last_frame: false};
}
function taskParameterRows(options, defaults = {}) {
  const describe = (url, index) => window.referencePicker?.describe(url, index) || url;
  return Object.entries(options).filter(([key, value]) =>
    optionLabels[key] && !(key === 'video_url' && options.video_urls?.length) &&
    !(key === 'audio_url' && options.audio_urls?.length) && !(key === 'duration' && value <= 0) &&
    value !== '' && value != null && value !== false && !(Array.isArray(value) && !value.length) && value !== defaults[key]
  ).map(([key, value]) => {
    let text;
    if (key === 'parameters') text = Object.entries(value).map(([key,value])=>`${key}: ${value}`).join(' · ');
    else if (key === 'reference_voices') text = Object.entries(value).map(([visual, voice], i) => `${describe(visual,i)} → ${describe(voice,i)}`).join('\n');
    else if (Array.isArray(value)) text = (['image_urls','video_urls','audio_urls'].includes(key) ? value.map(describe) : value).join('\n');
    else if (key === 'reference_mode') text = ({none:'Text only',first_frame:'First frame',first_last_frames:'First and last frames',references:'References',continue_video:'Continue video',continue_video_last_frame:'Continue video to last frame',first_frame_references:'First frame and references'})[value] || value;
    else text = value === true ? 'On' : key === 'duration' ? `${value} s` : String(value);
    return [optionLabels[key], text];
  });
}
function fillSummary(container, rows) {
  container.replaceChildren(...rows.map(([label, value]) => {
    const row = document.createElement('div');
    const term = document.createElement('dt'); term.textContent = label;
    const detail = document.createElement('dd'); detail.textContent = value;
    row.append(term, detail); return row;
  }));
}
function updateTimer(timer) {
  const start = Date.parse(timer.dataset.createdAt);
  const end = timer.dataset.finishedAt ? Date.parse(timer.dataset.finishedAt) : Date.now();
  if (!Number.isFinite(start) || !Number.isFinite(end)) { timer.textContent = ''; return; }
  const seconds = Math.max(0, Math.floor((end - start) / 1000));
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor(seconds % 3600 / 60);
  timer.textContent = `Time ${hours ? hours+'h ' : ''}${minutes ? minutes+'m ' : ''}${seconds % 60}s`;
}
function reuseTask(task) {
  const provider = providerCatalog.find(item => item.id === task.provider);
  if (!provider?.models.some(item => item.id === task.model)) {
    setMessage('This provider or model is unavailable. Enable it in Providers before copying.', 'error');
    elements.message.scrollIntoView({block: 'center'});
    return;
  }
  updateModelSelection(modelSelectionValue(task.provider, task.model));
  const options = task.options || {};
  const defaults = optionDefaults(selectedModel());
  for (const [key, field] of Object.entries(optionFields)) {
    const input = elements[field] || document.getElementById(field);
    if ((key==='video_url'&&options.video_urls?.length)||(key==='audio_url'&&options.audio_urls?.length)) continue;
    const value = options[key] ?? defaults[key] ?? (key==='reference_mode' ? input.options[0]?.value : '') ?? '';
    if (input.type === 'checkbox') input.checked = value === true;
    else input.value = Array.isArray(value) ? value.join('\n') : value;
  }
  renderModelParameters(selectedModel(),options.parameters);
  window.referencePicker?.restoreMode(options);
  window.referencePicker?.restoreVoices(options.reference_voices);
  elements.prompt.value = task.prompt || promptHistory[task.id] || '';
  window.referencePicker?.render();
  elements.prompt.dispatchEvent(new Event('input'));
  updateDynamicCapabilities(); updatePrice();
  elements.form.querySelector('.advanced-settings').open = ['watermark', 'seed', 'draft', 'return_last_frame'].some(key => options[key] != null && options[key] !== false);
  document.querySelectorAll('.task-menu[open]').forEach(menu => { menu.open = false; });
  setMessage('Parameters copied. Ready to edit.', 'success');
  elements.prompt.scrollIntoView({behavior: 'smooth', block: 'center'});
  elements.prompt.focus({preventScroll: true});
}
function closeConfirmation() {
  if (submitting) return;
  dialog.close(); pendingSubmission = null;
  elements.submit.focus();
}
closeButton.addEventListener('click', closeConfirmation);
cancelButton.addEventListener('click', closeConfirmation);
dialog.addEventListener('cancel', event => { event.preventDefault(); closeConfirmation(); });
document.addEventListener('click', event => {
  document.querySelectorAll('.task-menu[open]').forEach(menu => { if (!menu.contains(event.target)) menu.open = false; });
});
document.addEventListener('keydown', event => {
  if (event.key === 'Escape') document.querySelectorAll('.task-menu[open]').forEach(menu => { menu.open = false; menu.querySelector('summary').focus(); });
});
elements.form.addEventListener('input', updatePrice);
elements.form.addEventListener('change', updatePrice);
elements.form.addEventListener('submit', async event => {
  event.preventDefault();
  if (submitting || dialog.open) return;
  if (window.referencePicker && !window.referencePicker.validate()) return;
  if (window.PromptReferences && !window.PromptReferences.validate()) return;
  const provider = selectedProvider();
  const model = selectedModel();
  const prompt = model?.no_prompt ? '' : elements.prompt.value.trim();
  const lyricsOnly = model?.media_type === 'music' && model.supports_lyrics && elements.lyrics.value.trim();
  if ((!prompt && !lyricsOnly && !model?.prompt_optional && !model?.no_prompt) || !provider || !model) {
    setMessage('Enter a prompt and select a model.', 'error'); return;
  }
  const request = {prompt, provider: provider.id, model: model.id, media_type: model.media_type, options: requestOptions(model)};
  pendingSubmission = {request, key: crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`};
  const rows = [['Provider', provider.name], ['Model', model.display_name], ['Media type', mediaTypeLabel(model.media_type)]];
  if (prompt) rows.push(['Prompt', prompt]);
  rows.push(...taskParameterRows(request.options, optionDefaults(model)));
  fillSummary(document.querySelector('#confirmFields'), rows);
  document.querySelector('#confirmPrice').textContent = 'Calculating…';
  confirmButton.disabled=true;
  confirmStatus.hidden = true; confirmStatus.className = '';
  confirmButton.textContent = 'Confirm and generate →';
  setMessage(''); dialog.showModal(); cancelButton.focus();
  const submission=pendingSubmission;
  try {
   const quote=await fetchEstimate(request);
   if(pendingSubmission!==submission || !dialog.open) return;
   document.querySelector('#confirmPrice').textContent=formatPrice((quote.cost.customer_expected_usd ?? quote.cost.expected_usd));
   submission.request.max_expected_cost_usd=(quote.cost.customer_expected_usd ?? quote.cost.expected_usd);
   confirmButton.disabled=!quote.can_generate;
   confirmStatus.hidden=false;
   confirmStatus.className=quote.can_generate ? '' : 'error';
   confirmStatus.textContent=quote.can_generate ? ((quote.cost.customer_expected_usd ?? quote.cost.expected_usd)===0 ? 'This test generation is free.' : 'Cost is recorded after generation.') : `Your budget is exhausted (${formatUSD(quote.remaining_usd)} remaining).`;
  } catch(error) {
   if(pendingSubmission!==submission) return;
   document.querySelector('#confirmPrice').textContent='Estimate unavailable';
   confirmStatus.hidden=false;confirmStatus.className='error';confirmStatus.textContent=error.message;
  }
});
async function moveRequestToJournal(task) {
  renderedTasksFingerprint = '';
  renderTasks([task, ...latestTasks.filter(item => item.id !== task.id)]);
  const article = elements.taskList.firstElementChild;
  article.classList.add('task-arriving');
  article.scrollIntoView({block: 'nearest', behavior: 'instant'});
  const from = dialog.getBoundingClientRect();
  const to = article.getBoundingClientRect();
  if (!window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    dialog.classList.add('transferring');
    try {
      await dialog.animate([
        {transform: 'translate(0, 0) scale(1)', opacity: 1},
        {transform: `translate(${to.left + to.width / 2 - from.left - from.width / 2}px, ${to.top + Math.min(to.height, 160) / 2 - from.top - from.height / 2}px) scale(.25)`, opacity: 0}
      ], {duration: 650, easing: 'cubic-bezier(.4,0,.2,1)'}).finished;
    } finally { dialog.classList.remove('transferring'); }
  }
  dialog.close(); article.classList.remove('task-arriving'); article.classList.add('task-highlight');
  article.tabIndex = -1; article.focus({preventScroll: true});
  document.querySelector('#journalAnnouncement').textContent = 'Generation added to the journal.';
}
confirmButton.addEventListener('click', async () => {
  if (submitting || !pendingSubmission || pendingSubmission.request.max_expected_cost_usd === undefined) return;
  submitting = true;
  const submission = pendingSubmission;
  confirmButton.disabled = true; cancelButton.disabled = true; closeButton.disabled = true;
  elements.submit.disabled = true;
  dialog.setAttribute('aria-busy', 'true');
  confirmStatus.hidden = false; confirmStatus.className = 'sending';
  confirmStatus.textContent = 'Preparing and sending your request…';
  confirmButton.textContent = 'Sending…';
  let accepted = false;
  try {
    // Keep the same idempotency key for retries after uncertain network failures.
    const [result] = await Promise.allSettled([
      api('/v1/generations', {method: 'POST', headers: {'Idempotency-Key': submission.key}, body: JSON.stringify(generationRequest(submission.request))}),
      new Promise(resolve => setTimeout(resolve, 2000))
    ]);
    if (result.status === 'rejected') throw result.reason;
    const task = {...submission.request, ...result.value};
    accepted = true; pendingSubmission = null;
    elements.prompt.value = ''; elements.prompt.dispatchEvent(new Event('input'));
    await moveRequestToJournal(task);
  } catch (error) {
    if (accepted) {
      dialog.close(); // Never offer another submission after the API accepted it.
    } else {
      confirmStatus.className = 'error';
      confirmStatus.textContent = `Could not submit: ${error.message}`;
      confirmButton.textContent = 'Retry submission →';
      if(['estimate_changed','budget_exceeded','pricing_unavailable'].includes(error.code)) {
        dialog.close();pendingSubmission=null;updatePrice();
        setMessage(`${error.message}. Review the updated estimate before confirming again.`, 'error');
      }
    }
  } finally {
    submitting = false;
    confirmButton.disabled = false; cancelButton.disabled = false; closeButton.disabled = false;
    elements.submit.disabled = !window.GenerationKey.value(); dialog.removeAttribute('aria-busy');
    if (accepted) { loadTasks(); updatePrice(); refreshGenerationBudget(); }
  }
});

async function initialize() {
  await loadInfo();
	authMode = serviceInfo.auth_mode || 'api_key';
  consolePolicy={...consolePolicy,...(serviceInfo.console||{})};elements.generationNav.hidden=!consolePolicy.generation_ui;elements.providersNav.hidden=!consolePolicy.personal_providers;
	if (authMode === 'google') {
    await window.EmailLogin?.initialize();
    if(window.EmailLogin?.hasAction()){consoleInitialized=true;showConsoleSection(currentConsoleSection);return;}
		elements.connectionPanel.hidden = false;
		try {
			const response = await fetch('/v1/me', {credentials: 'same-origin'});
			if (!response.ok) throw new Error('unauthenticated');
			const user = await response.json();
      window.GatewayProfile?.identify(user);
			elements.userName.textContent = user.name || user.email;
			elements.userEmail.textContent = user.email;
			const initials = document.querySelector('#userInitials');
			initials.textContent = (user.name || user.email || '?').trim().split(/\s+/).map(part => Array.from(part)[0]).slice(0, 2).join('').toUpperCase();
			elements.userPicture.addEventListener('load', () => { elements.userPicture.hidden = false; initials.hidden = true; });
			elements.userPicture.addEventListener('error', () => { elements.userPicture.hidden = true; initials.hidden = false; });
			if (user.picture) elements.userPicture.src = user.picture;
			elements.userMenu.hidden = false;
			elements.sectionNav.hidden = false;
			elements.authGate.hidden = true;
			consoleAuthenticated = true;
      consoleInitialized=true;
      clientManager=!!user.is_client_manager;clientManagerRole=user.sales_role||(clientManager?'client_manager':'');
      window.GatewaySystem?.identify(user);window.applySystemAccess(serviceInfo.console||{},!!user.is_system_admin);
      await window.Workspace.initialize(user);
			showConsoleSection(currentConsoleSection);
			if(clientManager)window.GenerationKey.forget();else window.GenerationKey.initialize(user);
		} catch {
      consoleInitialized=true;
			window.GenerationKey.forget('Sign in again and enter your generation key.');
			showConsoleSection(currentConsoleSection);
			elements.authError.hidden = !new URLSearchParams(window.location.search).has('auth_error');
		}
		return;
	}
  consoleInitialized=true;
	elements.authGate.hidden = true;
	elements.sectionNav.hidden = false;
  showConsoleSection(currentConsoleSection);
  window.GenerationKey.initialize(null);
}

showConsoleSection(currentConsoleSection);
initialize();
setInterval(() => {
  if (!document.hidden && currentConsoleSection === 'generation' && window.GenerationKey.value()) loadTasks();
}, 2000);
document.addEventListener('visibilitychange', () => {
  if (document.hidden) taskListRequest?.controller.abort();
  else if (currentConsoleSection === 'generation') loadTasks();
});
setInterval(() => { if (window.GenerationKey.value()) refreshGenerationBudget(); }, 15000);
window.addEventListener('focus', () => { if (window.GenerationKey.value()) refreshGenerationBudget(); });
setInterval(() => document.querySelectorAll('.task-elapsed').forEach(updateTimer), 1000);


let storageLoadSequence=0;
async function loadStorageSettings(){
  const sequence=++storageLoadSequence,scope=sharedConfiguration,parent=document.querySelector('#storageContent');
  parent.replaceChildren();
  // Remove the previous external Save button when the same view changes scope.
  document.querySelector('#storageView .form-heading #saveStorage')?.remove();
  await StorageSettings.open(parent,{base:scope?'/v1/system/storage':'/v1/admin/storage',heading:document.querySelector('#storageView h1'),allowLocal:scope||systemAdministrator||authMode!=='google',legacyIDs:true,isCurrent:()=>sequence===storageLoadSequence&&scope===sharedConfiguration});
}

function confirmResultDeletion(task){
 const dialog=document.createElement('dialog');dialog.className='keys-revoke-dialog';dialog.setAttribute('aria-labelledby','deleteResultTitle');
 dialog.innerHTML='<h2 id="deleteResultTitle">Delete stored result files?</h2><p>This deletes the saved result and last-frame files from storage. The generation, prompt, parameters and accounting history stay in your journal. The provider link will remain available, but it may expire.</p><p role="status"></p><div class="docs-actions"><button type="button" class="secondary" data-cancel>Cancel</button><button type="button" class="secondary docs-danger" data-confirm>Delete files</button></div>';
 document.body.append(dialog);dialog.querySelector('[data-cancel]').onclick=()=>dialog.close();dialog.addEventListener('close',()=>dialog.remove());
 const confirm=dialog.querySelector('[data-confirm]');confirm.onclick=async()=>{confirm.disabled=true;FormUI.message(dialog.querySelector('[role=status]'),'Deleting…');try{await api(`/v1/generations/${task.id}/content`,{method:'DELETE'});dialog.close();await loadTasks(true);window.dispatchEvent(new Event('gateway-storage-change'));}catch(error){FormUI.message(dialog.querySelector('[role=status]'),error.message,'error');confirm.disabled=false;}};dialog.showModal();dialog.querySelector('[data-cancel]').focus();
}

window.addEventListener('hashchange',()=>{if(location.pathname==='/configuration'){const name=location.hash.slice(1);if(configurationSections.includes(name)&&name!==currentConsoleSection)showConsoleSection(name)}});

function weeklyResetText() {
 const reset=new Date();reset.setUTCHours(0,0,0,0);
 reset.setUTCDate(reset.getUTCDate()+((8-reset.getUTCDay())%7||7));
 return 'Resets '+reset.toLocaleString(undefined,{weekday:'long',month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'})+'.';
}

function renderProviderFields(provider){
 const area=elements.providerForm.querySelector('.provider-dynamic-fields');
 area.replaceChildren();
 for(const field of provider.fields||[]){
  const label=document.createElement('label'),input=document.createElement('input');
  label.textContent=field.label+(field.group==='billing'?' (optional)':'');
  input.dataset.providerField=field.key;input.id='provider-field-'+field.key;input.type='text';input.autocomplete='off';input.spellcheck=false;
  if(field.secret){const hint=provider.hints?.[field.key]||'';FormUI.credentials.render(input,!!hint,hint);}else{input.value=provider.values?.[field.key]||field.default||'';}
  label.append(input);area.append(label);
 }
 FormUI.refresh();
}

function renderModelParameters(model,values={}){
 let area=document.querySelector('#modelParameters');if(!area){area=document.createElement('div');area.id='modelParameters';document.querySelector('#commonMediaFields').append(area);}area.replaceChildren();area.hidden=!(model.parameters||[]).length;
 for(const field of model.parameters||[]){const label=document.createElement('label'),input=document.createElement(field.type==='select'?'select':'input');label.textContent=field.label;input.id='model-param-'+field.key;
 if(field.type==='select'){for(const value of field.choices||[])input.append(new Option(value,value));}else{input.type=field.type==='boolean'?'checkbox':['number','integer'].includes(field.type)?'number':'text';}
 if(field.min!=null)input.min=field.min;if(field.max!=null)input.max=field.max;if(field.type==='integer')input.step=1;else if(field.type==='number')input.step='any';if(field.max_length)input.maxLength=field.max_length;
 if(field.type!=='boolean')input.required=!!field.required;const value=values[field.key]??field.default??'';if(input.type==='checkbox')input.checked=value===true;else input.value=value;
 input.addEventListener('change',()=>{pendingSubmission=null;updatePrice();});label.append(input);area.append(label);}
 FormUI.refresh();
}

window.addEventListener('model-settings-change',()=>{if(window.GenerationKey?.value())loadProviders().catch(()=>{});});
