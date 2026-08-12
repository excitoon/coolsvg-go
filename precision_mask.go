package coolsvg

import (
	"fmt"
	"image"
	"math"
	"slices"
	"sort"
)

// coverageMask retains fractional coverage until destination quantization.
type coverageMask struct {
	bounds image.Rectangle
	values []float64
}

type precisionSampleCell struct {
	y          int
	domain     *sampleMask
	partitions *precisionPartitions
	atomic     bool
}

const (
	maxPrecisionPartitionEndpoints  = 2_000_000
	maxPrecisionCellPartitions      = 1_024
	precisionPartitionRowBytes      = 32
	precisionPartitionEndpointBytes = 16
)

// precisionPartitions records every horizontal coverage transition seen while
// discovering a sampled scene. Replaying the scene over the resulting atomic
// intervals keeps sibling source-over operations spatially correlated instead
// of compositing their independently averaged pixel coverages.
type precisionPartitions struct {
	bounds    image.Rectangle
	rows      [][]float64
	parent    *precisionPartitions
	work      *precisionPartitionWorkBudget
	auxiliary *precisionAuxiliaryBudget
	err       error
}

type precisionPartitionWorkBudget struct{ endpoints int }

func newPrecisionPartitions(bounds image.Rectangle, parent *precisionPartitions, work *precisionPartitionWorkBudget, auxiliary *precisionAuxiliaryBudget) *precisionPartitions {
	partitions := &precisionPartitions{
		bounds:    bounds,
		parent:    parent,
		work:      work,
		auxiliary: auxiliary,
	}
	height := bounds.Dy()
	if height < 0 || uint64(height) > ^uint64(0)/precisionPartitionRowBytes {
		partitions.err = fmt.Errorf("svg: sampled coverage partition row storage overflows")
		return partitions
	}
	if err := auxiliary.reserve(uint64(height) * precisionPartitionRowBytes); err != nil {
		partitions.err = err
		return partitions
	}
	partitions.rows = make([][]float64, height)
	return partitions
}

func (p *precisionPartitions) add(py int, left, right float64) {
	if p == nil {
		return
	}
	p.addLocal(py, left)
	p.addLocal(py, right)
	if p.parent != nil {
		p.parent.add(py, left, right)
	}
}

func (p *precisionPartitions) addLocal(py int, value float64) {
	if p.err != nil || py < p.bounds.Min.Y || py >= p.bounds.Max.Y || math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	// Pixel boundaries already delimit independently accumulated cells, so they
	// do not create atomic subsegments and must not consume the endpoint budget.
	if value <= float64(p.bounds.Min.X) || value >= float64(p.bounds.Max.X) || value == math.Trunc(value) {
		return
	}
	if p.work == nil {
		p.work = &precisionPartitionWorkBudget{}
	}
	if p.work.endpoints >= maxPrecisionPartitionEndpoints {
		p.err = fmt.Errorf("svg: sampled coverage partitioning exceeds %d endpoints per render", maxPrecisionPartitionEndpoints)
		return
	}
	if err := p.auxiliary.reserve(precisionPartitionEndpointBytes); err != nil {
		p.err = err
		return
	}
	row := py - p.bounds.Min.Y
	p.rows[row] = append(p.rows[row], value)
	p.work.endpoints++
}

func (p *precisionPartitions) finalize() error {
	if p == nil {
		return nil
	}
	if p.err != nil {
		return p.err
	}
	for row := range p.rows {
		values := p.rows[row]
		slices.Sort(values)
		unique := values[:0]
		for _, value := range values {
			if len(unique) == 0 || value != unique[len(unique)-1] {
				unique = append(unique, value)
			}
		}
		p.rows[row] = unique

		cell, count := 0, 0
		for _, value := range unique {
			if value <= float64(p.bounds.Min.X) || value >= float64(p.bounds.Max.X) || value == math.Trunc(value) {
				continue
			}
			current := int(math.Floor(value))
			if count == 0 || current != cell {
				cell, count = current, 1
			} else {
				count++
			}
			if count > maxPrecisionCellPartitions {
				return fmt.Errorf("svg: sampled coverage partitioning exceeds %d boundaries in one pixel", maxPrecisionCellPartitions)
			}
		}
	}
	return nil
}

func (p *precisionPartitions) row(py int) []float64 {
	if p == nil || py < p.bounds.Min.Y || py >= p.bounds.Max.Y {
		return nil
	}
	return p.rows[py-p.bounds.Min.Y]
}

func newCoverageMask(bounds image.Rectangle) *coverageMask {
	return &coverageMask{bounds: bounds, values: make([]float64, bounds.Dx()*bounds.Dy())}
}

