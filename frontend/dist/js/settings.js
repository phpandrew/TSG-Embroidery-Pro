// Settings window: startup, auto-open of new downloads, updates, help.
import { api } from './api.js';
import { esc, modal, toast } from './ui.js';
import { S, actions, renderQuickSends } from './main.js';

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
            <h4>Quick send</h4>
            <p class="hint">One-click buttons that export the current design as a DST straight to a machine's USB stick or folder. Each machine you add gets its own button on the toolbar.</p>
            <div id="qs-list"></div>
            <div class="set-row"><input class="inp qs-new" id="qs-name" placeholder="Machine name, e.g. Tajima 1" maxlength="40" style="height:34px;border:1px solid #d5d9df;border-radius:6px;padding:0 8px;flex:1;min-width:0"><button class="btn btn-ghost btn-sm" id="qs-add">Add machine…</button></div>
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
        drawQS();
      };

      // quick-send machines
      const drawQS = list => {
        const box = root.querySelector('#qs-list');
        if (!box) return;
        if (!list) { api.QuickSends().then(drawQS).catch(fail); return; }
        renderQuickSends();
        box.innerHTML = list.length ? list.map(m => `<div class="qs-row" data-id="${esc(m.id)}">
            <input class="inp qs-nm" value="${esc(m.name)}" maxlength="40" title="Button name">
            <code title="${esc(m.folder)}"><span class="qs-dot${m.connected ? ' on' : ''}" title="${m.connected ? 'Connected' : 'Not found'}"></span>${esc(m.folder)}</code>
            <button class="btn btn-ghost btn-sm qs-folder">Folder…</button>
            <button class="btn btn-ghost btn-sm qs-del">Remove</button>
          </div>`).join('') : '<p class="hint">No machines yet.</p>';
        box.querySelectorAll('.qs-row').forEach(row => {
          const id = row.dataset.id;
          row.querySelector('.qs-nm').onchange = e => api.RenameQuickSend(id, e.target.value).then(drawQS).catch(fail);
          row.querySelector('.qs-folder').onclick = () => api.ChangeQuickSendFolder(id).then(drawQS).catch(fail);
          row.querySelector('.qs-del').onclick = () => api.RemoveQuickSend(id).then(drawQS).catch(fail);
          row.querySelector('.qs-nm').onkeydown = e => { if (e.key === 'Enter') { e.stopPropagation(); e.target.blur(); } };
        });
        const add = root.querySelector('#qs-add'), nm = root.querySelector('#qs-name');
        add.onclick = () => {
          if (!nm.value.trim()) { nm.focus(); toast('Type a name for the machine first'); return; }
          api.AddQuickSend(nm.value).then(l => { nm.value = ''; drawQS(l); }).catch(fail);
        };
        nm.onkeydown = e => { if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); add.click(); } };
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
