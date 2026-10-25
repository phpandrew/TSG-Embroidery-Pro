package formats

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/dst"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// expected.json is produced by pyembroidery (testdata/gen_fixtures.py, run
// manually; never at test time): per fixture stitch count, colour changes (COLOR_CHANGE
// + STOP), and the bounding box of STITCH records in pyembroidery's internal
// coordinates, which are Y-down 0.1 mm like ours.
type oracle struct {
	Stitches     int       `json:"stitches"`
	ColorChanges int       `json:"colorChanges"`
	Trims        int       `json:"trims"`
	MinX         float64   `json:"minX"`
	MinY         float64   `json:"minY"`
	MaxX         float64   `json:"maxX"`
	MaxY         float64   `json:"maxY"`
	First        []float64 `json:"first"`
}

func loadOracle(t *testing.T) map[string]oracle {
	t.Helper()
	b, err := os.ReadFile("testdata/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]oracle{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func readFixture(t testing.TB, name string) *pattern.Pattern {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := Read(f, filepath.Ext(name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return p
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestAgainstPyembroidery(t *testing.T) {
	exp := loadOracle(t)
	names := make([]string, 0, len(exp))
	for k := range exp {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) < 16 {
		t.Fatalf("only %d fixtures in expected.json", len(names))
	}
	for _, name := range names {
		want := exp[name]
		t.Run(name, func(t *testing.T) {
			p := readFixture(t, name)
			c := pattern.Count(p.Stitches)
			if c.Stitches != want.Stitches {
				t.Errorf("stitches = %d, want %d", c.Stitches, want.Stitches)
			}
			if c.ColorChanges != want.ColorChanges {
				t.Errorf("color changes = %d, want %d", c.ColorChanges, want.ColorChanges)
			}
			r := pattern.Bounds(p.Stitches)
			if !near(r.MinX, want.MinX, 1) || !near(r.MinY, want.MinY, 1) ||
				!near(r.MaxX, want.MaxX, 1) || !near(r.MaxY, want.MaxY, 1) {
				t.Errorf("bounds = %+v, want %v,%v..%v,%v", r, want.MinX, want.MinY, want.MaxX, want.MaxY)
			}
			for _, s := range p.Stitches {
				if s.Cmd == pattern.Stitch {
					if !near(s.X, want.First[0], 1) || !near(s.Y, want.First[1], 1) {
						t.Errorf("first stitch = %v,%v, want %v", s.X, s.Y, want.First)
					}
					break
				}
			}
			// PEC/PES carry explicit trims; ours dedupes stacked trim-jumps,
			// so never more than the reference.
			ext := filepath.Ext(name)
			if (ext == ".pes" || ext == ".pec") && c.Trims > want.Trims {
				t.Errorf("trims = %d, want <= %d", c.Trims, want.Trims)
			}
			// The shared pipeline must accept the output.
			blocks := pattern.SplitBlocks(pattern.CollapseJumps(p.Stitches, 3))
			if len(blocks) != want.ColorChanges+1 {
				t.Errorf("blocks = %d, want %d", len(blocks), want.ColorChanges+1)
			}
		})
	}
}

// TestOrientationMatchesDST reads the original sample DSTs through this
// repo's dst.Read and checks every converted copy has the same size and the
// same first-stitch position relative to the design centre (so a Y flip or
// X mirror would fail).
func TestOrientationMatchesDST(t *testing.T) {
	srcs := map[string]string{
		"ANDREW":   "../../samples/ANDREW.dst",
		"alphabet": "../../samples/alphabet.dst",
		"crest":    "../../samples/Team crest.dst",
		"square":   "testdata/square.dst",
	}
	for name, path := range srcs {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		dp, _, err := dst.Read(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		ref := pattern.Bounds(dp.Stitches)
		refFirst := firstStitch(dp.Stitches)
		for _, ext := range []string{".pes", ".pec", ".jef", ".exp"} {
			fx := name + ext
			if _, err := os.Stat(filepath.Join("testdata", fx)); err != nil {
				continue
			}
			t.Run(fx, func(t *testing.T) {
				p := readFixture(t, fx)
				r := pattern.Bounds(p.Stitches)
				if !near(r.Width(), ref.Width(), 2) || !near(r.Height(), ref.Height(), 2) {
					t.Errorf("size %vx%v, DST %vx%v", r.Width(), r.Height(), ref.Width(), ref.Height())
				}
				fs := firstStitch(p.Stitches)
				ox, oy := fs.X-r.CenterX(), fs.Y-r.CenterY()
				rx, ry := refFirst.X-ref.CenterX(), refFirst.Y-ref.CenterY()
				if !near(ox, rx, 2) || !near(oy, ry, 2) {
					t.Errorf("first stitch offset %v,%v, DST %v,%v", ox, oy, rx, ry)
				}
				if ry != 0 && math.Signbit(oy) != math.Signbit(ry) {
					t.Errorf("Y orientation flipped")
				}
			})
		}
	}
}

func firstStitch(sts []pattern.St) pattern.St {
	for _, s := range sts {
		if s.Cmd == pattern.Stitch {
			return s
		}
	}
	return pattern.St{}
}

// TestSquare checks the synthetic design: three 200x200 squares (21 stitches
// each, one colour each) at (0,0), (300,0), (0,300), Y down, with trims
// between them.
func TestSquare(t *testing.T) {
	origins := [][2]float64{{0, 0}, {300, 0}, {0, 300}}
	for _, ext := range Exts() {
		t.Run(ext, func(t *testing.T) {
			p := readFixture(t, "square"+ext)
			blocks := pattern.SplitBlocks(pattern.CollapseJumps(p.Stitches, 3))
			if len(blocks) != 3 {
				t.Fatalf("blocks = %d, want 3", len(blocks))
			}
			for i, b := range blocks {
				r := pattern.Bounds(b)
				if pattern.Count(b).Stitches != 21 {
					t.Errorf("block %d stitches = %d", i, pattern.Count(b).Stitches)
				}
				if !near(r.MinX, origins[i][0], 1) || !near(r.MinY, origins[i][1], 1) ||
					!near(r.Width(), 200, 1) || !near(r.Height(), 200, 1) {
					t.Errorf("block %d bounds %+v", i, r)
				}
				// second stitch goes +X along the top edge (Y down: top = minY)
				var sts []pattern.St
				for _, s := range b {
					if s.Cmd == pattern.Stitch {
						sts = append(sts, s)
					}
				}
				if sts[1].X <= sts[0].X || sts[1].Y != sts[0].Y {
					t.Errorf("block %d first edge direction wrong: %v -> %v", i, sts[0], sts[1])
				}
				if sts[6].Y <= sts[5].Y {
					t.Errorf("block %d second edge should go down (+Y): %v -> %v", i, sts[5], sts[6])
				}
			}
			if ext == ".pes" || ext == ".pec" {
				if n := pattern.Count(p.Stitches).Trims; n != 2 {
					t.Errorf("trims = %d, want 2", n)
				}
				if p.Stitches[len(p.Stitches)-1].Cmd != pattern.End {
					t.Errorf("missing End record")
				}
			}
		})
	}
}

func TestExtHelpers(t *testing.T) {
	for _, e := range []string{".pes", ".PES", "pec", ".Jef", ".exp"} {
		if !Supported(e) {
			t.Errorf("Supported(%q) = false", e)
		}
	}
	for _, e := range []string{".dst", ".vp3", "", "."} {
		if Supported(e) {
			t.Errorf("Supported(%q) = true", e)
		}
	}
	if got := strings.Join(Exts(), ","); got != ".pes,.pec,.jef,.exp" {
		t.Errorf("Exts = %s", got)
	}
	if _, err := Read(bytes.NewReader(nil), ".dst"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("dst: %v", err)
	}
}

func TestBadHeaders(t *testing.T) {
	pesBadOff := []byte("#PES0001\xff\xff\xff\xff")
	pesBadOff = append(pesBadOff, make([]byte, 1000)...)
	pesShortOff := []byte("#PES0001\x0c\x00\x00\x00") // PEC block would need 0x210 bytes
	jefBadOff := make([]byte, 0x80)
	binary.LittleEndian.PutUint32(jefBadOff, 0xFFFFFFF0)
	jefLowOff := make([]byte, 0x80)
	binary.LittleEndian.PutUint32(jefLowOff, 4)
	cases := []struct {
		ext  string
		data []byte
		want error
	}{
		{".pes", nil, ErrPESMagic},
		{".pes", []byte("GARBAGE!garbage"), ErrPESMagic},
		{".pes", []byte("#PES00"), ErrPESTruncated},
		{".pes", pesBadOff, ErrPESOffset},
		{".pes", pesShortOff, ErrPESOffset},
		{".pec", []byte("#PES0001"), ErrPECMagic},
		{".pec", []byte("#PEC0001LA:short"), ErrPECTruncated},
		{".jef", make([]byte, 10), ErrJEFTruncated},
		{".jef", jefBadOff, ErrJEFOffset},
		{".jef", jefLowOff, ErrJEFOffset},
	}
	for _, c := range cases {
		if _, err := Read(bytes.NewReader(c.data), c.ext); !errors.Is(err, c.want) {
			t.Errorf("%s %q: err = %v, want %v", c.ext, c.data[:min(len(c.data), 16)], err, c.want)
		}
	}
	// EXP has no header: empty input is an empty pattern, not an error.
	p, err := Read(bytes.NewReader(nil), ".exp")
	if err != nil || len(p.Stitches) != 0 {
		t.Errorf("empty exp: %v %v", p, err)
	}
}

// Truncated stitch data returns what was read, without error.
func TestTruncated(t *testing.T) {
	for _, ext := range Exts() {
		full, err := os.ReadFile(filepath.Join("testdata", "crest"+ext))
		if err != nil {
			t.Fatal(err)
		}
		whole, err := Read(bytes.NewReader(full), ext)
		if err != nil {
			t.Fatal(err)
		}
		mid := len(full) / 2
		if ext == ".pes" { // cut inside the embedded PEC stitch data
			s := int(binary.LittleEndian.Uint32(full[8:12])) + pecStitchStart
			mid = s + (len(full)-s)/2
		}
		for _, cut := range []int{mid, mid + 1, mid + 3} {
			p, err := Read(bytes.NewReader(full[:cut]), ext)
			if err != nil {
				t.Errorf("%s cut %d: %v", ext, cut, err)
				continue
			}
			n := pattern.Count(p.Stitches).Stitches
			if n == 0 || n >= pattern.Count(whole.Stitches).Stitches {
				t.Errorf("%s cut %d: %d stitches", ext, cut, n)
			}
			if p.Stitches[len(p.Stitches)-1].Cmd == pattern.End {
				t.Errorf("%s cut %d: unexpected End", ext, cut)
			}
		}
	}
}

func TestRecordLimit(t *testing.T) {
	data := bytes.Repeat([]byte{1, 0}, MaxRecords+1) // EXP: tiny stitches
	if _, err := Read(bytes.NewReader(data), ".exp"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
	p, err := Read(bytes.NewReader(data[:2*MaxRecords]), ".exp")
	if err != nil || len(p.Stitches) != MaxRecords {
		t.Errorf("at limit: %v, %d records", err, len(p.Stitches))
	}
}

type zeros struct{}

func (zeros) Read(b []byte) (int, error) {
	clear(b)
	return len(b), nil
}

func TestInputLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 64 MB")
	}
	if _, err := Read(io.LimitReader(zeros{}, MaxInput+1), ".exp"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

func TestPECLabel(t *testing.T) {
	p := readFixture(t, "ANDREW.pec")
	if p.Label == "" || len(p.Label) > 16 {
		t.Errorf("label = %q", p.Label)
	}
}

func fuzzSeeds(f *testing.F, ext string) {
	matches, _ := filepath.Glob(filepath.Join("testdata", "*"+ext))
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte{})
}

func fuzzRead(f *testing.F, ext string) {
	fuzzSeeds(f, ext)
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Read(bytes.NewReader(data), ext)
		if err != nil {
			return
		}
		if len(p.Stitches) > MaxRecords {
			t.Fatalf("%d records", len(p.Stitches))
		}
		_ = pattern.SplitBlocks(pattern.CollapseJumps(p.Stitches, 3))
	})
}

func FuzzReadPES(f *testing.F) { fuzzRead(f, ".pes") }
func FuzzReadPEC(f *testing.F) { fuzzRead(f, ".pec") }
func FuzzReadJEF(f *testing.F) { fuzzRead(f, ".jef") }
func FuzzReadEXP(f *testing.F) { fuzzRead(f, ".exp") }
