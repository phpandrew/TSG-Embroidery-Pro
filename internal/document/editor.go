package document

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/dst"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/formats"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

const (
	maxUndo      = 100
	maxFileBytes = 64 << 20 // refuse absurdly large inputs
	ProjectExt   = ".tep"
	projectMagic = "tsg-embroidery-pro"
)

// Editor wraps a Doc with undo/redo and a mutex; all UI calls go through it.
type Editor struct {
	mu    sync.Mutex
	doc   *Doc
	undo  []*Doc
	redo  []*Doc
	path  string // project path, "" until saved
	dirty bool
	Fonts *lettering.Registry
	// defaults are the settings new documents start with (the user's last
	// machine settings and hoop).
	defaults Settings
	// live edits (typing in the lettering panel) with the same key within a
	// few seconds share one undo step
	liveKey string
	liveAt  time.Time
	// source is the DST file this document was opened from, if any
	source string
	// version changes whenever the document does (auto-backup uses it)
	version uint64
}

// Version changes every time the document changes.
func (e *Editor) Version() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.version
}

// WriteBackup saves the document to path as a project without changing the
// editor's own save path or unsaved state.
func (e *Editor) WriteBackup(path string) error {
	e.mu.Lock()
	doc := e.doc
	e.mu.Unlock()
	data, err := json.Marshal(projectFile{Format: projectMagic, Version: 1, Doc: doc})
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// RestoreBackup loads a backup written by WriteBackup. origPath is where the
// work came from (a project to save back to, or a DST it was opened from);
// the restored document is marked unsaved.
func (e *Editor) RestoreBackup(backupPath, origPath string) error {
	if err := e.OpenProject(backupPath); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.path, e.source = "", ""
	if strings.EqualFold(filepath.Ext(origPath), ProjectExt) {
		e.path = origPath
	} else {
		e.source = origPath
	}
	e.dirty = true
	e.version++
	return nil
}

func NewEditor(fonts *lettering.Registry) *Editor {
	if fonts == nil {
		fonts = lettering.NewRegistry()
	}
	return &Editor{doc: NewDoc(), Fonts: fonts, defaults: DefaultSettings()}
}

// mutate runs fn on a working copy; on success the old doc goes on the undo stack.
func (e *Editor) mutate(fn func(d *Doc) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mutateLocked(fn)
}

// mutateLive is mutate for live edits: repeated calls with the same key in
// quick succession collapse into a single undo step.
func (e *Editor) mutateLive(key string, fn func(d *Doc) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	merge := key == e.liveKey && time.Since(e.liveAt) < 4*time.Second && len(e.undo) > 0
	if err := e.mutateLocked(fn); err != nil {
		return err
	}
	if merge {
		e.undo = e.undo[:len(e.undo)-1]
	}
	e.liveKey, e.liveAt = key, time.Now()
	return nil
}

func (e *Editor) mutateLocked(fn func(d *Doc) error) error {
	e.liveKey = ""
	e.version++
	work := e.doc.clone()
	if err := fn(work); err != nil {
		return err
	}
	e.undo = append(e.undo, e.doc)
	if len(e.undo) > maxUndo {
		e.undo = e.undo[1:]
	}
	e.redo = nil
	e.doc = work
	e.dirty = true
	return nil
}

func (e *Editor) Undo() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.liveKey = ""
	e.version++
	if n := len(e.undo); n > 0 {
		e.redo = append(e.redo, e.doc)
		e.doc = e.undo[n-1]
		e.undo = e.undo[:n-1]
		e.dirty = true
	}
}

func (e *Editor) Redo() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.liveKey = ""
	e.version++
	if n := len(e.redo); n > 0 {
		e.undo = append(e.undo, e.doc)
		e.doc = e.redo[n-1]
		e.redo = e.redo[:n-1]
		e.dirty = true
	}
}

// Dirty reports unsaved changes.
func (e *Editor) Dirty() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dirtyLocked()
}

