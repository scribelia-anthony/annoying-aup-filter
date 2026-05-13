/* prompt-cleaner UI */
(() => {
'use strict';

// ─── State ──────────────────────────────────────────────────────────
const state = {
  captures: new Map(),       // id -> capture
  order: [],                 // chronological (oldest -> newest); list renders reversed
  selectedId: null,
  intercept: false,
  rules: [],
  upstream: '',
  filter: '',
  rulesDraft: [],
  interceptModalFor: null,
  interceptQueue: [],
};

// ─── DOM helpers ────────────────────────────────────────────────────
const $ = (id) => document.getElementById(id);
const el = (tag, attrs = {}, ...kids) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') e.className = v;
    else if (k === 'data') for (const [dk, dv] of Object.entries(v)) e.dataset[dk] = dv;
    else if (k.startsWith('on') && typeof v === 'function') e.addEventListener(k.slice(2), v);
    else if (v === true) e.setAttribute(k, '');
    else if (v != null && v !== false) e.setAttribute(k, v);
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    e.appendChild(typeof kid === 'string' ? document.createTextNode(kid) : kid);
  }
  return e;
};

const fmtMs = (ms) => {
  if (ms == null) return '';
  if (ms < 1000) return ms + 'ms';
  return (ms / 1000).toFixed(1) + 's';
};
const fmtTimeAgo = (iso) => {
  if (!iso) return '';
  const d = new Date(iso);
  const s = (Date.now() - d.getTime()) / 1000;
  if (s < 1) return 'now';
  if (s < 60) return Math.round(s) + 's';
  if (s < 3600) return Math.round(s / 60) + 'm';
  return Math.round(s / 3600) + 'h';
};

const toast = (msg, kind = '') => {
  const t = el('div', { class: 'toast ' + kind }, msg);
  $('toast-container').appendChild(t);
  setTimeout(() => t.remove(), 3500);
};

// ─── Formatting ─────────────────────────────────────────────────────
function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function tryPrettyJSON(s) {
  if (!s) return '';
  try {
    const parsed = JSON.parse(s);
    return JSON.stringify(parsed, null, 2);
  } catch {
    return s;
  }
}

