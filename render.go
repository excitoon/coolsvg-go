package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"math"
	"strings"
)

// Render draws the icon into dst at full opacity.
func (s *SvgIcon) Render(dst stddraw.Image) error {
	return s.Draw(dst, 1)
}

// RenderRegion draws a region of the virtual destination into dst at full
// opacity. region uses the same global destination coordinates as SetTarget;
// region.Min is mapped to dst.Bounds().Min. The two rectangles must have the
// same dimensions.
func (s *SvgIcon) RenderRegion(dst stddraw.Image, region image.Rectangle) error {
	return s.DrawRegion(dst, region, 1)
}

// Draw draws the icon into dst. SetTarget may be used first to map the SVG
// viewBox to a destination rectangle. Opacity is clamped to [0, 1].
func (s *SvgIcon) Draw(dst stddraw.Image, opacity float64) error {
	if s == nil || s.root == nil {
		return fmt.Errorf("svg: cannot draw a nil icon")
	}
	if dst == nil {
		return fmt.Errorf("svg: destination is nil")
	}
	if math.IsNaN(opacity) || math.IsInf(opacity, 0) {
		return fmt.Errorf("svg: opacity must be finite")
	}
	opacity = math.Max(0, math.Min(1, opacity))
	if opacity == 0 || dst.Bounds().Empty() {
		return nil
	}
	r := renderer{
		icon:     s,
		bounds:   dst.Bounds(),
		useStack: make(map[*node]bool),
		budget:   maxRenderOperations,
	}
	base := defaultComputedStyle()
	return r.renderNode(dst, s.root, base, s.target, s.ViewBox, opacity)
}

// DrawRegion draws a region of the virtual destination into dst. region uses
// the same global destination coordinates as SetTarget; region.Min is mapped
// to dst.Bounds().Min. The two rectangles must have the same dimensions.
// Opacity is clamped to [0, 1].
//
// DrawRegion is useful for rendering tiles of a large target without
// allocating an image for the full target.
func (s *SvgIcon) DrawRegion(dst stddraw.Image, region image.Rectangle, opacity float64) error {
	if dst == nil {
		return fmt.Errorf("svg: destination is nil")
	}
	destinationBounds := dst.Bounds()
	if !rectanglesHaveSameSize(destinationBounds, region) {
		return fmt.Errorf("svg: destination bounds %v and region %v must have the same dimensions", destinationBounds, region)
	}
	return s.Draw(translatedDrawImage{Image: dst, bounds: region, destinationMin: destinationBounds.Min}, opacity)
}

// translatedDrawImage exposes a local image through global coordinates. Using
// relative coordinates in translatedPoint avoids overflowing when the two
// rectangle minima are far apart.
type translatedDrawImage struct {
	stddraw.Image
	bounds         image.Rectangle
	destinationMin image.Point
}

func (i translatedDrawImage) Bounds() image.Rectangle { return i.bounds }

func (i translatedDrawImage) At(x, y int) color.Color {
	p := i.translatedPoint(x, y)
	return i.Image.At(p.X, p.Y)
}

func (i translatedDrawImage) Set(x, y int, value color.Color) {
	p := i.translatedPoint(x, y)
	i.Image.Set(p.X, p.Y, value)
}

func (i translatedDrawImage) RGBA64At(x, y int) color.RGBA64 {
	p := i.translatedPoint(x, y)
	if source, ok := i.Image.(image.RGBA64Image); ok {
		return source.RGBA64At(p.X, p.Y)
	}
	return color.RGBA64Model.Convert(i.Image.At(p.X, p.Y)).(color.RGBA64)
}

func (i translatedDrawImage) SetRGBA64(x, y int, value color.RGBA64) {
	p := i.translatedPoint(x, y)
	if destination, ok := i.Image.(stddraw.RGBA64Image); ok {
		destination.SetRGBA64(p.X, p.Y, value)
		return
	}
	i.Image.Set(p.X, p.Y, value)
}

