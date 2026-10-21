package density

import (
	"math"
	"testing"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
	sg "github.com/phpandrew/TSG-Embroidery-Pro/internal/stitchgen"
)

func needle(sts []pattern.St) []pattern.St {
	var out []pattern.St
	for _, s := range sts {
		if s.Cmd == pattern.Stitch {
			out = append(out, s)
		}
	}
	return out
}

func TestSatinDensity(t *testing.T) {
	col := sg.Satin([]sg.Pt{{X: 0, Y: 0}, {X: 400, Y: 0}}, sg.SatinOptions{Width: 40, Spacing: 4})
	b := pattern.Bounds(col)
	for _, f := range []float64{1.5, 0.7} {
		out, rep := Adjust(col, Options{Factor: f})
		if rep.Satins != 1 {
			t.Fatalf("f=%v satins=%d", f, rep.Satins)
		}
		ratio := float64(rep.After) / float64(rep.Before)
		if math.Abs(ratio-f) > 0.12 {
			t.Fatalf("f=%v stitch ratio %.2f", f, ratio)
		}
		ob := pattern.Bounds(out)
		if math.Abs(ob.Width()-b.Width()) > 1 || math.Abs(ob.Height()-b.Height()) > 1 {
			t.Fatalf("shape changed: %+v vs %+v", ob, b)
		}
	}
}

func TestFillDensity(t *testing.T) {
	sq := []sg.Pt{{X: 0, Y: 0}, {X: 300, Y: 0}, {X: 300, Y: 300}, {X: 0, Y: 300}}
	fill := needle(sg.Fill(sq, sg.FillOptions{Angle: 0, Spacing: 4}))
	b := pattern.Bounds(fill)
	out, rep := Adjust(fill, Options{Factor: 0.5})
	if rep.Fills != 1 {
		t.Fatalf("fills=%d satins=%d untouched=%d", rep.Fills, rep.Satins, rep.Untouched)
	}
	ratio := float64(rep.After) / float64(rep.Before)
	if math.Abs(ratio-0.5) > 0.12 {
		t.Fatalf("stitch ratio %.2f", ratio)
	}
	ob := pattern.Bounds(out)
	if math.Abs(ob.Width()-b.Width()) > 2 || math.Abs(ob.Height()-b.Height()) > 2 {
		t.Fatalf("fill area changed: %+v vs %+v", ob, b)
	}
	out2, rep2 := Adjust(fill, Options{Factor: 1.25})
	if rep2.Fills != 1 || float64(rep2.After)/float64(rep2.Before) < 1.1 {
		t.Fatalf("denser fill: %+v", rep2)
	}
	_ = out2
}

func TestRunningStitchUntouched(t *testing.T) {
	run := needle(sg.Run([]sg.Pt{{X: 0, Y: 0}, {X: 500, Y: 100}, {X: 200, Y: 400}}, 25))
	out, rep := Adjust(run, Options{Factor: 1.5})
	if rep.Satins+rep.Fills != 0 || len(out) != len(run) {
		t.Fatalf("running stitch changed: %+v", rep)
	}
}

func TestClampAndCommandsPreserved(t *testing.T) {
	col := sg.Satin([]sg.Pt{{X: 0, Y: 0}, {X: 200, Y: 0}}, sg.SatinOptions{Width: 40, Spacing: 2})
	in := append([]pattern.St{{X: 0, Y: 0, Cmd: pattern.Jump}}, col...)
	in = append(in, pattern.St{Cmd: pattern.Trim}, pattern.St{X: 500, Y: 0, Cmd: pattern.Jump})
	out, rep := Adjust(in, Options{Factor: 3})
	if !rep.Clamped {
		t.Fatal("expected clamp at minimum spacing")
	}
	c := pattern.Count(out)
	if c.Trims != 1 || c.Jumps != 2 {
		t.Fatalf("commands lost: %+v", c)
	}
}
