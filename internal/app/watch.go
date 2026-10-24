package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/applog"
)

// Watch folder: when a single new DST file lands in the watched folder
// (Downloads by default) it opens in a new tab and the window comes to the
// front. If several arrive together - unzipping a batch, say - none are
// opened; the user just gets a notice.

var (
	watchInterval = 1500 * time.Millisecond
	watchSettle   = 3 * time.Second // quiet time before deciding "one or many"
)

const watchMaxBytes = 64 << 20

type fileSig struct {
	size int64
	mod  time.Time
}

type pendingFile struct {
	sig    fileSig
	stable int // polls in a row with the same size/time
}

type watcher struct {
	dir    string
	stop   chan struct{}
	onOne  func(path string)
	onMany func(paths []string)
}

func isDST(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".dst") && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "~")
}

// scan lists DST files in dir and in subfolders changed in the last few
// minutes (a freshly unzipped folder), one level deep.
func (w *watcher) scan() map[string]fileSig {
	out := map[string]fileSig{}
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return out
	}
	recent := time.Now().Add(-5 * time.Minute)
	add := func(dir string, e os.DirEntry) {
		if !isDST(e.Name()) {
			return
		}
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && fi.Size() <= watchMaxBytes {
			out[filepath.Join(dir, e.Name())] = fileSig{fi.Size(), fi.ModTime()}
		}
	}
	for _, e := range entries {
		if e.IsDir() {
			fi, err := e.Info()
			if err != nil || fi.ModTime().Before(recent) {
				continue
			}
			sub := filepath.Join(w.dir, e.Name())
			if subs, err := os.ReadDir(sub); err == nil {
				for _, se := range subs {
					if !se.IsDir() {
						add(sub, se)
					}
				}
			}
			continue
		}
		add(w.dir, e)
	}
	return out
}

func (w *watcher) run() {
	seen := w.scan() // files already there are never opened
	pending := map[string]*pendingFile{}
	var lastNew time.Time
	t := time.NewTicker(watchInterval)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
		}
		cur := w.scan()
		for p, sig := range cur {
			if _, ok := seen[p]; ok {
				continue
			}
			if pf, ok := pending[p]; ok {
				if pf.sig == sig {
					pf.stable++
				} else {
					pf.sig, pf.stable = sig, 0
				}
				continue
			}
			pending[p] = &pendingFile{sig: sig}
			lastNew = time.Now()
		}
		for p := range pending {
			if _, ok := cur[p]; !ok {
				delete(pending, p) // renamed or deleted before it settled
			}
		}
		for p := range seen {
			if _, ok := cur[p]; !ok {
				delete(seen, p)
			}
		}
		if len(pending) == 0 || time.Since(lastNew) < watchSettle {
			continue
		}
		ready := true
		for _, pf := range pending {
			if pf.stable < 1 || pf.sig.size == 0 {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		var paths []string
		for p, pf := range pending {
			paths = append(paths, p)
			seen[p] = pf.sig
		}
		pending = map[string]*pendingFile{}
		if len(paths) == 1 {
			w.onOne(paths[0])
		} else {
			w.onMany(paths)
		}
	}
}

// ---- app wiring ----------------------------------------------------------

func defaultDownloads() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, "Downloads")
	}
	return ""
}

// watchDir is the folder being watched (the user's choice or Downloads).
func (a *App) watchDir() string {
	a.prefsMu.Lock()
	d := a.prefs.WatchDir
	a.prefsMu.Unlock()
	if d == "" {
		d = downloadsDir()
	}
	return d
}

