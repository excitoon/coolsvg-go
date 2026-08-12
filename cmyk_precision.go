package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

type cmykFloat struct {
	c float64
	m float64
	y float64
	k float64
	a float64
}

type cmykFloatSurface struct {
	bounds image.Rectangle
	stride int
	pix    []float64
}

func newCMYKFloatSurface(bounds image.Rectangle) (*cmykFloatSurface, error) {
	length, stride, ok := checkedImageSize(bounds, 5)
	if !ok || length > precisionMaxSurfaceBytes/8 {
		return nil, fmt.Errorf("svg: floating-point CMYK surface bounds %v are too large", bounds)
	}
	return &cmykFloatSurface{bounds: bounds, stride: stride, pix: make([]float64, length)}, nil
}

func (s *cmykFloatSurface) offset(x, y int) int {
	return (y-s.bounds.Min.Y)*s.stride + (x-s.bounds.Min.X)*5
}

func (s *cmykFloatSurface) cmykAt(x, y int) cmykFloat {
	offset := s.offset(x, y)
	return cmykFloat{c: s.pix[offset], m: s.pix[offset+1], y: s.pix[offset+2], k: s.pix[offset+3], a: s.pix[offset+4]}
}

func (s *cmykFloatSurface) fill(value cmykFloat) {
	for offset := 0; offset < len(s.pix); offset += 5 {
		s.pix[offset], s.pix[offset+1], s.pix[offset+2] = value.c, value.m, value.y
		s.pix[offset+3], s.pix[offset+4] = value.k, value.a
	}
}

func (s *cmykFloatSurface) blend(offset int, source cmykFloat, coverage float64) {
	sourceAlpha := clampUnit(source.a * coverage)
	if sourceAlpha <= 0 {
		return
	}
	if sourceAlpha >= 1 {
		s.pix[offset], s.pix[offset+1], s.pix[offset+2] = source.c, source.m, source.y
		s.pix[offset+3], s.pix[offset+4] = source.k, 1
		return
	}
	destinationAlpha := s.pix[offset+4]
	if destinationAlpha <= 0 {
		s.pix[offset], s.pix[offset+1], s.pix[offset+2] = source.c, source.m, source.y
		s.pix[offset+3], s.pix[offset+4] = source.k, sourceAlpha
		return
	}
	inverse := 1 - sourceAlpha
	outputAlpha := sourceAlpha + destinationAlpha*inverse
	sourceChannels := [4]float64{source.c, source.m, source.y, source.k}
	for channel, sourceChannel := range sourceChannels {
		s.pix[offset+channel] = (sourceChannel*sourceAlpha + s.pix[offset+channel]*destinationAlpha*inverse) / outputAlpha
	}
	s.pix[offset+4] = outputAlpha
}

type cmykFloatPaint interface {
	cmykAt(x, y int) cmykFloat
}

type cmykFloatUniform struct{ value cmykFloat }

func (u cmykFloatUniform) cmykAt(_, _ int) cmykFloat { return u.value }

func (s *cmykFloatSurface) drawMask(source cmykFloatPaint, mask *coverageMask) {
	if source == nil || mask == nil {
		return
	}
	bounds := s.bounds.Intersect(mask.bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		destinationOffset := s.offset(bounds.Min.X, y)
		maskOffset := mask.offset(bounds.Min.X, y)
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if coverage := mask.values[maskOffset]; coverage > 0 {
				s.blend(destinationOffset, source.cmykAt(x, y), coverage)
			}
			destinationOffset += 5
			maskOffset++
		}
	}
}

type cmykFloatConverter func(r, g, b float64) cmykFloat

