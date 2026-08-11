package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"
)

func TestGroupOpacityIsCompositedOnce(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 10 10"><g opacity=".5"><rect width="7" height="10" fill="red"/><rect x="3" width="7" height="10" fill="red"/></g></svg>`)
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	for _, x := range []int{1, 5, 8} {
		if alpha := img.NRGBAAt(x, 5).A; alpha < 126 || alpha > 129 {
			t.Fatalf("alpha at x=%d is %d, want 50%%", x, alpha)
		}
	}
}

func TestLinearGradientUsesObjectBounds(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 10 2">
<defs><linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>
<rect x="2" width="6" height="2" fill="url(#g)"/>
</svg>`)
	img := image.NewNRGBA(image.Rect(0, 0, 10, 2))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	left, right := img.NRGBAAt(2, 1), img.NRGBAAt(7, 1)
	if left.R <= left.B || right.B <= right.R {
		t.Fatalf("gradient endpoints = %#v and %#v", left, right)
	}
}

func TestUserSpaceGradientPercentageDefaultsUseViewport(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 10 2">
<defs><linearGradient id="g" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>
<rect width="10" height="2" fill="url(#g)"/>
</svg>`)
	img := image.NewNRGBA(image.Rect(0, 0, 10, 2))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	left, right := img.NRGBAAt(0, 1), img.NRGBAAt(9, 1)
	if left.R <= left.B || right.B <= right.R {
		t.Fatalf("gradient endpoints = %#v and %#v", left, right)
	}
}

func TestDerivedGradientInterpretsInheritedPercentagesInFinalUnits(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 100 1">
<defs>
  <linearGradient id="base" x2="100%"><stop stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>
  <linearGradient id="derived" href="#base" gradientUnits="userSpaceOnUse"/>
</defs>
<rect width="100" height="1" fill="url(#derived)"/>
</svg>`)
	img := image.NewNRGBA(image.Rect(0, 0, 100, 1))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	middle := img.NRGBAAt(49, 0)
	if middle.R < 110 || middle.R > 145 || middle.B < 110 || middle.B > 145 {
		t.Fatalf("derived gradient midpoint = %#v, want red/blue blend", middle)
	}
}

func TestGradientStopsUseStylesheetAndCurrentColor(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 10 2" color="#00aa00">
<style>stop.start { stop-color: currentColor } stop.end { stop-color: blue; stop-opacity: .5 }</style>
<defs><linearGradient id="g"><stop class="start" offset="0"/><stop class="end" offset="1"/></linearGradient></defs>
<rect width="10" height="2" fill="url(#g)"/>
</svg>`)
	img := image.NewNRGBA(image.Rect(0, 0, 10, 2))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	left, right := img.NRGBAAt(0, 1), img.NRGBAAt(9, 1)
	if left.G <= left.R || left.G <= left.B {
		t.Fatalf("currentColor stop = %#v, want green", left)
	}
	if right.B <= right.R || right.A >= 200 {
		t.Fatalf("styled stop = %#v, want translucent blue", right)
	}
}

func TestStrokeWithRoundCaps(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 12 10"><line x1="3" y1="5" x2="9" y2="5" stroke="black" stroke-width="4" stroke-linecap="round"/></svg>`)
	img := image.NewNRGBA(image.Rect(0, 0, 12, 10))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	if img.NRGBAAt(2, 5).A < 240 || img.NRGBAAt(0, 5).A > 15 || img.NRGBAAt(6, 1).A > 15 {
		t.Fatal("round-capped stroke has incorrect coverage")
	}
}

