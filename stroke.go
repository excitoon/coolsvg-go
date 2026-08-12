package coolsvg

import (
	"math"
)

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