func resolveCMYKFloatConverter(options CMYKOptions) (cmykFloatConverter, error) {
	if options.ColorSpaceFloat != nil && nilInterface(options.ColorSpaceFloat) {
		return nil, fmt.Errorf("svg: CMYK floating-point color space is nil")
	}
	if options.ColorSpace64 != nil && nilInterface(options.ColorSpace64) {
		return nil, fmt.Errorf("svg: CMYK 16-bit color space is nil")
	}
	if options.ColorSpace != nil && nilInterface(options.ColorSpace) {
		return nil, fmt.Errorf("svg: CMYK color space is nil")
	}
	if function, ok := options.ColorSpaceFloat.(CMYKColorSpaceFloatFunc); ok && function == nil {
		return nil, fmt.Errorf("svg: CMYK floating-point color-space function is nil")
	}
	if function, ok := options.ColorSpace64.(CMYKColorSpace64Func); ok && function == nil {
		return nil, fmt.Errorf("svg: CMYK 16-bit color-space function is nil")
	}
	if function, ok := options.ColorSpace.(CMYKColorSpaceFunc); ok && function == nil {
		return nil, fmt.Errorf("svg: CMYK color-space function is nil")
	}
	if options.ColorSpaceFloat != nil {
		return func(r, g, b float64) cmykFloat {
			c, m, y, k := options.ColorSpaceFloat.ConvertRGBFloat(r, g, b)
			return cmykFloat{c: clampUnit(c), m: clampUnit(m), y: clampUnit(y), k: clampUnit(k), a: 1}
		}, nil
	}
	if options.ColorSpace64 != nil {
		if floatSpace, ok := options.ColorSpace64.(CMYKColorSpaceFloat); ok {
			return func(r, g, b float64) cmykFloat {
				c, m, y, k := floatSpace.ConvertRGBFloat(r, g, b)
				return cmykFloat{c: clampUnit(c), m: clampUnit(m), y: clampUnit(y), k: clampUnit(k), a: 1}
			}, nil
		}
		return func(r, g, b float64) cmykFloat {
			value := options.ColorSpace64.ConvertRGB64(
				quantizeNearest(r, 16), quantizeNearest(g, 16), quantizeNearest(b, 16),
			)
			return cmyk64ColorToFloat(value)
		}, nil
	}
	if options.ColorSpace != nil {
		if floatSpace, ok := options.ColorSpace.(CMYKColorSpaceFloat); ok {
			return func(r, g, b float64) cmykFloat {
				c, m, y, k := floatSpace.ConvertRGBFloat(r, g, b)
				return cmykFloat{c: clampUnit(c), m: clampUnit(m), y: clampUnit(y), k: clampUnit(k), a: 1}
			}, nil
		}
		return func(r, g, b float64) cmykFloat {
			value := options.ColorSpace.ConvertRGB(
				uint8(quantizeNearest(r, 8)), uint8(quantizeNearest(g, 8)), uint8(quantizeNearest(b, 8)),
			)
			return cmykFloat{
				c: float64(value.C) / 255, m: float64(value.M) / 255,
				y: float64(value.Y) / 255, k: float64(value.K) / 255, a: 1,
			}
		}, nil
	}
	return deviceRGBToCMYKFloat, nil
}

func deviceRGBToCMYKFloat(r, g, b float64) cmykFloat {
	r, g, b = clampUnit(r), clampUnit(g), clampUnit(b)
	k := 1 - math.Max(r, math.Max(g, b))
	if k >= 1 {
		return cmykFloat{k: 1, a: 1}
	}
	denominator := 1 - k
	return cmykFloat{
		c: (1 - r - k) / denominator,
		m: (1 - g - k) / denominator,
		y: (1 - b - k) / denominator,
		k: k,
		a: 1,
	}
}

func cmyk64ColorToFloat(value CMYK64Color) cmykFloat {
	return cmykFloat{
		c: float64(value.C) / 0xffff, m: float64(value.M) / 0xffff,
		y: float64(value.Y) / 0xffff, k: float64(value.K) / 0xffff, a: 1,
	}
}

// RenderCMYK64 renders the icon into an opaque 16-bit CMYK raster.
func (s *SvgIcon) RenderCMYK64(dst *CMYK64, options CMYKOptions) error {
	if dst == nil {
		return fmt.Errorf("svg: CMYK64 destination is nil")
	}
	return s.RenderCMYK64Region(dst, dst.Bounds(), options)
}

// RenderCMYK64Region renders a global region into an opaque 16-bit CMYK
// raster, using bounded floating-point working bands.
func (s *SvgIcon) RenderCMYK64Region(dst *CMYK64, region image.Rectangle, options CMYKOptions) error {
	if dst == nil {
		return fmt.Errorf("svg: CMYK64 destination is nil")
	}
	if !rectanglesHaveSameSize(dst.Bounds(), region) {
		return fmt.Errorf("svg: destination bounds %v and region %v must have the same dimensions", dst.Bounds(), region)
	}
	return s.renderCMYKPrecision(region, options, nil, dst)
}

