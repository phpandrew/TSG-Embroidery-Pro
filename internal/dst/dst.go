// Package dst reads and writes Tajima DST embroidery files.
//
// Layout: a 512-byte ASCII header followed by 3-byte records. Each record
// carries a relative move of at most ±121 units (0.1 mm) per axis plus flags
// for stitch / jump / color change / end. DST stores Y pointing UP; the
// pattern package uses Y pointing DOWN, so Y is negated at this boundary.
//
// Bit layout follows the public DST description used by pyembroidery (MIT).
package dst

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

const (
	HeaderSize = 512
	MaxMove    = 121 // max |dx|,|dy| per record, in 0.1 mm

	// MaxRecords guards against hostile or corrupt files (≈ 30 MB of records).
	MaxRecords = 10_000_000
)

// Header holds the parsed key/value fields from the 512-byte header.
type Header struct {
	Label  string
	Fields map[string]string
}

var (
	ErrShortHeader = errors.New("dst: file shorter than 512-byte header")
	ErrTooLarge    = errors.New("dst: file exceeds record limit")
)

func bit(b byte, n uint) int { return int(b>>n) & 1 }

// decode returns dx, dy in DST space (Y up).
func decode(b0, b1, b2 byte) (int, int) {
	x := bit(b2, 2)*81 - bit(b2, 3)*81 +
		bit(b1, 2)*27 - bit(b1, 3)*27 +
		bit(b0, 2)*9 - bit(b0, 3)*9 +
		bit(b1, 0)*3 - bit(b1, 1)*3 +
		bit(b0, 0)*1 - bit(b0, 1)*1
	y := bit(b2, 5)*81 - bit(b2, 4)*81 +
		bit(b1, 5)*27 - bit(b1, 4)*27 +
		bit(b0, 5)*9 - bit(b0, 4)*9 +
		bit(b1, 7)*3 - bit(b1, 6)*3 +
		bit(b0, 7)*1 - bit(b0, 6)*1
	return x, y
}

type recKind int

const (
	recStitch recKind = iota
	recJump
	recColor
	recEnd
)

// encode packs a move (DST space, Y up) into one record.
func encode(x, y int, k recKind) ([3]byte, error) {
	var b0, b1, b2 byte
	switch k {
	case recColor:
		return [3]byte{0, 0, 0xC3}, nil
	case recEnd:
		return [3]byte{0, 0, 0xF3}, nil
	case recJump:
		b2 |= 1 << 7
	}
	b2 |= 0b11
	if x < -MaxMove || x > MaxMove || y < -MaxMove || y > MaxMove {
		return [3]byte{}, fmt.Errorf("dst: move %d,%d exceeds ±%d", x, y, MaxMove)
	}
	type step struct {
		v      int
		b      *byte
		pb, nb uint
	}
	xs := []step{{81, &b2, 2, 3}, {27, &b1, 2, 3}, {9, &b0, 2, 3}, {3, &b1, 0, 1}, {1, &b0, 0, 1}}
	ys := []step{{81, &b2, 5, 4}, {27, &b1, 5, 4}, {9, &b0, 5, 4}, {3, &b1, 7, 6}, {1, &b0, 7, 6}}
	apply := func(v int, steps []step) int {
		for _, s := range steps {
			half := s.v / 2
			if v > half {
				*s.b |= 1 << s.pb
				v -= s.v
			} else if v < -half {
				*s.b |= 1 << s.nb
				v += s.v
			}
		}
		return v
	}
	if apply(x, xs) != 0 || apply(y, ys) != 0 {
		return [3]byte{}, fmt.Errorf("dst: cannot encode %d,%d", x, y)
	}
	return [3]byte{b0, b1, b2}, nil
}

