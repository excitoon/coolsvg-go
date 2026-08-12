package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

type rgbaFloat struct {
	r float64
	g float64
	b float64
	a float64
}

type rgbaFloatSurface struct {
	bounds image.Rectangle
	stride int
	pix    []float64
}

func newRGBAFloatSurface(bounds image.Rectangle) (*rgbaFloatSurface, error) {
	length, stride, ok := checkedImageSize(bounds, 4)
	if !ok || length > precisionMaxSurfaceBytes/8 {
		return nil, fmt.Errorf("svg: floating-point RGBA surface bounds %v are too large", bounds)
	}
	return &rgbaFloatSurface{bounds: bounds, stride: stride, pix: make([]float64, length)}, nil
}

func (s *rgbaFloatSurface) offset(x, y int) int {
	return (y-s.bounds.Min.Y)*s.stride + (x-s.bounds.Min.X)*4
}

func (s *rgbaFloatSurface) rgbaAt(x, y int) rgbaFloat {
	offset := s.offset(x, y)
	return rgbaFloat{r: s.pix[offset], g: s.pix[offset+1], b: s.pix[offset+2], a: s.pix[offset+3]}
}

func (s *rgbaFloatSurface) set(x, y int, value rgbaFloat) {
	offset := s.offset(x, y)
	s.pix[offset], s.pix[offset+1], s.pix[offset+2], s.pix[offset+3] = value.r, value.g, value.b, value.a
}

func (s *rgbaFloatSurface) blend(offset int, source rgbaFloat, coverage float64) {
	sourceAlpha := clampUnit(source.a * coverage)
	if sourceAlpha <= 0 {
		return
	}
	if sourceAlpha >= 1 {
		s.pix[offset], s.pix[offset+1], s.pix[offset+2], s.pix[offset+3] = source.r, source.g, source.b, 1
		return
	}
	destinationAlpha := s.pix[offset+3]
	if destinationAlpha <= 0 {
		s.pix[offset], s.pix[offset+1], s.pix[offset+2], s.pix[offset+3] = source.r, source.g, source.b, sourceAlpha
		return
	}
	inverse := 1 - sourceAlpha
	outputAlpha := sourceAlpha + destinationAlpha*inverse
	sourceChannels := [3]float64{source.r, source.g, source.b}
	for channel, sourceChannel := range sourceChannels {
		s.pix[offset+channel] = (sourceChannel*sourceAlpha + s.pix[offset+channel]*destinationAlpha*inverse) / outputAlpha
	}
	s.pix[offset+3] = outputAlpha
}

func clampUnit(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return math.Max(0, math.Min(1, value))
}

type rgbaFloatPaint interface {
	rgbaAt(x, y int) rgbaFloat
}

type rgbaFloatUniform struct{ value rgbaFloat }

func (u rgbaFloatUniform) rgbaAt(_, _ int) rgbaFloat { return u.value }

func (s *rgbaFloatSurface) drawMask(source rgbaFloatPaint, mask *coverageMask) {
	if source == nil || mask == nil {
		return
	}
	bounds := s.bounds.Intersect(mask.bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		destinationOffset := s.offset(bounds.Min.X, y)
		maskOffset := mask.offset(bounds.Min.X, y)
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if coverage := mask.values[maskOffset]; coverage > 0 {
				s.blend(destinationOffset, source.rgbaAt(x, y), coverage)
			}
			destinationOffset += 4
			maskOffset++
		}
	}
}

type rgbaFloatRenderer struct {
	renderer
	surfacePool []*rgbaFloatSurface
}

func newRGBAFloatRenderer(icon *SvgIcon, bounds image.Rectangle) rgbaFloatRenderer {
	return rgbaFloatRenderer{renderer: renderer{
		icon: icon, bounds: bounds, useStack: make(map[*node]bool), budget: maxRenderOperations,
		flatten: newFlattenBudget(), surfaces: newPrecisionSurfaceBudget(),
	}}
}