func (s *SvgIcon) renderCMYKPrecision8(dst *image.CMYK, region image.Rectangle, options CMYKOptions) error {
	return s.renderCMYKPrecision(region, options, dst, nil)
}

func (s *SvgIcon) renderCMYKPrecision(region image.Rectangle, options CMYKOptions, dst8 *image.CMYK, dst16 *CMYK64) error {
	if s == nil || s.root == nil {
		return fmt.Errorf("svg: cannot draw a nil icon")
	}
	if options.ProcessingMode > CMYKNative {
		return fmt.Errorf("svg: unsupported CMYK processing mode %d", options.ProcessingMode)
	}
	if err := validateDither(options.Dither); err != nil {
		return err
	}
	converter, err := resolveCMYKFloatConverter(options)
	if err != nil {
		return err
	}
	if region.Empty() {
		return nil
	}
	targetBits := 16
	if dst8 != nil {
		targetBits = 8
	}
	dither, externalState := isolateFloydSteinbergState(options.Dither)
	for minimumY := region.Min.Y; minimumY < region.Max.Y; {
		height := precisionBandHeight(region.Dx(), 10, region.Max.Y-minimumY)
		band := image.Rect(region.Min.X, minimumY, region.Max.X, minimumY+height)
		var surface *cmykFloatSurface
		if options.ProcessingMode == CMYKNative {
			surface, err = s.renderCMYKNativeFloatBand(band, options.Background, converter)
		} else {
			surface, err = s.renderCMYKPostConvertFloatBand(band, options.Background, converter)
		}
		if err != nil {
			return err
		}
		if err := quantizeCMYKFloatSurface(dst8, dst16, region, surface, targetBits, dither); err != nil {
			return err
		}
		minimumY = band.Max.Y
	}
	commitFloydSteinbergState(externalState, dither.State)
	return nil
}

func (s *SvgIcon) renderCMYKPostConvertFloatBand(band image.Rectangle, background color.Color, converter cmykFloatConverter) (*cmykFloatSurface, error) {
	rgb, err := newRGBAFloatSurface(band)
	if err != nil {
		return nil, err
	}
	backgroundValue := rgbaFloat{r: 1, g: 1, b: 1, a: 1}
	if !nilColor(background) {
		value := color.NRGBA64Model.Convert(background).(color.NRGBA64)
		alpha := float64(value.A) / 0xffff
		backgroundValue = rgbaFloat{
			r: float64(value.R)/0xffff*alpha + 1 - alpha,
			g: float64(value.G)/0xffff*alpha + 1 - alpha,
			b: float64(value.B)/0xffff*alpha + 1 - alpha,
			a: 1,
		}
	}
	for y := band.Min.Y; y < band.Max.Y; y++ {
		for x := band.Min.X; x < band.Max.X; x++ {
			rgb.set(x, y, backgroundValue)
		}
	}
	renderer := newRGBAFloatRenderer(s, band)
	if err := renderer.renderNode(rgb, s.root, defaultComputedStyle(), s.target, s.ViewBox, 1); err != nil {
		return nil, err
	}
	result, err := newCMYKFloatSurface(band)
	if err != nil {
		return nil, err
	}
	for y := band.Min.Y; y < band.Max.Y; y++ {
		for x := band.Min.X; x < band.Max.X; x++ {
			value := rgb.rgbaAt(x, y)
			separated := converter(value.r, value.g, value.b)
			separated.a = 1
			offset := result.offset(x, y)
			result.pix[offset], result.pix[offset+1], result.pix[offset+2] = separated.c, separated.m, separated.y
			result.pix[offset+3], result.pix[offset+4] = separated.k, 1
		}
	}
	return result, nil
}