// Read parses a DST stream. Consecutive jump runs are kept verbatim; call
// pattern.CollapseJumps to turn them into Trim+Jump pairs.
func Read(r io.Reader) (*pattern.Pattern, Header, error) {
	br := bufio.NewReader(r)
	head := make([]byte, HeaderSize)
	if _, err := io.ReadFull(br, head); err != nil {
		return nil, Header{}, ErrShortHeader
	}
	h := parseHeader(head)
	p := &pattern.Pattern{Label: h.Label}

	var x, y int
	rec := make([]byte, 3)
	for n := 0; ; n++ {
		if n > MaxRecords {
			return nil, h, ErrTooLarge
		}
		if _, err := io.ReadFull(br, rec); err != nil {
			break // EOF or truncated trailing record: stop like other readers do
		}
		b2 := rec[2]
		dx, dy := decode(rec[0], rec[1], b2)
		switch {
		case b2&0b11110011 == 0b11110011:
			p.Stitches = append(p.Stitches, pattern.St{X: float64(x), Y: float64(-y), Cmd: pattern.End})
			return p, h, nil
		case b2&0b11000011 == 0b11000011:
			x += dx
			y += dy
			p.Stitches = append(p.Stitches, pattern.St{X: float64(x), Y: float64(-y), Cmd: pattern.ColorChange})
		case b2&0b01000011 == 0b01000011:
			// sequin mode toggle: not used on TFHX, skip
		case b2&0b10000011 == 0b10000011:
			x += dx
			y += dy
			p.Stitches = append(p.Stitches, pattern.St{X: float64(x), Y: float64(-y), Cmd: pattern.Jump})
		default:
			x += dx
			y += dy
			p.Stitches = append(p.Stitches, pattern.St{X: float64(x), Y: float64(-y), Cmd: pattern.Stitch})
		}
	}
	return p, h, nil
}

func parseHeader(b []byte) Header {
	h := Header{Fields: map[string]string{}}
	if i := bytes.IndexByte(b, 0x1A); i >= 0 {
		b = b[:i]
	}
	for _, line := range strings.FieldsFunc(string(b), func(r rune) bool { return r == '\r' || r == '\n' }) {
		if len(line) < 3 || line[2] != ':' {
			continue
		}
		key := line[:2]
		val := strings.TrimSpace(line[3:])
		h.Fields[key] = val
	}
	h.Label = h.Fields["LA"]
	return h
}

// WriteOptions controls DST output.
type WriteOptions struct {
	Label     string
	TrimJumps int // jump codes emitted per Trim (machine "jump-trim" count), default 3
}