func acquireCoverageMask(owner *renderer, bounds image.Rectangle) *coverageMask {
	if owner != nil {
		if count := len(owner.coveragePool); count > 0 {
			mask := owner.coveragePool[count-1]
			owner.coveragePool = owner.coveragePool[:count-1]
			length := bounds.Dx() * bounds.Dy()
			if cap(mask.values) < length {
				mask.values = make([]float64, length)
			} else {
				mask.values = mask.values[:length]
				clear(mask.values)
			}
			mask.bounds = bounds
			return mask
		}
	}
	return newCoverageMask(bounds)
}

func releaseCoverageMask(owner *renderer, mask *coverageMask) {
	if owner != nil && mask != nil {
		owner.coveragePool = append(owner.coveragePool, mask)
	}
}

func (m *coverageMask) offset(x, y int) int {
	return (y-m.bounds.Min.Y)*m.bounds.Dx() + x - m.bounds.Min.X
}

func unionCoverageMask(dst, src *coverageMask) {
	if dst == nil || src == nil {
		return
	}
	bounds := dst.bounds.Intersect(src.bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			destination := dst.offset(x, y)
			value := src.values[src.offset(x, y)]
			if value > dst.values[destination] {
				dst.values[destination] = value
			}
		}
	}
}

// rasterizeContoursPrecisionClipped intersects analytic fill and clip
// intervals at each vertical subscanline before either is averaged. A cell
// pass normalizes the result to its exact horizontal clip segment.
func rasterizeContoursPrecisionClipped(bounds image.Rectangle, contours []contour, rule fillRule, clip *sampleMask, cell *precisionSampleCell, budget *flattenBudget, owner *renderer) (*coverageMask, error) {
	if bounds.Empty() || len(contours) == 0 {
		return acquireCoverageMask(owner, bounds), nil
	}
	type edge struct {
		a, b Point
	}
	var edges []edge
	for _, c := range contours {
		if len(c.points) < 2 {
			continue
		}
		for _, point := range c.points {
			if !finitePoint(point) {
				return nil, fmt.Errorf("svg: raster path contains non-finite coordinates")
			}
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
		return acquireCoverageMask(owner, bounds), nil
	}
	samples := antialiasSamples
	if cell != nil {
		samples = 1
	}
	if err := budget.spendRaster(len(edges), bounds.Dx(), bounds.Dy(), samples); err != nil {
		return nil, err
	}

	mask := acquireCoverageMask(owner, bounds)
	width := bounds.Dx()
	coverage := make([]float64, width)
	crossings := make([]crossing, 0, len(edges))
	for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
		clear(coverage)
		minimumSample, maximumSample := 0, antialiasSamples
		if cell != nil {
			if cell.y < 0 || cell.y >= antialiasSamples {
				return mask, nil
			}
			minimumSample, maximumSample = cell.y, cell.y+1
		}
		for sample := minimumSample; sample < maximumSample; sample++ {
			y := float64(py) + (float64(sample)+0.5)/antialiasSamples
			crossings = crossings[:0]
			for _, e := range edges {
				minimum, maximum := e.a.Y, e.b.Y
				winding := 1
				if minimum > maximum {
					minimum, maximum = maximum, minimum
					winding = -1
				}
				if y < minimum || y >= maximum {
					continue
				}
				x := e.a.X + (y-e.a.Y)*(e.b.X-e.a.X)/(e.b.Y-e.a.Y)
				crossings = append(crossings, crossing{x: x, winding: winding})
			}
			slices.SortFunc(crossings, compareCrossings)
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
						addPrecisionCoverage(coverage, bounds.Min.X, py, sample, start, x, clip, cell)
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
						addPrecisionCoverage(coverage, bounds.Min.X, py, sample, start, x, clip, cell)
					}
				}
			}
		}
		for px, value := range coverage {
			x := px + bounds.Min.X
			if cell == nil {
				mask.values[mask.offset(x, py)] = math.Min(1, value/antialiasSamples)
				continue
			}
			segmentWidth := 1.0
			if clip != nil {
				segmentWidth = sampleIntervalsCoverage(clip.row(py, cell.y), float64(x), float64(x+1))
			}
			if segmentWidth > 0 {
				mask.values[mask.offset(x, py)] = math.Min(1, value/segmentWidth)
			}
		}
	}
	if cell != nil && cell.partitions != nil && cell.partitions.err != nil {
		releaseCoverageMask(owner, mask)
		return nil, cell.partitions.err
	}
	return mask, nil
}

