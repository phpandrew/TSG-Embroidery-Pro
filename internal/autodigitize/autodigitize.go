// Package autodigitize turns a raster image (logo, artwork) into stitch
// blocks: it reduces the picture to a few flat thread colours, cleans out
// specks, traces each colour area into outlines and fills them with tatami.
//
// It is a first-pass digitizer for simple, flat artwork - logos, lettering
// as pictures, clip art. Photos and gradients will come out as blobs.
package autodigitize

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // register decoders
	_ "image/jpeg" //
	_ "image/png"  //
	"io"
	"math"
	"sort"

	_ "golang.org/x/image/bmp" // register decoders
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" //
	_ "golang.org/x/image/webp" //

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	_ "github.com/phpandrew/TSG-Embroidery-Pro/internal/psd" // register PSD decoder
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/stitchgen"
)

// Options control the conversion. Lengths are in 0.1 mm.
type Options struct {
	Width            float64 // finished width (0.1 mm); height follows the image
	Colors           int     // thread colours to reduce to (1-12), default 4
	RemoveBackground bool    // drop the colour at the image edges / transparent areas
	MinDetail        float64 // smallest area to keep, in mm² (default 2)
	Angle            float64 // fill angle, degrees
	Spacing          float64 // row spacing, default 4 = 0.4 mm
	Outline          bool    // running-stitch outline around each colour area
	Underlay         bool    // underlay under fills and satin (recommended)
	PullComp         float64 // grow every shape by this much, 0.1 mm (default 2.5)
	NoSatin          bool    // force tatami everywhere (satin is used for narrow shapes by default)
}

// Stats describes how the artwork was stitched.
type Stats struct {
	SatinRegions int
	FillRegions  int
	Trims        int
}

func (o *Options) defaults() {
	if o.Width <= 0 {
		o.Width = 800
	}
	if o.Colors <= 0 {
		o.Colors = 4
	}
	if o.Colors > 12 {
		o.Colors = 12
	}
	if o.MinDetail <= 0 {
		o.MinDetail = 2
	}
	if o.Spacing <= 0 {
		o.Spacing = 4
	}
	if o.PullComp < 0 {
		o.PullComp = 0
	}
}

// Result is the stitched artwork: one block per colour, with the colour each
// block should be sewn in.
type Result struct {
	Blocks [][]pattern.St
	Colors []color.RGBA // one per block
	Width  float64      // 0.1 mm
	Height float64
	// Preview is the reduced image
	Preview *image.NRGBA
	Stats   Stats
}

const (
	maxInputBytes = 256 << 20
	maxPixels     = 40_000_000 // decoded image size guard
	workPxPerU    = 0.5        // working raster: pixels per 0.1 mm (= 20 px/mm)
	maxWorkPx     = 1600       // working raster longest side
)

// Decode reads any supported image (PNG, JPEG, GIF, WebP, BMP, TIFF, PSD).
func Decode(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInputBytes {
		return nil, errors.New("image file is too large (over 256 MB)")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a supported image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is too large (%d×%d)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("could not read %s image: %w", format, err)
	}
	return img, nil
}

