package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	"testing"
)

func TestPrecisionIdenticalClipPreservesFractionalCoverageRGBA(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		x     string
		width string
	}{
		{name: "quarter-grid edge", x: ".25", width: ".5"},
		{name: "non-grid edge", x: ".1", width: ".6"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			geometry := fmt.Sprintf(`<rect x="%s" width="%s" height="1" fill="#d42a71"/>`, test.x, test.width)
			unclipped := fmt.Sprintf(`<svg viewBox="0 0 1 1"><g opacity=".63">%s</g></svg>`, geometry)
			clipped := fmt.Sprintf(`<svg viewBox="0 0 1 1">
<defs><clipPath id="same"><rect x="%s" width="%s" height="1"/></clipPath></defs>
<g opacity=".63" clip-path="url(#same)">%s</g></svg>`, test.x, test.width, geometry)

			want := renderPrecisionRGBA64Pixel(t, unclipped)
			got := renderPrecisionRGBA64Pixel(t, clipped)
			if got != want {
				t.Fatalf("identically clipped pixel = %#v, unclipped = %#v", got, want)
			}
			if got.A == 0 || got.A == 0xffff {
				t.Fatalf("fractional edge alpha = %#x, want strictly fractional coverage", got.A)
			}
		})
	}
}

func TestPrecisionNestedClipsIntersectAtSampleLevel(t *testing.T) {
	t.Parallel()

	const fill = `#2675d8`
	actual := `<svg viewBox="0 0 1 1">
<defs>
  <clipPath id="outer"><rect x=".1" width=".6" height="1"/></clipPath>
  <clipPath id="inner"><rect x=".4" width=".5" height="1"/></clipPath>
</defs>
<g opacity=".63" clip-path="url(#outer)">
  <g clip-path="url(#inner)"><rect width="1" height="1" fill="` + fill + `"/></g>
</g></svg>`
	// The exact horizontal clip intervals intersect over [.4,.7].
	reference := `<svg viewBox="0 0 1 1"><g opacity=".63"><rect x=".4" width=".3" height="1" fill="` + fill + `"/></g></svg>`

	want := renderPrecisionRGBA64Pixel(t, reference)
	got := renderPrecisionRGBA64Pixel(t, actual)
	if got != want {
		t.Fatalf("nested clipped pixel = %#v, exact-intersection reference = %#v", got, want)
	}
}

func TestPrecisionClippedGroupCompositesOverlappingChildrenOnce(t *testing.T) {
	t.Parallel()

	actual := `<svg viewBox="0 0 1 1">
<defs><clipPath id="half"><rect width=".5" height="1"/></clipPath></defs>
<g clip-path="url(#half)">
  <rect width=".5" height="1" fill="red"/>
  <rect x=".25" width=".5" height="1" fill="blue"/>
</g></svg>`

	got := renderPrecisionRGBA64Pixel(t, actual)
	if got.R != 0x8000 || got.G != 0 || got.B != 0x8000 || got.A != 0x8000 {
		t.Fatalf("overlapping clipped group color/alpha = %#v, want half-covered straight purple", got)
	}
	legacy := renderRGBA8Pixel(t, actual)
	if legacy != (color.NRGBA{R: 0x80, B: 0x80, A: 0x80}) {
		t.Fatalf("legacy Render overlapping clipped group = %#v, want half-covered straight purple", legacy)
	}
}

func TestPrecisionClippedGroupCompositesOverlappingChildrenOnceCMYKNative(t *testing.T) {
	t.Parallel()

	actual := `<svg viewBox="0 0 1 1">
<defs><clipPath id="half"><rect width=".5" height="1"/></clipPath></defs>
<g clip-path="url(#half)">
  <rect width=".5" height="1" fill="red"/>
  <rect x=".25" width=".5" height="1" fill="blue"/>
</g></svg>`

	got := renderPrecisionCMYK64Pixel(t, actual)
	want := CMYK64Color{C: 0x4000, M: 0x8000, Y: 0x4000}
	if got != want {
		t.Fatalf("overlapping clipped native-CMYK color = %#v, want %#v", got, want)
	}
}

