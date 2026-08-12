package coolsvg

import (
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
)

const (
	maxTIFFStripCount       = 1 << 20
	maxTIFFStripSampleBytes = 64 << 20
)

// TIFFPixelFormat describes the interleaved samples written for each pixel.
type TIFFPixelFormat uint8

const (
	// TIFFPixelFormatAuto infers CMYK, RGB, or RGBA from the source. It is only
	// valid in TIFFOptions; TIFFStripSource and TIFFSampleImage must report a
	// concrete format.
	TIFFPixelFormatAuto TIFFPixelFormat = iota
	// TIFFPixelFormatRGB writes three additive color samples.
	TIFFPixelFormatRGB
	// TIFFPixelFormatRGBA writes RGB plus unassociated (straight) alpha.
	TIFFPixelFormatRGBA
	// TIFFPixelFormatCMYK writes four opaque process-color samples.
	TIFFPixelFormatCMYK
)

// TIFFCompression selects strip compression.
type TIFFCompression uint8

const (
	// TIFFCompressionDefault selects Deflate compression.
	TIFFCompressionDefault TIFFCompression = iota
	// TIFFCompressionNone writes uncompressed strips.
	TIFFCompressionNone
	// TIFFCompressionDeflate writes each strip as an independent zlib stream.
	TIFFCompressionDeflate
	// TIFFCompressionPackBits writes TIFF PackBits-compressed strips.
	TIFFCompressionPackBits
)

// TIFFPredictor selects preprocessing applied before strip compression.
type TIFFPredictor uint8

const (
	// TIFFPredictorDefault selects horizontal differencing for Deflate and no
	// predictor for uncompressed or PackBits output.
	TIFFPredictorDefault TIFFPredictor = iota
	// TIFFPredictorNone disables prediction.
	TIFFPredictorNone
	// TIFFPredictorHorizontal subtracts the preceding pixel independently in
	// each channel and resets at every scanline.
	TIFFPredictorHorizontal
)

// TIFFVariant selects the TIFF offset format.
type TIFFVariant uint8

const (
	// TIFFVariantAuto writes classic TIFF when it can be proven to fit and
	// BigTIFF otherwise.
	TIFFVariantAuto TIFFVariant = iota
	// TIFFVariantClassic forces 32-bit classic TIFF offsets.
	TIFFVariantClassic
	// TIFFVariantBigTIFF forces 64-bit BigTIFF offsets.
	TIFFVariantBigTIFF
)

// TIFFByteOrder selects the byte order of TIFF fields and 16-bit samples.
type TIFFByteOrder uint8

const (
	// TIFFByteOrderDefault selects little-endian output.
	TIFFByteOrderDefault TIFFByteOrder = iota
	// TIFFByteOrderLittle selects little-endian output.
	TIFFByteOrderLittle
	// TIFFByteOrderBig selects big-endian output.
	TIFFByteOrderBig
)

// TIFFOptions controls TIFF encoding. Its zero value is valid and selects an
// inferred pixel format and bit depth, Deflate compression, automatic strip
// sizing, automatic classic/BigTIFF selection, and little-endian output.
type TIFFOptions struct {
	// Format selects RGB, RGBA, or CMYK output. Auto infers a format from an
	// image source and uses the format declared by a TIFFStripSource. Explicit
	// RGB or CMYK conversion uses straight color channels and discards source
	// alpha; callers that need flattening should composite a background first.
	Format TIFFPixelFormat
	// BitsPerSample must be 0, 8, or 16. Zero preserves a recognized 16-bit
	// source and otherwise selects 8 bits.
	BitsPerSample int
	// Compression selects strip compression. The zero value uses Deflate.
	Compression TIFFCompression
	// DeflateLevel controls Deflate compression. Zero uses zlib's default.
	// Other accepted values are zlib.HuffmanOnly, zlib.DefaultCompression,
	// and 1 through 9.
	DeflateLevel int
	// Predictor controls horizontal differencing. The zero value enables it
	// for Deflate, where it materially improves smooth-gradient compression.
	Predictor TIFFPredictor
	// RowsPerStrip controls source working-set size. Zero automatically targets
	// approximately 16 MiB for a full-width, float64-channel source strip.
	// Explicit values that require more than 64 MiB of uint32 source samples
	// are rejected; use more, smaller strips instead.
	RowsPerStrip int
	// Variant selects classic TIFF or BigTIFF. Auto never emits a classic file
	// whose offsets or byte counts might overflow 32 bits.
	Variant TIFFVariant
	// ByteOrder selects file byte order. The zero value is little-endian.
	ByteOrder TIFFByteOrder
	// Dither controls precision reduction. Dithering is applied only when the
	// declared source precision is greater than BitsPerSample. Alpha is never
	// dithered. A supplied FloydSteinbergState is updated transactionally only
	// after the complete file has been written successfully.
	Dither DitherOptions
	// ICCProfile, when non-empty, is copied into TIFF tag 34675. The encoder
	// does not inspect or transform the profile.
	ICCProfile []byte
}

