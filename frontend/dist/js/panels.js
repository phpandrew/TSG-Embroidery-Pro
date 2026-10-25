// Right-hand panel: Properties (Design tab), Lettering editor, Production settings.
import { api } from './api.js';
import { icon } from './icons.js';
import { esc, fmt, mm, numField, toast } from './ui.js';
import { S, run, runLive, selected, setTab, renderPropsPanel, redraw, actions } from './main.js';
import { openFontGallery, fontOptions, loadFavourites } from './fontgallery.js';

export function renderProps(root) {
  root.innerHTML = '';
  if (!S.view || !S.propsOpen) return;
  if (S.tab === 'lettering') return letteringPanel(root);
  if (S.tab === 'production') return productionPanel(root);
  return designPanel(root);
}

function head(root, title) {
  const h = document.createElement('div');
  h.className = 'props-head';
  h.innerHTML = `<h2>${esc(title)}</h2><button class="icon-btn" title="Close panel">${icon('close', 18)}</button>`;
  h.querySelector('button').onclick = () => { S.propsOpen = false; document.querySelector('.main').classList.add('no-props'); window.dispatchEvent(new Event('resize')); };
  root.appendChild(h);
}

function section(root, title, html = '') {
  const s = document.createElement('div');
  s.className = 'sec';
  s.innerHTML = (title ? `<h3>${esc(title)}</h3>` : '') + html;
  root.appendChild(s);
  return s;
}

// ------------------------------------------------------------- design tab
function designPanel(root) {
  head(root, 'Properties');
  const d = selected();
  const name = document.createElement('div');
  name.className = 'props-name';
  name.textContent = d ? d.name : 'No selection';
  root.appendChild(name);

  const tabs = document.createElement('div');
  tabs.className = 'subtabs';
  tabs.innerHTML = `<button data-p="transform">Transform</button><button data-p="thread">Thread</button>`;
  tabs.querySelectorAll('button').forEach(b => {
    b.classList.toggle('on', b.dataset.p === S.propTab);
    b.onclick = () => { S.propTab = b.dataset.p; renderPropsPanel(); };
  });
  root.appendChild(tabs);

  if (S.propTab === 'thread') {
    threadPanel(root, d);
  } else {
    if (d) { transformSections(root, d); densitySection(root, d); }
    else section(root, '', '<p class="hint">Select a design on the canvas or in the sequence list to move, resize, rotate or recolour it.</p>');
    needleTable(root);
  }
  summary(root);
}