func (r *rgbaFloatRenderer) newLayerSurface() (*rgbaFloatSurface, func(), error) {
	if count := len(r.surfacePool); count > 0 {
		surface := r.surfacePool[count-1]
		r.surfacePool = r.surfacePool[:count-1]
		clear(surface.pix)
		return surface, func() { r.surfacePool = append(r.surfacePool, surface) }, nil
	}
	elements, _, ok := checkedImageSize(r.bounds, 4)
	if !ok || elements > precisionMaxSurfaceBytes/8 {
		_, err := newRGBAFloatSurface(r.bounds)
		return nil, nil, err
	}
	bytes, err := r.surfaces.reserve(elements)
	if err != nil {
		return nil, nil, err
	}
	surface, err := newRGBAFloatSurface(r.bounds)
	if err != nil {
		r.surfaces.release(bytes)
		return nil, nil, err
	}
	return surface, func() { r.surfacePool = append(r.surfacePool, surface) }, nil
}

func (r *rgbaFloatRenderer) renderNode(dst *rgbaFloatSurface, n *node, parentStyle computedStyle, parentCTM Matrix, viewport ViewBox, inheritedOpacity float64) error {
	layer, releaseLayer, err := r.newLayerSurface()
	if err != nil {
		return err
	}
	defer releaseLayer()
	scratch, releaseScratch, err := r.newLayerSurface()
	if err != nil {
		return err
	}
	defer releaseScratch()

	for sy := 0; sy < antialiasSamples; sy++ {
		domain := newBudgetedSampleMask(r.bounds, &r.auxiliary)
		if domain.err != nil {
			return domain.err
		}
		for y := r.bounds.Min.Y; y < r.bounds.Max.Y; y++ {
			addSampleInterval(domain, y, sy, float64(r.bounds.Min.X), float64(r.bounds.Max.X))
		}
		if domain.err != nil {
			return domain.err
		}
		render := func(target *rgbaFloatSurface, active *sampleMask, cell *precisionSampleCell) error {
			return r.renderNodeClipped(target, n, parentStyle, parentCTM, viewport, inheritedOpacity, active, cell)
		}
		if err := r.renderRGBAFloatDomain(layer, scratch, domain, sy, nil, nil, render); err != nil {
			return err
		}
	}
	compositeRGBAFloatLayer(dst, layer, 1)
	return nil
}

type rgbaFloatDomainRenderer func(*rgbaFloatSurface, *sampleMask, *precisionSampleCell) error

func (r *rgbaFloatRenderer) renderRGBAFloatDomain(dst, scratch *rgbaFloatSurface, domain *sampleMask, sy int, parentSample *precisionSampleCell, parentPartitions *precisionPartitions, render rgbaFloatDomainRenderer) error {
	partitions := newPrecisionPartitions(r.bounds, parentPartitions, &r.partitionWork, &r.auxiliary)
	if partitions.err != nil {
		return partitions.err
	}
	clear(scratch.pix)
	discovery := precisionSampleCell{y: sy, domain: domain, partitions: partitions}
	if err := render(scratch, domain, &discovery); err != nil {
		return err
	}
	if err := partitions.finalize(); err != nil {
		return err
	}

	atomic := newBudgetedSampleMask(domain.bounds, &r.auxiliary)
	if atomic.err != nil {
		return atomic.err
	}
	if !selectPrecisionPartitionSegment(atomic, domain, sy, 1, partitions) {
		if atomic.err != nil {
			return atomic.err
		}
		accumulateRGBAFloatSample(dst, scratch, domain, sy, parentSample)
		return nil
	}
	stable := newBudgetedSampleMask(domain.bounds, &r.auxiliary)
	if stable.err != nil {
		return stable.err
	}
	if selectPrecisionUnpartitionedSegment(stable, domain, sy, partitions) {
		if stable.err != nil {
			return stable.err
		}
		accumulateRGBAFloatSample(dst, scratch, stable, sy, parentSample)
	} else if stable.err != nil {
		return stable.err
	}
	for ordinal := 0; ; ordinal++ {
		if !selectPrecisionPartitionSegment(atomic, domain, sy, ordinal, partitions) {
			if atomic.err != nil {
				return atomic.err
			}
			break
		}
		if atomic.err != nil {
			return atomic.err
		}
		clear(scratch.pix)
		cell := precisionSampleCell{y: sy, domain: atomic, atomic: true}
		if err := render(scratch, atomic, &cell); err != nil {
			return err
		}
		accumulateRGBAFloatSample(dst, scratch, atomic, sy, parentSample)
	}
	return nil
}

