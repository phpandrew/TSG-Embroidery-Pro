package formats

import (
	"errors"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// PEC layout (offsets relative to the start of the PEC block, i.e. byte 8 of
// a standalone .pec, or the PEC offset inside a .pes):
//
//	0x000  "LA:" + 16-byte label
//	0x030  colour-change count, colour indexes, padding
//	0x202  3-byte stitch block length
//	0x210  stitch data, terminated by FF 00
//
// Stitch data is already Y-down, so no axis flip is needed.
const (
	pecMagic       = "#PEC0001"
	pecStitchStart = 0x210

	pecFlagLong = 0x80
	pecFlagTrim = 0x20
	pecFlagJump = 0x10
)

var (
	ErrPECMagic     = errors.New("pec: missing #PEC0001 magic")
	ErrPECTruncated = errors.New("pec: header truncated")
)

func readPEC(data []byte) (*pattern.Pattern, error) {
	if len(data) < len(pecMagic) || string(data[:len(pecMagic)]) != pecMagic {
		return nil, ErrPECMagic
	}
	return decodePEC(data, len(pecMagic))
}

// decodePEC decodes the PEC block starting at data[start:].
func decodePEC(data []byte, start int) (*pattern.Pattern, error) {
	if start < 0 || start > len(data) || len(data)-start < pecStitchStart {
		return nil, ErrPECTruncated
	}
	b := newBuilder()
	b.p.Label = cleanLabel(data[start+3 : start+19])
	d := data[start+pecStitchStart:]
	i := 0
	trimmed := false // a Trim has been emitted for the current travel run
	for i+2 <= len(d) {
		v1, v2 := d[i], d[i+1]
		i += 2
		if v1 == 0xFF && v2 == 0x00 {
			if err := b.emit(pattern.End); err != nil {
				return nil, err
			}
			return b.p, nil
		}
		if v1 == 0xFE && v2 == 0xB0 {
			i++ // colour index byte (unused: the app assigns colours)
			if err := b.emit(pattern.ColorChange); err != nil {
				return nil, err
			}
			trimmed = false
			continue
		}
		var x, y int
		jump, trim := false, false
		if v1&pecFlagLong != 0 {
			trim = v1&pecFlagTrim != 0
			jump = v1&pecFlagJump != 0
			x = signed12(int(v1)<<8 | int(v2))
			if i >= len(d) {
				break // truncated
			}
			v2 = d[i]
			i++
		} else {
			x = signed7(v1)
		}
		if v2&pecFlagLong != 0 {
			trim = trim || v2&pecFlagTrim != 0
			jump = jump || v2&pecFlagJump != 0
			if i >= len(d) {
				break // truncated
			}
			y = signed12(int(v2)<<8 | int(d[i]))
			i++
		} else {
			y = signed7(v2)
		}
		var err error
		switch {
		case jump:
			err = b.move(x, y, pattern.Jump)
		case trim:
			// Trim+move. Only one Trim per travel run, so a long move split
			// into several trim-jump records doesn't produce stacked trims.
			if !trimmed {
				if err = b.emit(pattern.Trim); err != nil {
					return nil, err
				}
				trimmed = true
			}
			err = b.move(x, y, pattern.Jump)
		default:
			trimmed = false
			err = b.move(x, y, pattern.Stitch)
		}
		if err != nil {
			return nil, err
		}
	}
	return b.p, nil
}

func signed12(v int) int {
	v &= 0xFFF
	if v > 0x7FF {
		return v - 0x1000
	}
	return v
}

func signed7(b byte) int {
	if b > 63 {
		return int(b) - 128
	}
	return int(b)
}