function highlightJSON(s) {
  if (!s) return '';
  try { JSON.parse(s); } catch { return escapeHtml(s); }
  const esc = escapeHtml(s);
  return esc.replace(/("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,
    (m, str, colon, bool, num) => {
      if (str) return `<span class="${colon ? 'json-key' : 'json-str'}">${str}</span>${colon || ''}`;
      if (bool) return `<span class="${bool === 'null' ? 'json-null' : 'json-bool'}">${bool}</span>`;
      if (num) return `<span class="json-num">${num}</span>`;
      return m;
    });
}

function highlightSSE(s) {
  if (!s) return '';
  const lines = s.split('\n');
  return lines.map(line => {
    if (line.startsWith(':')) return `<span class="sse-comment">${escapeHtml(line)}</span>`;
    if (line.startsWith('event:')) return `<span class="sse-event-line">${escapeHtml(line)}</span>`;
    if (line.startsWith('data:')) {
      const data = line.slice(5).trim();
      if (data.startsWith('{') || data.startsWith('[')) {
        return `data: <span class="sse-data-line">${highlightJSON(data)}</span>`;
      }
      return `<span class="sse-data-line">${escapeHtml(line)}</span>`;
    }
    return escapeHtml(line);
  }).join('\n');
}

// ─── SSE connection ─────────────────────────────────────────────────
let evtSource = null;
function connect() {
  $('conn-status').textContent = 'connecting…';
  $('conn-status').className = 'status';
  evtSource = new EventSource('/events');
  evtSource.addEventListener('snapshot', (e) => {
    const snap = JSON.parse(e.data);
    state.intercept = !!snap.intercept;
    state.rules = snap.rules || [];
    state.upstream = snap.upstream || '';
    state.captures.clear();
    state.order = [];
    for (const c of (snap.captures || [])) {
      state.captures.set(c.id, c);
      state.order.push(c.id);
    }
    renderAll();
    $('conn-status').textContent = '● connected';
    $('conn-status').className = 'status ok';
  });
  evtSource.onmessage = (e) => {
    try {
      const ev = JSON.parse(e.data);
      handleEvent(ev);
    } catch (err) {
      console.error('bad event', err, e.data);
    }
  };
  evtSource.onerror = () => {
    $('conn-status').textContent = '● disconnected (retrying)';
    $('conn-status').className = 'status err';
  };
}

function handleEvent(ev) {
  switch (ev.type) {
    case 'capture_started':
    case 'capture_updated':
    case 'response_started':
    case 'capture_completed':
    case 'capture_errored': {
      if (!ev.payload) return;
      const cap = ev.payload;
      if (!state.captures.has(cap.id)) state.order.push(cap.id);
      state.captures.set(cap.id, cap);
      renderListItem(cap);
      if (state.selectedId === cap.id) renderDetail(cap);
      updateCount();
      break;
    }
    case 'capture_intercepted': {
      if (!ev.payload) return;
      const cap = ev.payload;
      if (!state.captures.has(cap.id)) state.order.push(cap.id);
      state.captures.set(cap.id, cap);
      renderListItem(cap);
      if (state.selectedId === cap.id) renderDetail(cap);
      openInterceptModal(cap);
      updateCount();
      break;
    }
    case 'response_chunk': {
      const cap = state.captures.get(ev.id);
      if (!cap) return;
      if (!cap.response) cap.response = { streaming: true, chunks: [] };
      cap.response.chunks = cap.response.chunks || [];
      cap.response.chunks.push(ev.payload);
      if (state.selectedId === cap.id) appendStreamChunk(ev.payload);
      break;
    }
    case 'captures_cleared':
      state.captures.clear();
      state.order = [];
      state.selectedId = null;
      renderAll();
      break;
    case 'intercept_toggled':
      state.intercept = ev.payload?.enabled ?? state.intercept;
      $('intercept-toggle').checked = state.intercept;
      break;
    case 'rules_updated':
      state.rules = ev.payload || [];
      $('rules-count').textContent = state.rules.filter(r => r.enabled).length;
      break;
    default:
      // ignore unknown events
  }
}

// ─── Rendering ──────────────────────────────────────────────────────
function renderAll() {
  $('upstream').textContent = `— upstream: ${state.upstream}`;
  $('intercept-toggle').checked = state.intercept;
  $('rules-count').textContent = (state.rules || []).filter(r => r.enabled).length;
  $('captures').innerHTML = '';
  // render newest first
  for (let i = state.order.length - 1; i >= 0; i--) {
    const cap = state.captures.get(state.order[i]);
    if (cap) renderListItem(cap);
  }
  updateCount();
  if (state.selectedId && state.captures.has(state.selectedId)) {
    renderDetail(state.captures.get(state.selectedId));
  } else {
    $('detail-pane').hidden = true;
    $('empty-pane').hidden = false;
  }
}

function updateCount() {
  const total = state.captures.size;
  const shown = filteredIds().length;
  $('capture-count').textContent = shown === total ? `${total}` : `${shown}/${total}`;
}

function captureMatches(cap, q) {
  if (!q) return true;
  q = q.toLowerCase();
  if (cap.request?.method?.toLowerCase().includes(q)) return true;
  if (cap.request?.url?.toLowerCase().includes(q)) return true;
  if (cap.request?.path?.toLowerCase().includes(q)) return true;
  if ((cap.status || '').toLowerCase().includes(q)) return true;
  if (String(cap.response?.status || '').includes(q)) return true;
  if (cap.request?.body?.toLowerCase().includes(q)) return true;
  if (cap.response?.body?.toLowerCase().includes(q)) return true;
  return false;
}

function filteredIds() {
  return state.order.filter(id => captureMatches(state.captures.get(id), state.filter));
}

function renderListItem(cap) {
  const existing = document.querySelector(`#captures li[data-id="${cap.id}"]`);
  const li = existing || el('li', { data: { id: cap.id }, onclick: () => selectCapture(cap.id) });

  li.innerHTML = '';
  li.className = '';
  if (state.selectedId === cap.id) li.classList.add('selected');
  if (cap.modified) li.classList.add('modified');
  if (cap.replay_of) li.classList.add('replay');

  const method = cap.request?.method || '?';
  const path = cap.request?.path || cap.request?.url || '?';
  const status = cap.response?.status ? String(cap.response.status) : (cap.status || '—');

  li.appendChild(el('span', { class: 'method' }, method));
  li.appendChild(el('span', { class: 'path', title: path }, path));
  li.appendChild(el('span', { class: 'meta st-' + (cap.status || 'pending') },
    cap.response?.status ? `${status} · ${fmtMs(cap.duration_ms)}` : cap.status));

  if (!existing) {
    // newest first
    const list = $('captures');
    if (captureMatches(cap, state.filter)) {
      list.insertBefore(li, list.firstChild);
    }
  } else if (!captureMatches(cap, state.filter)) {
    li.remove();
  }
}

function selectCapture(id) {
  state.selectedId = id;
  document.querySelectorAll('#captures li').forEach(li => li.classList.toggle('selected', li.dataset.id === id));
  const cap = state.captures.get(id);
  if (cap) renderDetail(cap);
}

function renderDetail(cap) {
  $('empty-pane').hidden = true;
  $('detail-pane').hidden = false;

  $('d-method').textContent = cap.request?.method || '';
  $('d-url').textContent = cap.request?.url || '';
  $('d-url').title = cap.request?.url || '';
  $('d-status').textContent = cap.response?.status ? `${cap.response.status} ${cap.status}` : (cap.status || '');
  $('d-time').textContent = [
    cap.started_at ? new Date(cap.started_at).toLocaleTimeString() : '',
    cap.duration_ms ? `· ${fmtMs(cap.duration_ms)}` : '',
    cap.modified ? '· modified' : '',
    cap.replay_of ? `· replay of ${cap.replay_of}` : '',
  ].filter(Boolean).join(' ');

  // Request tab
  $('req-line').textContent = `${cap.request?.method || ''} ${cap.request?.url || ''}`;
  renderHeaders($('req-headers'), cap.request?.headers || {});
  $('req-headers-count').textContent = `(${Object.keys(cap.request?.headers || {}).length})`;
  const reqBody = cap.request?.body || '';
  $('req-body-info').textContent = reqBody ? `(${reqBody.length} bytes)` : '(empty)';
  $('req-body').innerHTML = highlightJSON(tryPrettyJSON(reqBody));

  // Response tab
  $('resp-line').textContent = cap.response
    ? `${cap.response.status}${cap.response.streaming ? ' · streaming SSE' : ''}`
    : (cap.error ? 'ERROR: ' + cap.error : '(no response yet)');
  renderHeaders($('resp-headers'), cap.response?.headers || {});
  $('resp-headers-count').textContent = `(${Object.keys(cap.response?.headers || {}).length})`;
  const respBody = cap.response?.body || (cap.response?.chunks || []).map(c => c.data).join('');
  $('resp-body-info').textContent = respBody ? `(${respBody.length} bytes${cap.response?.streaming ? ', SSE' : ''})` : '(none)';
  if (cap.response?.streaming) {
    $('resp-body').innerHTML = highlightSSE(respBody);
  } else {
    $('resp-body').innerHTML = highlightJSON(tryPrettyJSON(respBody));
  }

  // Raw tab
  $('raw-json').innerHTML = highlightJSON(JSON.stringify(cap, null, 2));
}

function appendStreamChunk(chunk) {
  const cap = state.captures.get(state.selectedId);
  if (!cap || !cap.response?.streaming) return;
  if (document.querySelector('#tab-resp:not([hidden])')) {
    const respBody = (cap.response.chunks || []).map(c => c.data).join('');
    $('resp-body').innerHTML = highlightSSE(respBody);
    $('resp-body-info').textContent = `(${respBody.length} bytes, SSE, ${cap.response.chunks.length} chunks)`;
  }
}

function renderHeaders(tbl, headers) {
  tbl.innerHTML = '';
  const names = Object.keys(headers).sort();
  for (const k of names) {
    const vs = headers[k] || [];
    for (const v of vs) {
      const tr = el('tr', {},
        el('td', { class: 'k' }, k),
        el('td', { class: 'v' }, v)
      );
      tbl.appendChild(tr);
    }
  }
}

// ─── Tabs ──────────────────────────────────────────────────────────
document.querySelectorAll('.tab').forEach(t => {
  t.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach(x => x.classList.remove('active'));
    t.classList.add('active');
    const tab = t.dataset.tab;
    document.querySelectorAll('.tab-pane').forEach(p => p.hidden = true);
    $('tab-' + tab).hidden = false;
  });
});

