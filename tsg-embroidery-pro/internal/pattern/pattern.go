// Package pattern is the in-memory stitch model shared by every reader,
// writer and editing operation.
//
// Units are 0.1 mm (DST native). The Y axis points DOWN (screen space);
// format readers/writers convert at the boundary.
package pattern

import (
	"math"
)

// Cmd is the kind of needle/frame action a record represents.
type Cmd uint8

const (
	Stitch      Cmd = iota // needle penetration at X,Y
	Jump                   // frame move to X,Y without stitching
	Trim                   // cut thread at the current position (X,Y ignored)
	ColorChange            // advance to the next needle (X,Y ignored)
	Stop                   // machine pause (X,Y ignored)
	End                    // end of design
)

func (c Cmd) String() string {
	switch c {
	case Stitch:
		return "stitch"
	case Jump:
		return "jump"
	case Trim:
		return "trim"
	case ColorChange:
		return "color"
	case Stop:
		return "stop"
	case End:
		return "end"
	}
	return "unknown"
}

// St is one absolute-positioned record.
type St struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Cmd Cmd     `json:"c"`
}

// Pattern is a flat stitch list, possibly containing ColorChange markers.
type Pattern struct {
	Label    string
	Stitches []St
}

// Rect is an axis-aligned bounding box.
type Rect struct {
	MinX, MinY, MaxX, MaxY float64
}

func (r Rect) Width() float64  { return r.MaxX - r.MinX }
func (r Rect) Height() float64 { return r.MaxY - r.MinY }
func (r Rect) CenterX() float64 {
	return (r.MinX + r.MaxX) / 2
}
func (r Rect) CenterY() float64 {
	return (r.MinY + r.MaxY) / 2
}

// Empty reports whether the rect has never been extended.
func (r Rect) Empty() bool { return r.MinX > r.MaxX }

// EmptyRect returns a rect ready to be extended.
func EmptyRect() Rect {
	return Rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
}

// Add extends the rect to include x,y.
func (r *Rect) Add(x, y float64) {
	r.MinX = math.Min(r.MinX, x)
	r.MinY = math.Min(r.MinY, y)
	r.MaxX = math.Max(r.MaxX, x)
	r.MaxY = math.Max(r.MaxY, y)
}

// Union extends r by o.
func (r *Rect) Union(o Rect) {
	if o.Empty() {
		return
	}
	r.Add(o.MinX, o.MinY)
	r.Add(o.MaxX, o.MaxY)
}

// Bounds returns the extent of all stitch (needle) positions.
// Jumps are excluded so a stray travel move doesn't inflate the size.
func Bounds(sts []St) Rect {
	r := EmptyRect()
	for _, s := range sts {
		if s.Cmd == Stitch {
			r.Add(s.X, s.Y)
		}
	}
	if r.Empty() { // jump-only data: fall back to every positioned record
		for _, s := range sts {
			if s.Cmd == Jump {
				r.Add(s.X, s.Y)
			}
		}
	}
	return r
}

// Stats summarises a stitch list.
type Stats struct {
	Stitches     int `json:"stitches"`
	Jumps        int `json:"jumps"`
	Trims        int `json:"trims"`
	ColorChanges int `json:"colorChanges"`
	Stops        int `json:"stops"`
}

func Count(sts []St) Stats {
	var s Stats
	for _, st := range sts {
		switch st.Cmd {
		case Stitch:
			s.Stitches++
		case Jump:
			s.Jumps++
		case Trim:
			s.Trims++
		case ColorChange:
			s.ColorChanges++
		case Stop:
			s.Stops++
		}
	}
	return s
}

// SplitBlocks splits a flat list at ColorChange records. The ColorChange and
// End records themselves are dropped; every block keeps its jumps and trims.
// Empty blocks (e.g. a leading color change) are discarded.
func SplitBlocks(sts []St) [][]St {
	var blocks [][]St
	cur := []St{}
	flush := func() {
		if Count(cur).Stitches > 0 {
			blocks = append(blocks, cur)
		}
		cur = []St{}
	}
	for _, s := range sts {
		switch s.Cmd {
		case ColorChange:
			flush()
		case End:
			// ignore
		default:
			cur = append(cur, s)
		}
	}
	flush()
	return blocks
}

