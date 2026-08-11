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

// DeviceCMYKColorSpace uses Go's generic, unprofiled RGB-to-CMYK formula. It
// does not model a particular press, ink set, paper stock, or ICC profile. Use
// a custom CMYKColorSpace when a workflow requires a specific separation.
type DeviceCMYKColorSpace struct{}

// ConvertRGB implements CMYKColorSpace.
func (DeviceCMYKColorSpace) ConvertRGB(r, g, b uint8) color.CMYK {
	c, m, y, k := color.RGBToCMYK(r, g, b)
	return color.CMYK{C: c, M: m, Y: y, K: k}
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
	// Background is interpreted through Go's color model as sRGB and composited
	// behind the SVG because CMYK images have no alpha channel. Nil uses opaque
	// white. CMYKPostConvert flattens a translucent background onto white in
	// sRGB; CMYKNative separates it and white before channel-wise compositing.
	Background color.Color
	// ProcessingMode selects post-conversion or native CMYK processing. The zero
	// value is CMYKPostConvert.
	ProcessingMode CMYKProcessingMode
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
	space := options.ColorSpace
	if space == nil {
		space = DeviceCMYKColorSpace{}
	}
	if function, ok := space.(CMYKColorSpaceFunc); ok && function == nil {
		return fmt.Errorf("svg: CMYK color-space function is nil")
	}
	if options.ProcessingMode > CMYKNative {
		return fmt.Errorf("svg: unsupported CMYK processing mode %d", options.ProcessingMode)
	}
	if options.ProcessingMode == CMYKNative {
		return s.renderCMYKNativeRegion(dst, region, space, options.Background)
	}

	background := opaqueBackground(options.Background)
	surface := image.NewNRGBA(dst.Bounds())
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
	if err := s.RenderRegion(surface, region); err != nil {
		return err
	}

	for y := dst.Rect.Min.Y; y < dst.Rect.Max.Y; y++ {
		sourceOffset := surface.PixOffset(surface.Rect.Min.X, y)
		destinationOffset := dst.PixOffset(dst.Rect.Min.X, y)
		for x := dst.Rect.Min.X; x < dst.Rect.Max.X; x++ {
			value := space.ConvertRGB(surface.Pix[sourceOffset], surface.Pix[sourceOffset+1], surface.Pix[sourceOffset+2])
			dst.Pix[destinationOffset+0] = value.C
			dst.Pix[destinationOffset+1] = value.M
			dst.Pix[destinationOffset+2] = value.Y
			dst.Pix[destinationOffset+3] = value.K
			sourceOffset += 4
			destinationOffset += 4
		}
	}
	return nil
}

func opaqueBackground(value color.Color) color.NRGBA {
	if value == nil {
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
