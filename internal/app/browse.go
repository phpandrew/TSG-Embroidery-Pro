package app

import (
	"encoding/base64"
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/document"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/preview"
)

// Design browser: a searchable grid of stitch previews for the embroidery
// files in a folder (Downloads by default), like the font gallery.

var designExts = map[string]bool{".dst": true, ".pes": true, ".pec": true, ".jef": true, ".exp": true}

const (
	browseMaxFiles = 3000 // stop scanning after this many designs
	browseMaxDepth = 3    // folders inside the chosen folder
	thumbCacheMax  = 400
)

// BrowseFile is one design in the browser.
type BrowseFile struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Folder   string `json:"folder"` // relative to the browsed folder ("" = top level)
	Ext      string `json:"ext"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // unix seconds
}

// BrowseResult is a folder listing.
type BrowseResult struct {
	Dir       string       `json:"dir"`
	IsDefault bool         `json:"isDefault"` // Downloads
	Files     []BrowseFile `json:"files"`
	Truncated bool         `json:"truncated"`
}

// DesignThumb is a preview plus the numbers shown on a card.
type DesignThumb struct {
	Image    string   `json:"image"`
	Stitches int      `json:"stitches"`
	Colors   int      `json:"colors"`
	WidthMM  float64  `json:"widthMM"`
	HeightMM float64  `json:"heightMM"`
	Swatches []string `json:"swatches"`
}

type thumbCache struct {
	mu    sync.Mutex
	items map[string]DesignThumb
	order []string // oldest first
}

func (c *thumbCache) get(k string) (DesignThumb, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.items[k]
	return t, ok
}

func (c *thumbCache) put(k string, t DesignThumb) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]DesignThumb{}
	}
	if _, ok := c.items[k]; !ok {
		c.order = append(c.order, k)
	}
	c.items[k] = t
	for len(c.order) > thumbCacheMax {
		delete(c.items, c.order[0])
		c.order = c.order[1:]
	}
}

// browseDir is the folder the browser opens on: the last one used, else
// the auto-open folder, else Downloads.
func (a *App) browseDir() (string, bool) {
	a.prefsMu.Lock()
	d := a.prefs.BrowseDir
	a.prefsMu.Unlock()
	if d != "" && isDir(d) {
		return d, false
	}
	dl := downloadsDir()
	return dl, true
}

// BrowseDesigns lists the designs in dir ("" = the remembered folder),
// newest first.
func (a *App) BrowseDesigns(dir string) (BrowseResult, error) {
	isDefault := false
	if dir == "" {
		dir, isDefault = a.browseDir()
	} else {
		isDefault = strings.EqualFold(filepath.Clean(dir), filepath.Clean(downloadsDir()))
	}
	if !isDir(dir) {
		return BrowseResult{Dir: dir, IsDefault: isDefault, Files: []BrowseFile{}}, fmt.Errorf("folder not found: %s", dir)
	}
	res := BrowseResult{Dir: dir, IsDefault: isDefault, Files: []BrowseFile{}}
	root := filepath.Clean(dir)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			name := d.Name()
			rel, _ := filepath.Rel(root, p)
			if strings.HasPrefix(name, ".") || strings.Count(rel, string(filepath.Separator)) >= browseMaxDepth {
				return fs.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !designExts[ext] {
			return nil
		}
		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		if len(res.Files) >= browseMaxFiles {
			res.Truncated = true
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		if rel == "." {
			rel = ""
		}
		res.Files = append(res.Files, BrowseFile{
			Path: p, Name: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())), Folder: rel,
			Ext: strings.TrimPrefix(ext, "."), Size: fi.Size(), Modified: fi.ModTime().Unix(),
		})
		return nil
	})
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Modified > res.Files[j].Modified })
	return res, nil
}

// DesignPreview renders a design file to a thumbnail with its stats.
func (a *App) DesignPreview(path string, w, h int) (DesignThumb, error) {
	if !designExts[strings.ToLower(filepath.Ext(path))] {
		return DesignThumb{}, errors.New("not a design file")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return DesignThumb{}, err
	}
	w, h = min(max(w, 80), 800), min(max(h, 60), 600)
	key := fmt.Sprintf("%s|%d|%d|%dx%d", path, fi.Size(), fi.ModTime().UnixNano(), w, h)
	if t, ok := a.thumbs.get(key); ok {
		return t, nil
	}
	tj := a.cur().Settings().TrimJumps
	blocks, err := document.ReadDesignFile(path, tj)
	if err != nil {
		return DesignThumb{}, err
	}
	cols := make([]color.RGBA, len(blocks))
	sw := make([]string, len(blocks))
	all := pattern.EmptyRect()
	n := 0
	for i, b := range blocks {
		th := document.DefaultThreads[i%len(document.DefaultThreads)]
		cols[i] = preview.ParseHex(th.Color)
		sw[i] = th.Color
		all.Union(pattern.Bounds(b))
		n += pattern.Count(b).Stitches
	}
	png, err := preview.PNG(blocks, cols, w, h)
	if err != nil {
		return DesignThumb{}, err
	}
	t := DesignThumb{
		Image:    "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		Stitches: n, Colors: len(blocks),
		WidthMM: all.Width() / 10, HeightMM: all.Height() / 10,
		Swatches: sw,
	}
	a.thumbs.put(key, t)
	return t, nil
}

// ChooseBrowseFolder picks a folder for the browser and remembers it.
func (a *App) ChooseBrowseFolder() (BrowseResult, error) {
	cur, _ := a.browseDir()
	d, err := a.ui.PickFolder("Folder to browse for designs", cur)
	if err != nil || d == "" {
		return a.BrowseDesigns(cur)
	}
	a.prefsMu.Lock()
	a.prefs.BrowseDir = d
	a.prefsMu.Unlock()
	a.savePrefs()
	return a.BrowseDesigns(d)
}

// BrowseDownloads goes back to the Downloads folder.
func (a *App) BrowseDownloads() (BrowseResult, error) {
	a.prefsMu.Lock()
	a.prefs.BrowseDir = ""
	a.prefsMu.Unlock()
	a.savePrefs()
	return a.BrowseDesigns("")
}
