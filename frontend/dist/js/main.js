import { api, on, isDesktop } from './api.js';
import { Renderer, drawThumb, luminance, RULER } from './render.js';
import { icon } from './icons.js';
import { $, $$, esc, fmt, mm, toast, modal, prompt, contextMenu, numField, hydrateIcons } from './ui.js';
import { renderProps } from './panels.js';
import { openWorksheet, openSequenceGrid } from './worksheet.js';
import { openColorBlockPrint } from './print.js';
import { openFontGallery, fontOptions, loadFavourites } from './fontgallery.js';
import { openSettings } from './settings.js';

// ---------------------------------------------------------------- state
export const S = {
  view: null,
  sel: null,
  selBlock: null, // { id, index } when a colour block is picked in the list
  tab: 'design',
  propTab: 'transform',
  propsOpen: true,
  lock: true,
  showStitches: true,
  showJumps: false,
  tool: 'select',
  drag: null,
  measure: null,
  play: { pos: 0, playing: false, speed: 1, last: 0 },
  info: null,
  update: null,
};

const R = new Renderer($('#canvas'));
let needsDraw = true;
export const redraw = () => { needsDraw = true; };

export const selected = () => S.view && S.view.designs.find(d => d.id === S.sel);

// ---------------------------------------------------------------- apply view
// Guard against null lists from older backends / empty documents.
function normalize(v) {
  for (const k of ['designs', 'threads', 'hoops', 'fonts', 'sequence']) if (!Array.isArray(v[k])) v[k] = [];
  for (const d of v.designs) {
    if (!Array.isArray(d.blocks)) d.blocks = [];
    for (const b of d.blocks) if (!b.points) b.points = [];
  }
  return v;
}

// Send UI errors to debug.log (desktop) / the dev server console.
export function logError(msg) {
  try { api.LogClient('error', String(msg)); } catch { /* ignore */ }
}
window.addEventListener('error', e => logError(`${e.message} at ${e.filename}:${e.lineno}:${e.colno}\n${e.error && e.error.stack || ''}`));
window.addEventListener('unhandledrejection', e => logError('unhandled: ' + (e.reason && (e.reason.stack || e.reason.message) || e.reason)));

export function apply(view, opts = {}) {
  if (!view) return;
  normalize(view);
  const switched = !!S.view && view.tabId !== S.view.tabId; // another tab became active
  const wasAtEnd = switched || S.play.pos >= R.total;
  const firstLoad = !S.view || switched || (S.view.designs.length === 0 && view.designs.length > 0);
  if (switched) { S.sel = null; S.selBlock = null; }
  if (!Array.isArray(view.tabs)) view.tabs = [];
  S.view = view;
  R.setView(view);
  if (S.sel && !view.designs.some(d => d.id === S.sel)) S.sel = null;
  if (S.selBlock) {
    const d = view.designs.find(x => x.id === S.selBlock.id);
    if (!d || S.selBlock.index >= d.blocks.length) S.selBlock = null;
  }
  if (opts.select) S.sel = opts.select;
  if (!S.sel && firstLoad && view.designs.length) S.sel = view.designs[view.designs.length - 1].id;
  if (wasAtEnd || S.play.pos > R.total) S.play.pos = R.total;
  if (firstLoad) R.fit(view.hoop);
  renderTabs();
  renderSeq();
  if (!opts.keepProps) renderPropsPanel();
  renderPlayer();
  renderChrome();
  redraw();
}

// ---------------------------------------------------------------- recent files
async function renderRecentEmpty() {
  const box = $('#emptyRecent');
  if (!box) return;
  let list = [];
  try { list = await api.RecentFiles(); } catch { /* ignore */ }
  list = (list || []).slice(0, 6);
  box.innerHTML = list.length ? `<h4>Recent</h4>` + list.map((f, i) =>
    `<button data-i="${i}" class="${f.exists ? '' : 'gone'}" title="${esc(f.path)}"><span>${esc(f.name)}</span><span class="rf-dir">${esc(f.folder)}</span></button>`).join('') : '';
  box.querySelectorAll('[data-i]').forEach(b => b.onclick = () => openRecent(list[+b.dataset.i].path));
}

function openRecent(path) {
  return run(() => api.OpenRecent(path)).then(r => { if (r) { R.fit(S.view.hoop); updateZoomLabel(); redraw(); } });
}

async function recentMenu(x, y) {
  let list = [];
  try { list = await api.RecentFiles(); } catch { /* ignore */ }
  if (!list.length) { toast('No recent files yet'); return; }
  contextMenu(x, y, [
    ...list.map(f => ({ label: f.name + (f.exists ? '' : ' (missing)'), action: () => openRecent(f.path) })),
    '-',
    { label: 'Clear recent files', action: () => api.ClearRecent().then(() => renderRecentEmpty()) },
  ]);
}

// ---------------------------------------------------------------- quick send
export async function renderQuickSends() {
  const box = $('#quickSends');
  if (!box) return;
  let list = [];
  try { list = await api.QuickSends(); } catch { /* ignore */ }
  box.innerHTML = (list || []).map(q =>
    `<button class="btn btn-outline qs${q.connected ? '' : ' off'}" data-q="${esc(q.id)}" title="${esc(q.connected ? `Export DST straight to ${q.folder}` : `${q.folder} not found — plug in the stick`)}">${icon('export', 16)}Send to ${esc(q.name)}</button>`).join('');
  box.querySelectorAll('[data-q]').forEach(b => b.onclick = () => run(() => api.SendTo(b.dataset.q)).then(r => { if (r && r.message) toast(r.message); renderQuickSends(); }));
}

