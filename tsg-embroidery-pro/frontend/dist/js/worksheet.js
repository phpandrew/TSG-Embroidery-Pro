// Printable production worksheet.
import { renderImage, renderSteps } from './render.js';
import { esc, fmt, modal, toast } from './ui.js';
import { S, selectBlock } from './main.js';
import { openColorBlockPrint } from './print.js';

function sheetHTML() {
  const v = S.view;
  const sm = v.summary;
  const img = renderImage(v, 900);
  const tmap = new Map(v.threads.map(t => [t.id, t]));
  const blocks = [];
  v.designs.filter(d => d.visible).forEach(d => d.blocks.forEach(b => blocks.push({ d, b, t: tmap.get(b.thread) })));
  const date = new Date().toLocaleString();
  return `<div class="ws">
    <div class="ws-head"><h1>${esc(v.title || 'Untitled')}</h1><span>${esc(date)}</span></div>
    <div class="ws-grid">
      <div><img class="ws-img" src="${img}" alt="Design preview"></div>
      <div>
        <table>
          <tr><th>Machine</th><td>${esc(v.settings.machine)}</td></tr>
          <tr><th>Hoop</th><td>${esc(v.hoop.name)} ${sm.withinHoop ? '✓' : '⚠ design exceeds hoop'}</td></tr>
          <tr><th>Size</th><td>${sm.width.toFixed(1)} × ${sm.height.toFixed(1)} mm</td></tr>
          <tr><th>Stitches</th><td>${fmt(sm.stitches)}</td></tr>
          <tr><th>Colour changes</th><td>${sm.colorChanges}</td></tr>
          <tr><th>Trims</th><td>${sm.trims}</td></tr>
          <tr><th>Est. sew time</th><td>~${Math.max(1, Math.round(sm.sewMinutes))} min @ ${v.settings.spm} SPM</td></tr>
          <tr><th>Start point</th><td>${v.settings.startAt === 'hoop' ? 'Hoop centre' : 'Design centre'}</td></tr>
        </table>
        <h3 style="margin:14px 0 6px">Needle sequence</h3>
        <table>
          <tr><th>Stop</th><th>Needle</th><th>Thread</th><th>Stitches</th></tr>
          ${v.sequence.map(e => { const t = v.threads.find(x => x.name === e.thread); return `<tr><td>${e.step}</td><td>${e.needle}</td><td><span class="sw" style="background:${esc(t ? t.color : '#888')}"></span>${esc(e.thread)}</td><td>${fmt(e.stitches)}</td></tr>`; }).join('')}
        </table>
      </div>
    </div>
    <h3 style="margin:16px 0 6px">Colour blocks</h3>
    <table>
      <tr><th>#</th><th>Design</th><th>Thread</th><th>Needle</th><th>Stitches</th></tr>
      ${blocks.map(({ d, b, t }) => `<tr><td>${b.index}</td><td>${esc(d.name)}</td><td><span class="sw" style="background:${esc(t ? t.color : '#888')}"></span>${esc(t ? t.name : '?')}</td><td>${t ? t.needle : ''}</td><td>${fmt(b.stitches)}</td></tr>`).join('')}
    </table>
    <h3 style="margin:16px 0 6px">Sew sequence</h3>
    ${stepsHTML(220, false)}
    <div class="sign"><div>Operator</div><div>Approved / date</div></div>
  </div>`;
}

// Tiles for every sew step (one per colour block, in sew order).
function stepsHTML(px, clickable) {
  const v = S.view;
  const names = new Map(v.designs.map(d => [d.id, d.name]));
  return `<div class="steps">${renderSteps(v, px).map(({ url, ob, step }) => {
    const t = ob.block.thread;
    return `<${clickable ? 'button' : 'div'} class="step" data-id="${esc(ob.item.id)}" data-bi="${ob.block.bi}">
      <img src="${url}" alt="Step ${step}">
      <div class="cap"><b>Step ${step}</b> · Needle ${esc(t.needle ?? '')}<br>
        <span class="sw" style="background:${esc(t.color)}"></span>${esc(t.name)} · ${fmt(ob.block.count)} st<br>
        <span class="muted">${esc(names.get(ob.item.id) || '')}</span></div>
    </${clickable ? 'button' : 'div'}>`;
  }).join('')}</div>`;
}

export function openSequenceGrid() {
  if (!S.view || !S.view.designs.length) { toast('Nothing loaded yet'); return; }
  let picked = null;
  const body = `<p class="hint" style="margin:0 0 12px">Each step shows what sews at that colour change (earlier steps faded). Click a step to show it on the canvas.</p>${stepsHTML(260, true)}`;
  modal({
    title: `Sew sequence — ${S.view.summary.blocks} steps`,
    wide: true,
    body,
    buttons: [{ label: 'Print…', value: 'print' }, { label: 'Close', kind: 'primary', value: null }],
    onOpen: back => back.querySelectorAll('.step').forEach(el => {
      el.onclick = () => { picked = el; back.querySelector('[data-x]').click(); };
    }),
  }).then(v => {
    if (picked) selectBlock(picked.dataset.id, +picked.dataset.bi);
    if (v === 'print') openColorBlockPrint();
  });
}

export function openWorksheet() {
  if (!S.view || !S.view.designs.length) { toast('Nothing to print yet'); return; }
  const html = sheetHTML();
  modal({
    title: 'Production worksheet',
    wide: true,
    body: html,
    buttons: [{ label: 'Close', value: null }, { label: 'Print…', kind: 'primary', value: 'print' }],
  }).then(v => {
    if (v !== 'print') return;
    const root = document.getElementById('printRoot');
    const ps = document.getElementById('pageSize'); if (ps) ps.textContent = '@page { size: letter portrait; margin: 12mm; }';
    root.innerHTML = html;
    setTimeout(() => { window.print(); }, 50);
  });
}
