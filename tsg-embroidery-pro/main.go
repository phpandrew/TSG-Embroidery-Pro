// TSG Embroidery Pro — view, merge, resize and letter Tajima DST files.
package main

import (
	"context"
	"embed"
	"log"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/app"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/version"
)

//go:embed all:frontend/dist
var assets embed.FS

// wailsUI implements app.UI on top of the Wails runtime.
type wailsUI struct{ ctx context.Context }

func (u *wailsUI) OpenFiles(title string, multi, project bool, dir string) ([]string, error) {
	filters := []wr.FileFilter{{DisplayName: "Tajima DST (*.dst)", Pattern: "*.dst;*.DST"}}
	if project {
		filters = []wr.FileFilter{
			{DisplayName: "Designs and projects", Pattern: "*.dst;*.DST;*.tep"},
			{DisplayName: "Tajima DST (*.dst)", Pattern: "*.dst;*.DST"},
			{DisplayName: "Project (*.tep)", Pattern: "*.tep"},
		}
	}
	opts := wr.OpenDialogOptions{Title: title, Filters: filters, DefaultDirectory: dir}
	if multi {
		return wr.OpenMultipleFilesDialog(u.ctx, opts)
	}
	f, err := wr.OpenFileDialog(u.ctx, opts)
	if f == "" {
		return nil, err
	}
	return []string{f}, err
}

func (u *wailsUI) SaveFile(title, def, ext, dir string) (string, error) {
	name := strings.ToUpper(strings.TrimPrefix(ext, "."))
	return wr.SaveFileDialog(u.ctx, wr.SaveDialogOptions{
		Title: title, DefaultFilename: def, DefaultDirectory: dir,
		Filters: []wr.FileFilter{{DisplayName: name + " (*" + ext + ")", Pattern: "*" + ext}},
	})
}

func (u *wailsUI) Confirm(title, msg string) bool {
	r, err := wr.MessageDialog(u.ctx, wr.MessageDialogOptions{
		Type: wr.QuestionDialog, Title: title, Message: msg,
		Buttons: []string{"Yes", "No"}, DefaultButton: "No", CancelButton: "No",
	})
	return err == nil && strings.EqualFold(r, "Yes")
}

func (u *wailsUI) Emit(ev string, data any) { wr.EventsEmit(u.ctx, ev, data) }
func (u *wailsUI) SetTitle(t string)        { wr.WindowSetTitle(u.ctx, t) }
func (u *wailsUI) OpenURL(url string)       { wr.BrowserOpenURL(u.ctx, url) }
func (u *wailsUI) Quit()                    { wr.Quit(u.ctx) }

func buildMenu(emit func(string)) *menu.Menu {
	m := menu.NewMenu()
	item := func(sub *menu.Menu, label string, acc *keys.Accelerator, action string) {
		sub.AddText(label, acc, func(*menu.CallbackData) { emit(action) })
	}
	file := m.AddSubmenu("File")
	item(file, "New", keys.CmdOrCtrl("n"), "new")
	item(file, "Open…", keys.CmdOrCtrl("o"), "open")
	item(file, "Merge…", keys.CmdOrCtrl("m"), "merge")
	file.AddSeparator()
	item(file, "Save project", keys.CmdOrCtrl("s"), "save")
	item(file, "Save project as…", keys.Combo("s", keys.CmdOrCtrlKey, keys.ShiftKey), "saveAs")
	item(file, "Export DST…", keys.CmdOrCtrl("e"), "export")
	file.AddSeparator()
	item(file, "Production worksheet…", keys.CmdOrCtrl("p"), "worksheet")
	item(file, "Print colour block analysis…", keys.Combo("p", keys.CmdOrCtrlKey, keys.ShiftKey), "printBlocks")
	file.AddSeparator()
	item(file, "Exit", nil, "quit")

	edit := m.AddSubmenu("Edit")
	item(edit, "Undo", keys.CmdOrCtrl("z"), "undo")
	item(edit, "Redo", keys.CmdOrCtrl("y"), "redo")
	edit.AddSeparator()
	item(edit, "Duplicate", keys.CmdOrCtrl("d"), "duplicate")
	item(edit, "Delete", nil, "delete")

	view := m.AddSubmenu("View")
	item(view, "Zoom in", keys.CmdOrCtrl("="), "zoomIn")
	item(view, "Zoom out", keys.CmdOrCtrl("-"), "zoomOut")
	item(view, "Fit to hoop", keys.CmdOrCtrl("0"), "fit")
	item(view, "Toggle jumps", keys.CmdOrCtrl("j"), "toggleJumps")
	view.AddSeparator()
	item(view, "Sequence grid", keys.CmdOrCtrl("g"), "grid")

	design := m.AddSubmenu("Design")
	item(design, "Rotate 90°", keys.CmdOrCtrl("r"), "rotate")
	item(design, "Mirror horizontal", nil, "mirror")
	item(design, "Mirror vertical", nil, "mirrorV")
	item(design, "Center selection", nil, "center")
	item(design, "Center all in hoop", nil, "centerAll")
	design.AddSeparator()
	item(design, "Add lettering", keys.CmdOrCtrl("t"), "lettering")

	tools := m.AddSubmenu("Tools")
	item(tools, "Remove small stitches…", nil, "removeSmall")
	item(tools, "Combine same-colour blocks", nil, "combine")
	item(tools, "Machine settings…", nil, "production")

	help := m.AddSubmenu("Help")
	item(help, "Check for updates…", nil, "checkUpdates")
	item(help, "Open debug log folder", nil, "logs")
	item(help, "Project on GitHub", nil, "github")
	item(help, "About", nil, "about")
	return m
}

func main() {
	ui := &wailsUI{}
	a := app.New(ui)

	startupFile := ""
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		startupFile = os.Args[1]
	}

	err := wails.Run(&options.App{
		Title:            version.AppName,
		Width:            1480,
		Height:           940,
		MinWidth:         1100,
		MinHeight:        700,
		BackgroundColour: &options.RGBA{R: 246, G: 247, B: 249, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		Menu:             buildMenu(func(action string) { ui.Emit("menu", action) }),
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true},
		OnStartup: func(ctx context.Context) {
			ui.ctx = ctx
			app.Startup(a, ctx)
			wr.OnFileDrop(ctx, func(_, _ int, paths []string) { ui.Emit("files:dropped", paths) })
			if startupFile != "" {
				go func() { _, _ = a.OpenPath(startupFile, true); ui.Emit("state:changed", nil) }()
			}
		},
		OnBeforeClose: func(ctx context.Context) bool { return app.BeforeClose(a) },
		Bind:          []any{a},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.SystemDefault,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
