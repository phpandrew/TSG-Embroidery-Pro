// Design browser: searchable grid of stitch previews for the embroidery
// files in a folder (Downloads by default). Click a card to open it.
import { api } from './api.js';
import { icon } from './icons.js';
import { esc, modal, toast } from './ui.js';
import { S, run, R, updateZoomLabel, redraw } from './main.js';

let lastQuery = '';
let lastSort = 'new';
const PAGE = 100; // cards shown at once; "Show more" adds another page

const ago = secs => {
  const d = Date.now() / 1000 - secs;
  if (d < 90) return 'just now';
  if (d < 3600) return `${Math.round(d / 60)} min ago`;
  if (d < 86400) return `${Math.round(d / 3600)} h ago`;
  if (d < 86400 * 7) return `${Math.round(d / 86400)} days ago`;
  return new Date(secs * 1000).toLocaleDateString();
};

export async function openDesignBrowser() {
  let listing;
  try { listing = await api.BrowseDesigns(''); } catch (e) { listing = { dir: '', files: [], isDefault: true, err: e.message }; }
  let query = lastQuery, sort = lastSort;
  let limit = PAGE;
  let picked = null, merge = false;

  const body = `<div class="fg db">
    <div class="fg-bar">
      <label class="fg-search">${icon('search', 16)}<input id="db-q" placeholder="Search designs by name or folder" value="${esc(query)}"></label>
      <div class="seg db-sort"><button data-s="new">Newest</button><button data-s="name">Name</button></div>
      <span class="muted" id="db-count"></span>
    </div>
    <div class="db-folder">
      <code id="db-dir"></code>
      <button class="btn btn-ghost btn-sm" id="db-pick">Change folder…</button>
      <button class="btn btn-ghost btn-sm" id="db-dl">Downloads</button>
      <button class="btn btn-ghost btn-sm" id="db-refresh" title="Rescan">Refresh</button>
    </div>
    <div class="fg-grid db-grid" id="db-grid"></div>
  </div>`;

  await modal({
    title: 'Browse designs',
    wide: true,
    body,
    buttons: [{ label: 'Close', value: null }],
    onOpen: back => {
      back.querySelector('.modal').classList.add('fg-modal');
      const q = s => back.querySelector(s);
      const grid = q('#db-grid');
      const queue = [];
      let running = 0;
      const pump = () => {
        while (running < 3 && queue.length) {
          const card = queue.shift();
          if (!card.isConnected) continue;
          running++;
          const box = card.querySelector('.fg-img');
          const dpr = devicePixelRatio || 1;
          api.DesignPreview(card.dataset.path, Math.round(box.clientWidth * dpr) || 300, Math.round(box.clientHeight * dpr) || 200)
            .then(t => {
              box.innerHTML = `<img src="${t.image}" alt="">`;
              card.querySelector('.db-stats').innerHTML = `${t.widthMM.toFixed(0)} × ${t.heightMM.toFixed(0)} mm · ${t.stitches.toLocaleString()} st · ${t.colors} col`;
            })
            .catch(() => { box.textContent = 'Can’t preview'; })
            .finally(() => { running--; pump(); });
        }
      };
      const io = new IntersectionObserver(entries => {
        for (const e of entries) {
          if (!e.isIntersecting) continue;
          io.unobserve(e.target);
          queue.push(e.target);
          pump();
        }
      }, { root: grid, rootMargin: '400px' });

      const render = () => {
        io.disconnect();
        queue.length = 0;
        q('#db-dir').textContent = listing.dir || '—';
        q('#db-dir').title = listing.dir || '';
        q('#db-dl').hidden = !!listing.isDefault;
        back.querySelectorAll('.db-sort button').forEach(b => b.classList.toggle('on', b.dataset.s === sort));
        const words = query.toLowerCase().split(/\s+/).filter(Boolean);
        let list = (listing.files || []).filter(f => {
          const hay = (f.name + ' ' + f.folder + ' ' + f.ext).toLowerCase();
          return words.every(w => hay.includes(w));
        });
        if (sort === 'name') list = list.slice().sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
        const total = list.length;
        list = list.slice(0, limit);
        q('#db-count').textContent = total > list.length
          ? `Showing ${sort === 'new' ? 'newest ' : ''}${list.length} of ${total}`
          : `${total} of ${listing.files.length} design${listing.files.length === 1 ? '' : 's'}${listing.truncated ? ' (first 3000)' : ''}`;
        if (listing.err) { grid.innerHTML = `<p class="hint">${esc(listing.err)}</p>`; return; }
        grid.innerHTML = list.map(f => `<div class="fg-card db-card" tabindex="0" data-path="${esc(f.path)}" title="${esc(f.path)}">
            <div class="fg-img db-img"><span class="fg-loading"></span></div>
            <div class="fg-meta"><div class="db-text">
              <b>${esc(f.name)}</b> <span class="db-ext">${esc(f.ext.toUpperCase())}</span>
              <div class="muted db-stats">&nbsp;</div>
              <div class="muted">${f.folder ? esc(f.folder) + ' · ' : ''}${esc(ago(f.modified))}</div>
            </div>
            <button class="btn btn-ghost btn-sm db-merge" title="Add to the open design">Merge</button></div>
          </div>`).join('') + (total > list.length ? `<div class="db-more"><button class="btn btn-outline btn-sm" id="db-more">Show ${Math.min(PAGE, total - list.length)} more</button></div>` : '') || `<p class="hint">${listing.files.length ? 'No designs match.' : 'No DST, PES, PEC, JEF or EXP files in this folder (or the three folders below it).'}</p>`;
        const more = q('#db-more');
        if (more) more.onclick = () => { const y = grid.scrollTop; limit += PAGE; render(); grid.scrollTop = y; };
        grid.querySelectorAll('.db-card').forEach(card => {
          io.observe(card);
          const choose = m => { picked = card.dataset.path; merge = m; back.querySelector('[data-x]').click(); };
          card.onclick = () => choose(false);
          card.onkeydown = e => { if (e.key === 'Enter') choose(false); };
          card.querySelector('.db-merge').onclick = e => { e.stopPropagation(); choose(true); };
        });
      };
      const reload = p => p.then(l => { listing = l; render(); }).catch(e => toast(e.message || String(e), 'err'));
      q('#db-q').oninput = e => { query = lastQuery = e.target.value; limit = PAGE; render(); };
      q('#db-q').onkeydown = e => { if (e.key === 'Enter') e.stopPropagation(); };
      back.querySelectorAll('.db-sort button').forEach(b => b.onclick = () => { sort = lastSort = b.dataset.s; limit = PAGE; render(); });
      q('#db-pick').onclick = () => { limit = PAGE; reload(api.ChooseBrowseFolder()); };
      q('#db-dl').onclick = () => { limit = PAGE; reload(api.BrowseDownloads()); };
      q('#db-refresh').onclick = () => reload(api.BrowseDesigns(listing.dir));
      render();
      setTimeout(() => q('#db-q').focus(), 0);
    },
  });
  api.ReleaseMemory().catch(() => {});
  if (!picked) return;
  const canMerge = merge && S.view && S.view.designs.length > 0;
  const r = await run(() => api.OpenPath(picked, !canMerge));
  if (r && !canMerge) { R.fit(S.view.hoop); updateZoomLabel(); redraw(); }
}
