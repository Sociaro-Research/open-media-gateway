(() => {
  'use strict';
  const content = document.querySelector('#docsContent');
  const menu = document.querySelector('#docsMenu');
  const search = document.querySelector('#docsSearch');
  let gatewayURL = document.querySelector('meta[name=gateway-url]')?.content || location.origin;
  const standalone = !!document.querySelector('meta[name=gateway-url]');
  const weeklyResetText = () => 'Weekly limits reset on Monday at 00:00 UTC.';
  const methods = ['get', 'post', 'put', 'patch', 'delete', 'head'];
  let spec, loading;
  const node = (tag, text, className) => {
    const el = document.createElement(tag);
    if (text !== undefined) el.textContent = text;
    if (className) el.className = className;
    return el;
  };
  const link = (text, page) => { const el = node('a', text); el.href = page === 'api-keys' ? gatewayURL + '/api-keys' : `#docs/${page}`; return el; };
  const resolve = value => value?.$ref ? value.$ref.slice(2).split('/').reduce((v, k) => v?.[k], spec) : value;
  const heading = (title, subtitle, kicker = 'API REFERENCE') => {
    content.append(node('p', kicker, 'docs-eyebrow'), node('h1', title));

  };
  const paragraph = (text, parent = content) => parent.append(node('p', text));
  const note = text => content.append(node('p', text, 'docs-note'));
  function codeBlock(text, language = 'cURL', parent = content) {
    const block = node('div', undefined, 'docs-code');
    const bar = node('div', undefined, 'docs-code-bar');
    bar.append(node('span', language));
    const pre = node('pre'); pre.append(node('code', text));
    const body=node('div',undefined,'docs-code-body');body.append(pre,FormUI.copyButton(()=>text,`Copy ${language} example`));
    block.append(bar, body); parent.append(block); return block;
  }
  function table(headers, rows, parent = content) {
    const wrap = node('div', undefined, 'docs-table-wrap');
    const t = node('table', undefined, 'docs-table');
    const head = node('thead'), tr = node('tr');
    headers.forEach(h => { const th = node('th', h); th.scope = 'col'; tr.append(th); }); head.append(tr);
    const body = node('tbody');
    rows.forEach(row => { const r = node('tr'); row.forEach(cell => { const td = node('td'); td.append(cell instanceof Node ? cell : document.createTextNode(String(cell))); r.append(td); }); body.append(r); });
    t.append(head, body); wrap.append(t); parent.append(wrap);
  }
  function fields(schema, parent = content, depth = 0) {
    schema = resolve(schema) || {};
    if (schema.description) paragraph(schema.description, parent);
    const variants = schema.oneOf || schema.anyOf;
    if (variants && depth < 4) {
      variants.forEach((variant, index) => {
        const details = node('details');
        details.append(node('summary', variant.$ref?.split('/').pop() || `Variant ${index + 1}`));
        fields(variant, details, depth + 1); parent.append(details);
      });
      return;
    }
    const props = schema.properties || (schema.type === 'array' ? {items: schema.items} : {});
    if (!Object.keys(props).length) {
      paragraph(schema.enum ? `Values: ${schema.enum.join(', ')}` : (schema.type || 'Any JSON value'), parent); return;
    }
    const list = node('div', undefined, 'docs-fields');
    Object.entries(props).forEach(([name, raw]) => {
      const field = resolve(raw) || {};
      const row = node('div', undefined, 'docs-field');
      const title = node('div', undefined, 'docs-field-name');
      title.append(node('code', name), node('span', `${field.type || 'object'}${field.format ? ` · ${field.format}` : ''}`));
      if (schema.required?.includes(name)) title.append(node('em', 'required'));
      if (field.writeOnly) title.append(node('em', 'write only'));
      row.append(title);
      if (raw.description || field.description) paragraph(raw.description || field.description, row);
      const constraints = [];
      if (field.enum) constraints.push(`Values: ${field.enum.join(' · ')}`);
      if (field.default !== undefined) constraints.push(`Default: ${JSON.stringify(field.default)}`);
      for (const k of ['minimum','maximum','minLength','maxLength','minItems','maxItems']) if (field[k] !== undefined) constraints.push(`${k}: ${field[k]}`);
      if (constraints.length) row.append(node('small', constraints.join('  |  ')));
      const nested = field.items || (field.properties || field.oneOf || field.anyOf ? field : null);
      if (nested && depth < 4) {
        const details = node('details'); details.append(node('summary', field.items ? 'Item schema' : 'Object fields')); fields(nested, details, depth + 1); row.append(details);
      }
      if (field.additionalProperties) paragraph('Additional properties: ' + (typeof field.additionalProperties === 'object' ? field.additionalProperties.type || 'JSON' : 'any JSON value'), row);
      list.append(row);
    });
    parent.append(list);
  }
  function parameters(params, parent = content) {
    if (!params.length) return;
    parent.append(node('h2', 'Parameters'));
    table(['Name', 'Location / type', 'Description'], params.map(raw => {
      const p = resolve(raw), f = resolve(p.schema) || {};
      return [node('code', p.name), `${p.in} · ${f.type || 'string'}${p.required ? ' · required' : ''}`, [p.description, f.enum && `Values: ${f.enum.join(', ')}`, f.default !== undefined && `Default: ${f.default}`, f.minimum !== undefined && `Min: ${f.minimum}`, f.maximum !== undefined && `Max: ${f.maximum}`, f.maxLength !== undefined && `Max length: ${f.maxLength}`].filter(Boolean).join(' ')];
    }), parent);
  }
  function endpoints() {
    return Object.entries(spec.paths).flatMap(([path, item]) => methods.filter(m => item[m]).map(method => ({path, method, item, op: item[method]})));
  }
  function searchable(value, seen = new Set()) {
    if (!value || typeof value !== 'object') return String(value || '');
    if (value.$ref) {
      if (seen.has(value.$ref)) return '';
      seen.add(value.$ref);
      return searchable(resolve(value), seen);
    }
    return Object.entries(value).map(([key, child]) => key + ' ' + searchable(child, seen)).join(' ');
  }
  function renderMenu() {
    const q = search.value.toLowerCase().trim(); menu.replaceChildren();
    const guides = [['overview','Overview'],['quickstart','Quickstart'],['openai','OpenAI-style API'],['authentication','Authentication'],['costs','Costs & budgets'],['webhooks','Webhooks'],['errors','Errors & retries']];
    menu.append(node('p', 'GET STARTED', 'docs-menu-group'));
    for (const [id, title] of guides) if (!q || title.toLowerCase().includes(q)) menu.append(link(title, id));
    if (spec) for (const tag of spec.tags) {
      const entries = endpoints().filter(e => e.op.tags?.includes(tag.name) && (!q || `${e.path} ${e.method} ${e.op.summary} ${searchable(e.op)}`.toLowerCase().includes(q)));
      if (!entries.length) continue;
      menu.append(node('p', tag.name.toUpperCase(), 'docs-menu-group'));
      for (const e of entries) {
        const a = link('', e.op.operationId); a.title = e.op.summary;
        a.append(node('span', e.method.toUpperCase(), `docs-method ${e.method}`), node('span', e.path)); menu.append(a);
      }
    }
    if (spec) {
      const names = Object.keys(spec.components.schemas).filter(n => !q || `${n} ${searchable(spec.components.schemas[n])}`.toLowerCase().includes(q));
      if (names.length) menu.append(node('p', 'SCHEMAS', 'docs-menu-group'));
      names.forEach(n => menu.append(link(n, `schema-${n}`)));
    }
    if (!menu.querySelector('a')) menu.append(node('p', 'No matches. Try a method, path or field name.'));
    markActive();
  }
  function markActive() {
    const hash = location.hash || '#docs/overview';
    menu.querySelectorAll('a').forEach(a => {
      const active = a.getAttribute('href') === hash;
      a.classList.toggle('active', active);
      if (active) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    });
  }
  function overview() {
    heading('Build with Gateway.', 'One API. Your providers. Image, video, speech and music generation for your applications.', 'GATEWAY / DEVELOPERS');
    const actions = node('div', undefined, 'docs-actions'); actions.append(link('Make your first request →', 'quickstart'), link('Create an API key', 'api-keys')); content.append(actions);
    const strip = node('div', undefined, 'docs-base'); strip.append(node('span', 'BASE URL'), node('code', gatewayURL), node('span', spec.info.version)); content.append(strip);
    const grid = node('div', undefined, 'docs-feature-grid');
    for (const [title, text] of [['Connect once','Provider credentials belong to your account and stay on Gateway.'],['Generate anywhere','Your console, bot and application use the same API and task history.'],['Keep the result','Poll a task or receive a webhook, then download the archived media.']]) {
      const card = node('div'); card.append(node('h3', title), node('p', text)); grid.append(card);
    }
    content.append(grid, node('h2', 'A predictable task lifecycle'));
    const lifecycle = node('div', undefined, 'docs-lifecycle'); ['queued','→','in_progress','→','completed / failed'].forEach(t => lifecycle.append(node('span', t))); content.append(lifecycle);
    paragraph('POST /v1/images/generations, /v1/videos and /v1/audio/speech accept work with HTTP 202. A worker runs the selected provider and stores the result. Poll status_url until completed or failed, then download content_url. All generation stays asynchronous.');
    content.append(node('h2', 'Explore the API'));
    table(['Resource','Use it to'], [['Tasks','Create media and track generations.'],['Catalog','Discover enabled providers, models and supported options.'],['Media','Download files or issue expiring preview links.'],['Providers admin','Configure provider connections and inspect balances.'],['API keys','Issue and revoke application credentials.']]);
    note('Generation uses OpenAI endpoint and parameter names with asynchronous Gateway extensions. Synchronous OpenAI SDK helpers still need job polling.');
  }
  function quickstart() {
    heading('Your first generation', 'Create a key, discover a model, submit a task and retrieve its result.', 'GET STARTED');
    paragraph('Sign in with Google or email and create a key in the API keys console section. Connect a provider in Providers, or use the enabled mock provider for a free fixed test asset. The examples below use mock-video-v1.');
    content.append(link('Manage your API keys →','api-keys'));
    const tabs = node('div', undefined, 'docs-tabs'); tabs.setAttribute('role','group'); tabs.setAttribute('aria-label','Example language');
    const target = node('div');
    const curl = `export GATEWAY_URL="${gatewayURL}"\nexport GATEWAY_API_KEY="YOUR_GATEWAY_API_KEY"\n\n# Discover your account's providers and models\ncurl -fsS "$GATEWAY_URL/v1/models" \\\n  -H "Authorization: Bearer $GATEWAY_API_KEY"\n\n# Use a fresh idempotency key per intended generation.\n# Reuse this value only when retrying the same request.\nexport IDEMPOTENCY_KEY="$(uuidgen)"\ncurl -fsS "$GATEWAY_URL/v1/videos" \\\n  -H "Authorization: Bearer $GATEWAY_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -H "Idempotency-Key: $IDEMPOTENCY_KEY" \\\n  -d '{"model": "mock/mock-video-v1",\n       "prompt":"A sunrise over the ocean",\n       "size":"1280x720","seconds":"5"}'\n\n# Copy id from the 202 response; repeat until completed or failed.\nexport TASK_ID="TASK_ID_FROM_RESPONSE"\ncurl -fsS "$GATEWAY_URL/v1/videos/$TASK_ID" \\\n  -H "Authorization: Bearer $GATEWAY_API_KEY"\n\n# Once completed, download the result.\ncurl -fSL "$GATEWAY_URL/v1/videos/$TASK_ID/content?download=1" \\\n  -H "Authorization: Bearer $GATEWAY_API_KEY" -o result.mp4`;
    const python = `# Python 3 · standard library only\n# Set GATEWAY_URL and GATEWAY_API_KEY in your environment.\nimport json, os, time, uuid\nfrom urllib.request import Request, urlopen\nfrom urllib.parse import urljoin\n\nbase = os.environ["GATEWAY_URL"].rstrip("/")\nheaders = {"Authorization": "Bearer " + os.environ["GATEWAY_API_KEY"],\n           "User-Agent": "GatewayClient/1.0"}\n\ndef request(path, payload=None, extra=None):\n    body = json.dumps(payload).encode() if payload is not None else None\n    req = Request(base + path, data=body, headers={\n        **headers, **({"Content-Type": "application/json"} if body else {}),\n        **(extra or {})})\n    with urlopen(req, timeout=30) as response:\n        return json.load(response)\n\nprint(request("/v1/models"))\nidempotency_key = str(uuid.uuid4())\ntask = request("/v1/videos", {\n    "model": "mock/mock-video-v1",\n    "prompt": "A sunrise over the ocean",\n    "size": "1280x720", "seconds": "5"\n}, {"Idempotency-Key": idempotency_key})\n\ndeadline = time.monotonic() + 600\nwhile task["status"] not in ("completed", "failed"):\n    if time.monotonic() > deadline:\n        raise TimeoutError("Keep the task ID and resume polling: " + task["id"])\n    time.sleep(2)\n    task = request("/v1/videos/" + task["id"])\nif task["status"] == "failed":\n    raise RuntimeError(task.get("error", "Generation failed"))\naccess = request("/v1/generations/" + task["id"] + "/media-access")\n# Signed URLs need no API credential, including on cross-host redirects.\nurl = urljoin(base + "/", access["download_url"])\nwith urlopen(Request(url, headers={"User-Agent": "GatewayClient/1.0"}), timeout=60) as response:\n    with open("result.mp4", "wb") as output:\n        while chunk := response.read(1024 * 1024):\n            output.write(chunk)`;
    const js = `// Node.js 22+ · no dependencies\n// Set GATEWAY_URL and GATEWAY_API_KEY in your environment.\nimport { randomUUID } from 'node:crypto';\nimport { writeFile } from 'node:fs/promises';\n\nconst base = process.env.GATEWAY_URL.replace(/\\/$/, '');\nconst headers = { Authorization: \`Bearer \${process.env.GATEWAY_API_KEY}\`,\n  'User-Agent': 'GatewayClient/1.0' };\nasync function request(path, options = {}) {\n  const res = await fetch(base + path, {\n    ...options, signal: AbortSignal.timeout(30000),\n    headers: { ...headers, ...options.headers }\n  });\n  if (!res.ok) throw new Error(\`HTTP \${res.status}: \${await res.text()}\`);\n  return res.json();\n}\nconsole.log(await request('/v1/models'));\nconst idempotencyKey = randomUUID();\nlet task = await request('/v1/videos', {\n  method: 'POST',\n  headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey },\n  body: JSON.stringify({ model: 'mock/mock-video-v1',\n    prompt: 'A sunrise over the ocean',\n    size: '1280x720', seconds: '5' })\n});\nconst deadline = Date.now() + 600000;\nwhile (!['completed', 'failed'].includes(task.status)) {\n  if (Date.now() > deadline) throw new Error('Resume polling task ' + task.id);\n  await new Promise(resolve => setTimeout(resolve, 2000));\n  task = await request('/v1/videos/' + task.id);\n}\nif (task.status === 'failed') throw new Error(task.error);\nconst result = await fetch(new URL(task.content_url, base), {\n  headers, signal: AbortSignal.timeout(60000)\n});\nif (!result.ok) throw new Error('Download failed: ' + result.status);\nawait writeFile('result.mp4', new Uint8Array(await result.arrayBuffer()));`;
    for (const [name, example] of [['cURL',curl],['Python',python],['JavaScript',js]]) {
      const button = node('button',name); button.type='button'; button.setAttribute('aria-pressed',String(name==='cURL'));
      button.addEventListener('click',()=> { tabs.querySelectorAll('button').forEach(b=>b.setAttribute('aria-pressed',String(b===button))); target.replaceChildren(); codeBlock(example,name,target); }); tabs.append(button);
    }
    content.append(tabs,target); codeBlock(curl,'cURL',target);
    content.append(node('h2','Other media types'));
    table(['Type','Endpoint','Mock model'],[['image','/v1/images/generations','mock/mock-image-v1'],['speech','/v1/audio/speech','mock/mock-speech-v1'],['music','/v1/audio/generations','mock/mock-music-v1']]);
    paragraph('For live generation, choose a model ID and supported fields using your account catalog. Mock returns fixed sample media; it does not synthesize the prompt.');
  }
  function openai() {
    heading('OpenAI-style API');
    paragraph('Use familiar endpoint and parameter names while keeping every generation asynchronous. Creation returns HTTP 202 with a job ID, status_url and content_url. Poll until completed or failed.');
    table(['Operation','Endpoint'],[['Images','POST /v1/images/generations'],['Image edits','POST /v1/images/edits'],['Video','POST /v1/videos'],['Speech','POST /v1/audio/speech'],['Music','POST /v1/audio/generations'],['Any media','POST /v1/generations'],['Estimate','POST /v1/generations/estimate'],['Models','GET /v1/models']]);
    table(['Canonical field','Meaning'],[['input','Speech text'],['size','Image dimensions or exact video resolution/aspect ratio'],['seconds','Video duration as a string'],['output_format','Image encoding'],['response_format','Speech encoding'],['image / input_reference','Source images as URLs, reference file IDs, or multipart uploads']]);
    paragraph('Use exact model IDs from /v1/models. IDs initially use provider/model and can be renamed by the administrator. Only models available to your organization appear. Separate provider and media_type request fields are not accepted. Capabilities include all supported controls and gateway prices for your account. Unknown model creation dates are null; release dates stay separate.');
    paragraph('Additional provider features remain in options: negative prompts, seeds, reference modes, multiple references, audio, draft mode, last frames, lyrics and advertised parameters. webhook_url, max_expected_cost_usd and timeout_seconds remain top-level extensions. timeout_seconds overrides the provider response deadline; the provider setting otherwise defaults to 30 seconds. Use 180 for three minutes or 600 for ten minutes. This controls submission HTTP responses, not asynchronous rendering time. Standard fields inside options and unsupported fields return errors.');
    note('This is not drop-in synchronous SDK compatibility. Images and speech return jobs, not immediate media. Streaming and n greater than 1 are rejected. Multipart edits/video uploads allow 32 MiB total and persist reusable library references, even if later admission fails. Prepare reference uploads through /v1/references/uploads. Use its returned mode: upload directly to S3, reuse an existing file, or use multipart /v1/references for local storage.');
    paragraph('Canonical states: queued, in_progress, completed, failed. Top-level timestamps use Unix seconds. Download content_url with the same API key and follow redirects (curl -L); S3 delivers the bytes directly. Never forward the API key to a redirected storage host; unavailable or deleted content returns 404. Every route retains ownership, scopes, storage checks, customer markup and budget admission.');
  }
  function authentication() {
    heading('Authentication', 'Organization-owned generation, with the same API for the console and integrations.','GET STARTED');
    codeBlock('Authorization: Bearer YOUR_GATEWAY_API_KEY','HTTP header');
    paragraph('Use a personal Gateway key on your server or in a trusted agent. Use Authorization: Bearer with the key. An explicitly supplied invalid or revoked key returns 401 even if a browser session is present.');
    table(['Credential','Account and capabilities'],[['browser session','Console account. Can bootstrap the first key and manage the account through the same API.'],['Personal API key','Personal workspace with no organization required; access follows the account and key scopes.'],['Organization API key','One organization and its member; access follows membership, role and key scopes.'],['Deployment service key','Separate system owner. Does not access users’ tasks or provider settings; cannot issue personal keys.']]);
    content.append(node('h2','Key permissions'));
    table(['Scope','Access'],[['generation','Required on every key. Create media, list/read authorized organization tasks, access results, read the provider catalog and own profile.'],['providers:manage','Read administrative provider details, configure connections and check availability. Provider account balances are not exposed.'],['storage:manage','Read, configure and test your account’s local or S3 storage.'],['keys:manage','Issue and revoke keys for eligible recipients according to the current organization role. Sessions can manage keys across authorized memberships.'],['organizations:manage','Manage organization membership, weekly limits and invitations according to your role.'],['system:manage','System administration. Organization funding also requires organizations:manage and an administrator role.']]);
    paragraph('Use separate named keys for applications so each can be revoked independently. Keys do not create separate provider stores or task histories. An api_key_id on a task identifies the personal key that created it.');
    note('Provider API keys and Gateway API keys are different secrets. Gateway keys authorize operations; they do not reveal saved provider credentials.');
    content.append(node('h2','Browser sessions'));
    paragraph('Client managers have read-only access to assigned organization statistics, including balances, member spending and reports. They cannot create keys, generate, manage resources or access other organizations. Administrators assign the role in Users and roles and the organization via PUT /v1/organizations/{org}/client-manager.');
    paragraph('Organization keys are issued by Developers, Managers or Supermanagers for eligible active participants. A key records its recipient and issuer separately; requests and weekly spending belong to the recipient. Members cannot issue keys. Managers see request metadata without content; Content Officers and Supermanagers see all retained content; Developers and Members see only their own content. Shared generation requires organization funds or credit plus an available weekly member allowance.');
    paragraph('Google or email sign-in establishes an HttpOnly cookie. The console uses this session for account administration. Generation and model catalog operations require a Gateway API key. Organization-scoped request logs, content and reference operations also support browser sessions, subject to current role and author checks. Cross-origin browser mutations are rejected. There is no public Google token-exchange endpoint or cross-origin browser SDK in this version.');
    paragraph('Keep long-lived API keys on your server. Playground remembers a pasted or newly created key in this tab’s sessionStorage for up to 24 hours, bound to the current browser login. Sign-out, session change, expiration, or Forget key clears it. A copied secret cannot be retrieved from Gateway after its creation response.');
  }
  function webhooks() {
    heading('Webhooks', 'Receive task lifecycle events at your own HTTP endpoint.','GUIDES');
    paragraph('Set webhook_url when creating a task. Gateway delivers generation.queued, generation.in_progress, generation.completed and generation.failed as POST requests. Delivery is asynchronous; events can arrive more than once or out of order.');
    note('The current signing secret is configured per deployment (WEBHOOK_SIGNING_SECRET). Coordinate verification with your deployment operator. There is no per-account webhook secret management endpoint in this version; use polling when a suitable verification setup is unavailable.');
    const op=spec.webhooks.taskEvent.post;parameters(op.parameters);
    codeBlock('expected = "v1=" + hex(HMAC_SHA256(\n  WEBHOOK_SIGNING_SECRET, timestamp + "." + raw_request_body\n))','Signature');
    paragraph('Verify the signature against the exact raw body using a constant-time comparison. Check the timestamp against a short tolerance (for example five minutes), deduplicate by delivery ID and acknowledge with any 2xx only after durable acceptance. The API key is not the webhook signing secret.');
    paragraph('Network failures and non-2xx responses retry up to WEBHOOK_MAX_ATTEMPTS. Delay starts at WEBHOOK_RETRY_BASE, doubles per attempt and is capped at five minutes. Reconcile current state with GET /v1/generations/{id}.');
    content.append(node('h2','Event body'));fields(spec.components.schemas.WebhookEvent);
  }
  function errors() {
    heading('Errors & retries','Handle request failures separately from generation failures.','GUIDES');
    codeBlock(JSON.stringify({error:{type:'invalid_request_error',param:'limit',code:'invalid_request',message:'limit must be an integer from 1 to 100'}},null,2),'JSON error');
    table(['HTTP','Code / meaning','Next step'],[['400','invalid_request / provider_configuration_invalid','Correct parameters or provider settings.'],['401','unauthorized','Check the credential, expiration and revocation.'],['403','insufficient_scope / personal_account_required','Use a key with the required scope or sign in.'],['403','invalid_media_url / cross_origin_request','Refresh the signed URL, or send browser mutations from the same origin.'],['404','not_found','Check ownership and ID; media may not be archived yet.'],['402','budget_exceeded','Check organization funds, credit and the recipient weekly allowance.'],['409','idempotency_conflict / estimate_changed / direct_upload_required / active_key_limit','Keep the same request for idempotent retries; otherwise refresh the quote, upload directly, or free a key slot.'],['410','upload_expired','Prepare a new reference upload.'],['422','pricing_unavailable','Use a model with a valid quote or ask the operator to configure pricing.'],['429','rate_limit_exceeded / upload_busy','Honor Retry-After in seconds and use bounded backoff with jitter.'],['503','service_unavailable','Retry with bounded backoff; retain the generation idempotency key.'],['500','internal_error','Keep the task ID if known and investigate the deployment.']]);
    paragraph('JSON API handlers use error.code and error.message. Health checks use {"status":"unhealthy"}; browser redirects, unknown routes, unsupported methods and HTTP file-serving errors may have different or plain-text bodies. Check the status and Content-Type before parsing.');
    paragraph('A CDN or reverse proxy can reject a request before it reaches Gateway. For example, Cloudflare error 1010 is an edge rejection, not a Gateway credential error. Use a descriptive User-Agent for server clients, as in the quickstart examples; if rejection continues, ask your deployment operator to review the API route policy.');
    content.append(node('h2','Retry generation safely'));
    paragraph('Create an Idempotency-Key (maximum 200 bytes) for each intended generation. After a timeout or uncertain 503, repeat the same normalized request with the same key. Gateway returns the original task with 202. Keys are scoped to the account, across all its API keys. A new idempotency key starts new work.');
    paragraph('Once you have a task ID, poll it instead of submitting again. A task with status failed is a terminal asynchronous failure even though GET returns 200. A new generation may incur provider charges.');
    content.append(node('h2','Current limits'));
    table(['Area','Contract'],[['Generation body','64 KiB; prompt up to 10,000 Unicode characters.'],['Provider update body','16 KiB.'],['API key creation body','4 KiB; name up to 80 characters.'],['Generation history','1–100 jobs per page; default 50. Use offset (0–1,000,000), has_more and next_offset. Cursor parameters are rejected.'],['Provider options','Model-dependent; discover from /v1/providers.'],['Rate limits','Configurable rolling per-key and per-user quotas. HTTP 429 includes Retry-After in seconds. System administrators manage /v1/system/limits and organization overrides.'],['References','Local multipart uploads or direct S3 uploads; maximum image/video/audio sizes are 30/200/50 MiB. Generation multipart bodies allow 32 MiB total.'],['Result deletion','DELETE /v1/generations/{id}/content removes terminal-job files while retaining history and accounting.'],['Task operations','No cancellation, bulk submission or deletion of the generation history record.']]);
  }
  function operation(entry) {
    const {path,method,item,op}=entry;
    heading(op.summary,op.description,op.tags?.[0]?.toUpperCase());
    const badge=node('div',undefined,'docs-endpoint');badge.append(node('span',method.toUpperCase(),`docs-method ${method}`),node('code',path));content.append(badge);
    const secured=op.security?.length===0?false:true;
    const sessionOnly=op.security?.length>0&&op.security.every(rule=>Object.keys(rule).length>0&&Object.keys(rule).every(key=>key==='sessionAuth'));
    if(op['x-required-scope']) paragraph(`Personal key scope: ${op['x-required-scope']}. ${op['x-required-scope']==='generation' ? 'A browser session alone cannot call this endpoint.' : 'Browser sessions can also manage the account here.'}`);
    parameters([...(item.parameters||[]),...(op.parameters||[])]);
    const body=op.requestBody?.content?.['application/json'];
    if(body) {content.append(node('h2','Request body'));fields(body.schema);}
    let examplePath=path.replace('{id}','TASK_ID').replace('{providerId}','bytedance');
    if(path==='/v1/api-keys/{id}') examplePath=path.replace('{id}','KEY_ID');
    let example=`curl ${method==='head'?'-I':`-X ${method.toUpperCase()}`} "${gatewayURL}${examplePath}"`;
    if(secured&&!sessionOnly) example+=' \\\n  -H "Authorization: Bearer $GATEWAY_API_KEY"';
    if(sessionOnly) example+=' \\\n  -b "$GATEWAY_COOKIE_JAR"';
    if(body) example+=' \\\n  -H "Content-Type: application/json"'+(path==='/v1/generations'||path==='/v1/videos'||path==='/v1/images/generations'||path==='/v1/images/edits'||path.startsWith('/v1/audio/')?' \\\n  -H "Idempotency-Key: $IDEMPOTENCY_KEY"':'')+` \\\n  -d '${JSON.stringify(body.example||{},null,2)}'`;
    content.append(node('h2','Example request'));
    if(path.startsWith('/auth/')) paragraph('This endpoint uses the session flow. It can be called programmatically with a cookie jar and the documented redirect or email-token exchange. API keys do not establish browser sessions.');
    else if(path==='/media/{id}') codeBlock('curl -fSL "$GATEWAY_URL$SIGNED_DOWNLOAD_URL" -o result.mp4','cURL · use download_url from media-access');
    else codeBlock(example);
    content.append(node('h2','Responses'));
    for(const [status,raw] of Object.entries(op.responses)) {
      const response=resolve(raw);const details=node('details',undefined,'docs-response');
      details.open=Number(status)<300;
      details.append(node('summary',`${status}  ${response.description}`));
      const json=response.content?.['application/json'];
      if(response.headers) {paragraph('Response headers:',details);for(const [name,h] of Object.entries(response.headers)) paragraph(`${name}: ${h.description||''}`,details);}
      if(json) { if(json.example) codeBlock(JSON.stringify(json.example,null,2),'Example response',details); fields(json.schema,details); }
      else if(response.content) paragraph('Binary media. Content-Type reflects the generated file.',details);
      content.append(details);
    }
  }
  function costs() {
    heading('Costs & budgets','Estimate before admission. Compare the accepted estimate with gross generation cost.','GUIDE');
    paragraph('Call POST /v1/generations/estimate with your generation request, then send cost.customer_expected_usd as max_expected_cost_usd when creating the task. The console uses the same endpoints. Estimation creates no task and reserves no money.');
    table(['Field','Meaning'],[['customer_expected_usd / expected_usd','Gateway estimate for the selected configuration. Use for max_expected_cost_usd.'],['customer_metered_usd / metered_usd','Gateway cost based on actual usage.'],['customer_tariff_usd / tariff_usd','Confirmed gateway cost.']]);
    paragraph('Organizations have a persistent USD balance and a Credit Limit set by an administrator. Members have weekly caps across all their keys. '+weeklyResetText()+' Admission checks only known completed spending. Estimates and pending tasks do not occupy budget. Completed gateway cost consumes allowance. A final cost above the estimate can leave a negative balance.');
    note('A $0.50 estimate can be accepted with $0.51 remaining. If the final generation cost is $0.55, the remaining budget becomes −$0.04 and subsequent paid requests are blocked.');
    paragraph('Gateway prices are reviewed and versioned with releases. Quotes are calculated on the server when parameters change and again on confirmation. The accepted tariff is retained for audit. Unknown model, region, or reference-video pricing blocks admission instead of guessing a zero charge.');
    paragraph('Configure billing access keys in Providers. The worker checks current and previous month bills every five minutes. Actual cost is populated only for a settled USD bill matched to the exact provider task. Aggregate model/day bills remain pending; they are never presented as exact per-task charges. Billing integration still needs validation against your provider account.');
    content.append(link('Manage API keys →','api-keys'),link('Estimate endpoint →','post_v1_generations_estimate'));
  }
  async function render() {
    if(!spec) return;
    content.replaceChildren();markActive();
    const page=location.hash.startsWith('#docs/')?location.hash.slice(6):'overview';
    const pages={overview,quickstart,openai,authentication,costs,webhooks,errors};
    if(page==='api-keys') { heading('API keys', 'Manage credentials in the API keys console section.'); content.append(link('Open API keys →', 'api-keys')); }
    else if(pages[page]) pages[page]();
    else if(page.startsWith('schema-') && spec.components.schemas[page.slice(7)]) {heading(page.slice(7),null,'SCHEMAS');fields(spec.components.schemas[page.slice(7)]);}
    else {const entry=endpoints().find(e=>e.op.operationId===page);if(entry) operation(entry);else {heading('Page not found','Choose a page in the API reference.');content.append(link('Back to overview','overview'));}}
  }
  window.GatewayDocs={async open(){
    const site=await window.GatewaySite?.ready;gatewayURL=site?.api_url||document.querySelector('meta[name=gateway-url]')?.content||location.origin;
    if(!spec) {
      try {loading ||= fetch('/openapi.json').then(async r=>{if(!r.ok) throw new Error(`HTTP ${r.status}`);return r.json();});spec=await loading;renderMenu();}
      catch(e){loading=null;content.replaceChildren(node('p',`Could not load API reference: ${e.message}`,'operation-error'));const retry=node('button','Retry','secondary');retry.addEventListener('click',()=>window.GatewayDocs.open());content.append(retry);return;}
    }
    await render();
  }};
  search.addEventListener('input',renderMenu);
  window.addEventListener('hashchange',()=>{if(location.hash.startsWith('#docs/')) {if(standalone)window.GatewayDocs.open();else showConsoleSection('docs');content.scrollIntoView({block:'start'});}});
  if(!document.querySelector('#docsView').hidden) window.GatewayDocs.open();
})();
