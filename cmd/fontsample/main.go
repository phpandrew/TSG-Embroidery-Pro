// Command fontsample renders a text sample for every font in a fonts
// directory to PNG (quick visual check after packing).
//
//	go run ./cmd/fontsample -fonts fonts -out /tmp/samples -text "Andrew 23"
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

func main() {
	fontsDir := flag.String("fonts", "fonts", "fonts directory")
	out := flag.String("out", "samples-out", "output directory")
	text := flag.String("text", "Andrew 23", "sample text")
	height := flag.Float64("height", 0, "cap height in mm (0 = font's native size)")
	system := flag.String("system", "", "only installed fonts whose name contains this (e.g. \"Bold\")")
	style := flag.String("style", "", "installed fonts: fill | fill+outline | satin | run")
	flag.Parse()
	r := lettering.NewRegistry()
	if *system != "" {
		r = lettering.NewRegistry()
		r.LoadSystemFonts(lettering.SystemFontDirs())
	}
	if *system == "" {
		for _, err := range r.LoadFS(os.DirFS(*fontsDir)) {
			log.Print(err)
		}
	}
	_ = os.MkdirAll(*out, 0o755)
	for _, fi := range r.List() {
		if *system != "" && (fi.Kind != "windows" || !strings.Contains(fi.Name, *system)) {
			continue
		}
		h := *height * 10
		if h == 0 {
			h = fi.Native
		}
		res, err := r.Layout(lettering.Spec{Text: *text, Font: fi.Name, Height: h, Style: *style})
		if err != nil {
			log.Printf("%s: %v", fi.Name, err)
			continue
		}
		name := strings.Map(func(r rune) rune {
			if r == ' ' || r == '/' {
				return '_'
			}
			return r
		}, fi.Name)
		if err := writePNG(filepath.Join(*out, name+".png"), res.Blocks); err != nil {
			log.Fatal(err)
		}
		c := pattern.Count(res.Stitches)
		log.Printf("%-30s %6d stitches %3d trims %d colours %s", fi.Name, c.Stitches, c.Trims, len(res.Blocks), res.Warning)
	}
}

func writePNG(path string, blocks [][]pattern.St) error {
	all := pattern.EmptyRect()
	for _, b := range blocks {
		all.Union(pattern.Bounds(b))
	}
	scale := math.Min(1, 1400/math.Max(all.Width(), 1))
	W, H := int(all.Width()*scale)+20, int(all.Height()*scale)+20
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	pal := []color.RGBA{{25, 45, 95, 255}, {210, 140, 10, 255}, {180, 30, 40, 255}, {30, 130, 60, 255}}
	for bi, b := range blocks {
		c := pal[bi%len(pal)]
		var px, py float64
		have := false
		for _, s := range b {
			switch s.Cmd {
			case pattern.Jump:
				px, py, have = s.X, s.Y, true
			case pattern.Stitch:
				if have {
					x0, y0 := (px-all.MinX)*scale+10, (py-all.MinY)*scale+10
					x1, y1 := (s.X-all.MinX)*scale+10, (s.Y-all.MinY)*scale+10
					n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
					for k := 0; k <= n; k++ {
						t := float64(k) / float64(n)
						img.Set(int(x0+(x1-x0)*t), int(y0+(y1-y0)*t), c)
					}
				}
				px, py, have = s.X, s.Y, true
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