// ---------------------------------------------------------------- doc tabs
function renderTabs() {
  const bar = $('#docTabs');
  if (!bar) return;
  const v = S.view;
  bar.innerHTML = v.tabs.map(t => `<div class="doctab${t.id === v.tabId ? ' on' : ''}" role="tab" data-id="${esc(t.id)}" title="${esc(t.title)}">
      ${t.dirty ? '<span class="dt-dot" title="Unsaved changes"></span>' : ''}<span class="dt-name">${esc(t.title)}</span>
      <button class="dt-x" title="Close (Ctrl+W)">${icon('close', 14)}</button></div>`).join('') +
    `<button class="doctab-new" title="New design (Ctrl+N)">${icon('plus', 16)}</button>`;
  bar.querySelectorAll('.doctab').forEach(el => {
    const id = el.dataset.id;
    el.onclick = () => { if (id !== S.view.tabId) run(() => api.SwitchTab(id)); };
    el.onauxclick = e => { if (e.button === 1) run(() => api.CloseTab(id)); }; // middle-click closes
    el.querySelector('.dt-x').onclick = e => { e.stopPropagation(); run(() => api.CloseTab(id)); };
  });
  bar.querySelector('.doctab-new').onclick = () => actions.new();
  const on = bar.querySelector('.doctab.on');
  if (on) on.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}

export async function run(fn, okMsg) {
  try {
    const r = await fn();
    if (r && r.designs) apply(r);
    else if (r && r.view) {
      apply(r.view, { select: r.id });
      if (r.warning) toast(r.warning, 'warn');
      if (r.message) setStatus(r.message);
    }
    if (okMsg) setStatus(okMsg);
    return r;
  } catch (e) {
    toast(e.message || String(e), 'err');
    setStatus('Error: ' + (e.message || e));
    if (e && e.stack) logError(e.stack);
    return null;
  }
}

// runLive applies an edit made while the user is typing: the side panel is
// left alone (so the text box keeps focus) and problems go to the status bar.
export async function runLive(fn) {
  try {
    const r = await fn();
    if (r && r.view) apply(r.view, { select: r.id, keepProps: true });
    setStatus(r && r.warning ? r.warning : 'Ready');
    return r;
  } catch (e) {
    setStatus(e.message || String(e));
    return null;
  }
}

export function setStatus(msg) { $('#statusMsg').textContent = msg; }

