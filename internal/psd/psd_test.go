package psd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixW, fixH = 16, 9

// fixtureNames lists the files emitted by testdata/gen_fixtures.py.
var fixtureNames = []string{
	"rgb8_raw.psd",
	"rgb8_rle.psd",
	"rgba8_rle.psd",
	"gray8_raw.psd",
	"gray8_alpha_rle.psd",
	"rgb16_raw.psd",
	"indexed8_raw.psd",
	"cmyk8_raw.psd",
	"duotone8_raw.psd",
	"psb_rgb8_rle.psb",
}

func palette(i int) (r, g, b uint8) {
	return uint8(i), uint8(255 - i), uint8(i * 7)
}

// expectedPixel mirrors expected_rgba() in testdata/gen_fixtures.py.
func expectedPixel(name string, x, y int) color.NRGBA {
	switch {
	case strings.HasPrefix(name, "rgb"), strings.HasPrefix(name, "psb"):
		a := uint8(255)
		if strings.Contains(name, "rgba") {
			a = uint8(255 - x*15)
		}
		return color.NRGBA{uint8(x * 16), uint8(y * 28), uint8(x * y * 3), a}
	case strings.HasPrefix(name, "gray"), strings.HasPrefix(name, "duotone"):
		v := uint8(x*16 + y*28)
		a := uint8(255)
		if strings.Contains(name, "alpha") {
			a = uint8(255 - y*25)
		}
		return color.NRGBA{v, v, v, a}
	case strings.HasPrefix(name, "indexed"):
		r, g, b := palette((x + y*16) % 256)
		return color.NRGBA{r, g, b, 255}
	case strings.HasPrefix(name, "cmyk"):
		c, m, yy, k := 255-x*15, 255-y*20, 200-(x*y)%100, 255-(x+y)*5
		f := func(v int) uint8 { return uint8((v*k + 127) / 255) }
		return color.NRGBA{f(c), f(m), f(yy), 255}
	}
	panic("unknown fixture " + name)
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

func TestDecodeFixtures(t *testing.T) {
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			data := readFixture(t, name)
			img, err := Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			nrgba, ok := img.(*image.NRGBA)
			if !ok {
				t.Fatalf("got %T, want *image.NRGBA", img)
			}
			if b := nrgba.Bounds(); b.Dx() != fixW || b.Dy() != fixH {
				t.Fatalf("bounds %v, want %dx%d", b, fixW, fixH)
			}
			bad := 0
			for y := 0; y < fixH; y++ {
				for x := 0; x < fixW; x++ {
					got := nrgba.NRGBAAt(x, y)
					want := expectedPixel(name, x, y)
					if absDiff(got.R, want.R) > 1 || absDiff(got.G, want.G) > 1 ||
						absDiff(got.B, want.B) > 1 || got.A != want.A {
						bad++
						if bad <= 3 {
							t.Errorf("pixel (%d,%d) = %v, want %v", x, y, got, want)
						}
					}
				}
			}
			if bad > 0 {
				t.Errorf("%d mismatched pixels", bad)
			}
		})
	}
}