func (r *rgbaFloatRenderer) renderNodeClipped(dst *rgbaFloatSurface, n *node, parentStyle computedStyle, parentCTM Matrix, viewport ViewBox, inheritedOpacity float64, activeClip *sampleMask, sampleCell *precisionSampleCell) error {
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
	effectiveClip := activeClip
	if hasClip {
		if activeClip != nil {
			clipSamples, err = cloneSampleMask(clipSamples, &r.auxiliary)
			if err != nil {
				return err
			}
			intersectSamples(clipSamples, activeClip)
			if clipSamples.err != nil {
				return clipSamples.err
			}
		}
		effectiveClip = clipSamples
	}
	groupOpacity := inheritedOpacity * style.opacity
	if groupOpacity <= 0 {
		return nil
	}
	if hasClip {
		if sampleCell != nil && sampleCell.atomic {
			layer, releaseLayer, err := r.newLayerSurface()
			if err != nil {
				return err
			}
			defer releaseLayer()
			if err := r.renderContent(layer, n, style, ctm, viewport, effectiveClip, sampleCell); err != nil {
				return err
			}
			compositeRGBAFloatLayer(dst, layer, groupOpacity)
			return nil
		}
		return r.renderSampledClipGroup(dst, n, style, ctm, viewport, groupOpacity, effectiveClip, sampleCell)
	}
	if groupOpacity < 1 {
		layer, releaseLayer, err := r.newLayerSurface()
		if err != nil {
			return err
		}
		defer releaseLayer()
		if err := r.renderContent(layer, n, style, ctm, viewport, effectiveClip, sampleCell); err != nil {
			return err
		}
		compositeRGBAFloatLayer(dst, layer, groupOpacity)
		return nil
	}
	return r.renderContent(dst, n, style, ctm, viewport, effectiveClip, sampleCell)
}

func (r *rgbaFloatRenderer) renderSampledClipGroup(dst *rgbaFloatSurface, n *node, style computedStyle, ctm Matrix, viewport ViewBox, groupOpacity float64, clip *sampleMask, parentSample *precisionSampleCell) error {
	layer, releaseLayer, err := r.newLayerSurface()
	if err != nil {
		return err
	}
	defer releaseLayer()
	interior, boundary, err := splitPrecisionClip(clip)
	if err != nil {
		return err
	}
	if boundary == nil {
		if err := r.renderContent(layer, n, style, ctm, viewport, clip, parentSample); err != nil {
			return err
		}
	} else {
		if interior != nil {
			if err := r.renderContent(layer, n, style, ctm, viewport, interior, parentSample); err != nil {
				return err
			}
		}
		scratch, releaseScratch, err := r.newLayerSurface()
		if err != nil {
			return err
		}
		defer releaseScratch()
		segment := newBudgetedSampleMask(boundary.bounds, &r.auxiliary)
		if segment.err != nil {
			return segment.err
		}
		minimumSample, maximumSample := 0, antialiasSamples
		if parentSample != nil {
			minimumSample, maximumSample = parentSample.y, parentSample.y+1
		}
		for sy := minimumSample; sy < maximumSample; sy++ {
			for ordinal := 0; ; ordinal++ {
				if !selectPrecisionClipSegment(segment, boundary, sy, ordinal) {
					if segment.err != nil {
						return segment.err
					}
					break
				}
				if segment.err != nil {
					return segment.err
				}
				render := func(target *rgbaFloatSurface, active *sampleMask, cell *precisionSampleCell) error {
					if err := r.step(); err != nil {
						return err
					}
					return r.renderContent(target, n, style, ctm, viewport, active, cell)
				}
				var parentPartitions *precisionPartitions
				if parentSample != nil {
					parentPartitions = parentSample.partitions
				}
				if err := r.renderRGBAFloatDomain(layer, scratch, segment, sy, parentSample, parentPartitions, render); err != nil {
					return err
				}
			}
		}
	}
	compositeRGBAFloatLayer(dst, layer, groupOpacity)
	return nil
}