// Convert digitizes img.
func Convert(img image.Image, o Options) (*Result, error) {
	o.defaults()
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, errors.New("empty image")
	}
	// working raster at 20 px/mm, capped
	w := int(math.Round(o.Width * workPxPerU))
	if w < 8 {
		w = 8
	}
	if w > maxWorkPx {
		w = maxWorkPx
	}
	h := int(math.Round(float64(w) * float64(b.Dy()) / float64(b.Dx())))
	if h > maxWorkPx {
		h = maxWorkPx
		w = int(math.Round(float64(h) * float64(b.Dx()) / float64(b.Dy())))
	}
	if h < 1 {
		h = 1
	}
	unit := o.Width / float64(w) // 0.1 mm per working pixel
	work := image.NewNRGBA(image.Rect(0, 0, w, h))
	// flatten onto white, then downscale with area averaging
	flat := image.NewNRGBA(b)
	draw.Draw(flat, b, image.White, image.Point{}, draw.Src)
	draw.Draw(flat, b, img, b.Min, draw.Over)
	draw.CatmullRom.Scale(work, work.Bounds(), flat, b, draw.Src, nil)
	alpha := alphaMask(img, w, h)

	// quantize
	px := make([][3]float64, w*h)
	for i := 0; i < w*h; i++ {
		px[i] = [3]float64{float64(work.Pix[i*4]), float64(work.Pix[i*4+1]), float64(work.Pix[i*4+2])}
	}
	k := o.Colors
	if o.RemoveBackground {
		k++ // the background takes one slot; it is dropped afterwards
	}
	palette, labels := kmeans(px, alpha, k)
	// transparent pixels never count
	for i := range labels {
		if !alpha[i] {
			labels[i] = -1
		}
	}
	if o.RemoveBackground {
		bg := edgeColour(labels, w, h, k)
		if bg >= 0 {
			for i := range labels {
				if labels[i] == bg {
					labels[i] = -1
				}
			}
		}
	}
	// clean: majority filter then drop small regions
	labels = smooth(labels, w, h)
	minPx := int(o.MinDetail * 100 / (unit * unit)) // mm² → working px (1 mm = 10 units)
	labels = dropSmall(labels, w, h, minPx)
	labels = smooth(labels, w, h)

	// per colour: contours → shapes → fill
	res := &Result{Width: float64(w) * unit, Height: float64(h) * unit}
	type colourJob struct {
		idx  int
		area int
	}
	var jobs []colourJob
	for c := 0; c < k; c++ {
		n := 0
		for _, l := range labels {
			if l == c {
				n++
			}
		}
		if n > 0 {
			jobs = append(jobs, colourJob{c, n})
		}
	}
	// sew big areas first (backgrounds under details), light before dark on ties
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].area != jobs[j].area {
			return jobs[i].area > jobs[j].area
		}
		return lum(palette[jobs[i].idx]) > lum(palette[jobs[j].idx])
	})
	pullPx := int(math.Round(o.PullComp / unit)) // pull compensation in working pixels
	var lastEnd *stitchgen.Pt
	for ji, j := range jobs {
		// pull compensation: grow this colour's area so it sews to size and
		// tucks under its neighbours (later colours cover the overlap)
		lab := labels
		if pullPx > 0 {
			lab = dilate(labels, w, h, j.idx, pullPx)
		}
		dist := distanceTransform(maskOf(lab, j.idx), w, h)
		var pieces [][]pattern.St // one per region, ordered afterwards
		for _, r := range regions(lab, w, h, j.idx) {
			if ribbon, _ := isRibbon(r, dist, unit); ribbon && !o.NoSatin {
				if sts := satinRegion(lab, w, h, r, dist, unit, o); pattern.Count(sts).Stitches > 0 {
					pieces = append(pieces, sts)
					res.Stats.SatinRegions++
					continue
				}
			}
			sub := regionLabels(lab, r, w*h)
			shapes := traceContours(sub, w, h, 1, unit)
			if len(shapes) == 0 {
				continue
			}
			for _, group := range groupShapes(shapes) {
				sts := stitchgen.FillShapes(group, stitchgen.FillOptions{Angle: o.Angle, Spacing: o.Spacing, Underlay: o.Underlay})
				if o.Outline {
					for _, poly := range group {
						sts = append(sts, stitchgen.Outline(poly, 25)...)
					}
				}
				if pattern.Count(sts).Stitches > 0 {
					pieces = append(pieces, sts)
					res.Stats.FillRegions++
				}
			}
		}
		if len(pieces) == 0 {
			continue
		}
		// sew the pieces in nearest-neighbour order from where the needle is,
		// so the machine isn't sent back and forth across the design
		// travel (hidden running stitches) is allowed across this colour's own
		// area and across anything a later colour will sew over
		covered := make([]bool, len(labels))
		for _, later := range jobs[ji:] {
			for i, l := range lab {
				if l == later.idx || labels[i] == later.idx {
					covered[i] = true
				}
			}
		}
		canTravel := func(a, b stitchgen.Pt) bool {
			n := int(math.Hypot(b.X-a.X, b.Y-a.Y)/5) + 1
			for i := 0; i <= n; i++ {
				t := float64(i) / float64(n)
				x := int((a.X + (b.X-a.X)*t) / unit)
				y := int((a.Y + (b.Y-a.Y)*t) / unit)
				if x < 0 || y < 0 || x >= w || y >= h || !covered[y*w+x] {
					return false
				}
			}
			return true
		}
		block, end := orderPieces(pieces, lastEnd, canTravel)
		lastEnd = &end
		res.Blocks = append(res.Blocks, block)
		c := palette[j.idx]
		res.Colors = append(res.Colors, color.RGBA{uint8(c[0] + .5), uint8(c[1] + .5), uint8(c[2] + .5), 255})
	}
	if len(res.Blocks) == 0 {
		return nil, errors.New("nothing to stitch: try more colours, a smaller 'smallest detail', or turn off background removal")
	}
	// centre on the origin
	all := pattern.EmptyRect()
	for _, bl := range res.Blocks {
		all.Union(pattern.Bounds(bl))
	}
	cx, cy := all.CenterX(), all.CenterY()
	for _, bl := range res.Blocks {
		for i := range bl {
			if bl[i].Cmd == pattern.Stitch || bl[i].Cmd == pattern.Jump {
				bl[i].X -= cx
				bl[i].Y -= cy
			}
		}
	}
	// preview of the reduced image
	pv := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i, l := range labels {
		if l < 0 {
			pv.Pix[i*4+3] = 0
			continue
		}
		c := palette[l]
		pv.Pix[i*4], pv.Pix[i*4+1], pv.Pix[i*4+2], pv.Pix[i*4+3] = uint8(c[0]+.5), uint8(c[1]+.5), uint8(c[2]+.5), 255
	}
	res.Preview = pv
	return res, nil
}