func TestPrecisionNoOpClipPreservesAdjacentSiblings(t *testing.T) {
	t.Parallel()

	const content = `<g><rect width=".25" height="1" fill="red"/><rect x=".25" width=".25" height="1" fill="blue"/></g>`
	unclipped := `<svg viewBox="0 0 1 1">` + content + `</svg>`
	clipped := `<svg viewBox="0 0 1 1">
<defs><clipPath id="half"><rect width=".5" height="1"/></clipPath></defs>
<g clip-path="url(#half)">` + content + `</g></svg>`

	wantRGBA64 := color.NRGBA64{R: 0x8000, B: 0x8000, A: 0x8000}
	if got := renderPrecisionRGBA64Pixel(t, unclipped); got != wantRGBA64 {
		t.Fatalf("unclipped adjacent siblings = %#v, want spatially integrated %#v", got, wantRGBA64)
	}
	if got := renderPrecisionRGBA64Pixel(t, clipped); got != wantRGBA64 {
		t.Fatalf("clipped adjacent siblings = %#v, want no-op clip result %#v", got, wantRGBA64)
	}
	wantRGBA8 := color.NRGBA{R: 0x80, B: 0x80, A: 0x80}
	if got := renderRGBA8Pixel(t, unclipped); got != wantRGBA8 {
		t.Fatalf("legacy unclipped adjacent siblings = %#v, want %#v", got, wantRGBA8)
	}
	if got := renderRGBA8Pixel(t, clipped); got != wantRGBA8 {
		t.Fatalf("legacy clipped adjacent siblings = %#v, want %#v", got, wantRGBA8)
	}
	wantCMYK64 := CMYK64Color{C: 0x4000, M: 0x8000, Y: 0x4000}
	if got := renderPrecisionCMYK64Pixel(t, unclipped); got != wantCMYK64 {
		t.Fatalf("native-CMYK unclipped adjacent siblings = %#v, want %#v", got, wantCMYK64)
	}
	if got := renderPrecisionCMYK64Pixel(t, clipped); got != wantCMYK64 {
		t.Fatalf("native-CMYK clipped adjacent siblings = %#v, want %#v", got, wantCMYK64)
	}
}

func TestPrecisionNoOpClipPreservesSlopedSiblingBoundary(t *testing.T) {
	t.Parallel()

	const content = `<g><path d="M0 0H1L0 1Z" fill="red"/><path d="M1 0V1H0Z" fill="blue"/></g>`
	unclipped := `<svg viewBox="0 0 1 1">` + content + `</svg>`
	clipped := `<svg viewBox="0 0 1 1">
<defs><clipPath id="all"><rect width="1" height="1"/></clipPath></defs>
<g clip-path="url(#all)">` + content + `</g></svg>`

	wantRGBA64 := color.NRGBA64{R: 0x8000, B: 0x8000, A: 0xffff}
	for name, source := range map[string]string{"unclipped": unclipped, "clipped": clipped} {
		if got := renderPrecisionRGBA64Pixel(t, source); got != wantRGBA64 {
			t.Errorf("%s sloped siblings = %#v, want %#v", name, got, wantRGBA64)
		}
	}
	wantCMYK64 := CMYK64Color{C: 0x8000, M: 0xffff, Y: 0x8000}
	for name, source := range map[string]string{"unclipped": unclipped, "clipped": clipped} {
		if got := renderPrecisionCMYK64Pixel(t, source); got != wantCMYK64 {
			t.Errorf("native-CMYK %s sloped siblings = %#v, want %#v", name, got, wantCMYK64)
		}
	}
}

func TestPrecisionNestedSameCellDisjointClipsStayEmpty(t *testing.T) {
	t.Parallel()

	const source = `<svg viewBox="0 0 1 1">
<defs>
  <clipPath id="left"><rect x=".01" width=".05" height="1"/></clipPath>
  <clipPath id="right"><rect x=".15" width=".05" height="1"/></clipPath>
</defs>
<g clip-path="url(#left)"><g clip-path="url(#right)"><rect width="1" height="1" fill="red"/></g></g>
</svg>`

	if got := renderPrecisionRGBA64Pixel(t, source); got != (color.NRGBA64{}) {
		t.Fatalf("disjoint nested same-cell clips rendered %#v, want transparent", got)
	}
	if got := renderPrecisionCMYK64Pixel(t, source); got != (CMYK64Color{}) {
		t.Fatalf("native-CMYK disjoint nested same-cell clips rendered %#v, want paper white", got)
	}
}

func TestRenderCMYKNativeClippedGroupPreservesSpatialComposition(t *testing.T) {
	t.Parallel()

	const source = `<svg viewBox="0 0 1 1">
<defs><clipPath id="half"><rect width=".5" height="1"/></clipPath></defs>
<g clip-path="url(#half)">
  <rect width=".5" height="1" fill="red"/>
  <rect x=".25" width=".5" height="1" fill="blue"/>
</g></svg>`

	icon := mustReadSVG(t, source)
	destination := image.NewCMYK(image.Rect(0, 0, 1, 1))
	if err := icon.RenderCMYK(destination, CMYKOptions{ProcessingMode: CMYKNative}); err != nil {
		t.Fatal(err)
	}
	if got, want := destination.CMYKAt(0, 0), (color.CMYK{C: 64, M: 128, Y: 64}); got != want {
		t.Fatalf("spatially composited clipped native-CMYK group = %#v, want %#v", got, want)
	}
}