// ─── Topbar actions ─────────────────────────────────────────────────
$('intercept-toggle').addEventListener('change', async () => {
  const enabled = $('intercept-toggle').checked;
  await fetch('/admin/intercept', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  });
  state.intercept = enabled;
});

$('clear-btn').addEventListener('click', async () => {
  if (!confirm('Clear all captured requests?')) return;
  await fetch('/admin/clear', { method: 'POST' });
});

$('rules-btn').addEventListener('click', openRulesModal);
$('replay-btn').addEventListener('click', () => {
  const cap = state.captures.get(state.selectedId);
  if (cap) openReplayModal(cap);
});

$('filter').addEventListener('input', (e) => {
  state.filter = e.target.value;
  $('captures').innerHTML = '';
  for (let i = state.order.length - 1; i >= 0; i--) {
    const cap = state.captures.get(state.order[i]);
    if (cap && captureMatches(cap, state.filter)) renderListItem(cap);
  }
  updateCount();
});

// ─── Intercept modal ────────────────────────────────────────────────
function openInterceptModal(cap) {
  if (state.interceptModalFor && state.interceptModalFor !== cap.id) {
    // already showing another intercepted request — queue this one
    if (!state.interceptQueue.includes(cap.id)) state.interceptQueue.push(cap.id);
    return;
  }
  state.interceptModalFor = cap.id;
  $('im-id').textContent = `${cap.id} · ${cap.request.method} ${cap.request.path}`;
  $('im-url').value = cap.request.url || '';
  $('im-headers').value = JSON.stringify(cap.request.headers || {}, null, 2);
  $('im-body').value = tryPrettyJSON(cap.request.body || '');
  $('intercept-modal').hidden = false;
}