func (i translatedDrawImage) translatedPoint(x, y int) image.Point {
	return image.Pt(
		i.destinationMin.X+(x-i.bounds.Min.X),
		i.destinationMin.Y+(y-i.bounds.Min.Y),
	)
}

func rectanglesHaveSameSize(a, b image.Rectangle) bool {
	if a.Min.X > a.Max.X || a.Min.Y > a.Max.Y || b.Min.X > b.Max.X || b.Min.Y > b.Max.Y {
		return false
	}
	aWidth, aHeight := uint(a.Max.X)-uint(a.Min.X), uint(a.Max.Y)-uint(a.Min.Y)
	bWidth, bHeight := uint(b.Max.X)-uint(b.Min.X), uint(b.Max.Y)-uint(b.Min.Y)
	maxInt := uint(^uint(0) >> 1)
	return aWidth <= maxInt && aHeight <= maxInt && bWidth <= maxInt && bHeight <= maxInt &&
		aWidth == bWidth && aHeight == bHeight
}

type renderer struct {
	icon     *SvgIcon
	bounds   image.Rectangle
	useStack map[*node]bool
	budget   int
}

const maxRenderOperations = 1_000_000
const maxReferenceDepth = 256

func (r *renderer) step() error {
	r.budget--
	if r.budget < 0 {
		return fmt.Errorf("svg: render operation limit exceeded")
	}
	return nil
}

func (r *renderer) renderNode(dst stddraw.Image, n *node, parentStyle computedStyle, parentCTM Matrix, viewport ViewBox, inheritedOpacity float64) error {
	if err := r.step(); err != nil {
		return err
	}
	style, err := r.icon.computeStyle(n, parentStyle)
	if err != nil {
		return fmt.Errorf("svg: <%s>: %w", n.tag, err)
	}
	if !style.display {
		return nil
	}
	ctm := parentCTM.Mul(n.transform)
	clipSamples, hasClip, err := r.clipMask(n, style, ctm, viewport, make(map[*node]bool))
	if err != nil {
		return err
	}
	groupOpacity := inheritedOpacity * style.opacity
	if groupOpacity <= 0 {
		return nil
	}
	if hasClip || groupOpacity < 1 {
		layer := image.NewNRGBA(r.bounds)
		if err := r.renderContent(layer, n, style, ctm, viewport); err != nil {
			return err
		}
		var clip *image.Alpha
		if hasClip {
			clip = clipSamples.alpha()
		} else {
			clip = opaqueMask(r.bounds)
		}
		scaleMask(clip, groupOpacity)
		stddraw.DrawMask(dst, r.bounds, layer, r.bounds.Min, clip, r.bounds.Min, stddraw.Over)
		return nil
	}
	return r.renderContent(dst, n, style, ctm, viewport)
}

func (r *renderer) renderContent(dst stddraw.Image, n *node, style computedStyle, ctm Matrix, viewport ViewBox) error {
	if n.unsupported {
		return nil
	}
	switch n.tag {
	case "defs", "clippath", "lineargradient", "radialgradient", "stop", "title", "desc", "style", "metadata":
		return nil
	case "use":
		ref, ok := referenceID(n.href())
		if !ok {
			return nil
		}
		target := r.icon.ids[ref]
		if target == nil || r.useStack[target] {
			if r.useStack[target] {
				return fmt.Errorf("svg: cyclic <use> reference to %q", ref)
			}
			return nil
		}
		if len(r.useStack) >= maxReferenceDepth {
			return fmt.Errorf("svg: <use> reference depth exceeds %d", maxReferenceDepth)
		}
		x, err := parseLength(n.attr("x"), viewport.W)
		if err != nil {
			return err
		}
		y, err := parseLength(n.attr("y"), viewport.H)
		if err != nil {
			return err
		}
		r.useStack[target] = true
		err = r.renderNode(dst, target, style, ctm.Mul(translate(x, y)), viewport, 1)
		delete(r.useStack, target)
		return err
	case "symbol":
		if !r.useStack[n] {
			return nil
		}
		for _, child := range n.children {
			if err := r.renderNode(dst, child, style, ctm, viewport, 1); err != nil {
				return err
			}
		}
		return nil
	case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon":
		if !style.visible {
			return nil
		}
		path, err := n.geometry(viewport)
		if err != nil || path == nil {
			return err
		}
		return r.paintGeometry(dst, path, style, ctm)
	default:
		for _, child := range n.children {
			if err := r.renderNode(dst, child, style, ctm, viewport, 1); err != nil {
				return err
			}
		}
		return nil
	}
}

