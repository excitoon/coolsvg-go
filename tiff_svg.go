package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
)

// TIFFRenderOptions controls direct SvgIcon rendering and TIFF encoding. The
// embedded TIFFOptions selects the file format. Format Auto means RGBA so SVG
// transparency is preserved. RGB output is composited onto Background, or
// opaque white when Background is nil. For RGBA, a nil Background remains
// transparent; a non-nil background is composited behind the SVG with its
// alpha preserved. CMYKOptions controls separation and its own Background;
// CMYK output is necessarily opaque. TIFFOptions.Dither is authoritative;
// when it is zero, a nonzero CMYKOptions.Dither is inherited for convenience.
// Supplying two different nonzero dither settings is an error.
type TIFFRenderOptions struct {
	// TIFFOptions controls the encoded TIFF container and quantization.
	TIFFOptions
	// Background is used for RGB/RGBA rendering as described above.
	Background color.Color
	// CMYKOptions controls separation, processing mode, and the opaque CMYK
	// background. Its Dither field is inherited only when TIFFOptions.Dither
	// does not select a dither mode.
	CMYKOptions CMYKOptions
}

// EncodeSVGToTIFF renders icon directly into bounded full-width strips and
// encodes them as TIFF. It retains floating-point render precision until the
// TIFF encoder performs the requested 8- or 16-bit quantization. It is suitable
// for very large bounds because it never materializes the complete raster.
// Call SetTarget before encoding to map the SVG viewBox to bounds; bounds do
// not implicitly change the icon's target transform. A non-seekable compressed
// destination causes each compressed strip to be rendered twice for
// deterministic size planning. Forced classic compressed output can require
// the same counting pass even for a seekable destination. Both paths retain
// only one strip at a time.
func EncodeSVGToTIFF(w io.Writer, icon *SvgIcon, bounds image.Rectangle, options *TIFFRenderOptions) error {
	if icon == nil || icon.root == nil {
		return fmt.Errorf("svg: cannot encode a nil icon as TIFF")
	}
	width, height, err := tiffRectangleDimensions(bounds)
	if err != nil {
		return fmt.Errorf("svg: %w", err)
	}
	var value TIFFRenderOptions
	if options != nil {
		value = *options
		value.TIFFOptions.ICCProfile = append([]byte(nil), options.TIFFOptions.ICCProfile...)
	}
	format := value.TIFFOptions.Format
	if format == TIFFPixelFormatAuto {
		format = TIFFPixelFormatRGBA
	}
	if format < TIFFPixelFormatRGB || format > TIFFPixelFormatCMYK {
		return fmt.Errorf("svg: unsupported TIFF pixel format %d", format)
	}
	if value.TIFFOptions.RowsPerStrip < 0 {
		return fmt.Errorf("svg: TIFF rows per strip cannot be negative")
	}
	if format == TIFFPixelFormatCMYK {
		outerDither := value.TIFFOptions.Dither
		cmykDither := value.CMYKOptions.Dither
		outerSet := outerDither.Mode != DitherNone
		cmykSet := cmykDither.Mode != DitherNone
		if outerSet && cmykSet && outerDither != cmykDither {
			return fmt.Errorf("svg: conflicting TIFF and CMYK dither options")
		}
		if !outerSet && cmykSet {
			value.TIFFOptions.Dither = cmykDither
		}
		value.CMYKOptions.Dither = DitherOptions{}
	}
	if value.TIFFOptions.RowsPerStrip == 0 {
		// The CMYK post-conversion path temporarily holds both RGBA and CMYK
		// float surfaces (nine float64 channels). Size for that worst case so a
		// 100k-wide strip remains a tens-of-MiB operation rather than approaching
		// a GiB.
		workingChannels := 4
		if format == TIFFPixelFormatCMYK {
			workingChannels = 9
		}
		value.TIFFOptions.RowsPerStrip = svgTIFFRowsPerStrip(width, height, workingChannels)
	} else {
		workingChannels := 4
		if format == TIFFPixelFormatCMYK {
			workingChannels = 9
		}
		actualRows := min(value.TIFFOptions.RowsPerStrip, height)
		workingBytes, ok := multiplyTIFFUint64(uint64(width), uint64(actualRows), uint64(workingChannels), 8)
		if !ok || workingBytes > 128<<20 {
			return fmt.Errorf("svg: TIFF render strip working set exceeds 128 MiB; reduce RowsPerStrip")
		}
	}
	source, err := newSvgTIFFStripSource(icon, bounds, format, value.Background, value.CMYKOptions)
	if err != nil {
		return err
	}
	return EncodeTIFFStrips(w, source, &value.TIFFOptions)
}

// EncodeTIFF is the method form of EncodeSVGToTIFF.
func (s *SvgIcon) EncodeTIFF(w io.Writer, bounds image.Rectangle, options *TIFFRenderOptions) error {
	return EncodeSVGToTIFF(w, s, bounds, options)
}

type svgTIFFStripSource struct {
	icon       *SvgIcon
	bounds     image.Rectangle
	format     TIFFPixelFormat
	background color.Color
	cmyk       CMYKOptions
	converter  cmykFloatConverter
}