// TIFFSampleImage exposes integer samples with their real source precision.
// This is useful for sources with more than 16 bits of working precision.
// TIFFSamplesAt returns tightly interpreted channels in TIFFFormat order;
// unused array elements are ignored. For depth d, every used value must be in
// [0, 2^d-1]. Implementations must return a concrete TIFFFormat and a depth
// from 1 through 32.
type TIFFSampleImage interface {
	image.Image
	TIFFFormat() TIFFPixelFormat
	TIFFSampleDepth() int
	TIFFSamplesAt(x, y int) [4]uint32
}

// TIFFStripSource supplies bounded full-width strips without allocating a
// complete raster. TIFFReadStrip must fill dst with tightly packed,
// row-major uint32 samples in TIFFFormat order for exactly rect. The encoder
// may call it twice for a non-seekable compressed destination. Forced classic
// TIFF may also require a counting pass when the conservative compressed-size
// bound does not fit 32-bit offsets. Repeated calls for the same rectangle must
// therefore be deterministic. Values use the unsigned range declared by
// TIFFSampleDepth, which must be from 1 through 32.
type TIFFStripSource interface {
	TIFFBounds() image.Rectangle
	TIFFFormat() TIFFPixelFormat
	TIFFSampleDepth() int
	TIFFReadStrip(rect image.Rectangle, dst []uint32) error
}

// ErrTIFFClassicTooLarge is returned when forced classic TIFF output would
// overflow a 32-bit offset, count, dimension, or IFD field. Use BigTIFF or the
// automatic variant for such images.
var ErrTIFFClassicTooLarge = errors.New("coolsvg: classic TIFF exceeds 32-bit limits")

// EncodeTIFF encodes an image as RGB, RGBA, or CMYK TIFF. Explicit conversion
// from an alpha-bearing image to RGB or CMYK retains its straight color and
// discards alpha; composite a background first when flattening is required. It
// streams bounded strips. A seekable destination is normally encoded in one
// image pass; forced classic compressed output may need a counting pass to
// prove it fits. A non-seekable destination uses a deterministic counting pass
// for compressed strips and never retains the full raster or all compressed
// strips.
func EncodeTIFF(w io.Writer, src image.Image, options *TIFFOptions) error {
	if w == nil || nilInterface(w) {
		return fmt.Errorf("coolsvg: TIFF destination is nil")
	}
	if src == nil || nilInterface(src) {
		return fmt.Errorf("coolsvg: TIFF source image is nil")
	}
	configured, err := normalizeTIFFOptions(options)
	if err != nil {
		return err
	}
	adapter, err := newImageTIFFSource(src, configured.format)
	if err != nil {
		return err
	}
	return encodeTIFFSource(w, adapter, configured)
}

// EncodeTIFFStrips encodes a streaming strip source. It is the direct export
// path for renderers that cannot materialize a complete large canvas.
func EncodeTIFFStrips(w io.Writer, src TIFFStripSource, options *TIFFOptions) error {
	if w == nil || nilInterface(w) {
		return fmt.Errorf("coolsvg: TIFF destination is nil")
	}
	if src == nil || nilInterface(src) {
		return fmt.Errorf("coolsvg: TIFF strip source is nil")
	}
	configured, err := normalizeTIFFOptions(options)
	if err != nil {
		return err
	}
	return encodeTIFFSource(w, src, configured)
}

type tiffConfig struct {
	format        TIFFPixelFormat
	bits          int
	compression   TIFFCompression
	deflateLevel  int
	predictor     TIFFPredictor
	rowsPerStrip  int
	variant       TIFFVariant
	order         binary.ByteOrder
	dither        DitherOptions
	iccProfile    []byte
	formatWasAuto bool
}