function transformSections(root, d) {
  const b = d.bounds;
  const isText = d.kind === 'lettering';
  const s = section(root, 'Size & position');
  const g = document.createElement('div');
  g.className = 'grid2';
  const w = numField({ label: 'Width', value: (b.MaxX - b.MinX) / 10, unit: 'mm', step: 1, onCommit: v => run(() => api.SetSize(d.id, v * 10, 0, S.lock)) });
  const h = numField({ label: 'Height', value: (b.MaxY - b.MinY) / 10, unit: 'mm', step: 1, onCommit: v => run(() => api.SetSize(d.id, 0, v * 10, S.lock)) });
  const lock = document.createElement('button');
  lock.className = 'lock' + (S.lock ? '' : ' off');
  lock.title = S.lock ? 'Aspect ratio locked' : 'Aspect ratio unlocked';
  lock.innerHTML = icon(S.lock ? 'lock' : 'unlock', 16);
  lock.onclick = () => { S.lock = !S.lock; renderPropsPanel(); };
  const x = numField({ label: 'X', value: (b.MinX + b.MaxX) / 20, unit: 'mm', step: 1, onCommit: v => run(() => api.MoveTo(d.id, v * 10, (b.MinY + b.MaxY) / 2)) });
  const y = numField({ label: 'Y', value: (b.MinY + b.MaxY) / 20, unit: 'mm', step: 1, onCommit: v => run(() => api.MoveTo(d.id, (b.MinX + b.MaxX) / 2, v * 10)) });
  g.append(w, lock, h, x, document.createElement('span'), y);
  s.appendChild(g);
  const rr = document.createElement('div');
  rr.className = 'row-inline';
  rr.innerHTML = '<span>Rotation</span>';
  rr.appendChild(numField({ value: d.transform.rotation, unit: '°', step: 1, onCommit: v => run(() => api.SetRotation(d.id, v)) }));
  s.appendChild(rr);

  const rs = section(root, 'Resize');
  if (isText) {
    rs.insertAdjacentHTML('beforeend', `<p class="note">Lettering is regenerated at the new size (satin density kept). Change height here or on the Lettering tab.</p>`);
    const hr = document.createElement('div');
    hr.className = 'row-inline';
    hr.innerHTML = '<span>Height</span>';
    hr.appendChild(numField({ value: d.lettering.height / 10, unit: 'mm', step: 0.5, onCommit: v => run(() => api.UpdateLettering(d.id, { ...d.lettering, height: v * 10 })) }));
    rs.appendChild(hr);
    return;
  }
  const pct = d.scalePct;
  const sr = document.createElement('div');
  sr.className = 'row-inline';
  sr.style.marginTop = '0';
  sr.innerHTML = '<span>Scale</span>';
  sr.appendChild(numField({ value: pct, unit: '%', step: 1, decimals: 0, onCommit: v => run(() => api.SetScale(d.id, v)) }));
  rs.appendChild(sr);
  const sl = document.createElement('div');
  sl.className = 'slider';
  const clamped = Math.max(85, Math.min(115, pct));
  sl.innerHTML = `<span>85%</span><input type="range" min="85" max="115" step="1" value="${clamped}" style="--p:${((clamped - 85) / 30) * 100}%"><span>115%</span>`;
  const range = sl.querySelector('input');
  range.oninput = () => { range.style.setProperty('--p', ((range.value - 85) / 30) * 100 + '%'); };
  range.onchange = () => run(() => api.SetScale(d.id, +range.value));
  rs.appendChild(sl);
  const out = Math.abs(pct - 100) > 15;
  rs.insertAdjacentHTML('beforeend', out
    ? `<p class="note warn">${icon('warn', 14)} ${pct.toFixed(0)}% is beyond the safe ±15% for plain scaling — expect gaps or bunching. Test sew first.</p>`
    : `<p class="note">Coordinate scaling · <u title="Stitches are moved, not regenerated">stitch count unchanged</u></p>`);
}

function densitySection(root, d) {
  const s = section(root, 'Density');
  const scale = d.kind === 'lettering' ? 100 : d.scalePct;
  s.insertAdjacentHTML('beforeend', `
    <div class="slider"><span>60%</span><input type="range" min="60" max="150" step="5" value="100" style="--p:${(40 / 90) * 100}%"><span>150%</span></div>
    <div class="row-inline" style="margin-top:0"><span>Target</span><b class="dens-val">100%</b>
      <button class="btn btn-primary btn-sm dens-apply" style="margin-left:auto">Apply</button></div>
    ${Math.abs(scale - 100) > 2 ? `<button class="btn btn-ghost btn-sm dens-match" style="margin-top:8px">Match density to ${Math.round(scale)}% scale</button>` : ''}
    <p class="note" style="margin-top:8px">${d.kind === 'lettering'
      ? 'Regenerates the lettering with a new satin spacing.'
      : 'Rebuilds satin columns and tatami fills at the new spacing; running stitches and underlay are left alone. Test sew first.'}</p>`);
  const range = s.querySelector('input[type=range]');
  const val = s.querySelector('.dens-val');
  range.oninput = () => {
    range.style.setProperty('--p', ((range.value - 60) / 90) * 100 + '%');
    val.textContent = range.value + '%';
  };
  s.querySelector('.dens-apply').onclick = () => {
    const f = +range.value / 100;
    if (Math.abs(f - 1) < 0.001) { toast('Pick a density other than 100%'); return; }
    run(() => api.AdjustDensity(d.id, f));
  };
  const m = s.querySelector('.dens-match');
  // after scaling by s, spacing grew by s: s × density restores the original
  if (m) m.onclick = () => run(() => api.AdjustDensity(d.id, scale / 100));
}

