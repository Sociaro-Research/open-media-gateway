'use strict';
// Pure site.js contract tests: no browser, network, storage or live installation.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const styles = new Map();
const root = {dataset: {theme: 'dark'}, style: {setProperty: (k, v) => styles.set(k, v)}};
const document = {
  documentElement: root,
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: () => ({dataset:{},append() {},setAttribute() {},querySelectorAll:()=>[]}),
  body: {classList:{add() {}},append() {}},
  head: {append() {}},
};
const window = {};
const context = vm.createContext({
  window, document, AbortController, setTimeout, clearTimeout, URL,
  localStorage: {getItem: () => '', setItem() {}},
  location: {origin: 'https://fixture.invalid', pathname: '/', hash: ''},
  fetch: () => Promise.resolve({ok: true, json: async () => ({}), headers: {get: () => ''}}),
  MutationObserver: class {observe() {}},
});
vm.runInContext(fs.readFileSync(path.join(__dirname, '../../web/public/ui/site.js'), 'utf8'), context);
const site = window.GatewaySite;
const plain = value => JSON.parse(JSON.stringify(value));
const legacy = () => ({accent: '#b8dc00', highlight: '#00ff00', red: '#123456', 'on-accent': '#171717'});
for (const [mode, danger, foreground] of [['dark', '#f08088', '#171717'], ['light', '#b42336', '#ffffff']]) {
  const input = Object.freeze(legacy());
  const snapshot = {...input};
  const result = site.paletteColors(input, mode);
  assert.equal(result.danger, danger);
  assert.equal(result['on-danger'], foreground);
  for (const [key, value] of Object.entries(input)) assert.equal(result[key], value);
  assert.deepEqual(input, snapshot);
  assert.equal(result['accent-text'], input.accent);
  assert.equal(result['switch-thumb'], input['on-accent']);
  assert.deepEqual(plain(site.paletteColors(result, mode)), plain(result));
  result.red = '#ffffff';
  assert.equal(input.red, '#123456');
  for (const role of ['danger', 'on-danger']) {
    for (const value of ['', null, false, '#abc', '#00FF00', undefined]) {
      const explicit = site.paletteColors({...legacy(), [role]: value}, mode);
      assert.ok(Object.hasOwn(explicit, role));
      assert.equal(explicit[role], value, `${mode}: explicit ${role} was masked`);
    }
  }
}
const inherited = Object.assign(Object.create({danger: '#00ff00', 'on-danger': '#000000'}), legacy());
assert.equal(site.paletteColors(inherited, 'light').danger, '#b42336');
assert.equal(site.paletteColors(inherited, 'light')['on-danger'], '#ffffff');
assert.equal(site.paletteColors(legacy()).danger, '#f08088');
assert.equal(site.paletteColors({...legacy(), 'accent-text': ''})['accent-text'], '#b8dc00');
const settings = {
  dark: legacy(), light: legacy(),
  presets: [
    {id: 'custom', name: 'Custom', dark: {...legacy(), danger: '#ff00ff', 'on-danger': '#010101'}, light: {...legacy(), danger: '#00ffff', 'on-danger': '#020202'}},
    {id: 'old', name: 'Old', dark: legacy(), light: legacy()},
  ],
  default_preset: 'custom',
};
const snapshot = plain(settings);
const normalized = site.normalize(settings);
assert.deepEqual(plain(settings), snapshot);
assert.deepEqual(plain(site.normalize(normalized)), plain(normalized));
assert.equal(normalized.presets[1].dark.danger, '#f08088');
assert.equal(normalized.presets[1].light.danger, '#b42336');
assert.equal(normalized.presets[0].dark.danger, '#ff00ff');
const catalogless = site.normalize({dark: legacy(), light: legacy()});
assert.equal(catalogless.presets.length, 1);
assert.equal(catalogless.presets[0].id, 'original');
assert.equal(catalogless.presets[0].light.danger, '#b42336');
site.apply(settings);
assert.equal(styles.get('--danger'), '#ff00ff');
site.selectPreset('old');
assert.equal(styles.get('--danger'), '#f08088');
assert.equal(styles.get('--on-danger'), '#171717');
assert.equal(styles.get('--accent'), '#b8dc00');
assert.equal(styles.get('--red'), '#123456');
root.dataset.theme = 'light';
site.apply(settings);
assert.equal(styles.get('--danger'), '#b42336');
assert.equal(styles.get('--on-danger'), '#ffffff');
site.selectPreset('custom');
assert.equal(styles.get('--danger'), '#00ffff');
assert.equal(styles.get('--on-danger'), '#020202');
assert.deepEqual(plain(settings), snapshot);
console.log('danger palette contracts passed (Node VM only; not browser/visual validation)');
