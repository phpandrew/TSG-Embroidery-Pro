package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/document"
)

// Each open design or project lives in its own tab with its own editor,
// undo history and save path. Every editing call works on the active tab.

type tab struct {
	id string
	ed *document.Editor
}

// cur returns the active tab's editor.
func (a *App) cur() *document.Editor {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()
	return a.tabs[a.active].ed
}

// newEditor makes an empty editor with the user's default hoop/machine settings.
func (a *App) newEditor() *document.Editor {
	ed := document.NewEditor(a.fonts)
	a.prefsMu.Lock()
	m := a.prefs.Machine
	a.prefsMu.Unlock()
	if m != nil {
		ed.SetDefaults(*m)
	}
	return ed
}

// addTab appends a tab and makes it active. Caller must not hold tabsMu.
func (a *App) addTab(ed *document.Editor) string {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()
	a.nextTab++
	id := "t" + strconv.Itoa(a.nextTab)
	a.tabs = append(a.tabs, &tab{id: id, ed: ed})
	a.active = len(a.tabs) - 1
	return id
}

func (a *App) tabIndex(id string) int {
	for i, t := range a.tabs {
		if t.id == id {
			return i
		}
	}
	return -1
}

func (a *App) removeTab(id string) {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()
	i := a.tabIndex(id)
	if i < 0 {
		return
	}
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if len(a.tabs) == 0 {
		a.nextTab++
		a.tabs = []*tab{{id: "t" + strconv.Itoa(a.nextTab), ed: a.newEditor()}}
	}
	switch {
	case a.active > i:
		a.active--
	case a.active >= len(a.tabs):
		a.active = len(a.tabs) - 1
	}
}

// tabInfos lists the open tabs for the View.
func (a *App) tabInfos() ([]document.TabInfo, string) {
	a.tabsMu.Lock()
	tabs := append([]*tab(nil), a.tabs...)
	activeID := a.tabs[a.active].id
	a.tabsMu.Unlock()
	out := make([]document.TabInfo, len(tabs))
	for i, t := range tabs {
		title := t.ed.Title()
		if title == "" {
			title = "Untitled"
		}
		out[i] = document.TabInfo{ID: t.id, Title: title, Dirty: t.ed.Dirty()}
	}
	return out, activeID
}

// openInTab opens a design or project. The active tab is reused when it is
// blank; a file that is already open just switches to its tab.
func (a *App) openInTab(path string) error {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	a.tabsMu.Lock()
	for i, t := range a.tabs {
		if p := t.ed.Path(); p != "" && strings.EqualFold(p, path) {
			a.active = i
			a.tabsMu.Unlock()
			return nil
		}
		if src := t.ed.Source(); src != "" && strings.EqualFold(src, path) && !t.ed.Dirty() {
			a.active = i
			a.tabsMu.Unlock()
			return nil
		}
	}
	reuse := a.tabs[a.active].ed.Blank()
	a.tabsMu.Unlock()

	if reuse {
		return a.cur().OpenFile(path)
	}
	ed := a.newEditor()
	if err := ed.OpenFile(path); err != nil {
		return err
	}
	a.addTab(ed)
	return nil
}

// ---- tab API -------------------------------------------------------------

// NewTab opens an empty design in a new tab (or reuses a blank active tab).
func (a *App) NewTab() (document.View, error) {
	if !a.cur().Blank() {
		a.addTab(a.newEditor())
	}
	return a.view(nil)
}

// SwitchTab makes tab id active.
func (a *App) SwitchTab(id string) (document.View, error) {
	a.tabsMu.Lock()
	i := a.tabIndex(id)
	if i >= 0 {
		a.active = i
	}
	a.tabsMu.Unlock()
	if i < 0 {
		return a.view(errors.New("that tab is no longer open"))
	}
	return a.view(nil)
}

// CloseTab closes tab id, asking first if it has unsaved changes.
func (a *App) CloseTab(id string) (document.View, error) {
	a.tabsMu.Lock()
	i := a.tabIndex(id)
	var ed *document.Editor
	if i >= 0 {
		ed = a.tabs[i].ed
	}
	a.tabsMu.Unlock()
	if ed == nil {
		return a.view(nil)
	}
	if ed.Dirty() {
		name := ed.Title()
		if name == "" {
			name = "Untitled"
		}
		if !a.ui.Confirm("Unsaved changes", fmt.Sprintf("Close %q and discard its unsaved changes?", name)) {
			return a.view(nil)
		}
	}
	a.removeTab(id)
	return a.view(nil)
}

// dirtyTabs counts tabs with unsaved changes.
func (a *App) dirtyTabs() int {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()
	n := 0
	for _, t := range a.tabs {
		if t.ed.Dirty() {
			n++
		}
	}
	return n
}

// confirmQuit asks before throwing away unsaved work in any tab.
func (a *App) confirmQuit(action string) bool {
	switch n := a.dirtyTabs(); n {
	case 0:
		return true
	case 1:
		return a.ui.Confirm("Unsaved changes", "A design has unsaved changes. Discard them and "+action+"?")
	default:
		return a.ui.Confirm("Unsaved changes", fmt.Sprintf("%d designs have unsaved changes. Discard them and %s?", n, action))
	}
}