func TestPrecisionNarrowClipRetainsExactIntervalPosition(t *testing.T) {
	t.Parallel()

	const narrow = `<rect x=".01" width=".05" height="1" fill="#4a91e8"/>`
	identical := `<svg viewBox="0 0 1 1">
<defs><clipPath id="narrow"><rect x=".01" width=".05" height="1"/></clipPath></defs>
<g clip-path="url(#narrow)">` + narrow + `</g></svg>`
	unclipped := `<svg viewBox="0 0 1 1">` + narrow + `</svg>`
	if got, want := renderPrecisionRGBA64Pixel(t, identical), renderPrecisionRGBA64Pixel(t, unclipped); got != want {
		t.Fatalf("identical narrow clip = %#v, unclipped = %#v", got, want)
	} else if got.A == 0 {
		t.Fatal("identical narrow clip disappeared")
	}

	fullContent := `<svg viewBox="0 0 1 1">
<defs><clipPath id="narrow"><rect x=".01" width=".05" height="1"/></clipPath></defs>
<g clip-path="url(#narrow)"><rect width="1" height="1" fill="#4a91e8"/></g></svg>`
	if got, want := renderPrecisionRGBA64Pixel(t, fullContent), renderPrecisionRGBA64Pixel(t, unclipped); got != want {
		t.Fatalf("full content through narrow clip = %#v, narrow-shape reference = %#v", got, want)
	}

	overlap := `<svg viewBox="0 0 1 1">
<defs><clipPath id="narrow"><rect x=".01" width=".05" height="1"/></clipPath></defs>
<g clip-path="url(#narrow)"><rect width="1" height="1" fill="red"/><rect width="1" height="1" fill="#4a91e8"/></g></svg>`
	if got, want := renderPrecisionRGBA64Pixel(t, overlap), renderPrecisionRGBA64Pixel(t, unclipped); got != want {
		t.Fatalf("overlapping content through narrow clip = %#v, narrow top-color reference = %#v", got, want)
	}
	if got, want := renderRGBA8Pixel(t, overlap), renderRGBA8Pixel(t, unclipped); got != want {
		t.Fatalf("legacy overlapping content through narrow clip = %#v, narrow top-color reference = %#v", got, want)
	}

	nestedOverlap := `<svg viewBox="0 0 1 1">
<defs>
  <clipPath id="outer"><rect width=".2" height="1"/></clipPath>
  <clipPath id="narrow"><rect x=".01" width=".05" height="1"/></clipPath>
</defs>
<g clip-path="url(#outer)"><g clip-path="url(#narrow)">
  <rect width="1" height="1" fill="red"/><rect width="1" height="1" fill="#4a91e8"/>
</g></g></svg>`
	if got, want := renderPrecisionRGBA64Pixel(t, nestedOverlap), renderPrecisionRGBA64Pixel(t, unclipped); got != want {
		t.Fatalf("nested partial-clip overlap = %#v, narrow top-color reference = %#v", got, want)
	}

	disjoint := `<svg viewBox="0 0 1 1">
<defs><clipPath id="narrow"><rect x=".01" width=".05" height="1"/></clipPath></defs>
<g clip-path="url(#narrow)"><rect x=".15" width=".05" height="1" fill="#4a91e8"/></g></svg>`
	if got := renderPrecisionRGBA64Pixel(t, disjoint); got != (color.NRGBA64{}) {
		t.Fatalf("same-cell disjoint content and clip = %#v, want transparent", got)
	}
}

func TestPrecisionIdenticalClipPreservesFractionalCoverageCMYKNative(t *testing.T) {
	t.Parallel()

	const geometry = `<rect x=".1" width=".6" height="1" fill="#d42a71"/>`
	unclipped := `<svg viewBox="0 0 1 1"><g opacity=".63">` + geometry + `</g></svg>`
	clipped := `<svg viewBox="0 0 1 1">
<defs><clipPath id="same"><rect x=".1" width=".6" height="1"/></clipPath></defs>
<g opacity=".63" clip-path="url(#same)">` + geometry + `</g></svg>`

	want := renderPrecisionCMYK64Pixel(t, unclipped)
	got := renderPrecisionCMYK64Pixel(t, clipped)
	if got != want {
		t.Fatalf("identically clipped native-CMYK pixel = %#v, unclipped = %#v", got, want)
	}
	if got == (CMYK64Color{}) {
		t.Fatal("fractional native-CMYK edge unexpectedly remained paper white")
	}
}

func renderPrecisionRGBA64Pixel(t *testing.T, source string) color.NRGBA64 {
	t.Helper()
	icon := mustReadSVG(t, source)
	destination := image.NewNRGBA64(image.Rect(0, 0, 1, 1))
	if err := icon.Render64(destination, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	return destination.NRGBA64At(0, 0)
}

func renderRGBA8Pixel(t *testing.T, source string) color.NRGBA {
	t.Helper()
	icon := mustReadSVG(t, source)
	destination := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	if err := icon.Render(destination); err != nil {
		t.Fatal(err)
	}
	return destination.NRGBAAt(0, 0)
}

func renderPrecisionCMYK64Pixel(t *testing.T, source string) CMYK64Color {
	t.Helper()
	icon := mustReadSVG(t, source)
	destination := NewCMYK64(image.Rect(0, 0, 1, 1))
	if err := icon.RenderCMYK64(destination, CMYKOptions{ProcessingMode: CMYKNative}); err != nil {
		t.Fatal(err)
	}
	return destination.CMYK64At(0, 0)
}
