package lettering

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/stitchgen"
)

// Styles for outline (TrueType/OpenType) fonts.
const (
	StyleFill        = "fill"         // tatami fill (default)
	StyleFillOutline = "fill+outline" // fill with a satin border
	StyleSatin       = "satin"        // satin border only
	StyleRun         = "run"          // running-stitch outline only
)

// sysFont auto-digitizes letters from an installed TrueType/OpenType font.
// Stitches are generated for the requested size, so density stays constant.
type sysFont struct {
	name  string
	path  string
	index int // font index inside a .ttc collection

	once sync.Once
	err  error
	f    *sfnt.Font
	capH float64 // cap height in font units
	upm  float64

	mu    sync.Mutex
	cache map[string]sysGlyph
}

type sysGlyph struct {
	sts []pattern.St
	adv float64
}

func (f *sysFont) Name() string    { return f.name }
func (f *sysFont) Native() float64 { return 0 }
func (f *sysFont) Path() string    { return f.path }

func (f *sysFont) load() error {
	f.once.Do(func() {
		data, err := os.ReadFile(f.path)
		if err != nil {
			f.err = err
			return
		}
		f.f, f.err = parseIndex(data, f.index)
		if f.err != nil {
			return
		}
		f.upm = float64(f.f.UnitsPerEm())
		f.capH = f.upm * 0.7
		var buf sfnt.Buffer
		for _, r := range "HIEX" {
			if segs, ok := f.outline(&buf, r); ok {
				b := segBounds(segs)
				if b.Height() > 0 {
					f.capH = b.Height()
					break
				}
			}
		}
		f.cache = map[string]sysGlyph{}
	})
	return f.err
}

func parseIndex(data []byte, index int) (*sfnt.Font, error) {
	if index == 0 {
		if f, err := sfnt.Parse(data); err == nil {
			return f, nil
		}
	}
	c, err := sfnt.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	return c.Font(index)
}

// outline returns the glyph's contours in font units (y down, baseline 0).
func (f *sysFont) outline(buf *sfnt.Buffer, r rune) ([][]stitchgen.Pt, bool) {
	gi, err := f.f.GlyphIndex(buf, r)
	if err != nil || gi == 0 {
		return nil, false
	}
	segs, err := f.f.LoadGlyph(buf, gi, fixed.I(int(f.upm)), nil)
	if err != nil {
		return nil, false
	}
	var contours [][]stitchgen.Pt
	var cur []stitchgen.Pt
	pt := func(p fixed.Point26_6) stitchgen.Pt {
		return stitchgen.Pt{X: float64(p.X) / 64, Y: float64(p.Y) / 64}
	}
	step := f.upm / 120 // curve flattening resolution
	for _, s := range segs {
		switch s.Op {
		case sfnt.SegmentOpMoveTo:
			if len(cur) > 2 {
				contours = append(contours, cur)
			}
			cur = []stitchgen.Pt{pt(s.Args[0])}
		case sfnt.SegmentOpLineTo:
			cur = append(cur, pt(s.Args[0]))
		case sfnt.SegmentOpQuadTo:
			p0 := cur[len(cur)-1]
			p1, p2 := pt(s.Args[0]), pt(s.Args[1])
			n := int(math.Ceil((dist(p0, p1) + dist(p1, p2)) / step))
			n = max(2, min(n, 32))
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				a, b := lerpPt(p0, p1, t), lerpPt(p1, p2, t)
				cur = append(cur, lerpPt(a, b, t))
			}
		case sfnt.SegmentOpCubeTo:
			p0 := cur[len(cur)-1]
			p1, p2, p3 := pt(s.Args[0]), pt(s.Args[1]), pt(s.Args[2])
			n := int(math.Ceil((dist(p0, p1) + dist(p1, p2) + dist(p2, p3)) / step))
			n = max(2, min(n, 48))
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				a, b, c := lerpPt(p0, p1, t), lerpPt(p1, p2, t), lerpPt(p2, p3, t)
				d, e := lerpPt(a, b, t), lerpPt(b, c, t)
				cur = append(cur, lerpPt(d, e, t))
			}
		}
	}
	if len(cur) > 2 {
		contours = append(contours, cur)
	}
	return contours, len(contours) > 0
}

func dist(a, b stitchgen.Pt) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }
func lerpPt(a, b stitchgen.Pt, t float64) stitchgen.Pt {
	return stitchgen.Pt{X: a.X + (b.X-a.X)*t, Y: a.Y + (b.Y-a.Y)*t}
}

func segBounds(contours [][]stitchgen.Pt) pattern.Rect {
	r := pattern.EmptyRect()
	for _, c := range contours {
		for _, p := range c {
			r.Add(p.X, p.Y)
		}
	}
	return r
}

