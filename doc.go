// Package coolsvg parses and rasterizes SVG images using pure Go.
//
// Unlike parsers that flatten an SVG into an unordered list of painted paths,
// coolsvg retains the scene tree. That is necessary to apply clip paths at the
// correct group boundary and to compose nested clips, transforms, and opacity.
// RenderRegion and DrawRegion rasterize bounded tiles in full-target
// coordinates, allowing very large virtual canvases without a full-size image
// allocation.
//
// RenderWithOptions retains floating-point working precision until 8- or
// 16-bit destination quantization and supports nearest, globally anchored
// Bayer, and stateful Floyd-Steinberg quantization. RenderCMYK, RenderCMYK64,
// and their region forms provide pluggable post-converted and native CMYK
// separation paths.
//
// EncodeSVGToTIFF and SvgIcon.EncodeTIFF render directly into bounded strips
// and write RGB, RGBA, or CMYK classic TIFF/BigTIFF without materializing a
// complete large raster.
package coolsvg
