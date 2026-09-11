(() => {
  const dialog = document.querySelector('[data-catalog-conflict-dialog]');
  if (!dialog) return;
  const title = dialog.querySelector('[data-catalog-conflict-title]');
  const summary = dialog.querySelector('[data-catalog-conflict-summary]');
  const status = dialog.querySelector('[data-catalog-conflict-status]');
  const content = dialog.querySelector('[data-catalog-conflict-content]');
  let controller;
  const node = (tag, text, className) => {
    const result = document.createElement(tag);
    if (text != null) result.textContent = text;
    if (className) result.className = className;
    return result;
  };
  function render(conflict) {
    summary.textContent = `${conflict.fields.length} differing fields · ${conflict.sources.length} account definitions`;
    content.append(node('p', 'Only discrepancies are shown. Retained marks the complete model definition selected from the primary account when available, otherwise the first eligible account. Values are JSON; “Missing” means the field is absent. Array positions start at 0.', 'muted'));
    const wrap = node('div', null, 'catalog-conflict-scroll');
    wrap.tabIndex = 0;
    wrap.setAttribute('role', 'region');
    wrap.setAttribute('aria-label', 'Account catalog comparison; scroll horizontally to see every account');
    const table = node('table', null, 'catalog-conflict-table');
    const caption = node('caption', `Differing fields for ${conflict.model}`, 'sr-only');
    const head = node('thead');
    const headings = node('tr');
    const fieldHeading = node('th', 'Field'); fieldHeading.scope = 'col'; headings.append(fieldHeading);
    for (const source of conflict.sources) {
      const cell = node('th', null, source.retained ? 'catalog-value-retained' : '');
      cell.scope = 'col';
      cell.append(node('strong', source.plan ? source.plan.toUpperCase() : 'Unknown plan'));
      cell.append(node('span', source.account || 'Account', 'catalog-source-label'));
      cell.append(node('span', source.account_id, 'catalog-source-id'));
      cell.append(node('small', `Catalog entry ${source.catalog_entry}${source.primary ? ' · Primary account' : ''}`));
      if (source.retained) cell.append(node('span', 'Retained', 'catalog-retained-badge'));
      headings.append(cell);
    }
    head.append(headings);
    const body = node('tbody');
    for (const field of conflict.fields) {
      const row = node('tr');
      const heading = node('th'); heading.scope = 'row';
      heading.append(node('code', field.path || '/'));
      if (field.container) heading.append(node('small', 'Container presence'));
      row.append(heading);
      field.values.forEach((value, index) => {
        const retained = conflict.sources[index].retained;
        const cell = node('td', null, retained ? 'catalog-value-retained' : value.matches_retained ? '' : 'catalog-value-different');
        if (!value.present) cell.append(node('span', 'Missing', 'catalog-value-missing'));
        else cell.append(node('pre', field.container ? JSON.parse(value.json) : value.json));
        if (!retained) cell.append(node('small', value.matches_retained ? 'Matches retained' : 'Differs from retained'));
        row.append(cell);
      });
      body.append(row);
    }
    table.append(caption, head, body); wrap.append(table); content.append(wrap);
  }
  async function open(model) {
    controller?.abort();
    const current = new AbortController(); controller = current;
    title.textContent = `${model} · discrepancies`;
    summary.textContent = '';
    status.textContent = 'Comparing stored account catalogs…';
    content.replaceChildren();
    dialog.showModal();
    try {
      const response = await fetch(`/admin/catalog/conflicts?${new URLSearchParams({model})}`, { signal: current.signal, cache: 'no-store' });
      if (response.redirected) { window.location.assign('/admin/login'); return; }
      const result = await response.json();
      if (!response.ok) throw new Error(result.error?.message || 'Catalog discrepancies could not be loaded. Close and retry.');
      if (controller !== current || !dialog.open) return;
      render(result); status.textContent = '';
    } catch (error) {
      if (error.name !== 'AbortError') status.textContent = error.message;
    }
  }
  document.querySelectorAll('[data-catalog-conflict]').forEach((button) => {
    button.addEventListener('click', () => open(button.dataset.catalogConflict));
  });
  dialog.querySelector('[data-catalog-conflict-close]').addEventListener('click', () => dialog.close());
  dialog.addEventListener('close', () => controller?.abort());
})();
