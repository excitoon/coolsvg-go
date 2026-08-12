# coolsvg-go

[![CI](https://github.com/excitoon/coolsvg-go/actions/workflows/ci.yml/badge.svg)](https://github.com/excitoon/coolsvg-go/actions/workflows/ci.yml)

MIT-licensed, pure-Go SVG rasterization with scene-tree-correct clipping and
bounded-memory rendering for very large canvases, plus profile-pluggable CMYK
separation, real 16-bit output, deterministic dithering, and streaming
classic TIFF/BigTIFF export.

`coolsvg-go` is an independent, MIT-licensed reimplementation of the useful
high-level API of [`oksvg`](https://github.com/srwiley/oksvg). It keeps groups,
definitions, and references intact through rendering, so a clip path is applied
to the element or group that owns it instead of being lost when paths are
flattened.

The renderer uses Go's `image` interfaces, requires no CGO or native libraries,
and has no dependency on `rasterx`.

Large canvases are a first-class use case. A virtual target such as
100,000×100,000 can be rasterized tile by tile without allocating the complete
image. Gradients, transforms, clipping, opacity, and antialiasing stay anchored
to full-canvas coordinates, so independently rendered tiles join without
restarting a gradient at each tile edge.

## Install

```sh
go get github.com/excitoon/coolsvg-go
```

The module requires Go 1.26 or newer.

## Render an icon

```go
package example

import (
	"image"
	"io"

	"github.com/excitoon/coolsvg-go"
)

func renderSVG(source io.Reader, width, height int) (image.Image, error) {
	icon, err := coolsvg.ReadIconStream(source, coolsvg.StrictErrorMode)
	if err != nil {
		return nil, err
	}

	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	icon.SetTarget(0, 0, float64(width), float64(height))
	if err := icon.Render(destination); err != nil {
		return nil, err
	}
	return destination, nil
}
```

Use `icon.Draw(destination, opacity)` to composite with an opacity from zero to
one. Configure the target before sharing an icon; concurrent draws of an
unchanged icon are safe.

## Render a large canvas in tiles

`RenderRegion` maps a global region of the virtual target into any equally
sized destination image. Internal masks and compositing layers are bounded by
the tile, not by the complete target:

```go
icon.SetTarget(0, 0, 100_000, 100_000)

region := image.Rect(49_152, 65_536, 50_176, 66_560)
tile := image.NewNRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
if err := icon.RenderRegion(tile, region); err != nil {
	return err
}
```

Use `DrawRegion` for the opacity-taking form. Region rendering is tested for
pixel-identical results against the same area of a normal render, including
user-space gradients, transformed clips, and group opacity. A regression test
also renders a bounded tile at the far edge of a 100,000×100,000 target.

## High-precision rendering

`RenderWithOptions` and `RenderRegionWithOptions` retain floating-point paint,
gradient, coverage, and compositing values until destination quantization.
Choose an 8- or 16-bit destination through the standard Go image type:

```go
destination := image.NewNRGBA64(image.Rect(0, 0, width, height))
err := icon.RenderWithOptions(destination, coolsvg.RenderOptions{
	Dither: coolsvg.DitherOptions{Mode: coolsvg.DitherBayer8x8},
})
```

Sixteen-bit samples are quantized from that higher-precision working data; they
are not produced by repeating the bytes of an 8-bit render. `DitherNone` uses
nearest rounding. `DitherBayer8x8` is anchored to global canvas coordinates,
so tiles retain the same ordered pattern. `DitherFloydSteinberg` uses
deterministic serpentine diffusion; independently rendered bands must be
full-width, consecutive, and share a `FloydSteinbergState`. Dithering applies
to RGB or CMYK color channels; RGBA alpha always uses nearest rounding.

Do not split a Floyd–Steinberg scanline into horizontal tiles. Strip height
may vary, but every band must keep the same full-image x-range so residuals
cross only memory boundaries, never image boundaries. `SvgIcon.EncodeTIFF`
maintains that state automatically, and changing `RowsPerStrip` does not change
the quantized pixels.

## Stream TIFF and BigTIFF

`EncodeSVGToTIFF` and `SvgIcon.EncodeTIFF` render directly into bounded
full-width strips. The complete raster is never allocated, and the automatic
variant selects classic TIFF when offsets fit or BigTIFF otherwise:

```go
icon.SetTarget(0, 0, 100_000, 100_000)
err := icon.EncodeTIFF(output, image.Rect(0, 0, 100_000, 100_000),
	&coolsvg.TIFFRenderOptions{TIFFOptions: coolsvg.TIFFOptions{
		BitsPerSample: 16,
		Compression:   coolsvg.TIFFCompressionDeflate,
		Dither: coolsvg.DitherOptions{
			Mode: coolsvg.DitherBayer8x8,
		},
	}})
```

The writer supports RGB, straight-alpha RGBA, and CMYK samples; 8- and 16-bit
depth; Deflate, PackBits, or no compression; ICC profile embedding; byte-order
selection; and explicit classic TIFF or BigTIFF selection. A seekable file is
normally encoded in one rendering pass. A non-seekable compressed writer, or a
forced classic file whose conservative size bound does not fit, may render a
strip twice to plan offsets while still retaining only one strip at a time.
Generic `EncodeTIFF` conversion to RGB or CMYK retains straight color channels
and discards alpha; callers can composite a background first. Direct SVG RGB
export instead flattens onto its configured background, or opaque white.

## Export CMYK separations

`RenderCMYK` and `RenderCMYKRegion` write Go `image.CMYK` rasters;
`RenderCMYK64` and `RenderCMYK64Region` write the package's 16-bit `CMYK64`
image. Supply a `CMYKColorSpace`, `CMYKColorSpace64`, or
`CMYKColorSpaceFloat` implementation for a particular press profile, lookup
table, or separation policy; the zero value uses Go's generic, unprofiled
Device-CMYK formula.

The processing order is explicit:

- `CMYKPostConvert` (the default) implements `CMYK(grad(svg))`: render and
  composite according to the SVG's sRGB behavior, then separate the finished
  pixels. This is the SVG-faithful mode.
- `CMYKNative` implements `grad(CMYK(svg))`: separate paint colors and gradient
  stops first, interpolate C/M/Y/K channels, and composite in a CMYK-native
  knockout raster. This is useful when the ink ramp itself must be controlled.

```go
tile := image.NewCMYK(image.Rect(0, 0, region.Dx(), region.Dy()))
err := icon.RenderCMYKRegion(tile, region, coolsvg.CMYKOptions{
	ColorSpace:     pressColorSpace,
	ProcessingMode: coolsvg.CMYKNative,
})
```

CMYK output remains tile-bounded. The color-space hook provides separation
values only; embedding an ICC profile belongs to the chosen file encoder.
Native mode is channel-wise source-over, not physical overprint, spot colors,
trapping, or rendering-intent emulation.

## SIMD acceleration

The default build is portable pure Go. On Go 1.26 and AMD64, the experimental
Go SIMD backend accelerates clip-mask union and intersection with AVX or AVX2,
selected through runtime CPU checks:

```sh
GOEXPERIMENT=simd go build ./...
```

This uses Go source only—there is no assembly or CGO fallback. CI tests both the
portable build and the SIMD-enabled build. The `simd/archsimd` API is still an
upstream Go experiment, so enabling it is intentionally opt-in for library
builds. Tagged AMD64 CLI archives enable it and retain a runtime scalar
fallback for CPUs without AVX.

## CLI

Install the rasterizer:

```sh
go install github.com/excitoon/coolsvg-go/cmd/coolsvg@latest
```

Render a file, or use `-` for standard input:

```sh
coolsvg -width 512 -height 512 -o icon.png icon.svg
coolsvg -width 100000 -height 100000 -format tiff -bit-depth 16 \
  -dither bayer -tiff-compression deflate -o artwork.tiff artwork.svg
```

The CLI uses compatibility mode by default and reports unsupported elements.
Pass `-strict` to reject them.

The output contract is explicit:

- `-format auto|png|tiff` defaults to `auto`. Auto recognizes `.png`, `.tif`,
  and `.tiff` case-insensitively. An unknown extension is an error; an explicit
  format that conflicts with a recognized extension is also an error. Without
  `-o`, the default is `output.png`, or `output.tiff` for explicit TIFF.
- `-bit-depth 8|16` defaults to 8. Both depths are quantized from the
  high-precision renderer; 16-bit output is not byte-expanded 8-bit data.
- `-dither none|bayer|floyd-steinberg` defaults to `none`. Bayer is
  canvas-coordinate-stable. Floyd–Steinberg is scanline-dependent; streaming
  TIFF maintains the required deterministic full-width state between strips.
- `-tiff-compression deflate|none` defaults to Deflate and is rejected for PNG.
- `-color-model rgba|rgb|cmyk` defaults to RGBA. RGB flattens transparency onto
  white. CMYK requires TIFF and may select
  `-cmyk-processing=post-convert|native`; post-convert is the SVG-faithful
  default.

PNG uses an in-memory high-precision raster and has a 512 MiB allocation safety
limit. Use TIFF for larger output; its renderer and encoder are strip-bounded
and automatically use BigTIFF when classic offsets cannot fit. Every output is
written to a temporary file in the destination directory, synced, and then
atomically replaced, so parse, render, or encode errors do not leave a partial
final file.

## Resource limits

Parsing and rendering untrusted input is bounded. The parser accepts at most
64 MiB of XML, 512 nested elements, one million elements, and one million path
segments. Rendering also caps reference depth, scene operations, curve
flattening, generated points, raster work, individual floating-point surfaces,
and simultaneously live compositing layers. TIFF strip tables and source
buffers have independent limits. Exceeding a limit returns an error rather than
attempting an unbounded allocation or amount of work.

These safeguards constrain the active strip or tile, not the virtual canvas.
A 100,000×100,000 target remains valid when exported through bounded
full-width TIFF strips or explicit regions. Callers that set `RowsPerStrip`
manually receive an error when a strip would exceed the working-set bound.

## Clipping semantics

Clipping is part of the scene graph, not a post-processing rectangle. The
implementation covers:

- `clipPathUnits="userSpaceOnUse"` and `"objectBoundingBox"`;
- transforms on the clipped element, group, clip path, and clip geometry;
- union of sibling geometry inside one clip path;
- intersection of nested and ancestor clip paths;
- non-zero and even-odd `clip-rule`;
- `<use>` inside clip paths;
- anti-aliased sample-level union and intersection; and
- empty clip paths, which correctly produce an empty result.

Clip geometry is derived from shapes, independent of their fill, stroke, and
opacity paint. This is important for exported design-tool SVGs that annotate
clip shapes with `fill="none"` or `stroke="none"`.

Fractional clip boundaries are partitioned before source-over compositing.
Consequently, an identity clip cannot alter antialiased seams between adjacent
shapes, and a group clip is applied once to the composed group rather than once
per child.

## Supported SVG subset

The package targets self-contained icons rather than arbitrary web documents.

- Paths: `M`, `L`, `H`, `V`, `C`, `S`, `Q`, `T`, `A`, and `Z`, including
  relative commands.
- Shapes: `path`, `rect` (including rounded corners), `circle`, `ellipse`,
  `line`, `polyline`, and `polygon`.
- Structure: `svg`, `g`, `defs`, `symbol`, local `use`, `title`, and `desc`.
- Paint: CSS/SVG colors, `currentColor`, fill and stroke opacity, group opacity,
  line caps, joins, dashes, and linear and radial gradients.
- Styling: presentation attributes, inline declarations, and simple element,
  class, ID, and universal CSS selectors, including `!important`.
- Geometry: SVG affine transform lists and common absolute length units.

Text layout, external resources, images, patterns, filters, masks, markers,
scripts, animation, and full browser CSS are intentionally not implemented.
Nested SVG viewports and `preserveAspectRatio` are not yet modeled; `SetTarget`
uses independent X and Y scaling for compatibility with oksvg.

`IgnoreErrorMode` skips unsupported subtrees, `WarnErrorMode` also reports them,
and `StrictErrorMode` rejects them. Some geometry and style validation happens
when rendering because percentages can depend on the active viewport, so callers
should handle errors from both parsing and drawing.

## Migrating from oksvg

The familiar high-level names are retained:

- `ReadIcon`, `ReadIconStream`, and `ReadReplacingCurrentColor`;
- `ErrorMode` and its ignore, warn, and strict constants;
- `SvgIcon.ViewBox`, `Titles`, and `Descriptions`; and
- `SvgIcon.SetTarget`.

`RenderRegion` and `DrawRegion` are coolsvg extensions for bounded-memory
partial rasterization. `RenderCMYK` and `RenderCMYKRegion` add configurable
post-converted or CMYK-native separations. The precision render options,
`CMYK64`, and streaming TIFF writer are coolsvg extensions as well.

Rendering writes directly to an `image/draw.Image`:

```go
import oksvg "github.com/excitoon/coolsvg-go"

icon, err := oksvg.ReadIconStream(source)
if err != nil {
	return err
}
icon.SetTarget(0, 0, float64(width), float64(height))
if err := icon.Draw(destination, 1); err != nil {
	return err
}
```

No `rasterx.Scanner` or `rasterx.Dasher` is needed. Code that directly modifies
oksvg's exported renderer internals is not source-compatible; those internals
are deliberately not reproduced.

## License

[MIT](LICENSE.md). Prebuilt command archives also include the notices in
[THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
