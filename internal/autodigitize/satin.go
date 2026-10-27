package autodigitize

import (
	"math"
	"sort"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/stitchgen"
)

// Narrow regions - letter strokes, outlines, thin bars - sew far better as
// satin columns than as tatami rows. For each connected region of a colour
// we measure its width with a distance transform; ribbon-like regions under
// the satin limit are thinned to a centre line and stitched as satin.

const (
	satinMaxWidth = 55.0 // 0.1 mm: regions narrower than 5.5 mm go satin
	satinMinLen   = 25.0 // skip stubs shorter than 2.5 mm
)

// region is one connected group of pixels of a colour.
type region struct {
	px                     []int // pixel indices
	minX, minY, maxX, maxY int
}

// regions splits the pixels with label c into 4-connected regions.
func regions(labels []int, w, h, c int) []region {
	seen := make([]bool, len(labels))
	var out []region
	var stack []int
	for start, l := range labels {
		if l != c || seen[start] {
			continue
		}
		r := region{minX: w, minY: h}
		seen[start] = true
		stack = append(stack[:0], start)
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			r.px = append(r.px, i)
			x, y := i%w, i/w
			r.minX, r.maxX = min(r.minX, x), max(r.maxX, x)
			r.minY, r.maxY = min(r.minY, y), max(r.maxY, y)
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				xx, yy := x+d[0], y+d[1]
				if xx < 0 || yy < 0 || xx >= w || yy >= h {
					continue
				}
				j := yy*w + xx
				if labels[j] == c && !seen[j] {
					seen[j] = true
					stack = append(stack, j)
				}
			}
		}
		out = append(out, r)
	}
	return out
}

// distanceTransform gives each region pixel its distance (in pixels) to the
// nearest non-region pixel, using a two-pass chamfer (3-4 metric / 3).
func distanceTransform(mask []bool, w, h int) []float64 {
	const inf = 1e9
	d := make([]float64, w*h)
	for i, m := range mask {
		if m {
			d[i] = inf
		}
	}
	at := func(x, y int) float64 {
		if x < 0 || y < 0 || x >= w || y >= h {
			return 0
		}
		return d[y*w+x]
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if d[i] == 0 {
				continue
			}
			d[i] = math.Min(d[i], math.Min(math.Min(at(x-1, y)+1, at(x, y-1)+1), math.Min(at(x-1, y-1)+1.3334, at(x+1, y-1)+1.3334)))
		}
	}
	for y := h - 1; y >= 0; y-- {
		for x := w - 1; x >= 0; x-- {
			i := y*w + x
			if d[i] == 0 {
				continue
			}
			d[i] = math.Min(d[i], math.Min(math.Min(at(x+1, y)+1, at(x, y+1)+1), math.Min(at(x+1, y+1)+1.3334, at(x-1, y+1)+1.3334)))
		}
	}
	return d
}

// widthOf estimates a region's typical width (pixels) as twice the mean
// distance of its pixels to the edge, weighted towards the middle: for a
// ribbon the mean distance is width/4, so width ≈ 4 × mean.
func widthOf(r region, dist []float64) float64 {
	sum := 0.0
	for _, i := range r.px {
		sum += dist[i]
	}
	return 4 * sum / float64(len(r.px))
}

// isRibbon reports whether a region is a long thin shape: its typical width
// is under the satin limit and it is clearly longer than it is wide.
func isRibbon(r region, dist []float64, unit float64) (bool, float64) {
	wpx := widthOf(r, dist)
	wu := wpx * unit
	if wu > satinMaxWidth || wu < 4 {
		return false, wu
	}
	length := float64(len(r.px)) / math.Max(wpx, 1)
	return length*unit > 2.5*wu && length*unit >= satinMinLen, wu
}

// thin reduces a region mask to a one-pixel skeleton (Zhang-Suen).
func thin(mask []bool, w, h int, r region) []bool {
	sk := make([]bool, len(mask))
	for _, i := range r.px {
		sk[i] = true
	}
	at := func(x, y int) int {
		if x < 0 || y < 0 || x >= w || y >= h || !sk[y*w+x] {
			return 0
		}
		return 1
	}
	changed := true
	for iter := 0; changed && iter < 200; iter++ {
		changed = false
		for pass := 0; pass < 2; pass++ {
			var del []int
			for y := r.minY; y <= r.maxY; y++ {
				for x := r.minX; x <= r.maxX; x++ {
					if !sk[y*w+x] {
						continue
					}
					p2, p3, p4, p5 := at(x, y-1), at(x+1, y-1), at(x+1, y), at(x+1, y+1)
					p6, p7, p8, p9 := at(x, y+1), at(x-1, y+1), at(x-1, y), at(x-1, y-1)
					b := p2 + p3 + p4 + p5 + p6 + p7 + p8 + p9
					if b < 2 || b > 6 {
						continue
					}
					seq := [9]int{p2, p3, p4, p5, p6, p7, p8, p9, p2}
					a := 0
					for i := 0; i < 8; i++ {
						if seq[i] == 0 && seq[i+1] == 1 {
							a++
						}
					}
					if a != 1 {
						continue
					}
					if pass == 0 {
						if p2*p4*p6 != 0 || p4*p6*p8 != 0 {
							continue
						}
					} else if p2*p4*p8 != 0 || p2*p6*p8 != 0 {
						continue
					}
					del = append(del, y*w+x)
				}
			}
			for _, i := range del {
				sk[i] = false
			}
			if len(del) > 0 {
				changed = true
			}
		}
	}
	return sk
}

