package coolsvg

import (
	"fmt"
	"image"
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
	if dst == nil || nilInterface(dst) {
		return fmt.Errorf("svg: destination is nil")
	}
	if math.IsNaN(opacity) || math.IsInf(opacity, 0) {
		return fmt.Errorf("svg: opacity must be finite")
	}
	opacity = math.Max(0, math.Min(1, opacity))
	if opacity == 0 || dst.Bounds().Empty() {
		return nil
	}
	return s.DrawRegionWithOptions(dst, dst.Bounds(), opacity, RenderOptions{})
}

// DrawRegion draws a region of the virtual destination into dst. region uses
// the same global destination coordinates as SetTarget; region.Min is mapped
// to dst.Bounds().Min. The two rectangles must have the same dimensions.
// Opacity is clamped to [0, 1].
//
// DrawRegion is useful for rendering tiles of a large target without
// allocating an image for the full target.
func (s *SvgIcon) DrawRegion(dst stddraw.Image, region image.Rectangle, opacity float64) error {
	if dst == nil || nilInterface(dst) {
		return fmt.Errorf("svg: destination is nil")
	}
	destinationBounds := dst.Bounds()
	if !rectanglesHaveSameSize(destinationBounds, region) {
		return fmt.Errorf("svg: destination bounds %v and region %v must have the same dimensions", destinationBounds, region)
	}
	return s.DrawRegionWithOptions(dst, region, opacity, RenderOptions{})
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
	icon          *SvgIcon
	bounds        image.Rectangle
	useStack      map[*node]bool
	budget        int
	flatten       flattenBudget
	surfaces      precisionSurfaceBudget
	partitionWork precisionPartitionWorkBudget
	auxiliary     precisionAuxiliaryBudget
	coveragePool  []*coverageMask
	clipCache     map[clipMaskCacheKey]*sampleMask
	fillMaskCache map[fillMaskCacheKey]*sampleMask
	strokeCache   map[strokeMaskCacheKey][]strokeMaskCacheEntry
	geometryCache map[geometryCacheKey]geometryCacheValue
	flattenCache  map[flattenCacheKey]flattenCacheValue
}

type clipMaskCacheKey struct {
	target    *node
	reference string
	ctm       Matrix
	viewport  ViewBox
	display   bool
}

type fillMaskCacheKey struct {
	path   *Path
	matrix Matrix
	rule   fillRule
}

type geometryCacheKey struct {
	node     *node
	viewport ViewBox
}

type geometryCacheValue struct {
	path *Path
	err  error
}

type flattenCacheKey struct {
	path      *Path
	matrix    Matrix
	tolerance float64
}

