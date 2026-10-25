// Package formats reads non-DST machine embroidery files (Brother PES/PEC,
// Janome JEF, Melco EXP) into the shared pattern model.
//
// Output mirrors internal/dst.Read so the same pipeline
// (pattern.CollapseJumps -> pattern.SplitBlocks) works unchanged:
//   - absolute coordinates in 0.1 mm, Y pointing DOWN;
//   - every move is its own record (Stitch or Jump) at the new position;
//   - ColorChange is its own record at the current position;
//   - an explicit trim in the file becomes a Trim record before the jump;
//   - End is emitted only when the file contains an explicit end marker;
//     truncated stitch data ends cleanly with what was read.
//
// Inputs are treated as untrusted: total input is capped at MaxInput bytes,
// output at MaxRecords records, and all header offsets are bounds-checked.
// Byte layouts follow the public descriptions used by pyembroidery (MIT).
package formats

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

const (
	// MaxInput is the largest file accepted, in bytes.
	MaxInput = 64 << 20
	// MaxRecords caps the number of records produced from one file.
	MaxRecords = 2_000_000
)

var (
	ErrTooLarge    = errors.New("formats: file exceeds size or record limit")
	ErrUnsupported = errors.New("formats: unsupported file type")
)

type reader func(data []byte) (*pattern.Pattern, error)

var readers = map[string]reader{
	".pes": readPES,
	".pec": readPEC,
	".jef": readJEF,
	".exp": readEXP,
}

// normExt lower-cases ext and ensures a leading dot.
func normExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext != "" && ext[0] != '.' {
		ext = "." + ext
	}
	return ext
}

// Read decodes a stitch file by extension. Returns stitches in the shared model.
func Read(r io.Reader, ext string) (*pattern.Pattern, error) {
	fn, ok := readers[normExt(ext)]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnsupported, ext)
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxInput+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxInput {
		return nil, ErrTooLarge
	}
	return fn(data)
}

// Supported reports whether ext (e.g. ".pes", case-insensitive) is readable here.
func Supported(ext string) bool {
	_, ok := readers[normExt(ext)]
	return ok
}

// Exts lists supported extensions, lower-case with dot.
func Exts() []string {
	return []string{".pes", ".pec", ".jef", ".exp"}
}

// builder accumulates absolute records from relative moves.
type builder struct {
	p    *pattern.Pattern
	x, y int // current position, Y down
}

func newBuilder() *builder { return &builder{p: &pattern.Pattern{}} }

func (b *builder) emit(c pattern.Cmd) error {
	if len(b.p.Stitches) >= MaxRecords {
		return ErrTooLarge
	}
	b.p.Stitches = append(b.p.Stitches, pattern.St{X: float64(b.x), Y: float64(b.y), Cmd: c})
	return nil
}

// move applies a relative move (Y down) and emits a record at the new position.
func (b *builder) move(dx, dy int, c pattern.Cmd) error {
	b.x += dx
	b.y += dy
	return b.emit(c)
}

// last returns the most recent command, or End if none.
func (b *builder) last() pattern.Cmd {
	if n := len(b.p.Stitches); n > 0 {
		return b.p.Stitches[n-1].Cmd
	}
	return pattern.End
}

func signed8(b byte) int { return int(int8(b)) }

// cleanLabel keeps printable ASCII and trims padding.
func cleanLabel(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7F {
			sb.WriteByte(c)
		}
	}
	return strings.TrimSpace(sb.String())
}