function renderChrome() {
  const v = S.view;
  $('#btnUndo').disabled = !v.canUndo;
  $('#btnRedo').disabled = !v.canRedo;
  $('#statusMachine').textContent = v.settings.machine;
  $('#emptyState').hidden = v.designs.length > 0;
  if (v.designs.length === 0) renderRecentEmpty();
  const hs = $('#hoopSelect');
  if (hs.options.length !== v.hoops.length) hs.innerHTML = v.hoops.map(h => `<option value="${h.id}">${esc(h.name)}</option>`).join('');
  hs.value = v.hoop.id;
  document.title = (v.dirty ? '• ' : '') + (v.title || 'Untitled') + ' — TSG Embroidery Pro';
  $$('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === S.tab));
  $('.main').classList.toggle('no-props', !S.propsOpen);
}

// ---------------------------------------------------------------- sequence list
// m:ss (or h:mm:ss) for projected sewing time.
export function fmtTime(sec) {
  sec = Math.max(0, Math.round(sec));
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  return h ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`;
}

function blockTime(id, bi) {
  const ob = R.order.find(o => o.item.id === id && o.block.bi === bi);
  return ob ? ` <span class="muted btime">· ${fmtTime(ob.tDur)}</span>` : '';
}

function renderSeq() {
  const v = S.view;
  const list = $('#seqList');
  list.innerHTML = '';
  const tmap = new Map(v.threads.map(t => [t.id, t]));
  v.designs.forEach((d, di) => {
    const row = document.createElement('div');
    row.className = 'd-row' + (d.id === S.sel ? ' sel' : '');
    row.draggable = true;
    row.dataset.id = d.id;
    row.innerHTML = `
      <button class="eye ${d.visible ? '' : 'off'}" title="${d.visible ? 'Hide (not exported)' : 'Show'}">${icon(d.visible ? 'eye' : 'eyeOff', 20)}</button>
      <span class="grip">${icon('grip', 18)}</span>
      <span class="dthumb">${d.kind === 'lettering' ? '<span class="t-glyph">T</span>' : '<canvas></canvas>'}</span>
      <div><div class="d-name"><b>${String(di + 1).padStart(2, '0')}</b>${esc(d.name)}</div><div class="d-sub">${fmt(d.stitches)} stitches</div></div>
      <button class="icon-btn kebab" title="More">${icon('kebab', 18)}</button>`;
    const cv = row.querySelector('canvas');
    if (cv) drawThumb(cv, d, v.threads);
    row.onclick = e => { if (!e.target.closest('button')) select(d.id); };
    row.ondblclick = () => { if (d.kind === 'lettering') setTab('lettering'); };
    row.querySelector('.eye').onclick = () => run(() => api.SetVisible(d.id, !d.visible));
    row.querySelector('.kebab').onclick = e => { select(d.id); designMenu(d, e.clientX, e.clientY); };
    row.oncontextmenu = e => { e.preventDefault(); select(d.id); designMenu(d, e.clientX, e.clientY); };
    dnd(row, { kind: 'design', id: d.id, index: di });
    list.appendChild(row);
    d.blocks.forEach((b, bi) => {
      const t = tmap.get(b.thread) || { name: '?', color: '#888' };
      const br = document.createElement('div');
      const isSel = S.selBlock && S.selBlock.id === d.id && S.selBlock.index === bi;
      br.className = 'b-row' + (isSel ? ' sel' : '');
      br.title = 'Click to show this block on the canvas';
      br.draggable = true;
      br.innerHTML = `<span class="grip">${icon('grip', 18)}</span><span class="sw" style="background:${esc(t.color)}"></span>
        <span><span class="bnum">${b.index}</span> ${esc(t.name)} • ${fmt(b.stitches)}${blockTime(d.id, bi)}</span>`;
      br.onclick = () => (isSel ? select(d.id) : selectBlock(d.id, bi));
      br.oncontextmenu = e => { e.preventDefault(); selectBlock(d.id, bi); blockMenu(d, bi, e.clientX, e.clientY); };
      dnd(br, { kind: 'block', id: d.id, index: bi });
      list.appendChild(br);
    });
  });
  updateSeqButtons();
  const blocks = v.designs.reduce((a, d) => a + d.blocks.length, 0);
  $('#seqCount').textContent = `${blocks} block${blocks === 1 ? '' : 's'} • ${v.designs.length} design${v.designs.length === 1 ? '' : 's'}${R.total ? ' • ' + fmtTime(R.totalSec) : ''}`;
}

let dragSrc = null;
function dnd(el, info) {
  el.addEventListener('dragstart', e => { dragSrc = info; e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', ''); });
  el.addEventListener('dragover', e => {
    if (!dragSrc || dragSrc.kind !== info.kind || (info.kind === 'block' && dragSrc.id !== info.id)) return;
    e.preventDefault();
    el.classList.add('drop-above');
  });
  el.addEventListener('dragleave', () => el.classList.remove('drop-above'));
  el.addEventListener('drop', e => {
    e.preventDefault();
    el.classList.remove('drop-above');
    const src = dragSrc; dragSrc = null;
    if (!src || src.kind !== info.kind) return;
    if (info.kind === 'design') {
      const delta = info.index - src.index;
      if (delta) moveDesignBy(src.id, delta);
    } else if (src.id === info.id && src.index !== info.index) {
      run(() => api.MoveBlock(info.id, src.index, info.index));
    }
  });
  el.addEventListener('dragend', () => { dragSrc = null; $$('.drop-above').forEach(x => x.classList.remove('drop-above')); });
}

async function moveDesignBy(id, delta) {
  const step = delta > 0 ? 1 : -1;
  for (let i = 0; i !== delta; i += step) {
    const r = await run(() => api.MoveDesign(id, step));
    if (!r) break;
  }
}

function designMenu(d, x, y) {
  contextMenu(x, y, [
    { label: 'Rename…', action: async () => { const n = await prompt('Rename design', 'Name', d.name); if (n) run(() => api.Rename(d.id, n)); } },
    { label: 'Duplicate', kbd: 'Ctrl+D', action: () => run(() => api.Duplicate(d.id)) },
    { label: 'Snap start to previous design\'s end', action: () => run(() => api.SnapToPrevious(d.id), 'Connected: no jump or trim between designs') },
    { label: d.visible ? 'Hide' : 'Show', action: () => run(() => api.SetVisible(d.id, !d.visible)) },
    '-',
    { label: 'Rotate 90°', kbd: 'Ctrl+R', action: () => run(() => api.Rotate(d.id, 90)) },
    { label: 'Mirror horizontal', action: () => run(() => api.Mirror(d.id, false)) },
    { label: 'Mirror vertical', action: () => run(() => api.Mirror(d.id, true)) },
    { label: 'Center in hoop', action: () => run(() => api.Center(d.id)) },
    '-',
    ...(d.kind === 'lettering' ? [{ label: 'Edit lettering', action: () => setTab('lettering') }] : [
      { label: 'Combine same-colour blocks', action: () => run(() => api.CombineBlocks(d.id)) },
      { label: 'Remove small stitches…', action: () => actions.removeSmall() },
    ]),
    '-',
    { label: 'Delete', kbd: 'Del', danger: true, action: () => run(() => api.Delete(d.id)) },
  ]);
}

function blockMenu(d, bi, x, y) {
  const threads = S.view.threads;
  contextMenu(x, y, [
    { label: 'Reverse sew direction', action: () => run(() => api.ReverseBlock(d.id, bi)) },
    { label: 'Sew earlier', action: () => bi > 0 && run(() => api.MoveBlock(d.id, bi, bi - 1)) },
    { label: 'Sew later', action: () => bi < d.blocks.length - 1 && run(() => api.MoveBlock(d.id, bi, bi + 1)) },
    '-',
    ...threads.map(t => ({ label: `Thread: ${t.name} (needle ${t.needle})`, action: () => run(() => api.SetBlockThread(d.id, bi, t.id)) })),
  ]);
}

export function select(id) {
  S.sel = id;
  S.selBlock = null;
  if (id) S.propsOpen = true;
  renderSeq();
  renderPropsPanel();
  renderChrome();
  redraw();
}

// Pick one colour block: it stays bright on the canvas, everything else fades.
export function selectBlock(id, index) {
  S.sel = id;
  S.selBlock = { id, index };
  S.propsOpen = true;
  renderSeq();
  renderPropsPanel();
  renderChrome();
  redraw();
  const ob = R.order.find(o => o.item.id === id && o.block.bi === index);
  if (ob) setStatus(`Block ${ob.block.index}: ${ob.block.thread.name} — ${fmt(ob.block.count)} stitches`);
}

// Up/down move the selected block within its design, or the selected design.
function updateSeqButtons() {
  const v = S.view;
  let up = false, down = false, what = 'Select a design or block';
  if (S.selBlock) {
    const d = v.designs.find(x => x.id === S.selBlock.id);
    up = S.selBlock.index > 0;
    down = d && S.selBlock.index < d.blocks.length - 1;
    what = 'block';
  } else if (S.sel) {
    const i = v.designs.findIndex(x => x.id === S.sel);
    up = i > 0;
    down = i >= 0 && i < v.designs.length - 1;
    what = 'design';
  }
  $('#seqUp').disabled = !up;
  $('#seqDown').disabled = !down;
  $('#seqUp').title = what.startsWith('Select') ? what : `Sew this ${what} earlier`;
  $('#seqDown').title = what.startsWith('Select') ? what : `Sew this ${what} later`;
}

async function moveSel(dir) {
  if (S.selBlock) {
    const { id, index } = S.selBlock;
    const r = await run(() => api.MoveBlock(id, index, index + dir));
    if (r) selectBlock(id, index + dir);
  } else if (S.sel) {
    run(() => api.MoveDesign(S.sel, dir));
  }
}

export function renderPropsPanel() { renderProps($('#props')); }

export function setTab(tab) {
  S.tab = tab;
  S.propsOpen = true;
  renderChrome();
  renderPropsPanel();
  requestAnimationFrame(resize);
}

// ---------------------------------------------------------------- player
function renderPlayer() {
  const segs = $('#trackSegs');
  segs.innerHTML = '';
  const total = Math.max(R.total, 1);
  for (const ob of R.order) {
    const div = document.createElement('div');
    div.style.width = (ob.block.count / total) * 100 + '%';
    div.style.background = ob.block.color;
    if (luminance(ob.block.color) > 0.85) div.className = 'white';
    segs.appendChild(div);
  }
  updatePlayhead();
}

function updatePlayhead() {
  const total = R.total;
  const pos = Math.min(S.play.pos, total);
  const pct = total ? (pos / total) * 100 : 0;
  $('#thumb').style.left = pct + '%';
  let mask = $('#track .mask');
  if (!mask) { mask = document.createElement('div'); mask.className = 'mask'; $('#track').appendChild(mask); }
  mask.style.left = pct + '%';
  $('#pCount').innerHTML = `${fmt(Math.round(pos))} / ${fmt(total)}<span class="ptime">${fmtTime(R.timeAt(pos))} / ${fmtTime(R.totalSec)}</span>`;
  $('#pCount').title = 'Stitches sewn / total · projected time elapsed / total (at the SPM, trim and colour-change times on the Production tab)';
  $('#pPlay').innerHTML = icon(S.play.playing ? 'pause' : 'play', 26);
}

function setPlay(pos, playing = S.play.playing) {
  S.play.pos = Math.max(0, Math.min(pos, R.total));
  S.play.playing = playing && S.play.pos < R.total;
  updatePlayhead();
  redraw();
}

function blockEdges() { return R.order.map(o => o.start).concat([R.total]); }

$('#pFirst').onclick = () => setPlay(0, false);
$('#pLast').onclick = () => setPlay(R.total, false);
$('#pPrev').onclick = () => { const e = blockEdges(); setPlay([...e].reverse().find(x => x < S.play.pos - 1) ?? 0); };
$('#pNext').onclick = () => { const e = blockEdges(); setPlay(e.find(x => x > S.play.pos + 1) ?? R.total); };
$('#pPlay').onclick = togglePlay;
$('#pSpeed').onchange = e => { S.play.speed = +e.target.value; };
function togglePlay() {
  if (!S.play.playing && S.play.pos >= R.total) S.play.pos = 0;
  S.play.playing = !S.play.playing;
  S.play.last = performance.now();
  updatePlayhead();
}
{
  const track = $('#track');
  const scrub = e => {
    const r = track.getBoundingClientRect();
    setPlay(((e.clientX - r.left) / r.width) * R.total, false);
  };
  track.addEventListener('pointerdown', e => { track.setPointerCapture(e.pointerId); scrub(e); track.onpointermove = scrub; });
  track.addEventListener('pointerup', () => { track.onpointermove = null; });
}

// ---------------------------------------------------------------- canvas interaction
const wrap = $('#canvasWrap');
const cv = $('#canvas');
let spaceDown = false;

function resize() { R.resize(); redraw(); }
new ResizeObserver(resize).observe(wrap);

function localXY(e) { const r = cv.getBoundingClientRect(); return [e.clientX - r.left, e.clientY - r.top]; }

function hitHandle(sx, sy) {
  if (!S.sel || S.tool !== 'select') return null;
  const r = R.selRect(S, S.sel);
  if (!r) return null;
  const h = R.handles(r);
  if (Math.hypot(sx - h.rot[0], sy - h.rot[1]) < 9) return { kind: 'rotate', h };
  for (const [name, x, y] of h.list) if (Math.abs(sx - x) < 7 && Math.abs(sy - y) < 7) return { kind: 'scale', name, h };
  return null;
}

const cursorFor = { nw: 'nwse-resize', se: 'nwse-resize', ne: 'nesw-resize', sw: 'nesw-resize', n: 'ns-resize', s: 'ns-resize', e: 'ew-resize', w: 'ew-resize' };

cv.addEventListener('pointerdown', e => {
  const [sx, sy] = localXY(e);
  if (sx < RULER || sy < RULER) return;
  cv.setPointerCapture(e.pointerId);
  const [wx, wy] = R.toWorld(sx, sy);
  if (e.button === 1 || spaceDown || S.tool === 'pan') {
    S.drag = { type: 'pan', sx, sy, px: R.panX, py: R.panY };
    return;
  }
  if (S.tool === 'measure') { S.measure = { x0: wx, y0: wy, x1: wx, y1: wy }; S.drag = { type: 'measure' }; redraw(); return; }
  const hh = hitHandle(sx, sy);
  const d = selected();
  if (hh && d) {
    const b = d.bounds;
    if (hh.kind === 'rotate') {
      const cx = (b.MinX + b.MaxX) / 2, cy = (b.MinY + b.MaxY) / 2;
      S.drag = { type: 'rotate', id: d.id, cx, cy, a0: Math.atan2(wy - cy, wx - cx), angle: 0, base: d.transform.rotation };
    } else {
      const n = hh.name;
      const ax = n.includes('w') ? b.MaxX : n.includes('e') ? b.MinX : (b.MinX + b.MaxX) / 2;
      const ay = n.includes('n') ? b.MaxY : n.includes('s') ? b.MinY : (b.MinY + b.MaxY) / 2;
      S.drag = { type: 'scale', id: d.id, name: n, ax, ay, sx0: wx, sy0: wy, fx: 1, fy: 1, b };
    }
    return;
  }
  const id = R.hitDesign(sx, sy);
  if (id) {
    if (id !== S.sel) select(id);
    S.drag = { type: 'move', id, x0: wx, y0: wy, dx: 0, dy: 0 };
  } else {
    select(null);
  }
});

cv.addEventListener('pointermove', e => {
  const [sx, sy] = localXY(e);
  const [wx, wy] = R.toWorld(sx, sy);
  const d = S.drag;
  if (!d) {
    const hh = hitHandle(sx, sy);
    cv.style.cursor = S.tool === 'pan' || spaceDown ? 'grab' : S.tool === 'measure' ? 'crosshair'
      : hh ? (hh.kind === 'rotate' ? 'alias' : cursorFor[hh.name]) : R.hitDesign(sx, sy) ? 'move' : 'default';
    return;
  }
  if (d.type === 'pan') { R.panX = d.px + sx - d.sx; R.panY = d.py + sy - d.sy; }
  else if (d.type === 'measure') { S.measure.x1 = wx; S.measure.y1 = wy; }
  else if (d.type === 'move') { d.dx = wx - d.x0; d.dy = wy - d.y0; }
  else if (d.type === 'rotate') {
    let a = (Math.atan2(wy - d.cy, wx - d.cx) - d.a0) * 180 / Math.PI;
    a = e.shiftKey ? Math.round(a / 15) * 15 : Math.round(a);
    d.angle = a;
    setStatus(`Rotation ${((d.base + a) % 360 + 360) % 360}°`);
  } else if (d.type === 'scale') {
    let fx = (wx - d.ax) / ((d.sx0 - d.ax) || 1);
    let fy = (wy - d.ay) / ((d.sy0 - d.ay) || 1);
    const corner = d.name.length === 2;
    if (d.name === 'n' || d.name === 's') fx = S.lock ? fy : 1;
    if (d.name === 'e' || d.name === 'w') fy = S.lock ? fx : 1;
    if (corner && (S.lock !== e.shiftKey)) { const f = Math.max(fx, fy); fx = fy = f; }
    d.fx = Math.max(0.05, fx); d.fy = Math.max(0.05, fy);
    const sel = selected();
    if (sel && sel.kind !== 'lettering') {
      setStatus(`Scale ${(sel.scalePct * d.fx).toFixed(1)}%`);
    }
  }
  redraw();
});

cv.addEventListener('pointerup', async () => {
  const d = S.drag;
  S.drag = null;
  if (!d) return;
  if (d.type === 'move' && (Math.abs(d.dx) > 0.5 || Math.abs(d.dy) > 0.5)) {
    S.drag = d; // keep preview until the new view arrives
    await run(() => api.MoveBy([d.id], d.dx, d.dy));
    S.drag = null;
  } else if (d.type === 'rotate' && d.angle) {
    S.drag = d;
    await run(() => api.SetRotation(d.id, d.base + d.angle));
    S.drag = null;
  } else if (d.type === 'scale' && (Math.abs(d.fx - 1) > 0.001 || Math.abs(d.fy - 1) > 0.001)) {
    const b = d.b;
    const nx0 = d.ax + (b.MinX - d.ax) * d.fx, nx1 = d.ax + (b.MaxX - d.ax) * d.fx;
    const ny0 = d.ay + (b.MinY - d.ay) * d.fy, ny1 = d.ay + (b.MaxY - d.ay) * d.fy;
    S.drag = d;
    await run(() => api.SetBox(d.id, (nx0 + nx1) / 2, (ny0 + ny1) / 2, Math.abs(nx1 - nx0), Math.abs(ny1 - ny0), false));
    S.drag = null;
  }
  redraw();
});

cv.addEventListener('wheel', e => {
  e.preventDefault();
  const [sx, sy] = localXY(e);
  if (e.shiftKey) { R.panX -= e.deltaY; }
  else R.zoomAt(Math.exp(-e.deltaY * 0.0015), sx, sy);
  updateZoomLabel();
  redraw();
}, { passive: false });

cv.addEventListener('dblclick', e => {
  const [sx, sy] = localXY(e);
  const id = R.hitDesign(sx, sy);
  const d = S.view.designs.find(x => x.id === id);
  if (d && d.kind === 'lettering') setTab('lettering');
});

function updateZoomLabel() { $('#zoomLabel').textContent = Math.round(R.zoom * 100) + '%'; }
$('#zoomIn').onclick = () => { R.zoomAt(1.25, R.ox, R.oy); updateZoomLabel(); redraw(); };
$('#zoomOut').onclick = () => { R.zoomAt(0.8, R.ox, R.oy); updateZoomLabel(); redraw(); };
$('#zoomFit').onclick = () => { R.fit(S.view.hoop); updateZoomLabel(); redraw(); };

$$('.tool').forEach(b => b.onclick = () => setTool(b.dataset.tool));
function setTool(t) {
  S.tool = t;
  if (t !== 'measure') S.measure = null;
  $$('.tool').forEach(x => x.classList.toggle('on', x.dataset.tool === t));
  wrap.classList.toggle('pan', t === 'pan');
  wrap.classList.toggle('measure', t === 'measure');
  redraw();
}

$('#segStitches').onclick = () => { S.showStitches = !S.showStitches; $('#segStitches').classList.toggle('on', S.showStitches); redraw(); };
$('#segJumps').onclick = () => { S.showJumps = !S.showJumps; $('#segJumps').classList.toggle('on', S.showJumps); redraw(); };
$('#hoopSelect').onchange = e => run(() => api.SetSettings({ ...S.view.settings, hoop: e.target.value })).then(() => { R.fit(S.view.hoop); updateZoomLabel(); redraw(); });

$('#seqUp').onclick = () => moveSel(-1);
$('#seqDown').onclick = () => moveSel(1);
$$('.tab').forEach(t => t.onclick = () => setTab(t.dataset.tab));

// ---------------------------------------------------------------- actions
const sel = () => S.sel;
const needSel = () => { if (!S.sel) { toast('Select a design first'); return false; } return true; };

export const actions = {
  new: () => run(() => api.NewDocument()),
  closeTab: () => run(() => api.CloseTab(S.view.tabId)),
  nextTab: (dir = 1) => {
    const t = S.view.tabs, i = t.findIndex(x => x.id === S.view.tabId);
    if (t.length > 1) run(() => api.SwitchTab(t[(i + dir + t.length) % t.length].id));
  },
  open: () => run(() => api.Open()).then(r => { if (r) { R.fit(S.view.hoop); updateZoomLabel(); redraw(); } }),
  merge: () => run(() => api.Merge()),
  save: () => run(() => api.Save(), 'Project saved'),
  saveAs: () => run(() => api.SaveAs(), 'Project saved'),
  export: async () => {
    try {
      const p = await api.ExportDST();
      if (p) { toast('Exported ' + p); setStatus('Exported ' + p); }
    } catch (e) { toast(e.message, 'err'); }
  },
  worksheet: () => openWorksheet(),
  grid: () => openSequenceGrid(),
  printBlocks: () => openColorBlockPrint(),
  quit: () => window.runtime && window.runtime.Quit && window.runtime.Quit(),
  undo: () => run(() => api.Undo()),
  redo: () => run(() => api.Redo()),
  duplicate: () => needSel() && run(() => api.Duplicate(sel())),
  delete: () => needSel() && run(() => api.Delete(sel())),
  rotate: () => needSel() && run(() => api.Rotate(sel(), 90)),
  mirror: () => needSel() && run(() => api.Mirror(sel(), false)),
  mirrorV: () => needSel() && run(() => api.Mirror(sel(), true)),
  center: () => run(() => api.Center(S.sel || '')),
  centerAll: () => run(() => api.Center('')),
  lettering: () => {
    setTab('lettering');
    setTimeout(() => { const i = $('#lt-text'); if (i) { i.focus(); i.select(); } }, 0);
  },
  quickText: async () => {
    const v = S.view;
    await loadFavourites();
    const startFont = (S.lastSpec && S.lastSpec.font) || 'Block';
    const fonts = fontOptions(v.fonts, startFont, !!S.showWinFonts);
    let align = (S.lastSpec && S.lastSpec.align) || 'center';
    const setFont = (b, name) => {
      const f = v.fonts.find(x => x.name === name);
      b.querySelector('#q-font').value = name;
      if (f && f.native) b.querySelector('#q-h').value = (f.native / 10).toFixed(1); // digitized fonts sew best at their own size
    };
    const spec = await modal({
      title: 'Add lettering',
      body: `<div class="form">
        <label class="f">Text <span class="muted">(Enter for a new line)</span><textarea class="inp" id="q-text" rows="3" placeholder="e.g. ANDREW" maxlength="400"></textarea></label>
        <label class="f">Font<div class="font-row"><select class="inp" id="q-font">${fonts}</select>
          <button type="button" class="btn btn-ghost btn-sm" id="q-browse" style="height:38px">${icon('grid', 15)} Browse…</button></div>
          <span class="check sm"><input type="checkbox" id="q-win" ${S.showWinFonts ? 'checked' : ''}> Include Windows fonts</span></label>
        <div class="two">
          <label class="f">Cap height (mm)<input class="inp" id="q-h" type="number" min="3" max="300" step="0.5" value="18"></label>
          <label class="f">Align<div class="seg lt-align"><button type="button" data-a="left">Left</button><button type="button" data-a="center">Centre</button><button type="button" data-a="right">Right</button></div></label>
        </div>
        </div>`,
      onOpen: b => {
        setFont(b, startFont);
        const ab = b.querySelectorAll('.lt-align button');
        const showAlign = () => ab.forEach(x => x.classList.toggle('on', x.dataset.a === align));
        ab.forEach(x => x.onclick = e => { e.preventDefault(); align = x.dataset.a; showAlign(); });
        showAlign();
        b.querySelector('#q-win').onchange = e => {
          S.showWinFonts = e.target.checked;
          const sel = b.querySelector('#q-font'), cur = sel.value;
          sel.innerHTML = fontOptions(v.fonts, cur, S.showWinFonts);
          sel.value = cur;
        };
        b.querySelector('#q-font').onchange = e => setFont(b, e.target.value);
        b.querySelector('#q-browse').onclick = async () => {
          const f = await openFontGallery({ text: b.querySelector('#q-text').value.split('\n')[0], current: b.querySelector('#q-font').value });
          if (f) {
            const sel = b.querySelector('#q-font');
            if (![...sel.options].some(o => o.value === f.name)) sel.innerHTML = fontOptions(v.fonts, f.name, S.showWinFonts);
            setFont(b, f.name);
          }
          b.querySelector('#q-text').focus();
        };
      },
      buttons: [{ label: 'Cancel', value: null }, {
        label: 'Add', kind: 'primary',
        value: b => ({ text: b.querySelector('#q-text').value, font: b.querySelector('#q-font').value, height: parseFloat(b.querySelector('#q-h').value) * 10, spacing: 0, weight: 17, density: 4, align }),
      }],
    });
    if (!spec) return;
    if (!spec.text.trim()) { toast('Enter some text'); return; }
    S.lastSpec = spec;
    const r = await run(() => api.AddLettering(spec));
    if (r) { R.fit(S.view.hoop); updateZoomLabel(); setTab('lettering'); }
  },
  production: () => setTab('production'),
  zoomIn: () => $('#zoomIn').click(),
  zoomOut: () => $('#zoomOut').click(),
  fit: () => $('#zoomFit').click(),
  toggleJumps: () => $('#segJumps').click(),
  combine: () => needSel() && run(() => api.CombineBlocks(sel())),
  removeSmall: async () => {
    const v = await prompt('Remove small stitches', `Remove stitches shorter than (mm) — ${S.sel ? 'selected design' : 'all designs'}`, (S.view.settings.minStitch / 10).toFixed(1), 'number', 'step="0.1" min="0.1" max="2"');
    if (v) run(() => api.RemoveSmallStitches(S.sel || '', parseFloat(v) * 10));
  },
  checkUpdates: () => checkUpdates(true),
  logs: () => api.OpenLogFolder().catch(e => toast(e.message, 'err')),
  settings: () => openSettings(),
  recent: () => { const b = $('#recentBtn').getBoundingClientRect(); recentMenu(b.left, b.bottom + 4); },
  github: () => api.OpenRepoPage(),
  about: async () => {
    const i = S.info || {};
    modal({
      title: 'About TSG Embroidery Pro',
      body: `<p><b>TSG Embroidery Pro</b> ${esc(i.version)}${i.commit ? ` (${esc(i.commit.slice(0, 7))})` : ''}</p>
        <p class="hint">View, merge, resize and letter Tajima DST files.<br>${esc(i.os)}/${esc(i.arch)} · github.com/${esc(i.repo)}</p>
        <p class="hint">Glyph-library fonts folder:<br><code>${esc(i.fontDir)}</code></p>
        <p class="hint">Debug log:<br><code>${esc(i.logPath || 'not available')}</code></p>`,
      buttons: [{ label: 'Open log folder', value: 'log' }, { label: 'GitHub', value: 'gh' }, { label: 'Close', kind: 'primary', value: null }],
    }).then(v => { if (v === 'gh') api.OpenRepoPage(); if (v === 'log') actions.logs(); });
  },
};

$$('[data-act]').forEach(b => b.addEventListener('click', () => actions[b.dataset.act] && actions[b.dataset.act]()));
$('#recentBtn').onclick = e => { e.stopPropagation(); actions.recent(); };

on('menu', a => actions[a] && actions[a]());
on('state:changed', () => api.State().then(apply));
// ---------------------------------------------------------------- drag & drop
// Drop files anywhere to open each in its own tab; drop them on the Designs
// list to merge into the current design instead.
const DESIGN_RE = /\.(dst|pes|pec|jef|exp|tep)$/i;
async function handleDrop(paths, merge) {
  for (const p of paths || []) {
    if (/\.emb$/i.test(p)) { toast('EMB is Wilcom\'s own format and can\'t be read. Open it in Wilcom (or the free Wilcom TrueSizer) and save it as DST.', 'warn'); continue; }
    if (!DESIGN_RE.test(p)) { toast(`Can't open ${p.split(/[\\/]/).pop()}: not a design file`, 'warn'); continue; }
    const asMerge = merge && !/\.tep$/i.test(p) && S.view.designs.length > 0;
    const r = await run(() => api.OpenPath(p, !asMerge));
    if (r && !asMerge) { R.fit(S.view.hoop); updateZoomLabel(); redraw(); }
  }
}
const overSeq = (x, y) => { const el = document.elementFromPoint(x, y); return !!(el && el.closest('.panel.seq')); };
if (window.runtime && window.runtime.OnFileDrop) {
  window.runtime.OnFileDrop((x, y, paths) => { dropHint(false); handleDrop(paths, overSeq(x, y)); }, false);
}
// drop hint overlay
let hintTimer = null;
function dropHint(on, merge) {
  const h = $('#dropHint');
  if (!h) return;
  h.hidden = !on;
  if (on) h.textContent = merge ? 'Drop to merge into this design' : 'Drop to open (drop on the Designs list to merge)';
}
window.addEventListener('dragover', e => {
  if (!e.dataTransfer || !e.dataTransfer.types.includes('Files')) return;
  e.preventDefault();
  dropHint(true, overSeq(e.clientX, e.clientY) && S.view && S.view.designs.length > 0);
  clearTimeout(hintTimer);
  hintTimer = setTimeout(() => dropHint(false), 300);
});
window.addEventListener('drop', e => {
  dropHint(false);
  if (!isDesktop && e.dataTransfer && e.dataTransfer.types.includes('Files')) e.preventDefault(); // browser dev server: no file paths
});
on('update:available', rel => showUpdate(rel));
on('fonts:changed', () => api.State().then(v => apply(v)));
on('watch:opened', name => toast(`Opened new download: ${name}`));
on('watch:many', d => toast(`${d.count} new DST files arrived together in ${d.dir} — open the one you need with Open.`, 'warn'));
on('watch:error', msg => toast(`Couldn't open the new file: ${msg}`, 'err'));

