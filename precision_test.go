package coolsvg

import (
	"image"
	"image/color"
	"testing"
)

func TestRender64PreservesSub8BitGradientAndGroupPrecision(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 8 2">
<defs>
  <linearGradient id="g"><stop stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>
  <clipPath id="c"><rect width="8" height="2"/></clipPath>
</defs>
<g opacity=".731" clip-path="url(#c)"><rect width="8" height="2" fill="url(#g)"/></g>
</svg>`)
	destination := image.NewNRGBA64(image.Rect(0, 0, 8, 2))
	if err := icon.Render(destination); err != nil {
		t.Fatal(err)
	}
	value := destination.NRGBA64At(3, 0)
	if value.R%0x101 == 0 || value.B%0x101 == 0 {
		t.Fatalf("gradient was reduced to 8-bit expansion: %#v", value)
	}
	if value.A%0x101 == 0 {
		t.Fatalf("group opacity was reduced to 8-bit expansion: %#v", value)
	}
}

func TestRenderDestinationDepthUsesImageCapabilities(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 8 1"><defs><linearGradient id="g"><stop stop-color="black"/><stop offset="1" stop-color="white"/></linearGradient></defs><rect width="8" height="1" fill="url(#g)"/></svg>`)
	modelOnly := &rgba64ModelOnlyImage{pixels: image.NewNRGBA(image.Rect(0, 0, 8, 1))}
	if err := icon.RenderWithOptions(modelOnly, RenderOptions{}); err != nil {
		t.Fatal(err)
	}

	capability := &customModelRGBA64Image{pixels: image.NewNRGBA64(image.Rect(0, 0, 8, 1))}
	if err := icon.Render64(capability, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	value := capability.pixels.NRGBA64At(3, 0)
	if value.R%0x101 == 0 {
		t.Fatalf("custom RGBA64 destination was reduced to 8-bit expansion: %#v", value)
	}
}

func TestRender64BayerMatchesIndependentTilesAtLargeCoordinates(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#123456"/><stop offset="1" stop-color="#edcba9"/></linearGradient></defs>
<g opacity=".83"><rect width="1" height="1" fill="url(#g)"/></g>
</svg>`)
	const origin = 1 << 30
	icon.SetTarget(origin, origin, 1_000, 1_000)
	region := image.Rect(origin+960, origin+480, origin+1_000, origin+504)
	options := RenderOptions{Dither: DitherOptions{Mode: DitherBayer8x8}}
	full := image.NewNRGBA64(image.Rect(7, 9, 7+region.Dx(), 9+region.Dy()))
	if err := icon.Render64Region(full, region, options); err != nil {
		t.Fatal(err)
	}

	for top := region.Min.Y; top < region.Max.Y; top += 7 {
		bottom := min(top+7, region.Max.Y)
		for left := region.Min.X; left < region.Max.X; left += 11 {
			right := min(left+11, region.Max.X)
			tileRegion := image.Rect(left, top, right, bottom)
			tile := image.NewNRGBA64(image.Rect(-3, 5, -3+tileRegion.Dx(), 5+tileRegion.Dy()))
			if err := icon.Render64Region(tile, tileRegion, options); err != nil {
				t.Fatal(err)
			}
			for y := tileRegion.Min.Y; y < tileRegion.Max.Y; y++ {
				for x := tileRegion.Min.X; x < tileRegion.Max.X; x++ {
					got := tile.NRGBA64At(tile.Rect.Min.X+x-tileRegion.Min.X, tile.Rect.Min.Y+y-tileRegion.Min.Y)
					want := full.NRGBA64At(full.Rect.Min.X+x-region.Min.X, full.Rect.Min.Y+y-region.Min.Y)
					if got != want {
						t.Fatalf("tile pixel (%d,%d) = %#v, full = %#v", x, y, got, want)
					}
				}
			}
		}
	}
}

func TestRender64FloydSteinbergCarriesAcrossStrips(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 19 9">
<defs><linearGradient id="g" x1="0" y1="0" x2="19" y2="9" gradientUnits="userSpaceOnUse"><stop stop-color="#182f51"/><stop offset="1" stop-color="#d8b779"/></linearGradient></defs>
<rect width="19" height="9" fill="url(#g)"/>
</svg>`)
	options := RenderOptions{Dither: DitherOptions{Mode: DitherFloydSteinberg}}
	full := image.NewNRGBA64(image.Rect(0, 0, 19, 9))
	if err := icon.Render64(full, options); err != nil {
		t.Fatal(err)
	}

	state := &FloydSteinbergState{}
	stripOptions := RenderOptions{Dither: DitherOptions{Mode: DitherFloydSteinberg, State: state}}
	top := image.NewNRGBA64(image.Rect(4, 6, 23, 10))
	if err := icon.Render64Region(top, image.Rect(0, 0, 19, 4), stripOptions); err != nil {
		t.Fatal(err)
	}
	bottom := image.NewNRGBA64(image.Rect(-2, 3, 17, 8))
	if err := icon.Render64Region(bottom, image.Rect(0, 4, 19, 9), stripOptions); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 9; y++ {
		for x := 0; x < 19; x++ {
			var got color.NRGBA64
			if y < 4 {
				got = top.NRGBA64At(top.Rect.Min.X+x, top.Rect.Min.Y+y)
			} else {
				got = bottom.NRGBA64At(bottom.Rect.Min.X+x, bottom.Rect.Min.Y+y-4)
			}
			if want := full.NRGBA64At(x, y); got != want {
				t.Fatalf("strip pixel (%d,%d) = %#v, full = %#v", x, y, got, want)
			}
		}
	}

	invalid := image.NewNRGBA64(image.Rect(0, 0, 18, 1))
	if err := icon.Render64Region(invalid, image.Rect(0, 9, 18, 10), stripOptions); err == nil {
		t.Fatal("Floyd-Steinberg accepted a mismatched strip width")
	}
}

