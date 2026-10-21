// Package document holds the editable multi-design document: designs,
// colour blocks, thread palette, machine settings, undo history, project
// files and DST export assembly.
package document

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// Thread is one entry in the document palette.
type Thread struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Brand  string `json:"brand"`
	Code   string `json:"code"`
	Color  string `json:"color"` // #rrggbb
	Needle int    `json:"needle"`
}

// Block is one colour block of a design. Stitch slices are treated as
// immutable once stored: edits always build a new slice, which lets undo
// snapshots share memory.
type Block struct {
	Thread   string       `json:"thread"`
	Stitches []pattern.St `json:"-"`
}

type blockJSON struct {
	Thread string    `json:"thread"`
	Points []float64 `json:"points"` // x,y,cmd triples
}

func (b Block) MarshalJSON() ([]byte, error) {
	pts := make([]float64, 0, len(b.Stitches)*3)
	for _, s := range b.Stitches {
		pts = append(pts, math.Round(s.X*100)/100, math.Round(s.Y*100)/100, float64(s.Cmd))
	}
	return json.Marshal(blockJSON{Thread: b.Thread, Points: pts})
}

func (b *Block) UnmarshalJSON(data []byte) error {
	var j blockJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	if len(j.Points)%3 != 0 {
		return fmt.Errorf("block points length %d not a multiple of 3", len(j.Points))
	}
	b.Thread = j.Thread
	b.Stitches = make([]pattern.St, 0, len(j.Points)/3)
	for i := 0; i < len(j.Points); i += 3 {
		c := pattern.Cmd(j.Points[i+2])
		if c > pattern.End {
			return fmt.Errorf("bad command %v", j.Points[i+2])
		}
		b.Stitches = append(b.Stitches, pattern.St{X: j.Points[i], Y: j.Points[i+1], Cmd: c})
	}
	return nil
}

// Kind of design.
const (
	KindFile      = "file"
	KindLettering = "lettering"
)

// Design is one placed item (an imported file or a lettering object).
type Design struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Source    string            `json:"source,omitempty"`
	Visible   bool              `json:"visible"`
	Blocks    []Block           `json:"blocks"`
	CX        float64           `json:"cx"` // original centre (transform pivot)
	CY        float64           `json:"cy"`
	T         pattern.Transform `json:"transform"`
	Lettering *lettering.Spec   `json:"lettering,omitempty"`
}

// Placed returns the block's stitches with the design transform applied.
func (d *Design) Placed(i int) []pattern.St {
	return d.T.Apply(d.Blocks[i].Stitches, d.CX, d.CY)
}

// Bounds returns the transformed extent of the design.
func (d *Design) Bounds() pattern.Rect {
	r := pattern.EmptyRect()
	for i := range d.Blocks {
		r.Union(pattern.Bounds(d.Placed(i)))
	}
	return r
}

// OrigBounds returns the untransformed extent.
func (d *Design) OrigBounds() pattern.Rect {
	r := pattern.EmptyRect()
	for _, b := range d.Blocks {
		r.Union(pattern.Bounds(b.Stitches))
	}
	return r
}

// Settings are machine and output preferences saved with the project.
type Settings struct {
	Machine      string  `json:"machine"`
	SPM          int     `json:"spm"`          // average stitches per minute
	TrimSeconds  float64 `json:"trimSeconds"`  // time cost per trim
	ColorSeconds float64 `json:"colorSeconds"` // time cost per colour change
	TrimJumps    int     `json:"trimJumps"`    // jump codes = one trim on the machine
	TrimAbove    float64 `json:"trimAbove"`    // connectors longer than this get a trim (0.1 mm)
	Hoop         string  `json:"hoop"`
	StartAt      string  `json:"startAt"`   // "design" (centre of design) or "hoop"
	MinStitch    float64 `json:"minStitch"` // small-stitch threshold (0.1 mm)
	Needles      int     `json:"needles"`
}

func DefaultSettings() Settings {
	return Settings{
		Machine:      "Tajima TFHX 1506",
		SPM:          800,
		TrimSeconds:  4,
		ColorSeconds: 6,
		TrimJumps:    3,
		TrimAbove:    30,
		Hoop:         "r200",
		StartAt:      "design",
		MinStitch:    3,
		Needles:      15,
	}
}

