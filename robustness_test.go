package coolsvg

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadIconStreamRejectsNilReaders(t *testing.T) {
	t.Parallel()

	if _, err := ReadIconStream(nil); err == nil || err.Error() != "svg: input is nil" {
		t.Fatalf("nil input error = %v", err)
	}
	var source *bytes.Reader
	if _, err := ReadIconStream(source); err == nil || err.Error() != "svg: input is nil" {
		t.Fatalf("typed-nil input error = %v", err)
	}
}

func TestPrecisionAPIsRejectTypedNilImages(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`)
	var destination64 *image.NRGBA64
	if err := icon.RenderWithOptions(destination64, RenderOptions{}); err == nil {
		t.Fatal("RenderWithOptions accepted a typed-nil destination")
	}
	if err := icon.Render64(destination64, RenderOptions{}); err == nil {
		t.Fatal("Render64 accepted a typed-nil destination")
	}
	var source64 *image.NRGBA64
	if err := QuantizeRGBA64(image.NewNRGBA(image.Rect(0, 0, 1, 1)), source64, DitherOptions{}); err == nil {
		t.Fatal("QuantizeRGBA64 accepted a typed-nil source")
	}
	var destination8 *image.NRGBA
	if err := QuantizeRGBA64(destination8, image.NewNRGBA64(image.Rect(0, 0, 1, 1)), DitherOptions{}); err == nil {
		t.Fatal("QuantizeRGBA64 accepted a typed-nil destination")
	}
}

func TestDitherCoordinateOverflowReturnsErrors(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1" fill="#123456"/></svg>`)
	for _, mode := range []DitherMode{DitherBayer8x8, DitherFloydSteinberg} {
		options := DitherOptions{Mode: mode, CoordinateOffset: image.Pt(math.MaxInt, 0)}
		if err := icon.RenderWithOptions(image.NewNRGBA64(image.Rect(0, 0, 1, 1)), RenderOptions{Dither: options}); err == nil || !strings.Contains(err.Error(), "overflows") {
			t.Fatalf("RGBA mode %d overflow error = %v", mode, err)
		}
		if err := icon.RenderCMYK64(NewCMYK64(image.Rect(0, 0, 1, 1)), CMYKOptions{Dither: options}); err == nil || !strings.Contains(err.Error(), "overflows") {
			t.Fatalf("CMYK mode %d overflow error = %v", mode, err)
		}
	}
}

func TestPrecisionSurfaceLimitReturnsError(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`)
	destination := hugePrecisionImage{bounds: image.Rect(0, 0, math.MaxInt/4, 1)}
	if err := icon.RenderWithOptions(destination, RenderOptions{}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("huge surface error = %v", err)
	}
}

func TestRenderCMYKCompatibilityPathRejectsOversizedBand(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`)
	bounds := image.Rect(0, 0, math.MaxInt/4+1, 1)
	destination := &image.CMYK{Rect: bounds}
	if err := icon.RenderCMYKRegion(destination, bounds, CMYKOptions{}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized compatibility CMYK error = %v", err)
	}
}

func TestSetTargetRejectsNonFiniteTransform(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1" fill="red"/></svg>`)
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		icon.SetTarget(value, 0, 1, 1)
		destination := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		if err := icon.Render(destination); err != nil {
			t.Fatalf("non-finite target %v returned error: %v", value, err)
		}
		if got := destination.NRGBAAt(0, 0); got != (color.NRGBA{}) {
			t.Fatalf("non-finite target %v rendered %#v, want transparent", value, got)
		}
	}
}

