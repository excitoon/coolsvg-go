package coolsvg

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestReadIconStreamRetainsSceneAndMetadata(t *testing.T) {
	t.Parallel()

	icon, err := ReadIconStream(strings.NewReader(`
<svg xmlns="http://www.w3.org/2000/svg" viewBox="10 20 80 40">
  <title>Sample icon</title>
  <desc>Parser fixture</desc>
  <defs><clipPath id="round"><rect width="80" height="40" rx="8"/></clipPath></defs>
  <g clip-path="url(#round)" transform="translate(2 3)"><path d="M0 0h80v40H0z"/></g>
</svg>`), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	if icon.ViewBox != (ViewBox{X: 10, Y: 20, W: 80, H: 40}) {
		t.Fatalf("ViewBox = %+v", icon.ViewBox)
	}
	if len(icon.Titles) != 1 || icon.Titles[0] != "Sample icon" {
		t.Fatalf("Titles = %#v", icon.Titles)
	}
	if len(icon.Descriptions) != 1 || icon.Descriptions[0] != "Parser fixture" {
		t.Fatalf("Descriptions = %#v", icon.Descriptions)
	}
	if icon.ids["round"] == nil || icon.ids["round"].tag != "clippath" {
		t.Fatal("clipPath definition was not retained")
	}
}

func TestStrictModeRejectsUnsupportedElement(t *testing.T) {
	t.Parallel()

	_, err := ReadIconStream(strings.NewReader(`<svg viewBox="0 0 10 10"><text>hello</text></svg>`), StrictErrorMode)
	if err == nil || !strings.Contains(err.Error(), "unsupported SVG element <text>") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParserRejectsDoctype(t *testing.T) {
	t.Parallel()

	_, err := ReadIconStream(strings.NewReader(`<!DOCTYPE svg><svg viewBox="0 0 10 10"/>`))
	if err == nil || !strings.Contains(err.Error(), "document type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParserAcceptsStandardExternalSVGDoctypeWithoutResolvingIt(t *testing.T) {
	t.Parallel()

	for _, declaration := range []string{
		`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.0//EN" "http://www.w3.org/TR/2001/REC-SVG-20010904/DTD/svg10.dtd">`,
		`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd" >`,
		`<!DOCTYPE svg SYSTEM "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">`,
	} {
		if _, err := ReadIconStream(strings.NewReader(declaration + `<svg viewBox="0 0 10 10"/>`)); err != nil {
			t.Fatalf("standard SVG doctype %q: %v", declaration, err)
		}
	}

	malicious := `<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd" [<!ENTITY x "expanded">]><svg viewBox="0 0 1 1"><title>&x;</title></svg>`
	if _, err := ReadIconStream(strings.NewReader(malicious)); err == nil || !strings.Contains(err.Error(), "document type") {
		t.Fatalf("internal-subset doctype error = %v", err)
	}
}

func TestParserDuplicateIDFollowsErrorModeAndKeepsFirstDefinition(t *testing.T) {
	t.Parallel()

	const source = `<svg viewBox="0 0 1 1"><defs><rect id="shape" width="1" height="1" fill="red"/><rect id="shape" width="1" height="1" fill="blue"/></defs><use href="#shape"/></svg>`
	if _, err := ReadIconStream(strings.NewReader(source), StrictErrorMode); err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("strict duplicate-id error = %v", err)
	}
	icon, err := ReadIconStream(strings.NewReader(source), IgnoreErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	destination := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	if err := icon.Render(destination); err != nil {
		t.Fatal(err)
	}
	if got := destination.NRGBAAt(0, 0); got != (color.NRGBA{R: 255, A: 255}) {
		t.Fatalf("duplicate-id reference = %#v, want first red definition", got)
	}
}

func TestReadIconStreamDecodesLegacyXMLCharset(t *testing.T) {
	t.Parallel()

	source := []byte("<?xml version=\"1.0\" encoding=\"iso-8859-1\"?><svg viewBox=\"0 0 1 1\"><title>caf\xe9</title></svg>")
	icon, err := ReadIconStream(bytes.NewReader(source), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	if len(icon.Titles) != 1 || icon.Titles[0] != "café" {
		t.Fatalf("titles = %#v", icon.Titles)
	}
}

func TestForeignNamespaceCannotSmuggleSVGGeometry(t *testing.T) {
	t.Parallel()

	source := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:x="urn:foreign" viewBox="0 0 4 4"><x:path d="M0 0H4V4H0Z"/></svg>`
	icon, err := ReadIconStream(strings.NewReader(source), IgnoreErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			t.Fatal("foreign path was rendered as SVG geometry")
		}
	}
	if _, err := ReadIconStream(strings.NewReader(source), StrictErrorMode); err == nil {
		t.Fatal("strict mode accepted a foreign element")
	}
}

func TestForeignAttributeCannotSmuggleSVGGeometry(t *testing.T) {
	t.Parallel()

	source := `<svg xmlns:x="urn:foreign" viewBox="0 0 4 4"><path x:d="M0 0H4V4H0Z" fill="red"/></svg>`
	icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			t.Fatal("foreign path data was rendered as SVG geometry")
		}
	}
}

func TestUnqualifiedHrefWinsOverLegacyXLink(t *testing.T) {
	t.Parallel()

	source := `<svg xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 2 1">
<defs><rect id="red" width="1" height="1" fill="red"/><rect id="blue" width="1" height="1" fill="blue"/></defs>
<use href="#blue" xlink:href="#red"/>
<use x="1" xlink:href="#red"/>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	if got := img.NRGBAAt(0, 0); got != (color.NRGBA{B: 255, A: 255}) {
		t.Fatalf("href precedence rendered %#v, want blue", got)
	}
	if got := img.NRGBAAt(1, 0); got != (color.NRGBA{R: 255, A: 255}) {
		t.Fatalf("legacy xlink:href rendered %#v, want red", got)
	}
}

func TestUnsupportedSubtreeIsNotParsedOrReferenceable(t *testing.T) {
	t.Parallel()

	source := `<svg viewBox="0 0 4 4"><foreignObject><path id="hidden" d="not valid path data"/></foreignObject><use href="#hidden"/></svg>`
	icon, err := ReadIconStream(strings.NewReader(source), IgnoreErrorMode)
	if err != nil {
		t.Fatalf("ignored subtree was parsed: %v", err)
	}
	if icon.ids["hidden"] != nil {
		t.Fatal("an ID inside an unsupported subtree was registered")
	}
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			t.Fatal("unsupported subtree was rendered through use")
		}
	}
}

func TestUnsupportedStylesAndMetadataAreIsolated(t *testing.T) {
	t.Parallel()

	source := `<svg xmlns:x="urn:foreign" viewBox="0 0 2 1">
<x:style>rect { fill: red }</x:style>
<foreignObject><style>rect { fill: green }</style><title>foreign</title></foreignObject>
<rect width="2" height="1" fill="blue"/>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(source), IgnoreErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	if len(icon.Titles) != 0 {
		t.Fatalf("foreign titles = %#v", icon.Titles)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	if got := img.NRGBAAt(0, 0); got != (color.NRGBA{B: 255, A: 255}) {
		t.Fatalf("foreign style affected fill: %#v", got)
	}
}

func TestMalformedElementIsSkippedOnlyInCompatibilityModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		source     string
		wantDetail string
	}{
		{
			name:       "path data",
			source:     `<path d="hoop" fill="blue"/>`,
			wantDetail: "svg path",
		},
		{
			name:       "container transform and subtree",
			source:     `<g transform="rotate(nope)"><rect width="2" height="2" fill="blue"/></g>`,
			wantDetail: "transform",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := `<svg viewBox="0 0 4 2">` + test.source + `<rect x="2" width="2" height="2" fill="red"/></svg>`
			for _, mode := range []ErrorMode{IgnoreErrorMode, WarnErrorMode} {
				icon, err := ReadIconStream(strings.NewReader(source), mode)
				if err != nil {
					t.Fatalf("mode %d: %v", mode, err)
				}
				warnings := strings.Join(icon.Warnings(), "\n")
				if mode == IgnoreErrorMode && warnings != "" {
					t.Fatalf("ignore mode warnings = %q", warnings)
				}
				if mode == WarnErrorMode && !strings.Contains(warnings, test.wantDetail) {
					t.Fatalf("warn mode warnings = %q", warnings)
				}

				img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
				if err := icon.Render(img); err != nil {
					t.Fatal(err)
				}
				if alpha := img.NRGBAAt(0, 0).A; alpha != 0 {
					t.Fatalf("skipped element alpha = %d", alpha)
				}
				if got := img.NRGBAAt(3, 0); got != (color.NRGBA{R: 255, A: 255}) {
					t.Fatalf("valid sibling = %#v", got)
				}
			}

			_, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
			if err == nil || !strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("strict mode error = %v", err)
			}
		})
	}
}

func TestSetTargetMapsViewBox(t *testing.T) {
	t.Parallel()

	icon, err := ReadIconStream(strings.NewReader(`<svg viewBox="10 20 40 20"/>`))
	if err != nil {
		t.Fatal(err)
	}
	icon.SetTarget(5, 7, 80, 60)
	min := icon.target.Apply(Point{10, 20})
	max := icon.target.Apply(Point{50, 40})
	if min != (Point{5, 7}) || max != (Point{85, 67}) {
		t.Fatalf("mapped corners = %+v, %+v", min, max)
	}
}

func TestParseSVGColorForms(t *testing.T) {
	t.Parallel()

	tests := map[string]color.NRGBA{
		"#f60":                 {R: 255, G: 102, A: 255},
		"#ff660080":            {R: 255, G: 102, A: 128},
		"rgb(100%, 40%, 0%)":   {R: 255, G: 102, A: 255},
		"rgb(255 102 0 / 50%)": {R: 255, G: 102, A: 128},
		"hsl(24, 100%, 50%)":   {R: 255, G: 102, A: 255},
		"rebeccapurple":        {R: 102, G: 51, B: 153, A: 255},
	}
	for source, want := range tests {
		source, want := source, want
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSVGColor(source)
			if err != nil {
				t.Fatal(err)
			}
			if got := color.NRGBAModel.Convert(got).(color.NRGBA); got != want {
				t.Fatalf("color = %#v, want %#v", got, want)
			}
		})
	}
}

func TestReadReplacingCurrentColor(t *testing.T) {
	t.Parallel()

	icon, err := ReadReplacingCurrentColor(strings.NewReader(`<svg viewBox="0 0 1 1"><g color="currentColor"><rect width="1" height="1" fill="currentColor"/></g></svg>`), "#123456")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	if err := icon.Render(img); err != nil {
		t.Fatal(err)
	}
	if got := img.NRGBAAt(0, 0); got != (color.NRGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Fatalf("fill = %#v", got)
	}
}
