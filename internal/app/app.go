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
	PickFolder(title, dir string) (string, error)
	Focus() // bring the window to the front
}

// Prefs are per-user app preferences (not saved in projects).
type Prefs struct {
	AutoCheckUpdates bool     `json:"autoCheckUpdates"`
	Prerelease       bool     `json:"prerelease"`
	LastCheck        int64    `json:"lastCheck"`
	LastOpenDir      string   `json:"lastOpenDir"` // folder of the last opened/merged file
	LastSaveDir      string   `json:"lastSaveDir"` // folder of the last save/export
	FavoriteFonts    []string `json:"favoriteFonts"`
	// Machine is the last machine settings and hoop used; new designs start with it.
	Machine *document.Settings `json:"machine,omitempty"`
	// Window is how the main window was left when the app last closed.
	Window *WindowPrefs `json:"window,omitempty"`
	// StartWithWindows launches the app (minimised) when the user signs in.
	StartWithWindows bool `json:"startWithWindows"`
	// WatchEnabled opens a new DST file as soon as it lands in WatchDir
	// (default: the Downloads folder).
	WatchEnabled bool   `json:"watchEnabled"`
	WatchDir     string `json:"watchDir"`
	// Recent is the recently opened files, newest first.
	Recent []string `json:"recent"`
	// QuickSends are the one-click export targets (one per machine).
	QuickSends []QuickSend `json:"quickSends"`
}

// WindowPrefs is the main window's size and state, restored at launch.
type WindowPrefs struct {
	Width      int  `json:"width"` // normal (not maximised) size
	Height     int  `json:"height"`
	Maximised  bool `json:"maximised"`
	Fullscreen bool `json:"fullscreen"`
}

// App is bound to the frontend.
type App struct {
	ctx     context.Context
	ui      UI
	fonts   *lettering.Registry
	tabsMu  sync.Mutex
	tabs    []*tab
	active  int
	nextTab int
	watch   *watcher
	watchMu sync.Mutex
	// auto-backup
	session  string
	backupMu sync.Mutex
	backedUp map[string]uint64
	upd      *updater.Updater
	prefs    Prefs
	prefsMu  sync.Mutex
	pending  *updater.Release
	cfgDir   string
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
		fonts:  fonts,
		upd:    updater.New(version.Repo, version.Version),
		prefs:  Prefs{AutoCheckUpdates: true, WatchEnabled: true},
		cfgDir: cfg,
		// unique per run so two sessions never share backup files
		session:  fmt.Sprintf("%s-%d", time.Now().Format("20060102-150405"), os.Getpid()),
		backedUp: map[string]uint64{},
	}
	a.loadPrefs()
	a.addTab(a.newEditor())
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
	a.syncStartup()
	a.restartWatch()
	a.startBackups()
	go func() {
		time.Sleep(1500 * time.Millisecond) // let the window finish loading
		a.offerRestore()
	}()
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
// WindowState returns the window size/state saved at the last close.
func WindowState(a *App) WindowPrefs {
	a.prefsMu.Lock()
	defer a.prefsMu.Unlock()
	if a.prefs.Window == nil {
		return WindowPrefs{}
	}
	return *a.prefs.Window
}

// SaveWindowState records the window state as the app closes. The normal
// size is only updated when the window isn't maximised or full screen.
func SaveWindowState(a *App, w, h int, maximised, fullscreen bool) {
	a.prefsMu.Lock()
	ws := WindowPrefs{}
	if a.prefs.Window != nil {
		ws = *a.prefs.Window
	}
	ws.Maximised, ws.Fullscreen = maximised, fullscreen
	if !maximised && !fullscreen && w >= 1100 && h >= 700 { // ignore minimised sizes
		ws.Width, ws.Height = w, h
	}
	a.prefs.Window = &ws
	a.prefsMu.Unlock()
	a.savePrefs()
}

func BeforeClose(a *App) bool {
	if !a.confirmQuit("quit") {
		return true
	}
	a.clearBackups()
	return false
}