function needleTable(root) {
  const v = S.view;
  const tmap = new Map(v.threads.map(t => [t.id, t]));
  const s = section(root, 'Needle assignments');
  const rows = [];
  v.designs.forEach(d => d.blocks.forEach((b, bi) => rows.push({ d, b, bi })));
  if (!rows.length) { s.insertAdjacentHTML('beforeend', '<p class="hint">No colour blocks yet.</p>'); return; }
  const opts = v.threads.map(t => `<option value="${t.id}">${esc(t.name)}</option>`).join('');
  s.insertAdjacentHTML('beforeend', `<table class="tbl"><thead><tr><th style="width:62px">Block</th><th>Thread</th><th style="width:84px">Needle</th></tr></thead><tbody>${rows.map(({ d, b, bi }) => {
    const t = tmap.get(b.thread) || { color: '#888', needle: 0 };
    return `<tr data-d="${d.id}" data-b="${bi}" data-t="${t.id}"><td>${b.index}</td>
      <td class="th-cell"><span class="sw" style="background:${esc(t.color)}"></span><span class="cell-select"><select>${opts}</select></span></td>
      <td><input type="number" min="1" max="${v.settings.needles}" value="${t.needle}"></td></tr>`;
  }).join('')}</tbody></table><p class="note" style="margin-top:6px">Colors saved with project</p>`);
  s.querySelectorAll('tbody tr').forEach(tr => {
    const sel = tr.querySelector('select');
    sel.value = tr.dataset.t;
    sel.onchange = () => run(() => api.SetBlockThread(tr.dataset.d, +tr.dataset.b, sel.value));
    const inp = tr.querySelector('input');
    inp.onchange = () => {
      const t = tmap.get(tr.dataset.t);
      if (t) run(() => api.SetThread({ ...t, needle: parseInt(inp.value, 10) }));
    };
  });
}

function threadPanel(root, d) {
  const v = S.view;
  const s = section(root, 'Thread palette');
  s.insertAdjacentHTML('beforeend', '<p class="hint" style="margin-top:-4px">DST files carry no colours. Name each thread and set the needle it sits on; this is saved with the project and printed on the worksheet.</p>');
  for (const t of v.threads) {
    const r = document.createElement('div');
    r.className = 'thread-row form';
    r.innerHTML = `<input type="color" value="${esc(t.color)}" title="Colour"><input class="inp" value="${esc(t.name)}" title="Thread name / code">
      <input class="inp" type="number" min="1" max="${v.settings.needles}" value="${t.needle}" title="Needle"><span class="muted" title="Needle">#</span>`;
    const [col, nm, nd] = r.querySelectorAll('input');
    const save = () => run(() => api.SetThread({ ...t, color: col.value, name: nm.value, needle: parseInt(nd.value, 10) }));
    col.onchange = save; nm.onchange = save; nd.onchange = save;
    s.appendChild(r);
  }
  const add = document.createElement('button');
  add.className = 'btn btn-ghost btn-sm';
  add.innerHTML = `${icon('plus', 16)} Add thread`;
  add.onclick = () => {
    const used = new Set(v.threads.map(t => t.needle));
    let n = 1; while (used.has(n) && n < v.settings.needles) n++;
    run(() => api.SetThread({ id: '', name: 'New thread', color: '#888888', needle: n, brand: '', code: '' }));
  };
  s.appendChild(add);
  if (d) {
    const bs = section(root, `Blocks in ${d.name}`);
    const opts = v.threads.map(t => `<option value="${t.id}">${esc(t.name)} — needle ${t.needle}</option>`).join('');
    d.blocks.forEach((b, i) => {
      const row = document.createElement('div');
      row.className = 'form';
      row.style.marginBottom = '6px';
      row.innerHTML = `<label class="f">Block ${b.index} · ${fmt(b.stitches)} stitches<select class="inp">${opts}</select></label>`;
      const sel = row.querySelector('select');
      sel.value = b.thread;
      sel.onchange = () => run(() => api.SetBlockThread(d.id, i, sel.value));
      bs.appendChild(row);
    });
  }
}