// coverageFromSampleMask intersects cached exact geometry spans with the
// current sampled domain. Geometry masks are immutable and reusable across the
// discovery pass and every atomic replay.
func coverageFromSampleMask(bounds image.Rectangle, geometry, clip *sampleMask, cell *precisionSampleCell, budget *flattenBudget, owner *renderer) (*coverageMask, error) {
	if geometry == nil || bounds.Empty() || len(geometry.intervals) == 0 {
		return nil, nil
	}
	if geometry.err != nil {
		return nil, geometry.err
	}
	if clip != nil && clip.err != nil {
		return nil, clip.err
	}
	samples := antialiasSamples
	minimumSample, maximumSample := 0, antialiasSamples
	if cell != nil {
		samples = 1
		if cell.y < 0 || cell.y >= antialiasSamples {
			return nil, nil
		}
		minimumSample, maximumSample = cell.y, cell.y+1
	}
	coverageBounds := sampleMaskSelectedBounds(bounds, geometry, minimumSample, maximumSample)
	if clip != nil {
		coverageBounds = sampleMasksIntersectionBounds(bounds, geometry, clip, minimumSample, maximumSample)
	}
	if coverageBounds.Empty() {
		return nil, nil
	}
	if err := budget.spendRaster(0, coverageBounds.Dx(), coverageBounds.Dy(), samples); err != nil {
		return nil, err
	}

	mask := acquireCoverageMask(owner, coverageBounds)
	width := coverageBounds.Dx()
	minimumX, maximumX := float64(coverageBounds.Min.X), float64(coverageBounds.Max.X)
	for py := coverageBounds.Min.Y; py < coverageBounds.Max.Y; py++ {
		rowStart := (py - coverageBounds.Min.Y) * width
		coverage := mask.values[rowStart : rowStart+width]
		for sy := minimumSample; sy < maximumSample; sy++ {
			geometryIntervals := geometry.row(py, sy)
			if clip == nil {
				for _, interval := range geometryIntervals {
					left := math.Max(minimumX, interval.left)
					right := math.Min(maximumX, interval.right)
					if right <= left {
						continue
					}
					if cell != nil && cell.partitions != nil {
						cell.partitions.add(py, left, right)
					}
					addCoverage(coverage, coverageBounds.Min.X, left, right)
				}
				continue
			}

			clipIntervals := clip.row(py, sy)
			for geometryIndex, clipIndex := 0, 0; geometryIndex < len(geometryIntervals) && clipIndex < len(clipIntervals); {
				geometryInterval := geometryIntervals[geometryIndex]
				clipInterval := clipIntervals[clipIndex]
				left := math.Max(minimumX, math.Max(geometryInterval.left, clipInterval.left))
				right := math.Min(maximumX, math.Min(geometryInterval.right, clipInterval.right))
				if right > left {
					if cell != nil && cell.partitions != nil {
						cell.partitions.add(py, left, right)
					}
					addCoverage(coverage, coverageBounds.Min.X, left, right)
				}
				if geometryInterval.right <= clipInterval.right {
					geometryIndex++
				}
				if clipInterval.right <= geometryInterval.right {
					clipIndex++
				}
			}
		}
		for px, value := range coverage {
			x := px + coverageBounds.Min.X
			if cell == nil {
				coverage[px] = math.Min(1, value/antialiasSamples)
				continue
			}
			segmentWidth := 1.0
			if clip != nil {
				segmentWidth = sampleIntervalsCoverage(clip.row(py, cell.y), float64(x), float64(x+1))
			}
			if segmentWidth > 0 {
				coverage[px] = math.Min(1, value/segmentWidth)
			}
		}
	}
	if cell != nil && cell.partitions != nil && cell.partitions.err != nil {
		releaseCoverageMask(owner, mask)
		return nil, cell.partitions.err
	}
	return mask, nil
}

func sampleMasksOverlap(bounds image.Rectangle, left, right *sampleMask, minimumSample, maximumSample int) bool {
	return !sampleMasksIntersectionBounds(bounds, left, right, minimumSample, maximumSample).Empty()
}

func sampleMaskSelectedBounds(bounds image.Rectangle, mask *sampleMask, minimumSample, maximumSample int) image.Rectangle {
	if mask == nil || bounds.Empty() {
		return image.Rectangle{}
	}
	minimumSample = max(0, minimumSample)
	maximumSample = min(antialiasSamples, maximumSample)
	result := image.Rectangle{}
	found := false
	for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
		for sy := minimumSample; sy < maximumSample; sy++ {
			for _, interval := range mask.row(py, sy) {
				includeSampleIntervalBounds(&result, &found, bounds, py, interval.left, interval.right)
			}
		}
	}
	return result
}