// Hoop is a sewing-field template centred on the canvas origin.
type Hoop struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Shape string  `json:"shape"` // round | rect
	W     float64 `json:"w"`     // 0.1 mm
	H     float64 `json:"h"`
}

var Hoops = []Hoop{
	{"r90", "90 mm round", "round", 900, 900},
	{"r120", "120 mm round", "round", 1200, 1200},
	{"r150", "150 mm round", "round", 1500, 1500},
	{"r180", "180 mm round", "round", 1800, 1800},
	{"r200", "200 mm round", "round", 2000, 2000},
	{"s240", "240 × 240 mm", "rect", 2400, 2400},
	{"s300", "300 × 300 mm", "rect", 3000, 3000},
	{"cap", "Cap 360 × 75 mm", "rect", 3600, 750},
	{"max", "360 × 500 mm field", "rect", 3600, 5000},
}

func HoopByID(id string) Hoop {
	for _, h := range Hoops {
		if h.ID == id {
			return h
		}
	}
	return Hoops[4]
}

// Contains reports whether all of r fits in the hoop (centred at 0,0).
func (h Hoop) Contains(r pattern.Rect) bool {
	if r.Empty() {
		return true
	}
	if h.Shape == "round" {
		rad := h.W / 2
		for _, c := range [][2]float64{{r.MinX, r.MinY}, {r.MaxX, r.MinY}, {r.MinX, r.MaxY}, {r.MaxX, r.MaxY}} {
			if math.Hypot(c[0], c[1]) > rad {
				return false
			}
		}
		return true
	}
	return r.MinX >= -h.W/2 && r.MaxX <= h.W/2 && r.MinY >= -h.H/2 && r.MaxY <= h.H/2
}

// Doc is the whole editable document.
type Doc struct {
	Name     string    `json:"name"`
	Designs  []*Design `json:"designs"`
	Threads  []Thread  `json:"threads"`
	Settings Settings  `json:"settings"`
	NextID   int       `json:"nextId"`
}

func NewDoc() *Doc {
	return &Doc{Name: "Untitled", Settings: DefaultSettings(), NextID: 1}
}

func (d *Doc) newID(prefix string) string {
	id := fmt.Sprintf("%s%d", prefix, d.NextID)
	d.NextID++
	return id
}

func (d *Doc) Design(id string) (*Design, int) {
	for i, ds := range d.Designs {
		if ds.ID == id {
			return ds, i
		}
	}
	return nil, -1
}

func (d *Doc) Thread(id string) (Thread, bool) {
	for _, t := range d.Threads {
		if t.ID == id {
			return t, true
		}
	}
	return Thread{}, false
}

// clone makes a snapshot. Stitch slices are shared (immutable by convention).
func (d *Doc) clone() *Doc {
	c := *d
	c.Threads = append([]Thread(nil), d.Threads...)
	c.Designs = make([]*Design, len(d.Designs))
	for i, ds := range d.Designs {
		cp := *ds
		cp.Blocks = append([]Block(nil), ds.Blocks...)
		if ds.Lettering != nil {
			l := *ds.Lettering
			cp.Lettering = &l
		}
		c.Designs[i] = &cp
	}
	return &c
}

// DefaultThreads is the starter palette; needle numbers follow list order.
var DefaultThreads = []Thread{
	{Name: "Navy", Color: "#1c2f5e"},
	{Name: "Gold", Color: "#e6a91f"},
	{Name: "White", Color: "#f4f4f2"},
	{Name: "Red", Color: "#c8202f"},
	{Name: "Black", Color: "#151515"},
	{Name: "Royal", Color: "#1f4fb4"},
	{Name: "Kelly Green", Color: "#178a3e"},
	{Name: "Orange", Color: "#f07b1c"},
	{Name: "Purple", Color: "#5b2c8f"},
	{Name: "Maroon", Color: "#6e1a2b"},
	{Name: "Silver", Color: "#a7a9ac"},
	{Name: "Sky Blue", Color: "#6fb1e6"},
	{Name: "Pink", Color: "#ef7fb0"},
	{Name: "Brown", Color: "#6b4226"},
	{Name: "Yellow", Color: "#f7d117"},
}