func TestBayerChanges16BitQuantizationFromFloatResidual(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 64 1"><defs><linearGradient id="g"><stop stop-color="black"/><stop offset="1" stop-color="white"/></linearGradient></defs><rect width="64" height="1" fill="url(#g)"/></svg>`)
	plain := image.NewNRGBA64(image.Rect(0, 0, 64, 1))
	if err := icon.Render64(plain, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	dithered := image.NewNRGBA64(image.Rect(0, 0, 64, 1))
	if err := icon.Render64(dithered, RenderOptions{Dither: DitherOptions{Mode: DitherBayer8x8}}); err != nil {
		t.Fatal(err)
	}
	different := false
	for x := 0; x < 64; x++ {
		if plain.NRGBA64At(x, 0) != dithered.NRGBA64At(x, 0) {
			different = true
			break
		}
	}
	if !different {
		t.Fatal("16-bit Bayer dither did not consume floating-point residuals")
	}
}

func TestFloydSteinbergChanges16BitQuantizationFromFloatResidual(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 64 4"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#17395b"/><stop offset="1" stop-color="#d7b97d"/></linearGradient></defs><rect width="64" height="4" fill="url(#g)"/></svg>`)
	plain := image.NewNRGBA64(image.Rect(0, 0, 64, 4))
	if err := icon.Render64(plain, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	dithered := image.NewNRGBA64(image.Rect(0, 0, 64, 4))
	if err := icon.Render64(dithered, RenderOptions{Dither: DitherOptions{Mode: DitherFloydSteinberg}}); err != nil {
		t.Fatal(err)
	}
	if equalNRGBA64(plain, dithered) {
		t.Fatal("16-bit Floyd-Steinberg dither did not consume floating-point residuals")
	}
}

func TestRenderWithOptionsSupportsBoth8BitDitherModes(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 64 4"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#17395b"/><stop offset="1" stop-color="#d7b97d"/></linearGradient></defs><rect width="64" height="4" fill="url(#g)"/></svg>`)
	render := func(mode DitherMode) *image.NRGBA {
		destination := image.NewNRGBA(image.Rect(0, 0, 64, 4))
		if err := icon.RenderWithOptions(destination, RenderOptions{Dither: DitherOptions{Mode: mode}}); err != nil {
			t.Fatal(err)
		}
		return destination
	}
	plain := render(DitherNone)
	for _, mode := range []DitherMode{DitherBayer8x8, DitherFloydSteinberg} {
		if got := render(mode); string(got.Pix) == string(plain.Pix) {
			t.Fatalf("8-bit dither mode %d did not affect quantization", mode)
		}
	}
}

func TestRenderCMYKSupportsBoth8BitDitherModes(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 64 4"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#f30"/><stop offset=".4" stop-color="#2a7"/><stop offset="1" stop-color="#15e"/></linearGradient></defs><rect width="64" height="4" fill="url(#g)"/></svg>`)
	render := func(mode DitherMode) *image.CMYK {
		destination := image.NewCMYK(image.Rect(0, 0, 64, 4))
		options := CMYKOptions{
			ColorSpaceFloat: DeviceCMYKColorSpace{},
			Dither:          DitherOptions{Mode: mode},
		}
		if err := icon.RenderCMYK(destination, options); err != nil {
			t.Fatal(err)
		}
		return destination
	}
	plain := render(DitherNone)
	for _, mode := range []DitherMode{DitherBayer8x8, DitherFloydSteinberg} {
		if got := render(mode); string(got.Pix) == string(plain.Pix) {
			t.Fatalf("8-bit CMYK dither mode %d did not affect quantization", mode)
		}
	}
}

func equalNRGBA64(a, b *image.NRGBA64) bool {
	return a.Rect == b.Rect && string(a.Pix) == string(b.Pix)
}

func TestCMYK64ImageLayoutAndSubImage(t *testing.T) {
	t.Parallel()

	image64 := NewCMYK64(image.Rect(3, 5, 5, 7))
	want := CMYK64Color{C: 0x1234, M: 0x5678, Y: 0x9abc, K: 0xdef0}
	image64.SetCMYK64(4, 6, want)
	if got := image64.CMYK64At(4, 6); got != want {
		t.Fatalf("CMYK64At = %#v", got)
	}
	offset := image64.PixOffset(4, 6)
	gotBytes := image64.Pix[offset : offset+8]
	wantBytes := []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}
	if string(gotBytes) != string(wantBytes) {
		t.Fatalf("big-endian bytes = % x", gotBytes)
	}
	sub := image64.SubImage(image.Rect(4, 6, 5, 7)).(*CMYK64)
	sub.SetCMYK64(4, 6, CMYK64Color{K: 0xffff})
	if got := image64.CMYK64At(4, 6); got.K != 0xffff {
		t.Fatalf("subimage did not share storage: %#v", got)
	}
}

func TestRenderCMYK64NativeAndPostConvertDifferAtMidpoint(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><defs><linearGradient id="g"><stop stop-color="red"/><stop offset="1" stop-color="black"/></linearGradient></defs><rect width="1" height="1" fill="url(#g)"/></svg>`)
	post := NewCMYK64(image.Rect(0, 0, 1, 1))
	if err := icon.RenderCMYK64(post, CMYKOptions{}); err != nil {
		t.Fatal(err)
	}
	native := NewCMYK64(image.Rect(0, 0, 1, 1))
	if err := icon.RenderCMYK64(native, CMYKOptions{ProcessingMode: CMYKNative}); err != nil {
		t.Fatal(err)
	}
	postValue, nativeValue := post.CMYK64At(0, 0), native.CMYK64At(0, 0)
	if postValue == nativeValue {
		t.Fatalf("native and post-convert midpoints unexpectedly match: %#v", postValue)
	}
	if nativeValue.M != 0x8000 || nativeValue.Y != 0x8000 || nativeValue.K != 0x8000 {
		t.Fatalf("native CMYK midpoint = %#v", nativeValue)
	}
}

