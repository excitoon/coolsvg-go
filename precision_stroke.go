package coolsvg

import (
	"fmt"
	"image"
	"math"
	"slices"
)

type strokeMaskCacheKey struct {
	path                  *Path
	matrix                Matrix
	width, miter, dashOff float64
	cap, join             string
	dashLength            int
	dashHash              uint64
}

type strokeMaskCacheEntry struct {
	dashArray []float64
	masks     []*sampleMask
}

func rasterizeStrokePrecision(bounds image.Rectangle, path *Path, ctm Matrix, style computedStyle, clip *sampleMask, cell *precisionSampleCell, budget *flattenBudget, owner *renderer) (*coverageMask, error) {
	half := style.strokeWidth / 2
	if half <= 0 {
		return nil, nil
	}
	if owner == nil {
		return rasterizeStrokePrecisionUncached(bounds, path, ctm, style, clip, cell, budget)
	}
	masks, err := owner.strokeSampleMasks(path, ctm, style)
	if err != nil {
		return nil, err
	}
	minimumSample, maximumSample := 0, antialiasSamples
	if cell != nil {
		if cell.y < 0 || cell.y >= antialiasSamples {
			return nil, nil
		}
		minimumSample, maximumSample = cell.y, cell.y+1
	}
	effectiveClip := clip
	if effectiveClip == nil && cell != nil {
		effectiveClip = cell.domain
	}
	if effectiveClip != nil && effectiveClip.err != nil {
		return nil, effectiveClip.err
	}

	activeBounds := image.Rectangle{}
	hasActive := false
	for _, mask := range masks {
		candidate := sampleMaskSelectedBounds(mask.bounds, mask, minimumSample, maximumSample)
		if effectiveClip != nil {
			candidate = sampleMasksIntersectionBounds(mask.bounds, mask, effectiveClip, minimumSample, maximumSample)
		}
		if candidate.Empty() {
			continue
		}
		if !hasActive {
			activeBounds, hasActive = candidate, true
		} else {
			activeBounds = activeBounds.Union(candidate)
		}
	}
	if !hasActive {
		return nil, nil
	}
	result := acquireCoverageMask(owner, activeBounds)
	for _, primitive := range masks {
		mask, err := coverageFromSampleMask(primitive.bounds, primitive, effectiveClip, cell, budget, owner)
		if err != nil {
			releaseCoverageMask(owner, result)
			return nil, err
		}
		unionCoverageMask(result, mask)
		releaseCoverageMask(owner, mask)
	}
	return result, nil
}

