package coolsvg

import (
	"image"
	"math"
)

func rasterizeStroke(bounds image.Rectangle, path *Path, ctm Matrix, style computedStyle) *image.Alpha {
	result := image.NewAlpha(bounds)
	half := style.strokeWidth / 2
	if half <= 0 {
		return result
	}
	for _, source := range path.flatten(identityMatrix(), 0.1) {
		pieces := []contour{source}
		if len(style.dashArray) > 0 {
			pieces = dashContours(source, style.dashArray, style.dashOffset)
		}
		for _, piece := range pieces {
			strokeContour(bounds, result, piece, ctm, half, style)
		}
	}
	return result
}

func strokeContour(bounds image.Rectangle, dst *image.Alpha, c contour, ctm Matrix, half float64, style computedStyle) {
	points := append([]Point(nil), c.points...)
	if len(points) < 2 {
		return
	}
	closed := c.closed
	if closed && almostEqual(points[0], points[len(points)-1]) {
		points = points[:len(points)-1]
	}
	if len(points) < 2 {
		return
	}
	if !closed && style.strokeCap == "square" {
		firstDirection, ok := unitDirection(points[0], points[1])
		if ok {
			points[0] = sub(points[0], mul(firstDirection, half))
		}
		last := len(points) - 1
		lastDirection, ok := unitDirection(points[last-1], points[last])
		if ok {
			points[last] = add(points[last], mul(lastDirection, half))
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
		addStrokePrimitive(bounds, dst, ctm, []Point{
			add(a, normal), add(b, normal), sub(b, normal), sub(a, normal),
		})
	}
	joinStart, joinEnd := 1, len(points)-1
	if closed {
		joinStart, joinEnd = 0, len(points)
	}
	for i := joinStart; i < joinEnd; i++ {
		previous := points[(i-1+len(points))%len(points)]
		joint := points[i%len(points)]
		next := points[(i+1)%len(points)]
		addStrokeJoin(bounds, dst, ctm, previous, joint, next, half, style)
	}
	if !closed && style.strokeCap == "round" {
		addStrokeCircle(bounds, dst, ctm, points[0], half)
		addStrokeCircle(bounds, dst, ctm, points[len(points)-1], half)
	}
}

func addStrokeJoin(bounds image.Rectangle, dst *image.Alpha, ctm Matrix, previous, joint, next Point, half float64, style computedStyle) {
	before, okBefore := unitDirection(previous, joint)
	after, okAfter := unitDirection(joint, next)
	if !okBefore || !okAfter {
		return
	}
	cross := before.X*after.Y - before.Y*after.X
	if math.Abs(cross) < 1e-12 {
		return
	}
	if style.strokeJoin == "round" || style.strokeJoin == "arcs" {
		addStrokeCircle(bounds, dst, ctm, joint, half)
		return
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
			addStrokePrimitive(bounds, dst, ctm, []Point{joint, a, intersection, b})
			return
		}
	}
	addStrokePrimitive(bounds, dst, ctm, []Point{joint, a, b})
}

func addStrokeCircle(bounds image.Rectangle, dst *image.Alpha, ctm Matrix, center Point, radius float64) {
	const sides = 32
	points := make([]Point, sides)
	for i := range points {
		angle := float64(i) * 2 * math.Pi / sides
		points[i] = Point{center.X + math.Cos(angle)*radius, center.Y + math.Sin(angle)*radius}
	}
	addStrokePrimitive(bounds, dst, ctm, points)
}

func addStrokePrimitive(bounds image.Rectangle, dst *image.Alpha, ctm Matrix, points []Point) {
	if len(points) < 3 {
		return
	}
	transformed := make([]Point, len(points)+1)
	for i, point := range points {
		transformed[i] = ctm.Apply(point)
	}
	transformed[len(points)] = transformed[0]
	mask := rasterizeContours(bounds, []contour{{points: transformed, closed: true}}, nonZero)
	unionMask(dst, mask)
}

func unitDirection(a, b Point) (Point, bool) {
	dx, dy := b.X-a.X, b.Y-a.Y
	length := math.Hypot(dx, dy)
	if length < 1e-15 {
		return Point{}, false
	}
	return Point{dx / length, dy / length}, true
}

func lineIntersection(a, directionA, b, directionB Point) (Point, bool) {
	det := directionA.X*directionB.Y - directionA.Y*directionB.X
	if math.Abs(det) < 1e-12 {
		return Point{}, false
	}
	delta := sub(b, a)
	t := (delta.X*directionB.Y - delta.Y*directionB.X) / det
	return add(a, mul(directionA, t)), true
}

func dashContours(source contour, pattern []float64, offset float64) []contour {
	if len(source.points) < 2 || len(pattern) == 0 {
		return []contour{source}
	}
	total := 0.0
	for _, value := range pattern {
		total += value
	}
	if total <= 0 {
		return []contour{source}
	}
	offset = math.Mod(offset, total)
	if offset < 0 {
		offset += total
	}
	index := 0
	for offset >= pattern[index] && pattern[index] > 0 {
		offset -= pattern[index]
		index = (index + 1) % len(pattern)
	}
	remaining := pattern[index] - offset
	on := index%2 == 0
	var output []contour
	var current []Point
	flush := func() {
		if len(current) > 1 {
			output = append(output, contour{points: current})
		}
		current = nil
	}
	points := source.points
	if source.closed && !almostEqual(points[0], points[len(points)-1]) {
		points = append(append([]Point(nil), points...), points[0])
	}
	for segment := 1; segment < len(points); segment++ {
		a, b := points[segment-1], points[segment]
		direction, ok := unitDirection(a, b)
		if !ok {
			continue
		}
		length := math.Hypot(b.X-a.X, b.Y-a.Y)
		position := 0.0
		for position < length-1e-12 {
			if remaining <= 1e-12 {
				if on {
					flush()
				}
				index = (index + 1) % len(pattern)
				on = index%2 == 0
				remaining = pattern[index]
				if remaining == 0 {
					continue
				}
			}
			step := math.Min(remaining, length-position)
			start := add(a, mul(direction, position))
			end := add(a, mul(direction, position+step))
			if on {
				if len(current) == 0 {
					current = append(current, start)
				}
				current = append(current, end)
			}
			position += step
			remaining -= step
		}
	}
	flush()
	return output
}