function closeInterceptModal() {
  state.interceptModalFor = null;
  $('intercept-modal').hidden = true;
  // pop next from queue, if any
  while (state.interceptQueue.length > 0) {
    const nextId = state.interceptQueue.shift();
    const next = state.captures.get(nextId);
    if (next && next.status === 'intercepted') {
      openInterceptModal(next);
      return;
    }
  }
}

$('im-forward-orig').addEventListener('click', async () => {
  const id = state.interceptModalFor; if (!id) return;
  const cap = state.captures.get(id);
  if (!cap) return;
  await postForward(id, cap.request.url, cap.request.headers, cap.request.body);
  closeInterceptModal();
});

$('im-forward-mod').addEventListener('click', async () => {
  const id = state.interceptModalFor; if (!id) return;
  let headers;
  try { headers = JSON.parse($('im-headers').value); }
  catch (e) { toast('Invalid headers JSON: ' + e.message, 'error'); return; }
  await postForward(id, $('im-url').value, headers, $('im-body').value);
  closeInterceptModal();
});

$('im-drop').addEventListener('click', async () => {
  const id = state.interceptModalFor; if (!id) return;
  const res = await fetch(`/admin/captures/${id}/drop`, { method: 'POST' });
  if (!res.ok) { toast('Drop failed: ' + res.status, 'error'); return; }
  toast('Request dropped', 'ok');
  closeInterceptModal();
});

async function postForward(id, url, headers, body) {
  const res = await fetch(`/admin/captures/${id}/forward`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url, headers, body }),
  });
  if (!res.ok) toast('Forward failed: ' + res.status, 'error');
  else toast('Forwarded', 'ok');
}

// ─── Replay modal ───────────────────────────────────────────────────
function openReplayModal(cap) {
  $('rm-from').textContent = `from ${cap.id}`;
  $('rm-method').value = cap.request.method || 'POST';
  $('rm-url').value = cap.request.url || '';
  $('rm-headers').value = JSON.stringify(cap.request.headers || {}, null, 2);
  $('rm-body').value = tryPrettyJSON(cap.request.body || '');
  $('replay-modal').dataset.fromId = cap.id;
  $('replay-modal').hidden = false;
}