// ---------------------------------------------------------------- keyboard
document.addEventListener('keydown', e => {
  const typing = /INPUT|SELECT|TEXTAREA/.test(document.activeElement.tagName);
  if (e.code === 'Space' && !typing) { e.preventDefault(); if (!spaceDown && !e.repeat) { if (S.tool === 'select' && !e.shiftKey) togglePlay(); } spaceDown = true; return; }
  if (e.ctrlKey && e.key === 'Tab') { e.preventDefault(); actions.nextTab(e.shiftKey ? -1 : 1); return; }
  if (typing) return;
  const ctrl = e.ctrlKey || e.metaKey;
  if (!isDesktop && ctrl) { // the desktop app handles these through native menu accelerators
    const map = { ',': 'settings', w: 'closeTab', n: 'new', o: 'open', m: 'merge', s: e.shiftKey ? 'saveAs' : 'save', e: 'export', p: e.shiftKey ? 'printBlocks' : 'worksheet', z: 'undo', y: 'redo', d: 'duplicate', r: 'rotate', t: 'lettering', j: 'toggleJumps', g: 'grid', '0': 'fit', '=': 'zoomIn', '-': 'zoomOut' };
    const a = map[e.key.toLowerCase()];
    if (a) { e.preventDefault(); actions[a](); return; }
  }
  if (e.key === 'Delete' || e.key === 'Backspace') { if (S.sel) actions.delete(); return; }
  if (e.key === 'Escape') { S.measure = null; select(null); return; }
  const nud = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key];
  if (nud && S.sel) { e.preventDefault(); const k = e.shiftKey ? 100 : 10; run(() => api.MoveBy([S.sel], nud[0] * k, nud[1] * k)); return; }
  if (!ctrl) {
    const t = { v: 'select', n: 'node', h: 'pan', m: 'measure' }[e.key.toLowerCase()];
    if (t) setTool(t);
  }
});
document.addEventListener('keyup', e => { if (e.code === 'Space') spaceDown = false; });

