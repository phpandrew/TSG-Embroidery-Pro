import { api, on, isDesktop } from './api.js';
import { Renderer, drawThumb, luminance, RULER } from './render.js';
import { icon } from './icons.js';
import { $, $$, esc, fmt, mm, toast, modal, prompt, contextMenu, numField, hydrateIcons } from './ui.js';
import { renderProps } from './panels.js';
import { openWorksheet, openSequenceGrid } from './worksheet.js';
import { openColorBlockPrint } from './print.js';

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
  const wasAtEnd = S.play.pos >= R.total;
  const firstLoad = !S.view || (S.view.designs.length === 0 && view.designs.length > 0);
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
  renderSeq();
  renderPropsPanel();
  renderPlayer();
  renderChrome();
  redraw();
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

export function setStatus(msg) { $('#statusMsg').textContent = msg; }

function renderChrome() {
  const v = S.view;
  $('#btnUndo').disabled = !v.canUndo;
  $('#btnRedo').disabled = !v.canRedo;
  $('#statusMachine').textContent = v.settings.machine;
  $('#emptyState').hidden = v.designs.length > 0;
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
    const fonts = v.fonts.map(f => `<option value="${esc(f.name)}">${esc(f.name)}</option>`).join('');
    const spec = await modal({
      title: 'Add lettering',
      body: `<div class="form">
        <label class="f">Text<input class="inp" id="q-text" placeholder="e.g. ANDREW" maxlength="200"></label>
        <div class="two">
          <label class="f">Font<select class="inp" id="q-font">${fonts}</select></label>
          <label class="f">Cap height (mm)<input class="inp" id="q-h" type="number" min="3" max="300" step="0.5" value="18"></label>
        </div></div>`,
      buttons: [{ label: 'Cancel', value: null }, {
        label: 'Add', kind: 'primary',
        value: b => ({ text: b.querySelector('#q-text').value, font: b.querySelector('#q-font').value, height: parseFloat(b.querySelector('#q-h').value) * 10, spacing: 0, weight: 17, density: 4 }),
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

on('menu', a => actions[a] && actions[a]());
on('state:changed', () => api.State().then(apply));
on('files:dropped', async paths => {
  for (const p of paths || []) {
    const replace = S.view.designs.length === 0 && /\.(dst|tep)$/i.test(p);
    await run(() => api.OpenPath(p, replace));
  }
});
on('update:available', rel => showUpdate(rel));

// ---------------------------------------------------------------- keyboard
document.addEventListener('keydown', e => {
  const typing = /INPUT|SELECT|TEXTAREA/.test(document.activeElement.tagName);
  if (e.code === 'Space' && !typing) { e.preventDefault(); if (!spaceDown && !e.repeat) { if (S.tool === 'select' && !e.shiftKey) togglePlay(); } spaceDown = true; return; }
  if (typing) return;
  const ctrl = e.ctrlKey || e.metaKey;
  if (!isDesktop && ctrl) { // the desktop app handles these through native menu accelerators
    const map = { n: 'new', o: 'open', m: 'merge', s: e.shiftKey ? 'saveAs' : 'save', e: 'export', p: e.shiftKey ? 'printBlocks' : 'worksheet', z: 'undo', y: 'redo', d: 'duplicate', r: 'rotate', t: 'lettering', j: 'toggleJumps', g: 'grid', '0': 'fit', '=': 'zoomIn', '-': 'zoomOut' };
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
  ['File', [['New', 'new', 'Ctrl+N'], ['Open…', 'open', 'Ctrl+O'], ['Merge…', 'merge', 'Ctrl+M'], '-', ['Save project', 'save', 'Ctrl+S'], ['Save project as…', 'saveAs', 'Ctrl+Shift+S'], ['Export DST…', 'export', 'Ctrl+E'], '-', ['Production worksheet…', 'worksheet', 'Ctrl+P'], ['Print colour block analysis…', 'printBlocks', 'Ctrl+Shift+P']]],
  ['Edit', [['Undo', 'undo', 'Ctrl+Z'], ['Redo', 'redo', 'Ctrl+Y'], '-', ['Duplicate', 'duplicate', 'Ctrl+D'], ['Delete', 'delete', 'Del']]],
  ['View', [['Zoom in', 'zoomIn', 'Ctrl+='], ['Zoom out', 'zoomOut', 'Ctrl+-'], ['Fit to hoop', 'fit', 'Ctrl+0'], ['Toggle jumps', 'toggleJumps', 'Ctrl+J'], '-', ['Sequence grid', 'grid', 'Ctrl+G']]],
  ['Design', [['Rotate 90°', 'rotate', 'Ctrl+R'], ['Mirror horizontal', 'mirror'], ['Mirror vertical', 'mirrorV'], ['Center selection', 'center'], ['Center all in hoop', 'centerAll'], '-', ['Add lettering', 'lettering', 'Ctrl+T']]],
  ['Tools', [['Remove small stitches…', 'removeSmall'], ['Combine same-colour blocks', 'combine'], ['Machine settings…', 'production']]],
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
  } catch (e) {
    toast('Failed to start: ' + e.message, 'err');
    logError('startup: ' + (e.stack || e.message));
  }
  requestAnimationFrame(frame);
})();

if (!isDesktop) window.__dbg = { R, S }; // test hook (browser dev server only)
export { R, updateZoomLabel };
