package document

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/density"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/lettering"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

var errNoDesign = errors.New("design not found")

func (e *Editor) withDesign(id string, fn func(d *Doc, ds *Design) error) error {
	return e.mutate(func(d *Doc) error {
		ds, _ := d.Design(id)
		if ds == nil {
			return errNoDesign
		}
		return fn(d, ds)
	})
}

// MoveTo places the design centre at x,y (0.1 mm).
func (e *Editor) MoveTo(id string, x, y float64) error {
	return e.withDesign(id, func(_ *Doc, ds *Design) error {
		b := ds.Bounds()
		ds.T.X += x - b.CenterX()
		ds.T.Y += y - b.CenterY()
		return nil
	})
}

// MoveBy nudges designs by dx,dy (0.1 mm). Empty ids = all designs.
func (e *Editor) MoveBy(ids []string, dx, dy float64) error {
	return e.mutate(func(d *Doc) error {
		for _, ds := range targets(d, ids) {
			ds.T.X += dx
			ds.T.Y += dy
		}
		return nil
	})
}

func targets(d *Doc, ids []string) []*Design {
	if len(ids) == 0 {
		return d.Designs
	}
	var out []*Design
	for _, id := range ids {
		if ds, _ := d.Design(id); ds != nil {
			out = append(out, ds)
		}
	}
	return out
}

// Rotate adds deg degrees (clockwise) about the design centre.
func (e *Editor) Rotate(id string, deg float64) error {
	return e.withDesign(id, func(_ *Doc, ds *Design) error {
		ds.T.Rotation = normDeg(ds.T.Rotation + deg)
		return nil
	})
}

// SetRotation sets an absolute rotation.
func (e *Editor) SetRotation(id string, deg float64) error {
	return e.withDesign(id, func(_ *Doc, ds *Design) error {
		ds.T.Rotation = normDeg(deg)
		return nil
	})
}

func normDeg(v float64) float64 {
	v = math.Mod(v, 360)
	if v < 0 {
		v += 360
	}
	return math.Round(v*100) / 100
}

// Mirror flips horizontally (or vertically when vertical is true).
func (e *Editor) Mirror(id string, vertical bool) error {
	return e.withDesign(id, func(_ *Doc, ds *Design) error {
		if vertical {
			ds.T.FlipV = !ds.T.FlipV
		} else {
			ds.T.FlipH = !ds.T.FlipH
		}
		return nil
	})
}

// Center centres one design in the hoop, or the whole layout when id == "".
func (e *Editor) Center(id string) error {
	return e.mutate(func(d *Doc) error {
		var ts []*Design
		if id == "" {
			ts = d.Designs
		} else if ds, _ := d.Design(id); ds != nil {
			ts = []*Design{ds}
		} else {
			return errNoDesign
		}
		r := pattern.EmptyRect()
		for _, ds := range ts {
			r.Union(ds.Bounds())
		}
		if r.Empty() {
			return nil
		}
		dx, dy := -r.CenterX(), -r.CenterY()
		for _, ds := range ts {
			ds.T.X += dx
			ds.T.Y += dy
		}
		return nil
	})
}

// SetScale sets uniform scale in percent. Lettering is regenerated at the new
// size instead of being stretched.
func (e *Editor) SetScale(id string, pct float64) error {
	if pct < 10 || pct > 1000 {
		return errors.New("scale must be 10–1000%")
	}
	return e.withDesign(id, func(d *Doc, ds *Design) error {
		if ds.Kind == KindLettering {
			cur := ds.Lettering.Height
			return e.relayout(d, ds, func(s *lettering.Spec) { s.Height = cur * pct / 100 / currentScale(ds) })
		}
		k := pct / 100
		ds.T.ScaleX, ds.T.ScaleY = k, k
		return nil
	})
}

func currentScale(ds *Design) float64 {
	if ds.Kind == KindLettering {
		return 1
	}
	return ds.T.ScaleY
}

// SetSize sets the transformed width/height in 0.1 mm. When lock is set the
// aspect ratio is kept (h is ignored if w > 0).
func (e *Editor) SetSize(id string, w, h float64, lock bool) error {
	return e.withDesign(id, func(_ *Doc, ds *Design) error {
		return e.resize(ds, w, h, lock)
	})
}

// SetBox resizes and positions a design in one undo step (canvas handles).
func (e *Editor) SetBox(id string, cx, cy, w, h float64, lock bool) error {
	return e.withDesign(id, func(_ *Doc, ds *Design) error {
		if err := e.resize(ds, w, h, lock); err != nil {
			return err
		}
		b := ds.Bounds()
		ds.T.X += cx - b.CenterX()
		ds.T.Y += cy - b.CenterY()
		return nil
	})
}