// restartWatch (re)starts or stops the folder watcher to match prefs.
func (a *App) restartWatch() {
	a.watchMu.Lock()
	defer a.watchMu.Unlock()
	if a.watch != nil {
		close(a.watch.stop)
		a.watch = nil
	}
	a.prefsMu.Lock()
	on := a.prefs.WatchEnabled
	a.prefsMu.Unlock()
	dir := a.watchDir()
	if !on || dir == "" {
		return
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		applog.Errorf("watch folder %q not available", dir)
		return
	}
	w := &watcher{dir: dir, stop: make(chan struct{})}
	w.onOne = func(p string) {
		applog.Infof("watch: opening new file %q", p)
		if err := a.openInTab(p); err != nil {
			applog.Errorf("watch: %v", err)
			a.ui.Emit("watch:error", err.Error())
			return
		}
		a.ui.Emit("state:changed", nil)
		a.ui.Emit("watch:opened", filepath.Base(p))
		a.ui.Focus()
	}
	w.onMany = func(paths []string) {
		applog.Infof("watch: %d new DST files arrived together; not opening them", len(paths))
		a.ui.Emit("watch:many", map[string]any{"count": len(paths), "dir": filepath.Dir(paths[0])})
	}
	a.watch = w
	applog.Infof("watching %q for new DST files", dir)
	go w.run()
}

// syncStartup keeps the Windows startup entry pointing at this exe.
func (a *App) syncStartup() {
	if !startupSupported() {
		return
	}
	a.prefsMu.Lock()
	on := a.prefs.StartWithWindows
	a.prefsMu.Unlock()
	if on || runAtStartup() {
		if err := setRunAtStartup(on); err != nil {
			applog.Errorf("startup entry: %v", err)
		}
	}
}

// ---- settings API --------------------------------------------------------

// AutoPrefs is the state shown in the "Startup & auto-open" settings.
type AutoPrefs struct {
	StartWithWindows bool   `json:"startWithWindows"`
	StartupSupported bool   `json:"startupSupported"`
	WatchEnabled     bool   `json:"watchEnabled"`
	WatchDir         string `json:"watchDir"`
	WatchIsDefault   bool   `json:"watchIsDefault"`
}

func (a *App) GetAutoPrefs() AutoPrefs {
	a.prefsMu.Lock()
	p := AutoPrefs{
		StartWithWindows: a.prefs.StartWithWindows,
		StartupSupported: startupSupported(),
		WatchEnabled:     a.prefs.WatchEnabled,
		WatchIsDefault:   a.prefs.WatchDir == "",
	}
	a.prefsMu.Unlock()
	if p.StartupSupported {
		p.StartWithWindows = runAtStartup()
	}
	p.WatchDir = a.watchDir()
	return p
}

// SetStartWithWindows turns launch-at-sign-in on or off.
func (a *App) SetStartWithWindows(on bool) (AutoPrefs, error) {
	if err := setRunAtStartup(on); err != nil {
		return a.GetAutoPrefs(), err
	}
	a.prefsMu.Lock()
	a.prefs.StartWithWindows = on
	a.prefsMu.Unlock()
	a.savePrefs()
	return a.GetAutoPrefs(), nil
}

// SetWatch turns auto-open of new DST files on or off.
func (a *App) SetWatch(on bool) (AutoPrefs, error) {
	a.prefsMu.Lock()
	a.prefs.WatchEnabled = on
	a.prefsMu.Unlock()
	a.savePrefs()
	a.restartWatch()
	return a.GetAutoPrefs(), nil
}

// ChooseWatchFolder lets the user pick the folder to watch.
func (a *App) ChooseWatchFolder() (AutoPrefs, error) {
	d, err := a.ui.PickFolder("Folder to watch for new DST files", a.watchDir())
	if err != nil || d == "" {
		return a.GetAutoPrefs(), err
	}
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		return a.GetAutoPrefs(), errors.New("that folder isn't available")
	}
	a.prefsMu.Lock()
	a.prefs.WatchDir = d
	a.prefsMu.Unlock()
	a.savePrefs()
	a.restartWatch()
	return a.GetAutoPrefs(), nil
}

// ResetWatchFolder goes back to watching Downloads.
func (a *App) ResetWatchFolder() (AutoPrefs, error) {
	a.prefsMu.Lock()
	a.prefs.WatchDir = ""
	a.prefsMu.Unlock()
	a.savePrefs()
	a.restartWatch()
	return a.GetAutoPrefs(), nil
}