// Blank reports a document with nothing in it that was never saved: opening
// a file can reuse its tab instead of making a new one.
func (e *Editor) Blank() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.path == "" && len(e.doc.Designs) == 0
}

// dirtyLocked reports unsaved work. A never-saved document with nothing in
// it has nothing to lose, even if designs were added and then deleted.
func (e *Editor) dirtyLocked() bool {
	return e.dirty && (e.path != "" || len(e.doc.Designs) > 0)
}

// Reset starts a new empty document.
func (e *Editor) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.doc, e.undo, e.redo, e.path, e.dirty, e.source = newDocWith(e.defaults), nil, nil, "", false, ""
	e.version++
}

// SetDefaults sets the settings new documents start with. If the current
// document is still empty and unsaved it picks them up too.
func (e *Editor) SetDefaults(s Settings) {
	if s.SPM <= 0 {
		return
	}
	s.Hoop = HoopByID(s.Hoop).ID
	e.mu.Lock()
	defer e.mu.Unlock()
	e.defaults = s
	if e.path == "" && !e.dirty && len(e.undo) == 0 && len(e.doc.Designs) == 0 {
		e.doc.Settings = s
	}
}

// Settings returns the current document's settings.
func (e *Editor) Settings() Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.doc.Settings
}

// ---- threads -------------------------------------------------------------

// threadFor returns the id of the n-th palette thread, creating defaults.
func (d *Doc) threadFor(n int) string {
	for len(d.Threads) <= n {
		i := len(d.Threads)
		t := DefaultThreads[i%len(DefaultThreads)]
		t.ID = d.newID("t")
		t.Needle = i%d.Settings.Needles + 1
		if d.Settings.Needles <= 0 {
			t.Needle = i + 1
		}
		d.Threads = append(d.Threads, t)
	}
	return d.Threads[n].ID
}

// SetThread creates or updates a palette entry.
func (e *Editor) SetThread(t Thread) (string, error) {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return "", errors.New("thread name required")
	}
	if !validColor(t.Color) {
		return "", fmt.Errorf("invalid colour %q", t.Color)
	}
	var id string
	err := e.mutate(func(d *Doc) error {
		if t.Needle < 1 || t.Needle > max(d.Settings.Needles, 1) {
			return fmt.Errorf("needle must be 1–%d", d.Settings.Needles)
		}
		if t.ID == "" {
			t.ID = d.newID("t")
			d.Threads = append(d.Threads, t)
			id = t.ID
			return nil
		}
		for i := range d.Threads {
			if d.Threads[i].ID == t.ID {
				d.Threads[i] = t
				id = t.ID
				return nil
			}
		}
		return errors.New("thread not found")
	})
	return id, err
}