func (a *App) view(err error) (document.View, error) {
	if err != nil {
		applog.Errorf("%v", err)
	}
	v := a.cur().State()
	v.Tabs, v.TabID = a.tabInfos()
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

// NewDocument starts a new design in its own tab.
func (a *App) NewDocument() (document.View, error) { return a.NewTab() }

// Open shows a file dialog and opens the file in a new tab (or the current
// one if it is blank).
func (a *App) Open() (document.View, error) {
	files, err := a.ui.OpenFiles("Open design or project", false, true, a.dir(false))
	a.rememberDir(false, files...)
	if err != nil || len(files) == 0 {
		return a.view(err)
	}
	applog.Infof("open %q", files[0])
	return a.view(a.openInTab(files[0]))
}

// Merge adds one or more DST files to the current document.
func (a *App) Merge() (document.View, error) {
	files, err := a.ui.OpenFiles("Merge designs", true, false, a.dir(false))
	a.rememberDir(false, files...)
	if err != nil {
		return a.view(err)
	}
	var errs []string
	ed := a.cur()
	for _, f := range files {
		applog.Infof("merge %q", f)
		if _, err := ed.ImportFile(f); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return a.view(errors.New(strings.Join(errs, "; ")))
	}
	return a.view(nil)
}

// OpenPath opens (replace=true, in a tab) or merges a file by path (drag &
// drop, command line).
func (a *App) OpenPath(path string, replace bool) (document.View, error) {
	applog.Infof("open path %q replace=%v", path, replace)
	a.rememberDir(false, path)
	if replace {
		return a.view(a.openInTab(path))
	}
	_, err := a.cur().ImportFile(path)
	return a.view(err)
}

func (a *App) Save() (document.View, error) {
	if p := a.cur().Path(); p != "" {
		err := a.cur().SaveProject(p)
		if err == nil {
			a.forgetBackup(a.activeID())
		}
		return a.view(err)
	}
	return a.SaveAs()
}

func (a *App) SaveAs() (document.View, error) {
	name := a.cur().Title()
	name = strings.TrimSuffix(name, filepath.Ext(name))
	p, err := a.ui.SaveFile("Save project", name+document.ProjectExt, document.ProjectExt, a.dir(true))
	a.rememberDir(true, p)
	if err != nil || p == "" {
		return a.view(err)
	}
	ed := a.cur()
	if err := ed.SaveProject(p); err != nil {
		return a.view(err)
	}
	a.addRecent(ed.Path())
	a.forgetBackup(a.activeID())
	return a.view(nil)
}

// ExportDST shows a save dialog and writes the merged design. Returns the path.
func (a *App) ExportDST() (string, error) {
	name := a.cur().Title()
	name = strings.TrimSuffix(name, filepath.Ext(name))
	p, err := a.ui.SaveFile("Export DST", name+".dst", ".dst", a.dir(true))
	a.rememberDir(true, p)
	if err != nil || p == "" {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(p), ".dst") {
		p += ".dst"
	}
	return p, a.cur().ExportDST(p)
}

// ExportDSTTo writes to an explicit path (dev server / batch use).
func (a *App) ExportDSTTo(path string) error { return a.cur().ExportDST(path) }

// ---- editing (thin wrappers returning the new view) ------------------------

func (a *App) Undo() (document.View, error) { a.cur().Undo(); return a.view(nil) }
func (a *App) Redo() (document.View, error) { a.cur().Redo(); return a.view(nil) }

func (a *App) MoveTo(id string, x, y float64) (document.View, error) {
	return a.view(a.cur().MoveTo(id, x, y))
}
func (a *App) MoveBy(ids []string, dx, dy float64) (document.View, error) {
	return a.view(a.cur().MoveBy(ids, dx, dy))
}
func (a *App) Rotate(id string, deg float64) (document.View, error) {
	return a.view(a.cur().Rotate(id, deg))
}
func (a *App) SetRotation(id string, deg float64) (document.View, error) {
	return a.view(a.cur().SetRotation(id, deg))
}
func (a *App) Mirror(id string, vertical bool) (document.View, error) {
	return a.view(a.cur().Mirror(id, vertical))
}
func (a *App) Center(id string) (document.View, error) { return a.view(a.cur().Center(id)) }
func (a *App) SetScale(id string, pct float64) (document.View, error) {
	return a.view(a.cur().SetScale(id, pct))
}
func (a *App) SetSize(id string, w, h float64, lock bool) (document.View, error) {
	return a.view(a.cur().SetSize(id, w, h, lock))
}
func (a *App) SetBox(id string, cx, cy, w, h float64, lock bool) (document.View, error) {
	return a.view(a.cur().SetBox(id, cx, cy, w, h, lock))
}
func (a *App) SnapToPrevious(id string) (document.View, error) {
	return a.view(a.cur().SnapToPrevious(id))
}
func (a *App) SetVisible(id string, v bool) (document.View, error) {
	return a.view(a.cur().SetVisible(id, v))
}
func (a *App) Rename(id, name string) (document.View, error) { return a.view(a.cur().Rename(id, name)) }
func (a *App) Delete(id string) (document.View, error)       { return a.view(a.cur().Delete(id)) }
func (a *App) Duplicate(id string) (document.View, error) {
	_, err := a.cur().Duplicate(id)
	return a.view(err)
}
func (a *App) MoveDesign(id string, delta int) (document.View, error) {
	return a.view(a.cur().MoveDesign(id, delta))
}
func (a *App) MoveBlock(id string, from, to int) (document.View, error) {
	return a.view(a.cur().MoveBlock(id, from, to))
}
func (a *App) SetBlockThread(id string, block int, thread string) (document.View, error) {
	return a.view(a.cur().SetBlockThread(id, block, thread))
}
func (a *App) ReverseBlock(id string, block int) (document.View, error) {
	return a.view(a.cur().ReverseBlock(id, block))
}

// Result pairs a view with a human message (counts, warnings).
type Result struct {
	View    document.View `json:"view"`
	Message string        `json:"message"`
}

func (a *App) CombineBlocks(id string) (Result, error) {
	n, err := a.cur().CombineBlocks(id)
	v, err := a.view(err)
	return Result{v, fmt.Sprintf("Combined %d block(s)", n)}, err
}

func (a *App) RemoveSmallStitches(id string, minLen float64) (Result, error) {
	n, err := a.cur().RemoveSmallStitches(id, minLen)
	v, err := a.view(err)
	return Result{v, fmt.Sprintf("Removed %d small stitch(es)", n)}, err
}

// AdjustDensity changes stitch density; factor 1.2 = 20% denser.
func (a *App) AdjustDensity(id string, factor float64) (Result, error) {
	r, err := a.cur().AdjustDensity(id, factor)
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
	_, err := a.cur().SetThread(t)
	return a.view(err)
}

func (a *App) SetSettings(s document.Settings) (document.View, error) {
	if err := a.cur().SetSettings(s); err != nil {
		return a.view(err)
	}
	// remember hoop and machine settings for the next new design / launch
	ed := a.cur()
	cur := ed.Settings()
	ed.SetDefaults(cur)
	a.prefsMu.Lock()
	a.prefs.Machine = &cur
	a.prefsMu.Unlock()
	a.savePrefs()
	return a.view(nil)
}

type LetteringResult struct {
	View    document.View `json:"view"`
	ID      string        `json:"id"`
	Warning string        `json:"warning"`
}

func (a *App) AddLettering(spec lettering.Spec) (LetteringResult, error) {
	id, warn, err := a.cur().AddLettering(spec)
	v, err := a.view(err)
	return LetteringResult{v, id, warn}, err
}

func (a *App) UpdateLettering(id string, spec lettering.Spec) (LetteringResult, error) {
	warn, err := a.cur().UpdateLettering(id, spec)
	v, err := a.view(err)
	return LetteringResult{v, id, warn}, err
}

// ---- font gallery --------------------------------------------------------

// Fonts lists every lettering font with metadata for the gallery.
func (a *App) Fonts() []lettering.FontInfo { return a.fonts.List() }

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
	res, err := a.fonts.Layout(lettering.Spec{Text: text, Font: font})
	if err != nil || len(res.Missing) > 0 {
		// font lacks these characters (other script, monogram set): show its own
		if sample := a.fonts.SampleText(font); sample != "" {
			if r2, err2 := a.fonts.Layout(lettering.Spec{Text: sample, Font: font}); err2 == nil {
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
	if !a.confirmQuit("restart to update") {
		return errors.New("update cancelled")
	}
	a.clearBackups()
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

// SetPrefs updates the update-check options (other prefs have their own
// setters).
func (a *App) SetPrefs(p Prefs) Prefs {
	a.prefsMu.Lock()
	a.prefs.AutoCheckUpdates, a.prefs.Prerelease = p.AutoCheckUpdates, p.Prerelease
	out := a.prefs
	a.prefsMu.Unlock()
	a.savePrefs()
	return out
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
