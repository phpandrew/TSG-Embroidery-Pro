// "Color Block Analysis" print-off: one tile per colour stop, letter paper.
import { renderAnalysis } from './render.js';
import { esc, fmt, modal, toast } from './ui.js';
import { S } from './main.js';

const LAYOUTS = {
  landscape: { cols: 4, rows: 2, size: 'letter landscape' },
  portrait: { cols: 3, rows: 3, size: 'letter portrait' },
};

// Design title = design file name(s) without extension.
export function designTitle(v) {
  const names = v.designs.filter(d => d.visible).map(d => {
    const src = d.source ? d.source.split(/[\\/]/).pop().replace(/\.[^.]+$/, '') : '';
    return src || d.name;
  });
  if (!names.length) return v.title || 'Untitled';
  return names.length > 2 ? `${names[0]} + ${names.length - 1} more` : names.join(' + ');
}

// Approximate top-thread use: needle-to-needle length plus ~10% take-up.
function threadMetres(v) {
  let mm = 0;
  for (const d of v.designs) {
    if (!d.visible) continue;
    for (const b of d.blocks) {
      const p = b.points;
      let px = null, py = null;
      for (let i = 0; i < p.length; i += 3) {
        const c = p[i + 2];
        if (c === 0) { if (px !== null) mm += Math.hypot(p[i] - px, p[i + 1] - py) / 10; px = p[i]; py = p[i + 1]; }
        else if (c === 1) { px = p[i]; py = p[i + 1]; }
      }
    }
  }
  return (mm * 1.1) / 1000;
}

function infoBox(v, title) {
  const sm = v.summary;
  const vis = v.designs.filter(d => d.visible);
  const scales = [...new Set(vis.map(d => d.kind === 'lettering' ? 100 : Math.round(d.scalePct)))];
  const scale = scales.length === 1 ? scales[0] + '%' : scales.map(x => x + '%').join(' / ');
  const seq = v.sequence.map(e => e.needle).join(' – ');
  const inch = x => (x / 25.4).toFixed(2);
  return `<div class="cba-box">
    <div><span>Design's Name:</span><b>${esc(title)}</b></div>
    <div><span>Number of Stitches:</span><b>${fmt(sm.stitches)}</b></div>
    <div><span>Dimensions:</span><b>${inch(sm.width)} × ${inch(sm.height)} in</b> <i>(${sm.width.toFixed(1)} × ${sm.height.toFixed(1)} mm)</i></div>
    <div><span>Total Thread:</span><b>≈ ${Math.round(threadMetres(v))} m</b></div>
    <div><span>Scale:</span><b>${esc(scale)}</b></div>
    <div><span>Colour blocks / changes:</span><b>${v.summary.blocks} / ${sm.colorChanges}</b></div>
    <div><span>Trims:</span><b>${sm.trims}</b></div>
    <div><span>Est. sew time:</span><b>~${Math.max(1, Math.round(sm.sewMinutes))} min</b> <i>@ ${v.settings.spm} SPM</i></div>
    <div class="wide"><span>Needle sequence:</span><b>${esc(seq)}</b></div>
  </div>`;
}

function pagesHTML(opts) {
  const v = S.view;
  const sm = v.summary;
  const L = LAYOUTS[opts.layout];
  const per = L.cols * L.rows;
  const first = per - L.cols; // page 1 gives one tile row to the info box
  const tiles = renderAnalysis(v, { px: 720, useThread: opts.useThread });
  const title = designTitle(v);
  const pages = [];
  pages.push(tiles.slice(0, first));
  for (let i = first; i < tiles.length; i += per) pages.push(tiles.slice(i, i + per));
  const info = `${fmt(sm.stitches)} stitches · ${(sm.width / 25.4).toFixed(2)} × ${(sm.height / 25.4).toFixed(2)} in (${sm.width.toFixed(1)} × ${sm.height.toFixed(1)} mm) · ${tiles.length} colour blocks`;
  return pages.map((pg, pi) => `<section class="cba-page ${opts.layout}">
    <header class="cba-head"><span>Color Block Analysis</span><span>Design: ${esc(title)}</span></header>
    ${pi === 0 ? infoBox(v, title) : `<div class="cba-info">${esc(info)}</div>`}
    <div class="cba-grid" style="grid-template-columns:repeat(${L.cols},1fr);grid-template-rows:repeat(${pi === 0 ? L.rows - 1 : L.rows},1fr)">
      ${pg.map(({ url, ob, step }) => {
        const t = ob.block.thread;
        const prev = step > 1 ? tiles[step - 2].ob.block.thread : null;
        // same needle as the previous block = no machine stop, sews straight on
        const label = step === 1 ? `NEEDLE ${t.needle ?? ''} - 1` : prev && prev.needle === t.needle ? `CONT. - ${ob.start + 1}` : `STOP - ${ob.start + 1}`;
        return `<div class="cba-tile">
          <div class="cba-top"><span class="cba-sw" style="background:${esc(t.color)}"></span>
            <span class="cba-lbl"><b>${esc(label)}</b><br>${step}. Needle ${esc(t.needle ?? '')} · ${esc(t.name)} · ${fmt(ob.block.count)} st</span></div>
          <img src="${url}" alt="Stop ${step}">
        </div>`;
      }).join('')}
    </div>
    <footer class="cba-foot"><span>${esc(v.settings.machine)}</span><span>Page ${pi + 1} of ${pages.length}</span></footer>
  </section>`).join('');
}

function setPageSize(size) {
  let st = document.getElementById('pageSize');
  if (!st) { st = document.createElement('style'); st.id = 'pageSize'; document.head.appendChild(st); }
  st.textContent = `@page { size: ${size}; margin: 0.3in; }`;
}

export function openColorBlockPrint() {
  if (!S.view || !S.view.designs.length) { toast('Nothing to print yet'); return; }
  const opts = { layout: 'landscape', useThread: false };
  const controls = `<div class="cba-controls form">
      <label class="f">Orientation<select class="inp" id="cba-layout"><option value="landscape">Landscape — 8 per page</option><option value="portrait">Portrait — 9 per page</option></select></label>
      <label class="check"><input type="checkbox" id="cba-thread"> Show each block in its thread colour (default: green highlight)</label>
    </div><div class="cba-preview" id="cba-preview"></div>`;
  const draw = back => {
    const p = back.querySelector('#cba-preview');
    p.innerHTML = pagesHTML(opts);
  };
  modal({
    title: 'Print colour block analysis',
    wide: true,
    body: controls,
    buttons: [{ label: 'Close', value: null }, { label: 'Print…', kind: 'primary', value: 'print' }],
    onOpen: back => {
      draw(back);
      back.querySelector('#cba-layout').onchange = e => { opts.layout = e.target.value; draw(back); };
      back.querySelector('#cba-thread').onchange = e => { opts.useThread = e.target.checked; draw(back); };
    },
  }).then(v => {
    if (v !== 'print') return;
    setPageSize(LAYOUTS[opts.layout].size);
    document.getElementById('printRoot').innerHTML = pagesHTML(opts);
    // the system print dialog lets the operator pick any installed printer
    setTimeout(() => window.print(), 80);
  });
}
