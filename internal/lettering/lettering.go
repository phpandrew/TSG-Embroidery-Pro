// Package lettering lays out text as stitches.
//
// Two font sources are supported:
//   - the built-in "Block" stroke font (satin columns generated on the fly), and
//   - glyph libraries: a folder of pre-digitized DST letters plus font.json
//     (the recommended route for production-quality lettering).
package lettering

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/density"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/dst"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/stitchgen"
)

// Spec describes a piece of lettering.
type Spec struct {
	Text         string  `json:"text"`
	Font         string  `json:"font"`
	Height       float64 `json:"height"`                 // cap height, 0.1 mm
	Spacing      float64 `json:"spacing"`                // extra letter spacing, 0.1 mm
	Weight       float64 `json:"weight"`                 // stroke width as % of height (built-in font), default 17
	Density      float64 `json:"density"`                // satin spacing, 0.1 mm, default 4
	Style        string  `json:"style,omitempty"`        // installed fonts: fill | fill+outline | satin | run
	FillAngle    float64 `json:"fillAngle,omitempty"`    // installed fonts: fill direction, degrees
	OutlineWidth float64 `json:"outlineWidth,omitempty"` // installed fonts: satin border width, 0.1 mm
	DensityPct   float64 `json:"densityPct"`             // digitized fonts: % of native density (default 100)
	TrimAbove    float64 `json:"trimAbove"`              // trim jumps longer than this, 0.1 mm, default 40
}

// Font produces glyph stitches at a requested height.
type Font interface {
	Name() string
	// Glyph returns stitches for r with its left edge at x=0 and baseline at
	// y=0 (cap top at y=-height), plus the advance width.
	Glyph(r rune, s Spec) ([]pattern.St, float64, bool)
	// Native returns the digitized cap height (0 = scalable).
	Native() float64
}

// Registry holds the available fonts by name.
type Registry struct {
	mu    sync.RWMutex
	fonts map[string]Font
}

func NewRegistry() *Registry {
	r := &Registry{fonts: map[string]Font{}}
	b := blockFont{}
	r.fonts[strings.ToLower(b.Name())] = b
	return r
}

// LoadDir loads every glyph-library subfolder of dir (folders containing
// font.json). Missing dir is not an error.
func (r *Registry) LoadDir(dir string) []error {
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	return r.LoadFS(os.DirFS(dir))
}

// LoadFS loads every glyph-library subfolder at the root of fsys (used for
// fonts compiled into the program as well as on-disk font folders).
func (r *Registry) LoadFS(fsys fs.FS) []error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return []error{err}
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := fs.Stat(fsys, path.Join(e.Name(), "font.json")); err != nil {
			continue
		}
		var f Font
		raw, rerr := fs.ReadFile(fsys, path.Join(e.Name(), "font.json"))
		if rerr == nil && bytes.Contains(raw, []byte(`"`+PackedFormat+`"`)) {
			f, err = loadPackedFont(fsys, e.Name(), raw)
		} else {
			f, err = LoadGlyphFontFS(fsys, e.Name())
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		r.mu.Lock()
		r.fonts[strings.ToLower(f.Name())] = f
		r.mu.Unlock()
	}
	return errs
}