func sampleMasksIntersectionBounds(bounds image.Rectangle, left, right *sampleMask, minimumSample, maximumSample int) image.Rectangle {
	if left == nil || right == nil || bounds.Empty() {
		return image.Rectangle{}
	}
	minimumSample = max(0, minimumSample)
	maximumSample = min(antialiasSamples, maximumSample)
	result := image.Rectangle{}
	found := false
	for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
		for sy := minimumSample; sy < maximumSample; sy++ {
			leftIntervals := left.row(py, sy)
			rightIntervals := right.row(py, sy)
			for leftIndex, rightIndex := 0, 0; leftIndex < len(leftIntervals) && rightIndex < len(rightIntervals); {
				leftInterval := leftIntervals[leftIndex]
				rightInterval := rightIntervals[rightIndex]
				minimum := math.Max(float64(bounds.Min.X), math.Max(leftInterval.left, rightInterval.left))
				maximum := math.Min(float64(bounds.Max.X), math.Min(leftInterval.right, rightInterval.right))
				if maximum > minimum {
					includeSampleIntervalBounds(&result, &found, bounds, py, minimum, maximum)
				}
				if leftInterval.right <= rightInterval.right {
					leftIndex++
				}
				if rightInterval.right <= leftInterval.right {
					rightIndex++
				}
			}
		}
	}
	return result
}

func includeSampleIntervalBounds(result *image.Rectangle, found *bool, bounds image.Rectangle, py int, left, right float64) {
	minimum := max(bounds.Min.X, int(math.Floor(math.Max(float64(bounds.Min.X), left))))
	maximum := min(bounds.Max.X, int(math.Ceil(math.Min(float64(bounds.Max.X), right))))
	if maximum <= minimum {
		return
	}
	intervalBounds := image.Rect(minimum, py, maximum, py+1)
	if !*found {
		*result = intervalBounds
		*found = true
		return
	}
	*result = result.Union(intervalBounds)
}

func addPrecisionCoverage(row []float64, origin, py, sy int, left, right float64, clip *sampleMask, cell *precisionSampleCell) {
	if clip == nil && cell == nil {
		addCoverage(row, origin, left, right)
		return
	}
	if right < left {
		left, right = right, left
	}
	if sy < 0 || sy >= antialiasSamples {
		return
	}
	minimum := math.Max(left, float64(origin))
	maximum := math.Min(right, float64(origin)+float64(len(row)))
	if maximum <= minimum {
		return
	}
	add := func(clippedLeft, clippedRight float64) {
		if cell != nil && cell.partitions != nil {
			cell.partitions.add(py, clippedLeft, clippedRight)
		}
		addCoverage(row, origin, clippedLeft, clippedRight)
	}
	if clip == nil {
		add(minimum, maximum)
		return
	}
	for _, interval := range overlappingSampleIntervals(clip.row(py, sy), minimum, maximum) {
		clippedLeft := math.Max(minimum, interval.left)
		clippedRight := math.Min(maximum, interval.right)
		if clippedRight > clippedLeft {
			add(clippedLeft, clippedRight)
		}
	}
}

func splitPrecisionClip(mask *sampleMask) (interior, boundary *sampleMask, err error) {
	if mask == nil {
		return nil, nil, nil
	}
	if mask.err != nil {
		return nil, nil, mask.err
	}
	const allSamples uint16 = 0xffff
	for index, samples := range mask.samples {
		if samples == 0 {
			continue
		}
		x := mask.bounds.Min.X + index%mask.bounds.Dx()
		y := mask.bounds.Min.Y + index/mask.bounds.Dx()
		fullyCovered := samples == allSamples
		if fullyCovered {
			for sy := 0; sy < antialiasSamples; sy++ {
				if sampleIntervalsCoverage(mask.row(y, sy), float64(x), float64(x+1)) < 1-1e-12 {
					fullyCovered = false
					break
				}
			}
		}
		if fullyCovered {
			if interior == nil {
				interior = newBudgetedSampleMask(mask.bounds, mask.budget)
			}
			for sy := 0; sy < antialiasSamples; sy++ {
				addSampleInterval(interior, y, sy, float64(x), float64(x+1))
			}
			continue
		}
		if boundary == nil {
			boundary = newBudgetedSampleMask(mask.bounds, mask.budget)
		}
		for sy := 0; sy < antialiasSamples; sy++ {
			for _, interval := range overlappingSampleIntervals(mask.row(y, sy), float64(x), float64(x+1)) {
				addSampleInterval(boundary, y, sy, math.Max(float64(x), interval.left), math.Min(float64(x+1), interval.right))
			}
		}
	}
	if boundary != nil {
		hasBoundary := false
		for _, samples := range boundary.samples {
			if samples != 0 {
				hasBoundary = true
				break
			}
		}
		if !hasBoundary {
			boundary = nil
		}
	}
	if interior != nil && interior.err != nil {
		return nil, nil, interior.err
	}
	if boundary != nil && boundary.err != nil {
		return nil, nil, boundary.err
	}
	return interior, boundary, nil
}