func validColor(c string) bool {
	if len(c) != 7 || c[0] != '#' {
		return false
	}
	for _, r := range c[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// ---- import / open ---------------------------------------------------------

// ReadDST loads a DST into blocks (jump runs collapsed to Trim+Jump).
func ReadDST(r io.Reader, trimJumps int) ([][]pattern.St, string, error) {
	p, h, err := dst.Read(io.LimitReader(r, maxFileBytes))
	if err != nil {
		return nil, "", err
	}
	blocks := pattern.SplitBlocks(pattern.CollapseJumps(p.Stitches, trimJumps))
	if len(blocks) == 0 {
		return nil, "", errors.New("file contains no stitches")
	}
	return blocks, h.Label, nil
}

// readOther reads a non-DST stitch file (PES, PEC, JEF, EXP) into blocks.
func readOther(r io.Reader, ext string, trimJumps int) ([][]pattern.St, error) {
	p, err := formats.Read(r, ext)
	if err != nil {
		return nil, err
	}
	blocks := pattern.SplitBlocks(pattern.CollapseJumps(p.Stitches, trimJumps))
	if len(blocks) == 0 {
		return nil, errors.New("file contains no stitches")
	}
	return blocks, nil
}

// ImportFile adds a stitch file to the document (merge).
func (e *Editor) ImportFile(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ProjectExt {
		return "", e.OpenProject(path)
	}
	if ext != ".dst" && !formats.Supported(ext) {
		return "", fmt.Errorf("unsupported file type %q (DST, %s)", ext, strings.ToUpper(strings.Join(formats.Exts(), ", ")))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	e.mu.Lock()
	tj := e.doc.Settings.TrimJumps
	e.mu.Unlock()
	var blocks [][]pattern.St
	if ext == ".dst" {
		blocks, _, err = ReadDST(f, tj)
	} else {
		blocks, err = readOther(f, ext, tj)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return e.AddBlocks(name, path, blocks)
}

// AddBlocks adds a design built from raw blocks. Blocks map onto palette
// entries in order (block 1 -> thread 1 ...), matching needle order on a
// machine set up for the original file.
func (e *Editor) AddBlocks(name, source string, blocks [][]pattern.St) (string, error) {
	var id string
	err := e.mutate(func(d *Doc) error {
		ds := &Design{ID: d.newID("d"), Name: name, Kind: KindFile, Source: source, Visible: true}
		for i, b := range blocks {
			ds.Blocks = append(ds.Blocks, Block{Thread: d.threadFor(i), Stitches: b})
		}
		ob := ds.OrigBounds()
		ds.CX, ds.CY = ob.CenterX(), ob.CenterY()
		ds.T = pattern.Identity(ds.CX, ds.CY)
		d.Designs = append(d.Designs, ds)
		if len(d.Designs) == 1 && (d.Name == "" || d.Name == "Untitled") {
			d.Name = name
		}
		id = ds.ID
		return nil
	})
	return id, err
}

// OpenFile replaces the document with a single file (or project).
func (e *Editor) OpenFile(path string) error {
	if strings.EqualFold(filepath.Ext(path), ProjectExt) {
		return e.OpenProject(path)
	}
	e.Reset()
	if _, err := e.ImportFile(path); err != nil {
		return err
	}
	e.mu.Lock()
	e.undo, e.dirty, e.source = nil, false, path
	e.mu.Unlock()
	return nil
}

// Source is the DST file the document was opened from ("" for projects and
// new documents).
func (e *Editor) Source() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.source
}

// Title is the document name shown in the tab. A new, unnamed document is
// named after its first design.
func (e *Editor) Title() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if (e.doc.Name == "" || e.doc.Name == "Untitled") && len(e.doc.Designs) > 0 {
		return e.doc.Designs[0].Name
	}
	return e.doc.Name
}

// ---- project files -------------------------------------------------------

type projectFile struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Doc     *Doc   `json:"document"`
}

func (e *Editor) SaveProject(path string) error {
	if !strings.EqualFold(filepath.Ext(path), ProjectExt) {
		path += ProjectExt
	}
	e.mu.Lock()
	doc := e.doc
	e.mu.Unlock()
	data, err := json.Marshal(projectFile{Format: projectMagic, Version: 1, Doc: doc})
	if err != nil {
		return err
	}
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	e.mu.Lock()
	e.path, e.dirty = path, false
	e.mu.Unlock()
	return nil
}

func (e *Editor) OpenProject(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var pf projectFile
	if err := json.NewDecoder(io.LimitReader(f, maxFileBytes*4)).Decode(&pf); err != nil {
		return fmt.Errorf("not a valid project: %w", err)
	}
	if pf.Format != projectMagic || pf.Doc == nil {
		return errors.New("not a TSG Embroidery Pro project")
	}
	if pf.Version > 1 {
		return errors.New("project was saved by a newer version; please update")
	}
	if pf.Doc.Settings.SPM <= 0 {
		pf.Doc.Settings = DefaultSettings()
	}
	e.mu.Lock()
	e.doc, e.undo, e.redo, e.path, e.dirty = pf.Doc, nil, nil, path, false
	e.version++
	e.mu.Unlock()
	return nil
}

func (e *Editor) Path() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.path
}