// ---------------------------------------------------------------- dev-mode menu
const MENUS = [
  ['File', [['New', 'new', 'Ctrl+N'], ['Close tab', 'closeTab', 'Ctrl+W'], ['Open…', 'open', 'Ctrl+O'], ['Open recent…', 'recent'], ['Merge…', 'merge', 'Ctrl+M'], '-', ['Save project', 'save', 'Ctrl+S'], ['Save project as…', 'saveAs', 'Ctrl+Shift+S'], ['Export DST…', 'export', 'Ctrl+E'], '-', ['Production worksheet…', 'worksheet', 'Ctrl+P'], ['Print colour block analysis…', 'printBlocks', 'Ctrl+Shift+P']]],
  ['Edit', [['Undo', 'undo', 'Ctrl+Z'], ['Redo', 'redo', 'Ctrl+Y'], '-', ['Duplicate', 'duplicate', 'Ctrl+D'], ['Delete', 'delete', 'Del']]],
  ['View', [['Zoom in', 'zoomIn', 'Ctrl+='], ['Zoom out', 'zoomOut', 'Ctrl+-'], ['Fit to hoop', 'fit', 'Ctrl+0'], ['Toggle jumps', 'toggleJumps', 'Ctrl+J'], '-', ['Sequence grid', 'grid', 'Ctrl+G']]],
  ['Design', [['Rotate 90°', 'rotate', 'Ctrl+R'], ['Mirror horizontal', 'mirror'], ['Mirror vertical', 'mirrorV'], ['Center selection', 'center'], ['Center all in hoop', 'centerAll'], '-', ['Add lettering', 'lettering', 'Ctrl+T']]],
  ['Tools', [['Remove small stitches…', 'removeSmall'], ['Combine same-colour blocks', 'combine'], ['Machine settings…', 'production'], '-', ['Settings…', 'settings', 'Ctrl+,']]],
  ['Help', [['Check for updates…', 'checkUpdates'], ['Open debug log folder', 'logs'], ['Project on GitHub', 'github'], ['About', 'about']]],
];
if (!isDesktop) {
  const nav = $('#devmenu');
  nav.hidden = false;
  nav.innerHTML = MENUS.map(([name], i) => `<div><button data-m="${i}">${name}</button></div>`).join('');
  nav.querySelectorAll('[data-m]').forEach(b => b.onclick = () => {
    const r = b.getBoundingClientRect();
    contextMenu(r.left, r.bottom + 2, MENUS[+b.dataset.m][1].map(it => it === '-' ? '-' : { label: it[0], kbd: it[2], action: () => actions[it[1]]() }));
  });
}