func lum(c [3]float64) float64 { return 0.299*c[0] + 0.587*c[1] + 0.114*c[2] }

// alphaMask downsamples the image's alpha: a working pixel is opaque when
// more than half its source area is.
func alphaMask(img image.Image, w, h int) []bool {
	out := make([]bool, w*h)
	b := img.Bounds()
	_, hasAlpha := img.(interface{ Opaque() bool })
	opaque := true
	if hasAlpha {
		opaque = img.(interface{ Opaque() bool }).Opaque()
	}
	if opaque {
		for i := range out {
			out[i] = true
		}
		return out
	}
	a := image.NewAlpha(image.Rect(0, 0, w, h))
	src := image.NewAlpha(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, al := img.At(x, y).RGBA()
			src.SetAlpha(x, y, color.Alpha{uint8(al >> 8)})
		}
	}
	draw.CatmullRom.Scale(a, a.Bounds(), src, b, draw.Src, nil)
	for i := range out {
		out[i] = a.Pix[i] >= 128
	}
	return out
}

// edgeColour finds the label most common around the image border.
func edgeColour(labels []int, w, h, k int) int {
	count := make([]int, k)
	total := 0
	for x := 0; x < w; x++ {
		for _, y := range []int{0, h - 1} {
			if l := labels[y*w+x]; l >= 0 {
				count[l]++
				total++
			}
		}
	}
	for y := 1; y < h-1; y++ {
		for _, x := range []int{0, w - 1} {
			if l := labels[y*w+x]; l >= 0 {
				count[l]++
				total++
			}
		}
	}
	best, bestN := -1, 0
	for c, n := range count {
		if n > bestN {
			best, bestN = c, n
		}
	}
	// only treat it as background when it really surrounds the artwork
	if total == 0 || bestN*100 < total*40 {
		return -1
	}
	return best
}