func (s *SvgIcon) renderCMYKNativeFloatBand(band image.Rectangle, background color.Color, converter cmykFloatConverter) (*cmykFloatSurface, error) {
	result, err := newCMYKFloatSurface(band)
	if err != nil {
		return nil, err
	}
	backgroundValue := converter(1, 1, 1)
	backgroundValue.a = 1
	if !nilColor(background) {
		value := color.NRGBA64Model.Convert(background).(color.NRGBA64)
		alpha := float64(value.A) / 0xffff
		ink := converter(float64(value.R)/0xffff, float64(value.G)/0xffff, float64(value.B)/0xffff)
		backgroundValue = cmykFloat{
			c: ink.c*alpha + backgroundValue.c*(1-alpha),
			m: ink.m*alpha + backgroundValue.m*(1-alpha),
			y: ink.y*alpha + backgroundValue.y*(1-alpha),
			k: ink.k*alpha + backgroundValue.k*(1-alpha),
			a: 1,
		}
	}
	result.fill(backgroundValue)
	renderer := newCMYKFloatRenderer(s, band, converter)
	if err := renderer.renderNode(result, s.root, defaultComputedStyle(), s.target, s.ViewBox, 1); err != nil {
		return nil, err
	}
	return result, nil
}

func quantizeCMYKFloatSurface(dst8 *image.CMYK, dst16 *CMYK64, region image.Rectangle, surface *cmykFloatSurface, targetBits int, options DitherOptions) error {
	effectiveBounds := surface.bounds
	if options.Mode != DitherNone {
		var err error
		effectiveBounds, err = offsetDitherBounds(surface.bounds, options.CoordinateOffset)
		if err != nil {
			return fmt.Errorf("svg: %w", err)
		}
	}
	var state *FloydSteinbergState
	if options.Mode == DitherFloydSteinberg {
		state = options.State
		if err := state.begin(effectiveBounds, 4, targetBits); err != nil {
			return err
		}
		defer state.end(effectiveBounds.Max.Y)
	}
	width := surface.bounds.Dx()
	samples := make([]float64, width*4)
	quantized := make([]uint16, width*4)
	var destinationBounds image.Rectangle
	if dst8 != nil {
		destinationBounds = dst8.Bounds()
	} else {
		destinationBounds = dst16.Bounds()
	}
	for y := surface.bounds.Min.Y; y < surface.bounds.Max.Y; y++ {
		globalY := effectiveBounds.Min.Y + (y - surface.bounds.Min.Y)
		for x := surface.bounds.Min.X; x < surface.bounds.Max.X; x++ {
			value := surface.cmykAt(x, y)
			index := (x - surface.bounds.Min.X) * 4
			samples[index], samples[index+1], samples[index+2], samples[index+3] = value.c, value.m, value.y, value.k
			if options.Mode == DitherNone {
				quantized[index] = quantizeNearest(value.c, targetBits)
				quantized[index+1] = quantizeNearest(value.m, targetBits)
				quantized[index+2] = quantizeNearest(value.y, targetBits)
				quantized[index+3] = quantizeNearest(value.k, targetBits)
			} else if options.Mode == DitherBayer8x8 {
				globalX := effectiveBounds.Min.X + (x - surface.bounds.Min.X)
				quantized[index] = quantizeBayer(value.c, targetBits, globalX, globalY)
				quantized[index+1] = quantizeBayer(value.m, targetBits, globalX, globalY)
				quantized[index+2] = quantizeBayer(value.y, targetBits, globalX, globalY)
				quantized[index+3] = quantizeBayer(value.k, targetBits, globalX, globalY)
			}
		}
		if state != nil {
			state.quantizeRow(samples, globalY, targetBits, quantized)
		}
		for x := surface.bounds.Min.X; x < surface.bounds.Max.X; x++ {
			index := (x - surface.bounds.Min.X) * 4
			localX := destinationBounds.Min.X + x - region.Min.X
			localY := destinationBounds.Min.Y + y - region.Min.Y
			if dst8 != nil {
				dst8.SetCMYK(localX, localY, color.CMYK{
					C: uint8(quantized[index]), M: uint8(quantized[index+1]),
					Y: uint8(quantized[index+2]), K: uint8(quantized[index+3]),
				})
			} else {
				dst16.SetCMYK64(localX, localY, CMYK64Color{
					C: quantized[index], M: quantized[index+1],
					Y: quantized[index+2], K: quantized[index+3],
				})
			}
		}
	}
	return nil
}

