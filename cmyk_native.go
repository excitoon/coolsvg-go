package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

// cmykValue is a straight-alpha CMYK sample. Keeping alpha separate lets the
// renderer preserve SVG group semantics until the final opaque background is
// reached, without asking color.CMYK to convert itself back through RGB.
type cmykValue struct {
	C uint8
	M uint8
	Y uint8
	K uint8
	A uint8
}

// cmykSurface is deliberately bounded to the requested output tile. Pixels
// are stored as C, M, Y, K, A in straight-alpha form.
type cmykSurface struct {
	bounds image.Rectangle
	stride int
	pix    []uint8
}

func newCMYKSurface(bounds image.Rectangle) (*cmykSurface, error) {
	width, height := bounds.Dx(), bounds.Dy()
	if width < 0 || height < 0 {
		return nil, fmt.Errorf("svg: invalid CMYK surface bounds %v", bounds)
	}
	maxInt := int(^uint(0) >> 1)
	if width != 0 && height > maxInt/width {
		return nil, fmt.Errorf("svg: CMYK surface bounds %v are too large", bounds)
	}
	pixels := width * height
	if pixels > maxInt/5 {
		return nil, fmt.Errorf("svg: CMYK surface bounds %v are too large", bounds)
	}
	return &cmykSurface{
		bounds: bounds,
		stride: width * 5,
		pix:    make([]uint8, pixels*5),
	}, nil
}

func (s *cmykSurface) offset(x, y int) int {
	return (y-s.bounds.Min.Y)*s.stride + (x-s.bounds.Min.X)*5
}

func (s *cmykSurface) cmykAt(x, y int) cmykValue {
	offset := s.offset(x, y)
	return cmykValue{
		C: s.pix[offset], M: s.pix[offset+1], Y: s.pix[offset+2],
		K: s.pix[offset+3], A: s.pix[offset+4],
	}
}

func (s *cmykSurface) fill(value cmykValue) {
	for offset := 0; offset < len(s.pix); offset += 5 {
		s.pix[offset] = value.C
		s.pix[offset+1] = value.M
		s.pix[offset+2] = value.Y
		s.pix[offset+3] = value.K
		s.pix[offset+4] = value.A
	}
}

type cmykPaint interface {
	cmykAt(x, y int) cmykValue
}

type cmykUniform struct {
	value cmykValue
}

func (u cmykUniform) cmykAt(_, _ int) cmykValue { return u.value }

func (s *cmykSurface) drawMask(source cmykPaint, mask *image.Alpha) {
	if source == nil || mask == nil {
		return
	}
	bounds := s.bounds.Intersect(mask.Bounds())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		destinationOffset := s.offset(bounds.Min.X, y)
		maskOffset := mask.PixOffset(bounds.Min.X, y)
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			coverage := mask.Pix[maskOffset]
			if coverage != 0 {
				s.blend(destinationOffset, source.cmykAt(x, y), coverage)
			}
			destinationOffset += 5
			maskOffset++
		}
	}
}

func (s *cmykSurface) blend(offset int, source cmykValue, coverage uint8) {
	sourceAlpha := div255(uint32(source.A) * uint32(coverage))
	if sourceAlpha == 0 {
		return
	}
	if sourceAlpha == 255 {
		s.pix[offset] = source.C
		s.pix[offset+1] = source.M
		s.pix[offset+2] = source.Y
		s.pix[offset+3] = source.K
		s.pix[offset+4] = 255
		return
	}
	destinationAlpha := uint32(s.pix[offset+4])
	if destinationAlpha == 0 {
		s.pix[offset] = source.C
		s.pix[offset+1] = source.M
		s.pix[offset+2] = source.Y
		s.pix[offset+3] = source.K
		s.pix[offset+4] = uint8(sourceAlpha)
		return
	}

	inverseAlpha := 255 - sourceAlpha
	outputAlpha := sourceAlpha + div255(destinationAlpha*inverseAlpha)
	sourceChannels := [4]uint8{source.C, source.M, source.Y, source.K}
	for channel, sourceChannel := range sourceChannels {
		destinationChannel := uint32(s.pix[offset+channel])
		premultiplied := uint32(sourceChannel)*sourceAlpha +
			div255(destinationChannel*destinationAlpha*inverseAlpha)
		s.pix[offset+channel] = uint8((premultiplied + outputAlpha/2) / outputAlpha)
	}
	s.pix[offset+4] = uint8(outputAlpha)
}

func div255(value uint32) uint32 {
	return (value + 127) / 255
}

func (s *cmykSurface) copyTo(dst *image.CMYK) {
	for y := 0; y < s.bounds.Dy(); y++ {
		sourceOffset := y * s.stride
		destinationY := dst.Rect.Min.Y + y
		destinationOffset := dst.PixOffset(dst.Rect.Min.X, destinationY)
		for x := 0; x < s.bounds.Dx(); x++ {
			dst.Pix[destinationOffset] = s.pix[sourceOffset]
			dst.Pix[destinationOffset+1] = s.pix[sourceOffset+1]
			dst.Pix[destinationOffset+2] = s.pix[sourceOffset+2]
			dst.Pix[destinationOffset+3] = s.pix[sourceOffset+3]
			sourceOffset += 5
			destinationOffset += 4
		}
	}
}

func separatedCMYKValue(space CMYKColorSpace, value color.NRGBA) cmykValue {
	separated := space.ConvertRGB(value.R, value.G, value.B)
	return cmykValue{C: separated.C, M: separated.M, Y: separated.Y, K: separated.K, A: value.A}
}

