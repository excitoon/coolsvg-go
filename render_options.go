package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"math"
)

// RenderOptions controls high-precision rendering and destination
// quantization. Destination depth is selected from recognized standard color
// models. Custom images that expose draw.RGBA64Image with a custom model are
// treated as 16-bit; other unrecognized images are treated as 8-bit.
type RenderOptions struct {
	Dither DitherOptions
}

// RenderWithOptions renders at full opacity through the floating-point working
// pipeline and quantizes to the destination depth.
func (s *SvgIcon) RenderWithOptions(dst stddraw.Image, options RenderOptions) error {
	if dst == nil || nilInterface(dst) {
		return fmt.Errorf("svg: destination is nil")
	}
	return s.DrawRegionWithOptions(dst, dst.Bounds(), 1, options)
}

// DrawWithOptions renders with opacity through the floating-point working
// pipeline and quantizes to the destination depth.
func (s *SvgIcon) DrawWithOptions(dst stddraw.Image, opacity float64, options RenderOptions) error {
	if dst == nil || nilInterface(dst) {
		return fmt.Errorf("svg: destination is nil")
	}
	return s.DrawRegionWithOptions(dst, dst.Bounds(), opacity, options)
}

// RenderRegionWithOptions renders a global region through the floating-point
// working pipeline at full opacity.
func (s *SvgIcon) RenderRegionWithOptions(dst stddraw.Image, region image.Rectangle, options RenderOptions) error {
	return s.DrawRegionWithOptions(dst, region, 1, options)
}

// DrawRegionWithOptions renders a global region through the floating-point
// working pipeline. Floyd-Steinberg calls that share a state must use
// consecutive bands with the same global x-range.
func (s *SvgIcon) DrawRegionWithOptions(dst stddraw.Image, region image.Rectangle, opacity float64, options RenderOptions) error {
	if s == nil || s.root == nil {
		return fmt.Errorf("svg: cannot draw a nil icon")
	}
	if dst == nil || nilInterface(dst) {
		return fmt.Errorf("svg: destination is nil")
	}
	if !rectanglesHaveSameSize(dst.Bounds(), region) {
		return fmt.Errorf("svg: destination bounds %v and region %v must have the same dimensions", dst.Bounds(), region)
	}
	if math.IsNaN(opacity) || math.IsInf(opacity, 0) {
		return fmt.Errorf("svg: opacity must be finite")
	}
	if err := validateDither(options.Dither); err != nil {
		return err
	}
	opacity = clampUnit(opacity)
	if opacity == 0 || region.Empty() {
		return nil
	}
	targetBits := rgbaDestinationBits(dst)
	dither, externalState := isolateFloydSteinbergState(options.Dither)
	for minimumY := region.Min.Y; minimumY < region.Max.Y; {
		height := precisionBandHeight(region.Dx(), 8, region.Max.Y-minimumY)
		band := image.Rect(region.Min.X, minimumY, region.Max.X, minimumY+height)
		surface, err := newRGBAFloatSurface(band)
		if err != nil {
			return err
		}
		loadRGBAFloatDestination(surface, dst, region)
		renderer := newRGBAFloatRenderer(s, band)
		if err := renderer.renderNode(surface, s.root, defaultComputedStyle(), s.target, s.ViewBox, opacity); err != nil {
			return err
		}
		if err := quantizeRGBAFloatSurface(dst, region, surface, targetBits, dither); err != nil {
			return err
		}
		minimumY = band.Max.Y
	}
	commitFloydSteinbergState(externalState, dither.State)
	return nil
}

// Render64 renders into a 16-bit RGBA destination using floating-point working
// precision until final quantization.
func (s *SvgIcon) Render64(dst stddraw.RGBA64Image, options RenderOptions) error {
	if dst == nil || nilInterface(dst) {
		return fmt.Errorf("svg: destination is nil")
	}
	if rgbaDestinationBits(dst) != 16 {
		return fmt.Errorf("svg: Render64 requires a 16-bit destination")
	}
	return s.RenderWithOptions(dst, options)
}