func compositeRGBAFloatLayer(dst, layer *rgbaFloatSurface, opacity float64) {
	opacity = clampUnit(opacity)
	if opacity <= 0 {
		return
	}
	bounds := dst.bounds.Intersect(layer.bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		offset := dst.offset(bounds.Min.X, y)
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			layerOffset := layer.offset(x, y)
			if layer.pix[layerOffset+3] > 0 {
				dst.blend(offset, rgbaFloat{
					r: layer.pix[layerOffset], g: layer.pix[layerOffset+1],
					b: layer.pix[layerOffset+2], a: layer.pix[layerOffset+3],
				}, opacity)
			}
			offset += 4
		}
	}
}

func accumulateRGBAFloatSample(dst, sample *rgbaFloatSurface, segment *sampleMask, sy int, parentSample *precisionSampleCell) {
	for y := dst.bounds.Min.Y; y < dst.bounds.Max.Y; y++ {
		for x := dst.bounds.Min.X; x < dst.bounds.Max.X; x++ {
			segmentWidth := sampleIntervalsCoverage(segment.row(y, sy), float64(x), float64(x+1))
			if segmentWidth <= 0 {
				continue
			}
			weight := segmentWidth / antialiasSamples
			if parentSample != nil && parentSample.domain != nil {
				parentWidth := sampleIntervalsCoverage(parentSample.domain.row(y, sy), float64(x), float64(x+1))
				if parentWidth <= 0 {
					continue
				}
				weight = segmentWidth / parentWidth
			}
			offset := dst.offset(x, y)
			addedAlpha := sample.pix[offset+3] * weight
			if addedAlpha <= 0 {
				continue
			}
			previousAlpha := dst.pix[offset+3]
			combinedAlpha := previousAlpha + addedAlpha
			for channel := 0; channel < 3; channel++ {
				dst.pix[offset+channel] = (dst.pix[offset+channel]*previousAlpha + sample.pix[offset+channel]*addedAlpha) / combinedAlpha
			}
			dst.pix[offset+3] = combinedAlpha
		}
	}
}

func (r *rgbaFloatRenderer) renderContent(dst *rgbaFloatSurface, n *node, style computedStyle, ctm Matrix, viewport ViewBox, activeClip *sampleMask, sampleCell *precisionSampleCell) error {
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
		err = r.renderNodeClipped(dst, target, style, ctm.Mul(translate(x, y)), viewport, 1, activeClip, sampleCell)
		delete(r.useStack, target)
		return err
	case "symbol":
		if !r.useStack[n] {
			return nil
		}
		for _, child := range n.children {
			if err := r.renderNodeClipped(dst, child, style, ctm, viewport, 1, activeClip, sampleCell); err != nil {
				return err
			}
		}
		return nil
	case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon":
		if !style.visible {
			return nil
		}
		path, err := r.geometry(n, viewport)
		if err != nil || path == nil {
			return err
		}
		return r.paintGeometry(dst, path, style, ctm, activeClip, sampleCell)
	default:
		for _, child := range n.children {
			if err := r.renderNodeClipped(dst, child, style, ctm, viewport, 1, activeClip, sampleCell); err != nil {
				return err
			}
		}
		return nil
	}
}

func (r *rgbaFloatRenderer) paintGeometry(dst *rgbaFloatSurface, path *Path, style computedStyle, ctm Matrix, activeClip *sampleMask, sampleCell *precisionSampleCell) error {
	var localBounds floatBounds
	if style.fill.kind == paintReference || style.stroke.kind == paintReference {
		localContours, err := r.flattenPath(path, identityMatrix(), 0.1)
		if err != nil {
			return err
		}
		localBounds = boundsOfContours(localContours)
	}
	if style.fill.kind != paintNone {
		contours, err := r.flattenPath(path, ctm, 0.2)
		if err != nil {
			return err
		}
		geometryMask, err := r.fillSampleMask(path, ctm, contours, style.fillRule)
		if err != nil {
			return err
		}
		mask, err := coverageFromSampleMask(geometryMask.bounds, geometryMask, activeClip, sampleCell, &r.flatten, &r.renderer)
		if err != nil {
			return err
		}
		paint, err := r.paintSource(style.fill, style.color, style.fillOpacity, ctm, localBounds)
		if err != nil {
			releaseCoverageMask(&r.renderer, mask)
			return err
		}
		if paint != nil {
			dst.drawMask(paint, mask)
		}
		releaseCoverageMask(&r.renderer, mask)
	}
	if style.stroke.kind != paintNone && style.strokeWidth > 0 {
		mask, err := rasterizeStrokePrecision(r.bounds, path, ctm, style, activeClip, sampleCell, &r.flatten, &r.renderer)
		if err != nil {
			return err
		}
		paint, err := r.paintSource(style.stroke, style.color, style.strokeOpacity, ctm, localBounds)
		if err != nil {
			releaseCoverageMask(&r.renderer, mask)
			return err
		}
		if paint != nil {
			dst.drawMask(paint, mask)
		}
		releaseCoverageMask(&r.renderer, mask)
	}
	return nil
}

