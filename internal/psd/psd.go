// Package psd decodes the flattened composite image from Adobe Photoshop
// PSD and PSB files.
//
// Only the "merged image data" section at the end of the file is decoded;
// layers, masks, resources and ICC profiles are ignored. The decoder is
// written for untrusted input: it never panics, bounds every allocation and
// returns an error for truncated or malformed files.
//
// Supported: 8- and 16-bit Grayscale, Indexed, RGB, CMYK and Duotone (treated
// as grayscale) in raw or RLE (PackBits) compression. Unsupported and reported
// as an error: Bitmap, Multichannel and Lab modes, 1- and 32-bit depths, and
// ZIP compression.
package psd

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
)

const (
	// MaxDimension is the largest accepted width or height in pixels.
	MaxDimension = 30000
	// MaxPixels is the largest accepted width*height.
	MaxPixels = 80_000_000
	// MaxInput is the largest number of input bytes the decoder will read.
	MaxInput = 512 << 20

	signature = "8BPS"

	modeBitmap       = 0
	modeGrayscale    = 1
	modeIndexed      = 2
	modeRGB          = 3
	modeCMYK         = 4
	modeMultichannel = 7
	modeDuotone      = 8
	modeLab          = 9

	compressionRaw = 0
	compressionRLE = 1

	paletteSize = 768
	maxChannels = 56
)

var (
	// ErrUnsupported is returned (wrapped) for valid-looking files that use a
	// feature this decoder does not implement.
	ErrUnsupported = errors.New("psd: unsupported feature")
	// ErrFormat is returned (wrapped) for malformed or truncated files.
	ErrFormat = errors.New("psd: invalid format")
)

func init() {
	image.RegisterFormat("psd", signature, Decode, DecodeConfig)
}

func formatErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrFormat, fmt.Sprintf(format, args...))
}

func unsupportedErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}

// header is the fixed 26-byte file header.
type header struct {
	version  uint16 // 1 = PSD, 2 = PSB
	channels int
	height   int
	width    int
	depth    int
	mode     int
}

func (h *header) psb() bool { return h.version == 2 }

// bytesPerSample returns the number of input bytes per channel sample.
func (h *header) bytesPerSample() int { return h.depth / 8 }

// colorChannels returns the number of colour channels for the mode.
func (h *header) colorChannels() int {
	switch h.mode {
	case modeRGB:
		return 3
	case modeCMYK:
		return 4
	default:
		return 1
	}
}

// hasAlpha reports whether the channel after the colour channels is treated
// as transparency.
func (h *header) hasAlpha() bool {
	switch h.mode {
	case modeIndexed:
		return false
	default:
		return h.channels > h.colorChannels()
	}
}

type reader struct {
	r *bufio.Reader
	// remaining is the number of bytes still readable below MaxInput.
	remaining uint64
}

func newReader(r io.Reader) *reader {
	return &reader{
		r:         bufio.NewReaderSize(io.LimitReader(r, MaxInput), 64<<10),
		remaining: MaxInput,
	}
}

// read fills buf completely or returns a format error.
func (rd *reader) read(buf []byte) error {
	if uint64(len(buf)) > rd.remaining {
		return formatErr("file exceeds %d byte limit", MaxInput)
	}
	if _, err := io.ReadFull(rd.r, buf); err != nil {
		return formatErr("unexpected end of file")
	}
	rd.remaining -= uint64(len(buf))
	return nil
}

// skip discards n bytes or returns a format error.
func (rd *reader) skip(n uint64) error {
	if n > rd.remaining {
		return formatErr("section length %d exceeds remaining input", n)
	}
	if n == 0 {
		return nil
	}
	if _, err := rd.r.Discard(int(n)); err != nil {
		return formatErr("unexpected end of file")
	}
	rd.remaining -= n
	return nil
}

func (rd *reader) u16() (uint16, error) {
	var b [2]byte
	if err := rd.read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}

func (rd *reader) u32() (uint32, error) {
	var b [4]byte
	if err := rd.read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

func (rd *reader) u64() (uint64, error) {
	var b [8]byte
	if err := rd.read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b[:]), nil
}

// sectionLength reads a 4-byte (PSD) or 8-byte (PSB) section length.
func (rd *reader) sectionLength(psb bool) (uint64, error) {
	if psb {
		return rd.u64()
	}
	n, err := rd.u32()
	return uint64(n), err
}

