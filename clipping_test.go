package coolsvg

import (
	"image"
	"strings"
	"testing"
)

func TestClipPaths(t *testing.T) {
	t.Parallel()

	type probe struct {
		x, y    int
		visible bool
	}
	tests := []struct {
		name           string
		body           string
		probes         []probe
		allTransparent bool
	}{
		{
			name: "rounded geometry ignores paint",
			body: `
<defs>
  <clipPath id="c"><rect x="4" y="4" width="40" height="40" rx="10" fill="none" stroke="none"/></clipPath>
</defs>
<rect width="48" height="48" fill="#d43131" clip-path="url(#c)"/>`,
			probes: []probe{{5, 5, false}, {14, 6, true}, {24, 24, true}, {2, 24, false}},
		},
		{
			name: "transformed clipped group",
			body: `
<defs><clipPath id="c"><rect x="2" y="2" width="12" height="8"/></clipPath></defs>
<g transform="translate(12 8) scale(2 3)" clip-path="url(#c)"><rect width="18" height="12" fill="#d43131"/></g>`,
			probes: []probe{{24, 24, true}, {13, 24, false}, {44, 24, false}, {24, 10, false}, {24, 42, false}},
		},
		{
			name: "object bounding box units",
			body: `
<defs><clipPath id="c" clipPathUnits="objectBoundingBox"><rect x=".25" y=".25" width=".5" height=".5"/></clipPath></defs>
<rect x="8" y="12" width="32" height="24" fill="#d43131" clip-path="url(#c)"/>`,
			probes: []probe{{20, 24, true}, {12, 24, false}, {36, 24, false}, {24, 14, false}, {24, 34, false}},
		},
		{
			name: "sibling clip geometry is unioned",
			body: `
<defs><clipPath id="c"><rect x="4" y="8" width="24" height="20"/><rect x="20" y="20" width="24" height="20"/></clipPath></defs>
<rect width="48" height="48" fill="#d43131" clip-path="url(#c)"/>`,
			probes: []probe{{10, 14, true}, {24, 24, true}, {36, 34, true}, {10, 34, false}, {36, 14, false}},
		},
		{
			name: "nested clips are intersected",
			body: `
<defs>
  <clipPath id="vertical"><rect x="4" y="4" width="24" height="40"/></clipPath>
  <clipPath id="horizontal"><rect x="4" y="4" width="40" height="24"/></clipPath>
</defs>
<g clip-path="url(#vertical)"><g clip-path="url(#horizontal)"><rect width="48" height="48" fill="#d43131"/></g></g>`,
			probes: []probe{{12, 12, true}, {36, 12, false}, {12, 36, false}, {36, 36, false}},
		},
		{
			name: "evenodd clip rule",
			body: `
<defs><clipPath id="c"><path clip-rule="evenodd" d="M4 4H44V44H4Z M16 16H32V32H16Z"/></clipPath></defs>
<rect width="48" height="48" fill="#d43131" clip-path="url(#c)"/>`,
			probes: []probe{{8, 8, true}, {24, 24, false}, {40, 40, true}, {46, 24, false}},
		},
		{
			name: "use inside clip",
			body: `
<defs><rect id="clipShape" width="10" height="12"/><clipPath id="c"><use href="#clipShape" x="20" y="16"/></clipPath></defs>
<rect width="48" height="48" fill="#d43131" clip-path="url(#c)"/>`,
			probes: []probe{{24, 20, true}, {12, 20, false}, {36, 20, false}, {24, 34, false}, {4, 4, false}},
		},
		{
			name:           "empty clip hides everything",
			body:           `<defs><clipPath id="c"/></defs><rect width="48" height="48" fill="#d43131" clip-path="url(#c)"/>`,
			allTransparent: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">` + test.body + `</svg>`
			icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
			if err != nil {
				t.Fatal(err)
			}
			img := image.NewNRGBA(image.Rect(0, 0, 48, 48))
			if err := icon.Render(img); err != nil {
				t.Fatal(err)
			}
			if test.allTransparent {
				for i := 3; i < len(img.Pix); i += 4 {
					if img.Pix[i] != 0 {
						t.Fatalf("pixel %d has alpha %d, want transparent", i/4, img.Pix[i])
					}
				}
				return
			}
			for _, probe := range test.probes {
				alpha := img.NRGBAAt(probe.x, probe.y).A
				if probe.visible && alpha < 240 {
					t.Errorf("pixel (%d,%d) alpha = %d, want visible", probe.x, probe.y, alpha)
				}
				if !probe.visible && alpha > 15 {
					t.Errorf("pixel (%d,%d) alpha = %d, want transparent", probe.x, probe.y, alpha)
				}
			}
		})
	}
}

func TestSiblingClipUnionPreservesComplementarySubpixels(t *testing.T) {
	t.Parallel()

	source := `<svg viewBox="0 0 1 1">
<defs><clipPath id="c"><rect width=".25" height="1"/><rect x=".75" width=".25" height="1"/></clipPath></defs>
<rect width="1" height="1" fill="black" clip-path="url(#c)"/>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	if alpha := img.NRGBAAt(0, 0).A; alpha < 120 || alpha > 136 {
		t.Fatalf("union alpha = %d, want approximately 50%%", alpha)
	}
}

func TestObjectBoundingBoxExcludesDisplayNoneGeometry(t *testing.T) {
	t.Parallel()

	source := `<svg viewBox="0 0 100 20">
<defs><clipPath id="c" clipPathUnits="objectBoundingBox"><rect width=".5" height="1"/></clipPath></defs>
<g clip-path="url(#c)"><rect width="20" height="20"/><rect x="80" width="20" height="20" display="none"/></g>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 100, 20))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	if img.NRGBAAt(5, 10).A < 240 || img.NRGBAAt(15, 10).A > 15 {
		t.Fatal("display:none geometry changed the object bounding box")
	}
}
