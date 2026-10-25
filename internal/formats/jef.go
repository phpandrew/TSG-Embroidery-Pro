package formats

import (
	"encoding/binary"
	"errors"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// JEF (Janome): a header whose first uint32 (LE) is the offset of the stitch
// data, then 2-byte records. A plain record is a signed dx,dy byte pair;
// 0x80 introduces a 4-byte control record: 80 02 dx dy (move), 80 01 dx dy
// (colour change / stop), 80 10 (end). Y is stored pointing UP, so dy is
// negated here. JEF has no trim code; machines trim on jump runs, so moves
// are emitted as plain Jumps for pattern.CollapseJumps to interpret.

const jefMinHeader = 0x74 // fixed header fields before the colour tables

var (
	ErrJEFTruncated = errors.New("jef: header truncated")
	ErrJEFOffset    = errors.New("jef: stitch offset out of range")
)

func readJEF(data []byte) (*pattern.Pattern, error) {
	if len(data) < jefMinHeader {
		return nil, ErrJEFTruncated
	}
	off := uint64(binary.LittleEndian.Uint32(data[0:4]))
	if off < jefMinHeader || off > uint64(len(data)) {
		return nil, ErrJEFOffset
	}
	d := data[off:]
	b := newBuilder()
	for i := 0; i+2 <= len(d); {
		b0, b1 := d[i], d[i+1]
		i += 2
		if b0 != 0x80 {
			if err := b.move(signed8(b0), -signed8(b1), pattern.Stitch); err != nil {
				return nil, err
			}
			continue
		}
		ctrl := b1
		if ctrl == 0x10 {
			if err := b.emit(pattern.End); err != nil {
				return nil, err
			}
			return b.p, nil
		}
		if i+2 > len(d) {
			break // truncated
		}
		dx, dy := signed8(d[i]), -signed8(d[i+1])
		i += 2
		var err error
		switch ctrl {
		case 0x02:
			err = b.move(dx, dy, pattern.Jump)
		case 0x01:
			// Colour change (or a stop, which shares the code; the colour
			// table distinguishes them and the app assigns colours anyway).
			// pyembroidery writes a zero move here; apply any non-zero move
			// as a jump so later positions stay correct.
			err = b.emit(pattern.ColorChange)
			if err == nil && (dx != 0 || dy != 0) {
				err = b.move(dx, dy, pattern.Jump)
			}
		default:
			return b.p, nil // unknown control: stop like the reference reader
		}
		if err != nil {
			return nil, err
		}
	}
	return b.p, nil
}
