package document

import (
	"math"
	"path/filepath"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// View is the full UI state sent to the frontend after every change.
type View struct {
	Title    string               `json:"title"`
	Path     string               `json:"path"`
	Dirty    bool                 `json:"dirty"`
	CanUndo  bool                 `json:"canUndo"`
	CanRedo  bool                 `json:"canRedo"`
	Designs  []DesignView         `json:"designs"`
	Threads  []Thread             `json:"threads"`
	Settings Settings             `json:"settings"`
	Hoop     Hoop                 `json:"hoop"`
	Hoops    []Hoop               `json:"hoops"`
	Summary  Summary              `json:"summary"`
	Fonts    []lettering.FontInfo `json:"fonts"`
	Sequence []SeqEntry           `json:"sequence"` // needle sequence for the operator
	Tabs     []TabInfo            `json:"tabs"`     // open documents (filled in by the app)
	TabID    string               `json:"tabId"`    // the active one
}

// TabInfo describes one open document for the tab strip.
type TabInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Dirty bool   `json:"dirty"`
}

type DesignView struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Visible   bool              `json:"visible"`
	Stitches  int               `json:"stitches"`
	Bounds    pattern.Rect      `json:"bounds"`
	Transform pattern.Transform `json:"transform"`
	ScalePct  float64           `json:"scalePct"`
	Lettering *lettering.Spec   `json:"lettering,omitempty"`
	Blocks    []BlockView       `json:"blocks"`
	Source    string            `json:"source,omitempty"`
}

type BlockView struct {
	Index    int       `json:"index"` // global, 1-based sequence number
	Thread   string    `json:"thread"`
	Stitches int       `json:"stitches"`
	Points   []float32 `json:"points"` // transformed x,y,cmd triples
}

type Summary struct {
	Width        float64      `json:"width"`  // mm
	Height       float64      `json:"height"` // mm
	Stitches     int          `json:"stitches"`
	ColorChanges int          `json:"colorChanges"`
	Trims        int          `json:"trims"`
	Jumps        int          `json:"jumps"`
	SewMinutes   float64      `json:"sewMinutes"`
	WithinHoop   bool         `json:"withinHoop"`
	Bounds       pattern.Rect `json:"bounds"`
	Blocks       int          `json:"blocks"`
	Designs      int          `json:"designs"`
	Longest      float64      `json:"longest"` // mm
}

type SeqEntry struct {
	Step   int    `json:"step"`
	Needle int    `json:"needle"`
	Thread string `json:"thread"`
	Stops  int    `json:"stitches"`
}

// State builds the current View.
func (e *Editor) State() View {
	e.mu.Lock()
	doc, path, dirty := e.doc, e.path, e.dirtyLocked()
	canU, canR := len(e.undo) > 0, len(e.redo) > 0
	e.mu.Unlock()

	v := View{
		Title: doc.Name, Path: path, Dirty: dirty, CanUndo: canU, CanRedo: canR,
		Settings: doc.Settings, Hoop: HoopByID(doc.Settings.Hoop),
		Hoops: Hoops, Fonts: e.Fonts.List(),
		// never send JSON null for lists: the UI expects arrays
		Designs: []DesignView{}, Threads: append([]Thread{}, doc.Threads...), Sequence: []SeqEntry{},
	}
	if path != "" {
		v.Title = filepath.Base(path)
	}
	idx := 1
	all := pattern.EmptyRect()
	for _, ds := range doc.Designs {
		dv := DesignView{
			ID: ds.ID, Name: ds.Name, Kind: ds.Kind, Visible: ds.Visible, Transform: ds.T,
			Lettering: ds.Lettering, Source: ds.Source, Bounds: ds.Bounds(),
			ScalePct: math.Round(ds.T.ScaleY*1000) / 10,
			Blocks:   []BlockView{},
		}
		for i, b := range ds.Blocks {
			placed := ds.Placed(i)
			pts := make([]float32, 0, len(placed)*3)
			for _, s := range placed {
				pts = append(pts, float32(s.X), float32(s.Y), float32(s.Cmd))
			}
			n := pattern.Count(b.Stitches).Stitches
			dv.Stitches += n
			dv.Blocks = append(dv.Blocks, BlockView{Index: idx, Thread: b.Thread, Stitches: n, Points: pts})
			idx++
		}
		if ds.Visible {
			all.Union(dv.Bounds)
			v.Summary.Designs++
			v.Summary.Blocks += len(ds.Blocks)
		}
		v.Designs = append(v.Designs, dv)
	}
	asm := doc.Assemble()
	c := pattern.Count(asm)
	v.Summary.Stitches = c.Stitches
	v.Summary.ColorChanges = c.ColorChanges
	v.Summary.Trims = c.Trims
	v.Summary.Jumps = c.Jumps
	v.Summary.Longest = math.Round(pattern.LongestStitch(asm)) / 10
	if !all.Empty() {
		v.Summary.Bounds = all
		v.Summary.Width = math.Round(all.Width()) / 10
		v.Summary.Height = math.Round(all.Height()) / 10
	}
	v.Summary.WithinHoop = v.Hoop.Contains(all)
	if !v.Summary.WithinHoop && v.Hoop.Shape == "round" {
		// bbox corners are conservative for round hoops; check real needle points
		v.Summary.WithinHoop = true
		r2 := (v.Hoop.W / 2) * (v.Hoop.W / 2)
		for _, ds := range doc.Designs {
			if !ds.Visible {
				continue
			}
			for i := range ds.Blocks {
				for _, s := range ds.Placed(i) {
					if s.Cmd == pattern.Stitch && s.X*s.X+s.Y*s.Y > r2 {
						v.Summary.WithinHoop = false
					}
				}
			}
		}
	}
	spm := float64(max(doc.Settings.SPM, 1))
	v.Summary.SewMinutes = math.Round((float64(c.Stitches)/spm+
		(float64(c.Trims)*doc.Settings.TrimSeconds+float64(c.ColorChanges)*doc.Settings.ColorSeconds)/60)*10) / 10

	// operator needle sequence (consecutive same-needle blocks collapse)
	step := 0
	last := -1
	for _, ds := range doc.Designs {
		if !ds.Visible {
			continue
		}
		for _, b := range ds.Blocks {
			t, _ := doc.Thread(b.Thread)
			n := pattern.Count(b.Stitches).Stitches
			if t.Needle == last && len(v.Sequence) > 0 {
				v.Sequence[len(v.Sequence)-1].Stops += n
				continue
			}
			step++
			v.Sequence = append(v.Sequence, SeqEntry{Step: step, Needle: t.Needle, Thread: t.Name, Stops: n})
			last = t.Needle
		}
	}
	return v
}
