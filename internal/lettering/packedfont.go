package lettering

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"sync"
	"unicode"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/dst"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// PackedFormat identifies a packed font folder (font.json + glyphs.bin.gz).
// Packed fonts are produced by cmd/fontpack from Ink/Stitch font exports.
const PackedFormat = "tsgf-1"

// PackedFontFile is font.json for a packed font. Lengths are 0.1 mm at the
// font's native size; glyph stitches have their baseline at y=0 and their
// drawing origin at x=0.
type PackedFontFile struct {
	Format      string             `json:"format"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Keywords    []string           `json:"keywords,omitempty"`
	License     string             `json:"license"`
	Source      string             `json:"source"`
	CapHeight   float64            `json:"capHeight"`
	WordSpace   float64            `json:"wordSpace"`
	MinScale    float64            `json:"minScale"`
	MaxScale    float64            `json:"maxScale"`
	LetterCase  string             `json:"letterCase,omitempty"` // "upper", "lower" or ""
	Colors      []string           `json:"colors,omitempty"`     // thread colours by block
	Kerning     map[string]float64 `json:"kerning,omitempty"`    // "A V": amount to pull V left
	Glyphs      map[string]struct {
		Adv float64 `json:"adv"`
		Off int     `json:"o"` // byte offset into glyphs.bin
		Len int     `json:"n"`
	} `json:"glyphs"`
}

// packedMeta is the part of font.json kept in memory for every font: names,
// sizes and the glyph index. Kerning (the bulk of font.json, ~1.6 million
// pairs across the built-in fonts) is only read when the font is used.
type packedMeta struct {
	Format      string   `json:"format"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	License     string   `json:"license"`
	Source      string   `json:"source"`
	CapHeight   float64  `json:"capHeight"`
	WordSpace   float64  `json:"wordSpace"`
	MinScale    float64  `json:"minScale"`
	MaxScale    float64  `json:"maxScale"`
	LetterCase  string   `json:"letterCase,omitempty"`
	Colors      []string `json:"colors,omitempty"`
	Glyphs      map[string]struct {
		Adv float64 `json:"adv"`
		Off int     `json:"o"`
		Len int     `json:"n"`
	} `json:"glyphs"`
}

type glyphRef struct {
	adv      float32
	off, len int32
}

type packedFont struct {
	fsys fs.FS
	dir  string
	meta packedMeta // Glyphs is cleared after indexing; use index
	reg  *Registry

	index map[rune]glyphRef

	// loaded on first use, dropped again when the font falls out of the
	// registry's recently-used set (see Registry.touch)
	mu     sync.Mutex
	loaded bool
	err    error
	data   []byte                // decompressed DST records for every glyph
	kern   map[[2]rune]float32   // pull-left amounts, native 0.1 mm
	cache  map[rune][]pattern.St // glyphs decoded so far
}

func loadPackedFont(fsys fs.FS, dir string, raw []byte) (Font, error) {
	f := &packedFont{fsys: fsys, dir: dir}
	if err := json.Unmarshal(raw, &f.meta); err != nil {
		return nil, fmt.Errorf("font.json: %w", err)
	}
	if f.meta.Name == "" || f.meta.CapHeight <= 0 || len(f.meta.Glyphs) == 0 {
		return nil, errors.New("packed font is missing name, capHeight or glyphs")
	}
	f.index = make(map[rune]glyphRef, len(f.meta.Glyphs))
	for key, g := range f.meta.Glyphs {
		rs := []rune(key)
		if len(rs) != 1 || g.Off < 0 || g.Len <= 0 || g.Off > 1<<30 || g.Len > 1<<30 {
			continue
		}
		f.index[rs[0]] = glyphRef{float32(g.Adv), int32(g.Off), int32(g.Len)}
	}
	f.meta.Glyphs = nil
	return f, nil
}

// load decompresses the glyph data and reads the kerning table. Call with
// f.mu held.
func (f *packedFont) load() error {
	if f.loaded {
		return f.err
	}
	f.loaded = true
	fh, err := f.fsys.Open(path.Join(f.dir, "glyphs.bin.gz"))
	if err != nil {
		f.err = err
		return err
	}
	defer fh.Close()
	zr, err := gzip.NewReader(fh)
	if err != nil {
		f.err = err
		return err
	}
	f.data, err = io.ReadAll(io.LimitReader(zr, 64<<20))
	if err != nil {
		f.err = err
		return err
	}
	f.cache = map[rune][]pattern.St{}
	f.kern = nil
	if raw, err := fs.ReadFile(f.fsys, path.Join(f.dir, "font.json")); err == nil {
		var k struct {
			Kerning map[string]float64 `json:"kerning"`
		}
		if json.Unmarshal(raw, &k) == nil && len(k.Kerning) > 0 {
			f.kern = make(map[[2]rune]float32, len(k.Kerning))
			for key, v := range k.Kerning {
				if v == 0 {
					continue
				}
				rs := []rune(key)
				switch {
				case len(rs) == 3 && rs[1] == ' ':
					f.kern[[2]rune{rs[0], rs[2]}] = float32(v)
				case len(rs) == 2:
					if _, dup := f.kern[[2]rune{rs[0], rs[1]}]; !dup {
						f.kern[[2]rune{rs[0], rs[1]}] = float32(v)
					}
				}
			}
		}
	}
	return nil
}

