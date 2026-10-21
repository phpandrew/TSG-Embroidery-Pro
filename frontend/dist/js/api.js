// Backend bridge: Wails bindings in the desktop app, JSON over HTTP in the
// browser dev server (cmd/devserver).
export const isDesktop = !!(window.go && window.go.app && window.go.app.App);

async function httpCall(name, args) {
  const r = await fetch('/api/' + name, { method: 'POST', body: JSON.stringify(args) });
  let j = {};
  try { j = await r.json(); } catch { /* not JSON */ }
  if (!r.ok) throw new Error(j.error || r.statusText);
  return j.result;
}

export const api = new Proxy({}, {
  get: (_, name) => async (...args) => {
    if (isDesktop) {
      try { return await window.go.app.App[name](...args); }
      catch (e) { throw new Error(typeof e === 'string' ? e : (e && e.message) || String(e)); }
    }
    return httpCall(name, args);
  },
});

export function on(event, fn) {
  if (window.runtime && window.runtime.EventsOn) window.runtime.EventsOn(event, fn);
}
