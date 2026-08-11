package coolsvg

import (
	"image"
	"image/color"
	"testing"
)

func TestRenderCMYKUsesDeviceConversionAndWhiteBackground(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 2 1"><rect width="1" height="1" fill="red"/></svg>`)
	destination := image.NewCMYK(image.Rect(0, 0, 2, 1))
	if err := icon.RenderCMYK(destination, CMYKOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := destination.CMYKAt(0, 0); got != (color.CMYK{M: 255, Y: 255}) {
		t.Fatalf("red separation = %#v", got)
	}
	if got := destination.CMYKAt(1, 0); got != (color.CMYK{}) {
		t.Fatalf("transparent background separation = %#v, want paper white", got)
	}
}

func TestRenderCMYKRegionUsesCustomColorSpaceAtLargeCoordinates(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1">
<defs><linearGradient id="g"><stop stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>
<rect width="1" height="1" fill="url(#g)"/>
</svg>`)
	icon.SetTarget(0, 0, 100_000, 100_000)
	region := image.Rect(99_984, 50_000, 100_000, 50_016)
	destination := image.NewCMYK(image.Rect(7, 9, 23, 25))
	space := CMYKColorSpaceFunc(func(r, g, b uint8) color.CMYK {
		return color.CMYK{C: r, M: g, Y: b, K: 7}
	})
	if err := icon.RenderCMYKRegion(destination, region, CMYKOptions{ColorSpace: space}); err != nil {
		t.Fatal(err)
	}
	if got := destination.CMYKAt(22, 17); got.Y < 250 || got.C > 5 || got.K != 7 {
		t.Fatalf("custom large-gradient separation = %#v", got)
	}
}

func TestRenderCMYKNativeGradientInterpolatesSeparatedStops(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1">
<defs><linearGradient id="g"><stop stop-color="red"/><stop offset="1" stop-color="black"/></linearGradient></defs>
<rect width="1" height="1" fill="url(#g)"/>
</svg>`)
	rgbFirst := image.NewCMYK(image.Rect(0, 0, 1, 1))
	if err := icon.RenderCMYK(rgbFirst, CMYKOptions{}); err != nil {
		t.Fatal(err)
	}
	native := image.NewCMYK(image.Rect(0, 0, 1, 1))
	if err := icon.RenderCMYK(native, CMYKOptions{ProcessingMode: CMYKNative}); err != nil {
		t.Fatal(err)
	}

	if got := rgbFirst.CMYKAt(0, 0); got != (color.CMYK{M: 255, Y: 255, K: 127}) {
		t.Fatalf("sRGB midpoint separation = %#v", got)
	}
	if got := native.CMYKAt(0, 0); got != (color.CMYK{M: 128, Y: 128, K: 128}) {
		t.Fatalf("CMYK-native midpoint = %#v", got)
	}
}

func TestRenderCMYKNativeNormalizesInheritedGradientPercentages(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 100 1">
<defs>
  <linearGradient id="base" x2="100%"><stop stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>
  <linearGradient id="derived" href="#base" gradientUnits="userSpaceOnUse"/>
</defs>
<rect width="100" height="1" fill="url(#derived)"/>
</svg>`)
	destination := image.NewCMYK(image.Rect(0, 0, 100, 1))
	if err := icon.RenderCMYK(destination, CMYKOptions{ProcessingMode: CMYKNative}); err != nil {
		t.Fatal(err)
	}
	if got := destination.CMYKAt(49, 0); got.C < 110 || got.C > 145 || got.Y < 110 || got.Y > 145 || got.M != 255 {
		t.Fatalf("native derived gradient midpoint = %#v", got)
	}
}

func TestRenderCMYKNativeRegionUsesGlobalGradientCoordinates(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1">
<defs><linearGradient id="g"><stop stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>
<rect width="1" height="1" fill="url(#g)"/>
</svg>`)
	icon.SetTarget(1_000_000, 2_000_000, 100_000, 100_000)
	region := image.Rect(1_074_999, 2_049_999, 1_075_000, 2_050_000)
	destination := image.NewCMYK(image.Rect(7, 9, 8, 10))
	space := CMYKColorSpaceFunc(func(r, g, b uint8) color.CMYK {
		switch {
		case r == 255 && g == 255 && b == 255:
			return color.CMYK{}
		case r > b:
			return color.CMYK{C: 200, M: 10, Y: 20, K: 30}
		default:
			return color.CMYK{C: 40, M: 50, Y: 220, K: 230}
		}
	})
	options := CMYKOptions{ColorSpace: space, ProcessingMode: CMYKNative}
	if err := icon.RenderCMYKRegion(destination, region, options); err != nil {
		t.Fatal(err)
	}
	if got := destination.CMYKAt(7, 9); got != (color.CMYK{C: 80, M: 40, Y: 170, K: 180}) {
		t.Fatalf("native tile sample = %#v", got)
	}
}

