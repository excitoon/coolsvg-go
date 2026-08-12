package coolsvg_test

import (
	"bytes"
	"fmt"
	"image"
	"strings"

	coolsvg "github.com/excitoon/coolsvg-go"
)

func ExampleSvgIcon_RenderRegion() {
	icon, err := coolsvg.ReadIconStream(strings.NewReader(
		`<svg viewBox="0 0 1 1"><rect width="1" height="1" fill="#336699"/></svg>`,
	), coolsvg.StrictErrorMode)
	if err != nil {
		panic(err)
	}

	icon.SetTarget(0, 0, 100_000, 100_000)
	region := image.Rect(50_000, 60_000, 50_016, 60_016)
	tile := image.NewNRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	if err := icon.RenderRegion(tile, region); err != nil {
		panic(err)
	}

	pixel := tile.NRGBAAt(0, 0)
	fmt.Printf("%02x%02x%02x %d pixels\n", pixel.R, pixel.G, pixel.B, len(tile.Pix)/4)
	// Output: 336699 256 pixels
}

func ExampleSvgIcon_RenderWithOptions() {
	icon, err := coolsvg.ReadIconStream(strings.NewReader(
		`<svg viewBox="0 0 2 1"><rect width="2" height="1" fill="#336699"/></svg>`,
	), coolsvg.StrictErrorMode)
	if err != nil {
		panic(err)
	}

	destination := image.NewNRGBA64(image.Rect(0, 0, 2, 1))
	icon.SetTarget(0, 0, 2, 1)
	if err := icon.RenderWithOptions(destination, coolsvg.RenderOptions{
		Dither: coolsvg.DitherOptions{Mode: coolsvg.DitherBayer8x8},
	}); err != nil {
		panic(err)
	}

	fmt.Printf("%T %dx%d\n", destination, destination.Bounds().Dx(), destination.Bounds().Dy())
	// Output: *image.NRGBA64 2x1
}

func ExampleSvgIcon_EncodeTIFF() {
	icon, err := coolsvg.ReadIconStream(strings.NewReader(
		`<svg viewBox="0 0 2 1"><rect width="2" height="1" fill="#336699"/></svg>`,
	), coolsvg.StrictErrorMode)
	if err != nil {
		panic(err)
	}

	bounds := image.Rect(0, 0, 2, 1)
	icon.SetTarget(0, 0, 2, 1)
	var output bytes.Buffer
	if err := icon.EncodeTIFF(&output, bounds, &coolsvg.TIFFRenderOptions{
		TIFFOptions: coolsvg.TIFFOptions{
			BitsPerSample: 16,
			Compression:   coolsvg.TIFFCompressionDeflate,
		},
	}); err != nil {
		panic(err)
	}

	fmt.Printf("%s %t\n", output.Bytes()[:2], output.Len() > 8)
	// Output: II true
}