// kmeans clusters opaque pixels into k colours (k-means++ seeding, a few
// Lloyd iterations). Returns the palette and a label per pixel.
func kmeans(px [][3]float64, alpha []bool, k int) ([][3]float64, []int) {
	n := len(px)
	labels := make([]int, n)
	// sample for speed
	step := 1
	if n > 60000 {
		step = n / 60000
	}
	var sample [][3]float64
	for i := 0; i < n; i += step {
		if alpha[i] {
			sample = append(sample, px[i])
		}
	}
	if len(sample) == 0 {
		return make([][3]float64, k), labels
	}
	if k > len(sample) {
		k = len(sample)
	}
	// k-means++ seeding (deterministic: farthest-point)
	cent := [][3]float64{sample[0]}
	d2 := make([]float64, len(sample))
	for len(cent) < k {
		far, farD := 0, -1.0
		for i, p := range sample {
			d := dist2(p, cent[len(cent)-1])
			if len(cent) == 1 || d < d2[i] {
				d2[i] = d
			}
			if d2[i] > farD {
				far, farD = i, d2[i]
			}
		}
		if farD <= 0 {
			break
		}
		cent = append(cent, sample[far])
	}
	k = len(cent)
	for it := 0; it < 12; it++ {
		sum := make([][3]float64, k)
		cnt := make([]int, k)
		for _, p := range sample {
			c := nearest(p, cent)
			for j := 0; j < 3; j++ {
				sum[c][j] += p[j]
			}
			cnt[c]++
		}
		moved := 0.0
		for c := range cent {
			if cnt[c] == 0 {
				continue
			}
			nc := [3]float64{sum[c][0] / float64(cnt[c]), sum[c][1] / float64(cnt[c]), sum[c][2] / float64(cnt[c])}
			moved += dist2(nc, cent[c])
			cent[c] = nc
		}
		if moved < 1 {
			break
		}
	}
	for i, p := range px {
		if alpha[i] {
			labels[i] = nearest(p, cent)
		}
	}
	return cent, labels
}

func dist2(a, b [3]float64) float64 {
	// weighted RGB distance (eyes are most sensitive to green)
	dr, dg, db := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return 2*dr*dr + 4*dg*dg + 3*db*db
}

func nearest(p [3]float64, cent [][3]float64) int {
	best, bd := 0, math.Inf(1)
	for i, c := range cent {
		if d := dist2(p, c); d < bd {
			best, bd = i, d
		}
	}
	return best
}

// smooth replaces each pixel with the majority of its 3×3 neighbourhood
// (removes single-pixel noise and antialiasing fringes).
func smooth(labels []int, w, h int) []int {
	out := make([]int, len(labels))
	copy(out, labels)
	var count [16]int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for i := range count {
				count[i] = 0
			}
			bg := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					xx, yy := x+dx, y+dy
					if xx < 0 || yy < 0 || xx >= w || yy >= h {
						bg++
						continue
					}
					if l := labels[yy*w+xx]; l >= 0 && l < 16 {
						count[l]++
					} else {
						bg++
					}
				}
			}
			best, bn := -1, bg
			for l, n := range count {
				if n > bn {
					best, bn = l, n
				}
			}
			out[y*w+x] = best
		}
	}
	return out
}

