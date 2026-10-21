package dst

import (
	"bytes"
	"math"
	"math/rand"
	"testing"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

func TestEncodeDecodeAllMoves(t *testing.T) {
	for x := -MaxMove; x <= MaxMove; x++ {
		for y := -MaxMove; y <= MaxMove; y++ {
			for _, k := range []recKind{recStitch, recJump} {
				r, err := encode(x, y, k)
				if err != nil {
					t.Fatalf("encode %d,%d: %v", x, y, err)
				}
				dx, dy := decode(r[0], r[1], r[2])
				if dx != x || dy != y {
					t.Fatalf("roundtrip %d,%d -> %d,%d", x, y, dx, dy)
				}
				isJump := r[2]&0b10000011 == 0b10000011
				if isJump != (k == recJump) {
					t.Fatalf("jump flag wrong for %d,%d", x, y)
				}
				if r[2]&0b11000011 == 0b11000011 {
					t.Fatalf("move %d,%d collides with color-change code", x, y)
				}
			}
		}
	}
	if _, err := encode(122, 0, recStitch); err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var in []pattern.St
	x, y := 0.0, 0.0
	in = append(in, pattern.St{X: 50, Y: 50, Cmd: pattern.Jump})
	x, y = 50, 50
	for i := 0; i < 2000; i++ {
		x += float64(rng.Intn(61) - 30)
		y += float64(rng.Intn(61) - 30)
		in = append(in, pattern.St{X: x, Y: y, Cmd: pattern.Stitch})
		if i == 1000 {
			in = append(in, pattern.St{Cmd: pattern.Trim})
			x += 600 // 60 mm jump, must be split
			in = append(in, pattern.St{X: x, Y: y, Cmd: pattern.Jump})
			in = append(in, pattern.St{Cmd: pattern.ColorChange})
		}
	}
	var buf bytes.Buffer
	if err := Write(&buf, in, WriteOptions{Label: "test", TrimJumps: 3}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() < HeaderSize || (buf.Len()-HeaderSize)%3 != 0 {
		t.Fatalf("bad size %d", buf.Len())
	}
	p, h, err := Read(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if h.Label != "test" {
		t.Fatalf("label %q", h.Label)
	}
	if h.HeaderInt("CO", -1) != 1 {
		t.Fatalf("CO = %v", h.Fields["CO"])
	}
	// needle positions must match exactly
	var want, got []pattern.St
	for _, s := range in {
		if s.Cmd == pattern.Stitch {
			want = append(want, s)
		}
	}
	for _, s := range p.Stitches {
		if s.Cmd == pattern.Stitch {
			got = append(got, s)
		}
	}
	if len(want) != len(got) {
		t.Fatalf("stitch count %d != %d", len(got), len(want))
	}
	for i := range want {
		if want[i].X != got[i].X || want[i].Y != got[i].Y {
			t.Fatalf("stitch %d: %v != %v", i, got[i], want[i])
		}
	}
	// the collapsed form should recover exactly one trim
	c := pattern.CollapseJumps(p.Stitches, 3)
	if n := pattern.Count(c).Trims; n != 1 {
		t.Fatalf("trims after collapse = %d", n)
	}
	if blocks := pattern.SplitBlocks(p.Stitches); len(blocks) != 2 {
		t.Fatalf("blocks = %d", len(blocks))
	}
}

func TestLongStitchIsSplitNotJumped(t *testing.T) {
	in := []pattern.St{{X: 0, Y: 0, Cmd: pattern.Stitch}, {X: 300, Y: 0, Cmd: pattern.Stitch}}
	var buf bytes.Buffer
	if err := Write(&buf, in, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	p, _, _ := Read(&buf)
	s := pattern.Count(p.Stitches)
	if s.Jumps != 0 || s.Stitches != 4 { // zero stitch + 3 pieces of 100
		t.Fatalf("%+v", s)
	}
	if math.Abs(pattern.LongestStitch(p.Stitches)-100) > 1 {
		t.Fatal("split length wrong")
	}
}

func TestShortFile(t *testing.T) {
	if _, _, err := Read(bytes.NewReader([]byte("LA:x"))); err != ErrShortHeader {
		t.Fatalf("err = %v", err)
	}
}

func FuzzRead(f *testing.F) {
	var buf bytes.Buffer
	_ = Write(&buf, []pattern.St{{X: 10, Y: 10, Cmd: pattern.Stitch}}, WriteOptions{})
	f.Add(buf.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = Read(bytes.NewReader(data))
	})
}