func (e *Editor) resize(ds *Design, w, h float64, lock bool) error {
	b := ds.Bounds()
	if b.Empty() || b.Width() <= 0 || b.Height() <= 0 {
		return errors.New("design has no size")
	}
	fx, fy := 1.0, 1.0
	if w > 0 {
		fx = w / b.Width()
	}
	if h > 0 {
		fy = h / b.Height()
	}
	if lock {
		if w > 0 {
			fy = fx
		} else {
			fx = fy
		}
	}
	if fx <= 0.05 || fy <= 0.05 || fx > 20 || fy > 20 {
		return errors.New("size out of range")
	}
	if ds.Kind == KindLettering {
		return e.relayout(nil, ds, func(s *lettering.Spec) {
			s.Height *= fy
			s.Spacing *= fx
		})
	}
	// scale factors live in the design's own (unrotated) frame
	ds.T.ScaleX *= fx
	ds.T.ScaleY *= fy
	return nil
}

// SetVisible shows or hides a design (hidden designs are not exported).
func (e *Editor) SetVisible(id string, v bool) error {
	return e.withDesign(id, func(_ *Doc, ds *Design) error { ds.Visible = v; return nil })
}

// Rename changes a design's display name.
func (e *Editor) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name required")
	}
	return e.withDesign(id, func(_ *Doc, ds *Design) error { ds.Name = name; return nil })
}

// Delete removes a design.
func (e *Editor) Delete(id string) error {
	return e.mutate(func(d *Doc) error {
		_, i := d.Design(id)
		if i < 0 {
			return errNoDesign
		}
		d.Designs = append(d.Designs[:i], d.Designs[i+1:]...)
		return nil
	})
}

// Duplicate copies a design, offset slightly.
func (e *Editor) Duplicate(id string) (string, error) {
	var nid string
	err := e.withDesign(id, func(d *Doc, ds *Design) error {
		cp := *ds
		cp.ID = d.newID("d")
		cp.Name = ds.Name + " copy"
		cp.Blocks = append([]Block(nil), ds.Blocks...)
		if ds.Lettering != nil {
			l := *ds.Lettering
			cp.Lettering = &l
		}
		cp.T.X += 50
		cp.T.Y += 50
		d.Designs = append(d.Designs, &cp)
		nid = cp.ID
		return nil
	})
	return nid, err
}

// MoveDesign shifts a design up (-1) or down (+1) in the sew sequence.
func (e *Editor) MoveDesign(id string, delta int) error {
	return e.mutate(func(d *Doc) error {
		_, i := d.Design(id)
		if i < 0 {
			return errNoDesign
		}
		j := i + delta
		if j < 0 || j >= len(d.Designs) {
			return nil
		}
		d.Designs[i], d.Designs[j] = d.Designs[j], d.Designs[i]
		return nil
	})
}

// MoveBlock reorders colour blocks within a design.
func (e *Editor) MoveBlock(id string, from, to int) error {
	return e.withDesign(id, func(_ *Doc, ds *Design) error {
		n := len(ds.Blocks)
		if from < 0 || from >= n || to < 0 || to >= n {
			return errors.New("block index out of range")
		}
		b := ds.Blocks[from]
		bs := append(ds.Blocks[:from:from], ds.Blocks[from+1:]...)
		bs = append(bs[:to], append([]Block{b}, bs[to:]...)...)
		ds.Blocks = bs
		return nil
	})
}

// SetBlockThread assigns a palette thread to a block.
func (e *Editor) SetBlockThread(id string, block int, thread string) error {
	return e.withDesign(id, func(d *Doc, ds *Design) error {
		if block < 0 || block >= len(ds.Blocks) {
			return errors.New("block index out of range")
		}
		if _, ok := d.Thread(thread); !ok {
			return errors.New("thread not found")
		}
		ds.Blocks[block].Thread = thread
		return nil
	})
}

// ReverseBlock sews a block in the opposite direction.
func (e *Editor) ReverseBlock(id string, block int) error {
	return e.withDesign(id, func(_ *Doc, ds *Design) error {
		if block < 0 || block >= len(ds.Blocks) {
			return errors.New("block index out of range")
		}
		ds.Blocks[block].Stitches = pattern.Reverse(ds.Blocks[block].Stitches)
		return nil
	})
}

