(() => {
  const panel = document.querySelector('#instructions');
  if (!panel) return;
  const $ = selector => panel.querySelector(selector);
  const rows = $('[data-instruction-rows]'), status = $('[data-instruction-status]');
  const notice = $('[data-instruction-notice]'), badge = document.querySelector('[data-instruction-badge]');
  const form = $('[data-instruction-filters]'), dialog = $('[data-instruction-dialog]');
  const seenKey = 'opencdx.instructions.seen';
  let seen = 0, latest = 0, before = 0, nextBefore = 0, listController, detailController, timer, polling = false;
  let filters = new URLSearchParams();
  try { seen = Math.max(0, Number(localStorage.getItem(seenKey)) || 0); } catch {}
  const node = (tag, text, className) => {
    const result = document.createElement(tag);
    if (text != null) result.textContent = text;
    if (className) result.className = className;
    return result;
  };
  function date(value) {
    if (!value) return 'Unknown';
    let timeZone; try { timeZone = localStorage.getItem('opencdx-timezone') || undefined; } catch {}
    return new Date(value).toLocaleString(undefined, {timeZone, dateStyle:'medium', timeStyle:'medium'});
  }
  async function get(url, signal) {
    const response = await fetch(url, {signal, cache:'no-store'});
    if (response.redirected) { location.assign('/admin/login'); throw new Error('Sign in to view instruction history.'); }
    if (!response.ok) throw new Error('Instruction history could not be loaded. Refresh to retry.');
    return response.json();
  }
  function updateNotice(count) {
    badge.hidden = count === 0; badge.textContent = count > 99 ? '99+' : String(count);
    badge.setAttribute('aria-label', `${count} unseen instruction updates`);
    notice.textContent = count ? `${count} unseen instruction updates. “Mark all seen” clears the badge in this browser.` : 'No unseen instruction updates.';
  }
  async function refreshStatus() {
    const requestedSeen = seen;
    let result = await get(`/admin/instructions/status?after=${seen}`);
    if (seen !== requestedSeen) result = await get(`/admin/instructions/status?after=${seen}`);
    latest = result.latest_change_id;
    if (seen > latest) { seen = 0; try { localStorage.setItem(seenKey, '0'); } catch {} result = await get('/admin/instructions/status?after=0'); }
    updateNotice(result.unseen);
  }
  function cell(row, text, secondary) {
    const td = node('td', text); if (secondary) td.append(node('small', secondary)); row.append(td); return td;
  }
  async function refreshList() {
    listController?.abort(); const current = new AbortController(); listController = current;
    const query = new URLSearchParams(filters); if (before) query.set('before', before);
    status.textContent = 'Loading instruction history…';
    try {
      const page = await get(`/admin/instructions?${query}`, current.signal);
      if (listController !== current) return;
      rows.replaceChildren(); nextBefore = page.next_before || 0;
      for (const entry of page.revisions) {
        const row = node('tr');
        cell(row, date(entry.observed_at)); cell(row, entry.model);
        cell(row, entry.account || entry.account_id, entry.plan);
        cell(row, entry.client_version || 'Unknown', entry.previous_client_version && entry.previous_client_version !== entry.client_version ? `Previously ${entry.previous_client_version}` : '');
        cell(row, entry.kind === 'baseline' ? 'Baseline' : 'Changed', entry.kind === 'changed' && entry.id > seen ? 'Unseen' : '');
        cell(row, String(entry.changes.length));
        const button = node('button', entry.kind === 'baseline' ? 'View baseline' : 'View diff', 'btn btn-secondary'); button.type = 'button';
        button.setAttribute('aria-label', `View ${entry.kind === 'baseline' ? 'baseline' : 'diff'} for ${entry.model} at ${date(entry.observed_at)}`);
        button.addEventListener('click', () => showRevision(entry)); cell(row, '').append(button); rows.append(row);
      }
      if (!page.revisions.length) {
        const row = node('tr'), td = cell(row, 'No instruction history found. The first OpenAI catalog capture establishes a baseline; subsequent instruction changes appear here.');
        td.colSpan = 7; rows.append(row);
      }
      $('[data-instruction-older]').disabled = !nextBefore; $('[data-instruction-newest]').disabled = !before;
      status.textContent = `${page.revisions.length} entries shown · ${before ? 'Browsing older entries' : 'Refreshes every 15 seconds'} · observed times reflect catalog fetches, not upstream publication times.`;
    } catch (error) { if (error.name !== 'AbortError') status.textContent = error.message; }
  }
  function renderDiff(target, beforeValue, afterValue) {
    const label = value => !value.present ? 'Missing' : value.kind === 'text' ? 'Text' : 'JSON';
    target.append(node('p', `Before: ${label(beforeValue)} · After: ${label(afterValue)} · − removed · + added`, 'muted'));
    const result = OpenCDXInstructionDiff.diff(beforeValue.text, afterValue.text);
    if (!result.lines.some(line => line.kind !== 'same')) {
      target.append(node('p', beforeValue.present !== afterValue.present ? 'Field presence changed; its text is empty.' : beforeValue.kind !== afterValue.kind ? 'The value type changed; the displayed text is identical.' : 'No text changes.'));
    }
    if (result.coarse) target.append(node('p', 'Large rewrite: the changed region is shown as removed and added blocks. All lines are available.', 'muted'));
    const toggle = node('button', 'Show full field', 'btn btn-secondary'); toggle.type = 'button';
    const wrap = node('div', null, 'instruction-diff-wrap'); wrap.tabIndex = 0;
    wrap.setAttribute('role','region'); wrap.setAttribute('aria-label','Instruction line diff');
    const table = node('table', null, 'instruction-diff-table'); const head = node('thead'), heading = node('tr');
    for (const text of ['Before','After','Change','Instruction text']) { const th = node('th', text); th.scope = 'col'; heading.append(th); }
    head.append(heading); const body = node('tbody'); table.append(head, body); wrap.append(table);
    const more = node('button', '', 'btn btn-secondary'); more.type = 'button';
    let full = false, selected = [], offset = 0;
    function append() {
      const end = Math.min(offset + 400, selected.length);
      for (; offset < end; offset++) {
        const line = selected[offset], row = node('tr', null, `instruction-diff-${line.kind}`);
        if (line.kind === 'gap') { const td = node('td', `${line.count} unchanged lines hidden`); td.colSpan = 4; row.append(td); }
        else {
          row.append(node('td', line.oldLine ?? ''), node('td', line.newLine ?? ''), node('td', line.kind === 'add' ? '+' : line.kind === 'remove' ? '−' : ''));
          const td = node('td'); td.append(node('pre', line.text.replaceAll('\r', '␍') || ' ')); row.append(td);
        }
        body.append(row);
      }
      more.hidden = offset >= selected.length;
      more.textContent = `Show next ${Math.min(400, selected.length - offset)} lines (${selected.length - offset} remaining)`;
    }
    function draw() { body.replaceChildren(); selected = full ? result.lines : OpenCDXInstructionDiff.context(result.lines); offset = 0; append(); toggle.textContent = full ? 'Show changes only' : 'Show full field'; }
    toggle.addEventListener('click', () => { full = !full; draw(); }); more.addEventListener('click', append);
    target.append(toggle, wrap, more); draw();
  }
  function showRevision(entry) {
    detailController?.abort(); const current = new AbortController(); detailController = current;
    $('[data-instruction-title]').textContent = `${entry.model} · ${entry.kind === 'baseline' ? 'baseline' : 'instruction changes'}`;
    $('[data-instruction-detail-summary]').textContent = `${entry.account || entry.account_id} · ${entry.plan || 'Unknown plan'} · ${date(entry.observed_at)} · ${entry.changes.length} fields`;
    const detail = $('[data-instruction-detail]'); detail.replaceChildren();
    detail.append(node('p', entry.kind === 'baseline' ? 'First captured version. Earlier instruction history is unavailable.' : `Previous observation: ${date(entry.previous_observed_at)}. Catalog client version: ${entry.previous_client_version || 'unknown'} → ${entry.client_version || 'unknown'}.`, 'muted'));
    for (const change of entry.changes) {
      const section = node('details', null, 'instruction-field');
      const action = !change.before_hash ? 'Added' : !change.after_hash ? 'Removed' : 'Changed';
      section.append(node('summary', `${change.path} · ${entry.kind === 'baseline' ? 'Baseline' : action}`));
      const body = node('div', null, 'instruction-field-body'); section.append(body); detail.append(section);
      let loaded = false, loading = false;
      section.addEventListener('toggle', async () => {
        if (!section.open || loaded || loading) return;
        loading = true; body.textContent = 'Loading instruction diff…';
        try {
          const field = await get(`/admin/instructions/${entry.id}?${new URLSearchParams({path:change.path})}`, current.signal);
          if (current !== detailController) return;
          body.replaceChildren(); renderDiff(body, field.before, field.after); loaded = true;
        } catch (error) { if (error.name !== 'AbortError') body.textContent = `${error.message} Close and reopen this field to retry.`; }
        finally { loading = false; }
      });
    }
    dialog.showModal();
    detail.querySelector('details')?.setAttribute('open','');
  }
  $('[data-instruction-close]').addEventListener('click', () => dialog.close());
  dialog.addEventListener('close', () => detailController?.abort());
  $('[data-instruction-seen]').addEventListener('click', () => {
    seen = latest; try { localStorage.setItem(seenKey, String(seen)); } catch {}
    updateNotice(0); refreshList();
  });
  form.addEventListener('submit', event => { event.preventDefault(); filters = new URLSearchParams(new FormData(form)); before = 0; refreshList(); });
  $('[data-instruction-newest]').addEventListener('click', () => { before = 0; refreshList(); });
  $('[data-instruction-older]').addEventListener('click', () => { before = nextBefore; refreshList(); });
  async function poll() {
    clearTimeout(timer);
    if (polling || document.visibilityState === 'hidden') return;
    polling = true;
    try { await refreshStatus(); if (!panel.hidden && !before) await refreshList(); }
    catch (error) { notice.textContent = error.message; }
    finally { polling = false; if (document.visibilityState !== 'hidden') timer = setTimeout(poll, 15000); }
  }
  $('[data-instruction-refresh]').addEventListener('click', () => { if (before) refreshList(); poll(); });
  new MutationObserver(() => { if (!panel.hidden) { refreshList(); poll(); } else listController?.abort(); }).observe(panel,{attributes:true,attributeFilter:['hidden']});
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') { clearTimeout(timer); listController?.abort(); } else poll(); });
  window.addEventListener('storage', event => { if (event.key === seenKey) { seen = Math.max(0, Number(event.newValue) || 0); poll(); } });
  poll();
})();