// writeAtomic saves via a temp file + rename so a crash never leaves a
// half-written file. Some drives (USB stick emulators feeding the machine,
// SD readers, some network shares) refuse temp files or renames; for those
// it falls back to writing the file directly.
func writeAtomic(path string, data []byte) error {
	// USB sticks / machine stick emulators may only accept the design file
	// itself: build everything in memory and write just that one file.
	if isRemovable(path) {
		if err := writeDirect(path, data); err != nil {
			return fmt.Errorf("save %s: %w", filepath.Base(path), err)
		}
		return nil
	}
	err := writeViaTemp(path, data)
	if err == nil {
		return nil
	}
	if derr := writeDirect(path, data); derr != nil {
		return fmt.Errorf("save %s: %w", filepath.Base(path), derr)
	}
	return nil
}

func writeDirect(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	_ = f.Sync() // flush to the stick before the user pulls it
	return f.Close()
}

func writeViaTemp(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tsg-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// ---- export --------------------------------------------------------------

// Assemble merges all visible designs into one stitch list in sequence
// order, inserting colour changes where the needle changes and trims on
// long connectors. Coordinates are shifted so the machine start point is
// the design centre (or the hoop centre).
func (d *Doc) Assemble() []pattern.St {
	var out []pattern.St
	needle := -1
	var px, py float64
	started := false
	for _, ds := range d.Designs {
		if !ds.Visible {
			continue
		}
		for i := range ds.Blocks {
			sts := ds.Placed(i)
			t, _ := d.Thread(ds.Blocks[i].Thread)
			// find the first positioned record of the block
			fi := -1
			for k, s := range sts {
				if s.Cmd == pattern.Stitch || s.Cmd == pattern.Jump {
					fi = k
					break
				}
			}
			if fi < 0 {
				continue
			}
			changed := false
			if started && t.Needle != needle {
				out = append(out, pattern.St{X: px, Y: py, Cmd: pattern.ColorChange})
				changed = true
			}
			first := sts[fi]
			dist := math.Hypot(first.X-px, first.Y-py)
			if started && !changed && dist > d.Settings.TrimAbove && !hasLeadingTrim(sts[:fi+1]) {
				out = append(out, pattern.St{Cmd: pattern.Trim})
			}
			if first.Cmd == pattern.Stitch && (dist > 0.5 || !started) {
				out = append(out, pattern.St{X: first.X, Y: first.Y, Cmd: pattern.Jump})
			}
			for _, s := range sts {
				if s.Cmd == pattern.Trim && (changed || !started) {
					continue // colour change / start already cuts
				}
				out = append(out, s)
				if s.Cmd == pattern.Stitch || s.Cmd == pattern.Jump {
					px, py = s.X, s.Y
				}
				if s.Cmd == pattern.Stitch {
					changed = false
				}
			}
			needle = t.Needle
			started = true
		}
	}
	// drop a trailing trim and pick the origin
	for len(out) > 0 && out[len(out)-1].Cmd == pattern.Trim {
		out = out[:len(out)-1]
	}
	if d.Settings.StartAt != "hoop" {
		b := pattern.Bounds(out)
		if !b.Empty() {
			ox, oy := math.Round(b.CenterX()), math.Round(b.CenterY())
			for i := range out {
				out[i].X -= ox
				out[i].Y -= oy
			}
		}
	}
	out = append(out, pattern.St{Cmd: pattern.End})
	return out
}

func hasLeadingTrim(sts []pattern.St) bool {
	for _, s := range sts {
		if s.Cmd == pattern.Trim {
			return true
		}
	}
	return false
}

// ExportDST writes the assembled document.
func (e *Editor) ExportDST(path string) error {
	e.mu.Lock()
	doc := e.doc
	e.mu.Unlock()
	sts := doc.Assemble()
	if pattern.Count(sts).Stitches == 0 {
		return errors.New("nothing to export (no visible stitches)")
	}
	var buf bytes.Buffer
	label := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if err := dst.Write(&buf, sts, dst.WriteOptions{Label: label, TrimJumps: doc.Settings.TrimJumps}); err != nil {
		return err
	}
	return writeAtomic(path, buf.Bytes())
}
