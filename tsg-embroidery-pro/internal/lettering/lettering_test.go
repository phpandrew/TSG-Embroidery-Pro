package lettering

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/dst"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

func TestBlockLayout(t *testing.T) {
	r := NewRegistry()
	res, err := r.Layout(Spec{Text: "Andrew 23", Font: "Block", Height: 180})
	if err != nil {
		t.Fatal(err)
	}
	b := pattern.Bounds(res.Stitches)
	if math.Abs(b.Height()-180) > 12 {
		t.Fatalf("height %.1f, want ~180", b.Height())
	}
	if math.Abs(b.CenterX()) > 1 || math.Abs(b.CenterY()) > 1 {
		t.Fatal("not centred")
	}
	if pattern.Count(res.Stitches).Trims < 5 {
		t.Fatal("expected trims between letters")
	}
	if _, err := r.Layout(Spec{Text: "  "}); err == nil {
		t.Fatal("expected empty error")
	}
}

func TestGlyphFont(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	_ = dst.Write(&buf, []pattern.St{{X: 0, Y: 0, Cmd: pattern.Stitch}, {X: 100, Y: -250, Cmd: pattern.Stitch}}, dst.WriteOptions{})
	_ = os.WriteFile(filepath.Join(dir, "A.dst"), buf.Bytes(), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "font.json"), []byte(`{"name":"Test","capHeight":250,"glyphs":{"A":{"file":"A.dst"},"B":{"file":"../evil.dst"}}}`), 0o644)
	if _, err := LoadGlyphFont(dir); err == nil {
		t.Fatal("expected path escape to be rejected")
	}
	_ = os.WriteFile(filepath.Join(dir, "font.json"), []byte(`{"name":"Test","capHeight":250,"glyphs":{"A":{"file":"A.dst"}}}`), 0o644)
	r := NewRegistry()
	if errs := r.LoadDir(filepath.Dir(dir)); len(errs) > 0 {
		t.Log(errs)
	}
	f, err := LoadGlyphFont(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, adv, ok := f.Glyph('a', Spec{Height: 250})
	if !ok || adv <= 0 || len(g) == 0 {
		t.Fatal("glyph lookup failed")
	}
}

func TestLoadFSBuiltinStyle(t *testing.T) {
	var buf bytes.Buffer
	_ = dst.Write(&buf, []pattern.St{{X: 0, Y: 0, Cmd: pattern.Stitch}, {X: 80, Y: -200, Cmd: pattern.Stitch}}, dst.WriteOptions{})
	fsys := fstest.MapFS{
		"Varsity/font.json": {Data: []byte(`{"name":"Varsity","capHeight":200,"glyphs":{"A":{"file":"A.dst"}}}`)},
		"Varsity/A.dst":     {Data: buf.Bytes()},
		"README.md":         {Data: []byte("x")},
		"notafont/x.txt":    {Data: []byte("x")},
	}
	r := NewRegistry()
	if errs := r.LoadFS(fsys); len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, ok := r.Get("varsity"); !ok {
		t.Fatal("embedded font not registered")
	}
	if _, err := r.Layout(Spec{Text: "AA", Font: "Varsity", Height: 200}); err != nil {
		t.Fatal(err)
	}
}
