// Package app is the API surface bound to the frontend (Wails) and the
// browser dev server. Every editing call returns the full document View.
package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	builtinfonts "github.com/phpandrew/TSG-Embroidery-Pro/fonts"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/applog"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/document"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/preview"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/updater"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/version"
)

// UI is the platform layer (native dialogs, events). Wails provides the real
// one; the dev server provides a headless stub.
type UI interface {
	OpenFiles(title string, multi bool, project bool, dir string) ([]string, error)
	SaveFile(title, defaultName, ext, dir string) (string, error)
	Confirm(title, message string) bool
	Emit(event string, data any)
	SetTitle(title string)
	OpenURL(url string)
	Quit()
}

// Prefs are per-user app preferences (not saved in projects).
type Prefs struct {
	AutoCheckUpdates bool     `json:"autoCheckUpdates"`
	Prerelease       bool     `json:"prerelease"`
	LastCheck        int64    `json:"lastCheck"`
	LastOpenDir      string   `json:"lastOpenDir"` // folder of the last opened/merged file
	LastSaveDir      string   `json:"lastSaveDir"` // folder of the last save/export
	FavoriteFonts    []string `json:"favoriteFonts"`
}

// App is bound to the frontend.
type App struct {
	ctx     context.Context
	ui      UI
	ed      *document.Editor
	upd     *updater.Updater
	prefs   Prefs
	prefsMu sync.Mutex
	pending *updater.Release
	cfgDir  string
}

func New(ui UI) *App {
	fonts := lettering.NewRegistry()
	builtinErrs := fonts.LoadFS(builtinfonts.FS)
	cfg := ""
	if d, err := os.UserConfigDir(); err == nil {
		cfg = filepath.Join(d, "TSG Embroidery Pro")
		_ = os.MkdirAll(filepath.Join(cfg, "fonts"), 0o755)
		applog.Init(cfg)
		applog.Infof("start %s %s (%s) %s/%s", version.AppName, version.Version, version.Commit, runtime.GOOS, runtime.GOARCH)
		for _, err := range builtinErrs {
			applog.Errorf("built-in font: %v", err)
		}
		for _, err := range fonts.LoadDir(filepath.Join(cfg, "fonts")) {
			applog.Errorf("font: %v", err)
		}
	}
	if exe, err := os.Executable(); err == nil {
		fonts.LoadDir(filepath.Join(filepath.Dir(exe), "fonts"))
	}
	a := &App{
		ui:     ui,
		ed:     document.NewEditor(fonts),
		upd:    updater.New(version.Repo, version.Version),
		prefs:  Prefs{AutoCheckUpdates: true},
		cfgDir: cfg,
	}
	a.loadPrefs()
	// installed TrueType/OpenType fonts: names only, outlines load on use
	go func() {
		n := fonts.LoadSystemFonts(lettering.SystemFontDirs())
		applog.Infof("found %d installed fonts", n)
		if a.ui != nil {
			a.ui.Emit("fonts:changed", n)
		}
	}()
	return a
}

// Lifecycle hooks are package functions (not methods) so they are not
// exposed to the frontend by the Wails binder.

// SetUI swaps the platform layer (used once Wails has a context).
func SetUI(a *App, ui UI) { a.ui = ui }

// Startup is called by Wails when the window is ready.
func Startup(a *App, ctx context.Context) {
	a.ctx = ctx
	go func() {
		if !a.prefs.AutoCheckUpdates {
			return
		}
		time.Sleep(3 * time.Second)
		if rel, err := a.upd.Check(ctx, a.prefs.Prerelease); err == nil && rel != nil {
			a.pending = rel
			a.ui.Emit("update:available", rel)
		}
		a.prefsMu.Lock()
		a.prefs.LastCheck = time.Now().Unix()
		a.prefsMu.Unlock()
		a.savePrefs()
	}()
}

// BeforeClose asks about unsaved work. Returning true cancels closing.
func BeforeClose(a *App) bool {
	if a.ed.Dirty() {
		return !a.ui.Confirm("Unsaved changes", "Discard unsaved changes and quit?")
	}
	return false
}

func (a *App) view(err error) (document.View, error) {
	if err != nil {
		applog.Errorf("%v", err)
	}
	v := a.ed.State()
	title := version.AppName
	if v.Title != "" {
		mark := ""
		if v.Dirty {
			mark = "• "
		}
		title = mark + v.Title + " — " + version.AppName
	}
	a.ui.SetTitle(title)
	return v, err
}

// ---- info ----------------------------------------------------------------

