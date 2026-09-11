(() => {
  const panel = document.querySelector('#logs');
  if (!panel) return;
  const $ = (selector) => panel.querySelector(selector);
  const rows = $('[data-log-rows]');
  const status = $('[data-log-status]');
  const form = $('[data-log-filters]');
  const dialog = $('[data-log-dialog]');
  let before = 0, nextBefore = 0, controller, timer, entries = [], filters = new URLSearchParams();
  const number = (value) => value == null ? 'Unreported' : Number(value).toLocaleString();
  const element = (tag, text, className) => {
    const node = document.createElement(tag);
    if (text != null) node.textContent = text;
    if (className) node.className = className;
    return node;
  };
  function date(value) {
    let zone;
    try { zone = localStorage.getItem('opencdx-timezone') || undefined; } catch {}
    return new Date(value).toLocaleString(undefined, { timeZone: zone, year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' });
  }
  function cell(row, primary, secondary) {
    const td = element('td', primary);
    if (secondary) td.append(element('small', secondary));
    row.append(td);
    return td;
  }
  function render() {
    rows.replaceChildren();
    for (const entry of entries) {
      const row = element('tr');
      const attempt = entry.attempts?.at(-1);
      cell(row, date(entry.started_at));
      cell(row, entry.model || '—', entry.reasoning_effort || 'Default effort');
      cell(row, entry.provider || '—', attempt?.account || attempt?.account_id || '');
      cell(row, entry.device_name || entry.device_id || '—');
      cell(row, number(entry.usage?.input_tokens), entry.usage ? `${number(entry.usage.cached_input_tokens)} cached` : '');
      cell(row, number(entry.usage?.output_tokens), entry.usage ? `${number(entry.usage.reasoning_output_tokens)} reasoning` : '');
      const rate = entry.usage && attempt?.first_byte_ms != null && attempt.duration_ms > attempt.first_byte_ms
        ? (entry.usage.output_tokens * 1000 / (attempt.duration_ms - attempt.first_byte_ms)).toFixed(1) : '—';
      cell(row, rate).title = 'Output tokens per second after the first response byte (approximate)';
      cell(row, entry.status || 'No response', entry.outcome).className = `log-${entry.outcome}`;
      cell(row, `${number(entry.duration_ms)} ms`, entry.attempts?.length > 1 ? `${entry.attempts.length} attempts` : '');
      const button = element('button', 'Details', 'btn');
      button.type = 'button';
      button.setAttribute('aria-label', `Details for ${entry.model || 'request'} at ${date(entry.started_at)}`);
      button.addEventListener('click', () => showDetails(entry));
      cell(row, '').append(button);
      rows.append(row);
    }
    if (!entries.length) {
      const row = element('tr');
      const td = cell(row, 'No requests found. New requests appear here after they finish.');
      td.colSpan = 10;
      rows.append(row);
    }
    $('[data-log-older]').disabled = !nextBefore;
    $('[data-log-newest]').disabled = !before;
  }
  function detailsGrid(values) {
    const dl = element('dl', null, 'log-detail-grid');
    for (const [key, value] of Object.entries(values)) {
      if (value == null || value === '') continue;
      dl.append(element('dt', key), element('dd', String(value)));
    }
    return dl;
  }
  function showDetails(entry) {
    const detail = $('[data-log-detail]');
    detail.replaceChildren(detailsGrid({
      'Request ID': entry.id, Started: date(entry.started_at), Outcome: entry.outcome,
      'HTTP status': entry.status || 'No response', Endpoint: `${entry.method} ${entry.path}`,
      Model: entry.model, 'Upstream model': entry.upstream_model, 'Response model': entry.response_model,
      'Reasoning effort': entry.reasoning_effort || 'Provider default', 'Service tier': entry.service_tier || 'Provider default',
      Streaming: entry.stream ? 'Yes' : 'No', Provider: entry.provider,
      Machine: entry.device_name, 'Machine ID': entry.device_id, 'Client version': entry.client_version,
      'Thread ID': entry.thread_id, 'Session ID': entry.session_id, 'Response ID': entry.response_id,
      Duration: `${number(entry.duration_ms)} ms`, 'Request size': `${number(entry.request_bytes)} bytes`,
      'Response size': `${number(entry.response_bytes)} bytes`,
      'Input tokens': number(entry.usage?.input_tokens), 'Cached input': number(entry.usage?.cached_input_tokens),
      'Output tokens': number(entry.usage?.output_tokens), 'Reasoning tokens': number(entry.usage?.reasoning_output_tokens),
      'Error type': entry.error_type, 'Error code': entry.error_code, 'Error message': entry.error_message,
    }));
    (entry.attempts || []).forEach((attempt, index) => {
      detail.append(element('h3', `Attempt ${index + 1}`), detailsGrid({
        Account: attempt.account, 'Account ID': attempt.account_id, 'Upstream request ID': attempt.request_id,
        'HTTP status': attempt.status || 'No response', Duration: `${number(attempt.duration_ms)} ms`,
        'Response headers': `${number(attempt.headers_ms)} ms`, 'First response byte': attempt.first_byte_ms == null ? 'Unreported' : `${number(attempt.first_byte_ms)} ms`,
        'Error type': attempt.error_type, 'Error code': attempt.error_code, 'Error message': attempt.error_message,
      }));
    });
    detail.append(element('p', 'Token usage is shown only when reported in captured response metadata. Error messages are shortened and common credential patterns are redacted.', 'muted'));
    const raw = element('details');
    raw.append(element('summary', 'View metadata JSON'), element('pre', JSON.stringify(entry, null, 2)));
    detail.append(raw);
    dialog.showModal();
  }
  $('[data-log-close]').addEventListener('click', () => dialog.close());
  const active = () => !panel.hidden && document.visibilityState !== 'hidden';
  async function refresh() {
    clearTimeout(timer);
    controller?.abort();
    const current = new AbortController();
    controller = current;
    const query = new URLSearchParams(filters);
    if (before) query.set('before', before);
    status.textContent = 'Loading logs…';
    try {
      const response = await fetch(`/admin/logs?${query}`, { signal: current.signal, cache: 'no-store' });
      if (response.redirected) { window.location.assign('/admin/login'); return; }
      if (!response.ok) throw new Error('Request logs could not be loaded. Try Refresh.');
      const page = await response.json();
      if (controller !== current) return;
      entries = page.logs; nextBefore = page.next_before || 0;
      render();
      status.textContent = `${entries.length} requests shown · ${before ? 'Browsing older requests' : 'Refreshes every 5 seconds'} · ${date(new Date())}`;
    } catch (error) {
      if (error.name !== 'AbortError') status.textContent = error.message;
    } finally {
      if (controller === current && active() && !before) timer = setTimeout(refresh, 5000);
    }
  }
  function sync() {
    clearTimeout(timer);
    if (active()) refresh(); else controller?.abort();
  }
  new MutationObserver(sync).observe(panel, { attributes: true, attributeFilter: ['hidden'] });
  document.addEventListener('visibilitychange', sync);
  form.addEventListener('submit', (event) => {
    event.preventDefault(); filters = new URLSearchParams(new FormData(form)); before = 0; refresh();
  });
  $('[data-log-refresh]').addEventListener('click', refresh);
  $('[data-log-newest]').addEventListener('click', () => { before = 0; refresh(); });
  $('[data-log-older]').addEventListener('click', () => { before = nextBefore; refresh(); });
  const fileInput = $('[data-log-file]');
  const importButton = $('[data-log-import]');
  importButton.addEventListener('click', () => fileInput.click());
  fileInput.addEventListener('change', async () => {
    const file = fileInput.files[0];
    if (!file) return;
    importButton.disabled = true;
    const progress = $('[data-log-import-status]');
    let imported = 0, duplicates = 0, batch = [], pending = '';
    const reader = file.stream().getReader();
    const decoder = new TextDecoder();
    async function upload() {
      if (!batch.length) return;
      const response = await fetch('/admin/logs/import', {
        method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': panel.dataset.csrf }, body: JSON.stringify(batch),
      });
      if (response.redirected || !response.ok) throw new Error('Import failed. Check the backup and your sign-in, then retry.');
      const result = await response.json(); imported += result.imported; duplicates += result.duplicates;
      batch = []; progress.textContent = `Imported ${number(imported)} logs; skipped ${number(duplicates)} duplicates…`;
    }
    try {
      progress.textContent = 'Reading backup…';
      while (true) {
        const { value, done } = await reader.read();
        pending += decoder.decode(value, { stream: !done });
        let newline;
        while ((newline = pending.indexOf('\n')) >= 0) {
          const line = pending.slice(0, newline).trim(); pending = pending.slice(newline + 1);
          if (line) {
            if (line.length > 65536) throw new Error('Backup record is too large.');
            batch.push(JSON.parse(line)); if (batch.length === 200) await upload();
          }
        }
        if (pending.length > 65536) throw new Error('Backup record is too large.');
        if (done) break;
      }
      if (pending.trim()) batch.push(JSON.parse(pending));
      await upload();
      progress.textContent = `Import complete: ${number(imported)} added, ${number(duplicates)} duplicates skipped. Telemetry totals are unchanged.`;
      before = 0; refresh();
    } catch (error) {
      await reader.cancel();
      progress.textContent = `${error.message} ${number(imported)} logs were already imported; retrying safely skips duplicates.`;
    } finally { reader.releaseLock(); importButton.disabled = false; fileInput.value = ''; }
  });
  sync();
})();