function summary(root) {
  const sm = S.view.summary;
  const s = section(root, 'Design summary');
  const mins = sm.sewMinutes;
  s.insertAdjacentHTML('beforeend', `<div class="kv">
    <span>Size</span><span>${sm.width.toFixed(0)} × ${sm.height.toFixed(0)} mm</span>
    <span>Stitches</span><span>${fmt(sm.stitches)}</span>
    <span>Color changes</span><span>${sm.colorChanges}</span>
    <span>Trims</span><span>${sm.trims}</span>
    <span>Est. sew time</span><span>~${mins < 1 ? '<1' : Math.round(mins)} min</span>
  </div>
  ${sm.withinHoop
    ? `<div class="badge ok">${icon('check', 22)} Within hoop</div>`
    : `<div class="badge bad">${icon('warn', 22)} Outside ${esc(S.view.hoop.name)} hoop</div>`}`);
}

// ------------------------------------------------------------- lettering tab
function letteringPanel(root) {
  head(root, 'Lettering');
  const d = selected();
  const editing = d && d.kind === 'lettering';
  const spec = editing ? d.lettering : (S.lastSpec || { text: '', font: 'Block', height: 180, spacing: 0, weight: 17, density: 4 });
  const v = S.view;
  const s = section(root, editing ? `Edit “${d.name}”` : 'New lettering');
  const fontOpts = fontOptions(v.fonts, spec.font || 'Block', !!S.showWinFonts);
  const thrOpts = v.threads.map(t => `<option value="${t.id}">${esc(t.name)} — needle ${t.needle}</option>`).join('');
  s.insertAdjacentHTML('beforeend', `<div class="form">
    <label class="f">Text <span class="muted">(Enter for a new line)</span><textarea class="inp" id="lt-text" rows="2" placeholder="e.g. ANDREW" maxlength="400">${esc(spec.text)}</textarea></label>
    <div class="two">
      <label class="f">Align<div class="seg lt-align"><button type="button" data-a="left">Left</button><button type="button" data-a="center">Centre</button><button type="button" data-a="right">Right</button></div></label>
      <label class="f ml-only">Line spacing (% of height)<input class="inp" id="lt-ls" type="number" step="5" min="80" max="400" value="${spec.lineSpacing || 150}"></label>
    </div>
    <label class="f">Curve<div class="seg lt-arc"><button type="button" data-c="">Straight</button><button type="button" data-c="up">Arc up</button><button type="button" data-c="down">Arc down</button></div></label>
    <div class="two arc-only">
      <label class="f">Arc radius (mm)<input class="inp" id="lt-arcr" type="number" step="5" min="10" max="1000" placeholder="Auto" value="${spec.arcRadius ? (spec.arcRadius / 10).toFixed(0) : ''}"></label>
      <p class="hint" style="align-self:end;margin:0 0 8px">Leave blank for auto. Smaller = tighter curve.</p>
    </div>
    <label class="f">Font<div class="font-row"><select class="inp" id="lt-font">${fontOpts}</select>
      <button type="button" class="btn btn-ghost btn-sm" id="lt-browse" style="height:38px">${icon('grid', 15)} Browse…</button></div>
      <span class="check sm"><input type="checkbox" id="lt-win" ${S.showWinFonts ? 'checked' : ''}> Include Windows fonts</span></label>
    <div class="two">
      <label class="f">Cap height (mm)<input class="inp" id="lt-h" type="number" step="0.5" min="3" max="300" value="${(spec.height / 10).toFixed(1)}"></label>
      <label class="f">Letter spacing (mm)<input class="inp" id="lt-sp" type="number" step="0.1" value="${((spec.spacing || 0) / 10).toFixed(1)}"></label>
    </div>
    <div class="two sys-only">
      <label class="f">Style<select class="inp" id="lt-style">
        <option value="fill">Fill (tatami)</option>
        <option value="fill+outline">Fill + satin outline</option>
        <option value="satin">Satin outline only</option>
        <option value="run">Running outline only</option></select></label>
      <label class="f">Fill angle (°)<input class="inp" id="lt-angle" type="number" step="5" min="-90" max="90" value="${spec.fillAngle ?? 45}"></label>
    </div>
    <div class="two">
      <label class="f">Stroke weight (% of height)<input class="inp" id="lt-w" type="number" step="1" min="6" max="30" value="${spec.weight || 17}"></label>
      <label class="f">Satin density (mm)<input class="inp" id="lt-den" type="number" step="0.05" min="0.25" max="1" value="${((spec.density || 4) / 10).toFixed(2)}"></label>
    </div>
    ${editing ? '' : `<label class="f">Thread<select class="inp" id="lt-thread"><option value="">First palette thread</option>${thrOpts}</select></label>`}
    <div class="btn-row">
      ${editing ? '<span class="muted" style="align-self:center">Changes apply as you type</span><button class="btn btn-ghost btn-sm" id="lt-add">Add as new copy</button>' : '<button class="btn btn-primary btn-sm" id="lt-add">Add to design</button>'}
    </div>
  </div>`);
  const fontSel = s.querySelector('#lt-font');
  fontSel.value = spec.font || 'Block';
  const applyFont = name => {
    const f = v.fonts.find(x => x.name === name);
    if (![...fontSel.options].some(o => o.value === name)) fontSel.innerHTML = fontOptions(v.fonts, name, !!S.showWinFonts);
    fontSel.value = name;
    // digitized fonts sew best at their own size
    if (f && f.native) s.querySelector('#lt-h').value = (f.native / 10).toFixed(1);
    showFor(f);
  };
  fontSel.onchange = () => applyFont(fontSel.value);
  // show only the settings that apply to the chosen kind of font
  function showFor(f) {
    const kind = f ? f.kind : 'built-in';
    s.querySelector('#lt-w').closest('label').style.display = kind === 'built-in' ? '' : 'none';
    s.querySelector('#lt-den').closest('label').style.display = kind === 'digitized' ? 'none' : '';
    s.querySelector('.sys-only').style.display = kind === 'windows' ? '' : 'none';
  }
  s.querySelector('#lt-style').value = spec.style || 'fill';
  let arc = spec.arc || '';
  const arcBtns = s.querySelectorAll('.lt-arc button');
  const showArc = () => {
    arcBtns.forEach(b => b.classList.toggle('on', b.dataset.c === arc));
    s.querySelector('.arc-only').style.display = arc ? '' : 'none';
  };
  arcBtns.forEach(b => b.onclick = e => { e.preventDefault(); arc = b.dataset.c; showArc(); live(); });
  showArc();
  let align = spec.align || 'center';
  const alignBtns = s.querySelectorAll('.lt-align button');
  const showAlign = () => alignBtns.forEach(b => b.classList.toggle('on', b.dataset.a === align));
  alignBtns.forEach(b => b.onclick = e => { e.preventDefault(); align = b.dataset.a; showAlign(); live(); });
  showAlign();
  s.querySelector('#lt-win').onchange = e => {
    S.showWinFonts = e.target.checked;
    const cur = fontSel.value;
    fontSel.innerHTML = fontOptions(v.fonts, cur, S.showWinFonts);
    fontSel.value = cur;
  };
  const txt = s.querySelector('#lt-text');
  const fitText = () => {
    const n = txt.value.split('\n').length;
    txt.rows = Math.min(Math.max(n, 2), 8);
    s.querySelector('.ml-only').style.visibility = n > 1 ? '' : 'hidden';
  };
  txt.addEventListener('input', fitText);
  fitText();
  showFor(v.fonts.find(x => x.name === fontSel.value));
  s.querySelector('#lt-browse').onclick = async () => {
    const f = await openFontGallery({ text: s.querySelector('#lt-text').value, current: fontSel.value });
    if (f) applyFont(f.name);
    if (f && editing) { await run(() => api.UpdateLettering(d.id, read())); return; }
    S.lastSpec = read();
    renderPropsPanel(); // refresh the favourites group in the font list
  };
  if (!S._favLoaded) { S._favLoaded = true; loadFavourites().then(() => renderPropsPanel()); }
  const read = () => ({
    text: s.querySelector('#lt-text').value,
    font: s.querySelector('#lt-font').value,
    height: parseFloat(s.querySelector('#lt-h').value) * 10,
    spacing: parseFloat(s.querySelector('#lt-sp').value || '0') * 10,
    weight: parseFloat(s.querySelector('#lt-w').value),
    density: parseFloat(s.querySelector('#lt-den').value) * 10,
    style: s.querySelector('#lt-style').value,
    fillAngle: parseFloat(s.querySelector('#lt-angle').value) || 0,
    lineSpacing: parseFloat(s.querySelector('#lt-ls').value) || 150,
    align,
    arc,
    arcRadius: (parseFloat(s.querySelector('#lt-arcr').value) || 0) * 10,
  });
  s.querySelector('#lt-add').onclick = async () => {
    const sp = read();
    if (!sp.text.trim()) { toast('Enter some text'); return; }
    S.lastSpec = sp;
    const thr = s.querySelector('#lt-thread');
    const r = await run(() => api.AddLettering(sp));
    if (r && thr && thr.value) await run(() => api.SetBlockThread(r.id, 0, thr.value));
  };
  // editing: every change re-lays-out the lettering on the canvas as you type
  let liveTimer = null, liveBusy = false, liveAgain = false;
  const sendLive = async () => {
    if (liveBusy) { liveAgain = true; return; }
    const sp = read();
    if (!sp.text.trim() || !(sp.height >= 30)) return;
    liveBusy = true;
    const r = await runLive(() => api.UpdateLettering(d.id, sp));
    liveBusy = false;
    if (r) {
      S.lastSpec = sp;
      const h = s.querySelector('h3');
      if (h) h.textContent = `Edit “${sp.text.split('\n').filter(l => l.trim()).join(' / ')}”`;
    }
    if (liveAgain) { liveAgain = false; sendLive(); }
  };
  const live = () => { if (!editing) return; clearTimeout(liveTimer); liveTimer = setTimeout(sendLive, 250); };
  if (editing) {
    s.querySelectorAll('input.inp, textarea.inp').forEach(el => el.addEventListener('input', live));
    s.querySelectorAll('select.inp').forEach(el => el.addEventListener('change', live));
  }
  // Enter = new line; Ctrl+Enter = add (or apply now when editing)
  s.querySelector('#lt-text').addEventListener('keydown', e => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      if (editing) { clearTimeout(liveTimer); sendLive(); } else s.querySelector('#lt-add').click();
    }
  });

  section(root, 'About lettering', `<p class="hint"><b>Embroidery fonts</b> (from the Ink/Stitch library) are professionally digitized: best quality, use them near their listed size; resizing rebuilds density automatically.<br>
    <b>Windows fonts</b> are any installed TrueType/OpenType font, auto-digitized as a tatami fill (or outline). Best for bold, block or varsity styles from about 15 mm; thin and script fonts don't fill well.<br>
    <b>Block</b> is a built-in satin font for names and numbers from about 8 mm.<br>
    Your own digitized fonts can go in <code>${esc(S.info ? S.info.fontDir : '')}</code>.</p>`);
  summary(root);
}