// Render64Region renders a global region into a 16-bit RGBA destination.
func (s *SvgIcon) Render64Region(dst stddraw.RGBA64Image, region image.Rectangle, options RenderOptions) error {
	if dst == nil || nilInterface(dst) {
		return fmt.Errorf("svg: destination is nil")
	}
	if rgbaDestinationBits(dst) != 16 {
		return fmt.Errorf("svg: Render64Region requires a 16-bit destination")
	}
	return s.RenderRegionWithOptions(dst, region, options)
}

const (
	precisionWorkingSetBytes   = 16 << 20
	precisionMaxSurfaceBytes   = 128 << 20
	precisionMaxLiveLayerBytes = 128 << 20
)

type precisionSurfaceBudget struct {
	liveBytes    int
	maximumBytes int
}

func newPrecisionSurfaceBudget() precisionSurfaceBudget {
	return precisionSurfaceBudget{maximumBytes: precisionMaxLiveLayerBytes}
}

func (b *precisionSurfaceBudget) reserve(elements int) (int, error) {
	if elements < 0 || elements > int(^uint(0)>>1)/8 {
		return 0, fmt.Errorf("svg: floating-point layer size overflows")
	}
	if b.maximumBytes == 0 {
		b.maximumBytes = precisionMaxLiveLayerBytes
	}
	bytes := elements * 8
	if bytes > b.maximumBytes-b.liveBytes {
		return 0, fmt.Errorf("svg: live floating-point layers exceed %d MiB", b.maximumBytes>>20)
	}
	b.liveBytes += bytes
	return bytes, nil
}

func (b *precisionSurfaceBudget) release(bytes int) {
	if bytes <= 0 {
		return
	}
	if bytes >= b.liveBytes {
		b.liveBytes = 0
		return
	}
	b.liveBytes -= bytes
}

func precisionBandHeight(width, channels, remaining int) int {
	if remaining <= 0 {
		return 0
	}
	if width <= 0 || channels <= 0 {
		return remaining
	}
	maximum := int(^uint(0) >> 1)
	if channels > maximum/8 || width > maximum/(channels*8) {
		return 1
	}
	bytesPerRow := width * channels * 8
	height := precisionWorkingSetBytes / bytesPerRow
	if height < 1 {
		height = 1
	}
	return min(height, remaining)
}

func rgbaDestinationBits(dst image.Image) int {
	model := dst.ColorModel()
	if model == color.RGBA64Model || model == color.NRGBA64Model || model == color.Gray16Model || model == color.Alpha16Model {
		return 16
	}
	if model == color.RGBAModel || model == color.NRGBAModel || model == color.GrayModel || model == color.AlphaModel || model == color.CMYKModel {
		return 8
	}
	if _, paletted := model.(color.Palette); paletted {
		return 8
	}
	// A custom model has no standard depth marker. Prefer the explicit
	// high-precision drawing capability when it is available.
	if _, ok := dst.(stddraw.RGBA64Image); ok {
		return 16
	}
	return 8
}

func loadRGBAFloatDestination(surface *rgbaFloatSurface, dst stddraw.Image, region image.Rectangle) {
	destinationBounds := dst.Bounds()
	destinationBits := rgbaDestinationBits(dst)
	for y := surface.bounds.Min.Y; y < surface.bounds.Max.Y; y++ {
		localY := destinationBounds.Min.Y + y - region.Min.Y
		for x := surface.bounds.Min.X; x < surface.bounds.Max.X; x++ {
			localX := destinationBounds.Min.X + x - region.Min.X
			var value rgbaFloat
			if source, ok := dst.(interface {
				NRGBA64At(x, y int) color.NRGBA64
			}); ok {
				pixel := source.NRGBA64At(localX, localY)
				value = rgbaFloat{
					r: float64(pixel.R) / 0xffff, g: float64(pixel.G) / 0xffff,
					b: float64(pixel.B) / 0xffff, a: float64(pixel.A) / 0xffff,
				}
			} else if source, ok := dst.(image.RGBA64Image); ok {
				pixel := source.RGBA64At(localX, localY)
				alpha := float64(pixel.A) / 0xffff
				value.a = alpha
				if alpha > 0 {
					value.r = clampUnit(float64(pixel.R) / 0xffff / alpha)
					value.g = clampUnit(float64(pixel.G) / 0xffff / alpha)
					value.b = clampUnit(float64(pixel.B) / 0xffff / alpha)
				}
			} else if destinationBits == 16 {
				pixel := color.NRGBA64Model.Convert(dst.At(localX, localY)).(color.NRGBA64)
				value = rgbaFloat{
					r: float64(pixel.R) / 0xffff, g: float64(pixel.G) / 0xffff,
					b: float64(pixel.B) / 0xffff, a: float64(pixel.A) / 0xffff,
				}
			} else {
				pixel := color.NRGBAModel.Convert(dst.At(localX, localY)).(color.NRGBA)
				value = nrgbaToFloat(pixel, float64(pixel.A)/255)
			}
			surface.set(x, y, value)
		}
	}
}