// unload frees the glyph data and kerning (they reload on next use).
func (f *packedFont) unload() {
	f.mu.Lock()
	f.loaded, f.err, f.data, f.kern, f.cache = false, nil, nil, nil, nil
	f.mu.Unlock()
}

// glyph returns the decoded stitches for r (native size). Call with f.mu held.
func (f *packedFont) glyph(r rune) ([]pattern.St, bool) {
	if g, ok := f.cache[r]; ok {
		return g, true
	}
	ref, ok := f.index[r]
	if !ok {
		return nil, false
	}
	off, n := int(ref.off), int(ref.len)
	if off > len(f.data) || n > len(f.data)-off {
		return nil, false
	}
	sts, err := dst.DecodeRecords(f.data[off : off+n])
	if err != nil {
		return nil, false
	}
	var out []pattern.St
	for _, s := range pattern.CollapseJumps(sts, 3) {
		if s.Cmd != pattern.End {
			out = append(out, s)
		}
	}
	f.cache[r] = out
	return out, true
}

func (f *packedFont) Name() string    { return f.meta.Name }
func (f *packedFont) Native() float64 { return f.meta.CapHeight }

func (f *packedFont) lookup(r rune) (rune, bool) {
	if _, ok := f.index[r]; ok {
		return r, true
	}
	for _, alt := range []rune{unicode.ToUpper(r), unicode.ToLower(r)} {
		if _, ok := f.index[alt]; ok {
			return alt, true
		}
	}
	return r, false
}

func (f *packedFont) Glyph(r rune, s Spec) ([]pattern.St, float64, bool) {
	r, ok := f.lookup(r)
	if !ok {
		return nil, 0, false
	}
	f.mu.Lock()
	if f.load() != nil {
		f.mu.Unlock()
		return nil, 0, false
	}
	g, ok := f.glyph(r)
	k := s.Height / f.meta.CapHeight
	var out []pattern.St
	if ok {
		out = make([]pattern.St, len(g))
		for i, st := range g {
			out[i] = pattern.St{X: st.X * k, Y: st.Y * k, Cmd: st.Cmd}
		}
	}
	f.mu.Unlock()
	if f.reg != nil {
		f.reg.touch(f)
	}
	if !ok {
		return nil, 0, false
	}
	return out, float64(f.index[r].adv) * k, true
}

// Kern returns how far (0.1 mm, native size) to pull cur towards prev.
func (f *packedFont) Kern(prev, cur rune) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.load() != nil || f.kern == nil {
		return 0
	}
	return float64(f.kern[[2]rune{prev, cur}])
}

func (f *packedFont) WordSpace() float64 { return f.meta.WordSpace }
func (f *packedFont) ScaleRange() (float64, float64) {
	lo, hi := f.meta.MinScale, f.meta.MaxScale
	if lo <= 0 {
		lo = 0.8
	}
	if hi <= 0 {
		hi = 1.2
	}
	return lo, hi
}
func (f *packedFont) Case() string     { return f.meta.LetterCase }
func (f *packedFont) Colors() []string { return f.meta.Colors }
func (f *packedFont) Info() (string, string) {
	return f.meta.License, f.meta.Source
}

// PackGlyphs encodes glyph stitch lists (0.1 mm, baseline y=0) into the
// glyphs.bin.gz payload and fills the offsets in meta.
func PackGlyphs(meta *PackedFontFile, glyphs map[string][]pattern.St, advance map[string]float64) ([]byte, error) {
	var bin bytes.Buffer
	meta.Glyphs = map[string]struct {
		Adv float64 `json:"adv"`
		Off int     `json:"o"`
		Len int     `json:"n"`
	}{}
	keys := make([]string, 0, len(glyphs))
	for k := range glyphs {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		recs, err := dst.EncodeRecords(glyphs[k])
		if err != nil {
			return nil, fmt.Errorf("glyph %q: %w", k, err)
		}
		meta.Glyphs[k] = struct {
			Adv float64 `json:"adv"`
			Off int     `json:"o"`
			Len int     `json:"n"`
		}{advance[k], bin.Len(), len(recs)}
		bin.Write(recs)
	}
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if _, err := zw.Write(bin.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return gz.Bytes(), nil
}

// SampleText returns a few characters the font actually has (for previews of
// fonts that lack the requested text, e.g. Hebrew or monogram sets).
func (r *Registry) SampleText(name string) string {
	f, ok := r.Get(name)
	if !ok {
		return ""
	}
	pf, ok := f.(*packedFont)
	if !ok {
		return ""
	}
	keys := make([]string, 0, len(pf.index))
	for k := range pf.index {
		if k != ' ' {
			keys = append(keys, string(k))
		}
	}
	sortStrings(keys)
	if len(keys) > 6 {
		keys = keys[:6]
	}
	return strings.Join(keys, "")
}
