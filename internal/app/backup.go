package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/applog"
)

// Auto-backup: every few seconds each tab with unsaved changes is written to
// the backups folder. Saving, closing a tab or quitting normally removes its
// backup; after a crash or power cut the next launch offers to restore them.

var backupInterval = 20 * time.Second

type backupMeta struct {
	Title    string    `json:"title"`
	Original string    `json:"original"` // project or DST the work came from
	Saved    time.Time `json:"saved"`
}

func (a *App) backupDir() string {
	if a.cfgDir == "" {
		return ""
	}
	return filepath.Join(a.cfgDir, "backups")
}

// backupPath is this session's backup file for tab id.
func (a *App) backupPath(id string) string {
	return filepath.Join(a.backupDir(), a.session+"-"+id+".tep")
}

// backupOnce writes or removes backups so they match the open tabs.
func (a *App) backupOnce() {
	dir := a.backupDir()
	if dir == "" {
		return
	}
	a.tabsMu.Lock()
	tabs := append([]*tab(nil), a.tabs...)
	a.tabsMu.Unlock()
	for _, t := range tabs {
		if !t.ed.Dirty() {
			a.forgetBackup(t.id)
			continue
		}
		v := t.ed.Version()
		a.backupMu.Lock()
		same := a.backedUp[t.id] == v
		a.backupMu.Unlock()
		if same {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			applog.Errorf("backup: %v", err)
			return
		}
		p := a.backupPath(t.id)
		if err := t.ed.WriteBackup(p); err != nil {
			applog.Errorf("backup %s: %v", t.id, err)
			continue
		}
		orig := t.ed.Path()
		if orig == "" {
			orig = t.ed.Source()
		}
		meta, _ := json.Marshal(backupMeta{Title: t.ed.Title(), Original: orig, Saved: time.Now()})
		_ = os.WriteFile(strings.TrimSuffix(p, ".tep")+".json", meta, 0o644)
		a.backupMu.Lock()
		a.backedUp[t.id] = v
		a.backupMu.Unlock()
	}
}

// forgetBackup deletes tab id's backup (saved, closed or discarded).
func (a *App) forgetBackup(id string) {
	if a.backupDir() == "" {
		return
	}
	a.backupMu.Lock()
	delete(a.backedUp, id)
	a.backupMu.Unlock()
	p := a.backupPath(id)
	_ = os.Remove(p)
	_ = os.Remove(strings.TrimSuffix(p, ".tep") + ".json")
}

// clearBackups removes this session's backups (normal quit).
func (a *App) clearBackups() {
	dir := a.backupDir()
	if dir == "" {
		return
	}
	files, _ := filepath.Glob(filepath.Join(dir, a.session+"-*"))
	for _, f := range files {
		_ = os.Remove(f)
	}
}

func (a *App) startBackups() {
	go func() {
		t := time.NewTicker(backupInterval)
		defer t.Stop()
		for range t.C {
			a.backupOnce()
		}
	}()
}

type leftover struct {
	path string
	meta backupMeta
}

// leftoverBackups finds backups from earlier sessions that did not close
// cleanly, newest first.
func (a *App) leftoverBackups() []leftover {
	dir := a.backupDir()
	if dir == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.tep"))
	var out []leftover
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), a.session+"-") {
			continue
		}
		var m backupMeta
		if b, err := os.ReadFile(strings.TrimSuffix(f, ".tep") + ".json"); err == nil {
			_ = json.Unmarshal(b, &m)
		}
		if m.Saved.IsZero() {
			if fi, err := os.Stat(f); err == nil {
				m.Saved = fi.ModTime()
			}
		}
		out = append(out, leftover{f, m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].meta.Saved.After(out[j].meta.Saved) })
	return out
}

func removeLeftover(l leftover) {
	_ = os.Remove(l.path)
	_ = os.Remove(strings.TrimSuffix(l.path, ".tep") + ".json")
}

// offerRestore asks whether to bring back unsaved work from a session that
// ended unexpectedly, and opens each design in its own tab.
func (a *App) offerRestore() {
	left := a.leftoverBackups()
	if len(left) == 0 {
		return
	}
	var names []string
	for i, l := range left {
		if i == 5 {
			names = append(names, fmt.Sprintf("…and %d more", len(left)-5))
			break
		}
		n := l.meta.Title
		if n == "" {
			n = "Untitled"
		}
		names = append(names, "• "+n+" ("+l.meta.Saved.Format("Jan 2 15:04")+")")
	}
	msg := fmt.Sprintf("TSG Embroidery Pro didn't close properly last time. Restore %d unsaved design(s)?\n\n%s", len(left), strings.Join(names, "\n"))
	if !a.ui.Confirm("Restore unsaved work", msg) {
		for _, l := range left {
			removeLeftover(l)
		}
		return
	}
	blankID := ""
	if a.cur().Blank() {
		blankID = a.activeID()
	}
	restored := 0
	for i := len(left) - 1; i >= 0; i-- { // oldest first so the newest ends up active
		l := left[i]
		ed := a.newEditor()
		if err := ed.RestoreBackup(l.path, l.meta.Original); err != nil {
			applog.Errorf("restore %s: %v", l.path, err)
			removeLeftover(l) // unreadable: don't ask about it again
			continue
		}
		a.addTab(ed)
		restored++
		removeLeftover(l)
	}
	if restored > 0 && blankID != "" {
		a.removeTab(blankID)
	}
	applog.Infof("restored %d design(s) from backup", restored)
	a.ui.Emit("state:changed", nil)
}
