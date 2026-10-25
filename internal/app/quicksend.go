package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/applog"
)

// Quick send: named one-click export targets, one per machine (usually the
// USB stick or network folder each machine reads from). Set up in Settings;
// each target then gets its own toolbar button.

// QuickSend is a saved export target.
type QuickSend struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Folder string `json:"folder"`
}

// QuickSendInfo is a target plus whether its folder is reachable right now.
type QuickSendInfo struct {
	QuickSend
	Connected bool `json:"connected"`
}

const maxQuickSends = 6

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// QuickSends lists the configured targets.
func (a *App) QuickSends() []QuickSendInfo {
	a.prefsMu.Lock()
	list := append([]QuickSend(nil), a.prefs.QuickSends...)
	a.prefsMu.Unlock()
	out := make([]QuickSendInfo, 0, len(list))
	for _, q := range list {
		out = append(out, QuickSendInfo{q, isDir(q.Folder)})
	}
	return out
}

func cleanName(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	return s
}

// AddQuickSend asks for the machine's folder and saves a new target.
func (a *App) AddQuickSend(name string) ([]QuickSendInfo, error) {
	name = cleanName(name)
	if name == "" {
		return a.QuickSends(), errors.New("give the machine a name")
	}
	a.prefsMu.Lock()
	n := len(a.prefs.QuickSends)
	a.prefsMu.Unlock()
	if n >= maxQuickSends {
		return a.QuickSends(), fmt.Errorf("up to %d quick-send machines", maxQuickSends)
	}
	dir, err := a.ui.PickFolder("Where does "+name+" read designs from? (its USB stick or folder)", "")
	if err != nil || dir == "" {
		return a.QuickSends(), err
	}
	a.prefsMu.Lock()
	a.prefs.QuickSends = append(a.prefs.QuickSends, QuickSend{
		ID:     "q" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Name:   name,
		Folder: dir,
	})
	a.prefsMu.Unlock()
	a.savePrefs()
	return a.QuickSends(), nil
}

func (a *App) editQuickSend(id string, fn func(q *QuickSend)) ([]QuickSendInfo, error) {
	a.prefsMu.Lock()
	found := false
	for i := range a.prefs.QuickSends {
		if a.prefs.QuickSends[i].ID == id {
			fn(&a.prefs.QuickSends[i])
			found = true
		}
	}
	a.prefsMu.Unlock()
	if !found {
		return a.QuickSends(), errors.New("that machine is no longer set up")
	}
	a.savePrefs()
	return a.QuickSends(), nil
}

// RenameQuickSend changes a target's name.
func (a *App) RenameQuickSend(id, name string) ([]QuickSendInfo, error) {
	name = cleanName(name)
	if name == "" {
		return a.QuickSends(), errors.New("give the machine a name")
	}
	return a.editQuickSend(id, func(q *QuickSend) { q.Name = name })
}

// ChangeQuickSendFolder picks a new folder for a target.
func (a *App) ChangeQuickSendFolder(id string) ([]QuickSendInfo, error) {
	cur := ""
	for _, q := range a.QuickSends() {
		if q.ID == id {
			cur = q.Folder
		}
	}
	dir, err := a.ui.PickFolder("Folder this machine reads designs from", cur)
	if err != nil || dir == "" {
		return a.QuickSends(), err
	}
	return a.editQuickSend(id, func(q *QuickSend) { q.Folder = dir })
}

// RemoveQuickSend deletes a target.
func (a *App) RemoveQuickSend(id string) []QuickSendInfo {
	a.prefsMu.Lock()
	var list []QuickSend
	for _, q := range a.prefs.QuickSends {
		if q.ID != id {
			list = append(list, q)
		}
	}
	a.prefs.QuickSends = list
	a.prefsMu.Unlock()
	a.savePrefs()
	return a.QuickSends()
}

// fileSafe makes a design name usable as a file name on Windows.
func fileSafe(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	name = strings.Trim(name, ". ")
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	if name == "" {
		name = "design"
	}
	return name
}

// SendTo exports the current design as a DST straight into a target's folder.
func (a *App) SendTo(id string) (Result, error) {
	var target *QuickSend
	for _, q := range a.QuickSends() {
		if q.ID == id {
			t := q.QuickSend
			target = &t
		}
	}
	if target == nil {
		v, err := a.view(errors.New("that machine is no longer set up"))
		return Result{View: v}, err
	}
	if !isDir(target.Folder) {
		v, err := a.view(fmt.Errorf("%s isn't connected (%s not found). Plug in its stick or check the folder in Settings", target.Name, target.Folder))
		return Result{View: v}, err
	}
	ed := a.cur()
	if len(ed.State().Designs) == 0 {
		v, err := a.view(errors.New("nothing to send: open or add a design first"))
		return Result{View: v}, err
	}
	name := strings.TrimSuffix(ed.Title(), filepath.Ext(ed.Title()))
	p := filepath.Join(target.Folder, fileSafe(name)+".dst")
	applog.Infof("quick send to %s: %q", target.Name, p)
	if err := ed.ExportDST(p); err != nil {
		v, err := a.view(fmt.Errorf("send to %s: %w", target.Name, err))
		return Result{View: v}, err
	}
	v, _ := a.view(nil)
	return Result{View: v, Message: fmt.Sprintf("Sent %s to %s", filepath.Base(p), target.Name)}, nil
}