func TestCMYK64ColorSpaceTakesPrecedenceOverColorSpace(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1" fill="red"/></svg>`)
	destination := NewCMYK64(image.Rect(0, 0, 1, 1))
	want := CMYK64Color{C: 0x1234, M: 0x5678, Y: 0x9abc, K: 0xdef0}
	err := icon.RenderCMYK64(destination, CMYKOptions{
		ColorSpace: DeviceCMYKColorSpace{},
		ColorSpace64: CMYKColorSpace64Func(func(_, _, _ uint16) CMYK64Color {
			return want
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := destination.CMYK64At(0, 0); got != want {
		t.Fatalf("CMYK64 color-space precedence = %#v, want %#v", got, want)
	}
}

func TestRenderCMYK64BayerRegionMatchesTiles(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><defs><linearGradient id="g"><stop stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient><clipPath id="c"><circle cx=".5" cy=".5" r=".6"/></clipPath></defs><g opacity=".77" clip-path="url(#c)"><rect width="1" height="1" fill="url(#g)"/></g></svg>`)
	const origin = 1 << 30
	icon.SetTarget(origin, origin, 512, 512)
	region := image.Rect(origin+472, origin+240, origin+512, origin+264)
	options := CMYKOptions{ProcessingMode: CMYKNative, Dither: DitherOptions{Mode: DitherBayer8x8}}
	full := NewCMYK64(image.Rect(2, 3, 42, 27))
	if err := icon.RenderCMYK64Region(full, region, options); err != nil {
		t.Fatal(err)
	}
	leftRegion := image.Rect(region.Min.X, region.Min.Y, region.Min.X+17, region.Max.Y)
	rightRegion := image.Rect(region.Min.X+17, region.Min.Y, region.Max.X, region.Max.Y)
	for _, tileRegion := range []image.Rectangle{leftRegion, rightRegion} {
		tile := NewCMYK64(image.Rect(-5, 8, -5+tileRegion.Dx(), 8+tileRegion.Dy()))
		if err := icon.RenderCMYK64Region(tile, tileRegion, options); err != nil {
			t.Fatal(err)
		}
		for y := tileRegion.Min.Y; y < tileRegion.Max.Y; y++ {
			for x := tileRegion.Min.X; x < tileRegion.Max.X; x++ {
				got := tile.CMYK64At(tile.Rect.Min.X+x-tileRegion.Min.X, tile.Rect.Min.Y+y-tileRegion.Min.Y)
				want := full.CMYK64At(full.Rect.Min.X+x-region.Min.X, full.Rect.Min.Y+y-region.Min.Y)
				if got != want {
					t.Fatalf("CMYK64 tile pixel (%d,%d) = %#v, full = %#v", x, y, got, want)
				}
			}
		}
	}
}

func TestRenderCMYK64FloydSteinbergCarriesAcrossStrips(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 17 7"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#f40"/><stop offset=".4" stop-color="#2a8"/><stop offset="1" stop-color="#15e"/></linearGradient></defs><rect width="17" height="7" fill="url(#g)"/></svg>`)
	fullOptions := CMYKOptions{ProcessingMode: CMYKNative, Dither: DitherOptions{Mode: DitherFloydSteinberg}}
	full := NewCMYK64(image.Rect(0, 0, 17, 7))
	if err := icon.RenderCMYK64(full, fullOptions); err != nil {
		t.Fatal(err)
	}
	state := &FloydSteinbergState{}
	stripOptions := CMYKOptions{ProcessingMode: CMYKNative, Dither: DitherOptions{Mode: DitherFloydSteinberg, State: state}}
	top := NewCMYK64(image.Rect(3, 4, 20, 7))
	if err := icon.RenderCMYK64Region(top, image.Rect(0, 0, 17, 3), stripOptions); err != nil {
		t.Fatal(err)
	}
	bottom := NewCMYK64(image.Rect(-4, 2, 13, 6))
	if err := icon.RenderCMYK64Region(bottom, image.Rect(0, 3, 17, 7), stripOptions); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 7; y++ {
		for x := 0; x < 17; x++ {
			var got CMYK64Color
			if y < 3 {
				got = top.CMYK64At(top.Rect.Min.X+x, top.Rect.Min.Y+y)
			} else {
				got = bottom.CMYK64At(bottom.Rect.Min.X+x, bottom.Rect.Min.Y+y-3)
			}
			if want := full.CMYK64At(x, y); got != want {
				t.Fatalf("CMYK strip pixel (%d,%d) = %#v, full = %#v", x, y, got, want)
			}
		}
	}
}

type rgba64ModelOnlyImage struct {
	pixels *image.NRGBA
}

func (*rgba64ModelOnlyImage) ColorModel() color.Model   { return color.RGBA64Model }
func (p *rgba64ModelOnlyImage) Bounds() image.Rectangle { return p.pixels.Bounds() }
func (p *rgba64ModelOnlyImage) At(x, y int) color.Color { return p.pixels.At(x, y) }
func (p *rgba64ModelOnlyImage) Set(x, y int, value color.Color) {
	p.pixels.Set(x, y, value)
}

type customModelRGBA64Image struct {
	pixels *image.NRGBA64
}

var customRGBA64Model = color.ModelFunc(func(value color.Color) color.Color { return value })

func (*customModelRGBA64Image) ColorModel() color.Model   { return customRGBA64Model }
func (p *customModelRGBA64Image) Bounds() image.Rectangle { return p.pixels.Bounds() }
func (p *customModelRGBA64Image) At(x, y int) color.Color { return p.pixels.At(x, y) }
func (p *customModelRGBA64Image) Set(x, y int, value color.Color) {
	p.pixels.Set(x, y, value)
}
func (p *customModelRGBA64Image) RGBA64At(x, y int) color.RGBA64 {
	return p.pixels.RGBA64At(x, y)
}
func (p *customModelRGBA64Image) SetRGBA64(x, y int, value color.RGBA64) {
	p.pixels.SetRGBA64(x, y, value)
}