func (r *renderer) paintGeometry(dst stddraw.Image, path *Path, style computedStyle, ctm Matrix) error {
	contours := path.flatten(ctm, 0.2)
	localBounds := boundsOfContours(path.flatten(identityMatrix(), 0.1))
	if style.fill.kind != paintNone {
		mask := rasterizeContours(r.bounds, contours, style.fillRule)
		paint, err := r.paintSource(style.fill, style.color, style.fillOpacity, ctm, localBounds)
		if err != nil {
			return err
		}
		if paint != nil {
			stddraw.DrawMask(dst, r.bounds, paint, r.bounds.Min, mask, r.bounds.Min, stddraw.Over)
		}
	}
	if style.stroke.kind != paintNone && style.strokeWidth > 0 {
		mask := rasterizeStroke(r.bounds, path, ctm, style)
		paint, err := r.paintSource(style.stroke, style.color, style.strokeOpacity, ctm, localBounds)
		if err != nil {
			return err
		}
		if paint != nil {
			stddraw.DrawMask(dst, r.bounds, paint, r.bounds.Min, mask, r.bounds.Min, stddraw.Over)
		}
	}
	return nil
}

func (r *renderer) paintSource(spec paintSpec, current color.NRGBA, opacity float64, ctm Matrix, bounds floatBounds) (image.Image, error) {
	var value color.NRGBA
	switch spec.kind {
	case paintNone:
		return nil, nil
	case paintColor:
		value = spec.color
	case paintCurrentColor:
		value = current
		if r.icon.currentColor != nil {
			value = *r.icon.currentColor
		}
	case paintReference:
		// Paint servers are resolved in gradients.go. Unknown references make
		// the paint transparent, as if no paint server were available.
		return r.gradientSource(spec.ref, opacity, ctm, bounds)
	default:
		return nil, nil
	}
	value.A = uint8(math.Round(float64(value.A) * math.Max(0, math.Min(1, opacity))))
	return &boundedUniform{value: value, rgba64: nrgbaToRGBA64(value), bounds: r.bounds}, nil
}

type boundedUniform struct {
	value  color.NRGBA
	rgba64 color.RGBA64
	bounds image.Rectangle
}

func (u *boundedUniform) ColorModel() color.Model        { return color.NRGBAModel }
func (u *boundedUniform) Bounds() image.Rectangle        { return u.bounds }
func (u *boundedUniform) At(_, _ int) color.Color        { return u.value }
func (u *boundedUniform) RGBA64At(_, _ int) color.RGBA64 { return u.rgba64 }

func nrgbaToRGBA64(value color.NRGBA) color.RGBA64 {
	alpha := uint32(value.A)
	return color.RGBA64{
		R: uint16(uint32(value.R) * 0x101 * alpha / 0xff),
		G: uint16(uint32(value.G) * 0x101 * alpha / 0xff),
		B: uint16(uint32(value.B) * 0x101 * alpha / 0xff),
		A: uint16(alpha * 0x101),
	}
}

func opaqueMask(bounds image.Rectangle) *image.Alpha {
	mask := image.NewAlpha(bounds)
	for i := range mask.Pix {
		mask.Pix[i] = 255
	}
	return mask
}

func referenceID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "#") && len(value) > 1 {
		return value[1:], true
	}
	return "", false
}

