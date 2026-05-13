/* prompt-cleaner UI */
(() => {
'use strict';

// ─── State ──────────────────────────────────────────────────────────
const state = {
  captures: new Map(),
  order: [],
  selectedId: null,
  intercept: false,
  rules: [],
  upstream: '',
  filter: '',
  rulesDraft: [],
  interceptModalFor: null,
  interceptQueue: [],
};

let imEditor = null;
let rmEditor = null;

// ─── DOM helpers ────────────────────────────────────────────────────
const $ = (id) => document.getElementById(id);
const el = (tag, attrs = {}, ...kids) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') e.className = v;
    else if (k === 'data') for (const [dk, dv] of Object.entries(v)) e.dataset[dk] = dv;
    else if (k.startsWith('on') && typeof v === 'function') e.addEventListener(k.slice(2), v);
    else if (v === true) e.setAttribute(k, '');
    else e.setAttribute(k, v);
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
const fmtBytes = (n) => {
  if (n < 1024) return n + 'B';
  if (n < 1024*1024) return (n/1024).toFixed(1) + 'KB';
  return (n/1024/1024).toFixed(1) + 'MB';
};
// Try to extract the model name from a captured request body (parsed JSON).
function modelOfCapture(cap) {
  const body = cap.request?.body;
  if (!body) return '';
  try {
    const parsed = JSON.parse(body);
    return parsed.model || '';
  } catch { return ''; }
}

// Make the path display useful. /v1/messages is the same for ~everything;
// show endpoint + a short hint when the body is a Messages request.
function summarizePath(cap) {
  const p = cap.request?.path || cap.request?.url || '?';
  return p.replace(/^\/v1\//, '/');
}

const shortModel = (m) => {
  if (!m) return '';
  // claude-sonnet-4-6 → Sonnet 4.6 ; claude-opus-4-7 → Opus 4.7
  const match = m.match(/^claude-(opus|sonnet|haiku)-?(\d+)-?(\d+)?/);
  if (!match) return m.replace(/^claude-/, '');
  const fam = match[1].charAt(0).toUpperCase() + match[1].slice(1);
  return `${fam} ${match[2]}${match[3] ? '.' + match[3] : ''}`;
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
  try { return JSON.stringify(JSON.parse(s), null, 2); }
  catch { return s; }
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
  return s.split('\n').map(line => {
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

// ─── Structured body editor ─────────────────────────────────────────
// Builds a structured editor for Anthropic Messages-style request bodies.
// Returns { getBody(): string, isStructured: bool }.
//
// Mutates content blocks in place; `getBody()` re-serializes everything.
// Falls back to a single raw textarea for non-Messages bodies.

function createBodyEditor(hostId, bodyText, opts = {}) {
  const host = (typeof hostId === 'string') ? $(hostId) : hostId;
  host.innerHTML = '';
  const readOnly = !!opts.readOnly;

  let parsed = null;
  try { parsed = JSON.parse(bodyText); } catch {}

  if (!parsed || typeof parsed !== 'object' || !Array.isArray(parsed.messages)) {
    // Fallback: raw display/edit
    const ta = el(readOnly ? 'pre' : 'textarea', {
      class: readOnly ? 'code body' : 'raw-json-area',
      rows: 24,
    });
    if (readOnly) {
      ta.innerHTML = highlightJSON(tryPrettyJSON(bodyText));
    } else {
      ta.value = tryPrettyJSON(bodyText);
    }
    host.appendChild(ta);
    return {
      isStructured: false,
      getBody: () => readOnly ? bodyText : ta.value,
    };
  }

  // Deep-ish clone so edits don't mutate caller's parsed obj
  const body = JSON.parse(JSON.stringify(parsed));

  // ─── Meta row (model / max_tokens / stream / temperature) ───
  const metaInputs = {};
  const meta = el('div', { class: 'body-meta' },
    el('label', {}, 'Model',
      metaInputs.model = el('input', { type: 'text', value: body.model || '', disabled: readOnly ? true : undefined })),
    el('label', {}, 'Max tokens',
      metaInputs.max_tokens = el('input', { type: 'number', value: body.max_tokens ?? '', disabled: readOnly ? true : undefined })),
    el('label', { class: 'inline' },
      metaInputs.stream = el('input', { type: 'checkbox', checked: body.stream ? true : undefined, disabled: readOnly ? true : undefined }),
      'Stream'),
    el('label', {}, 'Temperature',
      metaInputs.temperature = el('input', { type: 'number', step: '0.1', value: body.temperature ?? '', disabled: readOnly ? true : undefined })),
  );
  host.appendChild(meta);

  // ─── System ───
  const systemDetails = el('details', { class: 'collapsible', open: body.system != null ? true : undefined });
  const systemSummary = el('summary');
  systemDetails.appendChild(systemSummary);
  const systemBody = el('div', { class: 'system-blocks' });
  systemDetails.appendChild(systemBody);

  // Normalize system into a list of {kind: 'text'|'raw', textarea}
  const systemState = normalizeSystem(body.system);
  function renderSystem() {
    systemBody.innerHTML = '';
    let totalChars = 0;
    if (systemState.kind === 'string') {
      totalChars = (systemState.text || '').length;
      const blk = el('div', { class: 'system-block' },
        el('div', { class: 'system-block-head' }, el('span', {}, `text · ${fmtBytes(totalChars)}`)),
      );
      const ta = el('textarea', { rows: 8, disabled: readOnly ? true : undefined });
      ta.value = systemState.text || '';
      if (!readOnly) ta.addEventListener('input', () => {
        systemState.text = ta.value;
        updateSystemSummary();
      });
      blk.appendChild(ta);
      systemBody.appendChild(blk);
    } else if (systemState.kind === 'array') {
      systemState.blocks.forEach((b, i) => {
        const isText = b && b.type === 'text';
        totalChars += isText ? (b.text || '').length : JSON.stringify(b).length;
        const blk = el('div', { class: 'system-block' },
          el('div', { class: 'system-block-head' },
            el('span', {}, `[${i}] ${b.type || '?'}${b.cache_control ? ' · ⚡cache' : ''}`),
            el('span', {}, fmtBytes(isText ? (b.text || '').length : JSON.stringify(b).length))),
        );
        if (isText) {
          const ta = el('textarea', { rows: 10, disabled: readOnly ? true : undefined });
          ta.value = b.text || '';
          if (!readOnly) ta.addEventListener('input', () => {
            b.text = ta.value;
            updateSystemSummary();
          });
          blk.appendChild(ta);
        } else {
          const ta = el('textarea', { class: 'raw-edit', rows: 8, disabled: readOnly ? true : undefined });
          ta.value = JSON.stringify(b, null, 2);
          if (!readOnly) ta.addEventListener('input', () => {
            try {
              const nb = JSON.parse(ta.value);
              Object.keys(b).forEach(k => delete b[k]);
              Object.assign(b, nb);
              updateSystemSummary();
            } catch {}
          });
          blk.appendChild(ta);
        }
        systemBody.appendChild(blk);
      });
    } else {
      systemBody.appendChild(el('div', { class: 'read-only-text muted' }, '(no system prompt)'));
    }
  }
  function updateSystemSummary() {
    let total = 0;
    if (systemState.kind === 'string') total = (systemState.text || '').length;
    else if (systemState.kind === 'array') {
      for (const b of systemState.blocks) {
        total += (b && b.type === 'text') ? (b.text || '').length : JSON.stringify(b || '').length;
      }
    }
    const count = systemState.kind === 'array' ? systemState.blocks.length : (systemState.kind === 'string' ? 1 : 0);
    systemSummary.innerHTML = '';
    systemSummary.append('System ');
    systemSummary.appendChild(el('span', { class: 'muted' }, `· ${count} block${count !== 1 ? 's' : ''} · ${fmtBytes(total)}`));
  }
  updateSystemSummary();
  renderSystem();
  host.appendChild(systemDetails);

  // ─── Messages ───
  const messagesDetails = el('details', { class: 'collapsible', open: true });
  const messagesSummary = el('summary');
  messagesDetails.appendChild(messagesSummary);
  const messagesList = el('div', { class: 'messages-list' });
  messagesDetails.appendChild(messagesList);

  function renderMessages() {
    messagesList.innerHTML = '';
    body.messages.forEach((m, mi) => {
      const card = renderMessageCard(m, mi, readOnly, () => {
        updateMessagesSummary();
      }, () => {
        // remove
        body.messages.splice(mi, 1);
        renderMessages();
        updateMessagesSummary();
      });
      messagesList.appendChild(card);
    });
    if (!readOnly) {
      const addRow = el('div', { class: 'add-message-row' },
        el('button', { class: 'btn', onclick: () => { body.messages.push({ role: 'user', content: [{ type: 'text', text: '' }] }); renderMessages(); updateMessagesSummary(); } }, '+ user'),
        el('button', { class: 'btn', onclick: () => { body.messages.push({ role: 'assistant', content: [{ type: 'text', text: '' }] }); renderMessages(); updateMessagesSummary(); } }, '+ assistant'),
      );
      messagesList.appendChild(addRow);
    }
  }
  function updateMessagesSummary() {
    let total = 0;
    for (const m of body.messages) {
      total += JSON.stringify(m).length;
    }
    messagesSummary.innerHTML = '';
    messagesSummary.append('Messages ');
    messagesSummary.appendChild(el('span', { class: 'muted' }, `· ${body.messages.length} · ${fmtBytes(total)}`));
  }
  updateMessagesSummary();
  renderMessages();
  host.appendChild(messagesDetails);

  // ─── Tools ───
  const toolsDetails = el('details', { class: 'collapsible' });
  const toolsSummary = el('summary');
  toolsDetails.appendChild(toolsSummary);
  const toolsState = { raw: body.tools ? JSON.stringify(body.tools, null, 2) : '' };
  const toolsTa = el('textarea', { class: 'raw-edit', rows: 14, disabled: readOnly ? true : undefined });
  toolsTa.value = toolsState.raw;
  if (!readOnly) toolsTa.addEventListener('input', () => { toolsState.raw = toolsTa.value; updateToolsSummary(); });
  toolsDetails.appendChild(toolsTa);
  function updateToolsSummary() {
    let count = 0;
    try { const arr = JSON.parse(toolsState.raw); if (Array.isArray(arr)) count = arr.length; } catch {}
    toolsSummary.innerHTML = '';
    toolsSummary.append('Tools ');
    toolsSummary.appendChild(el('span', { class: 'muted' }, `· ${count} · ${fmtBytes(toolsState.raw.length)}`));
  }
  updateToolsSummary();
  host.appendChild(toolsDetails);

  // ─── Other (every other top-level key as raw JSON) ───
  const structuredKeys = ['model', 'max_tokens', 'stream', 'temperature', 'system', 'messages', 'tools'];
  const otherObj = {};
  for (const [k, v] of Object.entries(body)) {
    if (!structuredKeys.includes(k)) otherObj[k] = v;
  }
  const otherDetails = el('details', { class: 'collapsible' });
  const otherSummary = el('summary');
  otherDetails.appendChild(otherSummary);
  const otherState = { raw: Object.keys(otherObj).length ? JSON.stringify(otherObj, null, 2) : '{}' };
  const otherTa = el('textarea', { class: 'raw-edit', rows: 10, disabled: readOnly ? true : undefined });
  otherTa.value = otherState.raw;
  if (!readOnly) otherTa.addEventListener('input', () => { otherState.raw = otherTa.value; updateOtherSummary(); });
  otherDetails.appendChild(otherTa);
  function updateOtherSummary() {
    let keys = [];
    try { keys = Object.keys(JSON.parse(otherState.raw)); } catch {}
    otherSummary.innerHTML = '';
    otherSummary.append('Other fields ');
    otherSummary.appendChild(el('span', { class: 'muted' }, `· ${keys.length ? keys.join(', ') : '(none)'}`));
  }
  updateOtherSummary();
  host.appendChild(otherDetails);

  return {
    isStructured: true,
    getBody: () => {
      const out = {};
      // Preserve original key order (model/system/messages/tools usually first)
      // Start with the structured fields in a natural order
      if (metaInputs.model.value !== '') out.model = metaInputs.model.value;
      if (metaInputs.max_tokens.value !== '') out.max_tokens = +metaInputs.max_tokens.value;

      // system
      if (systemState.kind === 'string') out.system = systemState.text;
      else if (systemState.kind === 'array') out.system = systemState.blocks;

      // messages
      out.messages = body.messages;

      // tools
      try { const t = JSON.parse(toolsState.raw); if (t != null) out.tools = t; }
      catch { if (body.tools != null) out.tools = body.tools; }

      // other
      try {
        const o = JSON.parse(otherState.raw);
        for (const [k, v] of Object.entries(o)) out[k] = v;
      } catch {}

      // Add stream / temperature into out (after others, but doesn't really matter)
      out.stream = metaInputs.stream.checked;
      if (metaInputs.temperature.value !== '') out.temperature = +metaInputs.temperature.value;

      return JSON.stringify(out, null, 2);
    },
  };
}

function normalizeSystem(sys) {
  if (sys == null) return { kind: 'absent' };
  if (typeof sys === 'string') return { kind: 'string', text: sys };
  if (Array.isArray(sys)) return { kind: 'array', blocks: sys };
  return { kind: 'absent' };
}

function renderMessageCard(message, idx, readOnly, onChange, onRemove) {
  const card = el('div', { class: 'message-card', data: { role: message.role || 'user' } });

  // Normalize content into array of blocks for editing convenience
  let blocks;
  if (typeof message.content === 'string') {
    blocks = [{ type: 'text', text: message.content }];
    message.content = blocks; // mutate the message so it stays array
  } else if (Array.isArray(message.content)) {
    blocks = message.content;
  } else {
    blocks = [];
    message.content = blocks;
  }

  // Summary
  const textCount = blocks.filter(b => b.type === 'text').length;
  const otherTypes = blocks.filter(b => b.type !== 'text').map(b => b.type);
  const summaryParts = [];
  if (textCount) summaryParts.push(`${textCount} text`);
  if (otherTypes.length) summaryParts.push(otherTypes.join(', '));
  const totalLen = JSON.stringify(blocks).length;

  const head = el('div', { class: 'message-head' },
    el('span', { class: 'message-role' }, `#${idx} ${message.role || '?'}`),
    el('span', { class: 'message-summary' }, `· ${summaryParts.join(' · ') || 'empty'} · ${fmtBytes(totalLen)}`),
    el('span', { class: 'message-actions' },
      !readOnly ? el('button', {
        class: 'icon-btn',
        title: 'switch role',
        onclick: () => {
          message.role = (message.role === 'user') ? 'assistant' : 'user';
          card.dataset.role = message.role;
          head.querySelector('.message-role').textContent = `#${idx} ${message.role}`;
          onChange?.();
        },
      }, '⇄') : null,
      !readOnly ? el('button', {
        class: 'icon-btn',
        title: '+ text block',
        onclick: () => {
          blocks.push({ type: 'text', text: '' });
          renderBlocks();
          onChange?.();
        },
      }, '+text') : null,
      !readOnly ? el('button', {
        class: 'icon-btn danger',
        title: 'remove message',
        onclick: () => onRemove?.(),
      }, '✕') : null,
    ),
  );
  card.appendChild(head);

  const body = el('div', { class: 'message-body' });
  card.appendChild(body);

  function renderBlocks() {
    body.innerHTML = '';
    blocks.forEach((b, bi) => {
      body.appendChild(renderContentBlock(b, bi, blocks, readOnly, () => onChange?.()));
    });
  }
  renderBlocks();

  return card;
}

function renderContentBlock(block, bi, blocks, readOnly, onChange) {
  const wrap = el('div', { class: 'content-block' });
  const type = block.type || 'unknown';
  const head = el('div', { class: 'content-block-head' },
    el('span', { class: 'content-block-type content-block-' + type }, type),
    type === 'tool_use' ? el('span', { class: 'muted' }, `· ${block.name || '?'} · id=${block.id || '?'}`) : null,
    type === 'tool_result' ? el('span', { class: 'muted' }, `· id=${block.tool_use_id || '?'}${block.is_error ? ' · ⚠error' : ''}`) : null,
    el('span', { class: 'muted', style: 'margin-left:auto' }, fmtBytes(JSON.stringify(block).length)),
    !readOnly ? el('button', {
      class: 'icon-btn danger',
      title: 'remove block',
      style: 'margin-left:6px',
      onclick: () => { blocks.splice(bi, 1); wrap.parentNode.removeChild(wrap); onChange?.(); },
    }, '✕') : null,
  );
  wrap.appendChild(head);

  if (type === 'text') {
    const ta = el('textarea', { disabled: readOnly ? true : undefined });
    ta.value = block.text || '';
    if (!readOnly) ta.addEventListener('input', () => { block.text = ta.value; onChange?.(); });
    wrap.appendChild(ta);
  } else if (type === 'thinking') {
    // text-shaped under .thinking
    const ta = el('textarea', { disabled: readOnly ? true : undefined });
    ta.value = block.thinking || '';
    if (!readOnly) ta.addEventListener('input', () => { block.thinking = ta.value; onChange?.(); });
    wrap.appendChild(ta);
  } else if (type === 'tool_result') {
    // tool_result content is itself a string or content blocks
    const content = block.content;
    if (typeof content === 'string') {
      const ta = el('textarea', { disabled: readOnly ? true : undefined });
      ta.value = content;
      if (!readOnly) ta.addEventListener('input', () => { block.content = ta.value; onChange?.(); });
      wrap.appendChild(ta);
    } else {
      // raw JSON for nested structure
      const ta = el('textarea', { class: 'raw-edit', disabled: readOnly ? true : undefined });
      ta.value = JSON.stringify(block, null, 2);
      if (!readOnly) ta.addEventListener('input', () => {
        try {
          const nb = JSON.parse(ta.value);
          Object.keys(block).forEach(k => delete block[k]);
          Object.assign(block, nb);
          onChange?.();
        } catch {}
      });
      wrap.appendChild(ta);
    }
  } else {
    // generic raw JSON view
    const det = el('details', {});
    det.appendChild(el('summary', {}, 'view / edit JSON'));
    const ta = el('textarea', { class: 'raw-edit', disabled: readOnly ? true : undefined });
    ta.value = JSON.stringify(block, null, 2);
    if (!readOnly) ta.addEventListener('input', () => {
      try {
        const nb = JSON.parse(ta.value);
        Object.keys(block).forEach(k => delete block[k]);
        Object.assign(block, nb);
        onChange?.();
      } catch {}
    });
    det.appendChild(ta);
    wrap.appendChild(det);
  }

  return wrap;
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
    state.fallback = !!snap.fallback;
    state.fallbackModel = snap.fallback_model || 'claude-sonnet-4-6';
    const proxyUrl = $('empty-proxy-url');
    if (proxyUrl) {
      proxyUrl.textContent = snap.proxy_addr ? `http://${snap.proxy_addr}` : 'http://127.0.0.1:8080';
    }
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
    try { handleEvent(JSON.parse(e.data)); }
    catch (err) { console.error('bad event', err, e.data); }
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
    case 'fallback_toggled':
      state.fallback = ev.payload?.enabled ?? state.fallback;
      state.fallbackModel = ev.payload?.model ?? state.fallbackModel;
      $('fallback-toggle').checked = state.fallback;
      $('fallback-model-label').textContent = shortModel(state.fallbackModel);
      break;
    case 'rules_updated':
      state.rules = ev.payload || [];
      $('rules-count').textContent = state.rules.filter(r => r.enabled).length;
      break;
  }
}

// ─── Rendering ──────────────────────────────────────────────────────
function renderAll() {
  $('upstream').textContent = `— upstream: ${state.upstream}`;
  $('intercept-toggle').checked = state.intercept;
  $('fallback-toggle').checked = !!state.fallback;
  $('fallback-model-label').textContent = shortModel(state.fallbackModel || 'claude-sonnet-4-6');
  $('rules-count').textContent = (state.rules || []).filter(r => r.enabled).length;
  $('captures').innerHTML = '';
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
  if (cap.status === 'aup_refused') li.classList.add('aup-refused');
  // fallback-saved only colors orange if the fallback actually returned 2xx
  if (cap.fallback_of) {
    const ok = (cap.response && cap.response.status >= 200 && cap.response.status < 300);
    li.classList.add(ok ? 'fallback-saved' : 'fallback-failed');
  }

  const method = cap.request?.method || '?';
  const path = cap.request?.path || cap.request?.url || '?';
  const status = cap.response?.status ? String(cap.response.status) : (cap.status || '—');

  // Top line: method + path + short status
  li.appendChild(el('span', { class: 'method' }, method));
  li.appendChild(el('span', { class: 'path', title: path }, summarizePath(cap)));

  let metaTop;
  if (cap.status === 'aup_refused')    metaTop = 'AUP';
  else if (cap.fallback_of)            metaTop = (cap.response?.status >= 200 && cap.response?.status < 300) ? `⤴ ${status}` : `⤴ ${status}`;
  else if (cap.response?.status)        metaTop = status;
  else                                  metaTop = cap.status?.slice(0,4) || '—';
  li.appendChild(el('span', { class: 'meta st-' + (cap.status || 'pending') }, metaTop));

  // Sub line: model/duration/markers — only if useful
  const subParts = [];
  if (cap.duration_ms != null && cap.duration_ms > 0) subParts.push(fmtMs(cap.duration_ms));
  const m = modelOfCapture(cap);
  if (m) subParts.push(shortModel(m));
  if (cap.modified) subParts.push('✎ modified');
  if (subParts.length) li.appendChild(el('span', { class: 'sub' }, subParts.join(' · ')));

  if (!existing) {
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
    cap.modified ? '· ✎ modified' : '',
    cap.replay_of ? `· ↻ replay of ${cap.replay_of}` : '',
    cap.fallback_of ? `· ⤴ fallback of ${cap.fallback_of}` : '',
    cap.fallback_to ? `· → fallback to ${cap.fallback_to}` : '',
  ].filter(Boolean).join(' ');

  $('req-line').textContent = `${cap.request?.method || ''} ${cap.request?.url || ''}`;
  renderHeaders($('req-headers'), cap.request?.headers || {}, 'req');
  $('req-headers-count').textContent = `(${Object.keys(cap.request?.headers || {}).length})`;
  const reqBody = cap.request?.body || '';
  $('req-body-info').textContent = reqBody ? `(${fmtBytes(reqBody.length)})` : '(empty)';
  // structured read-only view
  createBodyEditor('req-body-structured', reqBody, { readOnly: true });

  $('resp-line').textContent = cap.response
    ? `${cap.response.status}${cap.response.streaming ? ' · streaming SSE' : ''}`
    : (cap.error ? 'ERROR: ' + cap.error : '(no response yet)');
  renderHeaders($('resp-headers'), cap.response?.headers || {}, 'resp');
  $('resp-headers-count').textContent = `(${Object.keys(cap.response?.headers || {}).length})`;
  const respBody = cap.response?.body || (cap.response?.chunks || []).map(c => c.data).join('');
  $('resp-body-info').textContent = respBody ? `(${fmtBytes(respBody.length)}${cap.response?.streaming ? ', SSE' : ''})` : '(none)';
  if (cap.response?.streaming) {
    $('resp-body').innerHTML = highlightSSE(respBody);
  } else {
    $('resp-body').innerHTML = highlightJSON(tryPrettyJSON(respBody));
  }

  $('raw-json').innerHTML = highlightJSON(JSON.stringify(cap, null, 2));
}

function appendStreamChunk(chunk) {
  const cap = state.captures.get(state.selectedId);
  if (!cap || !cap.response?.streaming) return;
  if (document.querySelector('#tab-resp:not([hidden])')) {
    const respBody = (cap.response.chunks || []).map(c => c.data).join('');
    $('resp-body').innerHTML = highlightSSE(respBody);
    $('resp-body-info').textContent = `(${fmtBytes(respBody.length)}, SSE, ${cap.response.chunks.length} chunks)`;
  }
}

// Headers we consider noise by default — SDK plumbing, transport metadata.
// User can toggle to show them with "show all".
const NOISY_HEADERS = new Set([
  'accept', 'accept-encoding', 'connection', 'content-encoding',
  'content-length', 'content-type', 'cache-control',
  'cf-cache-status', 'cf-ray',
  'date', 'server', 'strict-transport-security', 'via', 'vary',
  'x-app', 'x-claude-code-session-id',
  'x-stainless-arch', 'x-stainless-lang', 'x-stainless-os',
  'x-stainless-package-version', 'x-stainless-retry-count',
  'x-stainless-runtime', 'x-stainless-runtime-version',
  'x-stainless-timeout',
  'x-robots-tag', 'x-should-retry',
  'set-cookie',
  'anthropic-dangerous-direct-browser-access',
  'user-agent',
]);
const SECRET_HEADERS = new Set(['authorization', 'x-api-key', 'cookie']);

// per-pane reveal toggles
const headerState = { req: { showAll: false, reveal: false }, resp: { showAll: false, reveal: false } };

function renderHeaders(tbl, headers, paneKey) {
  const wrap = tbl.parentElement; // host element where we render controls
  tbl.innerHTML = '';

  const allNames = Object.keys(headers).sort();
  const showAll = headerState[paneKey]?.showAll;
  const reveal = headerState[paneKey]?.reveal;
  const filtered = allNames.filter(k => showAll || !NOISY_HEADERS.has(k.toLowerCase()));
  const hiddenCount = allNames.length - filtered.length;

  // Inject control row at top of the section if not already there
  const controlsId = paneKey + '-headers-controls';
  let controls = document.getElementById(controlsId);
  if (!controls) {
    controls = el('div', { class: 'header-controls', id: controlsId });
    tbl.parentElement.insertBefore(controls, tbl);
  }
  controls.innerHTML = '';
  controls.appendChild(el('button', {
    class: 'btn-tiny',
    onclick: () => { headerState[paneKey].showAll = !headerState[paneKey].showAll; renderHeaders(tbl, headers, paneKey); },
  }, showAll ? `Hide ${hiddenCount} noisy` : (hiddenCount > 0 ? `Show all (+${hiddenCount})` : 'No hidden')));
  if (allNames.some(n => SECRET_HEADERS.has(n.toLowerCase()))) {
    controls.appendChild(el('button', {
      class: 'btn-tiny',
      onclick: () => { headerState[paneKey].reveal = !headerState[paneKey].reveal; renderHeaders(tbl, headers, paneKey); },
    }, reveal ? '🔒 Hide secrets' : '👁 Reveal secrets'));
  }

  for (const k of filtered) {
    const vs = headers[k] || [];
    const isSecret = SECRET_HEADERS.has(k.toLowerCase());
    for (const v of vs) {
      const displayed = (isSecret && !reveal) ? redact(v) : v;
      const cls = isSecret && !reveal ? 'v secret' : 'v';
      tbl.appendChild(el('tr', {}, el('td', { class: 'k' }, k), el('td', { class: cls }, displayed)));
    }
  }
}

function redact(v) {
  if (!v) return v;
  // Bearer xxxxx or sk-xxxxx — show prefix + ***
  const m = v.match(/^(Bearer\s+)?(\S{4,8})/);
  if (m) return `${m[1] || ''}${m[2]}…[redacted]`;
  return '[redacted]';
}

// ─── Detail tabs ───────────────────────────────────────────────────
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

$('fallback-toggle').addEventListener('change', async () => {
  const enabled = $('fallback-toggle').checked;
  const res = await fetch('/admin/fallback', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  });
  if (res.ok) {
    const data = await res.json();
    state.fallback = data.enabled;
    state.fallbackModel = data.model;
    toast(enabled ? `AUP fallback → ${shortModel(data.model)} ON` : 'AUP fallback OFF', 'ok');
  }
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

// ─── View tabs (Structured / Raw) ──────────────────────────────────
function setupViewTabs(prefix, getEditor, setEditor) {
  document.querySelectorAll(`.view-tabs[data-target="${prefix}"] .view-tab`).forEach(btn => {
    btn.addEventListener('click', () => {
      const view = btn.dataset.view;
      document.querySelectorAll(`.view-tabs[data-target="${prefix}"] .view-tab`)
        .forEach(b => b.classList.toggle('active', b === btn));
      const structuredEl = $(prefix + '-structured');
      const rawEl = $(prefix + '-raw');
      const rawTextarea = $(prefix + '-body');

      if (view === 'raw') {
        const cur = getEditor();
        rawTextarea.value = cur ? cur.getBody() : '';
        structuredEl.hidden = true;
        rawEl.hidden = false;
      } else {
        const newEditor = createBodyEditor(prefix + '-structured', rawTextarea.value);
        setEditor(newEditor);
        structuredEl.hidden = false;
        rawEl.hidden = true;
      }
    });
  });
}
setupViewTabs('im', () => imEditor, (e) => { imEditor = e; });
setupViewTabs('rm', () => rmEditor, (e) => { rmEditor = e; });

// ─── Intercept modal ────────────────────────────────────────────────
function openInterceptModal(cap) {
  if (state.interceptModalFor && state.interceptModalFor !== cap.id) {
    if (!state.interceptQueue.includes(cap.id)) state.interceptQueue.push(cap.id);
    return;
  }
  state.interceptModalFor = cap.id;
  $('im-id').textContent = `${cap.id} · ${cap.request.method} ${cap.request.path}`;
  $('im-url').value = cap.request.url || '';
  $('im-headers').value = JSON.stringify(cap.request.headers || {}, null, 2);
  $('im-headers-summary').textContent = `· ${Object.keys(cap.request.headers || {}).length} · ${fmtBytes(JSON.stringify(cap.request.headers || {}).length)}`;

  // Start in structured view
  document.querySelectorAll('.view-tabs[data-target="im"] .view-tab').forEach(b =>
    b.classList.toggle('active', b.dataset.view === 'structured'));
  $('im-structured').hidden = false;
  $('im-raw').hidden = true;
  imEditor = createBodyEditor('im-structured', cap.request.body || '');
  $('im-body').value = '';

  $('intercept-modal').hidden = false;
}

function closeInterceptModal() {
  state.interceptModalFor = null;
  $('intercept-modal').hidden = true;
  imEditor = null;
  while (state.interceptQueue.length > 0) {
    const nextId = state.interceptQueue.shift();
    const next = state.captures.get(nextId);
    if (next && next.status === 'intercepted') {
      openInterceptModal(next);
      return;
    }
  }
}

function readImBody() {
  if (!$('im-raw').hidden) return $('im-body').value;
  return imEditor ? imEditor.getBody() : '';
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
  const body = readImBody();
  try { JSON.parse(body); }
  catch (e) { toast('Invalid body JSON: ' + e.message, 'error'); return; }
  await postForward(id, $('im-url').value, headers, body);
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
  $('rm-headers-summary').textContent = `· ${Object.keys(cap.request.headers || {}).length} · ${fmtBytes(JSON.stringify(cap.request.headers || {}).length)}`;

  document.querySelectorAll('.view-tabs[data-target="rm"] .view-tab').forEach(b =>
    b.classList.toggle('active', b.dataset.view === 'structured'));
  $('rm-structured').hidden = false;
  $('rm-raw').hidden = true;
  rmEditor = createBodyEditor('rm-structured', cap.request.body || '');
  $('rm-body').value = '';

  $('replay-modal').dataset.fromId = cap.id;
  $('replay-modal').hidden = false;
}

$('rm-cancel').addEventListener('click', () => {
  $('replay-modal').hidden = true;
  rmEditor = null;
});

$('rm-send').addEventListener('click', async () => {
  const id = $('replay-modal').dataset.fromId;
  let headers;
  try { headers = JSON.parse($('rm-headers').value); }
  catch (e) { toast('Invalid headers JSON: ' + e.message, 'error'); return; }
  let body = !$('rm-raw').hidden ? $('rm-body').value : (rmEditor ? rmEditor.getBody() : '');
  try { JSON.parse(body); }
  catch (e) { toast('Invalid body JSON: ' + e.message, 'error'); return; }

  const res = await fetch(`/admin/captures/${id}/replay`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      method: $('rm-method').value,
      url: $('rm-url').value,
      headers,
      body,
    }),
  });
  if (!res.ok) { toast('Replay failed: ' + res.status, 'error'); return; }
  toast('Replay sent', 'ok');
  $('replay-modal').hidden = true;
  rmEditor = null;
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
    tbody.appendChild(el('tr', {},
      el('td', {}, el('input', {
        type: 'checkbox', checked: r.enabled ? true : undefined,
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
      }, '✕')),
    ));
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
    toast('Rule error: ' + (await res.text()), 'error');
    return;
  }
  state.rules = await res.json();
  $('rules-count').textContent = state.rules.filter(r => r.enabled).length;
  $('rules-modal').hidden = true;
  toast('Rules saved', 'ok');
});

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    if (!$('rules-modal').hidden) $('rules-modal').hidden = true;
    else if (!$('replay-modal').hidden) { $('replay-modal').hidden = true; rmEditor = null; }
  }
});

// ─── Boot ───────────────────────────────────────────────────────────
connect();

})();