func sampleIntervalsCoverage(intervals []sampleInterval, left, right float64) float64 {
	coverage := 0.0
	for _, interval := range overlappingSampleIntervals(intervals, left, right) {
		coverage += math.Min(right, interval.right) - math.Max(left, interval.left)
	}
	return coverage
}

func selectPrecisionClipSegment(dst, source *sampleMask, sy, ordinal int) bool {
	if dst == nil || source == nil || dst.bounds != source.bounds || sy < 0 || sy >= antialiasSamples || ordinal < 0 {
		return false
	}
	clearSampleMask(dst)
	found := false
	width := source.bounds.Dx()
	for index, samples := range source.samples {
		if samples == 0 {
			continue
		}
		x := source.bounds.Min.X + index%width
		y := source.bounds.Min.Y + index/width
		intervals := overlappingSampleIntervals(source.row(y, sy), float64(x), float64(x+1))
		if ordinal >= len(intervals) {
			continue
		}
		interval := intervals[ordinal]
		addSampleInterval(dst, y, sy, math.Max(float64(x), interval.left), math.Min(float64(x+1), interval.right))
		found = true
	}
	return found
}

// selectPrecisionPartitionSegment selects one atomic horizontal interval per
// pixel from source. The partition endpoints must have been finalized first.
func selectPrecisionPartitionSegment(dst, source *sampleMask, sy, ordinal int, partitions *precisionPartitions) bool {
	if dst == nil || source == nil || partitions == nil || dst.bounds != source.bounds || sy < 0 || sy >= antialiasSamples || ordinal < 0 {
		return false
	}
	clearSampleMask(dst)
	found := false
	width := source.bounds.Dx()
	for index, samples := range source.samples {
		if samples == 0 {
			continue
		}
		x := source.bounds.Min.X + index%width
		y := source.bounds.Min.Y + index/width
		points := partitions.row(y)
		sourceIntervals := overlappingSampleIntervals(source.row(y, sy), float64(x), float64(x+1))
		if precisionPartitionPieceCount(sourceIntervals, points, float64(x), float64(x+1)) <= 1 {
			continue
		}
		piece := 0
		for _, interval := range sourceIntervals {
			left := math.Max(float64(x), interval.left)
			right := math.Min(float64(x+1), interval.right)
			if right <= left {
				continue
			}
			start := sort.Search(len(points), func(i int) bool { return points[i] > left })
			end := start
			for end < len(points) && points[end] < right {
				end++
			}
			previous := left
			for boundary := start; boundary <= end; boundary++ {
				next := right
				if boundary < end {
					next = points[boundary]
				}
				if next > previous {
					if piece == ordinal {
						addSampleInterval(dst, y, sy, previous, next)
						found = true
					}
					piece++
				}
				previous = next
			}
		}
	}
	return found
}

func selectPrecisionUnpartitionedSegment(dst, source *sampleMask, sy int, partitions *precisionPartitions) bool {
	if dst == nil || source == nil || partitions == nil || dst.bounds != source.bounds || sy < 0 || sy >= antialiasSamples {
		return false
	}
	clearSampleMask(dst)
	found := false
	width := source.bounds.Dx()
	for index, samples := range source.samples {
		if samples == 0 {
			continue
		}
		x := source.bounds.Min.X + index%width
		y := source.bounds.Min.Y + index/width
		points := partitions.row(y)
		intervals := overlappingSampleIntervals(source.row(y, sy), float64(x), float64(x+1))
		if precisionPartitionPieceCount(intervals, points, float64(x), float64(x+1)) != 1 {
			continue
		}
		for _, interval := range intervals {
			addSampleInterval(dst, y, sy, math.Max(float64(x), interval.left), math.Min(float64(x+1), interval.right))
			found = true
		}
	}
	return found
}

func precisionPartitionPieceCount(intervals []sampleInterval, points []float64, left, right float64) int {
	count := 0
	for _, interval := range intervals {
		minimum := math.Max(left, interval.left)
		maximum := math.Min(right, interval.right)
		if maximum <= minimum {
			continue
		}
		start := sort.Search(len(points), func(i int) bool { return points[i] > minimum })
		end := start
		for end < len(points) && points[end] < maximum {
			end++
		}
		count += end - start + 1
	}
	return count
}