func TestRenderCMYKNativeSupportsClipUseGroupOpacityAndStroke(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 4 4">
<defs>
  <clipPath id="clip"><rect x="1" width="2" height="4"/></clipPath>
  <g id="art">
    <rect width="2" height="2" fill="red"/>
    <rect x="1" width="2" height="2" fill="red"/>
    <path d="M1 3H3" stroke="blue" stroke-width="2"/>
  </g>
</defs>
<g clip-path="url(#clip)" opacity=".5"><use href="#art"/></g>
</svg>`)
	destination := image.NewCMYK(image.Rect(0, 0, 4, 4))
	if err := icon.RenderCMYK(destination, CMYKOptions{ProcessingMode: CMYKNative}); err != nil {
		t.Fatal(err)
	}
	if got := destination.CMYKAt(0, 0); got != (color.CMYK{}) {
		t.Fatalf("outside clip = %#v, want paper white", got)
	}
	overlap := destination.CMYKAt(1, 0)
	edge := destination.CMYKAt(2, 0)
	if overlap != (color.CMYK{M: 128, Y: 128}) || edge != overlap {
		t.Fatalf("group opacity overlap = %#v, edge = %#v", overlap, edge)
	}
	stroke := destination.CMYKAt(1, 2)
	if stroke != (color.CMYK{C: 128, M: 128}) {
		t.Fatalf("native stroke/group composite = %#v", stroke)
	}
}

func TestRenderCMYKNativeRegionMatchesFullRender(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 16 12">
<defs>
  <linearGradient id="object" gradientTransform="rotate(17)">
    <stop stop-color="#f20"/><stop offset=".45" stop-color="#1c8"/><stop offset="1" stop-color="#24e"/>
  </linearGradient>
  <radialGradient id="user" gradientUnits="userSpaceOnUse" cx="8" cy="6" r="8" fx="5" fy="4" spreadMethod="reflect">
    <stop stop-color="#fff" stop-opacity=".8"/><stop offset="1" stop-color="#111"/>
  </radialGradient>
  <clipPath id="clip"><circle cx="8" cy="6" r="5"/></clipPath>
</defs>
<g clip-path="url(#clip)" opacity=".7" transform="rotate(8 8 6)">
  <rect x="1" y="1" width="14" height="10" fill="url(#object)"/>
  <circle cx="8" cy="6" r="4" fill="url(#user)"/>
  <path d="M2 9L8 2L14 9" fill="none" stroke="url(#object)" stroke-width="1.5"/>
</g>
</svg>`)
	icon.SetTarget(100, 200, 16, 12)
	space := CMYKColorSpaceFunc(func(r, g, b uint8) color.CMYK {
		return color.CMYK{C: r ^ b, M: g ^ r, Y: b ^ g, K: uint8((uint16(r) + uint16(g) + uint16(b)) / 3)}
	})
	options := CMYKOptions{ColorSpace: space, ProcessingMode: CMYKNative}
	full := image.NewCMYK(image.Rect(100, 200, 116, 212))
	if err := icon.RenderCMYK(full, options); err != nil {
		t.Fatal(err)
	}
	region := image.Rect(104, 203, 113, 210)
	tile := image.NewCMYK(image.Rect(-7, 9, 2, 16))
	if err := icon.RenderCMYKRegion(tile, region, options); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < region.Dy(); y++ {
		for x := 0; x < region.Dx(); x++ {
			want := full.CMYKAt(region.Min.X+x, region.Min.Y+y)
			got := tile.CMYKAt(tile.Rect.Min.X+x, tile.Rect.Min.Y+y)
			if got != want {
				t.Fatalf("tile pixel (%d,%d) = %#v, full = %#v", x, y, got, want)
			}
		}
	}
}

func TestRenderCMYKValidatesRegionAndColorSpace(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"/>`)
	if err := icon.RenderCMYK(nil, CMYKOptions{}); err == nil {
		t.Fatal("nil CMYK destination was accepted")
	}
	destination := image.NewCMYK(image.Rect(0, 0, 2, 2))
	if err := icon.RenderCMYKRegion(destination, image.Rect(0, 0, 1, 1), CMYKOptions{}); err == nil {
		t.Fatal("mismatched CMYK region was accepted")
	}
	if err := icon.RenderCMYK(destination, CMYKOptions{ColorSpace: CMYKColorSpaceFunc(nil)}); err == nil {
		t.Fatal("nil CMYK conversion function was accepted")
	}
	if err := icon.RenderCMYK(destination, CMYKOptions{ProcessingMode: CMYKProcessingMode(255)}); err == nil {
		t.Fatal("invalid CMYK processing mode was accepted")
	}
}
