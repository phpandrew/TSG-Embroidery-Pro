package app

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/applog"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/autodigitize"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/document"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/preview"
)

// Image → embroidery. The frontend opens an image, previews the conversion
// with live settings, then imports the result as a design.

var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".bmp": true, ".tif": true, ".tiff": true, ".psd": true,
}

// IsImagePath reports whether path looks like a picture we can digitize.
func IsImagePath(path string) bool { return imageExts[strings.ToLower(filepath.Ext(path))] }

// ImageSpec is the conversion settings chosen in the dialog.
type ImageSpec struct {
	Path             string  `json:"path"`
	Width            float64 `json:"width"`  // mm
	Colors           int     `json:"colors"` // thread colours
	RemoveBackground bool    `json:"removeBackground"`
	MinDetail        float64 `json:"minDetail"` // mm²
	Angle            float64 `json:"angle"`
	Density          float64 `json:"density"` // row spacing, mm
	Outline          bool    `json:"outline"`
	Underlay         bool    `json:"underlay"`
}

// ImagePreviewResult is what the dialog shows.
type ImagePreviewResult struct {
	Original string   `json:"original"` // data URL of the source image (small)
	Reduced  string   `json:"reduced"`  // data URL of the flat-colour version
	Stitches string   `json:"stitches"` // data URL of the stitch preview
	Colors   []string `json:"colors"`
	Count    int      `json:"count"`
	Blocks   int      `json:"blocks"`
	WidthMM  float64  `json:"widthMM"`
	HeightMM float64  `json:"heightMM"`
	Warning  string   `json:"warning,omitempty"`
}

// imageCache keeps the last decoded image so slider changes don't re-read
// the file.
type imageCache struct {
	mu   sync.Mutex
	path string
	img  image.Image
}

func (a *App) loadImage(path string) (image.Image, error) {
	a.imgCache.mu.Lock()
	defer a.imgCache.mu.Unlock()
	if a.imgCache.path == path && a.imgCache.img != nil {
		return a.imgCache.img, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := autodigitize.Decode(f)
	if err != nil {
		return nil, err
	}
	a.imgCache.path, a.imgCache.img = path, img
	return img, nil
}

func (s ImageSpec) options() autodigitize.Options {
	return autodigitize.Options{
		Width:            s.Width * 10,
		Colors:           s.Colors,
		RemoveBackground: s.RemoveBackground,
		MinDetail:        s.MinDetail,
		Angle:            s.Angle,
		Spacing:          s.Density * 10,
		Outline:          s.Outline,
		Underlay:         s.Underlay,
	}
}

func dataURL(img image.Image) string {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ImageInfo opens an image and suggests settings (fit the hoop width).
func (a *App) ImageInfo(path string) (ImageSpec, error) {
	img, err := a.loadImage(path)
	if err != nil {
		return ImageSpec{}, err
	}
	hoop := document.HoopByID(a.cur().Settings().Hoop)
	w := hoop.W * 0.9 / 10 // mm, 90% of the hoop
	b := img.Bounds()
	if h := w * float64(b.Dy()) / float64(b.Dx()); h > hoop.H*0.9/10 {
		w = w * (hoop.H * 0.9 / 10) / h
	}
	return ImageSpec{
		Path: path, Width: float64(int(w)), Colors: 4, RemoveBackground: true,
		MinDetail: 2, Angle: 45, Density: 0.4,
	}, nil
}

// ImagePreview converts with the given settings and returns previews.
func (a *App) ImagePreview(spec ImageSpec, pxW, pxH int) (ImagePreviewResult, error) {
	img, err := a.loadImage(spec.Path)
	if err != nil {
		return ImagePreviewResult{}, err
	}
	pxW = min(max(pxW, 100), 1200)
	pxH = min(max(pxH, 100), 1200)
	res, err := autodigitize.Convert(img, spec.options())
	if err != nil {
		return ImagePreviewResult{}, err
	}
	var cols []color.RGBA
	var hex []string
	total := 0
	for i, c := range res.Colors {
		cols = append(cols, c)
		hex = append(hex, fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B))
		total += pattern.Count(res.Blocks[i]).Stitches
	}
	stPNG, err := preview.PNG(res.Blocks, cols, pxW, pxH)
	if err != nil {
		return ImagePreviewResult{}, err
	}
	out := ImagePreviewResult{
		Reduced:  dataURL(res.Preview),
		Stitches: "data:image/png;base64," + base64.StdEncoding.EncodeToString(stPNG),
		Colors:   hex, Count: total, Blocks: len(res.Blocks),
		WidthMM: res.Width / 10, HeightMM: res.Height / 10,
	}
	if total > 60000 {
		out.Warning = "That's a lot of stitches: reduce the size, colours or detail."
	}
	return out, nil
}

// ImageThumb returns a small data-URL copy of the source picture.
func (a *App) ImageThumb(path string) (string, error) {
	img, err := a.loadImage(path)
	if err != nil {
		return "", err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > 400 || h > 300 {
		k := min(400/float64(w), 300/float64(h))
		w, h = max(int(float64(w)*k), 1), max(int(float64(h)*k), 1)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dataURL(dst), nil
}

// ImportImage digitizes the image and adds it as a design in the current
// tab (newTab: in a fresh tab).
func (a *App) ImportImage(spec ImageSpec, newTab bool) (LetteringResult, error) {
	img, err := a.loadImage(spec.Path)
	if err != nil {
		v, _ := a.view(err)
		return LetteringResult{View: v}, err
	}
	res, err := autodigitize.Convert(img, spec.options())
	if err != nil {
		v, _ := a.view(err)
		return LetteringResult{View: v}, err
	}
	var hex []string
	for _, c := range res.Colors {
		hex = append(hex, fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B))
	}
	if newTab && !a.cur().Blank() {
		a.addTab(a.newEditor())
	}
	name := strings.TrimSuffix(filepath.Base(spec.Path), filepath.Ext(spec.Path))
	applog.Infof("digitized %q: %d colours, %d blocks", spec.Path, len(hex), len(res.Blocks))
	id, err := a.cur().AddColouredBlocks(name, spec.Path, res.Blocks, hex)
	if err == nil {
		a.addRecent(spec.Path)
	}
	v, err := a.view(err)
	warn := ""
	if len(res.Blocks) > 6 {
		warn = fmt.Sprintf("%d colour changes: consider fewer colours", len(res.Blocks))
	}
	return LetteringResult{View: v, ID: id, Warning: warn}, err
}

// OpenImage shows a file dialog for pictures and returns the chosen path
// (the frontend then opens the conversion dialog).
func (a *App) OpenImage() (string, error) {
	files, err := a.ui.OpenFiles("Open picture to digitize", false, false, a.dir(false))
	if err != nil || len(files) == 0 {
		return "", err
	}
	a.rememberDir(false, files...)
	if !IsImagePath(files[0]) {
		return "", errors.New("choose a PNG, JPEG, GIF, WebP, BMP, TIFF or PSD file")
	}
	return files[0], nil
}