// ---------------------------------------------------------------- updates
async function checkUpdates(manual) {
  try {
    const rel = await api.CheckForUpdates();
    if (rel) showUpdate(rel);
    else if (manual) toast(S.info && S.info.version === 'dev' ? 'Development build — update checks are disabled.' : 'You are on the latest version.');
  } catch (e) { if (manual) toast('Update check failed: ' + e.message, 'err'); }
}

function showUpdate(rel) {
  S.update = rel;
  const bar = $('#updateBar');
  bar.hidden = false;
  bar.innerHTML = `${icon('update', 18)}<span><b>${esc(rel.version)}</b> is available (you have ${esc(S.info ? S.info.version : '')}).</span>
    ${rel.canInstall ? '<button class="btn btn-primary btn-sm" data-u="install">Install &amp; restart</button>' : '<button class="btn btn-primary btn-sm" data-u="page">Download</button>'}
    <a data-u="notes">What’s new</a><span class="spacer"></span><a data-u="later">Later</a>`;
  bar.querySelector('[data-u=later]').onclick = () => { bar.hidden = true; };
  bar.querySelector('[data-u=notes]').onclick = () => modal({ title: `What’s new in ${rel.version}`, body: `<pre class="hint" style="white-space:pre-wrap">${esc(rel.notes || 'No release notes.')}</pre>`, buttons: [{ label: 'Close', kind: 'primary', value: null }] });
  const page = bar.querySelector('[data-u=page]');
  if (page) page.onclick = () => api.OpenReleasePage();
  const inst = bar.querySelector('[data-u=install]');
  if (inst) inst.onclick = async () => {
    inst.disabled = true; inst.textContent = 'Downloading…';
    try { await api.InstallUpdate(); } catch (e) { toast(e.message, 'err'); inst.disabled = false; inst.textContent = 'Install & restart'; }
  };
  if (!rel.canInstall && rel.reason) bar.title = rel.reason;
}