// CombineBlocks merges consecutive blocks of a design that use the same thread.
func (e *Editor) CombineBlocks(id string) (int, error) {
	merged := 0
	err := e.withDesign(id, func(_ *Doc, ds *Design) error {
		var out []Block
		for _, b := range ds.Blocks {
			if n := len(out); n > 0 && out[n-1].Thread == b.Thread {
				joined := append(append([]pattern.St(nil), out[n-1].Stitches...), pattern.St{Cmd: pattern.Trim})
				out[n-1].Stitches = append(joined, b.Stitches...)
				merged++
				continue
			}
			out = append(out, b)
		}
		ds.Blocks = out
		return nil
	})
	return merged, err
}

// RemoveSmallStitches strips stitches shorter than minLen (0.1 mm) from a
// design (all designs when id == ""). Returns the number removed.
func (e *Editor) RemoveSmallStitches(id string, minLen float64) (int, error) {
	if minLen <= 0 || minLen > 20 {
		return 0, errors.New("threshold must be 0.1–2.0 mm")
	}
	total := 0
	err := e.mutate(func(d *Doc) error {
		var ids []string
		if id != "" {
			ids = []string{id}
		}
		for _, ds := range targets(d, ids) {
			if ds.Kind == KindLettering {
				continue
			}
			for i := range ds.Blocks {
				nb, n := pattern.RemoveSmallStitches(ds.Blocks[i].Stitches, minLen)
				ds.Blocks[i].Stitches = nb
				total += n
			}
		}
		if total == 0 {
			return errors.New("no stitches below threshold")
		}
		return nil
	})
	return total, err
}

// SetSettings replaces machine/output settings after validation.
func (e *Editor) SetSettings(s Settings) error {
	switch {
	case s.SPM < 100 || s.SPM > 2000:
		return errors.New("SPM must be 100–2000")
	case s.TrimJumps < 1 || s.TrimJumps > 8:
		return errors.New("trim jumps must be 1–8")
	case s.TrimAbove < 0 || s.TrimAbove > 500:
		return errors.New("trim threshold must be 0–50 mm")
	case s.Needles < 1 || s.Needles > 20:
		return errors.New("needles must be 1–20")
	case s.StartAt != "design" && s.StartAt != "hoop":
		return errors.New("start point must be design or hoop")
	}
	s.Hoop = HoopByID(s.Hoop).ID
	return e.mutate(func(d *Doc) error { d.Settings = s; return nil })
}

// ---- lettering -----------------------------------------------------------

// AddLettering lays out text and adds it as a design below the current layout.
func (e *Editor) AddLettering(spec lettering.Spec) (string, string, error) {
	var id, warn string
	err := e.mutate(func(d *Doc) error {
		res, err := e.Fonts.Layout(spec)
		if err != nil {
			return err
		}
		warn = res.Warning
		if len(res.Missing) > 0 {
			warn = strings.TrimSpace(warn + " Missing glyphs: " + strings.Join(res.Missing, " "))
		}
		ds := &Design{
			ID: d.newID("d"), Name: letteringName(spec.Text), Kind: KindLettering, Visible: true,
			Blocks:    letteringBlocks(d, res, nil),
			Lettering: &spec,
		}
		ds.T = pattern.Identity(0, 0)
		// place under existing content
		all := pattern.EmptyRect()
		for _, o := range d.Designs {
			all.Union(o.Bounds())
		}
		if !all.Empty() {
			ds.T.Y = all.MaxY + 60 + spec.Height/2
		}
		d.Designs = append(d.Designs, ds)
		id = ds.ID
		return nil
	})
	return id, warn, err
}

// UpdateLettering changes text/font/size of a lettering design.
func (e *Editor) UpdateLettering(id string, spec lettering.Spec) (string, error) {
	var warn string
	err := e.mutateLive("lettering:"+id, func(d *Doc) error {
		ds, _ := d.Design(id)
		if ds == nil {
			return errNoDesign
		}
		if ds.Kind != KindLettering {
			return errors.New("not a lettering object")
		}
		res, err := e.Fonts.Layout(spec)
		if err != nil {
			return err
		}
		warn = res.Warning
		if len(res.Missing) > 0 {
			warn = strings.TrimSpace(warn + " Missing glyphs: " + strings.Join(res.Missing, " "))
		}
		ds.Blocks = letteringBlocks(d, res, ds.Blocks)
		ds.Lettering = &spec
		ds.Name = letteringName(spec.Text)
		return nil
	})
	return warn, err
}