func (s *SvgIcon) renderCMYKNativeRegion(dst *image.CMYK, region image.Rectangle, space CMYKColorSpace, background color.Color) error {
	if s == nil || s.root == nil {
		return fmt.Errorf("svg: cannot draw a nil icon")
	}
	surface, err := newCMYKSurface(region)
	if err != nil {
		return err
	}
	backgroundValue := separatedCMYKValue(space, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	if background != nil {
		backgroundColor := color.NRGBAModel.Convert(background).(color.NRGBA)
		backgroundInk := separatedCMYKValue(space, backgroundColor)
		alpha := uint32(backgroundInk.A)
		mix := func(paper, ink uint8) uint8 {
			return uint8(div255(uint32(ink)*alpha + uint32(paper)*(255-alpha)))
		}
		backgroundValue = cmykValue{
			C: mix(backgroundValue.C, backgroundInk.C),
			M: mix(backgroundValue.M, backgroundInk.M),
			Y: mix(backgroundValue.Y, backgroundInk.Y),
			K: mix(backgroundValue.K, backgroundInk.K),
			A: 255,
		}
	}
	backgroundValue.A = 255
	surface.fill(backgroundValue)
	if region.Empty() {
		return nil
	}

	r := cmykRenderer{
		renderer: renderer{
			icon:     s,
			bounds:   region,
			useStack: make(map[*node]bool),
			budget:   maxRenderOperations,
		},
		colorSpace: space,
	}
	if err := r.renderNode(surface, s.root, defaultComputedStyle(), s.target, s.ViewBox, 1); err != nil {
		return err
	}
	surface.copyTo(dst)
	return nil
}

// cmykRenderer shares style resolution, geometry, masks, clipping, gradient
// definitions, <use> tracking, and the operation budget with renderer. Only
// paint sampling and compositing are CMYK-specific.
type cmykRenderer struct {
	renderer
	colorSpace CMYKColorSpace
}

func (r *cmykRenderer) renderNode(dst *cmykSurface, n *node, parentStyle computedStyle, parentCTM Matrix, viewport ViewBox, inheritedOpacity float64) error {
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
		layer, err := newCMYKSurface(r.bounds)
		if err != nil {
			return err
		}
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
		dst.drawMask(layer, clip)
		return nil
	}
	return r.renderContent(dst, n, style, ctm, viewport)
}

func (r *cmykRenderer) renderContent(dst *cmykSurface, n *node, style computedStyle, ctm Matrix, viewport ViewBox) error {
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

func (r *cmykRenderer) paintGeometry(dst *cmykSurface, path *Path, style computedStyle, ctm Matrix) error {
	contours := path.flatten(ctm, 0.2)
	localBounds := boundsOfContours(path.flatten(identityMatrix(), 0.1))
	if style.fill.kind != paintNone {
		mask := rasterizeContours(r.bounds, contours, style.fillRule)
		paint, err := r.paintSource(style.fill, style.color, style.fillOpacity, ctm, localBounds)
		if err != nil {
			return err
		}
		if paint != nil {
			dst.drawMask(paint, mask)
		}
	}
	if style.stroke.kind != paintNone && style.strokeWidth > 0 {
		mask := rasterizeStroke(r.bounds, path, ctm, style)
		paint, err := r.paintSource(style.stroke, style.color, style.strokeOpacity, ctm, localBounds)
		if err != nil {
			return err
		}
		if paint != nil {
			dst.drawMask(paint, mask)
		}
	}
	return nil
}

func (r *cmykRenderer) paintSource(spec paintSpec, current color.NRGBA, opacity float64, ctm Matrix, bounds floatBounds) (cmykPaint, error) {
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
	value.A = uint8(math.Round(float64(value.A) * math.Max(0, math.Min(1, opacity))))
	return cmykUniform{value: separatedCMYKValue(r.colorSpace, value)}, nil
}

type cmykGradientStop struct {
	offset float64
	value  cmykValue
}

type cmykGradient struct {
	definition gradientDefinition
	inverse    Matrix
	opacity    float64
	stops      []cmykGradientStop
}

func (g *cmykGradient) cmykAt(x, y int) cmykValue {
	value := interpolateCMYKStops(g.stops, gradientValueAt(g.definition, g.inverse, x, y))
	value.A = uint8(math.Round(float64(value.A) * g.opacity))
	return value
}

func (r *cmykRenderer) gradientSource(ref string, opacity float64, ctm Matrix, bounds floatBounds) (cmykPaint, error) {
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
	stops := make([]cmykGradientStop, len(definition.stops))
	for i, stop := range definition.stops {
		stops[i] = cmykGradientStop{
			offset: stop.offset,
			value:  separatedCMYKValue(r.colorSpace, stop.color),
		}
	}
	return &cmykGradient{
		definition: definition,
		inverse:    inverse,
		opacity:    math.Max(0, math.Min(1, opacity)),
		stops:      stops,
	}, nil
}

func interpolateCMYKStops(stops []cmykGradientStop, value float64) cmykValue {
	if len(stops) == 0 {
		return cmykValue{}
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
			return mixCMYK(stops[i-1].value, stops[i].value, (value-stops[i-1].offset)/span)
		}
	}
	return stops[len(stops)-1].value
}

func mixCMYK(a, b cmykValue, amount float64) cmykValue {
	mix := func(x, y uint8) uint8 {
		return uint8(math.Round(float64(x)*(1-amount) + float64(y)*amount))
	}
	return cmykValue{
		C: mix(a.C, b.C), M: mix(a.M, b.M), Y: mix(a.Y, b.Y),
		K: mix(a.K, b.K), A: mix(a.A, b.A),
	}
}
