// Command fontpack turns Ink/Stitch font exports (JSON written by
// tools/inkstitch-fontexport) into packed fonts under fonts/, which are
// compiled into the app.
//
//	go run ./cmd/fontpack -in /path/to/export-json -out fonts
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

type export struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Keywords    []string           `json:"keywords"`
	License     string             `json:"license"`
	LicenseText string             `json:"license_text"`
	Original    string             `json:"original_font"`
	OriginalURL string             `json:"original_font_url"`
	MinScale    float64            `json:"min_scale"`
	MaxScale    float64            `json:"max_scale"`
	LetterCase  string             `json:"letter_case"`
	WordSpacing float64            `json:"word_spacing"`
	Kerning     map[string]float64 `json:"kerning"`
	Glyphs      map[string]struct {
		Adv    float64 `json:"adv"`
		Blocks []struct {
			Color string      `json:"color"`
			St    [][]float64 `json:"st"`
		} `json:"blocks"`
	} `json:"glyphs"`
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func excluded(e *export) string {
	lic := strings.ToUpper(e.License)
	if strings.Contains(lic, "NC") || strings.Contains(lic, "-ND") || strings.Contains(lic, " ND") {
		return "licence " + e.License
	}
	for _, k := range e.Keywords {
		if strings.EqualFold(k, "crossstitch") {
			return "cross-stitch font"
		}
	}
	return ""
}

func main() {
	in := flag.String("in", "", "directory of exported font JSON files")
	out := flag.String("out", "fonts", "output fonts directory")
	flag.Parse()
	files, _ := filepath.Glob(filepath.Join(*in, "*.json"))
	sort.Strings(files)
	var credits []string
	total := 0
	for _, fp := range files {
		raw, err := os.ReadFile(fp)
		if err != nil {
			log.Fatal(err)
		}
		var e export
		if err := json.Unmarshal(raw, &e); err != nil {
			log.Printf("skip %s: %v", fp, err)
			continue
		}
		if why := excluded(&e); why != "" {
			log.Printf("skip %s: %s", e.ID, why)
			continue
		}
		glyphs := map[string][]pattern.St{}
		adv := map[string]float64{}
		var colors []string
		for ch, g := range e.Glyphs {
			var sts []pattern.St
			for bi, b := range g.Blocks {
				if bi > 0 {
					sts = append(sts, pattern.St{Cmd: pattern.ColorChange})
				}
				for _, p := range b.St {
					x, y := p[0]*10, p[1]*10
					switch int(p[2]) {
					case 1:
						sts = append(sts, pattern.St{X: x, Y: y, Cmd: pattern.Jump})
					case 2:
						sts = append(sts, pattern.St{Cmd: pattern.Trim})
					case 4:
						// stops (applique etc.) are not used in lettering
					default:
						sts = append(sts, pattern.St{X: x, Y: y, Cmd: pattern.Stitch})
					}
				}
			}
			if pattern.Count(sts).Stitches == 0 {
				continue
			}
			glyphs[ch] = sts
			adv[ch] = g.Adv * 10
			if len(g.Blocks) > len(colors) {
				colors = colors[:0]
				for _, b := range g.Blocks {
					colors = append(colors, b.Color)
				}
			}
		}
		if len(glyphs) == 0 {
			log.Printf("skip %s: no glyphs", e.ID)
			continue
		}
		// baseline and cap height from a reference capital
		var ref []pattern.St
		for _, c := range []string{"H", "E", "I", "T", "N", "L"} {
			if g, ok := glyphs[c]; ok {
				ref = g
				break
			}
		}
		if ref == nil {
			for _, g := range glyphs {
				ref = append(ref, g...)
			}
		}
		rb := pattern.Bounds(ref)
		base, capH := rb.MaxY, rb.Height()
		if capH <= 0 {
			log.Printf("skip %s: no height", e.ID)
			continue
		}
		for ch, g := range glyphs {
			for i := range g {
				if g[i].Cmd == pattern.Stitch || g[i].Cmd == pattern.Jump {
					g[i].Y -= base
				}
			}
			glyphs[ch] = g
		}
		meta := lettering.PackedFontFile{
			Format:      lettering.PackedFormat,
			Name:        e.Name,
			Description: e.Description,
			Keywords:    e.Keywords,
			License:     e.License,
			Source:      "Ink/Stitch embroidery-fonts: " + e.ID,
			CapHeight:   math.Round(capH*10) / 10,
			WordSpace:   e.WordSpacing * 10,
			MinScale:    e.MinScale,
			MaxScale:    e.MaxScale,
			LetterCase:  e.LetterCase,
			Colors:      colors,
			Kerning:     map[string]float64{},
		}
		for k, v := range e.Kerning {
			meta.Kerning[k] = math.Round(v*100) / 10
		}
		bin, err := lettering.PackGlyphs(&meta, glyphs, adv)
		if err != nil {
			log.Printf("skip %s: %v", e.ID, err)
			continue
		}
		dir := filepath.Join(*out, unsafe.ReplaceAllString(e.ID, "_"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatal(err)
		}
		mj, _ := json.Marshal(meta)
		must(os.WriteFile(filepath.Join(dir, "font.json"), mj, 0o644))
		must(os.WriteFile(filepath.Join(dir, "glyphs.bin.gz"), bin, 0o644))
		must(os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(e.LicenseText), 0o644))
		size := len(mj) + len(bin)
		total += size
		credit := fmt.Sprintf("| %s | %s | %s |", e.Name, e.License, e.ID)
		credits = append(credits, credit)
		log.Printf("ok %-32s %4d glyphs  cap %5.1f mm  %6.0f KB", e.ID, len(glyphs), capH/10, float64(size)/1024)
	}
	sort.Strings(credits)
	md := "# Font credits\n\nThese lettering fonts come from the [Ink/Stitch embroidery font library](https://github.com/inkstitch/embroidery-fonts), converted to stitches with `tools/inkstitch-fontexport` and `cmd/fontpack`. Each folder keeps the font's own LICENSE. Fonts licensed non-commercial (NC) or no-derivatives (ND) are not included.\n\n| Font | Licence | Ink/Stitch folder |\n|---|---|---|\n" + strings.Join(credits, "\n") + "\n"
	must(os.WriteFile(filepath.Join(*out, "CREDITS.md"), []byte(md), 0o644))
	log.Printf("packed %d fonts, %.1f MB", len(credits), float64(total)/(1<<20))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
