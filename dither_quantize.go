package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
)

// QuantizeRGBA64 converts a 16-bit RGBA image to an 8-bit destination. Bayer
// phase uses src.Bounds plus CoordinateOffset. A shared Floyd-Steinberg state
// permits consecutive full-width source strips. Dithering applies to RGB;
// alpha uses nearest rounding.
func QuantizeRGBA64(dst stddraw.Image, src image.RGBA64Image, options DitherOptions) error {
	if dst == nil || src == nil || nilInterface(dst) || nilInterface(src) {
		return fmt.Errorf("svg: quantization source and destination must be non-nil")
	}
	if rgbaDestinationBits(dst) == 16 {
		return fmt.Errorf("svg: QuantizeRGBA64 requires an 8-bit destination")
	}
	if !rectanglesHaveSameSize(dst.Bounds(), src.Bounds()) {
		return fmt.Errorf("svg: destination bounds %v and source bounds %v must have the same dimensions", dst.Bounds(), src.Bounds())
	}
	if err := validateDither(options); err != nil {
		return err
	}
	bounds := src.Bounds()
	if bounds.Empty() {
		return nil
	}
	options, externalState := isolateFloydSteinbergState(options)
	for minimumY := bounds.Min.Y; minimumY < bounds.Max.Y; {
		height := precisionBandHeight(bounds.Dx(), 4, bounds.Max.Y-minimumY)
		band := image.Rect(bounds.Min.X, minimumY, bounds.Max.X, minimumY+height)
		surface, err := newRGBAFloatSurface(band)
		if err != nil {
			return err
		}
		loadRGBAFloatSource(surface, src)
		if err := quantizeRGBAFloatSurface(dst, bounds, surface, 8, options); err != nil {
			return err
		}
		minimumY = band.Max.Y
	}
	commitFloydSteinbergState(externalState, options.State)
	return nil
}

func loadRGBAFloatSource(surface *rgbaFloatSurface, src image.RGBA64Image) {
	for y := surface.bounds.Min.Y; y < surface.bounds.Max.Y; y++ {
		for x := surface.bounds.Min.X; x < surface.bounds.Max.X; x++ {
			var value rgbaFloat
			if source, ok := src.(interface {
				NRGBA64At(x, y int) color.NRGBA64
			}); ok {
				pixel := source.NRGBA64At(x, y)
				value = rgbaFloat{
					r: float64(pixel.R) / 0xffff, g: float64(pixel.G) / 0xffff,
					b: float64(pixel.B) / 0xffff, a: float64(pixel.A) / 0xffff,
				}
			} else {
				pixel := src.RGBA64At(x, y)
				alpha := float64(pixel.A) / 0xffff
				value.a = alpha
				if alpha > 0 {
					value.r = clampUnit(float64(pixel.R) / 0xffff / alpha)
					value.g = clampUnit(float64(pixel.G) / 0xffff / alpha)
					value.b = clampUnit(float64(pixel.B) / 0xffff / alpha)
				}
			}
			surface.set(x, y, value)
		}
	}
}

// QuantizeCMYK64 converts a 16-bit CMYK image to image.CMYK. Bayer phase uses
// src.Bounds plus CoordinateOffset. A shared Floyd-Steinberg state permits
// consecutive full-width source strips.
func QuantizeCMYK64(dst *image.CMYK, src *CMYK64, options DitherOptions) error {
	if dst == nil || src == nil {
		return fmt.Errorf("svg: quantization source and destination must be non-nil")
	}
	if !rectanglesHaveSameSize(dst.Bounds(), src.Bounds()) {
		return fmt.Errorf("svg: destination bounds %v and source bounds %v must have the same dimensions", dst.Bounds(), src.Bounds())
	}
	if err := validateDither(options); err != nil {
		return err
	}
	bounds := src.Bounds()
	if bounds.Empty() {
		return nil
	}
	options, externalState := isolateFloydSteinbergState(options)
	for minimumY := bounds.Min.Y; minimumY < bounds.Max.Y; {
		height := precisionBandHeight(bounds.Dx(), 5, bounds.Max.Y-minimumY)
		band := image.Rect(bounds.Min.X, minimumY, bounds.Max.X, minimumY+height)
		surface, err := newCMYKFloatSurface(band)
		if err != nil {
			return err
		}
		for y := band.Min.Y; y < band.Max.Y; y++ {
			for x := band.Min.X; x < band.Max.X; x++ {
				value := cmyk64ColorToFloat(src.CMYK64At(x, y))
				offset := surface.offset(x, y)
				surface.pix[offset], surface.pix[offset+1], surface.pix[offset+2] = value.c, value.m, value.y
				surface.pix[offset+3], surface.pix[offset+4] = value.k, 1
			}
		}
		if err := quantizeCMYKFloatSurface(dst, nil, bounds, surface, 8, options); err != nil {
			return err
		}
		minimumY = band.Max.Y
	}
	commitFloydSteinbergState(externalState, options.State)
	return nil
}
