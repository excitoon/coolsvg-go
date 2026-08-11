package coolsvg

import (
	"image"
	"math"
	"math/bits"
	"sort"
)

const antialiasSamples = 4

type crossing struct {
	x       float64
	winding int
}

// sampleMask keeps clip coverage as subpixel membership rather than a single
// averaged alpha value. Sibling clip geometry can therefore use exact OR and
// nested clips exact AND before coverage is downsampled.
type sampleMask struct {
	bounds  image.Rectangle
	samples []uint16
}

func newSampleMask(bounds image.Rectangle) *sampleMask {
	return &sampleMask{bounds: bounds, samples: make([]uint16, bounds.Dx()*bounds.Dy())}
}

func (m *sampleMask) alpha() *image.Alpha {
	alpha := image.NewAlpha(m.bounds)
	for y := m.bounds.Min.Y; y < m.bounds.Max.Y; y++ {
		for x := m.bounds.Min.X; x < m.bounds.Max.X; x++ {
			value := m.samples[(y-m.bounds.Min.Y)*m.bounds.Dx()+x-m.bounds.Min.X]
			coverage := bits.OnesCount16(value)
			alpha.Pix[alpha.PixOffset(x, y)] = uint8((coverage*255 + antialiasSamples*antialiasSamples/2) / (antialiasSamples * antialiasSamples))
		}
	}
	return alpha
}

func unionSamples(dst, src *sampleMask) {
	if dst == nil || src == nil || dst.bounds != src.bounds {
		return
	}
	for i := range dst.samples {
		dst.samples[i] |= src.samples[i]
	}
}

func intersectSamples(dst, src *sampleMask) {
	if dst == nil {
		return
	}
	if src == nil || dst.bounds != src.bounds {
		clear(dst.samples)
		return
	}
	for i := range dst.samples {
		dst.samples[i] &= src.samples[i]
	}
}

func rasterizeClipContours(bounds image.Rectangle, contours []contour, rule fillRule) *sampleMask {
	mask := newSampleMask(bounds)
	if bounds.Empty() || len(contours) == 0 {
		return mask
	}
	type edge struct{ a, b Point }
	var edges []edge
	for _, contour := range contours {
		if len(contour.points) < 2 {
			continue
		}
		for i := 1; i < len(contour.points); i++ {
			if contour.points[i-1].Y != contour.points[i].Y {
				edges = append(edges, edge{contour.points[i-1], contour.points[i]})
			}
		}
		if !almostEqual(contour.points[0], contour.points[len(contour.points)-1]) && contour.points[0].Y != contour.points[len(contour.points)-1].Y {
			edges = append(edges, edge{contour.points[len(contour.points)-1], contour.points[0]})
		}
	}
	crossings := make([]crossing, 0, len(edges))
	for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
		for sy := 0; sy < antialiasSamples; sy++ {
			y := float64(py) + (float64(sy)+0.5)/antialiasSamples
			crossings = crossings[:0]
			for _, edge := range edges {
				minimum, maximum, winding := edge.a.Y, edge.b.Y, 1
				if minimum > maximum {
					minimum, maximum, winding = maximum, minimum, -1
				}
				if y < minimum || y >= maximum {
					continue
				}
				x := edge.a.X + (y-edge.a.Y)*(edge.b.X-edge.a.X)/(edge.b.Y-edge.a.Y)
				crossings = append(crossings, crossing{x: x, winding: winding})
			}
			sort.Slice(crossings, func(i, j int) bool { return crossings[i].x < crossings[j].x })
			addInterval := func(left, right float64) { addSampleInterval(mask, py, sy, left, right) }
			if rule == evenOdd {
				inside, start := false, 0.0
				for i := 0; i < len(crossings); {
					x, count := crossings[i].x, 0
					for i < len(crossings) && math.Abs(crossings[i].x-x) < 1e-12 {
						count++
						i++
					}
					if count%2 == 0 {
						continue
					}
					if inside {
						addInterval(start, x)
					}
					inside, start = !inside, x
				}
			} else {
				winding, start := 0, 0.0
				for i := 0; i < len(crossings); {
					x, delta := crossings[i].x, 0
					for i < len(crossings) && math.Abs(crossings[i].x-x) < 1e-12 {
						delta += crossings[i].winding
						i++
					}
					wasInside := winding != 0
					winding += delta
					isInside := winding != 0
					if !wasInside && isInside {
						start = x
					} else if wasInside && !isInside {
						addInterval(start, x)
					}
				}
			}
		}
	}
	return mask
}

func addSampleInterval(mask *sampleMask, py, sy int, left, right float64) {
	if right < left {
		left, right = right, left
	}
	start := int(math.Ceil((left-float64(mask.bounds.Min.X))*antialiasSamples - 0.5))
	end := int(math.Ceil((right-float64(mask.bounds.Min.X))*antialiasSamples - 0.5))
	start = max(0, start)
	end = min(mask.bounds.Dx()*antialiasSamples, end)
	row := (py - mask.bounds.Min.Y) * mask.bounds.Dx()
	for subpixel := start; subpixel < end; subpixel++ {
		px, sx := subpixel/antialiasSamples, subpixel%antialiasSamples
		bit := uint(sy*antialiasSamples + sx)
		mask.samples[row+px] |= uint16(1) << bit
	}
}