// readHeader parses and validates the file header. It does not apply the
// pixel-count limits so that DecodeConfig can still report the size of
// oversized files.
func readHeader(rd *reader) (*header, error) {
	var b [26]byte
	if err := rd.read(b[:]); err != nil {
		return nil, err
	}
	if string(b[0:4]) != signature {
		return nil, formatErr("bad signature")
	}
	h := &header{
		version:  binary.BigEndian.Uint16(b[4:6]),
		channels: int(binary.BigEndian.Uint16(b[12:14])),
		height:   int(binary.BigEndian.Uint32(b[14:18])),
		width:    int(binary.BigEndian.Uint32(b[18:22])),
		depth:    int(binary.BigEndian.Uint16(b[22:24])),
		mode:     int(binary.BigEndian.Uint16(b[24:26])),
	}
	if h.version != 1 && h.version != 2 {
		return nil, formatErr("unknown version %d", h.version)
	}
	if h.channels < 1 || h.channels > maxChannels {
		return nil, formatErr("channel count %d out of range", h.channels)
	}
	if h.width < 1 || h.height < 1 {
		return nil, formatErr("zero-sized image")
	}
	switch h.depth {
	case 8, 16:
	case 1, 32:
		return nil, unsupportedErr("%d-bit depth", h.depth)
	default:
		return nil, formatErr("invalid depth %d", h.depth)
	}
	switch h.mode {
	case modeGrayscale, modeIndexed, modeRGB, modeCMYK, modeDuotone:
	case modeBitmap, modeMultichannel, modeLab:
		return nil, unsupportedErr("colour mode %d", h.mode)
	default:
		return nil, formatErr("invalid colour mode %d", h.mode)
	}
	if h.channels < h.colorChannels() {
		return nil, formatErr("colour mode %d needs %d channels, file has %d", h.mode, h.colorChannels(), h.channels)
	}
	return h, nil
}

// DecodeConfig returns the width, height and colour model of a PSD/PSB
// without decoding pixel data. Decoded images are always *image.NRGBA.
func DecodeConfig(r io.Reader) (image.Config, error) {
	h, err := readHeader(newReader(r))
	if err != nil {
		return image.Config{}, err
	}
	return image.Config{
		ColorModel: color.NRGBAModel,
		Width:      h.width,
		Height:     h.height,
	}, nil
}

// Decode reads a PSD/PSB and returns its flattened composite image (the
// merged image data stored at the end of the file) as an *image.NRGBA.
func Decode(r io.Reader) (image.Image, error) {
	rd := newReader(r)
	h, err := readHeader(rd)
	if err != nil {
		return nil, err
	}
	if h.width > MaxDimension || h.height > MaxDimension {
		return nil, formatErr("image %dx%d exceeds %d pixel dimension limit", h.width, h.height, MaxDimension)
	}
	if uint64(h.width)*uint64(h.height) > MaxPixels {
		return nil, formatErr("image %dx%d exceeds %d pixel limit", h.width, h.height, MaxPixels)
	}

	// Color mode data section.
	cmLen, err := rd.u32()
	if err != nil {
		return nil, err
	}
	var palette []byte
	if h.mode == modeIndexed {
		if cmLen != paletteSize {
			return nil, formatErr("indexed colour table is %d bytes, want %d", cmLen, paletteSize)
		}
		palette = make([]byte, paletteSize)
		if err := rd.read(palette); err != nil {
			return nil, err
		}
	} else if err := rd.skip(uint64(cmLen)); err != nil {
		return nil, err
	}

	// Image resources section.
	resLen, err := rd.u32()
	if err != nil {
		return nil, err
	}
	if err := rd.skip(uint64(resLen)); err != nil {
		return nil, err
	}

	// Layer and mask information section.
	lmLen, err := rd.sectionLength(h.psb())
	if err != nil {
		return nil, err
	}
	if err := rd.skip(lmLen); err != nil {
		return nil, err
	}

	// Image data section.
	compression, err := rd.u16()
	if err != nil {
		return nil, err
	}

	img := image.NewNRGBA(image.Rect(0, 0, h.width, h.height))
	// Pre-fill opaque; alpha is overwritten if an alpha channel exists.
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xFF
	}

	decodeChannels := h.colorChannels()
	if h.hasAlpha() {
		decodeChannels++
	}

	var planes planeReader
	switch compression {
	case compressionRaw:
		planes = &rawPlanes{rd: rd}
	case compressionRLE:
		p, err := newRLEPlanes(rd, h)
		if err != nil {
			return nil, err
		}
		planes = p
	case 2, 3:
		return nil, unsupportedErr("zip compression")
	default:
		return nil, formatErr("unknown compression %d", compression)
	}

	rowBytes := h.width * h.bytesPerSample()
	row := make([]byte, rowBytes)
	for c := 0; c < decodeChannels; c++ {
		for y := 0; y < h.height; y++ {
			if err := planes.readRow(row); err != nil {
				return nil, err
			}
			storeRow(img, h, palette, c, y, row)
		}
	}
	if h.mode == modeIndexed {
		applyPalette(img, palette)
	}
	return img, nil
}

