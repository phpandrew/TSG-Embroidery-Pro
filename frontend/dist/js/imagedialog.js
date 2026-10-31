// Image → embroidery dialog: live preview of the auto-digitized picture
// with size / colour / detail controls, then import as a design.
import { api } from './api.js';
import { esc, modal, toast } from './ui.js';
import { S, run, R, updateZoomLabel, redraw } from './main.js';

export async function openImageDialog(path) {
  let spec;
  try { spec = await api.ImageInfo(path); } catch (e) { toast(e.message || String(e), 'err'); return; }
  const name = path.split(/[\\/]/).pop();
  let busy = false, again = false, timer = null, last = null;
  let imported = false;

  const body = `<div class="imgd">
    <div class="imgd-left">
      <div class="imgd-views">
        <figure><img id="im-src" alt=""><figcaption>Picture</figcaption></figure>
        <figure><img id="im-red" alt=""><figcaption>Thread colours</figcaption></figure>
        <figure class="big"><img id="im-st" alt=""><figcaption>Stitches</figcaption></figure>
      </div>
      <div class="imgd-stats muted" id="im-stats">Converting…</div>
    </div>
    <div class="imgd-right form">
      <label class="f">Width (mm)<input class="inp" id="im-w" type="number" min="10" max="400" step="1" value="${spec.width}"></label>
      <label class="f">Thread colours <span class="muted" id="im-cn">${spec.colors}</span><input id="im-c" type="range" min="1" max="10" value="${spec.colors}"></label>
      <label class="check"><input type="checkbox" id="im-bg" ${spec.removeBackground ? 'checked' : ''}> Remove background <span class="muted">(colour around the edges)</span></label>
      <label class="f">Smallest detail to keep <span class="muted" id="im-dn">${spec.minDetail} mm²</span><input id="im-d" type="range" min="0.5" max="15" step="0.5" value="${spec.minDetail}"></label>
      <div class="two">
        <label class="f">Fill angle (°)<input class="inp" id="im-a" type="number" min="-90" max="90" step="5" value="${spec.angle}"></label>
        <label class="f">Density (mm)<input class="inp" id="im-den" type="number" min="0.3" max="0.8" step="0.05" value="${spec.density}"></label>
      </div>
      <label class="check"><input type="checkbox" id="im-sat" ${spec.noSatin ? '' : 'checked'}> Satin for thin shapes <span class="muted">(strokes, outlines under ~5 mm)</span></label>
      <label class="check"><input type="checkbox" id="im-ul" ${spec.underlay ? 'checked' : ''}> Underlay <span class="muted">(recommended)</span></label>
      <label class="f">Pull compensation (mm) <span class="muted">grows shapes so they sew to size</span><input class="inp" id="im-pc" type="number" min="0" max="1" step="0.05" value="${spec.pullComp ?? 0.25}"></label>
      <label class="check"><input type="checkbox" id="im-ol"> Running-stitch outline around each colour</label>
      <p class="hint">Works best on flat artwork: logos, clip art, text as a picture. Photos and gradients come out as blobs — reduce the colours. Always test-sew.</p>
    </div>
  </div>`;

  await modal({
    title: `Picture to embroidery — ${name}`,
    wide: true,
    body,
    buttons: [
      { label: 'Cancel', value: null },
      { label: 'Add to this design', value: 'merge' },
      { label: 'Open as new design', kind: 'primary', value: 'new' },
    ],
    onOpen: back => {
      back.querySelector('.modal').classList.add('imgd-modal');
      const q = s => back.querySelector(s);
      const read = () => {
        spec = {
          path, width: parseFloat(q('#im-w').value) || spec.width,
          colors: parseInt(q('#im-c').value, 10) || 4,
          removeBackground: q('#im-bg').checked,
          minDetail: parseFloat(q('#im-d').value) || 2,
          angle: parseFloat(q('#im-a').value) || 0,
          density: parseFloat(q('#im-den').value) || 0.4,
          outline: q('#im-ol').checked, underlay: q('#im-ul').checked,
          pullComp: parseFloat(q('#im-pc').value) || 0, noSatin: !q('#im-sat').checked,
        };
        q('#im-cn').textContent = spec.colors;
        q('#im-dn').textContent = spec.minDetail + ' mm²';
        return spec;
      };
      const refresh = async () => {
        if (busy) { again = true; return; }
        busy = true;
        q('#im-stats').textContent = 'Converting…';
        try {
          const box = q('#im-st');
          const r = await api.ImagePreview(read(), Math.round(box.clientWidth * (devicePixelRatio || 1)) || 600, Math.round(box.clientHeight * (devicePixelRatio || 1)) || 450);
          last = r;
          q('#im-red').src = r.reduced;
          q('#im-st').src = r.stitches;
          q('#im-stats').innerHTML = `${esc(r.widthMM.toFixed(1))} × ${esc(r.heightMM.toFixed(1))} mm · <b>${r.count.toLocaleString()}</b> stitches · ${r.blocks} colour${r.blocks === 1 ? '' : 's'} · ${r.satin} satin, ${r.fills} fill area${r.fills === 1 ? '' : 's'} · ${r.jumps} jump${r.jumps === 1 ? '' : 's'} ` +
            r.colors.map(c => `<i class="sw" style="background:${esc(c)}"></i>`).join('') + (r.warning ? `<br><span class="warn-text">${esc(r.warning)}</span>` : '');
        } catch (e) {
          last = null;
          q('#im-stats').textContent = e.message || String(e);
        }
        busy = false;
        if (again) { again = false; refresh(); }
      };
      const sched = () => { clearTimeout(timer); timer = setTimeout(refresh, 250); };
      back.querySelectorAll('input').forEach(el => { el.addEventListener('input', sched); el.addEventListener('change', sched); });
      // original image thumbnail: read via the backend preview on first run
      q('#im-src').src = '';
      api.ImageThumb(path).then(u => { q('#im-src').src = u; }).catch(() => {});
      refresh();
    },
  }).then(async choice => {
    if (!choice || imported) { api.ImageClose().catch(() => {}); return; }
    imported = true;
    const r = await run(() => api.ImportImage(spec, choice === 'new'));
    api.ImageClose().catch(() => {});
    if (r) { R.fit(S.view.hoop); updateZoomLabel(); redraw(); if (r.warning) toast(r.warning, 'warn'); }
  });
}
