// Package preview renders stitch blocks to small PNG thumbnails (used by the
// font gallery). Lines are anti-aliased; stitches look like thin thread.
package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// ParseHex parses #rrggbb (falls back to navy).
func ParseHex(s string) color.RGBA {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.RGBA{28, 47, 94, 255}
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{28, 47, 94, 255}
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}

// PNG draws blocks (each in its colour) fitted into w×h with a margin.
func PNG(blocks [][]pattern.St, colors []color.RGBA, w, h int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	b := pattern.EmptyRect()
	for _, bl := range blocks {
		b.Union(pattern.Bounds(bl))
	}
	if !b.Empty() {
		margin := 6.0
		s := math.Min((float64(w)-2*margin)/math.Max(b.Width(), 1), (float64(h)-2*margin)/math.Max(b.Height(), 1))
		ox := float64(w)/2 - b.CenterX()*s
		oy := float64(h)/2 - b.CenterY()*s
		for bi, bl := range blocks {
			c := color.RGBA{28, 47, 94, 255}
			if bi < len(colors) {
				c = colors[bi]
			}
			// very light threads get a grey outline so they show on white
			lum := 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
			draw := c
			if lum > 215 {
				draw = color.RGBA{170, 174, 182, 255}
			}
			var px, py float64
			have := false
			for _, st := range bl {
				switch st.Cmd {
				case pattern.Jump:
					px, py, have = st.X*s+ox, st.Y*s+oy, true
				case pattern.Stitch:
					x, y := st.X*s+ox, st.Y*s+oy
					if have {
						wuLine(img, px, py, x, y, draw)
					}
					px, py, have = x, y, true
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func blend(img *image.RGBA, x, y int, c color.RGBA, a float64) {
	if !(image.Point{x, y}.In(img.Rect)) || a <= 0 {
		return
	}
	a = math.Min(1, a*0.9)
	i := img.PixOffset(x, y)
	p := img.Pix[i : i+3 : i+3]
	p[0] = uint8(float64(p[0])*(1-a) + float64(c.R)*a)
	p[1] = uint8(float64(p[1])*(1-a) + float64(c.G)*a)
	p[2] = uint8(float64(p[2])*(1-a) + float64(c.B)*a)
}

// wuLine draws an anti-aliased line (Xiaolin Wu).
func wuLine(img *image.RGBA, x0, y0, x1, y1 float64, c color.RGBA) {
	steep := math.Abs(y1-y0) > math.Abs(x1-x0)
	if steep {
		x0, y0, x1, y1 = y0, x0, y1, x1
	}
	if x0 > x1 {
		x0, x1, y0, y1 = x1, x0, y1, y0
	}
	dx, dy := x1-x0, y1-y0
	g := 1.0
	if dx != 0 {
		g = dy / dx
	}
	plot := func(x, y int, a float64) {
		if steep {
			blend(img, y, x, c, a)
		} else {
			blend(img, x, y, c, a)
		}
	}
	y := y0 + g*(math.Round(x0)-x0)
	for x := int(math.Round(x0)); x <= int(math.Round(x1)); x++ {
		fl := math.Floor(y)
		f := y - fl
		plot(x, int(fl), 1-f)
		plot(x, int(fl)+1, f)
		y += g
	}
}