func TestRendererFlattenBudgetRejectsAdversarialCurve(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><path d="M0 0C1e100 1e100 -1e100 -1e100 1 1"/></svg>`)
	surface, err := newRGBAFloatSurface(image.Rect(0, 0, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	renderer := newRGBAFloatRenderer(icon, surface.bounds)
	renderer.flatten = flattenBudget{maximumWork: 64, maximumPoints: 64, maximumRasterWork: maxRenderRasterWork}
	if err := renderer.renderNode(surface, icon.root, defaultComputedStyle(), icon.target, icon.ViewBox, 1); err == nil || !strings.Contains(err.Error(), "flattening work exceeds 64") {
		t.Fatalf("adversarial render error = %v", err)
	}
}

func TestPrecisionLayerBudgetIsSharedAcrossNestedGroups(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><g opacity=".9"><g opacity=".9"><rect width="1" height="1"/></g></g></svg>`)
	surface, err := newRGBAFloatSurface(image.Rect(0, 0, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	renderer := newRGBAFloatRenderer(icon, surface.bounds)
	renderer.surfaces.maximumBytes = 4 * 8 // Permit one RGBA layer, but not two live layers.
	if err := renderer.renderNode(surface, icon.root, defaultComputedStyle(), icon.target, icon.ViewBox, 1); err == nil || !strings.Contains(err.Error(), "live floating-point layers exceed") {
		t.Fatalf("nested-layer budget error = %v", err)
	}
}

func TestRasterBudgetRejectsExcessiveEdgeScanning(t *testing.T) {
	t.Parallel()

	contours := []contour{{closed: true, points: []Point{{0, 0}, {8, 0}, {8, 8}, {0, 8}, {0, 0}}}}
	budget := flattenBudget{maximumRasterWork: 1}
	if _, err := rasterizeContoursPrecisionClipped(image.Rect(0, 0, 8, 8), contours, nonZero, nil, nil, &budget, nil); err == nil || !strings.Contains(err.Error(), "rasterization work exceeds 1") {
		t.Fatalf("raster budget error = %v", err)
	}
}

func TestRepresentativeComplexStrokeStaysWithinRenderBudget(t *testing.T) {
	t.Parallel()

	var path strings.Builder
	path.WriteString("M0 256")
	for x := 2; x <= 512; x += 2 {
		fmt.Fprintf(&path, "L%d 256", x)
	}
	icon := mustReadSVG(t, `<svg viewBox="0 0 512 512"><path d="`+path.String()+`" fill="none" stroke="black" stroke-width="2"/></svg>`)
	destination := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	if err := icon.Render(destination); err != nil {
		t.Fatalf("representative complex stroke failed: %v", err)
	}
	if got := destination.NRGBAAt(256, 256); got.A != 0xff || got.R != 0 || got.G != 0 || got.B != 0 {
		t.Fatalf("representative stroke center = %#v, want opaque black", got)
	}
}

func TestFloydStateIsolatedFromDestinationCallbacks(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 8 2"><rect width="8" height="2" fill="#345678"/></svg>`)
	state := &FloydSteinbergState{}
	destination := &resettingDrawImage{
		pixels: image.NewNRGBA(image.Rect(0, 0, 8, 2)),
		state:  state,
	}
	done := make(chan error, 1)
	go func() {
		done <- icon.RenderWithOptions(destination, RenderOptions{Dither: DitherOptions{
			Mode: DitherFloydSteinberg, State: state,
		}})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("destination callback deadlocked Floyd-Steinberg state")
	}
}

func TestTypedNilCMYKHooksReturnErrorsOrUseDefaultBackground(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1" fill="red"/></svg>`)
	var space *nilCMYKFloatSpace
	if err := icon.RenderCMYK64(NewCMYK64(image.Rect(0, 0, 1, 1)), CMYKOptions{ColorSpaceFloat: space}); err == nil {
		t.Fatal("typed-nil CMYK color space was accepted")
	}
	var background *nilBackgroundColor
	if err := icon.RenderCMYK64(NewCMYK64(image.Rect(0, 0, 1, 1)), CMYKOptions{Background: background}); err != nil {
		t.Fatalf("typed-nil background was not treated as absent: %v", err)
	}
	if err := icon.RenderCMYK(image.NewCMYK(image.Rect(0, 0, 1, 1)), CMYKOptions{
		Background: background, ProcessingMode: CMYKNative,
	}); err != nil {
		t.Fatalf("legacy native typed-nil background was not treated as absent: %v", err)
	}
}

type hugePrecisionImage struct {
	bounds image.Rectangle
}

func (p hugePrecisionImage) ColorModel() color.Model   { return color.NRGBA64Model }
func (p hugePrecisionImage) Bounds() image.Rectangle   { return p.bounds }
func (hugePrecisionImage) At(_, _ int) color.Color     { return color.NRGBA64{} }
func (hugePrecisionImage) Set(_, _ int, _ color.Color) {}

type resettingDrawImage struct {
	pixels *image.NRGBA
	state  *FloydSteinbergState
	once   sync.Once
}

func (p *resettingDrawImage) ColorModel() color.Model { return p.pixels.ColorModel() }
func (p *resettingDrawImage) Bounds() image.Rectangle { return p.pixels.Bounds() }
func (p *resettingDrawImage) At(x, y int) color.Color { return p.pixels.At(x, y) }
func (p *resettingDrawImage) Set(x, y int, value color.Color) {
	p.once.Do(p.state.Reset)
	p.pixels.Set(x, y, value)
}

type nilCMYKFloatSpace struct{}

func (*nilCMYKFloatSpace) ConvertRGBFloat(_, _, _ float64) (_, _, _, _ float64) {
	panic("typed-nil CMYK color space called")
}

type nilBackgroundColor struct{}

func (*nilBackgroundColor) RGBA() (_, _, _, _ uint32) {
	panic("typed-nil background called")
}