type cmykFloatRenderer struct {
	renderer
	convert     cmykFloatConverter
	surfacePool []*cmykFloatSurface
}

func newCMYKFloatRenderer(icon *SvgIcon, bounds image.Rectangle, converter cmykFloatConverter) cmykFloatRenderer {
	return cmykFloatRenderer{
		renderer: renderer{
			icon: icon, bounds: bounds, useStack: make(map[*node]bool), budget: maxRenderOperations,
			flatten: newFlattenBudget(), surfaces: newPrecisionSurfaceBudget(),
		},
		convert: converter,
	}
}

func (r *cmykFloatRenderer) newLayerSurface() (*cmykFloatSurface, func(), error) {
	if count := len(r.surfacePool); count > 0 {
		surface := r.surfacePool[count-1]
		r.surfacePool = r.surfacePool[:count-1]
		clear(surface.pix)
		return surface, func() { r.surfacePool = append(r.surfacePool, surface) }, nil
	}
	elements, _, ok := checkedImageSize(r.bounds, 5)
	if !ok || elements > precisionMaxSurfaceBytes/8 {
		_, err := newCMYKFloatSurface(r.bounds)
		return nil, nil, err
	}
	bytes, err := r.surfaces.reserve(elements)
	if err != nil {
		return nil, nil, err
	}
	surface, err := newCMYKFloatSurface(r.bounds)
	if err != nil {
		r.surfaces.release(bytes)
		return nil, nil, err
	}
	return surface, func() { r.surfacePool = append(r.surfacePool, surface) }, nil
}

func (r *cmykFloatRenderer) renderNode(dst *cmykFloatSurface, n *node, parentStyle computedStyle, parentCTM Matrix, viewport ViewBox, inheritedOpacity float64) error {
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
		render := func(target *cmykFloatSurface, active *sampleMask, cell *precisionSampleCell) error {
			return r.renderNodeClipped(target, n, parentStyle, parentCTM, viewport, inheritedOpacity, active, cell)
		}
		if err := r.renderCMYKFloatDomain(layer, scratch, domain, sy, nil, nil, render); err != nil {
			return err
		}
	}
	compositeCMYKFloatLayer(dst, layer, 1)
	return nil
}

type cmykFloatDomainRenderer func(*cmykFloatSurface, *sampleMask, *precisionSampleCell) error

func (r *cmykFloatRenderer) renderCMYKFloatDomain(dst, scratch *cmykFloatSurface, domain *sampleMask, sy int, parentSample *precisionSampleCell, parentPartitions *precisionPartitions, render cmykFloatDomainRenderer) error {
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
		accumulateCMYKFloatSample(dst, scratch, domain, sy, parentSample)
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
		accumulateCMYKFloatSample(dst, scratch, stable, sy, parentSample)
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
		accumulateCMYKFloatSample(dst, scratch, atomic, sy, parentSample)
	}
	return nil
}

func (r *cmykFloatRenderer) renderNodeClipped(dst *cmykFloatSurface, n *node, parentStyle computedStyle, parentCTM Matrix, viewport ViewBox, inheritedOpacity float64, activeClip *sampleMask, sampleCell *precisionSampleCell) error {
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
			compositeCMYKFloatLayer(dst, layer, groupOpacity)
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
		compositeCMYKFloatLayer(dst, layer, groupOpacity)
		return nil
	}
	return r.renderContent(dst, n, style, ctm, viewport, effectiveClip, sampleCell)
}

func (r *cmykFloatRenderer) renderSampledClipGroup(dst *cmykFloatSurface, n *node, style computedStyle, ctm Matrix, viewport ViewBox, groupOpacity float64, clip *sampleMask, parentSample *precisionSampleCell) error {
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
				render := func(target *cmykFloatSurface, active *sampleMask, cell *precisionSampleCell) error {
					if err := r.step(); err != nil {
						return err
					}
					return r.renderContent(target, n, style, ctm, viewport, active, cell)
				}
				var parentPartitions *precisionPartitions
				if parentSample != nil {
					parentPartitions = parentSample.partitions
				}
				if err := r.renderCMYKFloatDomain(layer, scratch, segment, sy, parentSample, parentPartitions, render); err != nil {
					return err
				}
			}
		}
	}
	compositeCMYKFloatLayer(dst, layer, groupOpacity)
	return nil
}

