package coolsvg_test

import (
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