func rasterizeContours(bounds image.Rectangle, contours []contour, rule fillRule) *image.Alpha {
	mask := image.NewAlpha(bounds)
	if bounds.Empty() || len(contours) == 0 {
		return mask
	}
	type edge struct {
		a, b Point
	}
	var edges []edge
	for _, c := range contours {
		if len(c.points) < 2 {
			continue
		}
		for i := 1; i < len(c.points); i++ {
			if c.points[i-1].Y != c.points[i].Y {
				edges = append(edges, edge{a: c.points[i-1], b: c.points[i]})
			}
		}
		if !almostEqual(c.points[0], c.points[len(c.points)-1]) && c.points[0].Y != c.points[len(c.points)-1].Y {
			edges = append(edges, edge{a: c.points[len(c.points)-1], b: c.points[0]})
		}
	}
	if len(edges) == 0 {
		return mask
	}

	width := bounds.Dx()
	coverage := make([]float64, width)
	crossings := make([]crossing, 0, len(edges))
	for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
		clear(coverage)
		for sample := 0; sample < antialiasSamples; sample++ {
			y := float64(py) + (float64(sample)+0.5)/antialiasSamples
			crossings = crossings[:0]
			for _, e := range edges {
				minimum, maximum := e.a.Y, e.b.Y
				winding := 1
				if minimum > maximum {
					minimum, maximum = maximum, minimum
					winding = -1
				}
				// The half-open test makes shared vertices participate once.
				if y < minimum || y >= maximum {
					continue
				}
				x := e.a.X + (y-e.a.Y)*(e.b.X-e.a.X)/(e.b.Y-e.a.Y)
				crossings = append(crossings, crossing{x: x, winding: winding})
			}
			sort.Slice(crossings, func(i, j int) bool { return crossings[i].x < crossings[j].x })
			if rule == evenOdd {
				inside := false
				start := 0.0
				for i := 0; i < len(crossings); {
					x := crossings[i].x
					count := 0
					for i < len(crossings) && math.Abs(crossings[i].x-x) < 1e-12 {
						count++
						i++
					}
					if count%2 == 0 {
						continue
					}
					if inside {
						addCoverage(coverage, bounds.Min.X, start, x)
					}
					inside = !inside
					start = x
				}
			} else {
				winding := 0
				start := 0.0
				for i := 0; i < len(crossings); {
					x := crossings[i].x
					delta := 0
					for i < len(crossings) && math.Abs(crossings[i].x-x) < 1e-12 {
						delta += crossings[i].winding
						i++
					}
					wasInside := winding != 0
					winding += delta
					isInside := winding != 0
					if !wasInside && isInside {
						start = x
					} else if wasInside && !isInside {
						addCoverage(coverage, bounds.Min.X, start, x)
					}
				}
			}
		}
		for px, value := range coverage {
			alpha := uint8(math.Round(math.Min(1, value/antialiasSamples) * 255))
			mask.Pix[mask.PixOffset(px+bounds.Min.X, py)] = alpha
		}
	}
	return mask
}

func addCoverage(row []float64, origin int, left, right float64) {
	if right < left {
		left, right = right, left
	}
	minimum := math.Max(left, float64(origin))
	maximum := math.Min(right, float64(origin+len(row)))
	if maximum <= minimum {
		return
	}
	first := int(math.Floor(minimum))
	last := int(math.Ceil(maximum))
	for x := first; x < last; x++ {
		overlap := math.Min(maximum, float64(x+1)) - math.Max(minimum, float64(x))
		if overlap > 0 {
			row[x-origin] += overlap
		}
	}
}

func unionMask(dst, src *image.Alpha) {
	if dst == nil || src == nil {
		return
	}
	bounds := dst.Bounds().Intersect(src.Bounds())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			di, si := dst.PixOffset(x, y), src.PixOffset(x, y)
			if src.Pix[si] > dst.Pix[di] {
				dst.Pix[di] = src.Pix[si]
			}
		}
	}
}

func intersectMask(dst, src *image.Alpha) {
	if dst == nil {
		return
	}
	if src == nil {
		clear(dst.Pix)
		return
	}
	bounds := dst.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			di := dst.PixOffset(x, y)
			if !image.Pt(x, y).In(src.Bounds()) {
				dst.Pix[di] = 0
				continue
			}
			si := src.PixOffset(x, y)
			dst.Pix[di] = uint8((uint16(dst.Pix[di])*uint16(src.Pix[si]) + 127) / 255)
		}
	}
}

func scaleMask(mask *image.Alpha, opacity float64) {
	if mask == nil || opacity >= 1 {
		return
	}
	opacity = math.Max(0, opacity)
	for i, alpha := range mask.Pix {
		mask.Pix[i] = uint8(math.Round(float64(alpha) * opacity))
	}
}
