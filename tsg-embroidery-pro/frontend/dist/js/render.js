// Canvas renderer: grid, rulers, hoop, thread-simulated stitches, playback,
// selection handles. World units are 0.1 mm with Y down.

export const PX_PER_UNIT = 96 / 254; // CSS px per 0.1 mm at 100 % (real size)
export const RULER = 26;

const CMD = { STITCH: 0, JUMP: 1, TRIM: 2, COLOR: 3, STOP: 4, END: 5 };

function hexToRgb(h) {
  const n = parseInt((h || '#888888').slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}
function mix(rgb, to, t) {
  return `rgb(${rgb.map((c, i) => Math.round(c + (to[i] - c) * t)).join(',')})`;
}
export function luminance(hex) {
  const [r, g, b] = hexToRgb(hex);
  return (0.299 * r + 0.587 * g + 0.114 * b) / 255;
}

// Build Path2D objects for the first `limit` stitches of a block.
function buildPaths(points, limit = Infinity) {
  const st = new Path2D();
  const jp = new Path2D();
  let have = false, px = 0, py = 0, n = 0, lastX = 0, lastY = 0;
  for (let i = 0; i < points.length; i += 3) {
    const x = points[i], y = points[i + 1], c = points[i + 2];
    if (c === CMD.STITCH) {
      if (n >= limit) break;
      if (have) st.lineTo(x, y); else st.moveTo(x, y);
      have = true; px = x; py = y; n++; lastX = x; lastY = y;
    } else if (c === CMD.JUMP) {
      if (n >= limit) break;
      if (have) { jp.moveTo(px, py); jp.lineTo(x, y); }
      st.moveTo(x, y);
      have = true; px = x; py = y;
    }
  }
  return { st, jp, n, lastX, lastY };
}

export class Renderer {
  constructor(canvas) {
    this.cv = canvas;
    this.ctx = canvas.getContext('2d');
    this.zoom = 1;
    this.panX = 0;
    this.panY = 0;
    this.items = []; // per design: { id, blocks: [{paths, color, rgb, count, points}] }
    this.order = []; // visible blocks in sew order
    this.total = 0;
    this.view = null;
    this.W = 0; this.H = 0;
  }

  resize() {
    const r = this.cv.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    this.W = r.width; this.H = r.height;
    this.cv.width = Math.round(r.width * dpr);
    this.cv.height = Math.round(r.height * dpr);
    this.dpr = dpr;
  }

  get k() { return this.zoom * PX_PER_UNIT; }
  get ox() { return RULER + (this.W - RULER) / 2 + this.panX; }
  get oy() { return RULER + (this.H - RULER) / 2 + this.panY; }
  toScreen(x, y) { return [this.ox + x * this.k, this.oy + y * this.k]; }
  toWorld(sx, sy) { return [(sx - this.ox) / this.k, (sy - this.oy) / this.k]; }

  fit(hoop) {
    if (!this.W) return;
    const w = (hoop ? hoop.w : 2000) * 1.08, h = (hoop ? hoop.h : 2000) * 1.08;
    const z = Math.min((this.W - RULER - 40) / (w * PX_PER_UNIT), (this.H - RULER - 40) / (h * PX_PER_UNIT));
    this.zoom = Math.max(0.1, Math.min(z, 20));
    this.panX = 0; this.panY = 0;
  }

  zoomAt(factor, sx, sy) {
    const [wx, wy] = this.toWorld(sx, sy);
    this.zoom = Math.max(0.1, Math.min(this.zoom * factor, 40));
    // keep world point under cursor
    this.panX += sx - (this.ox + wx * this.k);
    this.panY += sy - (this.oy + wy * this.k);
  }

  setView(view) {
    this.view = view;
    const threads = new Map(view.threads.map(t => [t.id, t]));
    this.items = view.designs.map(d => ({
      id: d.id,
      visible: d.visible,
      bounds: d.bounds,
      blocks: d.blocks.map((b, bi) => {
        const t = threads.get(b.thread) || { color: '#888888', name: '?' };
        const rgb = hexToRgb(t.color);
        return { points: b.points, color: t.color, rgb, count: b.stitches, paths: buildPaths(b.points), thread: t, index: b.index, bi };
      }),
    }));
    this.order = [];
    let acc = 0;
    for (const it of this.items) {
      if (!it.visible) continue;
      for (const b of it.blocks) {
        this.order.push({ item: it, block: b, start: acc });
        acc += b.count;
      }
    }
    this.total = acc;
    // projected sewing time per block (same model as the design summary:
    // stitches / SPM + seconds per trim + seconds per colour change)
    const st = view.settings || {};
    this.spm = Math.max(st.spm || 800, 1);
    let t = 0, prevNeedle = null;
    for (const ob of this.order) {
      const b = ob.block;
      let trims = 0;
      for (let i = 2; i < b.points.length; i += 3) if (b.points[i] === CMD.TRIM) trims++;
      if (prevNeedle !== null && b.thread.needle !== prevNeedle) t += st.colorSeconds || 0;
      prevNeedle = b.thread.needle;
      ob.tStart = t;
      ob.trimSec = trims * (st.trimSeconds || 0);
      ob.tDur = (b.count / this.spm) * 60 + ob.trimSec;
      t += ob.tDur;
    }
    this.totalSec = t;
  }

  // Projected seconds of sewing up to stitch position pos.
  timeAt(pos) {
    if (pos >= this.total) return this.totalSec;
    for (const ob of this.order) {
      if (pos < ob.start + ob.block.count) {
        const f = ob.block.count ? (pos - ob.start) / ob.block.count : 0;
        return ob.tStart + ob.tDur * f;
      }
    }
    return this.totalSec;
  }

  // ---- drawing ----

  draw(s) {
    const { ctx } = this;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    ctx.clearRect(0, 0, this.W, this.H);
    ctx.fillStyle = '#f6f7f9';
    ctx.fillRect(0, 0, this.W, this.H);
    this.drawGrid();
    this.drawHoop(s.hoop);
    this.drawStitches(s);
    if (s.tool === 'node') this.drawNodes(s);
    this.drawSelection(s);
    if (s.measure) this.drawMeasure(s.measure);
    this.drawRulers();
  }

  gridStep() {
    // minor grid spacing in mm keeping lines >= 7 px apart
    for (const mm of [1, 2, 5, 10, 20, 50, 100]) if (mm * 10 * this.k >= 7) return mm;
    return 100;
  }

  drawGrid() {
    const { ctx, k } = this;
    const step = this.gridStep() * 10;
    const [x0, y0] = this.toWorld(RULER, RULER);
    const [x1, y1] = this.toWorld(this.W, this.H);
    ctx.lineWidth = 1;
    for (let pass = 0; pass < 2; pass++) {
      ctx.beginPath();
      ctx.strokeStyle = pass === 0 ? '#eceef2' : '#e1e4e9';
      const s = pass === 0 ? step : step * 2;
      for (let x = Math.ceil(x0 / s) * s; x <= x1; x += s) {
        const sx = Math.round(this.ox + x * k) + 0.5;
        ctx.moveTo(sx, RULER); ctx.lineTo(sx, this.H);
      }
      for (let y = Math.ceil(y0 / s) * s; y <= y1; y += s) {
        const sy = Math.round(this.oy + y * k) + 0.5;
        ctx.moveTo(RULER, sy); ctx.lineTo(this.W, sy);
      }
      ctx.stroke();
    }
  }

  drawHoop(h) {
    if (!h) return;
    const { ctx, k } = this;
    ctx.save();
    ctx.strokeStyle = '#a3aab5';
    ctx.lineWidth = 1.2;
    ctx.setLineDash([5, 4]);
    ctx.beginPath();
    if (h.shape === 'round') {
      ctx.arc(this.ox, this.oy, (h.w / 2) * k, 0, Math.PI * 2);
    } else {
      ctx.rect(this.ox - (h.w / 2) * k, this.oy - (h.h / 2) * k, h.w * k, h.h * k);
    }
    ctx.stroke();
    // centre lines
    ctx.strokeStyle = '#b4bac4';
    ctx.lineWidth = 1;
    ctx.setLineDash([6, 5]);
    ctx.beginPath();
    const ex = (h.w / 2) * k + 60, ey = (h.h / 2) * k + 30;
    ctx.moveTo(this.ox - ex, this.oy); ctx.lineTo(this.ox + ex, this.oy);
    ctx.moveTo(this.ox, this.oy - ey); ctx.lineTo(this.ox, this.oy + ey);
    ctx.stroke();
    ctx.restore();
  }

  previewMatrix(s, id) {
    const d = s.drag;
    if (!d || d.id !== id) return null;
    const m = new DOMMatrix();
    if (d.type === 'move') return m.translate(d.dx, d.dy);
    if (d.type === 'scale') return m.translate(d.ax, d.ay).scale(d.fx, d.fy).translate(-d.ax, -d.ay);
    if (d.type === 'rotate') return m.translate(d.cx, d.cy).rotate(d.angle).translate(-d.cx, -d.cy);
    return null;
  }

  drawStitches(s) {
    const { ctx, k } = this;
    const pos = s.playPos ?? this.total;
    const w = Math.max(4.2, 1.15 / k); // thread width in world units (0.42 mm)
    for (const ob of this.order) {
      if (ob.start >= pos) break;
      const b = ob.block;
      const partial = pos < ob.start + b.count;
      const paths = partial ? buildPaths(b.points, pos - ob.start) : b.paths;
      ctx.save();
      ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
      ctx.translate(this.ox, this.oy);
      ctx.scale(k, k);
      const pm = this.previewMatrix(s, ob.item.id);
      if (pm) ctx.transform(pm.a, pm.b, pm.c, pm.d, pm.e, pm.f);
      ctx.lineCap = 'round';
      ctx.lineJoin = 'round';
      // a block selected in the sequence list: fade everything else
      const sb = s.selBlock;
      const A = sb && !(sb.id === ob.item.id && sb.index === b.bi) ? 0.14 : 1;
      if (s.showStitches) {
        const light = luminance(b.color) > 0.8;
        ctx.globalAlpha = A;
        ctx.strokeStyle = mix(b.rgb, [0, 0, 0], light ? 0.3 : 0.38);
        ctx.lineWidth = w * 1.18;
        ctx.stroke(paths.st);
        ctx.strokeStyle = b.color;
        ctx.lineWidth = w * 0.82;
        ctx.stroke(paths.st);
        if (k > 0.35) {
          ctx.globalAlpha = 0.5 * A;
          ctx.strokeStyle = mix(b.rgb, [255, 255, 255], 0.55);
          ctx.lineWidth = w * 0.26;
          ctx.stroke(paths.st);
        }
      } else {
        ctx.globalAlpha = A;
        ctx.strokeStyle = b.color === '#f4f4f2' ? '#9aa2af' : b.color;
        ctx.lineWidth = 1 / k;
        ctx.stroke(paths.st);
      }
      if (s.showJumps) {
        ctx.globalAlpha = 0.85 * A;
        ctx.strokeStyle = '#d33a2f';
        ctx.lineWidth = 1 / k;
        ctx.setLineDash([4 / k, 3 / k]);
        ctx.stroke(paths.jp);
        ctx.setLineDash([]);
      }
      ctx.restore();
      if (partial) {
        const [sx, sy] = this.toScreen(paths.lastX, paths.lastY);
        ctx.save();
        ctx.fillStyle = '#d33a2f';
        ctx.strokeStyle = '#fff';
        ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(sx, sy, 5, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        ctx.restore();
      }
    }
  }

  drawNodes(s) {
    const it = this.items.find(i => i.id === s.sel);
    if (!it || this.k < 0.5) return;
    const { ctx } = this;
    ctx.save();
    ctx.fillStyle = 'rgba(22,104,227,.75)';
    for (const b of it.blocks) {
      const p = b.points;
      for (let i = 0; i < p.length; i += 3) {
        if (p[i + 2] !== 0) continue;
        const [sx, sy] = this.toScreen(p[i], p[i + 1]);
        ctx.fillRect(sx - 1.2, sy - 1.2, 2.4, 2.4);
      }
    }
    ctx.restore();
  }

  // Screen-space rectangle for a design (including drag preview).
  selRect(s, id) {
    const it = this.items.find(i => i.id === id);
    if (!it || !it.bounds || it.bounds.MinX > it.bounds.MaxX) return null;
    const b = it.bounds;
    let pts = [[b.MinX, b.MinY], [b.MaxX, b.MinY], [b.MaxX, b.MaxY], [b.MinX, b.MaxY]];
    const pm = this.previewMatrix(s, id);
    if (pm) pts = pts.map(([x, y]) => { const p = pm.transformPoint(new DOMPoint(x, y)); return [p.x, p.y]; });
    const sp = pts.map(([x, y]) => this.toScreen(x, y));
    return { pts: sp, world: pts, rotating: s.drag && s.drag.type === 'rotate' && s.drag.id === id };
  }

  handles(r) {
    const xs = r.pts.map(p => p[0]), ys = r.pts.map(p => p[1]);
    const x0 = Math.min(...xs), x1 = Math.max(...xs), y0 = Math.min(...ys), y1 = Math.max(...ys);
    const cx = (x0 + x1) / 2, cy = (y0 + y1) / 2;
    return {
      x0, x1, y0, y1, cx, cy,
      list: [
        ['nw', x0, y0], ['n', cx, y0], ['ne', x1, y0], ['e', x1, cy],
        ['se', x1, y1], ['s', cx, y1], ['sw', x0, y1], ['w', x0, cy],
      ],
      rot: [cx, y0 - 22],
    };
  }

  drawSelection(s) {
    if (!s.sel || s.tool === 'pan') return;
    const r = this.selRect(s, s.sel);
    if (!r) return;
    const { ctx } = this;
    ctx.save();
    ctx.strokeStyle = '#1668e3';
    ctx.lineWidth = 1.2;
    ctx.beginPath();
    if (r.rotating) {
      r.pts.forEach(([x, y], i) => (i ? ctx.lineTo(x, y) : ctx.moveTo(x, y)));
      ctx.closePath();
      ctx.stroke();
      ctx.restore();
      return;
    }
    const h = this.handles(r);
    ctx.rect(h.x0, h.y0, h.x1 - h.x0, h.y1 - h.y0);
    ctx.moveTo(h.cx, h.y0); ctx.lineTo(h.rot[0], h.rot[1] + 5);
    ctx.stroke();
    ctx.fillStyle = '#fff';
    for (const [, x, y] of h.list) {
      ctx.fillRect(x - 4, y - 4, 8, 8);
      ctx.strokeRect(x - 4, y - 4, 8, 8);
    }
    ctx.beginPath(); ctx.arc(h.rot[0], h.rot[1], 5.5, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
    // start (green) and end (red) needle points of the design
    const it0 = this.items.find(i => i.id === s.sel);
    const se = it0 && startEnd(it0);
    if (se) {
      const pm = this.previewMatrix(s, s.sel);
      const tp = (x, y) => { if (pm) { const p = pm.transformPoint(new DOMPoint(x, y)); x = p.x; y = p.y; } return this.toScreen(x, y); };
      const [ax, ay] = tp(se[0], se[1]);
      const [bx, by] = tp(se[2], se[3]);
      ctx.save();
      ctx.lineWidth = 2;
      ctx.strokeStyle = '#fff';
      ctx.fillStyle = '#1f9d55';
      ctx.beginPath(); ctx.arc(ax, ay, 6, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
      ctx.fillStyle = '#d33a2f';
      ctx.fillRect(bx - 5.5, by - 5.5, 11, 11); ctx.strokeRect(bx - 5.5, by - 5.5, 11, 11);
      ctx.font = '600 11px "Segoe UI", system-ui, sans-serif';
      ctx.lineWidth = 3;
      ctx.strokeText('START', ax + 9, ay - 8); ctx.fillStyle = '#1f9d55'; ctx.fillText('START', ax + 9, ay - 8);
      ctx.fillStyle = '#d33a2f'; ctx.strokeText('END', bx + 9, by + 16); ctx.fillText('END', bx + 9, by + 16);
      ctx.restore();
    }
    // dimension line
    const wmm = Math.abs(r.world[1][0] - r.world[0][0]) / 10;
    const it = this.items.find(i => i.id === s.sel);
    const widthMM = s.drag && s.drag.id === s.sel ? wmm : (it.bounds.MaxX - it.bounds.MinX) / 10;
    const dy = h.y1 + 20;
    ctx.beginPath();
    ctx.moveTo(h.x0, dy - 6); ctx.lineTo(h.x0, dy + 6);
    ctx.moveTo(h.x1, dy - 6); ctx.lineTo(h.x1, dy + 6);
    ctx.moveTo(h.x0, dy); ctx.lineTo(h.x1, dy);
    ctx.stroke();
    const label = widthMM.toFixed(1) + ' mm';
    ctx.font = '14px "Segoe UI", system-ui, sans-serif';
    const tw = ctx.measureText(label).width + 12;
    ctx.fillStyle = '#fff';
    ctx.fillRect(h.cx - tw / 2, dy - 9, tw, 18);
    ctx.fillStyle = '#1668e3';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(label, h.cx, dy);
    ctx.restore();
  }

  drawMeasure(m) {
    const { ctx } = this;
    const [ax, ay] = this.toScreen(m.x0, m.y0);
    const [bx, by] = this.toScreen(m.x1, m.y1);
    ctx.save();
    ctx.strokeStyle = '#d33a2f';
    ctx.fillStyle = '#d33a2f';
    ctx.lineWidth = 1.5;
    ctx.beginPath(); ctx.moveTo(ax, ay); ctx.lineTo(bx, by); ctx.stroke();
    for (const [x, y] of [[ax, ay], [bx, by]]) { ctx.beginPath(); ctx.arc(x, y, 3, 0, 7); ctx.fill(); }
    const d = Math.hypot(m.x1 - m.x0, m.y1 - m.y0) / 10;
    const label = `${d.toFixed(1)} mm  (Δx ${((m.x1 - m.x0) / 10).toFixed(1)}, Δy ${((m.y1 - m.y0) / 10).toFixed(1)})`;
    ctx.font = '13px "Segoe UI", system-ui, sans-serif';
    const tw = ctx.measureText(label).width + 12;
    ctx.fillStyle = 'rgba(255,255,255,.95)';
    ctx.fillRect(bx + 10, by - 10, tw, 20);
    ctx.fillStyle = '#b3261e';
    ctx.textBaseline = 'middle';
    ctx.fillText(label, bx + 16, by);
    ctx.restore();
  }

  drawRulers() {
    const { ctx, k } = this;
    ctx.save();
    ctx.fillStyle = '#ffffff';
    ctx.fillRect(0, 0, this.W, RULER);
    ctx.fillRect(0, 0, RULER, this.H);
    ctx.strokeStyle = '#d9dde3';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, RULER - 0.5); ctx.lineTo(this.W, RULER - 0.5);
    ctx.moveTo(RULER - 0.5, 0); ctx.lineTo(RULER - 0.5, this.H);
    ctx.stroke();
    // label step in mm, labels >= 60 px apart
    let major = 10;
    for (const mm of [5, 10, 20, 25, 50, 100, 200, 500]) { major = mm; if (mm * 10 * k >= 70) break; }
    const minor = major / 10;
    ctx.fillStyle = '#4a5263';
    ctx.font = '12px "Segoe UI", system-ui, sans-serif';
    ctx.strokeStyle = '#9aa2af';
    const [wx0] = this.toWorld(RULER, 0), [wx1] = this.toWorld(this.W, 0);
    const [, wy0] = this.toWorld(0, RULER), [, wy1] = this.toWorld(0, this.H);
    ctx.beginPath();
    for (let mm = Math.ceil(wx0 / 10 / minor) * minor; mm <= wx1 / 10; mm += minor) {
      const x = Math.round(this.ox + mm * 10 * k) + 0.5;
      const isMajor = Math.abs(mm / major - Math.round(mm / major)) < 1e-6;
      const half = Math.abs(mm / (major / 2) - Math.round(mm / (major / 2))) < 1e-6;
      ctx.moveTo(x, RULER); ctx.lineTo(x, RULER - (isMajor ? 9 : half ? 6 : 3));
      if (isMajor) { ctx.textAlign = 'center'; ctx.fillText(String(Math.round(mm)), x, 12); }
    }
    for (let mm = Math.ceil(wy0 / 10 / minor) * minor; mm <= wy1 / 10; mm += minor) {
      const y = Math.round(this.oy + mm * 10 * k) + 0.5;
      const isMajor = Math.abs(mm / major - Math.round(mm / major)) < 1e-6;
      const half = Math.abs(mm / (major / 2) - Math.round(mm / (major / 2))) < 1e-6;
      ctx.moveTo(RULER, y); ctx.lineTo(RULER - (isMajor ? 9 : half ? 6 : 3), y);
      if (isMajor && y > RULER + 8) { ctx.textAlign = 'right'; ctx.textBaseline = 'middle'; ctx.fillText(String(Math.round(mm)), RULER - 3, y - 7); ctx.textBaseline = 'alphabetic'; }
    }
    ctx.stroke();
    ctx.fillStyle = '#fff';
    ctx.fillRect(0, 0, RULER - 1, RULER - 1);
    ctx.restore();
  }

  hitDesign(sx, sy) {
    const [wx, wy] = this.toWorld(sx, sy);
    const pad = 6 / this.k;
    for (let i = this.items.length - 1; i >= 0; i--) {
      const it = this.items[i];
      const b = it.bounds;
      if (!it.visible || !b) continue;
      if (wx >= b.MinX - pad && wx <= b.MaxX + pad && wy >= b.MinY - pad && wy <= b.MaxY + pad) return it.id;
    }
    return null;
  }
}

// First and last needle points of a design item: [sx, sy, ex, ey].
function startEnd(it) {
  let s = null, e = null;
  for (const b of it.blocks) {
    const p = b.points;
    for (let i = 0; i < p.length; i += 3) {
      if (p[i + 2] !== CMD.STITCH) continue;
      if (!s) s = [p[i], p[i + 1]];
      e = [p[i], p[i + 1]];
    }
  }
  return s ? [s[0], s[1], e[0], e[1]] : null;
}

// Small thumbnail for the sequence list.
export function drawThumb(canvas, design, threads) {
  const dpr = window.devicePixelRatio || 1;
  const size = 36;
  canvas.width = size * dpr; canvas.height = size * dpr;
  const ctx = canvas.getContext('2d');
  ctx.scale(dpr, dpr);
  const b = design.bounds;
  if (!b || b.MinX > b.MaxX) return;
  const s = (size - 4) / Math.max(b.MaxX - b.MinX, b.MaxY - b.MinY, 1);
  const tmap = new Map(threads.map(t => [t.id, t.color]));
  ctx.translate(size / 2, size / 2);
  ctx.scale(s, s);
  ctx.translate(-(b.MinX + b.MaxX) / 2, -(b.MinY + b.MaxY) / 2);
  ctx.lineWidth = 1.4 / s;
  ctx.lineJoin = 'round';
  for (const blk of design.blocks) {
    const c = tmap.get(blk.thread) || '#888';
    ctx.strokeStyle = luminance(c) > 0.85 ? '#c9ccd2' : c;
    ctx.stroke(buildPaths(blk.points).st);
  }
}

// High-resolution image of the whole layout for the worksheet.
export function renderImage(view, px = 900) {
  const r = new Renderer(document.createElement('canvas'));
  r.setView(view);
  const b = view.summary.bounds;
  const w = Math.max(b.MaxX - b.MinX, 10), h = Math.max(b.MaxY - b.MinY, 10);
  const scale = (px - 40) / Math.max(w, h);
  const cw = Math.round(w * scale + 40), ch = Math.round(h * scale + 40);
  const cv = r.cv;
  cv.width = cw; cv.height = ch;
  const ctx = cv.getContext('2d');
  ctx.fillStyle = '#fff';
  ctx.fillRect(0, 0, cw, ch);
  ctx.translate(20 - b.MinX * scale, 20 - b.MinY * scale);
  ctx.scale(scale, scale);
  ctx.lineCap = 'round'; ctx.lineJoin = 'round';
  const w0 = Math.max(4.2, 1 / scale);
  for (const ob of r.order) {
    const bl = ob.block;
    ctx.strokeStyle = mix(bl.rgb, [0, 0, 0], 0.35); ctx.lineWidth = w0 * 1.18; ctx.stroke(bl.paths.st);
    ctx.strokeStyle = bl.color; ctx.lineWidth = w0 * 0.82; ctx.stroke(bl.paths.st);
  }
  return cv.toDataURL('image/png');
}

// One image per sew step: earlier steps faded, the current step in full
// colour, later steps hidden. Returns [{url, ob, step}] in sew order.
export function renderSteps(view, px = 260) {
  const r = new Renderer(document.createElement('canvas'));
  r.setView(view);
  const b = view.summary.bounds;
  const w = Math.max(b.MaxX - b.MinX, 10), h = Math.max(b.MaxY - b.MinY, 10);
  const pad = 12;
  const scale = (px - pad * 2) / Math.max(w, h);
  const cw = Math.round(w * scale + pad * 2), ch = Math.round(h * scale + pad * 2);
  const out = [];
  const lw = Math.max(4.2, 0.9 / scale);
  r.order.forEach((cur, step) => {
    const cv = document.createElement('canvas');
    cv.width = cw; cv.height = ch;
    const ctx = cv.getContext('2d');
    ctx.fillStyle = '#fff';
    ctx.fillRect(0, 0, cw, ch);
    ctx.translate(pad - b.MinX * scale, pad - b.MinY * scale);
    ctx.scale(scale, scale);
    ctx.lineCap = 'round'; ctx.lineJoin = 'round';
    for (let i = 0; i <= step; i++) {
      const bl = r.order[i].block;
      const now = i === step;
      ctx.globalAlpha = now ? 1 : 0.22;
      ctx.strokeStyle = mix(bl.rgb, [0, 0, 0], 0.35); ctx.lineWidth = lw * 1.18; ctx.stroke(bl.paths.st);
      ctx.strokeStyle = bl.color; ctx.lineWidth = lw * 0.82; ctx.stroke(bl.paths.st);
    }
    out.push({ url: cv.toDataURL('image/png'), ob: cur, step: step + 1 });
  });
  return out;
}

// "Color Block Analysis" tiles: the whole design in light grey, the current
// block on top (highlight colour or its own thread colour), centre crosshair.
// Returns [{url, ob, step}] in sew order.
export function renderAnalysis(view, { px = 700, highlight = '#3fae3a', useThread = false } = {}) {
  const r = new Renderer(document.createElement('canvas'));
  r.setView(view);
  const b = view.summary.bounds;
  const w = Math.max(b.MaxX - b.MinX, 10), h = Math.max(b.MaxY - b.MinY, 10);
  const pad = px * 0.04;
  const scale = (px - pad * 2) / Math.max(w, h);
  const ox = px / 2 - ((b.MinX + b.MaxX) / 2) * scale;
  const oy = px / 2 - ((b.MinY + b.MaxY) / 2) * scale;
  const lw = Math.max(4.2, 1.2 / scale);
  // background (all blocks, grey) rendered once and reused
  const bg = document.createElement('canvas');
  bg.width = bg.height = px;
  {
    const c = bg.getContext('2d');
    c.fillStyle = '#fff';
    c.fillRect(0, 0, px, px);
    c.strokeStyle = '#c9ccd1';
    c.lineWidth = 1;
    c.beginPath();
    c.moveTo(0, oy + ((b.MinY + b.MaxY) / 2) * scale); c.lineTo(px, oy + ((b.MinY + b.MaxY) / 2) * scale);
    c.moveTo(ox + ((b.MinX + b.MaxX) / 2) * scale, 0); c.lineTo(ox + ((b.MinX + b.MaxX) / 2) * scale, px);
    c.stroke();
    c.setTransform(scale, 0, 0, scale, ox, oy);
    c.lineCap = 'round'; c.lineJoin = 'round';
    c.strokeStyle = '#d3d6da';
    c.lineWidth = lw * 0.7;
    for (const o of r.order) c.stroke(o.block.paths.st);
  }
  const hl = hexToRgb(highlight);
  return r.order.map((cur, i) => {
    const cv = document.createElement('canvas');
    cv.width = cv.height = px;
    const c = cv.getContext('2d');
    c.drawImage(bg, 0, 0);
    c.setTransform(scale, 0, 0, scale, ox, oy);
    c.lineCap = 'round'; c.lineJoin = 'round';
    const bl = cur.block;
    // jumps of this block as hairlines
    c.strokeStyle = '#9aa0a8';
    c.lineWidth = 0.8 / scale;
    c.stroke(bl.paths.jp);
    const rgb = useThread ? bl.rgb : hl;
    const col = useThread ? bl.color : highlight;
    c.strokeStyle = mix(rgb, [0, 0, 0], luminance(col) > 0.8 ? 0.35 : 0.3);
    c.lineWidth = lw * 1.15;
    c.stroke(bl.paths.st);
    c.strokeStyle = col;
    c.lineWidth = lw * 0.8;
    c.stroke(bl.paths.st);
    c.globalAlpha = 0.45;
    c.strokeStyle = mix(rgb, [255, 255, 255], 0.5);
    c.lineWidth = lw * 0.25;
    c.stroke(bl.paths.st);
    return { url: cv.toDataURL('image/png'), ob: cur, step: i + 1 };
  });
}
