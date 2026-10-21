package lettering

import (
	"unicode"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/stitchgen"
)

// blockFont is a built-in stroke font: each glyph is a set of centre lines
// on a 0..100 cap grid (y down), rendered as satin columns. It is a starter
// font for names and numbers; digitized glyph libraries sew better.
type blockFont struct{}

func (blockFont) Name() string    { return "Block" }
func (blockFont) Native() float64 { return 0 }

type stroke []stitchgen.Pt

func p(x, y float64) stitchgen.Pt { return stitchgen.Pt{X: x, Y: y} }

var oShape = stroke{p(14, 0), p(46, 0), p(60, 14), p(60, 86), p(46, 100), p(14, 100), p(0, 86), p(0, 14), p(14, 0)}

var blockGlyphs = map[rune][]stroke{
	'A': {{p(0, 100), p(30, 0), p(60, 100)}, {p(12, 64), p(48, 64)}},
	'B': {{p(0, 100), p(0, 0), p(38, 0), p(52, 12), p(52, 36), p(40, 48), p(0, 48)}, {p(40, 48), p(56, 60), p(56, 88), p(44, 100), p(0, 100)}},
	'C': {{p(58, 14), p(46, 0), p(14, 0), p(0, 14), p(0, 86), p(14, 100), p(46, 100), p(58, 86)}},
	'D': {{p(0, 0), p(0, 100), p(36, 100), p(56, 80), p(56, 20), p(36, 0), p(0, 0)}},
	'E': {{p(52, 0), p(0, 0), p(0, 100), p(52, 100)}, {p(0, 50), p(40, 50)}},
	'F': {{p(52, 0), p(0, 0), p(0, 100)}, {p(0, 50), p(40, 50)}},
	'G': {{p(58, 14), p(46, 0), p(14, 0), p(0, 14), p(0, 86), p(14, 100), p(46, 100), p(58, 86), p(58, 56), p(34, 56)}},
	'H': {{p(0, 0), p(0, 100)}, {p(56, 0), p(56, 100)}, {p(0, 50), p(56, 50)}},
	'I': {{p(0, 0), p(0, 100)}},
	'J': {{p(46, 0), p(46, 86), p(32, 100), p(12, 100), p(0, 86), p(0, 72)}},
	'K': {{p(0, 0), p(0, 100)}, {p(54, 0), p(0, 60)}, {p(18, 40), p(56, 100)}},
	'L': {{p(0, 0), p(0, 100), p(50, 100)}},
	'M': {{p(0, 100), p(0, 0), p(34, 62), p(68, 0), p(68, 100)}},
	'N': {{p(0, 100), p(0, 0), p(56, 100), p(56, 0)}},
	'O': {oShape},
	'P': {{p(0, 100), p(0, 0), p(40, 0), p(54, 14), p(54, 40), p(40, 54), p(0, 54)}},
	'Q': {oShape, {p(36, 74), p(62, 104)}},
	'R': {{p(0, 100), p(0, 0), p(40, 0), p(54, 14), p(54, 38), p(40, 52), p(0, 52)}, {p(28, 52), p(56, 100)}},
	'S': {{p(56, 14), p(44, 0), p(14, 0), p(0, 14), p(0, 36), p(14, 48), p(42, 52), p(56, 64), p(56, 86), p(42, 100), p(12, 100), p(0, 86)}},
	'T': {{p(0, 0), p(60, 0)}, {p(30, 0), p(30, 100)}},
	'U': {{p(0, 0), p(0, 86), p(14, 100), p(44, 100), p(58, 86), p(58, 0)}},
	'V': {{p(0, 0), p(30, 100), p(60, 0)}},
	'W': {{p(0, 0), p(18, 100), p(38, 36), p(58, 100), p(76, 0)}},
	'X': {{p(0, 0), p(56, 100)}, {p(56, 0), p(0, 100)}},
	'Y': {{p(0, 0), p(30, 50), p(60, 0)}, {p(30, 50), p(30, 100)}},
	'Z': {{p(0, 0), p(56, 0), p(0, 100), p(56, 100)}},
	'0': {{p(14, 0), p(40, 0), p(54, 14), p(54, 86), p(40, 100), p(14, 100), p(0, 86), p(0, 14), p(14, 0)}},
	'1': {{p(0, 18), p(20, 0), p(20, 100)}},
	'2': {{p(0, 14), p(14, 0), p(42, 0), p(56, 14), p(56, 36), p(0, 100), p(58, 100)}},
	'3': {{p(0, 12), p(12, 0), p(44, 0), p(56, 12), p(56, 36), p(44, 48), p(20, 48)}, {p(44, 48), p(58, 60), p(58, 88), p(46, 100), p(12, 100), p(0, 88)}},
	'4': {{p(44, 100), p(44, 0), p(0, 70), p(60, 70)}},
	'5': {{p(54, 0), p(4, 0), p(0, 46), p(40, 42), p(56, 56), p(56, 86), p(42, 100), p(12, 100), p(0, 88)}},
	'6': {{p(52, 8), p(40, 0), p(16, 0), p(0, 16), p(0, 86), p(14, 100), p(42, 100), p(56, 86), p(56, 60), p(42, 46), p(14, 46), p(0, 56)}},
	'7': {{p(0, 0), p(58, 0), p(18, 100)}},
	'8': {
		{p(28, 46), p(46, 40), p(52, 22), p(46, 6), p(28, 0), p(10, 6), p(4, 22), p(10, 40), p(28, 46)},
		{p(28, 46), p(50, 54), p(58, 76), p(50, 96), p(28, 100), p(6, 96), p(0, 76), p(6, 54), p(28, 46)},
	},
	'9':  {{p(4, 92), p(16, 100), p(40, 100), p(56, 84), p(56, 14), p(42, 0), p(14, 0), p(0, 14), p(0, 40), p(14, 54), p(42, 54), p(56, 44)}},
	'-':  {{p(0, 56), p(36, 56)}},
	'.':  {{p(0, 94), p(0, 100)}},
	',':  {{p(4, 92), p(0, 112)}},
	'\'': {{p(0, 0), p(0, 26)}},
	'!':  {{p(0, 0), p(0, 68)}, {p(0, 92), p(0, 100)}},
	'#':  {{p(18, 0), p(10, 100)}, {p(46, 0), p(38, 100)}, {p(0, 32), p(56, 32)}, {p(0, 68), p(56, 68)}},
	'&':  {{p(60, 100), p(10, 40), p(10, 12), p(22, 0), p(38, 0), p(48, 12), p(48, 26), p(0, 66), p(0, 88), p(14, 100), p(34, 100), p(60, 66)}},
	'/':  {{p(40, 0), p(0, 100)}},
	'+':  {{p(0, 56), p(48, 56)}, {p(24, 32), p(24, 80)}},
}