func (f *sysFont) Glyph(r rune, s Spec) ([]pattern.St, float64, bool) {
	if f.load() != nil {
		return nil, 0, false
	}
	style := s.Style
	if style == "" {
		style = StyleFill
	}
	density := s.Density
	if density <= 0 {
		density = 4
	}
	key := fmt.Sprintf("%c|%.1f|%s|%.2f|%.0f|%.1f", r, s.Height, style, density, s.FillAngle, s.OutlineWidth)
	f.mu.Lock()
	if g, ok := f.cache[key]; ok {
		f.mu.Unlock()
		return clone(g.sts), g.adv, true
	}
	f.mu.Unlock()

	var buf sfnt.Buffer
	contours, ok := f.outline(&buf, r)
	gi, _ := f.f.GlyphIndex(&buf, r)
	if gi == 0 {
		return nil, 0, false
	}
	k := s.Height / f.capH // font units -> 0.1 mm
	adv, err := f.f.GlyphAdvance(&buf, gi, fixed.I(int(f.upm)), font.HintingNone)
	if err != nil {
		return nil, 0, false
	}
	advance := float64(adv) / 64 * k
	var sts []pattern.St
	if ok {
		for i := range contours {
			for j := range contours[i] {
				contours[i][j].X *= k
				contours[i][j].Y *= k
			}
		}
		ow := s.OutlineWidth
		if ow <= 0 {
			ow = 10 // 1.0 mm satin border
		}
		angle := s.FillAngle
		if style == StyleFill || style == StyleFillOutline {
			fill := stitchgen.FillShapes(contours, stitchgen.FillOptions{Angle: angle, Spacing: density, StitchLen: 35, Underlay: true})
			if len(fill) > 0 && fill[0].Cmd == pattern.Stitch {
				sts = append(sts, pattern.St{X: fill[0].X, Y: fill[0].Y, Cmd: pattern.Jump})
			}
			sts = append(sts, fill...)
		}
		for _, c := range contours {
			closed := append(append([]stitchgen.Pt{}, c...), c[0])
			var part []pattern.St
			switch style {
			case StyleFillOutline, StyleSatin:
				part = stitchgen.Satin(closed, stitchgen.SatinOptions{Width: ow, Spacing: density, Underlay: true})
			case StyleRun:
				part = stitchgen.Run(closed, 20)
			}
			if len(part) > 0 {
				sts = append(sts, pattern.St{X: part[0].X, Y: part[0].Y, Cmd: pattern.Jump})
				sts = append(sts, part...)
			}
		}
	}
	f.mu.Lock()
	if len(f.cache) > 2000 {
		f.cache = map[string]sysGlyph{}
	}
	f.cache[key] = sysGlyph{sts: sts, adv: advance}
	f.mu.Unlock()
	return clone(sts), advance, true
}

func clone(s []pattern.St) []pattern.St { return append([]pattern.St(nil), s...) }

// Kern implements kerner (0.1 mm at native size; sysFont has no native size,
// so the value returned is already in 0.1 mm per unit height k=1 below).
func (f *sysFont) kernAt(prev, cur rune, height float64) float64 {
	if f.load() != nil {
		return 0
	}
	var buf sfnt.Buffer
	a, _ := f.f.GlyphIndex(&buf, prev)
	b, _ := f.f.GlyphIndex(&buf, cur)
	if a == 0 || b == 0 {
		return 0
	}
	kv, err := f.f.Kern(&buf, a, b, fixed.I(int(f.upm)), font.HintingNone)
	if err != nil {
		return 0
	}
	return -float64(kv) / 64 * height / f.capH
}

// ---- discovery ------------------------------------------------------------

// SystemFontDirs returns the folders that hold installed fonts.
func SystemFontDirs() []string {
	switch runtime.GOOS {
	case "windows":
		var d []string
		if w := os.Getenv("WINDIR"); w != "" {
			d = append(d, filepath.Join(w, "Fonts"))
		}
		if l := os.Getenv("LOCALAPPDATA"); l != "" {
			d = append(d, filepath.Join(l, "Microsoft", "Windows", "Fonts"))
		}
		return d
	case "darwin":
		h, _ := os.UserHomeDir()
		return []string{"/Library/Fonts", "/System/Library/Fonts", filepath.Join(h, "Library", "Fonts")}
	default:
		h, _ := os.UserHomeDir()
		return []string{"/usr/share/fonts", filepath.Join(h, ".fonts"), filepath.Join(h, ".local", "share", "fonts")}
	}
}

// LoadSystemFonts scans dirs for TrueType/OpenType fonts and registers them.
// Only names are read now; outlines load on first use.
func (r *Registry) LoadSystemFonts(dirs []string) int {
	n := 0
	var buf sfnt.Buffer
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if ext != ".ttf" && ext != ".otf" && ext != ".ttc" {
				return nil
			}
			if fi, err := d.Info(); err != nil || fi.Size() > 40<<20 {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			var fonts []*sfnt.Font
			if f, err := sfnt.Parse(data); err == nil {
				fonts = []*sfnt.Font{f}
			} else if c, err := sfnt.ParseCollection(data); err == nil {
				for i := 0; i < c.NumFonts(); i++ {
					if f, err := c.Font(i); err == nil {
						fonts = append(fonts, f)
					}
				}
			}
			for i, f := range fonts {
				fam, _ := f.Name(&buf, sfnt.NameIDTypographicFamily)
				if fam == "" {
					fam, _ = f.Name(&buf, sfnt.NameIDFamily)
				}
				sub, _ := f.Name(&buf, sfnt.NameIDTypographicSubfamily)
				if sub == "" {
					sub, _ = f.Name(&buf, sfnt.NameIDSubfamily)
				}
				if fam == "" {
					continue
				}
				// must have Latin capitals to be useful for lettering
				if gi, err := f.GlyphIndex(&buf, 'A'); err != nil || gi == 0 {
					continue
				}
				name := fam
				if sub != "" && !strings.EqualFold(sub, "Regular") {
					name += " " + sub
				}
				name = strings.TrimSpace(name)
				r.mu.Lock()
				key := strings.ToLower(name)
				if _, exists := r.fonts[key]; exists {
					if _, isSys := r.fonts[key].(*sysFont); isSys {
						r.mu.Unlock()
						continue // same font installed twice
					}
					name += " (Windows)"
					key = strings.ToLower(name)
				}
				r.fonts[key] = &sysFont{name: name, path: p, index: i}
				r.mu.Unlock()
				n++
			}
			return nil
		})
	}
	return n
}