// dropSmall reassigns connected regions smaller than minPx to the most
// common neighbouring label (or background).
func dropSmall(labels []int, w, h, minPx int) []int {
	if minPx <= 1 {
		return labels
	}
	out := make([]int, len(labels))
	copy(out, labels)
	seen := make([]bool, len(labels))
	var stack []int
	for start := range out {
		if seen[start] {
			continue
		}
		l := out[start]
		// flood fill
		region := []int{start}
		seen[start] = true
		stack = append(stack[:0], start)
		neigh := map[int]int{}
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := i%w, i/w
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				xx, yy := x+d[0], y+d[1]
				if xx < 0 || yy < 0 || xx >= w || yy >= h {
					neigh[-1]++
					continue
				}
				j := yy*w + xx
				if out[j] != l {
					neigh[out[j]]++
					continue
				}
				if !seen[j] {
					seen[j] = true
					region = append(region, j)
					stack = append(stack, j)
				}
			}
		}
		if len(region) >= minPx || l < 0 && len(region) >= minPx/4 {
			continue
		}
		best, bn := -1, -1
		for nl, n := range neigh {
			if n > bn {
				best, bn = nl, n
			}
		}
		for _, i := range region {
			out[i] = best
			seen[i] = false // may be merged again as part of a bigger region
		}
	}
	return out
}

// traceContours extracts the outlines of every region with label c as
// closed polygons in 0.1 mm, simplified. Holes come out as separate
// polygons (FillShapes uses even-odd, so they become holes).
func traceContours(labels []int, w, h, c int, unit float64) [][]stitchgen.Pt {
	// edge set on the pixel-corner lattice: horizontal edges between (x,y)
	// and (x+1,y) where the pixel above/below differ in membership, etc.
	in := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < w && y < h && labels[y*w+x] == c
	}
	// directed edges keep the region on the left, so loops come out
	// consistently oriented and holes opposite - fine for even-odd.
	next := map[[2]int][][2]int{}
	add := func(a, b [2]int) { next[a] = append(next[a], b) }
	for y := 0; y <= h; y++ {
		for x := 0; x <= w; x++ {
			// horizontal edge from (x,y) to (x+1,y)
			if x < w {
				top, bot := in(x, y-1), in(x, y)
				if bot && !top {
					add([2]int{x, y}, [2]int{x + 1, y})
				} else if top && !bot {
					add([2]int{x + 1, y}, [2]int{x, y})
				}
			}
			if y < h {
				left, right := in(x-1, y), in(x, y)
				if left && !right {
					add([2]int{x, y}, [2]int{x, y + 1})
				} else if right && !left {
					add([2]int{x, y + 1}, [2]int{x, y})
				}
			}
		}
	}
	var polys [][]stitchgen.Pt
	for len(next) > 0 {
		var start [2]int
		for k := range next {
			start = k
			break
		}
		var loop [][2]int
		cur := start
		for {
			outs := next[cur]
			if len(outs) == 0 {
				break
			}
			nxt := outs[0]
			if len(outs) > 1 {
				// pixel-corner ambiguity: prefer turning right (keeps regions separate)
				prev := cur
				if len(loop) > 0 {
					prev = loop[len(loop)-1]
				}
				nxt = pickTurn(prev, cur, outs)
			}
			// remove used edge
			for i, o := range outs {
				if o == nxt {
					outs = append(outs[:i], outs[i+1:]...)
					break
				}
			}
			if len(outs) == 0 {
				delete(next, cur)
			} else {
				next[cur] = outs
			}
			loop = append(loop, cur)
			cur = nxt
			if cur == start {
				break
			}
			if len(loop) > 4*(w+h)*(w+h) {
				break
			}
		}
		if len(loop) < 4 {
			continue
		}
		pts := make([]stitchgen.Pt, 0, len(loop))
		for _, p := range loop {
			pts = append(pts, stitchgen.Pt{X: float64(p[0]) * unit, Y: float64(p[1]) * unit})
		}
		pts = simplify(pts, 0.6*unit+1.5)                 // ~0.15 mm + a bit: kills staircase
		if len(pts) >= 3 && math.Abs(polyArea(pts)) > 4 { // > 0.04 mm²
			polys = append(polys, pts)
		}
	}
	return polys
}

