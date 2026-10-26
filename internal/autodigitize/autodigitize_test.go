package autodigitize

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/preview"
)

func load(t *testing.T, name string) *Result {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(img, Options{Width: 800, Colors: 2, RemoveBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestLogoPNG(t *testing.T) {
	res := load(t, "logo.png")
	if len(res.Blocks) != 2 {
		t.Fatalf("expected blue + red blocks, got %d", len(res.Blocks))
	}
	all := pattern.EmptyRect()
	total := 0
	for _, b := range res.Blocks {
		all.Union(pattern.Bounds(b))
		total += pattern.Count(b).Stitches
	}
	if w := all.Width(); w < 700 || w > 800 {
		t.Fatalf("width %.0f, want ~720-800 (80 mm minus margins)", w)
	}
	if total < 1000 {
		t.Fatalf("too few stitches: %d", total)
	}
	// the ring's hole must not be filled: no stitch near the circle centre
	cx := all.MinX + 200 // circle: 100 px radius at 2 units/px, touching the left/top of the artwork
	cy := all.MinY + 200
	for _, s := range res.Blocks[0] {
		if s.Cmd == pattern.Stitch && (s.X-cx)*(s.X-cx)+(s.Y-cy)*(s.Y-cy) < 70*70 {
			t.Fatalf("hole was filled: stitch at %.0f,%.0f near centre %.0f,%.0f", s.X, s.Y, cx, cy)
		}
	}
	// write a preview PNG for eyeballing
	cols := res.Colors
	pngBytes, err := preview.PNG(res.Blocks, cols, 800, 600)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(t.TempDir(), "logo.png"), pngBytes, 0o644)
	_ = os.WriteFile("/tmp/claude-0/-home-claude/7002f46e-45fc-5dd8-8a35-1687e5f117c9/scratchpad/ad-logo.png", pngBytes, 0o644)
	f, _ := os.Create("/tmp/claude-0/-home-claude/7002f46e-45fc-5dd8-8a35-1687e5f117c9/scratchpad/ad-reduced.png")
	_ = png.Encode(f, res.Preview)
	f.Close()
}

func TestOtherFormatsAndAlpha(t *testing.T) {
	for _, n := range []string{"logo.jpg", "logo.webp"} {
		if res := load(t, n); len(res.Blocks) != 2 {
			t.Fatalf("%s: %d blocks", n, len(res.Blocks))
		}
	}
	f, _ := os.Open("testdata/square_alpha.png")
	defer f.Close()
	img, _ := Decode(f)
	res, err := Convert(img, Options{Width: 400, Colors: 1})
	if err != nil {
		t.Fatal(err)
	}
	b := pattern.Bounds(res.Blocks[0])
	if len(res.Blocks) != 1 || b.Width() < 220 || b.Width() > 250 {
		t.Fatalf("transparent border should be ignored: blocks=%d width=%.0f", len(res.Blocks), b.Width())
	}
}

func TestDecodePSD(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "psd", "testdata", "rgb8_rle.psd"))
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	img, err := Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() == 0 {
		t.Fatal("empty PSD")
	}
}
