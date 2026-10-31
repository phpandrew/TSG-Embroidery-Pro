package lettering

import (
	"testing"

	builtinfonts "github.com/phpandrew/TSG-Embroidery-Pro/fonts"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// Fonts load lazily, keep kerning, and come back identical after being
// pushed out of the recently-used set.
func TestPackedFontsUnloadAndReload(t *testing.T) {
	r := NewRegistry()
	if errs := r.LoadFS(builtinfonts.FS); len(errs) > 0 {
		t.Fatal(errs)
	}
	var kerned *packedFont
	var names []string
	for _, fi := range r.List() {
		f, _ := r.Get(fi.Name)
		if pf, ok := f.(*packedFont); ok {
			names = append(names, fi.Name)
			if kerned == nil {
				pf.mu.Lock()
				_ = pf.load()
				if len(pf.kern) > 0 {
					kerned = pf
				}
				pf.mu.Unlock()
				pf.unload()
			}
		}
	}
	if kerned == nil {
		t.Fatal("expected at least one built-in font with kerning")
	}
	// find a kerned pair and check Kern returns it
	kerned.mu.Lock()
	_ = kerned.load()
	var pair [2]rune
	var want float32
	for k, v := range kerned.kern {
		pair, want = k, v
		break
	}
	kerned.mu.Unlock()
	if got := kerned.Kern(pair[0], pair[1]); float32(got) != want {
		t.Fatalf("kern %q = %v, want %v", string(pair[:]), got, want)
	}
	before, err := r.Layout(Spec{Text: "Abc", Font: kerned.Name()})
	if err != nil {
		t.Fatal(err)
	}
	// use every other font so this one is unloaded
	for _, n := range names {
		_, _ = r.Layout(Spec{Text: "Ab", Font: n})
	}
	kerned.mu.Lock()
	unloaded := !kerned.loaded
	kerned.mu.Unlock()
	if !unloaded {
		t.Fatal("least recently used font should have been unloaded")
	}
	after, err := r.Layout(Spec{Text: "Abc", Font: kerned.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if pattern.Count(before.Stitches) != pattern.Count(after.Stitches) || pattern.Bounds(before.Stitches) != pattern.Bounds(after.Stitches) {
		t.Fatal("font laid out differently after reloading")
	}
	loaded := 0
	for _, n := range names {
		f, _ := r.Get(n)
		pf := f.(*packedFont)
		pf.mu.Lock()
		if pf.loaded {
			loaded++
		}
		pf.mu.Unlock()
	}
	if loaded > maxLoadedPacked {
		t.Fatalf("%d fonts loaded, cap is %d", loaded, maxLoadedPacked)
	}
}
