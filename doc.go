// Package coolsvg parses and rasterizes SVG images using pure Go.
//
// Unlike parsers that flatten an SVG into an unordered list of painted paths,
// coolsvg retains the scene tree. That is necessary to apply clip paths at the
// correct group boundary and to compose nested clips, transforms, and opacity.
package coolsvg
