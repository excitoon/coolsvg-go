package coolsvg

import (
	"image"
	"image/color"
	"strings"
	"sync"
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

func TestInvalidCSSClipPathDoesNotEraseValidDeclaration(t *testing.T) {
	t.Parallel()

	source := `<svg viewBox="0 0 20 20">
<style>.subject { clip-path: not-a-url }</style>
<defs><clipPath id="valid"><rect width="10" height="10"/></clipPath></defs>
<rect class="subject" width="20" height="20" clip-path="url(#valid)"/>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	style, err := icon.computeStyle(icon.root.children[2], defaultComputedStyle())
	if err != nil {
		t.Fatal(err)
	}
	if !style.hasClip || style.clipRef != "valid" {
		t.Fatalf("clip = %q (set=%v), want valid", style.clipRef, style.hasClip)
	}
}

func TestInvalidDeclarationsAreLenientOnlyInCompatibilityModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		property string
		value    string
	}{
		{property: "stroke-linecap", value: "flat"},
		{property: "stroke-linecap", value: "cubic"},
		{property: "stroke-linecap", value: "quadratic"},
		{property: "stroke-linejoin", value: "arc"},
		{property: "stroke-linejoin", value: "arc-clip"},
		{property: "stroke-linejoin", value: "arc-limit"},
		{property: "fill", value: "not-a-color"},
		{property: "fill-rule", value: "winding"},
		{property: "stroke-width", value: "-1"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.property+"_"+test.value, func(t *testing.T) {
			t.Parallel()
			source := `<svg viewBox="0 0 8 8"><path d="M1 7L4 1L7 7" fill="none" stroke="black" ` +
				test.property + `="` + test.value + `"/></svg>`

			for _, mode := range []ErrorMode{IgnoreErrorMode, WarnErrorMode} {
				icon, err := ReadIconStream(strings.NewReader(source), mode)
				if err != nil {
					t.Fatal(err)
				}
				if err := icon.Render(image.NewNRGBA(image.Rect(0, 0, 8, 8))); err != nil {
					t.Fatalf("mode %d: %v", mode, err)
				}
				warnings := strings.Join(icon.Warnings(), "\n")
				if mode == IgnoreErrorMode && warnings != "" {
					t.Fatalf("ignore mode warnings = %q", warnings)
				}
				if mode == WarnErrorMode && !strings.Contains(warnings, test.property) {
					t.Fatalf("warn mode warnings = %q", warnings)
				}
			}

			icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
			if err != nil {
				t.Fatal(err)
			}
			if err := icon.Render(image.NewNRGBA(image.Rect(0, 0, 8, 8))); err == nil || !strings.Contains(err.Error(), test.property) {
				t.Fatalf("strict mode error = %v", err)
			}
		})
	}
}

func TestWarnModeDeduplicatesConcurrentRenderWarnings(t *testing.T) {
	t.Parallel()

	icon, err := ReadIconStream(strings.NewReader(`<svg viewBox="0 0 8 8"><path d="M1 7L4 1L7 7" fill="none" stroke="black" stroke-linejoin="arc-clip"/></svg>`), WarnErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errors := make(chan error, 16)
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errors <- icon.Render(image.NewNRGBA(image.Rect(0, 0, 8, 8)))
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if warnings := icon.Warnings(); len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want one deduplicated warning", warnings)
	}
}