func pickTurn(prev, cur [2]int, outs [][2]int) [2]int {
	dx, dy := cur[0]-prev[0], cur[1]-prev[1]
	// right turn of direction (dx,dy) in y-down coords is (-dy, dx)
	want := [2]int{cur[0] - dy, cur[1] + dx}
	for _, o := range outs {
		if o == want {
			return o
		}
	}
	return outs[0]
}

func polyArea(p []stitchgen.Pt) float64 {
	a := 0.0
	for i := range p {
		b := p[(i+1)%len(p)]
		a += p[i].X*b.Y - b.X*p[i].Y
	}
	return a / 2
}

// simplify is Douglas-Peucker on a closed polygon.
func simplify(p []stitchgen.Pt, tol float64) []stitchgen.Pt {
	if len(p) < 4 {
		return p
	}
	// split at the two farthest-apart points so the closed loop is two open chains
	far := 0
	for i := range p {
		if d := math.Hypot(p[i].X-p[0].X, p[i].Y-p[0].Y); d > math.Hypot(p[far].X-p[0].X, p[far].Y-p[0].Y) {
			far = i
		}
	}
	a := dp(p[:far+1], tol)
	b := dp(append(append([]stitchgen.Pt{}, p[far:]...), p[0]), tol)
	out := append(a[:len(a)-1], b[:len(b)-1]...)
	return out
}

func dp(p []stitchgen.Pt, tol float64) []stitchgen.Pt {
	if len(p) <= 2 {
		return p
	}
	a, b := p[0], p[len(p)-1]
	idx, maxD := 0, 0.0
	for i := 1; i < len(p)-1; i++ {
		if d := segDist(p[i], a, b); d > maxD {
			idx, maxD = i, d
		}
	}
	if maxD <= tol {
		return []stitchgen.Pt{a, b}
	}
	left := dp(p[:idx+1], tol)
	right := dp(p[idx:], tol)
	return append(left[:len(left)-1], right...)
}

func segDist(p, a, b stitchgen.Pt) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(p.X-a.X, p.Y-a.Y)
	}
	t := ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / l2
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(p.X-(a.X+t*dx), p.Y-(a.Y+t*dy))
}

// groupShapes puts each outer contour with the holes inside it, so every
// group is one even-odd fill (a letter O is outer + hole; two separate
// blobs are two groups, each filled and connected with its own travel).
func groupShapes(shapes [][]stitchgen.Pt) [][][]stitchgen.Pt {
	type sh struct {
		poly  []stitchgen.Pt
		area  float64
		outer bool
		depth int
	}
	list := make([]sh, len(shapes))
	for i, p := range shapes {
		list[i] = sh{poly: p, area: math.Abs(polyArea(p))}
	}
	// depth = number of other shapes containing this one's first point
	for i := range list {
		for j := range list {
			if i != j && list[j].area > list[i].area && pointIn(list[i].poly[0], list[j].poly) {
				list[i].depth++
			}
		}
		list[i].outer = list[i].depth%2 == 0
	}
	sort.Slice(list, func(i, j int) bool { return list[i].area > list[j].area })
	var groups [][][]stitchgen.Pt
	var owners []int
	for i := range list {
		if !list[i].outer {
			continue
		}
		groups = append(groups, [][]stitchgen.Pt{list[i].poly})
		owners = append(owners, i)
	}
	for i := range list {
		if list[i].outer {
			continue
		}
		// smallest outer that contains it
		best := -1
		for g, oi := range owners {
			if pointIn(list[i].poly[0], list[oi].poly) && (best < 0 || list[oi].area < list[owners[best]].area) {
				best = g
			}
		}
		if best >= 0 {
			groups[best] = append(groups[best], list[i].poly)
		}
	}
	return groups
}

func pointIn(p stitchgen.Pt, poly []stitchgen.Pt) bool {
	in := false
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		if (a.Y > p.Y) != (b.Y > p.Y) && p.X < a.X+(p.Y-a.Y)/(b.Y-a.Y)*(b.X-a.X) {
			in = !in
		}
	}
	return in
}

