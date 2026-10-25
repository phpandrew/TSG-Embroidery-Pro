package formats

import (
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// EXP (Melco): headerless stream of 2-byte records. A plain record is a
// signed dx,dy byte pair (Y stored pointing UP, negated here). 0x80 starts a
// 4-byte control record:
//
//	80 02 dx dy  stitch
//	80 04 dx dy  jump
//	80 01 dx dy  colour change (optionally followed by a move)
//	80 80 xx xx  trim (payload ignored, usually 07 00)
//
// There is no end marker, so no End record is emitted (EOF just stops).

func readEXP(data []byte) (*pattern.Pattern, error) {
	b := newBuilder()
	d := data
	for i := 0; i+2 <= len(d); {
		b0, b1 := d[i], d[i+1]
		i += 2
		if b0 != 0x80 {
			if err := b.move(signed8(b0), -signed8(b1), pattern.Stitch); err != nil {
				return nil, err
			}
			continue
		}
		if i+2 > len(d) {
			break // truncated
		}
		ctrl := b1
		dx, dy := signed8(d[i]), -signed8(d[i+1])
		i += 2
		var err error
		switch ctrl {
		case 0x80:
			if b.last() != pattern.Trim {
				err = b.emit(pattern.Trim)
			}
		case 0x02:
			err = b.move(dx, dy, pattern.Stitch)
		case 0x04:
			err = b.move(dx, dy, pattern.Jump)
		case 0x01:
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