// letteringBlocks turns a layout into design blocks, keeping the threads the
// user already picked for each colour and using palette order for new ones.
func letteringBlocks(d *Doc, res lettering.Result, old []Block) []Block {
	out := make([]Block, len(res.Blocks))
	for i, b := range res.Blocks {
		th := ""
		if i < len(old) {
			th = old[i].Thread
		}
		if d == nil {
			if th == "" && len(old) > 0 {
				th = old[len(old)-1].Thread
			}
		} else if _, ok := d.Thread(th); !ok {
			th = d.threadFor(i)
		}
		out[i] = Block{Thread: th, Stitches: b}
	}
	return out
}

func (e *Editor) relayout(d *Doc, ds *Design, change func(*lettering.Spec)) error {
	spec := *ds.Lettering
	change(&spec)
	if spec.Height < 30 || spec.Height > 3000 {
		return fmt.Errorf("lettering height must be 3–300 mm")
	}
	res, err := e.Fonts.Layout(spec)
	if err != nil {
		return err
	}
	ds.Blocks = letteringBlocks(d, res, ds.Blocks)
	ds.Lettering = &spec
	return nil
}

// DensityResult summarises a density change.
type DensityResult struct {
	Satins, Fills int
	Before, After int
	Clamped       bool
	Lettering     bool
}

// AdjustDensity rebuilds satins and fills of a design at factor × density
// (1.2 = 20% denser). Lettering is regenerated with a new satin spacing.
func (e *Editor) AdjustDensity(id string, factor float64) (DensityResult, error) {
	var res DensityResult
	if factor < 0.4 || factor > 2.5 {
		return res, errors.New("density must be 40–250%")
	}
	err := e.withDesign(id, func(d *Doc, ds *Design) error {
		if ds.Kind == KindLettering {
			res.Lettering = true
			before := 0
			for _, b := range ds.Blocks {
				before += pattern.Count(b.Stitches).Stitches
			}
			err := e.relayout(d, ds, func(s *lettering.Spec) {
				if f, ok := e.Fonts.Get(s.Font); ok && f.Native() > 0 {
					p := s.DensityPct
					if p <= 0 {
						p = 100
					}
					s.DensityPct = math.Max(40, math.Min(250, p*factor))
					return
				}
				d := s.Density
				if d <= 0 {
					d = 4
				}
				s.Density = math.Max(1.5, d/factor)
			})
			for _, b := range ds.Blocks {
				res.After += pattern.Count(b.Stitches).Stitches
			}
			res.Before = before
			return err
		}
		for i := range ds.Blocks {
			out, rep := density.Adjust(ds.Blocks[i].Stitches, density.Options{Factor: factor})
			ds.Blocks[i].Stitches = out
			res.Satins += rep.Satins
			res.Fills += rep.Fills
			res.Before += rep.Before
			res.After += rep.After
			res.Clamped = res.Clamped || rep.Clamped
		}
		if res.Satins+res.Fills == 0 {
			return errors.New("no satin or fill areas recognised in this design")
		}
		return nil
	})
	return res, err
}

// StartEnd returns the first and last needle points of a placed design.
func (ds *Design) StartEnd() (sx, sy, ex, ey float64, ok bool) {
	first := true
	for i := range ds.Blocks {
		for _, s := range ds.Placed(i) {
			if s.Cmd != pattern.Stitch {
				continue
			}
			if first {
				sx, sy, first = s.X, s.Y, false
			}
			ex, ey = s.X, s.Y
		}
	}
	return sx, sy, ex, ey, !first
}

// SnapToPrevious moves a design so its first stitch lands exactly on the
// last stitch of the visible design sewn before it: no jump, no trim.
func (e *Editor) SnapToPrevious(id string) error {
	return e.mutate(func(d *Doc) error {
		ds, i := d.Design(id)
		if ds == nil {
			return errNoDesign
		}
		var prev *Design
		for j := i - 1; j >= 0; j-- {
			if d.Designs[j].Visible {
				prev = d.Designs[j]
				break
			}
		}
		if prev == nil {
			return errors.New("this is the first design in the sequence; nothing to connect to")
		}
		_, _, ex, ey, ok1 := prev.StartEnd()
		sx, sy, _, _, ok2 := ds.StartEnd()
		if !ok1 || !ok2 {
			return errors.New("design has no stitches")
		}
		ds.T.X += ex - sx
		ds.T.Y += ey - sy
		return nil
	})
}

// letteringName is the design-list name for lettering: lines joined by " / ".
func letteringName(text string) string {
	return strings.Join(lettering.Lines(text), " / ")
}