type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Repo    string `json:"repo"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	FontDir string `json:"fontDir"`
	LogPath string `json:"logPath"`
}

func (a *App) Info() Info {
	fd := ""
	if a.cfgDir != "" {
		fd = filepath.Join(a.cfgDir, "fonts")
	}
	return Info{version.AppName, version.Version, version.Commit, version.Repo, runtime.GOOS, runtime.GOARCH, fd, applog.Path()}
}

// LogClient records a frontend message (JS errors, failed calls) in debug.log.
func (a *App) LogClient(level, msg string) {
	if len(msg) > 8000 {
		msg = msg[:8000] + "…"
	}
	if level == "error" {
		applog.Errorf("ui: %s", msg)
	} else {
		applog.Infof("ui: %s", msg)
	}
}

// OpenLogFolder shows the folder containing debug.log.
func (a *App) OpenLogFolder() error {
	p := applog.Path()
	if p == "" {
		return errors.New("logging is not available")
	}
	return openFolder(filepath.Dir(p))
}

func (a *App) State() document.View { v, _ := a.view(nil); return v }

// ---- files ---------------------------------------------------------------

func (a *App) confirmDiscard() bool {
	return !a.ed.Dirty() || a.ui.Confirm("Unsaved changes", "Discard unsaved changes?")
}

func (a *App) NewDocument() (document.View, error) {
	if !a.confirmDiscard() {
		return a.view(nil)
	}
	a.ed.Reset()
	return a.view(nil)
}

// Open shows a file dialog and replaces the document.
func (a *App) Open() (document.View, error) {
	if !a.confirmDiscard() {
		return a.view(nil)
	}
	files, err := a.ui.OpenFiles("Open design or project", false, true, a.dir(false))
	a.rememberDir(false, files...)
	if err != nil || len(files) == 0 {
		return a.view(err)
	}
	applog.Infof("open %q", files[0])
	return a.view(a.ed.OpenFile(files[0]))
}

// Merge adds one or more DST files to the current document.
func (a *App) Merge() (document.View, error) {
	files, err := a.ui.OpenFiles("Merge designs", true, false, a.dir(false))
	a.rememberDir(false, files...)
	if err != nil {
		return a.view(err)
	}
	var errs []string
	for _, f := range files {
		applog.Infof("merge %q", f)
		if _, err := a.ed.ImportFile(f); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return a.view(errors.New(strings.Join(errs, "; ")))
	}
	return a.view(nil)
}

// OpenPath opens (replace=true) or merges a file by path (drag & drop).
func (a *App) OpenPath(path string, replace bool) (document.View, error) {
	applog.Infof("open path %q replace=%v", path, replace)
	a.rememberDir(false, path)
	if replace {
		if !a.confirmDiscard() {
			return a.view(nil)
		}
		return a.view(a.ed.OpenFile(path))
	}
	_, err := a.ed.ImportFile(path)
	return a.view(err)
}

func (a *App) Save() (document.View, error) {
	if p := a.ed.Path(); p != "" {
		return a.view(a.ed.SaveProject(p))
	}
	return a.SaveAs()
}

func (a *App) SaveAs() (document.View, error) {
	name := a.ed.State().Title
	name = strings.TrimSuffix(name, filepath.Ext(name))
	p, err := a.ui.SaveFile("Save project", name+document.ProjectExt, document.ProjectExt, a.dir(true))
	a.rememberDir(true, p)
	if err != nil || p == "" {
		return a.view(err)
	}
	return a.view(a.ed.SaveProject(p))
}

// ExportDST shows a save dialog and writes the merged design. Returns the path.
func (a *App) ExportDST() (string, error) {
	name := a.ed.State().Title
	name = strings.TrimSuffix(name, filepath.Ext(name))
	p, err := a.ui.SaveFile("Export DST", name+".dst", ".dst", a.dir(true))
	a.rememberDir(true, p)
	if err != nil || p == "" {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(p), ".dst") {
		p += ".dst"
	}
	return p, a.ed.ExportDST(p)
}

// ExportDSTTo writes to an explicit path (dev server / batch use).
func (a *App) ExportDSTTo(path string) error { return a.ed.ExportDST(path) }

// ---- editing (thin wrappers returning the new view) ------------------------

func (a *App) Undo() (document.View, error) { a.ed.Undo(); return a.view(nil) }
func (a *App) Redo() (document.View, error) { a.ed.Redo(); return a.view(nil) }

func (a *App) MoveTo(id string, x, y float64) (document.View, error) {
	return a.view(a.ed.MoveTo(id, x, y))
}
func (a *App) MoveBy(ids []string, dx, dy float64) (document.View, error) {
	return a.view(a.ed.MoveBy(ids, dx, dy))
}
func (a *App) Rotate(id string, deg float64) (document.View, error) {
	return a.view(a.ed.Rotate(id, deg))
}
func (a *App) SetRotation(id string, deg float64) (document.View, error) {
	return a.view(a.ed.SetRotation(id, deg))
}
func (a *App) Mirror(id string, vertical bool) (document.View, error) {
	return a.view(a.ed.Mirror(id, vertical))
}
func (a *App) Center(id string) (document.View, error) { return a.view(a.ed.Center(id)) }
func (a *App) SetScale(id string, pct float64) (document.View, error) {
	return a.view(a.ed.SetScale(id, pct))
}
func (a *App) SetSize(id string, w, h float64, lock bool) (document.View, error) {
	return a.view(a.ed.SetSize(id, w, h, lock))
}
func (a *App) SetBox(id string, cx, cy, w, h float64, lock bool) (document.View, error) {
	return a.view(a.ed.SetBox(id, cx, cy, w, h, lock))
}
func (a *App) SnapToPrevious(id string) (document.View, error) {
	return a.view(a.ed.SnapToPrevious(id))
}
func (a *App) SetVisible(id string, v bool) (document.View, error) {
	return a.view(a.ed.SetVisible(id, v))
}
func (a *App) Rename(id, name string) (document.View, error) { return a.view(a.ed.Rename(id, name)) }
func (a *App) Delete(id string) (document.View, error)       { return a.view(a.ed.Delete(id)) }
func (a *App) Duplicate(id string) (document.View, error) {
	_, err := a.ed.Duplicate(id)
	return a.view(err)
}
func (a *App) MoveDesign(id string, delta int) (document.View, error) {
	return a.view(a.ed.MoveDesign(id, delta))
}
func (a *App) MoveBlock(id string, from, to int) (document.View, error) {
	return a.view(a.ed.MoveBlock(id, from, to))
}
func (a *App) SetBlockThread(id string, block int, thread string) (document.View, error) {
	return a.view(a.ed.SetBlockThread(id, block, thread))
}
func (a *App) ReverseBlock(id string, block int) (document.View, error) {
	return a.view(a.ed.ReverseBlock(id, block))
}

// Result pairs a view with a human message (counts, warnings).
type Result struct {
	View    document.View `json:"view"`
	Message string        `json:"message"`
}

func (a *App) CombineBlocks(id string) (Result, error) {
	n, err := a.ed.CombineBlocks(id)
	v, err := a.view(err)
	return Result{v, fmt.Sprintf("Combined %d block(s)", n)}, err
}

func (a *App) RemoveSmallStitches(id string, minLen float64) (Result, error) {
	n, err := a.ed.RemoveSmallStitches(id, minLen)
	v, err := a.view(err)
	return Result{v, fmt.Sprintf("Removed %d small stitch(es)", n)}, err
}

// AdjustDensity changes stitch density; factor 1.2 = 20% denser.
func (a *App) AdjustDensity(id string, factor float64) (Result, error) {
	r, err := a.ed.AdjustDensity(id, factor)
	v, err := a.view(err)
	msg := fmt.Sprintf("Density %.0f%%: %d → %d stitches", factor*100, r.Before, r.After)
	if !r.Lettering {
		msg += fmt.Sprintf(" (%d satin, %d fill areas rebuilt)", r.Satins, r.Fills)
	}
	if r.Clamped {
		msg += "; some areas capped at 0.15 mm spacing"
	}
	return Result{v, msg}, err
}

func (a *App) SetThread(t document.Thread) (document.View, error) {
	_, err := a.ed.SetThread(t)
	return a.view(err)
}

func (a *App) SetSettings(s document.Settings) (document.View, error) {
	return a.view(a.ed.SetSettings(s))
}

type LetteringResult struct {
	View    document.View `json:"view"`
	ID      string        `json:"id"`
	Warning string        `json:"warning"`
}

func (a *App) AddLettering(spec lettering.Spec) (LetteringResult, error) {
	id, warn, err := a.ed.AddLettering(spec)
	v, err := a.view(err)
	return LetteringResult{v, id, warn}, err
}

func (a *App) UpdateLettering(id string, spec lettering.Spec) (LetteringResult, error) {
	warn, err := a.ed.UpdateLettering(id, spec)
	v, err := a.view(err)
	return LetteringResult{v, id, warn}, err
}

// ---- font gallery --------------------------------------------------------

// Fonts lists every lettering font with metadata for the gallery.
func (a *App) Fonts() []lettering.FontInfo { return a.ed.Fonts.List() }

// FontPreview renders text in a font at its native size to a PNG data URL.
func (a *App) FontPreview(font, text string, width, height int) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "Abc 123"
	}
	if len([]rune(text)) > 40 {
		text = string([]rune(text)[:40])
	}
	width = min(max(width, 80), 1600)
	height = min(max(height, 30), 800)
	res, err := a.ed.Fonts.Layout(lettering.Spec{Text: text, Font: font})
	if err != nil || len(res.Missing) > 0 {
		// font lacks these characters (other script, monogram set): show its own
		if sample := a.ed.Fonts.SampleText(font); sample != "" {
			if r2, err2 := a.ed.Fonts.Layout(lettering.Spec{Text: sample, Font: font}); err2 == nil {
				res, err = r2, nil
			}
		}
	}
	if err != nil {
		return "", err
	}
	var cols []color.RGBA
	for _, c := range res.Colors {
		cols = append(cols, preview.ParseHex(c))
	}
	if len(cols) < 2 { // single-colour fonts preview in navy
		cols = nil
	}
	png, err := preview.PNG(res.Blocks, cols, width, height)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// ToggleFavoriteFont adds or removes a font from favourites.
func (a *App) ToggleFavoriteFont(name string) Prefs {
	a.prefsMu.Lock()
	favs := []string{}
	found := false
	for _, f := range a.prefs.FavoriteFonts {
		if f == name {
			found = true
			continue
		}
		favs = append(favs, f)
	}
	if !found {
		favs = append(favs, name)
	}
	a.prefs.FavoriteFonts = favs
	p := a.prefs
	a.prefsMu.Unlock()
	a.savePrefs()
	return p
}

// ---- updates -------------------------------------------------------------

// CheckForUpdates queries GitHub. Returns nil when up to date.
func (a *App) CheckForUpdates() (*updater.Release, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a.prefsMu.Lock()
	pre := a.prefs.Prerelease
	a.prefsMu.Unlock()
	rel, err := a.upd.Check(ctx, pre)
	if err != nil {
		return nil, err
	}
	a.pending = rel
	return rel, nil
}

// InstallUpdate downloads + verifies the pending update, then restarts.
func (a *App) InstallUpdate() error {
	if a.pending == nil {
		return errors.New("no update pending; check first")
	}
	if a.ed.Dirty() && !a.ui.Confirm("Unsaved changes", "The app will restart after updating. Discard unsaved changes?") {
		return errors.New("update cancelled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := a.upd.Install(ctx, a.pending); err != nil {
		return err
	}
	if err := updater.Restart(); err != nil {
		return fmt.Errorf("updated, but restart failed (please reopen the app): %w", err)
	}
	a.ui.Quit()
	return nil
}

func (a *App) OpenReleasePage() {
	url := "https://github.com/" + version.Repo + "/releases"
	if a.pending != nil && strings.HasPrefix(a.pending.PageURL, "https://github.com/") {
		url = a.pending.PageURL
	}
	a.ui.OpenURL(url)
}

func (a *App) OpenRepoPage() { a.ui.OpenURL("https://github.com/" + version.Repo) }

func (a *App) GetPrefs() Prefs {
	a.prefsMu.Lock()
	defer a.prefsMu.Unlock()
	return a.prefs
}

func (a *App) SetPrefs(p Prefs) Prefs {
	a.prefsMu.Lock()
	p.LastCheck = a.prefs.LastCheck
	p.LastOpenDir, p.LastSaveDir = a.prefs.LastOpenDir, a.prefs.LastSaveDir
	if p.FavoriteFonts == nil {
		p.FavoriteFonts = a.prefs.FavoriteFonts
	}
	a.prefs = p
	a.prefsMu.Unlock()
	a.savePrefs()
	return p
}

// dir returns the folder a file dialog should start in (remembered across
// launches). Saves fall back to the last open folder.
func (a *App) dir(save bool) string {
	a.prefsMu.Lock()
	defer a.prefsMu.Unlock()
	d := a.prefs.LastOpenDir
	if save && a.prefs.LastSaveDir != "" {
		d = a.prefs.LastSaveDir
	}
	if d == "" {
		return ""
	}
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		return ""
	}
	return d
}

func (a *App) rememberDir(save bool, files ...string) {
	if len(files) == 0 || files[0] == "" {
		return
	}
	d := filepath.Dir(files[0])
	a.prefsMu.Lock()
	if save {
		a.prefs.LastSaveDir = d
	} else {
		a.prefs.LastOpenDir = d
	}
	a.prefsMu.Unlock()
	a.savePrefs()
}

func (a *App) prefsPath() string {
	if a.cfgDir == "" {
		return ""
	}
	return filepath.Join(a.cfgDir, "prefs.json")
}

func (a *App) loadPrefs() {
	p := a.prefsPath()
	if p == "" {
		return
	}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &a.prefs)
	}
}

func (a *App) savePrefs() {
	p := a.prefsPath()
	if p == "" {
		return
	}
	a.prefsMu.Lock()
	b, _ := json.MarshalIndent(a.prefs, "", "  ")
	a.prefsMu.Unlock()
	_ = os.WriteFile(p, b, 0o644)
}