// FontInfo is a font listing entry.
type FontInfo struct {
	Name        string   `json:"name"`
	Native      float64  `json:"native"` // digitized cap height in 0.1 mm, 0 = scalable
	MinHeight   float64  `json:"minHeight,omitempty"`
	MaxHeight   float64  `json:"maxHeight,omitempty"`
	Description string   `json:"description,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	License     string   `json:"license,omitempty"`
	Source      string   `json:"source,omitempty"`
	Colors      []string `json:"colors,omitempty"`
	Kind        string   `json:"kind"` // built-in | digitized
}

func (r *Registry) List() []FontInfo {
	out := []FontInfo{}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, f := range r.fonts {
		fi := FontInfo{Name: f.Name(), Native: f.Native(), Kind: "digitized"}
		if _, ok := f.(blockFont); ok {
			fi.Kind = "built-in"
			fi.Description = "Built-in satin block letters, any size from about 8 mm"
		}
		if sr, ok := f.(scaleRange); ok && fi.Native > 0 {
			lo, hi := sr.ScaleRange()
			fi.MinHeight, fi.MaxHeight = fi.Native*lo, fi.Native*hi
		}
		if _, ok := f.(*sysFont); ok {
			fi.Kind = "windows"
			fi.Description = "Installed font, auto-digitized (fill by default)"
		}
		if pf, ok := f.(*packedFont); ok {
			fi.Description = pf.meta.Description
			fi.Keywords = pf.meta.Keywords
			fi.License = pf.meta.License
			fi.Source = pf.meta.Source
			if len(pf.meta.Colors) > 1 {
				fi.Colors = pf.meta.Colors
			}
		}
		out = append(out, fi)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (r *Registry) Get(name string) (Font, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.fonts[strings.ToLower(name)]
	return f, ok
}

// Optional font capabilities.
type (
	kerner     interface{ Kern(prev, cur rune) float64 }
	wordSpacer interface{ WordSpace() float64 }
	scaleRange interface{ ScaleRange() (float64, float64) }
	caser      interface{ Case() string }
	colorer    interface{ Colors() []string }
)

// Result is laid-out text, centred on 0,0.
type Result struct {
	// Blocks holds one stitch list per thread colour (multi-colour fonts
	// sew colour 1 for every letter, then colour 2, ...).
	Blocks [][]pattern.St
	Colors []string // suggested thread colours per block (may be empty)
	// Stitches is all blocks joined by ColorChange records.
	Stitches []pattern.St
	Missing  []string // characters the font has no glyph for
	Warning  string
}

// Layout renders s with the named font.
func (r *Registry) Layout(s Spec) (Result, error) {
	f, ok := r.Get(s.Font)
	if !ok {
		f = blockFont{}
	}
	if s.Height <= 0 {
		s.Height = 180
		if n := f.Native(); n > 0 {
			s.Height = n
		}
	}
	if s.TrimAbove <= 0 {
		s.TrimAbove = 40
	}
	text := strings.TrimSpace(s.Text)
	if text == "" {
		return Result{}, errors.New("lettering: text is empty")
	}
	if c, ok := f.(caser); ok {
		switch c.Case() {
		case "upper":
			text = strings.ToUpper(text)
		case "lower":
			text = strings.ToLower(text)
		}
	}
	var res Result
	k := 1.0
	if n := f.Native(); n > 0 {
		k = s.Height / n
		lo, hi := 0.8, 1.2
		if sr, ok := f.(scaleRange); ok {
			lo, hi = sr.ScaleRange()
		}
		if k < lo-1e-9 || k > hi+1e-9 {
			res.Warning = fmt.Sprintf("%s is designed for %.0f–%.0f mm; %.1f mm may sew poorly", f.Name(), n*lo/10, n*hi/10, s.Height/10)
		}
	}
	if s.Height < 50 {
		res.Warning = "Lettering under 5 mm usually sews poorly"
	}
	if c, ok := f.(colorer); ok {
		res.Colors = c.Colors()
	}
	var blocks [][]pattern.St
	penX := 0.0
	var prev rune
	missing := map[rune]bool{}
	for _, ch := range text {
		if ch == ' ' {
			if ws, ok := f.(wordSpacer); ok && ws.WordSpace() > 0 {
				penX += ws.WordSpace()*k + s.Spacing
			} else {
				penX += s.Height*0.35 + s.Spacing
			}
			prev = 0
			continue
		}
		g, adv, ok := f.Glyph(ch, s)
		if !ok {
			missing[ch] = true
			penX += s.Height * 0.3
			prev = 0
			continue
		}
		if kn, ok := f.(kerner); ok && prev != 0 {
			penX -= kn.Kern(prev, ch) * k
		} else if sf, ok := f.(*sysFont); ok && prev != 0 {
			penX -= sf.kernAt(prev, ch, s.Height)
		}
		// split the glyph by colour; colour i of every letter goes to block i
		ci := 0
		var seg []pattern.St
		flush := func() {
			if pattern.Count(seg).Stitches == 0 {
				seg = nil
				return
			}
			for len(blocks) <= ci {
				blocks = append(blocks, nil)
			}
			if seg[0].Cmd == pattern.Stitch {
				seg = append([]pattern.St{{X: seg[0].X, Y: seg[0].Y, Cmd: pattern.Jump}}, seg...)
			}
			blocks[ci] = append(blocks[ci], seg...)
			seg = nil
		}
		for _, st := range g {
			if st.Cmd == pattern.ColorChange || st.Cmd == pattern.Stop {
				flush()
				ci++
				continue
			}
			if st.Cmd == pattern.Stitch || st.Cmd == pattern.Jump {
				st.X += penX
			}
			seg = append(seg, st)
		}
		flush()
		penX += adv + s.Spacing
		prev = ch
	}
	for ch := range missing {
		res.Missing = append(res.Missing, string(ch))
	}
	sort.Strings(res.Missing)
	// Digitized fonts are scaled, not regenerated: stitches spread out when
	// enlarged and bunch up when reduced. Rebuild satins/fills so density
	// matches the font's native density (times the user's density %).
	if _, isBlock := f.(blockFont); !isBlock && f.Native() > 0 {
		factor := k
		if s.DensityPct > 0 {
			factor *= s.DensityPct / 100
		}
		if math.Abs(factor-1) > 0.03 {
			for i := range blocks {
				blocks[i], _ = density.Adjust(blocks[i], density.Options{Factor: factor})
			}
		}
	}
	all := pattern.EmptyRect()
	for i := range blocks {
		blocks[i] = connect(blocks[i], s.TrimAbove)
		all.Union(pattern.Bounds(blocks[i]))
	}
	var nonEmpty [][]pattern.St
	for _, b := range blocks {
		if pattern.Count(b).Stitches > 0 {
			nonEmpty = append(nonEmpty, b)
		}
	}
	if len(nonEmpty) == 0 {
		return res, errors.New("lettering: no printable glyphs")
	}
	cx, cy := all.CenterX(), all.CenterY()
	for bi, b := range nonEmpty {
		for i := range b {
			if b[i].Cmd == pattern.Stitch || b[i].Cmd == pattern.Jump {
				b[i].X -= cx
				b[i].Y -= cy
			}
		}
		if bi > 0 {
			res.Stitches = append(res.Stitches, pattern.St{Cmd: pattern.ColorChange})
		}
		res.Stitches = append(res.Stitches, b...)
	}
	res.Blocks = nonEmpty
	return res, nil
}

func sortStrings(s []string) { sort.Strings(s) }

// connect inserts trims (with tie-off / tie-in) on jumps longer than trimAbove.
func connect(sts []pattern.St, trimAbove float64) []pattern.St {
	out := make([]pattern.St, 0, len(sts)+64)
	var last *pattern.St
	var lastDir stitchgen.Pt
	for i := 0; i < len(sts); i++ {
		s := sts[i]
		if s.Cmd == pattern.Jump && last != nil {
			// find the next needle point to aim the tie-in
			if math.Hypot(s.X-last.X, s.Y-last.Y) > trimAbove {
				out = append(out, stitchgen.Lock(stitchgen.Pt{X: last.X, Y: last.Y}, lastDir)...)
				out = append(out, pattern.St{Cmd: pattern.Trim})
				out = append(out, s)
				var dir stitchgen.Pt
				for j := i + 1; j < len(sts); j++ {
					if sts[j].Cmd == pattern.Stitch && (sts[j].X != s.X || sts[j].Y != s.Y) {
						dir = stitchgen.Pt{X: sts[j].X - s.X, Y: sts[j].Y - s.Y}
						break
					}
				}
				out = append(out, stitchgen.Lock(stitchgen.Pt{X: s.X, Y: s.Y}, dir)...)
			} else {
				out = append(out, s)
			}
			continue
		}
		if s.Cmd == pattern.Stitch {
			if last != nil {
				lastDir = stitchgen.Pt{X: last.X - s.X, Y: last.Y - s.Y}
			}
		}
		out = append(out, s)
		if s.Cmd == pattern.Stitch || s.Cmd == pattern.Jump {
			ss := s
			last = &ss
		}
	}
	if len(out) > 0 && last != nil {
		out = append(out, stitchgen.Lock(stitchgen.Pt{X: last.X, Y: last.Y}, lastDir)...)
	}
	// first record should be a jump to the first needle point
	if len(out) > 0 && out[0].Cmd == pattern.Stitch {
		out = append([]pattern.St{{X: out[0].X, Y: out[0].Y, Cmd: pattern.Jump}}, out...)
	}
	return out
}

// ---- glyph library fonts -------------------------------------------------

// GlyphFontFile is the font.json schema.
//
//	{
//	  "name": "Varsity",
//	  "capHeight": 250,            // digitized cap height, 0.1 mm
//	  "glyphs": {
//	    "A": { "file": "A.dst", "advance": 230 },
//	    "Q": { "file": "Q.dst", "advance": 250, "descender": true }
//	  }
//	}
type GlyphFontFile struct {
	Name      string  `json:"name"`
	CapHeight float64 `json:"capHeight"`
	Glyphs    map[string]struct {
		File      string  `json:"file"`
		Advance   float64 `json:"advance"`
		Descender bool    `json:"descender"`
	} `json:"glyphs"`
}

type glyph struct {
	sts     []pattern.St
	advance float64
}

type glyphFont struct {
	name   string
	cap    float64
	glyphs map[rune]glyph
}

// LoadGlyphFont reads a glyph-library folder.
func LoadGlyphFont(dir string) (Font, error) {
	f, err := LoadGlyphFontFS(os.DirFS(dir), ".")
	if err == nil {
		if gf, ok := f.(*glyphFont); ok && gf.name == "." {
			gf.name = filepath.Base(dir)
		}
	}
	return f, err
}

// LoadGlyphFontFS reads a glyph-library folder from any file system.
func LoadGlyphFontFS(fsys fs.FS, dir string) (Font, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, "font.json"))
	if err != nil {
		return nil, err
	}
	var ff GlyphFontFile
	if err := json.Unmarshal(raw, &ff); err != nil {
		return nil, fmt.Errorf("font.json: %w", err)
	}
	if ff.Name == "" {
		ff.Name = path.Base(dir)
	}
	if ff.CapHeight <= 0 {
		return nil, errors.New("font.json: capHeight must be > 0")
	}
	f := &glyphFont{name: ff.Name, cap: ff.CapHeight, glyphs: map[rune]glyph{}}
	for key, g := range ff.Glyphs {
		rs := []rune(key)
		if len(rs) != 1 {
			continue
		}
		// confine glyph files to the font folder
		if !filepath.IsLocal(g.File) || !fs.ValidPath(filepath.ToSlash(g.File)) {
			return nil, fmt.Errorf("glyph %q: file must be inside the font folder", key)
		}
		fh, err := fsys.Open(path.Join(dir, filepath.ToSlash(g.File)))
		if err != nil {
			return nil, fmt.Errorf("glyph %q: %w", key, err)
		}
		pat, _, err := dst.Read(fh)
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("glyph %q: %w", key, err)
		}
		var sts []pattern.St
		for _, s := range pattern.CollapseJumps(pat.Stitches, 3) {
			if s.Cmd == pattern.Stitch || s.Cmd == pattern.Jump || s.Cmd == pattern.Trim {
				sts = append(sts, s)
			}
		}
		b := pattern.Bounds(sts)
		dy := -b.MaxY // bottom on baseline
		if g.Descender {
			dy = -ff.CapHeight - b.MinY // top on cap line
		}
		for i := range sts {
			sts[i].X -= b.MinX
			sts[i].Y += dy
		}
		adv := g.Advance
		if adv <= 0 {
			adv = b.Width() + ff.CapHeight*0.08
		}
		f.glyphs[rs[0]] = glyph{sts: sts, advance: adv}
	}
	if len(f.glyphs) == 0 {
		return nil, errors.New("font has no glyphs")
	}
	return f, nil
}

func (f *glyphFont) Name() string    { return f.name }
func (f *glyphFont) Native() float64 { return f.cap }
func (f *glyphFont) Glyph(r rune, s Spec) ([]pattern.St, float64, bool) {
	g, ok := f.glyphs[r]
	if !ok {
		g, ok = f.glyphs[unicode.ToUpper(r)]
	}
	if !ok {
		return nil, 0, false
	}
	k := s.Height / f.cap
	out := make([]pattern.St, len(g.sts))
	for i, st := range g.sts {
		out[i] = pattern.St{X: st.X * k, Y: st.Y * k, Cmd: st.Cmd}
	}
	return out, g.advance * k, true
}