func (blockFont) Glyph(r rune, s Spec) ([]pattern.St, float64, bool) {
	strokes, ok := blockGlyphs[unicode.ToUpper(r)]
	if !ok {
		return nil, 0, false
	}
	weight := s.Weight
	if weight <= 0 {
		weight = 17
	}
	w := weight // stroke width in grid units (grid is 100 high)
	k := s.Height / (100 + w)
	widthMM := w * k
	if widthMM > 90 { // satin over 9 mm gets loose; cap it
		w = 90 / k
		widthMM = 90
	}
	maxX := 0.0
	var out []pattern.St
	for _, st := range strokes {
		center := make([]stitchgen.Pt, len(st))
		for i, q := range st {
			// grid -> 0.1 mm, inset so outer edges sit on the cap box
			center[i] = stitchgen.Pt{X: (q.X + w/2) * k, Y: (q.Y + w/2 - 100 - w) * k}
			if q.X > maxX {
				maxX = q.X
			}
		}
		sat := stitchgen.Satin(center, stitchgen.SatinOptions{
			Width:    widthMM,
			Spacing:  s.Density,
			PullComp: 2,
			Underlay: true,
		})
		if len(sat) == 0 {
			continue
		}
		out = append(out, pattern.St{X: sat[0].X, Y: sat[0].Y, Cmd: pattern.Jump})
		out = append(out, sat...)
	}
	advance := (maxX + w + 10) * k
	return out, advance, true
}
