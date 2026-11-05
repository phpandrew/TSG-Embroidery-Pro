// Order lookup: enter an order number, see the order and its embroidered
// positions with the shop's thread colours, then open the DST(s).
import { api } from './api.js';
import { esc, modal, toast, threadLabel } from './ui.js';
import { S, run, R, updateZoomLabel, redraw } from './main.js';

const POS = { front: 'Front', back: 'Back', sleeve_left: 'Left sleeve', sleeve_right: 'Right sleeve', yoke: 'Yoke', name_plate: 'Name plate' };
const posName = p => POS[p] || (p ? p[0].toUpperCase() + p.slice(1).replace(/_/g, ' ') : 'Design');
let lastNumber = '';

export async function openOrderDialog() {
  let info = null;
  let toOpen = [];
  await modal({
    title: 'Order lookup',
    wide: true,
    body: `<div class="od">
      <div class="od-bar">
        <input class="inp od-num" id="od-num" inputmode="numeric" autocomplete="off" placeholder="Order number" value="${esc(lastNumber)}">
        <button class="btn btn-primary" id="od-go">Look up</button>
        <span class="muted" id="od-mark"></span>
      </div>
      <div id="od-out" class="od-out"><p class="hint">Type or scan an order number and press Enter.</p></div>
    </div>`,
    buttons: [{ label: 'Close', value: null }],
    onOpen: back => {
      back.querySelector('.modal').classList.add('od-modal');
      const q = s => back.querySelector(s);
      const out = q('#od-out');
      const num = q('#od-num');
      const swatch = t => t.notSet || (!t.thread_code && !t.thread_name && !t.hex)
        ? '<span class="sw od-notset" title="Not set"></span>'
        : `<span class="sw" style="background:${esc(t.hex || '#888')}"></span>`;
      const threadRows = list => list && list.length
        ? `<table class="tbl od-threads"><tbody>${list.map(t => {
            const notSet = !t.thread_code && !t.thread_name && !t.hex;
            return `<tr><td class="od-seq">${t.sequence}</td><td>${swatch(t)}</td><td>${notSet ? '<i class="muted">Not set</i>' : esc(threadLabel({ code: t.thread_code, name: t.thread_name }))}</td><td class="muted">${esc(t.brand || '')}</td><td>${t.note ? `<span class="th-note">${esc(t.note)}</span>` : ''}</td></tr>`;
          }).join('')}</tbody></table>`
        : '<p class="hint">No thread colours saved for this file: the program’s default colours are used.</p>';

      const render = () => {
        const o = info.order, p = info.products || { files: [] };
        const files = p.files || [];
        const openable = files.filter(f => !f.missing && f.url);
        out.innerHTML = `
          <div class="od-head">
            <div><b>#${esc(o.number)}</b> <span class="od-status">${esc(o.status)}</span>
              <div class="muted">${[p.store, p.title].filter(Boolean).map(esc).join(' · ')}</div></div>
            <div class="od-garment">${[o.style, o.color, o.size].filter(Boolean).map(esc).join(' · ')}${o.qty ? ` · <b>Qty ${esc(o.qty)}</b>` : ''}</div>
          </div>
          ${info.warning ? `<div class="od-msg bad">${esc(info.warning)}</div>` : ''}
          ${o.adminMessage ? `<div class="od-msg">${esc(o.adminMessage)}</div>` : ''}
          ${p.noDst ? `<div class="od-msg">${esc(p.dstNote || 'Nothing on this product is embroidered.')}</div>` : ''}
          ${files.map((f, i) => `<div class="od-file${f.missing ? ' missing' : ''}">
              <div class="od-file-head"><b>${esc(posName(f.position))}</b>
                <span class="muted">${f.missing ? 'No DST attached yet' : esc(f.filename || 'design.dst')}</span>
                ${f.missing ? '' : `<button class="btn btn-outline btn-sm" data-open="${i}">Open</button>`}</div>
              ${f.missing ? '' : threadRows(f.threads)}
            </div>`).join('') || (p.noDst || info.warning ? '' : '<p class="hint">No embroidered positions on this order.</p>')}
          ${openable.length > 1 ? `<div class="od-all"><button class="btn btn-primary" id="od-all">Open all ${openable.length}</button></div>` : ''}`;
        out.querySelectorAll('[data-open]').forEach(b => b.onclick = () => { toOpen = [files[+b.dataset.open]]; back.querySelector('[data-x]').click(); });
        const all = q('#od-all');
        if (all) all.onclick = () => { toOpen = openable; back.querySelector('[data-x]').click(); };
      };

      const go = async () => {
        const n = num.value.trim().replace(/^#/, '');
        if (!/^\d{1,12}$/.test(n)) { toast('Order numbers are digits only', 'warn'); num.focus(); return; }
        lastNumber = n;
        out.innerHTML = '<p class="hint">Looking up…</p>';
        q('#od-go').disabled = true;
        try {
          info = await api.LookupOrder(n);
          render();
          q('#od-mark').textContent = info.marked ? 'Marked as printed' : '';
          // one file: go straight to the Open button
          const b = out.querySelector('[data-open]');
          if (b) b.focus();
        } catch (e) {
          info = null;
          out.innerHTML = `<div class="od-msg bad">${esc(e.message || String(e))}</div>`;
        }
        q('#od-go').disabled = false;
      };
      q('#od-go').onclick = go;
      num.onkeydown = e => { if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); go(); } };
      setTimeout(() => { num.focus(); num.select(); }, 0);
    },
  });
  if (!info || !toOpen.length) return;
  let ok = false;
  for (const f of toOpen) {
    const r = await run(() => api.OpenOrderFile(info.order.number, f));
    if (r) ok = true;
  }
  if (ok) { R.fit(S.view.hoop); updateZoomLabel(); redraw(); }
}
