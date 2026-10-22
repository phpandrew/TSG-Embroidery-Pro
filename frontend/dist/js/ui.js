// Small DOM helpers: escaping, toasts, modals, context menus, number fields.
import { icon } from './icons.js';

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

export function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

export const fmt = n => Number(n || 0).toLocaleString('en-US');
export const mm = (v, d = 1) => (Number(v) / 10).toFixed(d);

export function hydrateIcons(root = document) {
  for (const el of root.querySelectorAll('[data-ic]')) {
    if (el.dataset.done) continue;
    el.outerHTML = icon(el.dataset.ic, Number(el.dataset.size) || 18, el.className || '');
  }
}

export function toast(msg, kind = '') {
  const el = document.createElement('div');
  el.className = 'toast ' + kind;
  el.textContent = msg;
  $('#toasts').appendChild(el);
  setTimeout(() => el.remove(), kind === 'err' ? 6500 : 3200);
}

// modal({title, body (html), buttons:[{label, kind, value}], wide}) -> Promise<value|null>
export function modal({ title, body, buttons = [{ label: 'OK', kind: 'primary', value: true }], wide = false, onOpen }) {
  return new Promise(resolve => {
    const back = document.createElement('div');
    back.className = 'modal-back';
    back.innerHTML = `<div class="modal ${wide ? 'wide' : ''}" role="dialog" aria-modal="true">
      <div class="modal-head"><h3>${esc(title)}</h3><button class="icon-btn" data-x>${icon('close', 18)}</button></div>
      <div class="modal-body">${body}</div>
      <div class="modal-foot">${buttons.map((b, i) => `<button class="btn btn-sm btn-${b.kind || 'ghost'}" data-i="${i}">${esc(b.label)}</button>`).join('')}</div>
    </div>`;
    const close = v => { back.remove(); document.removeEventListener('keydown', key); resolve(v); };
    const key = e => {
      if (back !== $('#modalRoot').lastElementChild) return; // a dialog opened on top handles its own keys
      if (e.key === 'Escape') close(null);
      if (e.key === 'Enter' && (e.target.tagName !== 'TEXTAREA' || e.ctrlKey || e.metaKey)) {
        const primary = buttons.findIndex(b => b.kind === 'primary');
        if (primary >= 0) { e.preventDefault(); close(resolveValue(buttons[primary])); }
      }
    };
    const resolveValue = b => (typeof b.value === 'function' ? b.value(back) : b.value);
    back.addEventListener('mousedown', e => { if (e.target === back) close(null); });
    back.querySelector('[data-x]').onclick = () => close(null);
    back.querySelectorAll('[data-i]').forEach(btn => { btn.onclick = () => close(resolveValue(buttons[+btn.dataset.i])); });
    document.addEventListener('keydown', key);
    $('#modalRoot').appendChild(back);
    if (onOpen) onOpen(back);
    const f = back.querySelector('textarea, input, select');
    if (f) { f.focus(); if (f.select) f.select(); }
  });
}

export async function prompt(title, label, value = '', type = 'text', extra = '') {
  return modal({
    title,
    body: `<div class="form"><label class="f">${esc(label)}<input class="inp" type="${type}" value="${esc(value)}" ${extra}></label></div>`,
    buttons: [{ label: 'Cancel', value: null }, { label: 'OK', kind: 'primary', value: b => b.querySelector('input').value }],
  });
}

// context menu: items [{label, kbd, action, danger} | '-']
export function contextMenu(x, y, items) {
  const m = $('#ctxMenu');
  m.innerHTML = `<div class="menu">${items.map((it, i) => it === '-' ? '<hr>' :
    `<button data-i="${i}" class="${it.danger ? 'danger' : ''}"><span>${esc(it.label)}</span>${it.kbd ? `<kbd>${esc(it.kbd)}</kbd>` : ''}</button>`).join('')}</div>`;
  m.hidden = false;
  const r = m.firstElementChild.getBoundingClientRect();
  m.style.left = Math.min(x, innerWidth - r.width - 8) + 'px';
  m.style.top = Math.min(y, innerHeight - r.height - 8) + 'px';
  const close = () => { m.hidden = true; document.removeEventListener('mousedown', outside, true); };
  const outside = e => { if (!m.contains(e.target)) close(); };
  setTimeout(() => document.addEventListener('mousedown', outside, true));
  m.querySelectorAll('[data-i]').forEach(b => { b.onclick = () => { close(); items[+b.dataset.i].action(); }; });
}

// Number field with spinner; calls onCommit(value) on Enter, blur or spin.
export function numField({ label, value, unit = '', step = 1, decimals = 1, onCommit, disabled = false, width }) {
  const wrap = document.createElement('label');
  wrap.className = 'fld';
  wrap.innerHTML = `${label ? `<span>${esc(label)}</span>` : ''}<div class="num"${width ? ` style="width:${width}px"` : ''}>
    <input type="text" inputmode="decimal" value="${Number(value).toFixed(decimals)}" ${disabled ? 'disabled' : ''}>
    ${unit ? `<span class="unit">${esc(unit)}</span>` : ''}
    <div class="spin"><button type="button" tabindex="-1" data-d="1">${icon('up', 12)}</button><button type="button" tabindex="-1" data-d="-1">${icon('down', 12)}</button></div></div>`;
  const inp = wrap.querySelector('input');
  let last = Number(value);
  const commit = v => {
    if (!Number.isFinite(v) || Math.abs(v - last) < 1e-9) { inp.value = last.toFixed(decimals); return; }
    last = v;
    inp.value = v.toFixed(decimals);
    onCommit(v);
  };
  inp.addEventListener('keydown', e => {
    if (e.key === 'Enter') { e.preventDefault(); commit(parseFloat(inp.value)); inp.blur(); }
    if (e.key === 'Escape') { inp.value = last.toFixed(decimals); inp.blur(); }
    if (e.key === 'ArrowUp' || e.key === 'ArrowDown') { e.preventDefault(); commit(last + (e.key === 'ArrowUp' ? step : -step)); }
  });
  inp.addEventListener('blur', () => commit(parseFloat(inp.value)));
  wrap.querySelectorAll('[data-d]').forEach(b => {
    b.onmousedown = e => e.preventDefault();
    b.onclick = e => { e.preventDefault(); if (!disabled) commit(+(last + step * +b.dataset.d).toFixed(6)); };
  });
  return wrap;
}
