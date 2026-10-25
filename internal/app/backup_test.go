package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
)

type stubUI struct{ confirm bool }

func (stubUI) OpenFiles(string, bool, bool, string) ([]string, error)  { return nil, nil }
func (stubUI) SaveFile(string, string, string, string) (string, error) { return "", nil }
func (u stubUI) Confirm(string, string) bool                           { return u.confirm }
func (stubUI) Emit(string, any)                                        {}
func (stubUI) SetTitle(string)                                         {}
func (stubUI) OpenURL(string)                                          {}
func (stubUI) Quit()                                                   {}
func (stubUI) PickFolder(string, string) (string, error)               { return "", nil }
func (stubUI) Focus()                                                  {}

func testApp(cfg, session string, ui UI) *App {
	a := &App{ui: ui, fonts: lettering.NewRegistry(), cfgDir: cfg, session: session, backedUp: map[string]uint64{}}
	a.addTab(a.newEditor())
	return a
}

func TestBackupAndRestore(t *testing.T) {
	cfg := t.TempDir()
	a := testApp(cfg, "s1", stubUI{confirm: true})
	if _, err := a.AddLettering(lettering.Spec{Text: "CRASH", Font: "Block", Height: 100}); err != nil {
		t.Fatal(err)
	}
	a.backupOnce()
	files, _ := filepath.Glob(filepath.Join(cfg, "backups", "s1-*.tep"))
	if len(files) != 1 {
		t.Fatalf("expected one backup, got %v", files)
	}

	// next launch after a crash
	b := testApp(cfg, "s2", stubUI{confirm: true})
	b.offerRestore()
	v := b.State()
	if len(v.Tabs) != 1 || v.Tabs[0].Title != "CRASH" || !v.Tabs[0].Dirty || len(v.Designs) != 1 {
		t.Fatalf("restore failed: tabs=%+v designs=%d", v.Tabs, len(v.Designs))
	}
	if left, _ := filepath.Glob(filepath.Join(cfg, "backups", "s1-*")); len(left) != 0 {
		t.Fatalf("restored backups should be removed: %v", left)
	}

	// clean quit removes this session's backups
	b.backupOnce()
	b.clearBackups()
	if left, _ := os.ReadDir(filepath.Join(cfg, "backups")); len(left) != 0 {
		t.Fatalf("clean quit should leave no backups, found %d", len(left))
	}
}

func TestDeclinedRestoreDeletesBackups(t *testing.T) {
	cfg := t.TempDir()
	a := testApp(cfg, "s1", stubUI{confirm: true})
	_, _ = a.AddLettering(lettering.Spec{Text: "X", Font: "Block", Height: 100})
	a.backupOnce()
	b := testApp(cfg, "s2", stubUI{confirm: false})
	b.offerRestore()
	if left, _ := filepath.Glob(filepath.Join(cfg, "backups", "*.tep")); len(left) != 0 {
		t.Fatalf("declined restore should delete backups: %v", left)
	}
}

func TestQuickSendFileSafe(t *testing.T) {
	if got := fileSafe(` Order: 123/A? `); got != "Order_ 123_A_" {
		t.Fatalf("fileSafe = %q", got)
	}
	if fileSafe("...") != "design" {
		t.Fatal("empty names fall back to design")
	}
}
