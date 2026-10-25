package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/document"
)

const maxRecent = 12

// RecentFile is one entry in the recently opened list.
type RecentFile struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Folder string `json:"folder"`
	Exists bool   `json:"exists"`
}

// addRecent moves path to the top of the recent list.
func (a *App) addRecent(path string) {
	if path == "" {
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	a.prefsMu.Lock()
	list := []string{path}
	for _, p := range a.prefs.Recent {
		if !strings.EqualFold(p, path) && len(list) < maxRecent {
			list = append(list, p)
		}
	}
	a.prefs.Recent = list
	a.prefsMu.Unlock()
	a.savePrefs()
}

// RecentFiles lists recently opened designs and projects, newest first.
func (a *App) RecentFiles() []RecentFile {
	a.prefsMu.Lock()
	paths := append([]string(nil), a.prefs.Recent...)
	a.prefsMu.Unlock()
	out := make([]RecentFile, 0, len(paths))
	for _, p := range paths {
		_, err := os.Stat(p)
		out = append(out, RecentFile{
			Path:   p,
			Name:   filepath.Base(p),
			Folder: filepath.Dir(p),
			Exists: err == nil,
		})
	}
	return out
}

// OpenRecent opens a file from the recent list in a tab.
func (a *App) OpenRecent(path string) (document.View, error) {
	if _, err := os.Stat(path); err != nil {
		a.removeRecent(path)
		return a.view(err)
	}
	a.rememberDir(false, path)
	return a.view(a.openInTab(path))
}

// ClearRecent empties the recent list.
func (a *App) ClearRecent() []RecentFile {
	a.prefsMu.Lock()
	a.prefs.Recent = nil
	a.prefsMu.Unlock()
	a.savePrefs()
	return []RecentFile{}
}

func (a *App) removeRecent(path string) {
	a.prefsMu.Lock()
	var list []string
	for _, p := range a.prefs.Recent {
		if !strings.EqualFold(p, path) {
			list = append(list, p)
		}
	}
	a.prefs.Recent = list
	a.prefsMu.Unlock()
	a.savePrefs()
}
