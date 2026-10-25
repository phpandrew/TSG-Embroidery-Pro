package formats

import (
	"encoding/binary"
	"errors"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/pattern"
)

// PES is a Brother container: "#PES" + 4-char version, a 4-byte little-endian
// offset to the embedded PEC block, then PE-Design object data we ignore.
// All stitches are read from the PEC block, which every PES version carries.

var (
	ErrPESMagic     = errors.New("pes: missing #PES magic")
	ErrPESTruncated = errors.New("pes: header truncated")
	ErrPESOffset    = errors.New("pes: PEC offset out of range")
)

const pesHeaderSize = 12

func readPES(data []byte) (*pattern.Pattern, error) {
	if len(data) >= len(pecMagic) && string(data[:len(pecMagic)]) == pecMagic {
		return readPEC(data) // PEC mislabelled as .pes: same handling as pyembroidery
	}
	if len(data) < 4 || string(data[:4]) != "#PES" {
		return nil, ErrPESMagic
	}
	if len(data) < pesHeaderSize {
		return nil, ErrPESTruncated
	}
	off := uint64(binary.LittleEndian.Uint32(data[8:12]))
	// The PEC block can't overlap the PES header and must hold a full PEC
	// header. uint64 arithmetic: no overflow on 32-bit platforms.
	if off < pesHeaderSize || off+pecStitchStart > uint64(len(data)) {
		return nil, ErrPESOffset
	}
	return decodePEC(data, int(off))
}
