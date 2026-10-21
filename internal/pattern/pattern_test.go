package pattern

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestTransform(t *testing.T) {
	sts := []St{{X: 0, Y: 0, Cmd: Stitch}, {X: 100, Y: 0, Cmd: Stitch}}
	tr := Identity(50, 0)
	tr.Rotation = 90
	out := tr.Apply(sts, 50, 0)
	if !near(out[0].X, 50) || !near(out[0].Y, -50) || !near(out[1].X, 50) || !near(out[1].Y, 50) {
		t.Fatalf("rotate: %+v", out)
	}
	tr = Identity(0, 0)
	tr.ScaleX, tr.ScaleY, tr.FlipH = 2, 2, true
	out = tr.Apply(sts, 50, 0)
	if !near(out[0].X, 100) || !near(out[1].X, -100) {
		t.Fatalf("scale/flip: %+v", out)
	}
}

func TestCollapseJumps(t *testing.T) {
	sts := []St{
		{X: 0, Y: 0, Cmd: Stitch},
		{X: 1, Y: 1, Cmd: Jump}, {X: 2, Y: 2, Cmd: Jump}, {X: 3, Y: 3, Cmd: Jump},
		{X: 3, Y: 3, Cmd: Stitch},
		{X: 9, Y: 9, Cmd: Jump},
		{X: 9, Y: 9, Cmd: Stitch},
	}
	out := CollapseJumps(sts, 3)
	c := Count(out)
	if c.Trims != 1 || c.Jumps != 2 || c.Stitches != 3 {
		t.Fatalf("%+v", c)
	}
}

func TestRemoveSmallAndReverse(t *testing.T) {
	sts := []St{{X: 0, Cmd: Stitch}, {X: 1, Cmd: Stitch}, {X: 2, Cmd: Stitch}, {X: 40, Cmd: Stitch}, {X: 41, Cmd: Stitch}}
	out, n := RemoveSmallStitches(sts, 3)
	if n != 2 || len(out) != 3 || out[len(out)-1].X != 41 {
		t.Fatalf("n=%d %+v", n, out)
	}
	r := Reverse(sts)
	if r[0].Cmd != Jump || r[0].X != 41 || r[len(r)-1].X != 0 {
		t.Fatalf("reverse %+v", r)
	}
}

func TestSplitBlocks(t *testing.T) {
	sts := []St{{Cmd: ColorChange}, {X: 1, Cmd: Stitch}, {Cmd: ColorChange}, {X: 2, Cmd: Stitch}, {Cmd: End}}
	if b := SplitBlocks(sts); len(b) != 2 {
		t.Fatalf("%d", len(b))
	}
}