// CollapseJumps rewrites runs of consecutive jumps as a single jump to the
// run's final position, preceded by a Trim when the run is at least
// trimJumps long (Tajima machines trim on N consecutive jump codes).
// This makes trims survive transforms and lets the writer re-split moves.
func CollapseJumps(sts []St, trimJumps int) []St {
	out := make([]St, 0, len(sts))
	for i := 0; i < len(sts); {
		if sts[i].Cmd != Jump {
			out = append(out, sts[i])
			i++
			continue
		}
		j := i
		for j < len(sts) && sts[j].Cmd == Jump {
			j++
		}
		run := j - i
		if trimJumps > 0 && run >= trimJumps {
			out = append(out, St{Cmd: Trim})
		}
		last := sts[j-1]
		out = append(out, St{X: last.X, Y: last.Y, Cmd: Jump})
		i = j
	}
	return out
}

// Transform places a design on the canvas. It is applied about the design's
// own original centre: flip -> scale -> rotate -> translate to (X,Y).
type Transform struct {
	X        float64 `json:"x"` // canvas position of design centre (0.1 mm)
	Y        float64 `json:"y"`
	ScaleX   float64 `json:"scaleX"`
	ScaleY   float64 `json:"scaleY"`
	Rotation float64 `json:"rotation"` // degrees, clockwise on screen
	FlipH    bool    `json:"flipH"`
	FlipV    bool    `json:"flipV"`
}

// Identity returns a transform that leaves a design centred at cx,cy.
func Identity(cx, cy float64) Transform {
	return Transform{X: cx, Y: cy, ScaleX: 1, ScaleY: 1}
}

// Apply transforms sts (whose original centre is cx,cy) into a new slice.
func (t Transform) Apply(sts []St, cx, cy float64) []St {
	rad := t.Rotation * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	sx, sy := t.ScaleX, t.ScaleY
	if sx == 0 {
		sx = 1
	}
	if sy == 0 {
		sy = 1
	}
	if t.FlipH {
		sx = -sx
	}
	if t.FlipV {
		sy = -sy
	}
	out := make([]St, len(sts))
	for i, s := range sts {
		x := (s.X - cx) * sx
		y := (s.Y - cy) * sy
		out[i] = St{
			X:   x*cos - y*sin + t.X,
			Y:   x*sin + y*cos + t.Y,
			Cmd: s.Cmd,
		}
	}
	return out
}

// Reverse returns the block sewn in the opposite direction. Leading jumps
// (the travel into the block) stay at the front, pointed at the new start.
func Reverse(block []St) []St {
	var needle []St
	for _, s := range block {
		if s.Cmd == Stitch {
			needle = append(needle, s)
		}
	}
	if len(needle) == 0 {
		return append([]St(nil), block...)
	}
	out := make([]St, 0, len(needle)+2)
	start := needle[len(needle)-1]
	out = append(out, St{X: start.X, Y: start.Y, Cmd: Jump})
	for i := len(needle) - 1; i >= 0; i-- {
		out = append(out, needle[i])
	}
	return out
}

// RemoveSmallStitches drops needle points closer than minLen to the previous
// needle point. The first and last stitch of every run are always kept so
// tie-ins and block ends stay intact. Returns the new list and removed count.
func RemoveSmallStitches(sts []St, minLen float64) ([]St, int) {
	out := make([]St, 0, len(sts))
	removed := 0
	havePrev := false
	var px, py float64
	for i, s := range sts {
		if s.Cmd != Stitch {
			out = append(out, s)
			if s.Cmd == Jump {
				px, py, havePrev = s.X, s.Y, true
			}
			continue
		}
		lastOfRun := i+1 >= len(sts) || sts[i+1].Cmd != Stitch
		if havePrev && !lastOfRun && math.Hypot(s.X-px, s.Y-py) < minLen {
			removed++
			continue
		}
		out = append(out, s)
		px, py, havePrev = s.X, s.Y, true
	}
	return out, removed
}

// LongestStitch returns the longest needle-to-needle distance in the list.
func LongestStitch(sts []St) float64 {
	var longest, px, py float64
	have := false
	for _, s := range sts {
		switch s.Cmd {
		case Stitch:
			if have {
				longest = math.Max(longest, math.Hypot(s.X-px, s.Y-py))
			}
			px, py, have = s.X, s.Y, true
		case Jump:
			px, py, have = s.X, s.Y, true
		}
	}
	return longest
}
