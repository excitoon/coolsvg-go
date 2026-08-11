# coolsvg-go

Pure-Go SVG rasterization with scene-tree-correct clipping.

`coolsvg-go` is an independent, MIT-licensed reimplementation of the useful
high-level API of [`oksvg`](https://github.com/srwiley/oksvg). It keeps groups,
definitions, and references intact through rendering, so a clip path is applied
to the element or group that owns it instead of being lost when paths are
flattened.

The renderer uses Go's `image` interfaces, requires no CGO or native libraries,
and has no dependency on `rasterx`.

## Install

```sh
go get github.com/excitoon/coolsvg-go
```

The module supports Go 1.22 and newer.

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

## CLI

Install the PNG rasterizer:

```sh
go install github.com/excitoon/coolsvg-go/cmd/coolsvg@latest
```

Render a file, or use `-` for standard input:

```sh
coolsvg -width 512 -height 512 -o icon.png icon.svg
```

The CLI uses compatibility mode by default and reports unsupported elements.
Pass `-strict` to reject them.

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

[MIT](LICENSE.md)
