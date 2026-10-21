// Command samplegen writes sample DST files used for demos and tests, plus
// optional PNG previews (-png) for eyeballing generator output.
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

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/dst"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	sg "github.com/phpandrew/TSG-Embroidery-Pro/internal/stitchgen"
)

func pt(x, y float64) sg.Pt { return sg.Pt{X: x, Y: y} }

func closed(p []sg.Pt) []sg.Pt { return append(append([]sg.Pt{}, p...), p[0]) }

func jumpTo(sts []pattern.St) []pattern.St {
	if len(sts) == 0 {
		return sts
	}
	return append([]pattern.St{{X: sts[0].X, Y: sts[0].Y, Cmd: pattern.Jump}}, sts...)
}

func crest() []pattern.St {
	shield := []sg.Pt{pt(-440, -360), pt(0, -470), pt(440, -360), pt(435, 60), pt(360, 250), pt(200, 400), pt(0, 490), pt(-200, 400), pt(-360, 250), pt(-435, 60)}
	var navy, gold, white []pattern.St

	navy = append(navy, jumpTo(sg.Fill(sg.Offset(shield, -60), sg.FillOptions{Angle: 30, Underlay: true}))...)

	mountain := []sg.Pt{pt(-330, 230), pt(-170, 20), pt(-110, 70), pt(0, -110), pt(120, 60), pt(180, 10), pt(330, 230), pt(120, 170), pt(40, 300), pt(10, 120), pt(-150, 200)}
	white = append(white, jumpTo(sg.Fill(mountain, sg.FillOptions{Angle: 135, Underlay: true}))...)

	ring := closed(sg.Offset(shield, -45))
	gold = append(gold, jumpTo(sg.Satin(ring, sg.SatinOptions{Width: 30, Underlay: true}))...)
	var star []sg.Pt
	for i := 0; i < 16; i++ {
		r := 120.0
		if i%2 == 1 {
			r = 28
		}
		if i%4 == 2 {
			r = 70
		}
		a := float64(i)*math.Pi/8 - math.Pi/2
		star = append(star, pt(r*math.Cos(a), -230+r*math.Sin(a)))
	}
	gold = append(gold, pattern.St{Cmd: pattern.Trim})
	gold = append(gold, jumpTo(sg.Fill(star, sg.FillOptions{Angle: 0}))...)

	navy = append(navy, pattern.St{Cmd: pattern.Trim})
	navy = append(navy, jumpTo(sg.Satin(closed(sg.Offset(shield, 0)), sg.SatinOptions{Width: 45, Underlay: true}))...)

	var out []pattern.St
	out = append(out, navy...)
	out = append(out, pattern.St{Cmd: pattern.ColorChange})
	out = append(out, gold...)
	out = append(out, pattern.St{Cmd: pattern.ColorChange})
	out = append(out, white...)
	return out
}

func writeDST(path string, sts []pattern.St, label string) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := dst.Write(f, sts, dst.WriteOptions{Label: label}); err != nil {
		log.Fatal(err)
	}
}

func preview(path string, sts []pattern.St) {
	b := pattern.Bounds(sts)
	scale := 1.0
	W, H := int(b.Width()*scale)+40, int(b.Height()*scale)+40
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	palette := []color.RGBA{{20, 40, 90, 255}, {220, 160, 20, 255}, {150, 150, 150, 255}, {200, 40, 40, 255}}
	ci := 0
	var px, py float64
	have := false
	for _, s := range sts {
		switch s.Cmd {
		case pattern.ColorChange:
			ci = (ci + 1) % len(palette)
		case pattern.Jump:
			px, py, have = s.X, s.Y, true
		case pattern.Stitch:
			if have {
				x0, y0 := (px-b.MinX)*scale+20, (py-b.MinY)*scale+20
				x1, y1 := (s.X-b.MinX)*scale+20, (s.Y-b.MinY)*scale+20
				n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
				for k := 0; k <= n; k++ {
					t := float64(k) / float64(n)
					img.Set(int(x0+(x1-x0)*t), int(y0+(y1-y0)*t), palette[ci])
				}
			}
			px, py, have = s.X, s.Y, true
		}
	}
	f, _ := os.Create(path)
	defer f.Close()
	_ = png.Encode(f, img)
}

func main() {
	out := flag.String("out", "samples", "output directory")
	pngs := flag.Bool("png", false, "also write PNG previews")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	c := crest()
	writeDST(filepath.Join(*out, "Team crest.dst"), c, "Team crest")

	reg := lettering.NewRegistry()
	res, err := reg.Layout(lettering.Spec{Text: "ANDREW", Font: "Block", Height: 180})
	if err != nil {
		log.Fatal(err)
	}
	writeDST(filepath.Join(*out, "ANDREW.dst"), res.Stitches, "ANDREW")
	all, _ := reg.Layout(lettering.Spec{Text: "ABCDEFGHIJKLM NOPQRSTUVWXYZ 0123456789", Font: "Block", Height: 120})
	writeDST(filepath.Join(*out, "alphabet.dst"), all.Stitches, "alphabet")
	log.Printf("crest: %+v", pattern.Count(c))
	log.Printf("ANDREW: %+v", pattern.Count(res.Stitches))
	if *pngs {
		preview(filepath.Join(*out, "crest.png"), c)
		preview(filepath.Join(*out, "andrew.png"), res.Stitches)
		preview(filepath.Join(*out, "alphabet.png"), all.Stitches)
	}
}