func normalizeTIFFOptions(options *TIFFOptions) (tiffConfig, error) {
	var value TIFFOptions
	if options != nil {
		value = *options
	}
	if value.Format > TIFFPixelFormatCMYK {
		return tiffConfig{}, fmt.Errorf("coolsvg: unsupported TIFF pixel format %d", value.Format)
	}
	if err := validateDither(value.Dither); err != nil {
		return tiffConfig{}, fmt.Errorf("coolsvg: TIFF dithering: %w", err)
	}
	if value.BitsPerSample != 0 && value.BitsPerSample != 8 && value.BitsPerSample != 16 {
		return tiffConfig{}, fmt.Errorf("coolsvg: TIFF bits per sample must be 8 or 16, got %d", value.BitsPerSample)
	}
	compression := value.Compression
	if compression == TIFFCompressionDefault {
		compression = TIFFCompressionDeflate
	}
	if compression < TIFFCompressionNone || compression > TIFFCompressionPackBits {
		return tiffConfig{}, fmt.Errorf("coolsvg: unsupported TIFF compression %d", value.Compression)
	}
	level := value.DeflateLevel
	if level == 0 {
		level = zlib.DefaultCompression
	}
	if level < zlib.HuffmanOnly || level > zlib.BestCompression || level == zlib.NoCompression {
		return tiffConfig{}, fmt.Errorf("coolsvg: unsupported TIFF Deflate level %d", value.DeflateLevel)
	}
	predictor := value.Predictor
	if predictor == TIFFPredictorDefault {
		if compression == TIFFCompressionDeflate {
			predictor = TIFFPredictorHorizontal
		} else {
			predictor = TIFFPredictorNone
		}
	}
	if predictor < TIFFPredictorNone || predictor > TIFFPredictorHorizontal {
		return tiffConfig{}, fmt.Errorf("coolsvg: unsupported TIFF predictor %d", value.Predictor)
	}
	if predictor == TIFFPredictorHorizontal && compression != TIFFCompressionDeflate {
		return tiffConfig{}, fmt.Errorf("coolsvg: TIFF horizontal predictor requires Deflate compression")
	}
	if value.RowsPerStrip < 0 {
		return tiffConfig{}, fmt.Errorf("coolsvg: TIFF rows per strip cannot be negative")
	}
	if value.Variant > TIFFVariantBigTIFF {
		return tiffConfig{}, fmt.Errorf("coolsvg: unsupported TIFF variant %d", value.Variant)
	}
	var order binary.ByteOrder = binary.LittleEndian
	switch value.ByteOrder {
	case TIFFByteOrderDefault, TIFFByteOrderLittle:
	case TIFFByteOrderBig:
		order = binary.BigEndian
	default:
		return tiffConfig{}, fmt.Errorf("coolsvg: unsupported TIFF byte order %d", value.ByteOrder)
	}
	return tiffConfig{
		format:        value.Format,
		bits:          value.BitsPerSample,
		compression:   compression,
		deflateLevel:  level,
		predictor:     predictor,
		rowsPerStrip:  value.RowsPerStrip,
		variant:       value.Variant,
		order:         order,
		dither:        value.Dither,
		iccProfile:    append([]byte(nil), value.ICCProfile...),
		formatWasAuto: value.Format == TIFFPixelFormatAuto,
	}, nil
}

type imageTIFFSource struct {
	image  image.Image
	custom TIFFSampleImage
	bounds image.Rectangle
	format TIFFPixelFormat
	depth  int
}

func newImageTIFFSource(src image.Image, requested TIFFPixelFormat) (*imageTIFFSource, error) {
	adapter := &imageTIFFSource{image: src, bounds: src.Bounds()}
	if custom, ok := src.(TIFFSampleImage); ok {
		adapter.custom = custom
		adapter.format = custom.TIFFFormat()
		adapter.depth = custom.TIFFSampleDepth()
		if requested != TIFFPixelFormatAuto && requested != adapter.format {
			return nil, fmt.Errorf("coolsvg: TIFF option format %d does not match sample image format %d", requested, adapter.format)
		}
		return adapter, nil
	}

	adapter.format = requested
	if adapter.format == TIFFPixelFormatAuto {
		switch src.(type) {
		case *image.CMYK, *CMYK64:
			adapter.format = TIFFPixelFormatCMYK
		case *image.NRGBA, *image.RGBA, *image.NRGBA64, *image.RGBA64:
			adapter.format = TIFFPixelFormatRGBA
		default:
			if src.ColorModel() == color.CMYKModel {
				adapter.format = TIFFPixelFormatCMYK
			} else if opaque, ok := src.(interface{ Opaque() bool }); ok && opaque.Opaque() {
				adapter.format = TIFFPixelFormatRGB
			} else {
				adapter.format = TIFFPixelFormatRGBA
			}
		}
	}

	if adapter.format < TIFFPixelFormatRGB || adapter.format > TIFFPixelFormatCMYK {
		return nil, fmt.Errorf("coolsvg: unsupported TIFF image format %d", adapter.format)
	}
	switch src.(type) {
	case *image.NRGBA64, *image.RGBA64, *image.Gray16, *image.Alpha16, *CMYK64:
		adapter.depth = 16
	default:
		// The storage precision of an arbitrary image.Image cannot be inferred
		// from color.Color.RGBA, which always exposes 16-bit-scaled values.
		// Unrecognized sources therefore follow the documented 8-bit default;
		// custom high-precision sources can implement TIFFSampleImage.
		adapter.depth = 8
	}
	return adapter, nil
}