func newSvgTIFFStripSource(icon *SvgIcon, bounds image.Rectangle, format TIFFPixelFormat, background color.Color, cmyk CMYKOptions) (*svgTIFFStripSource, error) {
	source := &svgTIFFStripSource{
		icon: icon, bounds: bounds, format: format, background: background, cmyk: cmyk,
	}
	if format == TIFFPixelFormatCMYK {
		if cmyk.ProcessingMode > CMYKNative {
			return nil, fmt.Errorf("svg: unsupported CMYK processing mode %d", cmyk.ProcessingMode)
		}
		converter, err := resolveCMYKFloatConverter(cmyk)
		if err != nil {
			return nil, err
		}
		source.converter = converter
	}
	return source, nil
}

func (s *svgTIFFStripSource) TIFFBounds() image.Rectangle { return s.bounds }
func (s *svgTIFFStripSource) TIFFFormat() TIFFPixelFormat { return s.format }
func (s *svgTIFFStripSource) TIFFSampleDepth() int        { return 32 }

func (s *svgTIFFStripSource) TIFFReadStrip(rect image.Rectangle, dst []uint32) error {
	if rect.Empty() || rect.Min.X != s.bounds.Min.X || rect.Max.X != s.bounds.Max.X || !rect.In(s.bounds) {
		return fmt.Errorf("svg: invalid TIFF render strip %v for bounds %v", rect, s.bounds)
	}
	want, ok := checkedMulInt(rect.Dx(), rect.Dy(), tiffSamplesPerPixel(s.format))
	if !ok || len(dst) != want {
		return fmt.Errorf("svg: TIFF render strip buffer has length %d, want %d", len(dst), want)
	}
	if s.format == TIFFPixelFormatCMYK {
		return s.readCMYKStrip(rect, dst)
	}
	return s.readRGBStrip(rect, dst)
}

func (s *svgTIFFStripSource) readRGBStrip(rect image.Rectangle, dst []uint32) error {
	surface, err := newRGBAFloatSurface(rect)
	if err != nil {
		return err
	}
	background := rgbaFloat{}
	if s.format == TIFFPixelFormatRGB {
		background = opaqueRGBBackground(s.background)
	} else if !nilColor(s.background) {
		value := color.NRGBA64Model.Convert(s.background).(color.NRGBA64)
		background = rgbaFloat{
			r: float64(value.R) / 0xffff,
			g: float64(value.G) / 0xffff,
			b: float64(value.B) / 0xffff,
			a: float64(value.A) / 0xffff,
		}
	}
	if background != (rgbaFloat{}) {
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			for x := rect.Min.X; x < rect.Max.X; x++ {
				surface.set(x, y, background)
			}
		}
	}
	renderer := newRGBAFloatRenderer(s.icon, rect)
	if err := renderer.renderNode(surface, s.icon.root, defaultComputedStyle(), s.icon.target, s.icon.ViewBox, 1); err != nil {
		return err
	}
	offset := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			value := surface.rgbaAt(x, y)
			dst[offset+0] = normalizedTIFFSample(value.r)
			dst[offset+1] = normalizedTIFFSample(value.g)
			dst[offset+2] = normalizedTIFFSample(value.b)
			if s.format == TIFFPixelFormatRGBA {
				dst[offset+3] = normalizedTIFFSample(value.a)
				offset += 4
			} else {
				offset += 3
			}
		}
	}
	return nil
}

func (s *svgTIFFStripSource) readCMYKStrip(rect image.Rectangle, dst []uint32) error {
	var surface *cmykFloatSurface
	var err error
	if s.cmyk.ProcessingMode == CMYKNative {
		surface, err = s.icon.renderCMYKNativeFloatBand(rect, s.cmyk.Background, s.converter)
	} else {
		surface, err = s.icon.renderCMYKPostConvertFloatBand(rect, s.cmyk.Background, s.converter)
	}
	if err != nil {
		return err
	}
	offset := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			value := surface.cmykAt(x, y)
			dst[offset+0] = normalizedTIFFSample(value.c)
			dst[offset+1] = normalizedTIFFSample(value.m)
			dst[offset+2] = normalizedTIFFSample(value.y)
			dst[offset+3] = normalizedTIFFSample(value.k)
			offset += 4
		}
	}
	return nil
}

func opaqueRGBBackground(background color.Color) rgbaFloat {
	if nilColor(background) {
		return rgbaFloat{r: 1, g: 1, b: 1, a: 1}
	}
	value := color.NRGBA64Model.Convert(background).(color.NRGBA64)
	alpha := float64(value.A) / 0xffff
	return rgbaFloat{
		r: float64(value.R)/0xffff*alpha + 1 - alpha,
		g: float64(value.G)/0xffff*alpha + 1 - alpha,
		b: float64(value.B)/0xffff*alpha + 1 - alpha,
		a: 1,
	}
}

func normalizedTIFFSample(value float64) uint32 {
	value = clampUnit(value)
	return uint32(math.Floor(value*float64(math.MaxUint32) + 0.5))
}

func svgTIFFRowsPerStrip(width, height, channels int) int {
	if width <= 0 || height <= 0 || channels <= 0 {
		return 1
	}
	bytesPerRow, ok := checkedMulInt(width, channels, 8)
	if !ok || bytesPerRow >= precisionWorkingSetBytes {
		return 1
	}
	return min(height, max(1, precisionWorkingSetBytes/bytesPerRow))
}