// ---------------------------------------------------------------- loop
function frame(t) {
  if (S.play.playing) {
    const dt = Math.min((t - S.play.last) / 1000, 0.1);
    S.play.last = t;
    S.play.pos = Math.min(S.play.pos + dt * 250 * S.play.speed, R.total);
    if (S.play.pos >= R.total) S.play.playing = false;
    updatePlayhead();
    needsDraw = true;
  }
  if (needsDraw && S.view) {
    needsDraw = false;
    R.draw({ ...S, playPos: S.play.pos, hoop: S.view.hoop });
  }
  requestAnimationFrame(frame);
}

// ---------------------------------------------------------------- boot
hydrateIcons();
(async () => {
  try {
    S.info = await api.Info();
    const v = await api.State();
    R.resize();
    apply(v);
    R.fit(v.hoop);
    updateZoomLabel();
    setStatus('Ready');
    renderQuickSends();
    setInterval(renderQuickSends, 5000); // stick plugged in / pulled out
  } catch (e) {
    toast('Failed to start: ' + e.message, 'err');
    logError('startup: ' + (e.stack || e.message));
  }
  requestAnimationFrame(frame);
})();

if (!isDesktop) window.__dbg = { R, S }; // test hook (browser dev server only)
export { R, updateZoomLabel };