func (s *imageTIFFSource) TIFFBounds() image.Rectangle { return s.bounds }
func (s *imageTIFFSource) TIFFFormat() TIFFPixelFormat { return s.format }
func (s *imageTIFFSource) TIFFSampleDepth() int        { return s.depth }

func (s *imageTIFFSource) TIFFReadStrip(rect image.Rectangle, dst []uint32) error {
	if rect.Empty() || rect.Min.X != s.bounds.Min.X || rect.Max.X != s.bounds.Max.X || !rect.In(s.bounds) {
		return fmt.Errorf("coolsvg: invalid TIFF source strip %v for bounds %v", rect, s.bounds)
	}
	samples := tiffSamplesPerPixel(s.format)
	want, ok := checkedMulInt(rect.Dx(), rect.Dy(), samples)
	if !ok || len(dst) != want {
		return fmt.Errorf("coolsvg: TIFF source strip buffer has length %d, want %d", len(dst), want)
	}
	offset := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if s.custom != nil {
				value := s.custom.TIFFSamplesAt(x, y)
				copy(dst[offset:offset+samples], value[:samples])
				offset += samples
				continue
			}
			switch s.format {
			case TIFFPixelFormatCMYK:
				if source, ok := s.image.(*CMYK64); ok {
					value := source.CMYK64At(x, y)
					dst[offset+0] = uint32(value.C)
					dst[offset+1] = uint32(value.M)
					dst[offset+2] = uint32(value.Y)
					dst[offset+3] = uint32(value.K)
				} else if value, ok := s.image.At(x, y).(color.CMYK); ok {
					dst[offset+0] = uint32(value.C)
					dst[offset+1] = uint32(value.M)
					dst[offset+2] = uint32(value.Y)
					dst[offset+3] = uint32(value.K)
				} else if s.depth == 16 {
					value := color.NRGBA64Model.Convert(s.image.At(x, y)).(color.NRGBA64)
					separated := deviceRGBToCMYK64(value.R, value.G, value.B)
					dst[offset+0] = uint32(separated.C)
					dst[offset+1] = uint32(separated.M)
					dst[offset+2] = uint32(separated.Y)
					dst[offset+3] = uint32(separated.K)
				} else {
					value := color.NRGBAModel.Convert(s.image.At(x, y)).(color.NRGBA)
					c, m, y, k := color.RGBToCMYK(value.R, value.G, value.B)
					dst[offset+0] = uint32(c)
					dst[offset+1] = uint32(m)
					dst[offset+2] = uint32(y)
					dst[offset+3] = uint32(k)
				}
			case TIFFPixelFormatRGB, TIFFPixelFormatRGBA:
				if s.depth == 8 {
					value := color.NRGBAModel.Convert(s.image.At(x, y)).(color.NRGBA)
					dst[offset+0] = uint32(value.R)
					dst[offset+1] = uint32(value.G)
					dst[offset+2] = uint32(value.B)
					if s.format == TIFFPixelFormatRGBA {
						dst[offset+3] = uint32(value.A)
					}
				} else {
					value := color.NRGBA64Model.Convert(s.image.At(x, y)).(color.NRGBA64)
					dst[offset+0] = uint32(value.R)
					dst[offset+1] = uint32(value.G)
					dst[offset+2] = uint32(value.B)
					if s.format == TIFFPixelFormatRGBA {
						dst[offset+3] = uint32(value.A)
					}
				}
			}
			offset += samples
		}
	}
	return nil
}

