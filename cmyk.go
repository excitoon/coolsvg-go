package coolsvg

import (
	"fmt"
	"image"
	"image/color"
)

// CMYKColorSpace converts opaque sRGB pixels into CMYK separations. It can be
// implemented by a device profile, lookup table, or color-management system.
// This interface supplies separation values only; embedding an ICC profile in
// an output file remains the encoder's responsibility.
type CMYKColorSpace interface {
	ConvertRGB(r, g, b uint8) color.CMYK
}

// CMYKColorSpaceFunc adapts a function to CMYKColorSpace.
type CMYKColorSpaceFunc func(r, g, b uint8) color.CMYK

// ConvertRGB implements CMYKColorSpace.
func (f CMYKColorSpaceFunc) ConvertRGB(r, g, b uint8) color.CMYK {
	return f(r, g, b)
}

// CMYKColorSpace64 converts 16-bit sRGB samples into 16-bit CMYK separations.
// It is used by RenderCMYK64 when supplied; otherwise ColorSpace is adapted or
// the generic unprofiled conversion is used.
type CMYKColorSpace64 interface {
	ConvertRGB64(r, g, b uint16) CMYK64Color
}

// CMYKColorSpace64Func adapts a function to CMYKColorSpace64.
type CMYKColorSpace64Func func(r, g, b uint16) CMYK64Color

// ConvertRGB64 implements CMYKColorSpace64.
func (f CMYKColorSpace64Func) ConvertRGB64(r, g, b uint16) CMYK64Color {
	return f(r, g, b)
}

// CMYKColorSpaceFloat converts normalized sRGB values to normalized CMYK
// separations without forcing an intermediate integer quantization. Values are
// clamped by the renderer before compositing.
type CMYKColorSpaceFloat interface {
	ConvertRGBFloat(r, g, b float64) (c, m, y, k float64)
}

// CMYKColorSpaceFloatFunc adapts a function to CMYKColorSpaceFloat.
type CMYKColorSpaceFloatFunc func(r, g, b float64) (c, m, y, k float64)

// ConvertRGBFloat implements CMYKColorSpaceFloat.
func (f CMYKColorSpaceFloatFunc) ConvertRGBFloat(r, g, b float64) (c, m, y, k float64) {
	return f(r, g, b)
}

// DeviceCMYKColorSpace uses Go's generic, unprofiled RGB-to-CMYK formula. It
// does not model a particular press, ink set, paper stock, or ICC profile. Use
// a custom CMYKColorSpace when a workflow requires a specific separation.
type DeviceCMYKColorSpace struct{}

// ConvertRGB implements CMYKColorSpace.
func (DeviceCMYKColorSpace) ConvertRGB(r, g, b uint8) color.CMYK {
	c, m, y, k := color.RGBToCMYK(r, g, b)
	return color.CMYK{C: c, M: m, Y: y, K: k}
}

// ConvertRGB64 implements CMYKColorSpace64 using the same generic, unprofiled
// formula at 16-bit precision.
func (DeviceCMYKColorSpace) ConvertRGB64(r, g, b uint16) CMYK64Color {
	return deviceRGBToCMYK64(r, g, b)
}

// ConvertRGBFloat implements CMYKColorSpaceFloat using normalized working
// precision.
func (DeviceCMYKColorSpace) ConvertRGBFloat(r, g, b float64) (c, m, y, k float64) {
	value := deviceRGBToCMYKFloat(r, g, b)
	return value.c, value.m, value.y, value.k
}

// CMYKProcessingMode controls where paint interpolation and compositing happen
// during CMYK export.
type CMYKProcessingMode uint8

const (
	// CMYKPostConvert renders and composites the SVG in sRGB, then separates the
	// completed raster. This is the default and preserves Render's appearance.
	CMYKPostConvert CMYKProcessingMode = iota
	// CMYKNative separates paint colors and gradient stops before interpolation
	// and channel-wise source-over compositing. It is a knockout raster model;
	// it does not implement overprint, spot colors, trapping, rendering intent,
	// or ICC profile embedding.
	CMYKNative
)

// CMYKOptions controls raster color separation.
type CMYKOptions struct {
	// ColorSpace selects the RGB-to-CMYK conversion. Nil uses
	// DeviceCMYKColorSpace.
	ColorSpace CMYKColorSpace
	// ColorSpace64 optionally supplies a 16-bit separation. RenderCMYK64 uses it
	// in preference to ColorSpace. Nil adapts ColorSpace or uses
	// DeviceCMYKColorSpace.
	ColorSpace64 CMYKColorSpace64
	// ColorSpaceFloat optionally supplies a normalized floating-point
	// separation and is preferred by high-precision CMYK rendering. Integer
	// color spaces remain supported but necessarily quantize custom separation
	// results before final compositing.
	ColorSpaceFloat CMYKColorSpaceFloat
	// Background is interpreted through Go's color model as sRGB and composited
	// behind the SVG because CMYK images have no alpha channel. Nil uses opaque
	// white. CMYKPostConvert flattens a translucent background onto white in
	// sRGB; CMYKNative separates it and white before channel-wise compositing.
	Background color.Color
	// ProcessingMode selects post-conversion or native CMYK processing. The zero
	// value is CMYKPostConvert.
	ProcessingMode CMYKProcessingMode
	// Dither controls quantization from floating-point working precision. The
	// post-convert 8-bit ColorSpace path retains its compatibility fast path
	// when Mode is DitherNone.
	Dither DitherOptions
}