func (r *renderer) strokeSampleMasks(path *Path, ctm Matrix, style computedStyle) ([]*sampleMask, error) {
	key := strokeMaskCacheKey{
		path: path, matrix: ctm, width: style.strokeWidth, miter: style.miterLimit,
		cap: style.strokeCap, join: style.strokeJoin, dashOff: style.dashOffset,
		dashLength: len(style.dashArray), dashHash: strokeDashHash(style.dashArray),
	}
	for _, entry := range r.strokeCache[key] {
		if slices.Equal(entry.dashArray, style.dashArray) {
			return entry.masks, nil
		}
	}
	masks := make([]*sampleMask, 0)
	err := visitStrokePrimitives(path, style, &r.flatten, r, func(points []Point) error {
		transformed := make([]Point, len(points)+1)
		for i, point := range points {
			transformed[i] = ctm.Apply(point)
			if !finitePoint(transformed[i]) {
				return fmt.Errorf("svg: stroke transform produced non-finite coordinates")
			}
		}
		transformed[len(points)] = transformed[0]
		primitiveBounds := pixelBoundsForPoints(r.bounds, transformed)
		if primitiveBounds.Empty() {
			return nil
		}
		mask, err := rasterizeClipContours(primitiveBounds, []contour{{points: transformed, closed: true}}, nonZero, &r.flatten, &r.auxiliary)
		if err != nil {
			return err
		}
		if len(mask.intervals) != 0 {
			masks = append(masks, mask)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if r.strokeCache == nil {
		r.strokeCache = make(map[strokeMaskCacheKey][]strokeMaskCacheEntry)
	}
	r.strokeCache[key] = append(r.strokeCache[key], strokeMaskCacheEntry{
		dashArray: slices.Clone(style.dashArray), masks: masks,
	})
	return masks, nil
}

func strokeDashHash(values []float64) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	for _, value := range values {
		hash ^= math.Float64bits(value)
		hash *= prime
	}
	return hash
}

func rasterizeStrokePrecisionUncached(bounds image.Rectangle, path *Path, ctm Matrix, style computedStyle, clip *sampleMask, cell *precisionSampleCell, budget *flattenBudget) (*coverageMask, error) {
	if err := budget.spendRaster(0, bounds.Dx(), bounds.Dy(), 0); err != nil {
		return nil, err
	}
	result := newCoverageMask(bounds)
	err := visitStrokePrimitives(path, style, budget, nil, func(points []Point) error {
		return addStrokePrimitivePrecision(bounds, result, ctm, points, clip, cell, budget, nil)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func visitStrokePrimitives(path *Path, style computedStyle, budget *flattenBudget, owner *renderer, visit func([]Point) error) error {
	var contours []contour
	var err error
	if owner != nil {
		contours, err = owner.flattenPath(path, identityMatrix(), 0.1)
	} else {
		contours, err = path.flattenWithBudget(identityMatrix(), 0.1, budget)
	}
	if err != nil {
		return err
	}
	half := style.strokeWidth / 2
	for _, source := range contours {
		pieces := []contour{source}
		if len(style.dashArray) > 0 {
			pieces = dashContours(source, style.dashArray, style.dashOffset)
		}
		for _, piece := range pieces {
			if err := visitStrokeContourPrimitives(piece, half, style, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func visitStrokeContourPrimitives(c contour, half float64, style computedStyle, visit func([]Point) error) error {
	points := append([]Point(nil), c.points...)
	if len(points) < 2 {
		return nil
	}
	closed := c.closed
	if closed && almostEqual(points[0], points[len(points)-1]) {
		points = points[:len(points)-1]
	}
	if len(points) < 2 {
		return nil
	}
	if !closed && style.strokeCap == "square" {
		if direction, ok := unitDirection(points[0], points[1]); ok {
			points[0] = sub(points[0], mul(direction, half))
		}
		last := len(points) - 1
		if direction, ok := unitDirection(points[last-1], points[last]); ok {
			points[last] = add(points[last], mul(direction, half))
		}
	}
	segmentCount := len(points) - 1
	if closed {
		segmentCount = len(points)
	}
	for i := 0; i < segmentCount; i++ {
		a, b := points[i], points[(i+1)%len(points)]
		direction, ok := unitDirection(a, b)
		if !ok {
			continue
		}
		normal := Point{-direction.Y * half, direction.X * half}
		if err := visit([]Point{
			add(a, normal), add(b, normal), sub(b, normal), sub(a, normal),
		}); err != nil {
			return err
		}
	}
	joinStart, joinEnd := 1, len(points)-1
	if closed {
		joinStart, joinEnd = 0, len(points)
	}
	for i := joinStart; i < joinEnd; i++ {
		previous := points[(i-1+len(points))%len(points)]
		joint := points[i%len(points)]
		next := points[(i+1)%len(points)]
		if err := visitStrokeJoinPrimitive(previous, joint, next, half, style, visit); err != nil {
			return err
		}
	}
	if !closed && style.strokeCap == "round" {
		if err := visit(strokeCirclePrimitive(points[0], half)); err != nil {
			return err
		}
		if err := visit(strokeCirclePrimitive(points[len(points)-1], half)); err != nil {
			return err
		}
	}
	return nil
}

func visitStrokeJoinPrimitive(previous, joint, next Point, half float64, style computedStyle, visit func([]Point) error) error {
	before, okBefore := unitDirection(previous, joint)
	after, okAfter := unitDirection(joint, next)
	if !okBefore || !okAfter {
		return nil
	}
	cross := before.X*after.Y - before.Y*after.X
	if math.Abs(cross) < 1e-12 {
		return nil
	}
	if style.strokeJoin == "round" || style.strokeJoin == "arcs" {
		return visit(strokeCirclePrimitive(joint, half))
	}
	side := 1.0
	if cross < 0 {
		side = -1
	}
	n1 := Point{-before.Y * half * side, before.X * half * side}
	n2 := Point{-after.Y * half * side, after.X * half * side}
	a, b := add(joint, n1), add(joint, n2)
	if style.strokeJoin == "miter" || style.strokeJoin == "miter-clip" {
		if intersection, ok := lineIntersection(a, before, b, after); ok && math.Hypot(intersection.X-joint.X, intersection.Y-joint.Y) <= half*style.miterLimit {
			return visit([]Point{joint, a, intersection, b})
		}
	}
	return visit([]Point{joint, a, b})
}

func strokeCirclePrimitive(center Point, radius float64) []Point {
	const sides = 32
	points := make([]Point, sides)
	for i := range points {
		angle := float64(i) * 2 * math.Pi / sides
		points[i] = Point{center.X + math.Cos(angle)*radius, center.Y + math.Sin(angle)*radius}
	}
	return points
}

func addStrokePrimitivePrecision(bounds image.Rectangle, dst *coverageMask, ctm Matrix, points []Point, clip *sampleMask, cell *precisionSampleCell, budget *flattenBudget, owner *renderer) error {
	if len(points) < 3 {
		return nil
	}
	transformed := make([]Point, len(points)+1)
	for i, point := range points {
		transformed[i] = ctm.Apply(point)
		if !finitePoint(transformed[i]) {
			return fmt.Errorf("svg: stroke transform produced non-finite coordinates")
		}
	}
	transformed[len(points)] = transformed[0]
	primitiveBounds := pixelBoundsForPoints(bounds, transformed)
	if primitiveBounds.Empty() {
		return nil
	}
	mask, err := rasterizeContoursPrecisionClipped(primitiveBounds, []contour{{points: transformed, closed: true}}, nonZero, clip, cell, budget, owner)
	if err != nil {
		return err
	}
	unionCoverageMask(dst, mask)
	releaseCoverageMask(owner, mask)
	return nil
}

func pixelBoundsForPoints(bounds image.Rectangle, points []Point) image.Rectangle {
	if bounds.Empty() || len(points) == 0 {
		return image.Rectangle{}
	}
	minimumX, minimumY := math.Inf(1), math.Inf(1)
	maximumX, maximumY := math.Inf(-1), math.Inf(-1)
	for _, point := range points {
		minimumX, minimumY = math.Min(minimumX, point.X), math.Min(minimumY, point.Y)
		maximumX, maximumY = math.Max(maximumX, point.X), math.Max(maximumY, point.Y)
	}
	minimumX = math.Max(minimumX, float64(bounds.Min.X))
	minimumY = math.Max(minimumY, float64(bounds.Min.Y))
	maximumX = math.Min(maximumX, float64(bounds.Max.X))
	maximumY = math.Min(maximumY, float64(bounds.Max.Y))
	if maximumX <= minimumX || maximumY <= minimumY {
		return image.Rectangle{}
	}
	floor := func(value float64, lower, upper int) int {
		if value <= float64(lower) {
			return lower
		}
		if value >= float64(upper) {
			return upper
		}
		return int(math.Floor(value))
	}
	ceil := func(value float64, lower, upper int) int {
		if value <= float64(lower) {
			return lower
		}
		if value >= float64(upper) {
			return upper
		}
		return int(math.Ceil(value))
	}
	return image.Rect(
		floor(minimumX, bounds.Min.X, bounds.Max.X),
		floor(minimumY, bounds.Min.Y, bounds.Max.Y),
		ceil(maximumX, bounds.Min.X, bounds.Max.X),
		ceil(maximumY, bounds.Min.Y, bounds.Max.Y),
	).Intersect(bounds)
}

func pixelBoundsForContours(bounds image.Rectangle, contours []contour) image.Rectangle {
	result := image.Rectangle{}
	for _, contour := range contours {
		contourBounds := pixelBoundsForPoints(bounds, contour.points)
		if contourBounds.Empty() {
			continue
		}
		if result.Empty() {
			result = contourBounds
		} else {
			result = result.Union(contourBounds)
		}
	}
	return result
}