func compositeCMYKFloatLayer(dst, layer *cmykFloatSurface, opacity float64) {
	opacity = clampUnit(opacity)
	if opacity <= 0 {
		return
	}
	bounds := dst.bounds.Intersect(layer.bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		offset := dst.offset(bounds.Min.X, y)
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			layerOffset := layer.offset(x, y)
			if layer.pix[layerOffset+4] > 0 {
				dst.blend(offset, cmykFloat{
					c: layer.pix[layerOffset], m: layer.pix[layerOffset+1],
					y: layer.pix[layerOffset+2], k: layer.pix[layerOffset+3], a: layer.pix[layerOffset+4],
				}, opacity)
			}
			offset += 5
		}
	}
}

func accumulateCMYKFloatSample(dst, sample *cmykFloatSurface, segment *sampleMask, sy int, parentSample *precisionSampleCell) {
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
			addedAlpha := sample.pix[offset+4] * weight
			if addedAlpha <= 0 {
				continue
			}
			previousAlpha := dst.pix[offset+4]
			combinedAlpha := previousAlpha + addedAlpha
			for channel := 0; channel < 4; channel++ {
				dst.pix[offset+channel] = (dst.pix[offset+channel]*previousAlpha + sample.pix[offset+channel]*addedAlpha) / combinedAlpha
			}
			dst.pix[offset+4] = combinedAlpha
		}
	}
}

func (r *cmykFloatRenderer) renderContent(dst *cmykFloatSurface, n *node, style computedStyle, ctm Matrix, viewport ViewBox, activeClip *sampleMask, sampleCell *precisionSampleCell) error {
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

func (r *cmykFloatRenderer) paintGeometry(dst *cmykFloatSurface, path *Path, style computedStyle, ctm Matrix, activeClip *sampleMask, sampleCell *precisionSampleCell) error {
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

func (r *cmykFloatRenderer) convertColor(value color.NRGBA, alpha float64) cmykFloat {
	result := r.convert(float64(value.R)/255, float64(value.G)/255, float64(value.B)/255)
	result.a = clampUnit(alpha)
	return result
}

func (r *cmykFloatRenderer) paintSource(spec paintSpec, current color.NRGBA, opacity float64, ctm Matrix, bounds floatBounds) (cmykFloatPaint, error) {
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
	return cmykFloatUniform{value: r.convertColor(value, alpha)}, nil
}

type cmykFloatGradientStop struct {
	offset float64
	value  cmykFloat
}

type cmykFloatGradient struct {
	definition gradientDefinition
	inverse    Matrix
	opacity    float64
	stops      []cmykFloatGradientStop
}

func (g *cmykFloatGradient) cmykAt(x, y int) cmykFloat {
	value := interpolateCMYKFloatStops(g.stops, gradientValueAt(g.definition, g.inverse, x, y))
	value.a *= g.opacity
	return value
}

func (r *cmykFloatRenderer) gradientSource(ref string, opacity float64, ctm Matrix, bounds floatBounds) (cmykFloatPaint, error) {
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
	stops := make([]cmykFloatGradientStop, len(definition.stops))
	for i, stop := range definition.stops {
		stops[i] = cmykFloatGradientStop{offset: stop.offset, value: r.convertColor(stop.color, stop.alpha)}
	}
	return &cmykFloatGradient{
		definition: definition, inverse: inverse, opacity: clampUnit(opacity), stops: stops,
	}, nil
}

func interpolateCMYKFloatStops(stops []cmykFloatGradientStop, value float64) cmykFloat {
	if len(stops) == 0 {
		return cmykFloat{}
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
			return mixCMYKFloat(stops[i-1].value, stops[i].value, (value-stops[i-1].offset)/span)
		}
	}
	return stops[len(stops)-1].value
}

func mixCMYKFloat(a, b cmykFloat, amount float64) cmykFloat {
	mix := func(x, y float64) float64 { return x*(1-amount) + y*amount }
	return cmykFloat{
		c: mix(a.c, b.c), m: mix(a.m, b.m), y: mix(a.y, b.y),
		k: mix(a.k, b.k), a: mix(a.a, b.a),
	}
}