func (r *renderer) clipMask(target *node, style computedStyle, ctm Matrix, viewport ViewBox, visiting map[*node]bool) (*sampleMask, bool, error) {
	if !style.hasClip {
		return nil, false, nil
	}
	clip := r.icon.ids[style.clipRef]
	if clip == nil || clip.tag != "clippath" {
		return nil, false, nil
	}
	if visiting[clip] {
		return nil, false, fmt.Errorf("svg: cyclic clip-path reference to %q", style.clipRef)
	}
	if len(visiting) >= maxReferenceDepth {
		return nil, false, fmt.Errorf("svg: clip-path reference depth exceeds %d", maxReferenceDepth)
	}
	visiting[clip] = true
	defer delete(visiting, clip)

	base := ctm
	clipViewport := viewport
	if strings.EqualFold(clip.attr("clippathunits"), "objectBoundingBox") {
		box, ok, err := r.objectBoundingBox(target, style, viewport, make(map[*node]bool))
		if err != nil {
			return nil, false, err
		}
		if !ok || box.width() == 0 || box.height() == 0 {
			return newSampleMask(r.bounds), true, nil
		}
		base = base.Mul(translate(box.min.X, box.min.Y)).Mul(scale(box.width(), box.height()))
		clipViewport = ViewBox{W: 1, H: 1}
	}
	base = base.Mul(clip.transform)
	parentStyle, err := r.definitionParentStyle(clip)
	if err != nil {
		return nil, false, err
	}
	clipStyle, err := r.icon.computeStyle(clip, parentStyle)
	if err != nil {
		return nil, false, err
	}
	mask := newSampleMask(r.bounds)
	for _, child := range clip.children {
		childMask, err := r.renderClipNode(child, clipStyle, base, clipViewport, visiting, make(map[*node]bool))
		if err != nil {
			return nil, false, err
		}
		unionSamples(mask, childMask)
	}
	if clipStyle.hasClip {
		nested, valid, err := r.clipMask(clip, clipStyle, base, clipViewport, visiting)
		if err != nil {
			return nil, false, err
		}
		if valid {
			intersectSamples(mask, nested)
		}
	}
	return mask, true, nil
}

func (r *renderer) renderClipNode(n *node, parentStyle computedStyle, parentCTM Matrix, viewport ViewBox, visitingClips, visitingUses map[*node]bool) (*sampleMask, error) {
	if err := r.step(); err != nil {
		return nil, err
	}
	if n.unsupported {
		return newSampleMask(r.bounds), nil
	}
	style, err := r.icon.computeStyle(n, parentStyle)
	if err != nil {
		return nil, err
	}
	mask := newSampleMask(r.bounds)
	if !style.display {
		return mask, nil
	}
	ctm := parentCTM.Mul(n.transform)
	switch n.tag {
	case "path", "rect", "circle", "ellipse", "polygon", "polyline":
		if style.visible {
			path, err := n.geometry(viewport)
			if err != nil {
				return nil, err
			}
			if path != nil {
				mask = rasterizeClipContours(r.bounds, path.flatten(ctm, 0.2), style.clipRule)
			}
		}
	case "use":
		if ref, ok := referenceID(n.href()); ok {
			target := r.icon.ids[ref]
			if target != nil {
				if visitingUses[target] {
					return nil, fmt.Errorf("svg: cyclic <use> reference to %q", ref)
				}
				if len(visitingUses) >= maxReferenceDepth {
					return nil, fmt.Errorf("svg: clip <use> reference depth exceeds %d", maxReferenceDepth)
				}
				x, err := parseLength(n.attr("x"), viewport.W)
				if err != nil {
					return nil, err
				}
				y, err := parseLength(n.attr("y"), viewport.H)
				if err != nil {
					return nil, err
				}
				visitingUses[target] = true
				mask, err = r.renderClipNode(target, style, ctm.Mul(translate(x, y)), viewport, visitingClips, visitingUses)
				delete(visitingUses, target)
				if err != nil {
					return nil, err
				}
			}
		}
	default:
		for _, child := range n.children {
			childMask, err := r.renderClipNode(child, style, ctm, viewport, visitingClips, visitingUses)
			if err != nil {
				return nil, err
			}
			unionSamples(mask, childMask)
		}
	}
	if style.hasClip {
		nested, valid, err := r.clipMask(n, style, ctm, viewport, visitingClips)
		if err != nil {
			return nil, err
		}
		if valid {
			intersectSamples(mask, nested)
		}
	}
	return mask, nil
}

