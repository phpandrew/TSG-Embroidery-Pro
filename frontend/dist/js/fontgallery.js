// Font gallery: searchable grid of every lettering font with a live preview
// of your text, favourites (stars) saved per user.
import { api } from './api.js';
import { icon } from './icons.js';
import { esc, modal } from './ui.js';

const cache = new Map(); // `${font}\u0000${text}` -> data URL
let favourites = null;

export async function loadFavourites() {
  if (favourites) return favourites;
  try { favourites = new Set((await api.GetPrefs()).favoriteFonts || []); } catch { favourites = new Set(); }
  return favourites;
}

export function isFavourite(name) { return favourites ? favourites.has(name) : false; }

// fontOptions builds <option>s for a font dropdown: favourites first, then
// embroidery fonts. Windows fonts are listed only when withWindows is set
// (the current font is always kept so the dropdown can show it).
export function fontOptions(fonts, current, withWindows = false) {
  const opt = f => `<option value="${esc(f.name)}">${esc(f.name)}${f.native ? ` — ${(f.native / 10).toFixed(0)} mm` : f.kind === 'windows' ? ' (Windows)' : ' (built-in)'}</option>`;
  const list = fonts.filter(f => withWindows || f.kind !== 'windows' || f.name === current);
  const favs = list.filter(f => isFavourite(f.name));
  const label = withWindows ? 'All fonts' : 'Embroidery fonts';
  return (favs.length ? `<optgroup label="★ Favourites">${favs.map(opt).join('')}</optgroup><optgroup label="${label}">` : '') +
    list.map(opt).join('') + (favs.length ? '</optgroup>' : '');
}

// openFontGallery({ text, current }) -> Promise<FontInfo|null>
export async function openFontGallery({ text = '', current = '' } = {}) {
  const fonts = await api.Fonts();
  await loadFavourites();
  let picked = null;
  let previewText = text.trim() || 'Andrew 23';
  let query = '';
  let favOnly = false;
  let kind = 'embroidery'; // all | embroidery | windows

  const body = `<div class="fg">
    <div class="fg-bar">
      <label class="fg-search">${icon('search', 16)}<input id="fg-q" placeholder="Search fonts (name, style: script, block, serif…)"></label>
      <label class="fg-text">Preview<input id="fg-t" value="${esc(previewText)}" maxlength="40"></label>
      <div class="seg fg-kind"><button data-k="all">All</button><button data-k="embroidery" class="on">Embroidery fonts</button><button data-k="windows">Windows fonts</button></div>
      <button class="fg-chip" id="fg-fav">${icon('star', 15)} Favourites</button>
      <span class="muted" id="fg-count"></span>
    </div>
    <div class="fg-grid" id="fg-grid"></div>
  </div>`;

  return new Promise(resolve => {
    modal({
      title: 'Choose a font',
      wide: true,
      body,
      buttons: [{ label: 'Close', value: null }],
      onOpen: back => {
        back.querySelector('.modal').classList.add('fg-modal');
        const grid = back.querySelector('#fg-grid');
        const queue = [];
        let running = 0;
        const pump = () => {
          while (running < 3 && queue.length) {
            const { card, name } = queue.shift();
            if (!card.isConnected) continue;
            const key = name + '\u0000' + previewText;
            running++;
            const w = Math.round(card.querySelector('.fg-img').clientWidth * (window.devicePixelRatio || 1));
            api.FontPreview(name, previewText, w || 560, Math.round((w || 560) * 0.32))
              .then(url => { cache.set(key, url); show(card, url); })
              .catch(() => { card.querySelector('.fg-img').textContent = 'Preview unavailable'; })
              .finally(() => { running--; pump(); });
          }
        };
        const show = (card, url) => {
          const box = card.querySelector('.fg-img');
          box.innerHTML = `<img src="${url}" alt="">`;
        };
        const io = new IntersectionObserver(entries => {
          for (const e of entries) {
            if (!e.isIntersecting) continue;
            io.unobserve(e.target);
            const name = e.target.dataset.name;
            const url = cache.get(name + '\u0000' + previewText);
            if (url) show(e.target, url);
            else { queue.push({ card: e.target, name }); pump(); }
          }
        }, { root: grid, rootMargin: '300px' });

        const render = () => {
          io.disconnect();
          queue.length = 0;
          const q = query.toLowerCase();
          const list = fonts
            .filter(f => !favOnly || favourites.has(f.name))
            .filter(f => kind === 'all' || (kind === 'windows' ? f.kind === 'windows' : f.kind !== 'windows'))
            .filter(f => !q || (f.name + ' ' + (f.description || '') + ' ' + (f.keywords || []).join(' ')).toLowerCase().includes(q))
            .sort((a, b) => (favourites.has(b.name) - favourites.has(a.name)) || a.name.localeCompare(b.name));
          back.querySelector('#fg-count').textContent = `${list.length} of ${fonts.length} fonts`;
          grid.innerHTML = list.map(f => {
            const size = f.native ? `${(f.native / 10).toFixed(0)} mm${f.minHeight ? ` (${(f.minHeight / 10).toFixed(0)}–${(f.maxHeight / 10).toFixed(0)})` : ''}` : (f.kind === 'windows' ? 'any size' : 'any size');
            const dots = (f.colors || []).map(c => `<i style="background:${esc(c)}"></i>`).join('');
            return `<div class="fg-card${f.name === current ? ' cur' : ''}" data-name="${esc(f.name)}" tabindex="0">
              <div class="fg-img"><span class="fg-loading"></span></div>
              <div class="fg-meta">
                <div><b>${esc(f.name)}</b>${dots ? `<span class="fg-dots" title="${f.colors.length} colours">${dots}</span>` : ''}
                  <div class="muted">${esc(size)}${f.kind === 'built-in' ? ' · built-in' : f.kind === 'windows' ? ' · Windows font, auto-fill' : ''}${f.license ? ' · ' + esc(f.license) : ''}</div></div>
                <button class="fg-star${favourites.has(f.name) ? ' on' : ''}" title="Favourite">${icon('star', 18)}</button>
              </div>
            </div>`;
          }).join('') || '<p class="hint">No fonts match.</p>';
          grid.querySelectorAll('.fg-card').forEach(card => {
            io.observe(card);
            const name = card.dataset.name;
            const star = card.querySelector('.fg-star');
            star.onclick = async e => {
              e.stopPropagation();
              const p = await api.ToggleFavoriteFont(name);
              favourites = new Set(p.favoriteFonts || []);
              star.classList.toggle('on', favourites.has(name));
              if (favOnly) render();
            };
            const choose = () => { picked = fonts.find(f => f.name === name); back.querySelector('[data-x]').click(); };
            card.onclick = choose;
            card.onkeydown = e => { if (e.key === 'Enter') choose(); };
          });
        };

        let t;
        back.querySelector('#fg-q').oninput = e => { query = e.target.value; render(); };
        back.querySelector('#fg-t').oninput = e => {
          clearTimeout(t);
          t = setTimeout(() => { previewText = e.target.value.trim() || 'Andrew 23'; render(); }, 350);
        };
        back.querySelectorAll('.fg-kind button').forEach(b => b.onclick = () => {
          kind = b.dataset.k;
          back.querySelectorAll('.fg-kind button').forEach(x => x.classList.toggle('on', x === b));
          render();
        });
        const favBtn = back.querySelector('#fg-fav');
        favBtn.onclick = () => { favOnly = !favOnly; favBtn.classList.toggle('on', favOnly); render(); };
        render();
        setTimeout(() => back.querySelector('#fg-q').focus(), 0);
      },
    }).then(() => resolve(picked));
  });
}