$('rm-cancel').addEventListener('click', () => { $('replay-modal').hidden = true; });

$('rm-send').addEventListener('click', async () => {
  const id = $('replay-modal').dataset.fromId;
  let headers;
  try { headers = JSON.parse($('rm-headers').value); }
  catch (e) { toast('Invalid headers JSON: ' + e.message, 'error'); return; }
  const res = await fetch(`/admin/captures/${id}/replay`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      method: $('rm-method').value,
      url: $('rm-url').value,
      headers,
      body: $('rm-body').value,
    }),
  });
  if (!res.ok) { toast('Replay failed: ' + res.status, 'error'); return; }
  toast('Replay sent', 'ok');
  $('replay-modal').hidden = true;
});

// ─── Rules modal ────────────────────────────────────────────────────
function openRulesModal() {
  state.rulesDraft = state.rules.map(r => ({ ...r }));
  renderRulesTable();
  $('rules-modal').hidden = false;
}

function renderRulesTable() {
  const tbody = $('rules-body');
  tbody.innerHTML = '';
  state.rulesDraft.forEach((r, i) => {
    const tr = el('tr', {},
      el('td', {}, el('input', {
        type: 'checkbox',
        checked: r.enabled ? true : undefined,
        onchange: (e) => { state.rulesDraft[i].enabled = e.target.checked; },
      })),
      el('td', {}, el('input', {
        type: 'text', value: r.name || '',
        oninput: (e) => { state.rulesDraft[i].name = e.target.value; },
      })),
      el('td', {}, selectInput(['request','response'], r.phase || 'request', (v) => state.rulesDraft[i].phase = v)),
      el('td', {}, selectInput(['body','header','url'], r.target || 'body', (v) => state.rulesDraft[i].target = v)),
      el('td', {}, el('input', {
        type: 'text', value: r.header_name || '', placeholder: '(only for header)',
        oninput: (e) => { state.rulesDraft[i].header_name = e.target.value; },
      })),
      el('td', {}, el('input', {
        type: 'text', value: r.match || '', placeholder: 'regex',
        oninput: (e) => { state.rulesDraft[i].match = e.target.value; },
      })),
      el('td', {}, el('input', {
        type: 'text', value: r.replacement || '', placeholder: 'replacement (use $1, $2…)',
        oninput: (e) => { state.rulesDraft[i].replacement = e.target.value; },
      })),
      el('td', {}, el('button', {
        class: 'icon-btn', title: 'Delete',
        onclick: () => { state.rulesDraft.splice(i, 1); renderRulesTable(); },
      }, '✕'))
    );
    tbody.appendChild(tr);
  });
}

function selectInput(options, current, onchange) {
  const sel = el('select', { onchange: (e) => onchange(e.target.value) });
  for (const o of options) sel.appendChild(el('option', { value: o, selected: o === current ? true : undefined }, o));
  return sel;
}

$('rules-add').addEventListener('click', () => {
  state.rulesDraft.push({ name: '', enabled: true, phase: 'request', target: 'body', match: '', replacement: '' });
  renderRulesTable();
});

$('rules-cancel').addEventListener('click', () => { $('rules-modal').hidden = true; });

$('rules-save').addEventListener('click', async () => {
  const res = await fetch('/admin/rules', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(state.rulesDraft),
  });
  if (!res.ok) {
    const txt = await res.text();
    toast('Rule error: ' + txt, 'error');
    return;
  }
  state.rules = await res.json();
  $('rules-count').textContent = state.rules.filter(r => r.enabled).length;
  $('rules-modal').hidden = true;
  toast('Rules saved', 'ok');
});

// Close modals on Esc
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    if (!$('rules-modal').hidden) $('rules-modal').hidden = true;
    else if (!$('replay-modal').hidden) $('replay-modal').hidden = true;
    // intercept modal: don't auto-close, the user must decide
  }
});

// ─── Boot ───────────────────────────────────────────────────────────
connect();

})();