// RenderCMYK renders the icon into a CMYK raster. The destination bounds use
// the same global coordinates as Render.
func (s *SvgIcon) RenderCMYK(dst *image.CMYK, options CMYKOptions) error {
	if dst == nil {
		return fmt.Errorf("svg: CMYK destination is nil")
	}
	return s.RenderCMYKRegion(dst, dst.Bounds(), options)
}

// RenderCMYKRegion renders a global region of the virtual destination into a
// CMYK raster. Like RenderRegion, it allocates work buffers only for the
// supplied region, making it suitable for tiled large-canvas export.
func (s *SvgIcon) RenderCMYKRegion(dst *image.CMYK, region image.Rectangle, options CMYKOptions) error {
	if dst == nil {
		return fmt.Errorf("svg: CMYK destination is nil")
	}
	if !rectanglesHaveSameSize(dst.Bounds(), region) {
		return fmt.Errorf("svg: destination bounds %v and region %v must have the same dimensions", dst.Bounds(), region)
	}
	if s == nil || s.root == nil {
		return fmt.Errorf("svg: cannot draw a nil icon")
	}
	if options.ProcessingMode == CMYKNative || options.Dither.Mode != DitherNone || options.ColorSpace64 != nil || options.ColorSpaceFloat != nil {
		return s.renderCMYKPrecision8(dst, region, options)
	}
	space := options.ColorSpace
	if space == nil {
		space = DeviceCMYKColorSpace{}
	}
	if nilInterface(space) {
		return fmt.Errorf("svg: CMYK color space is nil")
	}
	if function, ok := space.(CMYKColorSpaceFunc); ok && function == nil {
		return fmt.Errorf("svg: CMYK color-space function is nil")
	}
	if options.ProcessingMode > CMYKNative {
		return fmt.Errorf("svg: unsupported CMYK processing mode %d", options.ProcessingMode)
	}
	background := opaqueBackground(options.Background)
	destinationBounds := dst.Bounds()
	for minimumY := region.Min.Y; minimumY < region.Max.Y; {
		height := precisionBandHeight(region.Dx(), 4, region.Max.Y-minimumY)
		band := image.Rect(region.Min.X, minimumY, region.Max.X, minimumY+height)
		localMinimumY := destinationBounds.Min.Y + (minimumY - region.Min.Y)
		localBand := image.Rect(destinationBounds.Min.X, localMinimumY, destinationBounds.Max.X, localMinimumY+height)
		length, _, ok := checkedImageSize(localBand, 4)
		if !ok || length > precisionMaxSurfaceBytes/8 {
			return fmt.Errorf("svg: floating-point RGBA surface bounds %v are too large", band)
		}
		surface := image.NewNRGBA(localBand)
		for y := surface.Rect.Min.Y; y < surface.Rect.Max.Y; y++ {
			offset := surface.PixOffset(surface.Rect.Min.X, y)
			for x := surface.Rect.Min.X; x < surface.Rect.Max.X; x++ {
				surface.Pix[offset+0] = background.R
				surface.Pix[offset+1] = background.G
				surface.Pix[offset+2] = background.B
				surface.Pix[offset+3] = 0xff
				offset += 4
			}
		}
		if err := s.RenderRegion(surface, band); err != nil {
			return err
		}

		for y := localBand.Min.Y; y < localBand.Max.Y; y++ {
			sourceOffset := surface.PixOffset(localBand.Min.X, y)
			destinationOffset := dst.PixOffset(destinationBounds.Min.X, y)
			for x := destinationBounds.Min.X; x < destinationBounds.Max.X; x++ {
				value := space.ConvertRGB(surface.Pix[sourceOffset], surface.Pix[sourceOffset+1], surface.Pix[sourceOffset+2])
				dst.Pix[destinationOffset+0] = value.C
				dst.Pix[destinationOffset+1] = value.M
				dst.Pix[destinationOffset+2] = value.Y
				dst.Pix[destinationOffset+3] = value.K
				sourceOffset += 4
				destinationOffset += 4
			}
		}
		minimumY = band.Max.Y
	}
	return nil
}

func opaqueBackground(value color.Color) color.NRGBA {
	if nilColor(value) {
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	}
	background := color.NRGBAModel.Convert(value).(color.NRGBA)
	if background.A == 255 {
		return background
	}
	alpha := uint32(background.A)
	flatten := func(channel uint8) uint8 {
		return uint8((uint32(channel)*alpha + 255*(255-alpha) + 127) / 255)
	}
	return color.NRGBA{R: flatten(background.R), G: flatten(background.G), B: flatten(background.B), A: 255}
}

func nilColor(value color.Color) bool {
	return value == nil || nilInterface(value)
}