func nrgbaToFloat(value color.NRGBA, alpha float64) rgbaFloat {
	return rgbaFloat{
		r: float64(value.R) / 255,
		g: float64(value.G) / 255,
		b: float64(value.B) / 255,
		a: clampUnit(alpha),
	}
}

func (r *rgbaFloatRenderer) paintSource(spec paintSpec, current color.NRGBA, opacity float64, ctm Matrix, bounds floatBounds) (rgbaFloatPaint, error) {
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
		return r.gradientSource(spec.ref, opacity, ctm, bounds)
	default:
		return nil, nil
	}
	alpha := float64(value.A) / 255 * clampUnit(opacity)
	return rgbaFloatUniform{value: nrgbaToFloat(value, alpha)}, nil
}

type rgbaFloatGradientStop struct {
	offset float64
	value  rgbaFloat
}

type rgbaFloatGradient struct {
	definition gradientDefinition
	inverse    Matrix
	opacity    float64
	stops      []rgbaFloatGradientStop
}

func (g *rgbaFloatGradient) rgbaAt(x, y int) rgbaFloat {
	value := interpolateRGBAFloatStops(g.stops, gradientValueAt(g.definition, g.inverse, x, y))
	value.a *= g.opacity
	return value
}

func (r *rgbaFloatRenderer) gradientSource(ref string, opacity float64, ctm Matrix, bounds floatBounds) (rgbaFloatPaint, error) {
	n := r.icon.ids[ref]
	if n == nil || n.tag != "lineargradient" && n.tag != "radialgradient" {
		return nil, nil
	}
	definition, err := r.resolveGradient(n, make(map[*node]bool))
	if err != nil {
		return nil, err
	}
	definition = normalizeGradientDefinition(definition, r.icon.ViewBox)
	if len(definition.stops) == 0 {
		return nil, nil
	}
	matrix := ctm
	if definition.units == "objectboundingbox" {
		if !bounds.valid || bounds.width() == 0 || bounds.height() == 0 {
			return nil, nil
		}
		matrix = matrix.Mul(translate(bounds.min.X, bounds.min.Y)).Mul(scale(bounds.width(), bounds.height()))
	}
	matrix = matrix.Mul(definition.transform)
	inverse, ok := matrix.inverse()
	if !ok {
		return nil, nil
	}
	stops := make([]rgbaFloatGradientStop, len(definition.stops))
	for i, stop := range definition.stops {
		stops[i] = rgbaFloatGradientStop{offset: stop.offset, value: nrgbaToFloat(stop.color, stop.alpha)}
	}
	return &rgbaFloatGradient{
		definition: definition, inverse: inverse, opacity: clampUnit(opacity), stops: stops,
	}, nil
}

func interpolateRGBAFloatStops(stops []rgbaFloatGradientStop, value float64) rgbaFloat {
	if len(stops) == 0 {
		return rgbaFloat{}
	}
	if value <= stops[0].offset {
		return stops[0].value
	}
	for i := 1; i < len(stops); i++ {
		if value <= stops[i].offset {
			span := stops[i].offset - stops[i-1].offset
			if span <= 0 {
				return stops[i].value
			}
			return mixRGBAFloat(stops[i-1].value, stops[i].value, (value-stops[i-1].offset)/span)
		}
	}
	return stops[len(stops)-1].value
}

func mixRGBAFloat(a, b rgbaFloat, amount float64) rgbaFloat {
	mix := func(x, y float64) float64 { return x*(1-amount) + y*amount }
	return rgbaFloat{r: mix(a.r, b.r), g: mix(a.g, b.g), b: mix(a.b, b.b), a: mix(a.a, b.a)}
}
