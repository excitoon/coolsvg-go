package coolsvg

import (
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