func TestDecodeConfig(t *testing.T) {
	for _, name := range fixtureNames {
		cfg, err := DecodeConfig(bytes.NewReader(readFixture(t, name)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if cfg.Width != fixW || cfg.Height != fixH || cfg.ColorModel != color.NRGBAModel {
			t.Errorf("%s: config %+v", name, cfg)
		}
	}
}

func TestImageRegisterFormat(t *testing.T) {
	data := readFixture(t, "rgb8_rle.psd")
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != "psd" {
		t.Fatalf("image.Decode: format=%q err=%v", format, err)
	}
	if got, want := img.At(3, 2), expectedPixel("rgb8_rle.psd", 3, 2); got != want {
		t.Errorf("pixel = %v, want %v", got, want)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "psd" || cfg.Width != fixW {
		t.Fatalf("image.DecodeConfig: %+v %q %v", cfg, format, err)
	}
}

// patch returns a copy of data with b overwritten at off.
func patch(data []byte, off int, b []byte) []byte {
	out := append([]byte(nil), data...)
	copy(out[off:], b)
	return out
}

func be16(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
func be32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

func TestDecodeRejects(t *testing.T) {
	rgb := readFixture(t, "rgb8_raw.psd")
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"empty", nil, ErrFormat},
		{"bad signature", patch(rgb, 0, []byte("8BPX")), ErrFormat},
		{"bad version", patch(rgb, 4, be16(3)), ErrFormat},
		{"zero channels", patch(rgb, 12, be16(0)), ErrFormat},
		{"too many channels", patch(rgb, 12, be16(57)), ErrFormat},
		{"too few channels for RGB", patch(rgb, 12, be16(2)), ErrFormat},
		{"zero width", patch(rgb, 18, be32(0)), ErrFormat},
		{"huge width", patch(rgb, 18, be32(30001)), ErrFormat},
		{"huge area", patch(patch(rgb, 18, be32(20000)), 14, be32(20000)), ErrFormat},
		{"depth 32", patch(rgb, 22, be16(32)), ErrUnsupported},
		{"depth 1", patch(rgb, 22, be16(1)), ErrUnsupported},
		{"depth 12", patch(rgb, 22, be16(12)), ErrFormat},
		{"lab mode", patch(rgb, 24, be16(9)), ErrUnsupported},
		{"mode 99", patch(rgb, 24, be16(99)), ErrFormat},
		{"huge colour mode section", patch(rgb, 26, be32(0xFFFFFFFF)), ErrFormat},
		{"truncated header", rgb[:20], ErrFormat},
		{"truncated pixels", rgb[:len(rgb)-10], ErrFormat},
		{"truncated before image data", rgb[:60], ErrFormat},
	}
	// ZIP compression: find the compression word (last 2 bytes before pixels).
	pixBytes := fixW * fixH * 3
	compOff := len(rgb) - pixBytes - 2
	cases = append(cases,
		struct {
			name string
			data []byte
			want error
		}{"zip compression", patch(rgb, compOff, be16(2)), ErrUnsupported},
		struct {
			name string
			data []byte
			want error
		}{"bad compression", patch(rgb, compOff, be16(7)), ErrFormat},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img, err := Decode(bytes.NewReader(tc.data))
			if err == nil {
				t.Fatalf("Decode succeeded (%v), want %v", img.Bounds(), tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecodeRejectsOversizedHeaderButConfigReportsIt(t *testing.T) {
	rgb := readFixture(t, "rgb8_raw.psd")
	big := patch(rgb, 18, be32(100000))
	if _, err := Decode(bytes.NewReader(big)); !errors.Is(err, ErrFormat) {
		t.Errorf("Decode err = %v", err)
	}
	cfg, err := DecodeConfig(bytes.NewReader(big))
	if err != nil || cfg.Width != 100000 {
		t.Errorf("DecodeConfig = %+v, %v", cfg, err)
	}
}

func TestRLETruncatedData(t *testing.T) {
	data := readFixture(t, "rgb8_rle.psd")
	// Cut the file in the middle of the packed data: must error, not panic.
	for _, cut := range []int{len(data) - 1, len(data) - 50, len(data) - 200} {
		if _, err := Decode(bytes.NewReader(data[:cut])); !errors.Is(err, ErrFormat) {
			t.Errorf("cut %d: err = %v", cut, err)
		}
	}
}

func TestRLEOverlongRowCountRejected(t *testing.T) {
	data := readFixture(t, "rgb8_rle.psd")
	// The byte-count table starts right after the 2-byte compression word,
	// which follows the empty layer section. Locate it by parsing lengths.
	off := 26
	off += 4 + int(binary.BigEndian.Uint32(data[off:]))
	off += 4 + int(binary.BigEndian.Uint32(data[off:]))
	off += 4 + int(binary.BigEndian.Uint32(data[off:]))
	off += 2 // compression
	bad := patch(data, off, be16(0xFFFF))
	if _, err := Decode(bytes.NewReader(bad)); !errors.Is(err, ErrFormat) {
		t.Errorf("err = %v", err)
	}
}

func TestUnpackBits(t *testing.T) {
	cases := []struct {
		src  []byte
		n    int
		want []byte
	}{
		{[]byte{0x02, 'a', 'b', 'c'}, 3, []byte("abc")},
		{[]byte{0xFE, 'z'}, 3, []byte("zzz")},
		{[]byte{0x80, 0xFE, 'z'}, 3, []byte("zzz")},
		{[]byte{0x01, 'a', 'b', 0xFF, 'c'}, 4, []byte("abcc")},
		// Short stream: fill with last value.
		{[]byte{0x00, 'q'}, 4, []byte("qqqq")},
		{nil, 2, []byte{0, 0}},
		// Over-long stream: truncated to dst.
		{[]byte{0x05, 'a', 'b', 'c', 'd', 'e', 'f'}, 3, []byte("abc")},
		{[]byte{0xF0, 'r'}, 2, []byte("rr")},
		// Literal header with missing bytes.
		{[]byte{0x05, 'a'}, 3, []byte("aaa")},
		// Repeat header with missing value byte.
		{[]byte{0x00, 'a', 0xFE}, 3, []byte("aaa")},
	}
	for i, tc := range cases {
		dst := make([]byte, tc.n)
		unpackBits(dst, tc.src)
		if !bytes.Equal(dst, tc.want) {
			t.Errorf("case %d: got %q, want %q", i, dst, tc.want)
		}
	}
}

func FuzzDecode(f *testing.F) {
	for _, name := range fixtureNames {
		f.Add(readFixture(f, name))
	}
	f.Add([]byte("8BPS"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		img, err := Decode(bytes.NewReader(data))
		if err == nil {
			b := img.Bounds()
			if b.Dx() < 1 || b.Dy() < 1 || b.Dx() > MaxDimension || b.Dy() > MaxDimension {
				t.Fatalf("bad bounds %v", b)
			}
		}
		_, _ = DecodeConfig(bytes.NewReader(data))
	})
}