func quantizeRGBAFloatSurface(dst stddraw.Image, region image.Rectangle, surface *rgbaFloatSurface, targetBits int, options DitherOptions) error {
	effectiveBounds := surface.bounds
	if options.Mode != DitherNone {
		var err error
		effectiveBounds, err = offsetDitherBounds(surface.bounds, options.CoordinateOffset)
		if err != nil {
			return fmt.Errorf("svg: %w", err)
		}
	}
	var state *FloydSteinbergState
	if options.Mode == DitherFloydSteinberg {
		state = options.State
		if err := state.begin(effectiveBounds, 3, targetBits); err != nil {
			return err
		}
		defer state.end(effectiveBounds.Max.Y)
	}
	width := surface.bounds.Dx()
	samples := make([]float64, width*3)
	quantized := make([]uint16, width*3)
	destinationBounds := dst.Bounds()
	for y := surface.bounds.Min.Y; y < surface.bounds.Max.Y; y++ {
		globalY := effectiveBounds.Min.Y + (y - surface.bounds.Min.Y)
		for x := surface.bounds.Min.X; x < surface.bounds.Max.X; x++ {
			value := surface.rgbaAt(x, y)
			index := (x - surface.bounds.Min.X) * 3
			samples[index], samples[index+1], samples[index+2] = value.r, value.g, value.b
			if options.Mode == DitherNone {
				quantized[index] = quantizeNearest(value.r, targetBits)
				quantized[index+1] = quantizeNearest(value.g, targetBits)
				quantized[index+2] = quantizeNearest(value.b, targetBits)
			} else if options.Mode == DitherBayer8x8 {
				globalX := effectiveBounds.Min.X + (x - surface.bounds.Min.X)
				quantized[index] = quantizeBayer(value.r, targetBits, globalX, globalY)
				quantized[index+1] = quantizeBayer(value.g, targetBits, globalX, globalY)
				quantized[index+2] = quantizeBayer(value.b, targetBits, globalX, globalY)
			}
		}
		if state != nil {
			state.quantizeRow(samples, globalY, targetBits, quantized)
		}
		for x := surface.bounds.Min.X; x < surface.bounds.Max.X; x++ {
			value := surface.rgbaAt(x, y)
			index := (x - surface.bounds.Min.X) * 3
			alpha := quantizeNearest(value.a, targetBits)
			localX := destinationBounds.Min.X + x - region.Min.X
			localY := destinationBounds.Min.Y + y - region.Min.Y
			setQuantizedRGBA(dst, localX, localY, quantized[index], quantized[index+1], quantized[index+2], alpha, targetBits)
		}
	}
	return nil
}

func setQuantizedRGBA(dst stddraw.Image, x, y int, r, g, b, a uint16, targetBits int) {
	if targetBits == 16 {
		if destination, ok := dst.(interface {
			SetNRGBA64(x, y int, value color.NRGBA64)
		}); ok {
			destination.SetNRGBA64(x, y, color.NRGBA64{R: r, G: g, B: b, A: a})
			return
		}
		alpha := uint32(a)
		premultiply := func(channel uint16) uint16 {
			return uint16((uint32(channel)*alpha + 0x7fff) / 0xffff)
		}
		value := color.RGBA64{R: premultiply(r), G: premultiply(g), B: premultiply(b), A: a}
		if destination, ok := dst.(stddraw.RGBA64Image); ok {
			destination.SetRGBA64(x, y, value)
		} else {
			dst.Set(x, y, value)
		}
		return
	}
	value := color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)}
	if destination, ok := dst.(interface {
		SetNRGBA(x, y int, value color.NRGBA)
	}); ok {
		destination.SetNRGBA(x, y, value)
		return
	}
	dst.Set(x, y, value)
}