func (r *renderer) definitionParentStyle(n *node) (computedStyle, error) {
	var chain []*node
	for parent := n.parent; parent != nil; parent = parent.parent {
		chain = append(chain, parent)
	}
	style := defaultComputedStyle()
	for i := len(chain) - 1; i >= 0; i-- {
		var err error
		style, err = r.icon.computeStyle(chain[i], style)
		if err != nil {
			return style, err
		}
	}
	return style, nil
}

type floatBounds struct {
	min   Point
	max   Point
	valid bool
}

func (b *floatBounds) add(p Point) {
	if !b.valid {
		b.min, b.max, b.valid = p, p, true
		return
	}
	b.min.X, b.min.Y = math.Min(b.min.X, p.X), math.Min(b.min.Y, p.Y)
	b.max.X, b.max.Y = math.Max(b.max.X, p.X), math.Max(b.max.Y, p.Y)
}

func (b *floatBounds) merge(other floatBounds) {
	if !other.valid {
		return
	}
	b.add(other.min)
	b.add(other.max)
}

func (b floatBounds) width() float64  { return b.max.X - b.min.X }
func (b floatBounds) height() float64 { return b.max.Y - b.min.Y }

func boundsOfContours(contours []contour) floatBounds {
	var bounds floatBounds
	for _, contour := range contours {
		for _, point := range contour.points {
			bounds.add(point)
		}
	}
	return bounds
}

func (r *renderer) objectBoundingBox(target *node, style computedStyle, viewport ViewBox, visiting map[*node]bool) (floatBounds, bool, error) {
	box, err := r.boundingContent(target, style, identityMatrix(), viewport, visiting)
	return box, box.valid, err
}

func (r *renderer) boundingNode(n *node, parentStyle computedStyle, parent Matrix, viewport ViewBox, visiting map[*node]bool) (floatBounds, error) {
	if err := r.step(); err != nil {
		return floatBounds{}, err
	}
	if n.unsupported {
		return floatBounds{}, nil
	}
	style, err := r.icon.computeStyle(n, parentStyle)
	if err != nil {
		return floatBounds{}, err
	}
	if !style.display {
		return floatBounds{}, nil
	}
	return r.boundingContent(n, style, parent.Mul(n.transform), viewport, visiting)
}

func (r *renderer) boundingContent(n *node, style computedStyle, ctm Matrix, viewport ViewBox, visiting map[*node]bool) (floatBounds, error) {
	var box floatBounds
	switch n.tag {
	case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon":
		path, err := n.geometry(viewport)
		if err != nil || path == nil {
			return box, err
		}
		for _, contour := range path.flatten(ctm, 0.1) {
			for _, point := range contour.points {
				box.add(point)
			}
		}
	case "use":
		ref, ok := referenceID(n.href())
		if !ok {
			return box, nil
		}
		target := r.icon.ids[ref]
		if target == nil || visiting[target] {
			return box, nil
		}
		if len(visiting) >= maxReferenceDepth {
			return box, fmt.Errorf("svg: bounding-box reference depth exceeds %d", maxReferenceDepth)
		}
		x, err := parseLength(n.attr("x"), viewport.W)
		if err != nil {
			return box, err
		}
		y, err := parseLength(n.attr("y"), viewport.H)
		if err != nil {
			return box, err
		}
		visiting[target] = true
		box, err = r.boundingNode(target, style, ctm.Mul(translate(x, y)), viewport, visiting)
		delete(visiting, target)
		return box, err
	default:
		for _, child := range n.children {
			if child.tag == "defs" || child.tag == "clippath" || child.tag == "lineargradient" || child.tag == "radialgradient" {
				continue
			}
			childBox, err := r.boundingNode(child, style, ctm, viewport, visiting)
			if err != nil {
				return box, err
			}
			box.merge(childBox)
		}
	}
	return box, nil
}
