package coolsvg

import (
	"bytes"
	"image"
	"math"
	"testing"
)

func FuzzParsePath(f *testing.F) {
	for _, seed := range []string{
		"M0 0H16V16H0Z",
		"M1 2l3 0h2v3c1 0 2 1 3 1s2 1 3 0q1-1 2 0t2 0a3 2 30 0 1 4 5z",
		"M0 0A10 10 0 0110 10",
		"M0 0Q100 0 0 0Z M4 4L8 8",
		"M-1e308 -1e308A1e308 1e308 360 1 0 1e308 1e308",
		"M0 0A10 10 0 2 0 20 20",
		"L1 1",
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data string) {
		// Keep fuzz workers focused on parser state transitions instead of very
		// large allocation behavior, which is covered by explicit input limits.
		if len(data) > 64<<10 {
			return
		}
		_, _ = ParsePath(data)
	})
}

func FuzzReadAndRenderSVG(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`<svg viewBox="0 0 16 16"><path d="M1 1H15V15H1Z" fill="#f60"/></svg>`),
		[]byte(`<svg viewBox="0 0 32 32">
<defs>
  <linearGradient id="g"><stop stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>
  <clipPath id="c"><circle cx="16" cy="16" r="13"/><rect x="2" y="14" width="28" height="4"/></clipPath>
</defs>
<g clip-path="url(#c)" opacity=".6"><rect width="32" height="32" fill="url(#g)"/><path d="M2 28Q16 2 30 28Z" fill="white"/></g>
</svg>`),
		[]byte(`<svg viewBox="0 0 8 8"><style>rect.hot { fill: currentColor }</style><g color="orange"><rect class="hot" width="8" height="8"/></g></svg>`),
		[]byte(`<svg viewBox="0 0 4 4"><defs><g id="shape"><rect width="4" height="4"/></g></defs><use href="#shape"/></svg>`),
		[]byte(`<svg viewBox="0 0 4 4"><defs><g id="a"><use href="#a"/></g></defs><use href="#a"/></svg>`),
		[]byte(`<!DOCTYPE svg><svg viewBox="0 0 1 1"/>`),
		[]byte(`<svg viewBox="0 0 1 1"><path d="M0 0 L"/></svg>`),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:x="urn:foreign" viewBox="0 0 1 1"><x:path d="M0 0H1V1Z"/></svg>`),
		{0xff, 0xfe, '<', 's', 'v', 'g', '>'},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, source []byte) {
		if len(source) > 64<<10 {
			return
		}
		icon, err := ReadIconStream(bytes.NewReader(source), StrictErrorMode)
		if err != nil || !boundedFuzzScene(icon) {
			return
		}

		icon.SetTarget(0, 0, 32, 32)
		full := image.NewNRGBA(image.Rect(0, 0, 32, 32))
		if err := icon.Render(full); err != nil {
			return
		}
		again := image.NewNRGBA(full.Bounds())
		if err := icon.Render(again); err != nil {
			t.Fatalf("second render failed after the first succeeded: %v", err)
		}
		if !bytes.Equal(full.Pix, again.Pix) {
			t.Fatal("repeated rendering was not deterministic")
		}

		region := image.Rect(7, 5, 23, 21)
		tile := image.NewNRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
		if err := icon.RenderRegion(tile, region); err != nil {
			t.Fatalf("region render failed after the full render succeeded: %v", err)
		}
		for y := region.Min.Y; y < region.Max.Y; y++ {
			for x := region.Min.X; x < region.Max.X; x++ {
				if got, want := tile.NRGBAAt(x-region.Min.X, y-region.Min.Y), full.NRGBAAt(x, y); got != want {
					t.Fatalf("region pixel (%d,%d) = %#v, want %#v", x, y, got, want)
				}
			}
		}
	})
}

func boundedFuzzScene(icon *SvgIcon) bool {
	const (
		maxNodes        = 256
		maxPathSegments = 64
		maxCoordinate   = 1e6
	)
	if icon == nil || icon.root == nil {
		return false
	}
	stack := []*node{icon.root}
	nodes, segments := 0, 0
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodes++
		if nodes > maxNodes {
			return false
		}
		if n.path != nil {
			segments += len(n.path.segments)
			if segments > maxPathSegments {
				return false
			}
			for _, segment := range n.path.segments {
				for _, point := range segment.p {
					if math.IsNaN(point.X) || math.IsNaN(point.Y) || math.IsInf(point.X, 0) || math.IsInf(point.Y, 0) ||
						math.Abs(point.X) > maxCoordinate || math.Abs(point.Y) > maxCoordinate {
						return false
					}
				}
			}
		}
		stack = append(stack, n.children...)
	}
	return true
}