// skeletonPaths turns a pixel skeleton into polylines (pixel coordinates),
// walking from endpoints and junctions; branches shorter than minLen pixels
// are pruned.
func skeletonPaths(sk []bool, w, h int, r region, minLen float64) [][][2]int {
	on := func(x, y int) bool { return x >= 0 && y >= 0 && x < w && y < h && sk[y*w+x] }
	// 8-neighbours, but a diagonal neighbour that is also reachable through
	// a shared 4-neighbour is redundant (thinning leaves such staircases) and
	// would otherwise look like a junction
	neigh := func(x, y int) [][2]int {
		var out [][2]int
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				if dx == 0 && dy == 0 || !on(x+dx, y+dy) {
					continue
				}
				if dx != 0 && dy != 0 && (on(x+dx, y) || on(x, y+dy)) {
					continue
				}
				out = append(out, [2]int{x + dx, y + dy})
			}
		}
		return out
	}
	used := make([]bool, len(sk))
	var paths [][][2]int
	walk := func(x, y int) {
		// follow from a start node until a junction/endpoint
		start := [2]int{x, y}
		for _, first := range neigh(x, y) {
			if used[first[1]*w+first[0]] {
				continue
			}
			path := [][2]int{start, first}
			used[start[1]*w+start[0]] = true
			used[first[1]*w+first[0]] = true
			cur := first
			for {
				ns := neigh(cur[0], cur[1])
				var next [][2]int
				for _, n := range ns {
					if !used[n[1]*w+n[0]] {
						next = append(next, n)
					}
				}
				if len(next) != 1 || len(ns) > 2 && len(path) > 2 {
					// closed loop: came back next to where we started
					if len(next) == 0 && len(path) > 3 {
						for _, n := range ns {
							if n == start {
								path = append(path, start)
							}
						}
					}
					break
				}
				cur = next[0]
				used[cur[1]*w+cur[0]] = true
				path = append(path, cur)
			}
			paths = append(paths, path)
		}
	}
	// endpoints and junctions first, so paths start at natural ends
	for pass := 0; pass < 2; pass++ {
		for y := r.minY; y <= r.maxY; y++ {
			for x := r.minX; x <= r.maxX; x++ {
				i := y*w + x
				if !sk[i] || used[i] {
					continue
				}
				n := len(neigh(x, y))
				if (pass == 0 && n != 2) || pass == 1 {
					walk(x, y)
				}
			}
		}
	}
	// join paths end to end where they meet at a shared pixel and no other
	// path does (a junction that was really just a kink)
	var out [][][2]int
	for _, p := range paths {
		l := 0.0
		for i := 1; i < len(p); i++ {
			l += math.Hypot(float64(p[i][0]-p[i-1][0]), float64(p[i][1]-p[i-1][1]))
		}
		if l >= minLen || len(paths) == 1 {
			out = append(out, p)
		}
	}
	return out
}

// satinRegion stitches one ribbon-shaped region as satin columns along its
// skeleton. Returns nil when the skeleton is unusable (caller falls back
// to tatami).
func satinRegion(labels []int, w, h int, r region, dist []float64, unit float64, o Options) []pattern.St {
	mask := make([]bool, len(labels))
	for _, i := range r.px {
		mask[i] = true
	}
	sk := thin(mask, w, h, r)
	_, wu := isRibbon(r, dist, unit)
	paths := skeletonPaths(sk, w, h, r, math.Max(wu/unit, 3))
	if len(paths) == 0 {
		return nil
	}
	var out []pattern.St
	for _, p := range paths {
		if len(p) < 2 {
			continue
		}
		// width along this path from the distance transform, in 0.1 mm
		ws := make([]float64, 0, len(p))
		pts := make([]stitchgen.Pt, 0, len(p))
		for _, q := range p {
			ws = append(ws, 2*dist[q[1]*w+q[0]]*unit)
			pts = append(pts, stitchgen.Pt{X: (float64(q[0]) + 0.5) * unit, Y: (float64(q[1]) + 0.5) * unit})
		}
		sort.Float64s(ws)
		width := ws[len(ws)/2] // median
		if width < 6 {
			width = 6
		}
		pts = simplifyOpen(pts, 0.8*unit+1)
		if polylineLen(pts) < satinMinLen*0.6 {
			continue
		}
		sts := stitchgen.Satin(pts, stitchgen.SatinOptions{
			Width:    width, // the region was already grown by the pull compensation
			Spacing:  o.Spacing,
			PullComp: 0,
			Underlay: o.Underlay,
		})
		if len(sts) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, pattern.St{X: sts[0].X, Y: sts[0].Y, Cmd: pattern.Jump})
		}
		out = append(out, sts...)
	}
	return out
}

func polylineLen(p []stitchgen.Pt) float64 {
	l := 0.0
	for i := 1; i < len(p); i++ {
		l += math.Hypot(p[i].X-p[i-1].X, p[i].Y-p[i-1].Y)
	}
	return l
}

// simplifyOpen is Douglas-Peucker for an open polyline.
func simplifyOpen(p []stitchgen.Pt, tol float64) []stitchgen.Pt {
	if len(p) < 3 {
		return p
	}
	return dp(p, tol)
}
