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

type packedFont struct {
	fsys fs.FS
	dir  string
	meta PackedFontFile

	once   sync.Once
	err    error
	glyphs map[rune][]pattern.St
}

func loadPackedFont(fsys fs.FS, dir string, raw []byte) (Font, error) {
	f := &packedFont{fsys: fsys, dir: dir}
	if err := json.Unmarshal(raw, &f.meta); err != nil {
		return nil, fmt.Errorf("font.json: %w", err)
	}
	if f.meta.Name == "" || f.meta.CapHeight <= 0 || len(f.meta.Glyphs) == 0 {
		return nil, errors.New("packed font is missing name, capHeight or glyphs")
	}
	return f, nil
}

// load decompresses glyph data on first use.
func (f *packedFont) load() error {
	f.once.Do(func() {
		fh, err := f.fsys.Open(path.Join(f.dir, "glyphs.bin.gz"))
		if err != nil {
			f.err = err
			return
		}
		defer fh.Close()
		zr, err := gzip.NewReader(fh)
		if err != nil {
			f.err = err
			return
		}
		data, err := io.ReadAll(io.LimitReader(zr, 64<<20))
		if err != nil {
			f.err = err
			return
		}
		f.glyphs = map[rune][]pattern.St{}
		for key, g := range f.meta.Glyphs {
			rs := []rune(key)
			if len(rs) != 1 || g.Off < 0 || g.Len <= 0 || g.Off+g.Len > len(data) {
				continue
			}
			sts, err := dst.DecodeRecords(data[g.Off : g.Off+g.Len])
			if err != nil {
				continue
			}
			var out []pattern.St
			for _, s := range pattern.CollapseJumps(sts, 3) {
				if s.Cmd != pattern.End {
					out = append(out, s)
				}
			}
			f.glyphs[rs[0]] = out
		}
	})
	return f.err
}

func (f *packedFont) Name() string    { return f.meta.Name }
func (f *packedFont) Native() float64 { return f.meta.CapHeight }

func (f *packedFont) lookup(r rune) (rune, bool) {
	if _, ok := f.meta.Glyphs[string(r)]; ok {
		return r, true
	}
	for _, alt := range []rune{unicode.ToUpper(r), unicode.ToLower(r)} {
		if _, ok := f.meta.Glyphs[string(alt)]; ok {
			return alt, true
		}
	}
	return r, false
}

func (f *packedFont) Glyph(r rune, s Spec) ([]pattern.St, float64, bool) {
	r, ok := f.lookup(r)
	if !ok || f.load() != nil {
		return nil, 0, false
	}
	g, ok := f.glyphs[r]
	if !ok {
		return nil, 0, false
	}
	k := s.Height / f.meta.CapHeight
	out := make([]pattern.St, len(g))
	for i, st := range g {
		out[i] = pattern.St{X: st.X * k, Y: st.Y * k, Cmd: st.Cmd}
	}
	return out, f.meta.Glyphs[string(r)].Adv * k, true
}

// Kern returns how far (0.1 mm, native size) to pull cur towards prev.
func (f *packedFont) Kern(prev, cur rune) float64 {
	if f.meta.Kerning == nil {
		return 0
	}
	if v, ok := f.meta.Kerning[string(prev)+" "+string(cur)]; ok {
		return v
	}
	return f.meta.Kerning[string(prev)+string(cur)]
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
