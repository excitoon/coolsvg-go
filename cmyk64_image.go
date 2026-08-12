package coolsvg

import (
	"image"
	"image/color"
)

// CMYK64Color is an opaque, straight 16-bit cyan, magenta, yellow, and black
// separation value.
type CMYK64Color struct {
	C uint16
	M uint16
	Y uint16
	K uint16
}

// RGBA implements color.Color. Conversion to RGB is necessarily lossy; the
// original separation values remain available through the CMYK64Color fields.
func (c CMYK64Color) RGBA() (r, g, b, a uint32) {
	const maximum = uint64(0xffff)
	convert := func(ink uint16) uint32 {
		return uint32(((maximum-uint64(ink))*(maximum-uint64(c.K)) + maximum/2) / maximum)
	}
	return convert(c.C), convert(c.M), convert(c.Y), 0xffff
}

// CMYK64Model converts colors to opaque 16-bit CMYK using the package's
// generic, unprofiled separation formula.
var CMYK64Model color.Model = color.ModelFunc(cmyk64Model)

func cmyk64Model(value color.Color) color.Color {
	if separated, ok := value.(CMYK64Color); ok {
		return separated
	}
	if separated, ok := value.(color.CMYK); ok {
		return CMYK64Color{
			C: uint16(separated.C) * 0x101,
			M: uint16(separated.M) * 0x101,
			Y: uint16(separated.Y) * 0x101,
			K: uint16(separated.K) * 0x101,
		}
	}
	r, g, b, a := value.RGBA()
	if a != 0 && a != 0xffff {
		r = min(0xffff, (r*0xffff+a/2)/a)
		g = min(0xffff, (g*0xffff+a/2)/a)
		b = min(0xffff, (b*0xffff+a/2)/a)
	}
	return deviceRGBToCMYK64(uint16(r), uint16(g), uint16(b))
}

func deviceRGBToCMYK64(r, g, b uint16) CMYK64Color {
	maximumRGB := max(r, max(g, b))
	k := uint32(0xffff - maximumRGB)
	if k == 0xffff {
		return CMYK64Color{K: 0xffff}
	}
	denominator := uint64(0xffff - k)
	convert := func(channel uint16) uint16 {
		numerator := uint64(0xffff-uint32(channel)-k) * 0xffff
		return uint16((numerator + denominator/2) / denominator)
	}
	return CMYK64Color{C: convert(r), M: convert(g), Y: convert(b), K: uint16(k)}
}

// CMYK64 is an in-memory 16-bit CMYK image. Each pixel occupies eight bytes in
// C, M, Y, K order, with every channel stored most-significant byte first. The
// layout mirrors image.RGBA64 and is suitable for direct 16-bit scanline I/O.
type CMYK64 struct {
	Pix    []uint8
	Stride int
	Rect   image.Rectangle
}

// NewCMYK64 returns a new zero-initialized 16-bit CMYK image.
func NewCMYK64(rect image.Rectangle) *CMYK64 {
	length, stride, ok := checkedImageSize(rect, 8)
	if !ok {
		panic("coolsvg: invalid CMYK64 rectangle")
	}
	return &CMYK64{Pix: make([]uint8, length), Stride: stride, Rect: rect}
}

func checkedImageSize(rect image.Rectangle, unitsPerPixel int) (length, stride int, ok bool) {
	if rect.Min.X > rect.Max.X || rect.Min.Y > rect.Max.Y || unitsPerPixel <= 0 {
		return 0, 0, false
	}
	maximumUint := uint(^uint(0) >> 1)
	widthUint := uint(rect.Max.X) - uint(rect.Min.X)
	heightUint := uint(rect.Max.Y) - uint(rect.Min.Y)
	if widthUint > maximumUint || heightUint > maximumUint {
		return 0, 0, false
	}
	width, height := int(widthUint), int(heightUint)
	maximum := int(maximumUint)
	if width != 0 && unitsPerPixel > maximum/width {
		return 0, 0, false
	}
	stride = width * unitsPerPixel
	if height != 0 && stride > maximum/height {
		return 0, 0, false
	}
	return stride * height, stride, true
}

// ColorModel implements image.Image.
func (p *CMYK64) ColorModel() color.Model { return CMYK64Model }

// Bounds implements image.Image.
func (p *CMYK64) Bounds() image.Rectangle { return p.Rect }

// At implements image.Image.
func (p *CMYK64) At(x, y int) color.Color { return p.CMYK64At(x, y) }

// CMYK64At returns the pixel at (x, y), or zero when the point is outside the
// image bounds.
func (p *CMYK64) CMYK64At(x, y int) CMYK64Color {
	if !(image.Point{X: x, Y: y}.In(p.Rect)) {
		return CMYK64Color{}
	}
	offset := p.PixOffset(x, y)
	return CMYK64Color{
		C: uint16(p.Pix[offset])<<8 | uint16(p.Pix[offset+1]),
		M: uint16(p.Pix[offset+2])<<8 | uint16(p.Pix[offset+3]),
		Y: uint16(p.Pix[offset+4])<<8 | uint16(p.Pix[offset+5]),
		K: uint16(p.Pix[offset+6])<<8 | uint16(p.Pix[offset+7]),
	}
}

// PixOffset returns the index of the first byte of the pixel at (x, y).
func (p *CMYK64) PixOffset(x, y int) int {
	return (y-p.Rect.Min.Y)*p.Stride + (x-p.Rect.Min.X)*8
}

// Set implements draw.Image.
func (p *CMYK64) Set(x, y int, value color.Color) {
	if !(image.Point{X: x, Y: y}.In(p.Rect)) {
		return
	}
	p.SetCMYK64(x, y, CMYK64Model.Convert(value).(CMYK64Color))
}

// SetCMYK64 sets the pixel at (x, y). It has no effect outside the image
// bounds.
func (p *CMYK64) SetCMYK64(x, y int, value CMYK64Color) {
	if !(image.Point{X: x, Y: y}.In(p.Rect)) {
		return
	}
	offset := p.PixOffset(x, y)
	p.Pix[offset] = uint8(value.C >> 8)
	p.Pix[offset+1] = uint8(value.C)
	p.Pix[offset+2] = uint8(value.M >> 8)
	p.Pix[offset+3] = uint8(value.M)
	p.Pix[offset+4] = uint8(value.Y >> 8)
	p.Pix[offset+5] = uint8(value.Y)
	p.Pix[offset+6] = uint8(value.K >> 8)
	p.Pix[offset+7] = uint8(value.K)
}

// Opaque reports whether the image is fully opaque. CMYK64 has no alpha
// channel, so it always reports true.
func (p *CMYK64) Opaque() bool { return true }

// SubImage returns an image representing the portion of p visible through r.
// The returned image shares pixels with the original image.
func (p *CMYK64) SubImage(r image.Rectangle) image.Image {
	r = r.Intersect(p.Rect)
	if r.Empty() {
		return &CMYK64{}
	}
	offset := p.PixOffset(r.Min.X, r.Min.Y)
	return &CMYK64{Pix: p.Pix[offset:], Stride: p.Stride, Rect: r}
}