// storeRow writes one decoded channel row into the image. For 16-bit input the
// high byte of each sample is used. Samples are written to the NRGBA slot
// that the final conversion expects; CMYK and Indexed are fixed up afterwards.
func storeRow(img *image.NRGBA, h *header, palette []byte, channel, y int, row []byte) {
	bps := h.bytesPerSample()
	pix := img.Pix[y*img.Stride : y*img.Stride+h.width*4]
	switch h.mode {
	case modeRGB:
		// channel 0..2 -> R,G,B ; 3 -> A
		for x := 0; x < h.width; x++ {
			pix[x*4+channel] = row[x*bps]
		}
	case modeGrayscale, modeDuotone:
		if channel == 0 {
			for x := 0; x < h.width; x++ {
				v := row[x*bps]
				pix[x*4] = v
				pix[x*4+1] = v
				pix[x*4+2] = v
			}
		} else {
			for x := 0; x < h.width; x++ {
				pix[x*4+3] = row[x*bps]
			}
		}
	case modeIndexed:
		// Store index in R; applyPalette expands it later.
		for x := 0; x < h.width; x++ {
			pix[x*4] = row[x*bps]
		}
	case modeCMYK:
		// PSD stores CMYK inverted, so stored value s = 255-C. Then
		// R = (255-C)*(255-K)/255 = sC*sK/255. Accumulate the channel
		// into R,G,B for C,M,Y (channels 0..2), multiply by K (3),
		// alpha is channel 4.
		switch {
		case channel < 3:
			for x := 0; x < h.width; x++ {
				pix[x*4+channel] = row[x*bps]
			}
		case channel == 3:
			for x := 0; x < h.width; x++ {
				k := uint32(row[x*bps])
				p := pix[x*4 : x*4+3 : x*4+3]
				p[0] = uint8((uint32(p[0])*k + 127) / 255)
				p[1] = uint8((uint32(p[1])*k + 127) / 255)
				p[2] = uint8((uint32(p[2])*k + 127) / 255)
			}
		default:
			for x := 0; x < h.width; x++ {
				pix[x*4+3] = row[x*bps]
			}
		}
	}
}

// applyPalette expands indices stored in the R slot into RGB using the
// planar 768-byte colour table (256 R, 256 G, 256 B).
func applyPalette(img *image.NRGBA, palette []byte) {
	for i := 0; i+3 < len(img.Pix); i += 4 {
		idx := int(img.Pix[i])
		img.Pix[i] = palette[idx]
		img.Pix[i+1] = palette[256+idx]
		img.Pix[i+2] = palette[512+idx]
	}
}

// planeReader yields successive channel rows of raw sample bytes.
type planeReader interface {
	readRow(dst []byte) error
}

type rawPlanes struct{ rd *reader }

func (p *rawPlanes) readRow(dst []byte) error { return p.rd.read(dst) }

type rlePlanes struct {
	rd     *reader
	counts []uint32 // byte count for every row of every channel
	next   int
	buf    []byte
}

func newRLEPlanes(rd *reader, h *header) (*rlePlanes, error) {
	rows := uint64(h.height) * uint64(h.channels) // bounded: <=30000*56
	rowBytes := uint64(h.width) * uint64(h.bytesPerSample())
	// PackBits worst case: one header byte per 128 literal bytes. Allow some
	// slack for sloppy encoders but reject absurd counts.
	maxCount := rowBytes + rowBytes/128 + 256

	entrySize := uint64(2)
	if h.psb() {
		entrySize = 4
	}
	table := make([]byte, rows*entrySize)
	if err := rd.read(table); err != nil {
		return nil, err
	}
	counts := make([]uint32, rows)
	var total, maxSeen uint64
	for i := range counts {
		var n uint64
		if h.psb() {
			n = uint64(binary.BigEndian.Uint32(table[i*4:]))
		} else {
			n = uint64(binary.BigEndian.Uint16(table[i*2:]))
		}
		if n > maxCount {
			return nil, formatErr("RLE row length %d exceeds maximum %d", n, maxCount)
		}
		counts[i] = uint32(n)
		total += n
		if n > maxSeen {
			maxSeen = n
		}
	}
	if total > rd.remaining {
		return nil, formatErr("RLE data length %d exceeds remaining input", total)
	}
	return &rlePlanes{rd: rd, counts: counts, buf: make([]byte, maxSeen)}, nil
}

func (p *rlePlanes) readRow(dst []byte) error {
	if p.next >= len(p.counts) {
		return formatErr("RLE row table exhausted")
	}
	n := p.counts[p.next]
	p.next++
	src := p.buf[:n]
	if err := p.rd.read(src); err != nil {
		return err
	}
	unpackBits(dst, src)
	return nil
}

// unpackBits decodes PackBits data from src into dst. If src decodes to fewer
// bytes than len(dst), the remainder is filled with the last decoded value
// (or zero if nothing was decoded). Output beyond len(dst) is discarded.
func unpackBits(dst, src []byte) {
	o := 0
	i := 0
	for i < len(src) && o < len(dst) {
		n := int(int8(src[i]))
		i++
		switch {
		case n >= 0:
			cnt := n + 1
			if cnt > len(src)-i {
				cnt = len(src) - i
			}
			if cnt > len(dst)-o {
				cnt = len(dst) - o
			}
			copy(dst[o:o+cnt], src[i:i+cnt])
			i += cnt
			o += cnt
		case n == -128:
			// no-op
		default:
			if i >= len(src) {
				break
			}
			cnt := 1 - n
			if cnt > len(dst)-o {
				cnt = len(dst) - o
			}
			v := src[i]
			i++
			for k := 0; k < cnt; k++ {
				dst[o+k] = v
			}
			o += cnt
		}
	}
	if o < len(dst) {
		var fill byte
		if o > 0 {
			fill = dst[o-1]
		}
		for ; o < len(dst); o++ {
			dst[o] = fill
		}
	}
}