// ------------------------------------------------------------- production tab
function productionPanel(root) {
  head(root, 'Production');
  const v = S.view;
  const st = v.settings;
  const s = section(root, 'Machine');
  s.insertAdjacentHTML('beforeend', `<div class="form">
    <label class="f">Machine name<input class="inp" id="pr-machine" value="${esc(st.machine)}"></label>
    <div class="two">
      <label class="f">Needles<input class="inp" id="pr-needles" type="number" min="1" max="20" value="${st.needles}"></label>
      <label class="f">Avg. speed (SPM)<input class="inp" id="pr-spm" type="number" min="100" max="2000" step="10" value="${st.spm}"></label>
    </div>
    <div class="two">
      <label class="f">Seconds per trim<input class="inp" id="pr-ts" type="number" min="0" step="0.5" value="${st.trimSeconds}"></label>
      <label class="f">Seconds per colour change<input class="inp" id="pr-cs" type="number" min="0" step="0.5" value="${st.colorSeconds}"></label>
    </div>
  </div>`);
  const o = section(root, 'DST output');
  o.insertAdjacentHTML('beforeend', `<div class="form">
    <div class="two">
      <label class="f">Jump codes per trim<input class="inp" id="pr-tj" type="number" min="1" max="8" value="${st.trimJumps}" title="Match the TFHX jump-trim setting"></label>
      <label class="f">Trim connectors over (mm)<input class="inp" id="pr-ta" type="number" min="0" max="50" step="0.5" value="${(st.trimAbove / 10).toFixed(1)}"></label>
    </div>
    <label class="f">Start point<select class="inp" id="pr-start"><option value="design">Design centre (Tajima convention)</option><option value="hoop">Hoop centre</option></select></label>
    <label class="f">Hoop<select class="inp" id="pr-hoop">${v.hoops.map(h => `<option value="${h.id}">${esc(h.name)}</option>`).join('')}</select></label>
    <p class="hint">Tajima machines trim when they see this many consecutive jump codes. Set it to match the machine, or trims will be missed or added.</p>
  </div>`);
  o.querySelector('#pr-start').value = st.startAt;
  o.querySelector('#pr-hoop').value = st.hoop;
  const save = () => {
    const q = id => root.querySelector(id);
    run(() => api.SetSettings({
      ...st,
      machine: q('#pr-machine').value,
      needles: parseInt(q('#pr-needles').value, 10),
      spm: parseInt(q('#pr-spm').value, 10),
      trimSeconds: parseFloat(q('#pr-ts').value),
      colorSeconds: parseFloat(q('#pr-cs').value),
      trimJumps: parseInt(q('#pr-tj').value, 10),
      trimAbove: parseFloat(q('#pr-ta').value) * 10,
      startAt: q('#pr-start').value,
      hoop: q('#pr-hoop').value,
    }));
  };
  root.querySelectorAll('.sec input, .sec select').forEach(el => { el.onchange = save; });

  const q = section(root, 'Needle sequence');
  q.insertAdjacentHTML('beforeend', v.sequence.length ? `<table class="tbl seq-table"><thead><tr><th>Stop</th><th>Needle</th><th>Thread</th><th>Stitches</th></tr></thead><tbody>${v.sequence.map(e => {
    const t = v.threads.find(t => t.name === e.thread);
    return `<tr><td>${e.step}</td><td>${e.needle}</td><td><span class="sw" style="background:${esc(t ? t.color : '#888')}"></span>${esc(e.thread)}</td><td>${fmt(e.stitches)}</td></tr>`;
  }).join('')}</tbody></table>` : '<p class="hint">Nothing to sew yet.</p>');
  const act = section(root, '');
  act.innerHTML = `<div class="btn-row"><button class="btn btn-primary btn-sm" data-a="export">${icon('export', 16)} Export DST</button><button class="btn btn-ghost btn-sm" data-a="worksheet">${icon('worksheet', 16)} Worksheet</button><button class="btn btn-ghost btn-sm" data-a="save">Save project</button></div>`;
  act.querySelectorAll('[data-a]').forEach(b => b.onclick = () => actions[b.dataset.a]());

  section(root, '', '<p class="hint">Startup, auto-open and update options are in <b>Tools → Settings</b>.</p>');
}
