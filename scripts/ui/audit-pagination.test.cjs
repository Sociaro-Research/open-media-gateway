const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// Exercise the actual AuditLog controller with explicitly ordered responses.
// The minimal DOM below does not claim browser or visual coverage.
const source = fs.readFileSync(process.env.AUDIT_LOG_SOURCE ||
  path.resolve(__dirname, '../../web/public/ui/audit-log.js'), 'utf8');
class FixtureNode {
  constructor(tag, text = '') {
    Object.assign(this, {tag, textContent: text, children: [], value: '', isConnected: true, disabled: false});
  }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute() {}
}
const find = (element, predicate) => predicate(element) ? element :
  element.children.map(child => find(child, predicate)).find(Boolean);
const turn = () => new Promise(resolve => setImmediate(resolve));

async function harness() {
  const host = new FixtureNode('host'), window = {}, calls = [];
  const document = {createElement: tag => new FixtureNode(tag), createTextNode: text => new FixtureNode('#text', text)};
  vm.runInNewContext(source, {
    window, document, Node: FixtureNode, Intl, Date, URLSearchParams,
    FormUI: {message: (node, text) => { node.textContent = text; }, refresh() {}},
    GatewayLayout: {bind: () => ({sync() {}, destroy() {}})},
    UserUI: {identity: () => new FixtureNode('identity')},
  });
  const api = endpoint => new Promise((resolve, reject) => calls.push({url: new URL(endpoint, 'https://fixture.test'), resolve, reject}));
  await window.AuditLog.mount(host, api, {organization: {id: 'fixture'}});
  const button = name => find(host, node => node.tag === 'button' && node.textContent === name);
  return {
    host, button, last: () => calls.at(-1),
    actor: () => find(host, node => node.id === 'audit-actor_id'),
    submit(actor) {
      this.actor().value = actor;
      find(host, node => node.tag === 'form').onsubmit({preventDefault() {}});
      return this.last();
    },
    async success(call, marker, hasMore = true) {
      const row = call.url.pathname.endsWith('/events') ?
        {id: 1, actor_type: 'system', action: 'settings', object_type: 'organization', object_name: marker, outcome: 'success'} :
        {id: 'key', snapshot: {name: marker}};
      call.resolve({data: [row], has_more: hasMore, next_before_id: 742, next_offset: 50});
      await turn();
    },
    assertPage(number, marker) {
      assert.ok(find(host, node => node.textContent === 'Page ' + number));
      assert.ok(find(host, node => node.textContent === marker));
    },
  };
}

function assertQuery(call, kind, actor, cursor) {
  assert.ok(call.url.pathname.endsWith(kind === 'Events' ? '/events' : '/revoked-keys'));
  assert.equal(call.url.searchParams.get('actor_id'), actor);
  assert.equal(call.url.searchParams.get(kind === 'Events' ? 'before_id' : 'offset'), String(cursor));
}

for (const kind of ['Events', 'Revoked keys']) {
  test(kind + ': paging keeps committed filters and Apply resets to the first page', async () => {
    const ui = await harness();
    await ui.success(ui.last(), 'initial');
    if (kind === 'Revoked keys') {
      ui.button(kind).onclick();
      await ui.success(ui.last(), 'keys initial');
    }
    await ui.success(ui.submit('applied'), 'first result');
    ui.actor().value = 'draft';
    ui.button('Next').onclick();
    const page2 = ui.last();
    assertQuery(page2, kind, 'applied', kind === 'Events' ? 742 : 50);
    assert.equal(ui.button('Next').disabled, true);
    await ui.success(page2, 'second result', false);
    ui.assertPage(2, 'second result');
    assert.equal(ui.button('Next').disabled, true);
    ui.button('Previous').onclick();
    assertQuery(ui.last(), kind, 'applied', 0);
    await ui.success(ui.last(), 'first result');
    ui.assertPage(1, 'first result');
    assert.equal(ui.button('Previous').disabled, true);
    const reset = ui.submit('draft');
    assertQuery(reset, kind, 'draft', 0);
    await ui.success(reset, 'new first result');
    ui.assertPage(1, 'new first result');
  });
}

test('failed Next and Apply retain the last successful rows and navigation', async () => {
  const ui = await harness();
  await ui.success(ui.last(), 'initial');
  await ui.success(ui.submit('applied'), 'applied first');
  ui.button('Next').onclick();
  ui.last().reject(new Error('page failed'));
  await turn();
  ui.assertPage(1, 'applied first');
  assert.equal(ui.button('Next').disabled, false);
  ui.button('Next').onclick();
  assertQuery(ui.last(), 'Events', 'applied', 742);
  await ui.success(ui.last(), 'applied second');
  const failure = ui.submit('bad draft');
  assertQuery(failure, 'Events', 'bad draft', 0);
  assert.equal(ui.button('Previous').disabled, true);
  failure.reject(new Error('filter failed'));
  await turn();
  ui.assertPage(2, 'applied second');
  assert.equal(ui.button('Previous').disabled, false);
  ui.button('Previous').onclick();
  assertQuery(ui.last(), 'Events', 'applied', 0);
  await ui.success(ui.last(), 'applied first');
  ui.assertPage(1, 'applied first');
});

test('a superseded Apply or tab response cannot replace the active page', async () => {
  const ui = await harness();
  await ui.success(ui.last(), 'initial');
  const slow = ui.submit('slow');
  const fast = ui.submit('fast');
  await ui.success(fast, 'fast result');
  await ui.success(slow, 'stale slow');
  ui.assertPage(1, 'fast result');
  assert.equal(find(ui.host, node => node.textContent === 'stale slow'), undefined);

  ui.button('Next').onclick();
  const oldTabRequest = ui.last();
  assertQuery(oldTabRequest, 'Events', 'fast', 742);
  ui.button('Revoked keys').onclick();
  assertQuery(ui.last(), 'Revoked keys', null, 0);
  await ui.success(ui.last(), 'active keys');
  await ui.success(oldTabRequest, 'stale event');
  ui.assertPage(1, 'active keys');
  assert.equal(find(ui.host, node => node.textContent === 'stale event'), undefined);
});