func encodeTIFFSource(w io.Writer, src TIFFStripSource, config tiffConfig) error {
	bounds := src.TIFFBounds()
	if bounds.Empty() {
		return fmt.Errorf("coolsvg: TIFF source bounds %v are empty", bounds)
	}
	format := src.TIFFFormat()
	if format < TIFFPixelFormatRGB || format > TIFFPixelFormatCMYK {
		return fmt.Errorf("coolsvg: TIFF strip source returned invalid format %d", format)
	}
	if config.formatWasAuto {
		config.format = format
	} else if config.format != format {
		return fmt.Errorf("coolsvg: TIFF option format %d does not match strip source format %d", config.format, format)
	}
	depth := src.TIFFSampleDepth()
	if depth < 1 || depth > 32 {
		return fmt.Errorf("coolsvg: TIFF source sample depth must be from 1 through 32, got %d", depth)
	}
	if config.bits == 0 {
		if depth > 8 {
			config.bits = 16
		} else {
			config.bits = 8
		}
	}
	width, height, err := tiffRectangleDimensions(bounds)
	if err != nil {
		return err
	}
	samples := tiffSamplesPerPixel(config.format)
	rows := config.rowsPerStrip
	if rows == 0 {
		const workingSet = 16 << 20
		rowWorking, ok := checkedMulInt(width, samples, 8)
		if !ok || rowWorking <= 0 || rowWorking >= workingSet {
			rows = 1
		} else {
			rows = workingSet / rowWorking
			if rows < 1 {
				rows = 1
			}
		}
	}
	if rows > height {
		rows = height
	}
	stripCount := 1 + (height-1)/rows
	if stripCount <= 0 {
		return fmt.Errorf("coolsvg: invalid TIFF strip count")
	}
	if stripCount > maxTIFFStripCount {
		return fmt.Errorf("coolsvg: TIFF requires %d strips; increase RowsPerStrip to keep the strip table at or below %d entries", stripCount, maxTIFFStripCount)
	}
	if config.variant == TIFFVariantClassic && (uint64(width) > math.MaxUint32 || uint64(height) > math.MaxUint32 || uint64(rows) > math.MaxUint32) {
		return fmt.Errorf("%w: image dimensions or rows per strip", ErrTIFFClassicTooLarge)
	}
	stripSampleCount, ok := multiplyTIFFUint64(uint64(width), uint64(rows), uint64(samples))
	if !ok || stripSampleCount > maxTIFFStripSampleBytes/4 {
		return fmt.Errorf("coolsvg: TIFF strip source buffer exceeds %d MiB; reduce RowsPerStrip", maxTIFFStripSampleBytes>>20)
	}
	if _, ok := checkedMulInt(width, samples, config.bits/8); !ok {
		return fmt.Errorf("coolsvg: TIFF scanline is too large")
	}

	job := &tiffJob{
		source:       src,
		bounds:       bounds,
		width:        width,
		height:       height,
		format:       config.format,
		sourceDepth:  depth,
		bits:         config.bits,
		samples:      samples,
		rowsPerStrip: rows,
		stripCount:   stripCount,
		compression:  config.compression,
		deflateLevel: config.deflateLevel,
		predictor:    config.predictor,
		order:        config.order,
		dither:       config.dither,
		iccProfile:   config.iccProfile,
	}
	if seeker, ok := w.(io.WriteSeeker); ok {
		base, err := seeker.Seek(0, io.SeekCurrent)
		if err == nil && base >= 0 {
			return job.encodeSeekable(seeker, base, config.variant)
		}
	}
	return job.encodeStreaming(w, config.variant)
}

func tiffSamplesPerPixel(format TIFFPixelFormat) int {
	switch format {
	case TIFFPixelFormatRGB:
		return 3
	case TIFFPixelFormatRGBA, TIFFPixelFormatCMYK:
		return 4
	default:
		return 0
	}
}

func checkedMulInt(values ...int) (int, bool) {
	result := 1
	for _, value := range values {
		if value < 0 || value != 0 && result > math.MaxInt/value {
			return 0, false
		}
		result *= value
	}
	return result, true
}

func tiffRectangleDimensions(bounds image.Rectangle) (int, int, error) {
	span := func(minimum, maximum int) (int, bool) {
		if maximum <= minimum {
			return 0, false
		}
		if minimum < 0 && maximum > math.MaxInt+minimum {
			return 0, false
		}
		return maximum - minimum, true
	}
	width, widthOK := span(bounds.Min.X, bounds.Max.X)
	height, heightOK := span(bounds.Min.Y, bounds.Max.Y)
	if !widthOK || !heightOK {
		return 0, 0, fmt.Errorf("coolsvg: TIFF bounds %v have an invalid or unrepresentable dimension span", bounds)
	}
	return width, height, nil
}

func multiplyTIFFUint64(values ...uint64) (uint64, bool) {
	result := uint64(1)
	for _, value := range values {
		if value != 0 && result > math.MaxUint64/value {
			return 0, false
		}
		result *= value
	}
	return result, true
}
