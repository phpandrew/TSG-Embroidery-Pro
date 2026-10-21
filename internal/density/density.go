// Package density changes the density of raw stitch data (no object data
// needed). It recognises the two structures that make up most digitized
// designs and rebuilds them at a new spacing:
//
//   - satin columns: needle points alternating between two rails (zig-zag).
//     Each rail is resampled independently and the rails are re-zipped, so
//     column shape and width are kept exactly.
//   - tatami fills: back-and-forth rows. Rows are re-interpolated between
//     their neighbours at the new row spacing and re-stitched at the original
//     stitch length.
//
// Running stitches, underlay (spacing wider than the cover limits), tie-ins
// and anything unrecognised pass through unchanged.
package density

import (
	"math"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// Options controls recognition and output.
type Options struct {
	Factor float64 // >1 denser, <1 lighter (1.2 = 20% more stitches)

	// Only cover stitching is changed: satins with zig-zag spacing up to
	// SatinMaxSpacing and fills with row spacing up to FillMaxSpacing (0.1 mm).
	SatinMaxSpacing float64
	FillMaxSpacing  float64
	// Safety limits on the result (0.1 mm).
	MinSpacing float64
}

func (o *Options) defaults() {
	if o.SatinMaxSpacing == 0 {
		o.SatinMaxSpacing = 12 // 1.2 mm
	}
	if o.FillMaxSpacing == 0 {
		o.FillMaxSpacing = 12
	}
	if o.MinSpacing == 0 {
		o.MinSpacing = 1.5 // 0.15 mm: denser than this just cuts fabric
	}
}

// Report says what was changed.
type Report struct {
	Satins     int     `json:"satins"`
	Fills      int     `json:"fills"`
	Before     int     `json:"before"`
	After      int     `json:"after"`
	Untouched  int     `json:"untouched"` // stitches left as they were
	Clamped    bool    `json:"clamped"`   // some spacing hit MinSpacing
	FactorUsed float64 `json:"factor"`
}

type pt struct{ x, y float64 }

func (a pt) sub(b pt) pt         { return pt{a.x - b.x, a.y - b.y} }
func (a pt) mul(k float64) pt    { return pt{a.x * k, a.y * k} }
func (a pt) dot(b pt) float64    { return a.x*b.x + a.y*b.y }
func (a pt) len() float64        { return math.Hypot(a.x, a.y) }
func lerp(a, b pt, t float64) pt { return pt{a.x + (b.x-a.x)*t, a.y + (b.y-a.y)*t} }

// Adjust rebuilds one block's stitches at a new density.
func Adjust(sts []pattern.St, o Options) ([]pattern.St, Report) {
	o.defaults()
	rep := Report{FactorUsed: o.Factor}
	if o.Factor <= 0 || math.Abs(o.Factor-1) < 1e-6 {
		n := pattern.Count(sts).Stitches
		return append([]pattern.St(nil), sts...), Report{Before: n, After: n, Untouched: n, FactorUsed: 1}
	}
	var out []pattern.St
	// work on runs of consecutive needle points; everything else is copied
	i := 0
	for i < len(sts) {
		if sts[i].Cmd != pattern.Stitch {
			out = append(out, sts[i])
			i++
			continue
		}
		j := i
		for j < len(sts) && sts[j].Cmd == pattern.Stitch {
			j++
		}
		run := make([]pt, j-i)
		for k := range run {
			run[k] = pt{sts[i+k].X, sts[i+k].Y}
		}
		rep.Before += len(run)
		for _, p := range processRun(run, o, &rep) {
			out = append(out, pattern.St{X: p.x, Y: p.y, Cmd: pattern.Stitch})
		}
		i = j
	}
	rep.After = pattern.Count(out).Stitches
	return out, rep
}

// processRun splits a needle run into satin / fill / other segments.
func processRun(run []pt, o Options, rep *Report) []pt {
	var out []pt
	i := 0
	for i < len(run) {
		if n := satinLen(run, i); n >= 8 {
			seg := run[i : i+n]
			if sp := satinSpacing(seg); sp > 0 && sp <= o.SatinMaxSpacing {
				res, clamped := resampleSatin(seg, sp, o)
				rep.Clamped = rep.Clamped || clamped
				out = appendJoin(out, res)
				rep.Satins++
				i += n
				continue
			}
		}
		if n, rows := fillRows(run, i); n > 0 {
			seg := run[i : i+n]
			if sp := rowSpacing(rows); sp > 0 && sp <= o.FillMaxSpacing {
				res, clamped := refill(rows, sp, o)
				rep.Clamped = rep.Clamped || clamped
				out = appendJoin(out, res)
				rep.Fills++
				i += n
				continue
			}
			_ = seg
		}
		out = append(out, run[i])
		rep.Untouched++
		i++
	}
	return out
}

func appendJoin(out, seg []pt) []pt {
	if len(out) > 0 && len(seg) > 0 && out[len(out)-1] == seg[0] {
		seg = seg[1:]
	}
	return append(out, seg...)
}

// ---- satin ----------------------------------------------------------------

// satinLen returns how many points starting at i form a zig-zag: consecutive
// segments roughly antiparallel, long compared with the advance per zig.
func satinLen(run []pt, i int) int {
	n := 1
	for k := i + 1; k+1 < len(run); k++ {
		a := run[k].sub(run[k-1])
		b := run[k+1].sub(run[k])
		la, lb := a.len(), b.len()
		if la < 6 || lb < 6 { // < 0.6 mm wide: not a satin
			break
		}
		if a.dot(b) > -0.6*la*lb { // not turning back
			break
		}
		// advance between same-side points must be small vs width
		adv := run[k+1].sub(run[k-1]).len()
		if adv > 0.6*math.Min(la, lb) {
			break
		}
		n++
	}
	if n < 2 {
		return 0
	}
	return n + 1
}

func satinSpacing(seg []pt) float64 {
	// mean distance between same-side neighbours
	var sum float64
	var c int
	for k := 2; k < len(seg); k++ {
		sum += seg[k].sub(seg[k-2]).len()
		c++
	}
	if c == 0 {
		return 0
	}
	return sum / float64(c)
}

type rail struct {
	p   []pt
	cum []float64
}

func newRail(p []pt) rail {
	r := rail{p: p, cum: make([]float64, len(p))}
	for i := 1; i < len(p); i++ {
		r.cum[i] = r.cum[i-1] + p[i].sub(p[i-1]).len()
	}
	return r
}

func (r rail) at(t float64) pt {
	total := r.cum[len(r.cum)-1]
	if total == 0 {
		return r.p[0]
	}
	d := t * total
	for i := 1; i < len(r.p); i++ {
		if r.cum[i] >= d {
			seg := r.cum[i] - r.cum[i-1]
			if seg == 0 {
				return r.p[i]
			}
			return lerp(r.p[i-1], r.p[i], (d-r.cum[i-1])/seg)
		}
	}
	return r.p[len(r.p)-1]
}

func resampleSatin(seg []pt, spacing float64, o Options) ([]pt, bool) {
	var a, b []pt
	for k, p := range seg {
		if k%2 == 0 {
			a = append(a, p)
		} else {
			b = append(b, p)
		}
	}
	ra, rb := newRail(a), newRail(b)
	newSp := spacing / o.Factor
	clamped := false
	if newSp < o.MinSpacing {
		newSp, clamped = o.MinSpacing, true
	}
	length := math.Max(ra.cum[len(ra.cum)-1], rb.cum[len(rb.cum)-1])
	pairs := int(math.Round(length/newSp)) + 1
	if pairs < 2 {
		pairs = 2
	}
	out := make([]pt, 0, pairs*2)
	for k := 0; k < pairs; k++ {
		t := float64(k) / float64(pairs-1)
		out = append(out, ra.at(t))
		// b point sits half a step on, so the zig-zag keeps its slant
		tb := math.Min(1, (float64(k)+0.5)/float64(pairs-1))
		if k == pairs-1 {
			tb = 1
		}
		out = append(out, rb.at(tb))
	}
	// keep the exact original last point so the next stitch connects
	if last := seg[len(seg)-1]; out[len(out)-1] != last {
		if len(seg)%2 == 1 { // original ended on rail a
			out = append(out, last)
		} else {
			out[len(out)-1] = last
		}
	}
	return out, clamped
}

// ---- fill -----------------------------------------------------------------

// fillRows detects back-and-forth rows starting at i. A row is a maximal
// chain of segments pointing (roughly) along the same direction d; the next
// row runs along -d after a short turn. Returns the number of points used
// and the rows (each row's points in sewing order, excluding turn points
// that belong to the next row).
func fillRows(run []pt, i int) (int, [][]pt) {
	if i+2 >= len(run) {
		return 0, nil
	}
	// row direction from the first long segment
	var d pt
	for k := i + 1; k < len(run) && k < i+6; k++ {
		s := run[k].sub(run[k-1])
		if s.len() >= 15 {
			d = s.mul(1 / s.len())
			break
		}
	}
	if d == (pt{}) {
		return 0, nil
	}
	norm := pt{-d.y, d.x}
	var rows [][]pt
	cur := []pt{run[i]}
	sign := 0.0
	k := i + 1
	for ; k < len(run); k++ {
		s := run[k].sub(run[k-1])
		l := s.len()
		along := s.dot(d)
		across := math.Abs(s.dot(norm))
		if l < 1e-9 {
			continue
		}
		switch {
		case math.Abs(along) >= 0.85*l: // along the row axis
			sg := math.Copysign(1, along)
			if sign == 0 {
				sign = sg
			}
			if sg != sign { // direction flipped: new row
				if len(cur) < 2 {
					goto done
				}
				rows = append(rows, cur)
				cur = []pt{run[k-1]}
				sign = sg
			}
			cur = append(cur, run[k])
		case across <= 15 && l <= 20: // short step to the next row
			if len(cur) < 2 {
				goto done
			}
			rows = append(rows, cur)
			cur = []pt{run[k]}
			sign = -sign
		default:
			goto done
		}
	}
done:
	if len(cur) >= 2 {
		rows = append(rows, cur)
	}
	if len(rows) < 4 {
		return 0, nil
	}
	// rows must step consistently sideways (a real fill, not a back-and-forth run)
	off := func(r []pt) float64 { return r[0].dot(norm) }
	dir := math.Copysign(1, off(rows[1])-off(rows[0]))
	used := 0
	for r := 1; r < len(rows); r++ {
		step := (off(rows[r]) - off(rows[r-1])) * dir
		if step <= 0.5 || step > 30 {
			rows = rows[:r]
			break
		}
	}
	if len(rows) < 4 {
		return 0, nil
	}
	for _, r := range rows {
		used += len(r)
	}
	// rows that start with the previous row's end point share it
	for r := 1; r < len(rows); r++ {
		if rows[r][0] == rows[r-1][len(rows[r-1])-1] {
			used--
		}
	}
	return used, rows
}

func rowSpacing(rows [][]pt) float64 {
	if len(rows) < 2 {
		return 0
	}
	d := rows[0][len(rows[0])-1].sub(rows[0][0])
	if d.len() == 0 {
		return 0
	}
	n := pt{-d.y / d.len(), d.x / d.len()}
	first, last := rows[0][0].dot(n), rows[len(rows)-1][0].dot(n)
	return math.Abs(last-first) / float64(len(rows)-1)
}

func stitchLen(rows [][]pt) float64 {
	var sum float64
	var c int
	for _, r := range rows {
		for k := 1; k < len(r); k++ {
			sum += r[k].sub(r[k-1]).len()
			c++
		}
	}
	if c == 0 {
		return 30
	}
	return sum / float64(c)
}

// refill interpolates new rows between the originals at the new spacing.
func refill(rows [][]pt, spacing float64, o Options) ([]pt, bool) {
	newSp := spacing / o.Factor
	clamped := false
	if newSp < o.MinSpacing {
		newSp, clamped = o.MinSpacing, true
	}
	sl := stitchLen(rows)
	// normalise every row to run in the first row's direction
	norm := make([]rail, len(rows))
	for r, row := range rows {
		p := row
		if r%2 == 1 {
			p = make([]pt, len(row))
			for k := range row {
				p[k] = row[len(row)-1-k]
			}
		}
		norm[r] = newRail(p)
	}
	total := float64(len(rows)-1) * spacing
	count := int(math.Round(total/newSp)) + 1
	if count < 2 {
		count = 2
	}
	var out []pt
	for k := 0; k < count; k++ {
		f := float64(k) / float64(count-1) * float64(len(rows)-1)
		r0 := int(math.Floor(f))
		if r0 >= len(rows)-1 {
			r0 = len(rows) - 2
		}
		t := f - float64(r0)
		a, b := norm[r0], norm[r0+1]
		start := lerp(a.at(0), b.at(0), t)
		end := lerp(a.at(1), b.at(1), t)
		length := end.sub(start).len()
		n := int(math.Ceil(length / sl))
		if n < 1 {
			n = 1
		}
		// brick offset: shift interior penetrations by a third per row
		phase := float64(k%3) / 3
		row := []pt{start}
		for s := 1; s < n; s++ {
			u := (float64(s) - 0.5 + phase) / float64(n)
			if u <= 0 || u >= 1 {
				continue
			}
			row = append(row, lerp(a.at(u), b.at(u), t))
		}
		row = append(row, end)
		if k%2 == 1 {
			for l, m := 0, len(row)-1; l < m; l, m = l+1, m-1 {
				row[l], row[m] = row[m], row[l]
			}
		}
		out = append(out, row...)
	}
	// finish where the original finished so the next stitch still connects
	last := rows[len(rows)-1][len(rows[len(rows)-1])-1]
	if out[len(out)-1] != last {
		out = append(out, last)
	}
	return out, clamped
}