func TestSetTargetMovesAndScalesRendering(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="5 10 10 5"><rect x="5" y="10" width="10" height="5" fill="#123456"/></svg>`)
	icon.SetTarget(10, 4, 20, 10)
	img := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	if got := img.NRGBAAt(20, 8); got != (color.NRGBA{R: 0x12, G: 0x34, B: 0x56, A: 255}) {
		t.Fatalf("target interior = %#v", got)
	}
	if img.NRGBAAt(5, 8).A != 0 || img.NRGBAAt(35, 8).A != 0 {
		t.Fatal("rendering escaped the target rectangle")
	}
}

func TestRegionRenderingMatchesFullRendering(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 48 40">
<defs>
  <linearGradient id="paint" gradientUnits="userSpaceOnUse" x1="3" y1="5" x2="45" y2="35">
    <stop offset="0" stop-color="#ff7a00"/><stop offset=".55" stop-color="#21a179"/><stop offset="1" stop-color="#334dff"/>
  </linearGradient>
  <clipPath id="cut"><circle cx="24" cy="20" r="17"/><rect x="3" y="15" width="42" height="10"/></clipPath>
</defs>
<g transform="rotate(8 24 20)" clip-path="url(#cut)" opacity=".67">
  <rect x="2" y="3" width="44" height="34" rx="5" fill="url(#paint)"/>
  <path d="M5 32C12 4 36 4 43 32L24 11z" fill="#fff" fill-opacity=".42"/>
</g>
</svg>`)
	icon.SetTarget(4, 3, 96, 80)
	region := image.Rect(19, 14, 83, 69)

	tests := []struct {
		name   string
		region func(*SvgIcon, *image.NRGBA, image.Rectangle) error
		full   func(*SvgIcon, *image.NRGBA) error
	}{
		{
			name: "render",
			region: func(icon *SvgIcon, dst *image.NRGBA, region image.Rectangle) error {
				return icon.RenderRegion(dst, region)
			},
			full: func(icon *SvgIcon, dst *image.NRGBA) error { return icon.Render(dst) },
		},
		{
			name: "draw",
			region: func(icon *SvgIcon, dst *image.NRGBA, region image.Rectangle) error {
				return icon.DrawRegion(dst, region, .61)
			},
			full: func(icon *SvgIcon, dst *image.NRGBA) error { return icon.Draw(dst, .61) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			full := image.NewNRGBA(image.Rect(0, 0, 108, 88))
			backgroundAt := func(x, y int) color.NRGBA {
				return color.NRGBA{R: uint8(x*3 + y), G: uint8(x + y*5), B: uint8(x*7 + y*2), A: 255}
			}
			for y := full.Bounds().Min.Y; y < full.Bounds().Max.Y; y++ {
				for x := full.Bounds().Min.X; x < full.Bounds().Max.X; x++ {
					full.SetNRGBA(x, y, backgroundAt(x, y))
				}
			}
			if err := test.full(icon, full); err != nil {
				t.Fatal(err)
			}
			localBounds := image.Rect(7, 11, 7+region.Dx(), 11+region.Dy())
			tile := image.NewNRGBA(localBounds)
			for y := region.Min.Y; y < region.Max.Y; y++ {
				for x := region.Min.X; x < region.Max.X; x++ {
					tile.SetNRGBA(localBounds.Min.X+x-region.Min.X, localBounds.Min.Y+y-region.Min.Y, backgroundAt(x, y))
				}
			}
			if err := test.region(icon, tile, region); err != nil {
				t.Fatal(err)
			}
			for y := region.Min.Y; y < region.Max.Y; y++ {
				for x := region.Min.X; x < region.Max.X; x++ {
					local := image.Pt(localBounds.Min.X+x-region.Min.X, localBounds.Min.Y+y-region.Min.Y)
					if got, want := tile.NRGBAAt(local.X, local.Y), full.NRGBAAt(x, y); got != want {
						t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got, want)
					}
				}
			}
		})
	}
}

