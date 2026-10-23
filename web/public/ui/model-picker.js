(() => {
  'use strict';
  const filters = new WeakMap();
  const el = (tag, cls, text) => { const n = document.createElement(tag); if (cls) n.className = cls; if (text != null) n.textContent = text; return n; };
  const key = (provider, model = '') => JSON.stringify([provider, model]);
  // Labels are presentation only: never change native/public IDs or selection values.
  function uniqueCatalog(providers) {
    const entries = providers.flatMap(p => (p.models || []).map(m => ({provider:p, model:m, key:key(p.id,m.id), base:(m.display_name || m.name || m.public_id || m.id).trim() || m.id})));
    const names = new Map(entries.map(e => [e.key,e.base]));
    const normalized = name => name.normalize('NFKC').trim().replace(/\s+/g,' ').toLowerCase();
    function collisions() {
      const groups = new Map();
      for (const e of entries) { const name=normalized(names.get(e.key));if(!groups.has(name))groups.set(name,new Set());groups.get(name).add(e.key); }
      return new Set([...groups.values()].filter(keys=>keys.size>1).flatMap(keys=>[...keys]));
    }
    for (let level=1;level<=3;level++) {
      const duplicate=collisions();if(!duplicate.size)break;
      for (const e of entries) if(duplicate.has(e.key)) {
        const context=level===1?'':level===2?(e.provider.name || e.provider.id):e.provider.id;
        names.set(e.key,e.base+' · '+(context?context+' · ':'')+e.model.id);
      }
    }
    // Reserve other labels, including literal names resembling generated labels.
    // Stable identity order also resolves case-only IDs and unusual imported names.
    const duplicate=collisions(),used=new Set(entries.filter(e=>!duplicate.has(e.key)).map(e=>normalized(names.get(e.key))));
    for (const identity of [...duplicate].sort()) {
      const base=names.get(identity);let name=base,suffix=2;
      while(used.has(normalized(name)))name=base+' · '+suffix++;
      names.set(identity,name);used.add(normalized(name));
    }
    return providers.map(p=>({...p,models:(p.models || []).map(m=>({...m,display_name:names.get(key(p.id,m.id))}))}));
  }
  function populate(control, providers, {value = control.value, placeholder, allModels = false, label = m => m.display_name || m.name || m.public_id || m.id, optionValue = (p, m) => key(p.id, m.id)} = {}) {
    control.dataset.modelPicker = '';
    const children = [];
    if (placeholder) children.push(new Option(placeholder, ''));
    for (const provider of uniqueCatalog(providers).sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id))) {
      if (!allModels && !provider.models?.length) continue;
      const group = el('optgroup'); group.label = provider.name || provider.id;
      group.providerInfo = provider;
      if (allModels) { const option = new Option('All models · ' + group.label, key(provider.id)); option.dataset.provider = provider.id; group.append(option); }
      const models = (provider.models || []).map(m => ({...m, display_name: m.display_name || m.name || m.public_id || m.id})).sort(window.ModelInfo.compare);
      for (const model of models) {
        const option = new Option(label(model), optionValue(provider, model));
        option.dataset.provider = provider.id; option.modelInfo = model; group.append(option);
      }
      children.push(group);
    }
    control.replaceChildren(...children);
    if ([...control.options].some(o => o.value === value)) control.value = value;
    window.ControlPickers?.sync(control);
  }
  function open(control, state, api) {
    const panel = el('div', 'select-popup model-select-popup'); panel.setAttribute('role', 'dialog'); panel.setAttribute('aria-label', api.labelFor(control) + ' choices');
    const toolbar = el('div', 'model-picker-filters'), search = el('input'), after = el('input'), type = el('select'), clear = el('button', 'secondary model-filter-clear', 'Clear');
    const saved = filters.get(control) || {search: '', after: '', type: ''};
    search.type = 'search'; search.value = saved.search;
    after.type = 'date'; after.value = saved.after;
    const types = [...new Set([...control.options].map(o => o.modelInfo?.media_type).filter(Boolean))];
    const typeNames = {video: 'Video', image: 'Image', speech: 'Speech', music: 'Music', audio: 'Audio'};
    type.append(new Option('All types', ''), ...types.sort((a, b) => (typeNames[a] || a).localeCompare(typeNames[b] || b)).map(value => new Option(typeNames[value] || value, value)));
    type.value = types.includes(saved.type) ? saved.type : '';
    for (const [name, input] of [['Search by Name', search], ['Released after', after], ['Content type', type]]) {
      const label = el('label', '', name); input.setAttribute('aria-label', name); label.append(input); toolbar.append(label);
    }
    clear.type = 'button'; clear.setAttribute('aria-label', 'Clear model filters');
    const summary = el('p', 'model-picker-summary'); summary.setAttribute('role', 'status');
    const list = el('div', 'model-picker-options'); list.setAttribute('role', 'listbox'); list.setAttribute('aria-label', api.labelFor(control)); list.tabIndex = 0;
    const filterStatus = el('div', 'model-picker-filter-status'); filterStatus.append(summary, clear);
    panel.append(toolbar, filterStatus, list);
    let items = [], current = -1;
    const options = [...control.options].filter(o => o.value && !o.hidden && !o.parentElement.hidden);
    function highlight(index, scroll = true) {
      current = index; items.forEach((entry, i) => entry.node.classList.toggle('highlighted', i === index));
      if (items[index]) { list.setAttribute('aria-activedescendant', items[index].node.id); if (scroll) items[index].node.scrollIntoView({block: 'nearest'}); }
      else list.removeAttribute('aria-activedescendant');
    }
    function pick(index) {
      const entry = items[index]; if (!entry || entry.disabled) return;
      control.value = entry.option.value; api.close(true); api.sync(control); api.changed(control);
    }
    function render() {
      window.ModelInfo?.close(); filters.set(control, {search: search.value, after: after.value, type: type.value});
      list.replaceChildren(); items = []; let group = null; const query = search.value.toLowerCase();
      for (const option of options) {
        const model = option.modelInfo;
        if (type.value && model?.media_type !== type.value) continue;
        const names = model ? [model.display_name, model.public_id, model.id] : [option.textContent];
        if (!names.some(name => (name || '').toLowerCase().includes(query))) continue;
        // Month-only metadata is included only when even its earliest possible day is later.
        const date = model?.metadata?.release_date || '';
        if (after.value && (!/^\d{4}-\d{2}(?:-\d{2})?$/.test(date) || (date.length === 7 ? date + '-01' : date) <= after.value)) continue;
        const parent = option.parentElement, name = parent.tagName === 'OPTGROUP' ? parent.label : '';
        if (name && parent !== group) {
          const provider = parent.providerInfo || {}, header = el('div', 'picker-group model-picker-provider'), mark = el('span', 'model-picker-provider-mark');
          header.setAttribute('role', 'presentation'); mark.setAttribute('aria-hidden', 'true'); mark.dataset.initial = [...name.trim()][0]?.toUpperCase() || '?';
          if (provider.icon_url) {
            const icon = el('img'); icon.src = provider.icon_url; icon.alt = ''; icon.width = icon.height = 32; icon.classList.toggle('monochrome', !!provider.icon_monochrome);
            icon.addEventListener('error', () => icon.remove(), {once:true}); mark.append(icon);
          }
          const title = el('strong', 'model-picker-provider-name', name); title.title = name;
          header.append(mark, title); list.append(header);
        }
        group = parent;
        const index = items.length, node = el('div', 'picker-option', option.textContent), disabled = option.disabled || option.parentElement.disabled;
        node.id = panel.id + '-option-' + index; node.setAttribute('role', 'option'); node.setAttribute('aria-selected', String(option.selected)); node.setAttribute('aria-disabled', String(!!disabled)); node.onclick = () => pick(index);
        if (model) { const row = el('div', 'model-picker-row'); row.setAttribute('role', 'presentation'); row.append(node, window.ModelInfo.button(model)); list.append(row); } else list.append(node);
        items.push({node, option, disabled});
      }
      if (!items.length) list.append(el('p', 'model-picker-empty', 'No models match these filters.'));
      summary.textContent = `${items.length} ${items.length === 1 ? 'option' : 'options'}` + (after.value ? ' · Models without a known release date are hidden.' : '');
      clear.disabled = !search.value && !after.value && !type.value;
      const selected = items.findIndex(entry => entry.option.selected && !entry.disabled);
      highlight(selected >= 0 ? selected : items.findIndex(entry => !entry.disabled), false); list.scrollTop = 0; api.position();
    }
    search.addEventListener('input', render); after.addEventListener('input', render); type.addEventListener('change', render);
    clear.onclick = () => { search.value = after.value = type.value = ''; window.FormUI.refresh(); render(); search.focus(); };
    panel.addEventListener('keydown', event => {
      if (event.target.closest('.model-info-button')) return;
      if (event.key === 'Tab') { setTimeout(() => { if (panel.isConnected && !window.ControlPickers.contains(document.activeElement) && !window.ModelInfo?.contains(document.activeElement)) api.close(); }, 0); return; }
      if (event.target !== search && event.target !== list) return;
      if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key) && (event.target !== search || event.key.startsWith('Arrow'))) {
        event.preventDefault(); const enabled = items.map((v, i) => v.disabled ? -1 : i).filter(i => i >= 0); let index = enabled.indexOf(current);
        index = event.key === 'Home' ? 0 : event.key === 'End' ? enabled.length - 1 : Math.max(0, Math.min(enabled.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)));
        list.focus(); highlight(enabled[index]);
      } else if (event.key === 'Enter' || event.key === ' ' && event.target === list) { event.preventDefault(); pick(current); }
    });
    api.show(state.trigger, panel); window.FormUI.refresh(); render(); search.focus();
  }
  window.ModelPicker = {key, uniqueCatalog, populate, open};
})();
