package coolsvg

import (
	"image/color"
	"strings"
	"testing"
)

func TestCSSCascade(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		styles string
		path   string
		want   color.NRGBA
	}{
		{
			name:   "author rule beats presentation attribute",
			styles: `<style>path { fill: red }</style>`,
			path:   `<path fill="blue" d="M0 0h1v1z"/>`,
			want:   color.NRGBA{R: 255, A: 255},
		},
		{
			name:   "important beats specificity",
			styles: `<style>.a { fill: red !important } #p { fill: blue }</style>`,
			path:   `<path id="p" class="a" d="M0 0h1v1z"/>`,
			want:   color.NRGBA{R: 255, A: 255},
		},
		{
			name:   "source order spans style blocks",
			styles: `<style>.a { fill: red } .a { fill: green }</style><style>.a { fill: blue }</style>`,
			path:   `<path class="a" d="M0 0h1v1z"/>`,
			want:   color.NRGBA{B: 255, A: 255},
		},
		{
			name:   "universal selector has zero specificity",
			styles: `<style>path { fill: red } * { fill: blue }</style>`,
			path:   `<path d="M0 0h1v1z"/>`,
			want:   color.NRGBA{R: 255, A: 255},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			icon, err := ReadIconStream(strings.NewReader(`<svg viewBox="0 0 1 1">`+test.styles+test.path+`</svg>`), StrictErrorMode)
			if err != nil {
				t.Fatal(err)
			}
			n := icon.root.children[len(icon.root.children)-1]
			style, err := icon.computeStyle(n, defaultComputedStyle())
			if err != nil {
				t.Fatal(err)
			}
			if style.fill.color != test.want {
				t.Fatalf("fill = %#v, want %#v", style.fill.color, test.want)
			}
		})
	}
}

func TestCSSClipPathBeatsPresentationAttribute(t *testing.T) {
	t.Parallel()

	source := `<svg viewBox="0 0 20 20">
<style>.subject { clip-path: url(#small) }</style>
<defs>
  <clipPath id="small"><rect width="10" height="10"/></clipPath>
  <clipPath id="large"><rect width="20" height="20"/></clipPath>
</defs>
<rect class="subject" width="20" height="20" fill="red" clip-path="url(#large)"/>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	style, err := icon.computeStyle(icon.root.children[2], defaultComputedStyle())
	if err != nil {
		t.Fatal(err)
	}
	if !style.hasClip || style.clipRef != "small" {
		t.Fatalf("clip = %q (set=%v), want small", style.clipRef, style.hasClip)
	}
}