// Write encodes sts (absolute, Y down, 0.1 mm) relative to origin 0,0.
// Moves longer than 12.1 mm are split; Trim becomes TrimJumps net-zero jumps.
// An End record is always appended.
func Write(w io.Writer, sts []pattern.St, opt WriteOptions) error {
	if opt.TrimJumps <= 0 {
		opt.TrimJumps = 3
	}
	var recs [][3]byte
	var cx, cy int // current position in DST space (Y up)
	ext := struct{ minX, maxX, minY, maxY int }{}
	colors := 0

	emit := func(dx, dy int, k recKind) error {
		r, err := encode(dx, dy, k)
		if err != nil {
			return err
		}
		recs = append(recs, r)
		cx += dx
		cy += dy
		ext.minX = min(ext.minX, cx)
		ext.maxX = max(ext.maxX, cx)
		ext.minY = min(ext.minY, cy)
		ext.maxY = max(ext.maxY, cy)
		return nil
	}
	moveTo := func(tx, ty int, k recKind) error {
		dx, dy := tx-cx, ty-cy
		n := int(math.Ceil(float64(max(abs(dx), abs(dy))) / MaxMove))
		if n == 0 {
			if k == recStitch {
				return emit(0, 0, k) // zero-length stitch is still a penetration
			}
			return nil
		}
		sx, sy := cx, cy
		for i := 1; i <= n; i++ {
			nx := sx + int(math.Round(float64(dx*i)/float64(n)))
			ny := sy + int(math.Round(float64(dy*i)/float64(n)))
			if err := emit(nx-cx, ny-cy, k); err != nil {
				return err
			}
		}
		return nil
	}

	for _, s := range sts {
		tx := int(math.Round(s.X))
		ty := -int(math.Round(s.Y))
		var err error
		switch s.Cmd {
		case pattern.Stitch:
			err = moveTo(tx, ty, recStitch)
		case pattern.Jump:
			err = moveTo(tx, ty, recJump)
		case pattern.Trim:
			err = emitTrim(emit, opt.TrimJumps)
		case pattern.ColorChange, pattern.Stop:
			colors++
			err = emit(0, 0, recColor)
		case pattern.End:
			// written below
		}
		if err != nil {
			return err
		}
	}
	end, _ := encode(0, 0, recEnd)
	recs = append(recs, end)

	label := opt.Label
	if len(label) > 16 {
		label = label[:16]
	}
	var hb bytes.Buffer
	fmt.Fprintf(&hb, "LA:%-16s\r", sanitizeLabel(label))
	fmt.Fprintf(&hb, "ST:%7d\r", len(recs))
	fmt.Fprintf(&hb, "CO:%3d\r", colors)
	fmt.Fprintf(&hb, "+X:%5d\r", abs(ext.maxX))
	fmt.Fprintf(&hb, "-X:%5d\r", abs(ext.minX))
	fmt.Fprintf(&hb, "+Y:%5d\r", abs(ext.maxY))
	fmt.Fprintf(&hb, "-Y:%5d\r", abs(ext.minY))
	fmt.Fprintf(&hb, "AX:%s%5d\r", sign(cx), abs(cx))
	fmt.Fprintf(&hb, "AY:%s%5d\r", sign(cy), abs(cy))
	fmt.Fprintf(&hb, "MX:+%5d\r", 0)
	fmt.Fprintf(&hb, "MY:+%5d\r", 0)
	fmt.Fprintf(&hb, "PD:%6s\r", "******")
	hb.WriteByte(0x1A)
	for hb.Len() < HeaderSize {
		hb.WriteByte(' ')
	}

	bw := bufio.NewWriter(w)
	if _, err := bw.Write(hb.Bytes()[:HeaderSize]); err != nil {
		return err
	}
	for _, r := range recs {
		if _, err := bw.Write(r[:]); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// emitTrim writes n jump codes that net to zero displacement.
func emitTrim(emit func(dx, dy int, k recKind) error, n int) error {
	if n == 1 {
		return emit(0, 0, recJump)
	}
	moves := make([]int, 0, n)
	sum := 0
	for i := 0; i < n-1; i++ {
		d := 2
		if i > 0 {
			d = 4
			if i%2 == 1 {
				d = -4
			}
		}
		moves = append(moves, d)
		sum += d
	}
	moves = append(moves, -sum)
	for _, d := range moves {
		if err := emit(d, d, recJump); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7F {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int) string {
	if v < 0 {
		return "-"
	}
	return "+"
}

// HeaderInt returns a numeric header field, or def when missing/invalid.
func (h Header) HeaderInt(key string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(h.Fields[key], "+")))
	if err != nil {
		return def
	}
	return v
}

// EncodeRecords encodes sts as bare DST records (no 512-byte header, with a
// trailing End record). Used to pack many small stitch lists compactly.
func EncodeRecords(sts []pattern.St) ([]byte, error) {
	var buf bytes.Buffer
	if err := Write(&buf, sts, WriteOptions{TrimJumps: 3}); err != nil {
		return nil, err
	}
	return buf.Bytes()[HeaderSize:], nil
}

// DecodeRecords reverses EncodeRecords (jump runs are kept verbatim).
func DecodeRecords(recs []byte) ([]pattern.St, error) {
	head := bytes.Repeat([]byte{' '}, HeaderSize)
	p, _, err := Read(io.MultiReader(bytes.NewReader(head), bytes.NewReader(recs)))
	if err != nil {
		return nil, err
	}
	return p.Stitches, nil
}