type flattenCacheValue struct {
	contours []contour
	err      error
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

func (r *renderer) newSampleMask() (*sampleMask, error) {
	mask := newBudgetedSampleMask(r.bounds, &r.auxiliary)
	if mask.err != nil {
		return nil, mask.err
	}
	return mask, nil
}

func (r *renderer) geometry(n *node, viewport ViewBox) (*Path, error) {
	key := geometryCacheKey{node: n, viewport: viewport}
	if cached, ok := r.geometryCache[key]; ok {
		return cached.path, cached.err
	}
	path, err := n.geometry(viewport)
	if r.geometryCache == nil {
		r.geometryCache = make(map[geometryCacheKey]geometryCacheValue)
	}
	r.geometryCache[key] = geometryCacheValue{path: path, err: err}
	return path, err
}

func (r *renderer) flattenPath(path *Path, matrix Matrix, tolerance float64) ([]contour, error) {
	key := flattenCacheKey{path: path, matrix: matrix, tolerance: tolerance}
	if cached, ok := r.flattenCache[key]; ok {
		return cached.contours, cached.err
	}
	contours, err := path.flattenWithBudget(matrix, tolerance, &r.flatten)
	if r.flattenCache == nil {
		r.flattenCache = make(map[flattenCacheKey]flattenCacheValue)
	}
	r.flattenCache[key] = flattenCacheValue{contours: contours, err: err}
	return contours, err
}

func (r *renderer) fillSampleMask(path *Path, matrix Matrix, contours []contour, rule fillRule) (*sampleMask, error) {
	key := fillMaskCacheKey{path: path, matrix: matrix, rule: rule}
	if cached := r.fillMaskCache[key]; cached != nil {
		return cached, nil
	}
	maskBounds := pixelBoundsForContours(r.bounds, contours)
	mask, err := rasterizeClipContours(maskBounds, contours, rule, &r.flatten, &r.auxiliary)
	if err != nil {
		return nil, err
	}
	if r.fillMaskCache == nil {
		r.fillMaskCache = make(map[fillMaskCacheKey]*sampleMask)
	}
	r.fillMaskCache[key] = mask
	return mask, nil
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
	key := clipMaskCacheKey{target: target, reference: style.clipRef, ctm: ctm, viewport: viewport, display: style.display}
	if cached := r.clipCache[key]; cached != nil {
		return cached, true, nil
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
			mask, err := r.newSampleMask()
			if err == nil {
				if r.clipCache == nil {
					r.clipCache = make(map[clipMaskCacheKey]*sampleMask)
				}
				r.clipCache[key] = mask
			}
			return mask, true, err
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
	mask, err := r.newSampleMask()
	if err != nil {
		return nil, false, err
	}
	for _, child := range clip.children {
		childMask, err := r.renderClipNode(child, clipStyle, base, clipViewport, visiting, make(map[*node]bool))
		if err != nil {
			return nil, false, err
		}
		unionSamples(mask, childMask)
		if mask.err != nil {
			return nil, false, mask.err
		}
	}
	if clipStyle.hasClip {
		nested, valid, err := r.clipMask(clip, clipStyle, base, clipViewport, visiting)
		if err != nil {
			return nil, false, err
		}
		if valid {
			intersectSamples(mask, nested)
			if mask.err != nil {
				return nil, false, mask.err
			}
		}
	}
	if r.clipCache == nil {
		r.clipCache = make(map[clipMaskCacheKey]*sampleMask)
	}
	r.clipCache[key] = mask
	return mask, true, nil
}

func (r *renderer) renderClipNode(n *node, parentStyle computedStyle, parentCTM Matrix, viewport ViewBox, visitingClips, visitingUses map[*node]bool) (*sampleMask, error) {
	if err := r.step(); err != nil {
		return nil, err
	}
	if n.unsupported {
		return r.newSampleMask()
	}
	style, err := r.icon.computeStyle(n, parentStyle)
	if err != nil {
		return nil, err
	}
	mask, err := r.newSampleMask()
	if err != nil {
		return nil, err
	}
	if !style.display {
		return mask, nil
	}
	ctm := parentCTM.Mul(n.transform)
	switch n.tag {
	case "path", "rect", "circle", "ellipse", "polygon", "polyline":
		if style.visible {
			path, err := r.geometry(n, viewport)
			if err != nil {
				return nil, err
			}
			if path != nil {
				contours, err := r.flattenPath(path, ctm, 0.2)
				if err != nil {
					return nil, err
				}
				mask, err = rasterizeClipContours(r.bounds, contours, style.clipRule, &r.flatten, &r.auxiliary)
				if err != nil {
					return nil, err
				}
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
			if mask.err != nil {
				return nil, mask.err
			}
		}
	}
	if style.hasClip {
		nested, valid, err := r.clipMask(n, style, ctm, viewport, visitingClips)
		if err != nil {
			return nil, err
		}
		if valid {
			intersectSamples(mask, nested)
			if mask.err != nil {
				return nil, mask.err
			}
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
		path, err := r.geometry(n, viewport)
		if err != nil || path == nil {
			return box, err
		}
		contours, err := r.flattenPath(path, ctm, 0.1)
		if err != nil {
			return box, err
		}
		for _, contour := range contours {
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
