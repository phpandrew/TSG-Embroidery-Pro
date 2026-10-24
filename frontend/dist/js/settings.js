// Settings window: startup, auto-open of new downloads, updates, help.
import { api } from './api.js';
import { esc, modal, toast } from './ui.js';
import { S, actions } from './main.js';

export async function openSettings() {
  let timer = null;
  await modal({
    title: 'Settings',
    body: '<div class="settings" id="set-body"><p class="hint">Loading…</p></div>',
    buttons: [{ label: 'Close', kind: 'primary', value: true }],
    onOpen: back => {
      const root = back.querySelector('#set-body');
      back.querySelector('.modal').classList.add('settings-modal');
      const fail = e => toast(e.message || String(e), 'err');

      const draw = (auto, prefs) => {
        root.innerHTML = `
          <section class="set-sec">
            <h4>Startup</h4>
            ${auto.startupSupported
              ? `<label class="check"><input type="checkbox" id="st-start" ${auto.startWithWindows ? 'checked' : ''}> Launch TSG Embroidery Pro when Windows starts <span class="muted">(opens minimised in the taskbar)</span></label>`
              : '<p class="hint">Launch at startup is only available on Windows.</p>'}
          </section>
          <section class="set-sec">
            <h4>Auto-open new designs</h4>
            <label class="check"><input type="checkbox" id="st-watch" ${auto.watchEnabled ? 'checked' : ''}> Open new DST files automatically when they land in this folder</label>
            <div class="set-row">
              <code class="set-path" title="${esc(auto.watchDir)}">${esc(auto.watchDir || '—')}</code>
              <button class="btn btn-ghost btn-sm" id="st-pick">Change folder…</button>
              ${auto.watchIsDefault ? '' : '<button class="btn btn-ghost btn-sm" id="st-reset">Use Downloads</button>'}
            </div>
            <p class="set-status" id="st-status">${esc(auto.watchStatus)}</p>
            <p class="hint">Only a single new file is opened, in a new tab, and the window comes to the front. If several arrive at once (unzipping a batch) none are opened. Files already in the folder when the app starts are left alone.</p>
          </section>
          <section class="set-sec">
            <h4>Updates</h4>
            <p class="hint" style="margin:0 0 6px">You have version <b>${esc(S.info ? S.info.version : '')}</b></p>
            <label class="check"><input type="checkbox" id="st-auto" ${prefs.autoCheckUpdates ? 'checked' : ''}> Check for updates at startup</label>
            <label class="check"><input type="checkbox" id="st-pre" ${prefs.prerelease ? 'checked' : ''}> Include pre-releases (beta)</label>
            <div class="set-row"><button class="btn btn-ghost btn-sm" id="st-check">Check now</button></div>
            <p class="hint">Updates come from GitHub Releases and are installed only if their Ed25519 signature and SHA-256 checksum verify.</p>
          </section>
          <section class="set-sec">
            <h4>Help</h4>
            <div class="set-row"><button class="btn btn-ghost btn-sm" id="st-logs">Open debug log folder</button></div>
          </section>`;
        const q = s => root.querySelector(s);
        const redraw = p => draw(p, prefs);
        const st = q('#st-start');
        if (st) st.onchange = () => api.SetStartWithWindows(st.checked).then(redraw).catch(fail);
        q('#st-watch').onchange = e => api.SetWatch(e.target.checked).then(redraw).catch(fail);
        q('#st-pick').onclick = () => api.ChooseWatchFolder().then(redraw).catch(fail);
        const rs = q('#st-reset');
        if (rs) rs.onclick = () => api.ResetWatchFolder().then(redraw).catch(fail);
        const savePrefs = () => api.SetPrefs({ autoCheckUpdates: q('#st-auto').checked, prerelease: q('#st-pre').checked })
          .then(p => { prefs = p; }).catch(fail);
        q('#st-auto').onchange = savePrefs;
        q('#st-pre').onchange = savePrefs;
        q('#st-check').onclick = () => actions.checkUpdates();
        q('#st-logs').onclick = () => actions.logs();
      };

      let prefs = {};
      Promise.all([api.GetAutoPrefs(), api.GetPrefs()])
        .then(([auto, p]) => { prefs = p; draw(auto, p); })
        .catch(fail);
      // keep the watcher status line live while the window is open
      timer = setInterval(() => {
        if (!back.isConnected) { clearInterval(timer); return; }
        api.GetAutoPrefs().then(a => { const el = root.querySelector('#st-status'); if (el) el.textContent = a.watchStatus; }).catch(() => {});
      }, 2000);
    },
  });
  clearInterval(timer);
}