func TestRenderRegionUsesBoundedTileForLargeTarget(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 100 100">
<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="100" y2="100">
<stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/>
</linearGradient></defs>
<g opacity=".5"><rect width="100" height="100" fill="url(#g)"/></g>
</svg>`)
	icon.SetTarget(0, 0, 100_000, 100_000)
	region := image.Rect(99_968, 99_968, 100_000, 100_000)
	tile := image.NewNRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	if err := icon.RenderRegion(tile, region); err != nil {
		t.Fatal(err)
	}
	if got := tile.NRGBAAt(31, 31); got.B <= got.R || got.A < 126 || got.A > 129 {
		t.Fatalf("large-target tile pixel = %#v, want translucent blue", got)
	}
}

func TestRenderRegionKeepsObjectBoundingBoxGradientAtLargeCoordinates(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 100 100">
<defs><linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>
<rect x="10" y="20" width="80" height="60" fill="url(#g)"/>
</svg>`)
	icon.SetTarget(0, 0, 100_000, 100_000)
	region := image.Rect(89_968, 49_984, 90_000, 50_016)
	tile := image.NewNRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	if err := icon.RenderRegion(tile, region); err != nil {
		t.Fatal(err)
	}
	if got := tile.NRGBAAt(31, 16); got.B < 250 || got.R > 5 || got.A != 255 {
		t.Fatalf("large object-bounds gradient endpoint = %#v, want blue", got)
	}
}

func TestRenderRegionGradientBeyondOneBillionCoordinates(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1">
<defs><linearGradient id="g"><stop stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>
<rect width="1" height="1" fill="url(#g)"/>
</svg>`)
	const origin = 1 << 30
	icon.SetTarget(origin, origin, 1_000, 1_000)
	region := image.Rect(origin+984, origin+496, origin+1_000, origin+512)
	tile := image.NewNRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	if err := icon.RenderRegion(tile, region); err != nil {
		t.Fatal(err)
	}
	if got := tile.NRGBAAt(15, 8); got.B < 250 || got.A != 255 {
		t.Fatalf("high-coordinate gradient endpoint = %#v, want blue", got)
	}
}

func TestRenderRegionSolidPaintBeyondOneBillionCoordinates(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1" fill="#4a73d1"/></svg>`)
	const origin = 1 << 30
	icon.SetTarget(origin, origin, 32, 32)
	region := image.Rect(origin+16, origin+16, origin+32, origin+32)
	tile := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	if err := icon.RenderRegion(tile, region); err != nil {
		t.Fatal(err)
	}
	if got := tile.NRGBAAt(8, 8); got != (color.NRGBA{R: 0x4a, G: 0x73, B: 0xd1, A: 0xff}) {
		t.Fatalf("high-coordinate solid paint = %#v", got)
	}
}

func TestRenderRegionRejectsMismatchedDestination(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`)
	destination := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	if err := icon.RenderRegion(destination, image.Rect(10, 20, 12, 22)); err == nil {
		t.Fatal("RenderRegion accepted a destination with different dimensions")
	}
}

func TestReferenceDepthIsBounded(t *testing.T) {
	t.Parallel()

	var source strings.Builder
	source.WriteString(`<svg viewBox="0 0 1 1"><defs>`)
	for i := 0; i <= maxReferenceDepth; i++ {
		fmt.Fprintf(&source, `<g id="n%d">`, i)
		if i < maxReferenceDepth {
			fmt.Fprintf(&source, `<use href="#n%d"/>`, i+1)
		} else {
			source.WriteString(`<rect width="1" height="1"/>`)
		}
		source.WriteString(`</g>`)
	}
	source.WriteString(`</defs><use href="#n0"/></svg>`)

	icon := mustReadSVG(t, source.String())
	err := icon.Render(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err == nil || !strings.Contains(err.Error(), "reference depth") {
		t.Fatalf("deep reference error = %v", err)
	}
}

func TestConcurrentDraw(t *testing.T) {
	icon := mustReadSVG(t, `<svg viewBox="0 0 16 16">
<defs><clipPath id="c"><circle cx="8" cy="8" r="7"/></clipPath></defs>
<g clip-path="url(#c)"><path d="M0 0h16v16H0z" fill="orange"/><path d="M2 8Q8 0 14 8T2 8" fill="navy"/></g>
</svg>`)
	icon.SetTarget(0, 0, 64, 64)

	var wait sync.WaitGroup
	errors := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
			errors <- icon.Render(img)
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func mustReadSVG(t *testing.T, source string) *SvgIcon {
	t.Helper()
	icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	return icon
}