// maskOf is the boolean mask of label c.
func maskOf(labels []int, c int) []bool {
	m := make([]bool, len(labels))
	for i, l := range labels {
		m[i] = l == c
	}
	return m
}

// regionLabels returns a label map with 1 for the region's pixels, 0 elsewhere.
func regionLabels(labels []int, r region, n int) []int {
	out := make([]int, n)
	for _, i := range r.px {
		out[i] = 1
	}
	return out
}

// dilate grows label c by rad pixels (round brush) into every other label
// and into the background, returning a new label map.
func dilate(labels []int, w, h, c, rad int) []int {
	out := make([]int, len(labels))
	copy(out, labels)
	var offs [][2]int
	for dy := -rad; dy <= rad; dy++ {
		for dx := -rad; dx <= rad; dx++ {
			if dx*dx+dy*dy <= rad*rad {
				offs = append(offs, [2]int{dx, dy})
			}
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if labels[y*w+x] != c {
				continue
			}
			for _, d := range offs {
				xx, yy := x+d[0], y+d[1]
				if xx >= 0 && yy >= 0 && xx < w && yy < h {
					out[yy*w+xx] = c
				}
			}
		}
	}
	return out
}

// orderPieces concatenates pieces nearest-neighbour from start, joining them
// with jumps, and returns the block plus where the needle ends.
func orderPieces(pieces [][]pattern.St, start *stitchgen.Pt, canTravel func(a, b stitchgen.Pt) bool) ([]pattern.St, stitchgen.Pt) {
	first := func(p []pattern.St) stitchgen.Pt {
		for _, s := range p {
			if s.Cmd == pattern.Stitch || s.Cmd == pattern.Jump {
				return stitchgen.Pt{X: s.X, Y: s.Y}
			}
		}
		return stitchgen.Pt{}
	}
	last := func(p []pattern.St) stitchgen.Pt {
		for i := len(p) - 1; i >= 0; i-- {
			if p[i].Cmd == pattern.Stitch || p[i].Cmd == pattern.Jump {
				return stitchgen.Pt{X: p[i].X, Y: p[i].Y}
			}
		}
		return stitchgen.Pt{}
	}
	cur := stitchgen.Pt{}
	if start != nil {
		cur = *start
	} else if len(pieces) > 0 {
		// no history: begin with the largest piece's start
		big := 0
		for i, p := range pieces {
			if len(p) > len(pieces[big]) {
				big = i
			}
		}
		cur = first(pieces[big])
	}
	used := make([]bool, len(pieces))
	var out []pattern.St
	for n := 0; n < len(pieces); n++ {
		best, bd := -1, math.Inf(1)
		for i, p := range pieces {
			if used[i] {
				continue
			}
			f := first(p)
			if d := math.Hypot(f.X-cur.X, f.Y-cur.Y); d < bd {
				best, bd = i, d
			}
		}
		used[best] = true
		p := pieces[best]
		f := first(p)
		if len(out) > 0 {
			if canTravel != nil && bd > 1 && bd < 400 && canTravel(cur, f) {
				// walk there under the stitching instead of a jump + trim
				for _, t := range stitchgen.Resample([]stitchgen.Pt{cur, f}, 25)[1:] {
					out = append(out, pattern.St{X: t.X, Y: t.Y, Cmd: pattern.Stitch})
				}
				if p[0].Cmd == pattern.Jump && len(p) > 1 && p[0].X == p[1].X && p[0].Y == p[1].Y {
					p = p[1:] // the piece's own leading jump is now redundant
				}
			} else if p[0].Cmd != pattern.Jump {
				out = append(out, pattern.St{X: f.X, Y: f.Y, Cmd: pattern.Jump})
			}
		}
		out = append(out, p...)
		cur = last(p)
	}
	return out, cur
}
