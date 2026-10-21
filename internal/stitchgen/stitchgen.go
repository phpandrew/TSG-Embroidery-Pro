// Package stitchgen generates stitches from geometry: running stitch,
// satin columns, tatami fills and tie-in/tie-off locks. All units are
// 0.1 mm with Y down.
package stitchgen

import (
	"math"
	"sort"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// Pt is a 2D point.
type Pt struct{ X, Y float64 }

func (a Pt) sub(b Pt) Pt         { return Pt{a.X - b.X, a.Y - b.Y} }
func (a Pt) add(b Pt) Pt         { return Pt{a.X + b.X, a.Y + b.Y} }
func (a Pt) mul(k float64) Pt    { return Pt{a.X * k, a.Y * k} }
func (a Pt) len() float64        { return math.Hypot(a.X, a.Y) }
func dist(a, b Pt) float64       { return a.sub(b).len() }
func lerp(a, b Pt, t float64) Pt { return Pt{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t} }

func unit(a Pt) Pt {
	l := a.len()
	if l == 0 {
		return Pt{}
	}
	return a.mul(1 / l)
}

func st(p Pt, c pattern.Cmd) pattern.St { return pattern.St{X: p.X, Y: p.Y, Cmd: c} }

// Resample walks a polyline and returns points every step units (always
// including both ends).
func Resample(poly []Pt, step float64) []Pt {
	if len(poly) == 0 {
		return nil
	}
	out := []Pt{poly[0]}
	carry := 0.0
	for i := 1; i < len(poly); i++ {
		a, b := poly[i-1], poly[i]
		seg := dist(a, b)
		pos := step - carry
		for pos <= seg {
			out = append(out, lerp(a, b, pos/seg))
			pos += step
		}
		carry = seg - (pos - step)
	}
	if last := poly[len(poly)-1]; dist(out[len(out)-1], last) > step*0.25 {
		out = append(out, last)
	} else {
		out[len(out)-1] = last
	}
	return out
}

// Run produces a running stitch along poly.
func Run(poly []Pt, stitchLen float64) []pattern.St {
	pts := Resample(poly, stitchLen)
	out := make([]pattern.St, len(pts))
	for i, p := range pts {
		out[i] = st(p, pattern.Stitch)
	}
	return out
}

// Lock returns a small tie-in/tie-off at p, stepping back and forth along dir.
func Lock(p, dir Pt) []pattern.St {
	d := unit(dir).mul(7) // 0.7 mm
	if d.len() == 0 {
		d = Pt{7, 0}
	}
	return []pattern.St{st(p, pattern.Stitch), st(p.add(d), pattern.Stitch), st(p, pattern.Stitch), st(p.add(d), pattern.Stitch), st(p, pattern.Stitch)}
}

// SatinOptions controls satin column generation.
type SatinOptions struct {
	Width     float64 // column width (0.1 mm)
	Spacing   float64 // same-side needle spacing (density), default 4 = 0.4 mm
	PullComp  float64 // added to width on each side, default 2 = 0.2 mm
	Underlay  bool    // centre walk (narrow) or edge walk (wide)
	RunLength float64 // underlay stitch length, default 20 = 2 mm
}

func (o *SatinOptions) defaults() {
	if o.Spacing <= 0 {
		o.Spacing = 4
	}
	if o.RunLength <= 0 {
		o.RunLength = 20
	}
}

// normals returns a per-vertex unit normal (miter, clamped) for poly.
func normals(poly []Pt) []Pt {
	n := len(poly)
	out := make([]Pt, n)
	segN := func(i int) Pt { // normal of segment i -> i+1
		d := unit(poly[i+1].sub(poly[i]))
		return Pt{-d.Y, d.X}
	}
	for i := 0; i < n; i++ {
		switch {
		case n == 1:
			out[i] = Pt{0, -1}
		case i == 0:
			out[i] = segN(0)
		case i == n-1:
			out[i] = segN(n - 2)
		default:
			a, b := segN(i-1), segN(i)
			m := unit(a.add(b))
			cos := m.X*a.X + m.Y*a.Y
			if cos < 0.7 { // clamp miter on sharp corners
				cos = 0.7
			}
			out[i] = m.mul(1 / cos)
		}
	}
	return out
}

// Satin builds a satin column along a centre line, including underlay.
// It sews underlay first, then the cover from start to end. The line is
// split at sharp corners (turns over ~95°) into overlapping columns, the way
// block lettering is digitized, instead of producing long miter spikes.
func Satin(center []Pt, o SatinOptions) []pattern.St {
	o.defaults()
	pieces := splitSharp(center, 95)
	if len(pieces) == 1 {
		return satin(pieces[0], o)
	}
	var out []pattern.St
	for i, pc := range pieces {
		// extend each piece by half the width into the corner so columns overlap
		if i > 0 {
			d := unit(pc[0].sub(pc[1]))
			pc[0] = pc[0].add(d.mul(o.Width / 2))
		}
		if i < len(pieces)-1 {
			n := len(pc)
			d := unit(pc[n-1].sub(pc[n-2]))
			pc[n-1] = pc[n-1].add(d.mul(o.Width / 2))
		}
		s := satin(pc, o)
		if len(s) == 0 {
			continue
		}
		if i > 0 {
			out = append(out, st(Pt{s[0].X, s[0].Y}, pattern.Jump))
		}
		out = append(out, s...)
	}
	return out
}

// splitSharp breaks a polyline at vertices that turn more than maxTurn degrees.
func splitSharp(poly []Pt, maxTurn float64) [][]Pt {
	lim := math.Cos(maxTurn * math.Pi / 180)
	var out [][]Pt
	cur := []Pt{poly[0]}
	for i := 1; i < len(poly); i++ {
		cur = append(cur, poly[i])
		if i < len(poly)-1 {
			a := unit(poly[i].sub(poly[i-1]))
			b := unit(poly[i+1].sub(poly[i]))
			if a.X*b.X+a.Y*b.Y < lim {
				out = append(out, cur)
				cur = []Pt{poly[i]}
			}
		}
	}
	return append(out, cur)
}

func satin(center []Pt, o SatinOptions) []pattern.St {
	if len(center) < 2 {
		if len(center) == 1 {
			return []pattern.St{st(center[0], pattern.Stitch)}
		}
		return nil
	}
	half := o.Width/2 + o.PullComp
	// sample the centre at half-spacing; each sample alternates side.
	samples := Resample(center, o.Spacing/2)
	// per-sample normal: interpolate from the nearest polyline vertex normals
	vn := normals(center)
	cum := []float64{0}
	for i := 1; i < len(center); i++ {
		cum = append(cum, cum[i-1]+dist(center[i-1], center[i]))
	}
	normalAt := func(p Pt, s float64) Pt {
		seg := sort.SearchFloat64s(cum, s)
		if seg <= 0 {
			return vn[0]
		}
		if seg >= len(cum) {
			return vn[len(vn)-1]
		}
		a, b := cum[seg-1], cum[seg]
		t := 0.0
		if b > a {
			t = (s - a) / (b - a)
		}
		// keep the segment normal across the middle, blend to miter near vertices
		d := unit(center[seg].sub(center[seg-1]))
		sn := Pt{-d.Y, d.X}
		blend := 6.0 / math.Max(b-a, 1) // blend region ≈ 0.6 mm
		switch {
		case t < blend:
			return lerp(vn[seg-1], sn, t/blend)
		case t > 1-blend:
			return lerp(sn, vn[seg], (t-(1-blend))/blend)
		}
		return sn
	}

	var out []pattern.St
	if o.Underlay {
		if o.Width > 30 { // edge walk for columns over 3 mm
			inset := half * 0.55
			var left, right []Pt
			for i, p := range center {
				left = append(left, p.add(vn[i].mul(inset)))
				right = append(right, p.sub(vn[i].mul(inset)))
			}
			out = append(out, Run(left, o.RunLength)...)
			rev := make([]Pt, len(right))
			for i := range right {
				rev[i] = right[len(right)-1-i]
			}
			out = append(out, Run(rev, o.RunLength)...)
		} else { // centre walk: out and back
			out = append(out, Run(center, o.RunLength)...)
			rev := make([]Pt, len(center))
			for i := range center {
				rev[i] = center[len(center)-1-i]
			}
			out = append(out, Run(rev, o.RunLength)[1:]...)
		}
	}
	s := 0.0
	for i, p := range samples {
		if i > 0 {
			s += dist(samples[i-1], p)
		}
		n := normalAt(p, s)
		side := 1.0
		if i%2 == 1 {
			side = -1
		}
		out = append(out, st(p.add(n.mul(half*side)), pattern.Stitch))
	}
	return out
}

// FillOptions controls tatami fill generation.
type FillOptions struct {
	Angle     float64 // degrees
	Spacing   float64 // row spacing, default 4 = 0.4 mm
	StitchLen float64 // default 35 = 3.5 mm
	Underlay  bool    // sparse perpendicular underlay first
}

// Fill generates a tatami fill for a closed polygon. Concave shapes are
// split into regions joined by jumps.
func Fill(poly []Pt, o FillOptions) []pattern.St {
	return FillShapes([][]Pt{poly}, o)
}

// FillShapes fills several closed contours with the even-odd rule, so inner
// contours become holes (the counters of O, A, B…).
func FillShapes(shapes [][]Pt, o FillOptions) []pattern.St {
	if o.Spacing <= 0 {
		o.Spacing = 4
	}
	if o.StitchLen <= 0 {
		o.StitchLen = 35
	}
	var out []pattern.St
	if o.Underlay {
		out = append(out, fillRows(shapes, o.Angle+90, 30, 30, 0, 0)...)
	}
	top := fillRows(shapes, o.Angle, o.Spacing, o.StitchLen, 3, 1)
	// join underlay to cover with a hidden travel run when it stays inside
	if n := len(out); n > 0 && len(top) > 1 && top[0].Cmd == pattern.Jump {
		from := Pt{out[n-1].X, out[n-1].Y}
		to := Pt{top[0].X, top[0].Y}
		if segmentInside(shapes, from, to) {
			for _, t := range Resample([]Pt{from, to}, 20)[1:] {
				out = append(out, st(t, pattern.Stitch))
			}
			top = top[2:] // drop the jump and its landing stitch
		}
	}
	out = append(out, top...)
	return out
}

// segmentInside reports whether the straight line a→b stays inside the
// shapes (even-odd), sampled every 0.5 mm.
func segmentInside(shapes [][]Pt, a, b Pt) bool {
	in := func(p Pt) bool {
		res := false
		for _, rp := range shapes {
			for i := range rp {
				c, d := rp[i], rp[(i+1)%len(rp)]
				if (c.Y > p.Y) != (d.Y > p.Y) && p.X < c.X+(p.Y-c.Y)/(d.Y-c.Y)*(d.X-c.X) {
					res = !res
				}
			}
		}
		return res
	}
	n := int(dist(a, b)/5) + 1
	for i := 1; i < n; i++ {
		if !in(lerp(a, b, float64(i)/float64(n))) {
			return false
		}
	}
	return true
}

// fillRows does the scan-line work. stagger is the number of rows in the
// brick pattern; inset trims row ends so the fill sits inside the outline.
func fillRows(shapes [][]Pt, angle, spacing, stitchLen float64, stagger int, inset float64) []pattern.St {
	rad := angle * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	rot := func(p Pt) Pt { return Pt{p.X*cos + p.Y*sin, -p.X*sin + p.Y*cos} }
	unrot := func(p Pt) Pt { return Pt{p.X*cos - p.Y*sin, p.X*sin + p.Y*cos} }
	rshapes := make([][]Pt, len(shapes))
	minY, maxY := math.Inf(1), math.Inf(-1)
	for si, poly := range shapes {
		rp := make([]Pt, len(poly))
		for i, p := range poly {
			rp[i] = rot(p)
			minY = math.Min(minY, rp[i].Y)
			maxY = math.Max(maxY, rp[i].Y)
		}
		rshapes[si] = rp
	}
	type seg struct{ a, b float64 }
	var rows [][]seg
	var ys []float64
	for y := minY + spacing/2; y < maxY; y += spacing {
		var xs []float64
		for _, rp := range rshapes {
			for i := range rp {
				a, b := rp[i], rp[(i+1)%len(rp)]
				if (a.Y <= y && b.Y > y) || (b.Y <= y && a.Y > y) {
					xs = append(xs, a.X+(y-a.Y)/(b.Y-a.Y)*(b.X-a.X))
				}
			}
		}
		sort.Float64s(xs)
		var row []seg
		for i := 0; i+1 < len(xs); i += 2 {
			if xs[i+1]-xs[i] > 2*inset+1 {
				row = append(row, seg{xs[i] + inset, xs[i+1] - inset})
			}
		}
		rows = append(rows, row)
		ys = append(ys, y)
	}
	// Group row segments into regions: a segment continues a region when it
	// overlaps exactly one segment of the previous row, and that segment
	// overlaps only it (simple boustrophedon decomposition).
	type region struct {
		rows []int
		segs []seg
	}
	var regions []region
	overlaps := func(p, q seg) bool { return p.a < q.b && q.a < p.b }
	var prevRow []seg
	var prevReg []int
	for ri, row := range rows {
		curReg := make([]int, len(row))
		for k, sg := range row {
			cands := []int{}
			for pk, ps := range prevRow {
				if overlaps(ps, sg) {
					cands = append(cands, pk)
				}
			}
			cont := -1
			if len(cands) == 1 {
				n := 0
				for _, s2 := range row {
					if overlaps(prevRow[cands[0]], s2) {
						n++
					}
				}
				if n == 1 {
					cont = prevReg[cands[0]]
				}
			}
			if cont < 0 {
				regions = append(regions, region{})
				cont = len(regions) - 1
			}
			regions[cont].rows = append(regions[cont].rows, ri)
			regions[cont].segs = append(regions[cont].segs, sg)
			curReg[k] = cont
		}
		prevRow, prevReg = row, curReg
	}

	// even-odd point-in-shape test in rotated space (for travel stitches)
	inside := func(p Pt) bool {
		in := false
		for _, rp := range rshapes {
			for i := range rp {
				a, b := rp[i], rp[(i+1)%len(rp)]
				if (a.Y > p.Y) != (b.Y > p.Y) && p.X < a.X+(p.Y-a.Y)/(b.Y-a.Y)*(b.X-a.X) {
					in = !in
				}
			}
		}
		return in
	}
	travelInside := func(a, b Pt) bool {
		d := math.Hypot(b.X-a.X, b.Y-a.Y)
		n := int(d/5) + 1
		for i := 1; i < n; i++ {
			t := float64(i) / float64(n)
			if !inside(Pt{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t}) {
				return false
			}
		}
		return true
	}

	// row endpoints for a region sewn from corner c (0..3):
	// bit0 = start from last row, bit1 = first row runs right-to-left
	corner := func(rg region, c int) (Pt, Pt) {
		ri := 0
		if c&1 == 1 {
			ri = len(rg.rows) - 1
		}
		sg, y := rg.segs[ri], ys[rg.rows[ri]]
		startX := sg.a
		if c&2 == 2 {
			startX = sg.b
		}
		n := len(rg.rows)
		li := n - 1
		if c&1 == 1 {
			li = 0
		}
		lsg, ly := rg.segs[li], ys[rg.rows[li]]
		// direction of the last row alternates from the first
		rightToLeft := c&2 == 2
		if (n-1)%2 == 1 {
			rightToLeft = !rightToLeft
		}
		endX := lsg.b
		if rightToLeft {
			endX = lsg.a
		}
		return Pt{startX, y}, Pt{endX, ly}
	}

	var out []pattern.St
	done := make([]bool, len(regions))
	var pos Pt
	havePos := false
	for range regions {
		best, bestC, bestD := -1, 0, math.Inf(1)
		for i, rg := range regions {
			if done[i] {
				continue
			}
			for c := 0; c < 4; c++ {
				sp, _ := corner(rg, c)
				d := 0.0
				if havePos {
					d = math.Hypot(sp.X-pos.X, sp.Y-pos.Y)
				} else {
					d = sp.Y*1e-3 + sp.X*1e-6 + float64(i) // first region: topmost
				}
				if d < bestD {
					best, bestC, bestD = i, c, d
				}
			}
		}
		done[best] = true
		rg := regions[best]
		order := make([]int, len(rg.rows))
		for i := range order {
			order[i] = i
		}
		if bestC&1 == 1 {
			for l, r := 0, len(order)-1; l < r; l, r = l+1, r-1 {
				order[l], order[r] = order[r], order[l]
			}
		}
		sp, ep := corner(rg, bestC)
		// connect from the previous region
		if havePos && travelInside(pos, sp) {
			for _, t := range Resample([]Pt{pos, sp}, 20)[1:] {
				out = append(out, st(unrot(t), pattern.Stitch))
			}
		} else {
			out = append(out, st(unrot(sp), pattern.Jump), st(unrot(sp), pattern.Stitch))
		}
		rtl := bestC&2 == 2
		for _, idx := range order {
			sg, ri := rg.segs[idx], rg.rows[idx]
			y := ys[ri]
			a, b := sg.a, sg.b
			if rtl {
				a, b = b, a
			}
			rtl = !rtl
			pts := []Pt{{a, y}}
			// brick offset: stitch points on a grid shifted per row
			phase := 0.0
			if stagger > 0 {
				phase = float64(ri%stagger) * stitchLen / float64(stagger)
			}
			lo, hi := math.Min(a, b), math.Max(a, b)
			var mids []float64
			startX := math.Floor((lo-phase)/stitchLen)*stitchLen + phase + stitchLen
			for x := startX; x < hi-stitchLen*0.3; x += stitchLen {
				if x-lo > stitchLen*0.3 {
					mids = append(mids, x)
				}
			}
			if a > b {
				for l, r := 0, len(mids)-1; l < r; l, r = l+1, r-1 {
					mids[l], mids[r] = mids[r], mids[l]
				}
			}
			for _, x := range mids {
				pts = append(pts, Pt{x, y})
			}
			pts = append(pts, Pt{b, y})
			for _, p := range pts {
				out = append(out, st(unrot(p), pattern.Stitch))
			}
		}
		pos, havePos = ep, true
	}
	return out
}

// Outline produces a running stitch around a closed polygon.
func Outline(poly []Pt, stitchLen float64) []pattern.St {
	closed := append(append([]Pt{}, poly...), poly[0])
	return Run(closed, stitchLen)
}

// Offset returns the polygon grown (d>0) or shrunk (d<0) along vertex
// normals. Suitable for simple convex-ish shapes only.
func Offset(poly []Pt, d float64) []Pt {
	n := len(poly)
	out := make([]Pt, n)
	area := 0.0
	for i := range poly {
		a, b := poly[i], poly[(i+1)%n]
		area += a.X*b.Y - b.X*a.Y
	}
	if area < 0 {
		d = -d
	}
	for i := range poly {
		prev, cur, next := poly[(i-1+n)%n], poly[i], poly[(i+1)%n]
		d1 := unit(cur.sub(prev))
		d2 := unit(next.sub(cur))
		n1 := Pt{d1.Y, -d1.X}
		n2 := Pt{d2.Y, -d2.X}
		m := unit(n1.add(n2))
		c := m.X*n1.X + m.Y*n1.Y
		if c < 0.3 {
			c = 0.3
		}
		out[i] = cur.add(m.mul(d / c))
	}
	return out
}
