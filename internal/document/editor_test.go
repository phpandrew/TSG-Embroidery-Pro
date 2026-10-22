package document

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/dst"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

func square(cx, cy float64) []pattern.St {
	var s []pattern.St
	for i := 0; i <= 40; i++ {
		s = append(s, pattern.St{X: cx + float64(i%2)*100 - 50, Y: cy + float64(i)*2.5 - 50, Cmd: pattern.Stitch})
	}
	return s
}

func writeSample(t *testing.T, dir string) string {
	t.Helper()
	sts := square(0, 0)
	sts = append(sts, pattern.St{Cmd: pattern.ColorChange})
	sts = append(sts, square(300, 0)...)
	var buf bytes.Buffer
	if err := dst.Write(&buf, sts, dst.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "sample.dst")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMergeExportUndo(t *testing.T) {
	dir := t.TempDir()
	src := writeSample(t, dir)
	e := NewEditor(lettering.NewRegistry())
	if err := e.OpenFile(src); err != nil {
		t.Fatal(err)
	}
	id2, err := e.ImportFile(src) // merge a second copy
	if err != nil {
		t.Fatal(err)
	}
	if err := e.MoveBy([]string{id2}, 0, 400); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.AddLettering(lettering.Spec{Text: "AB", Font: "Block", Height: 100}); err != nil {
		t.Fatal(err)
	}
	v := e.State()
	if len(v.Designs) != 3 || len(v.Threads) != 2 {
		t.Fatalf("designs=%d threads=%d", len(v.Designs), len(v.Threads))
	}
	// blocks: A(t1) B(t2) | A(t1) B(t2) | lettering(t1) => 4 colour changes
	if v.Summary.ColorChanges != 4 {
		t.Fatalf("colour changes = %d", v.Summary.ColorChanges)
	}
	out := filepath.Join(dir, "out.dst")
	if err := e.ExportDST(out); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(out)
	p, _, err := dst.Read(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := pattern.Count(p.Stitches).Stitches; got < v.Summary.Stitches {
		t.Fatalf("exported %d stitches, summary %d", got, v.Summary.Stitches)
	}
	if got := pattern.Count(p.Stitches).ColorChanges; got != 4 {
		t.Fatalf("exported colour changes = %d", got)
	}
	// design-centre start: exported bounds centred on origin
	b := pattern.Bounds(p.Stitches)
	if abs(b.CenterX()) > 2 || abs(b.CenterY()) > 2 {
		t.Fatalf("not centred: %+v", b)
	}

	e.Undo()
	e.Undo()
	if n := len(e.State().Designs); n != 2 {
		t.Fatalf("after undo designs=%d", n)
	}
	e.Redo()
	if n := len(e.State().Designs); n != 2 {
		t.Fatalf("after redo designs=%d", n)
	}

	proj := filepath.Join(dir, "job")
	if err := e.SaveProject(proj); err != nil {
		t.Fatal(err)
	}
	e2 := NewEditor(nil)
	if err := e2.OpenProject(proj + ProjectExt); err != nil {
		t.Fatal(err)
	}
	if a, b := e.State().Summary, e2.State().Summary; a.Stitches != b.Stitches || a.ColorChanges != b.ColorChanges {
		t.Fatalf("project roundtrip mismatch %+v vs %+v", a, b)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestSizeAndScale(t *testing.T) {
	e := NewEditor(nil)
	id, _ := e.AddBlocks("sq", "", [][]pattern.St{square(0, 0)})
	if err := e.SetSize(id, 200, 0, true); err != nil {
		t.Fatal(err)
	}
	d := e.State().Designs[0]
	if abs(d.Bounds.Width()-200) > 0.01 || abs(d.Bounds.Height()-200) > 0.01 {
		t.Fatalf("size %+v", d.Bounds)
	}
	if err := e.SetScale(id, 110); err != nil {
		t.Fatal(err)
	}
	if d := e.State().Designs[0]; abs(d.Bounds.Width()-110) > 0.01 {
		t.Fatalf("scale %+v", d.Bounds)
	}
	if err := e.Rotate(id, 90); err != nil {
		t.Fatal(err)
	}
	if err := e.Center(""); err != nil {
		t.Fatal(err)
	}
	if d := e.State().Designs[0]; abs(d.Bounds.CenterX()) > 0.01 {
		t.Fatal("center failed")
	}
}

func TestBadProject(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.tep")
	_ = os.WriteFile(p, []byte(`{"format":"other"}`), 0o644)
	if err := NewEditor(nil).OpenProject(p); err == nil {
		t.Fatal("expected error")
	}
}

// Regression: an empty document must serialise lists as [] (not null);
// the UI crashed on startup with "Cannot read properties of null (reading 'length')".
func TestEmptyStateHasNoNullLists(t *testing.T) {
	b, err := json.Marshal(NewEditor(nil).State())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"designs":null`, `"threads":null`, `"sequence":null`, `"blocks":null`, `"fonts":null`} {
		if strings.Contains(string(b), k) {
			t.Fatalf("state contains %s", k)
		}
	}
}

func TestSnapToPrevious(t *testing.T) {
	e := NewEditor(nil)
	a, _ := e.AddBlocks("a", "", [][]pattern.St{square(0, 0)})
	b, _ := e.AddBlocks("b", "", [][]pattern.St{square(0, 0)})
	if err := e.SnapToPrevious(a); err == nil {
		t.Fatal("first design should not snap")
	}
	if err := e.SnapToPrevious(b); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	_, _, ex, ey, _ := e.doc.Designs[0].StartEnd()
	sx, sy, _, _, _ := e.doc.Designs[1].StartEnd()
	e.mu.Unlock()
	if abs(ex-sx) > 1e-9 || abs(ey-sy) > 1e-9 {
		t.Fatalf("start %v,%v != end %v,%v", sx, sy, ex, ey)
	}
	if v := e.State(); v.Summary.Trims != 0 || v.Summary.Jumps > 1 {
		t.Fatalf("connected designs should need no trim: %+v", v.Summary)
	}
}

func TestLiveLetteringEditsShareOneUndo(t *testing.T) {
	e := NewEditor(nil)
	id, _, err := e.AddLettering(lettering.Spec{Text: "A", Font: "Block", Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, txt := range []string{"AB", "ABC", "ABCD"} {
		if _, err := e.UpdateLettering(id, lettering.Spec{Text: txt, Font: "Block", Height: 100}); err != nil {
			t.Fatal(err)
		}
	}
	e.Undo()
	if ds := e.State().Designs; len(ds) != 1 || ds[0].ID != id || ds[0].Name != "A" {
		t.Fatalf("one undo should go back to before typing, got %+v", ds)
	}
}
