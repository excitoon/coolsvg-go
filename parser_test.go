package coolsvg

import (
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

	icon, err := ReadReplacingCurrentColor(strings.NewReader(`<svg viewBox="0 0 1 1"><rect width="1" height="1" fill="currentColor"/></svg>`), "#123456")
	if err != nil {
		t.Fatal(err)
	}
	style, err := icon.computeStyle(icon.root.children[0], defaultComputedStyle())
	if err != nil {
		t.Fatal(err)
	}
	if style.fill.color != (color.NRGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Fatalf("fill = %#v", style.fill.color)
	}
}
