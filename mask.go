package coolsvg

import (
	"fmt"
	"image"
	"math"
	"slices"
	"sort"
)

const antialiasSamples = 4

const (
	maxSampleMaskBytes         = 64 << 20
	maxPrecisionAuxiliaryBytes = 128 << 20
	sampleMaskRowBytes         = 8
	sampleMaskIntervalBytes    = 16
	sampleMaskSampleBytes      = 2
)

type crossing struct {
	x       float64
	winding int
}

// sampleMask keeps exact horizontal clip intervals at each vertical
// antialiasing sample. samples is a conservative 4x4-cell occupancy index used
// to accelerate plane selection; rows and intervals form compact CSR storage
// and are authoritative for Boolean geometry.
type sampleMask struct {
	bounds         image.Rectangle
	samples        []uint16
	rows           []sampleMaskRow
	intervals      []sampleInterval
	lastRow        int
	allocatedBytes uint64
	maximumBytes   uint64
	budget         *precisionAuxiliaryBudget
	allocationErr  error
	err            error
}

type sampleInterval struct {
	left  float64
	right float64
}

type sampleMaskRow struct {
	start uint32
	count uint32
}

// precisionAuxiliaryBudget monotonically bounds compact clip masks and sampled
// compositor partition storage allocated by one renderer before garbage
// collection can reclaim it.
type precisionAuxiliaryBudget struct {
	allocatedBytes uint64
}

func (b *precisionAuxiliaryBudget) reserve(bytes uint64) error {
	if b == nil || bytes == 0 {
		return nil
	}
	if b.allocatedBytes > maxPrecisionAuxiliaryBytes || bytes > maxPrecisionAuxiliaryBytes-b.allocatedBytes {
		return fmt.Errorf("svg: precision auxiliary storage exceeds %d MiB per render", maxPrecisionAuxiliaryBytes>>20)
	}
	b.allocatedBytes += bytes
	return nil
}

func newSampleMask(bounds image.Rectangle) *sampleMask {
	return newBudgetedSampleMask(bounds, nil)
}

func newBudgetedSampleMask(bounds image.Rectangle, budget *precisionAuxiliaryBudget) *sampleMask {
	mask := &sampleMask{
		bounds: bounds, lastRow: -1, maximumBytes: maxSampleMaskBytes, budget: budget,
	}
	if bounds.Empty() {
		return mask
	}
	pixels, rowCount, ok := sampleMaskCounts(bounds)
	if !ok {
		mask.failAllocation(fmt.Errorf("svg: sample mask bounds %v overflow", bounds))
		return mask
	}
	baseBytes, ok := sampleMaskBaseBytes(pixels, rowCount)
	if !ok {
		mask.failAllocation(fmt.Errorf("svg: sample mask storage size overflows for bounds %v", bounds))
		return mask
	}
	if !mask.reserve(baseBytes) {
		return mask
	}
	mask.samples = make([]uint16, pixels)
	mask.rows = make([]sampleMaskRow, rowCount)
	return mask
}

func sampleMaskCounts(bounds image.Rectangle) (pixels, rows int, ok bool) {
	if bounds.Min.X > bounds.Max.X || bounds.Min.Y > bounds.Max.Y {
		return 0, 0, false
	}
	maximum := uint(^uint(0) >> 1)
	width := uint(bounds.Max.X) - uint(bounds.Min.X)
	height := uint(bounds.Max.Y) - uint(bounds.Min.Y)
	if width > maximum || height > maximum || height > maximum/antialiasSamples {
		return 0, 0, false
	}
	if width != 0 && height > maximum/width {
		return 0, 0, false
	}
	return int(width * height), int(height * antialiasSamples), true
}

func sampleMaskBaseBytes(pixels, rows int) (uint64, bool) {
	if pixels < 0 || rows < 0 {
		return 0, false
	}
	maximum := ^uint64(0)
	if uint64(pixels) > maximum/sampleMaskSampleBytes || uint64(rows) > maximum/sampleMaskRowBytes {
		return 0, false
	}
	sampleBytes := uint64(pixels) * sampleMaskSampleBytes
	rowBytes := uint64(rows) * sampleMaskRowBytes
	if rowBytes > maximum-sampleBytes {
		return 0, false
	}
	return sampleBytes + rowBytes, true
}

func (m *sampleMask) reserve(bytes uint64) bool {
	if m == nil || m.err != nil {
		return false
	}
	if m.allocatedBytes > m.maximumBytes || bytes > m.maximumBytes-m.allocatedBytes {
		m.failAllocation(fmt.Errorf("svg: sample mask storage exceeds %d MiB", m.maximumBytes>>20))
		return false
	}
	if err := m.budget.reserve(bytes); err != nil {
		m.failAllocation(err)
		return false
	}
	m.allocatedBytes += bytes
	return true
}

func (m *sampleMask) failAllocation(err error) {
	if m == nil || err == nil || m.err != nil {
		return
	}
	m.err = err
	m.allocationErr = err
}

func (m *sampleMask) ensureIntervalCapacity(required int) bool {
	if m == nil || m.err != nil {
		return false
	}
	if required <= cap(m.intervals) {
		return true
	}
	capacity := cap(m.intervals) * 2
	if capacity < 8 {
		capacity = 8
	}
	if capacity < required {
		capacity = required
	}
	if uint64(capacity) > ^uint64(0)/sampleMaskIntervalBytes || !m.reserve(uint64(capacity)*sampleMaskIntervalBytes) {
		return false
	}
	intervals := make([]sampleInterval, len(m.intervals), capacity)
	copy(intervals, m.intervals)
	m.intervals = intervals
	return true
}

func (m *sampleMask) allocateIntervalReplacement(length int) ([]sampleInterval, bool) {
	if length == 0 {
		return nil, true
	}
	if length < 0 || uint64(length) > ^uint64(0)/sampleMaskIntervalBytes || !m.reserve(uint64(length)*sampleMaskIntervalBytes) {
		return nil, false
	}
	return make([]sampleInterval, 0, length), true
}

func (m *sampleMask) rowIndex(index int) []sampleInterval {
	if m == nil || index < 0 || index >= len(m.rows) {
		return nil
	}
	span := m.rows[index]
	start, end := int(span.start), int(span.start+span.count)
	if start < 0 || end < start || end > len(m.intervals) {
		return nil
	}
	return m.intervals[start:end:end]
}

func clearSampleMask(mask *sampleMask) {
	if mask == nil {
		return
	}
	clear(mask.samples)
	clear(mask.rows)
	mask.intervals = mask.intervals[:0]
	mask.lastRow = -1
	mask.err = mask.allocationErr
}

func cloneSampleMask(source *sampleMask, budget *precisionAuxiliaryBudget) (*sampleMask, error) {
	if source == nil {
		return nil, nil
	}
	if source.err != nil {
		return nil, source.err
	}
	clone := newBudgetedSampleMask(source.bounds, budget)
	if clone.err != nil {
		return nil, clone.err
	}
	if !clone.ensureIntervalCapacity(len(source.intervals)) {
		return nil, clone.err
	}
	copy(clone.samples, source.samples)
	copy(clone.rows, source.rows)
	clone.intervals = append(clone.intervals, source.intervals...)
	clone.lastRow = source.lastRow
	return clone, nil
}

func unionSamples(dst, src *sampleMask) {
	if dst == nil || src == nil || dst.bounds != src.bounds {
		return
	}
	if dst.err != nil {
		return
	}
	if src.err != nil {
		dst.err = src.err
		return
	}
	total := 0
	for row := range dst.rows {
		count := unionSampleIntervalCount(dst.rowIndex(row), src.rowIndex(row))
		if count > int(^uint(0)>>1)-total {
			dst.failAllocation(fmt.Errorf("svg: sample mask interval count overflows"))
			return
		}
		total += count
	}
	intervals, ok := dst.allocateIntervalReplacement(total)
	if !ok {
		return
	}
	lastRow := -1
	for row := range dst.rows {
		start := len(intervals)
		intervals = appendUnionSampleIntervals(intervals, dst.rowIndex(row), src.rowIndex(row))
		dst.rows[row] = sampleMaskRow{start: uint32(start), count: uint32(len(intervals) - start)}
		if len(intervals) > start {
			lastRow = row
		}
	}
	dst.intervals, dst.lastRow = intervals, lastRow
	maskOr(dst.samples, src.samples)
}

func appendUnionSampleIntervals(result, left, right []sampleInterval) []sampleInterval {
	base := len(result)
	appendInterval := func(interval sampleInterval) {
		if interval.right <= interval.left {
			return
		}
		if len(result) > base && interval.left <= result[len(result)-1].right {
			result[len(result)-1].right = math.Max(result[len(result)-1].right, interval.right)
			return
		}
		result = append(result, interval)
	}
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) || rightIndex < len(right); {
		if rightIndex >= len(right) || leftIndex < len(left) && left[leftIndex].left <= right[rightIndex].left {
			appendInterval(left[leftIndex])
			leftIndex++
		} else {
			appendInterval(right[rightIndex])
			rightIndex++
		}
	}
	return result
}

func unionSampleIntervalCount(left, right []sampleInterval) int {
	count := 0
	lastRight := 0.0
	appendInterval := func(interval sampleInterval) {
		if interval.right <= interval.left {
			return
		}
		if count > 0 && interval.left <= lastRight {
			lastRight = math.Max(lastRight, interval.right)
			return
		}
		count++
		lastRight = interval.right
	}
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) || rightIndex < len(right); {
		if rightIndex >= len(right) || leftIndex < len(left) && left[leftIndex].left <= right[rightIndex].left {
			appendInterval(left[leftIndex])
			leftIndex++
		} else {
			appendInterval(right[rightIndex])
			rightIndex++
		}
	}
	return count
}

func intersectSamples(dst, src *sampleMask) {
	if dst == nil {
		return
	}
	if src == nil || dst.bounds != src.bounds {
		clearSampleMask(dst)
		return
	}
	if dst.err != nil {
		return
	}
	if src.err != nil {
		dst.err = src.err
		return
	}
	total := 0
	for row := range dst.rows {
		count := intersectSampleIntervalCount(dst.rowIndex(row), src.rowIndex(row))
		if count > int(^uint(0)>>1)-total {
			dst.failAllocation(fmt.Errorf("svg: sample mask interval count overflows"))
			return
		}
		total += count
	}
	intervals, ok := dst.allocateIntervalReplacement(total)
	if !ok {
		return
	}
	lastRow := -1
	for row := range dst.rows {
		start := len(intervals)
		intervals = appendIntersectSampleIntervals(intervals, dst.rowIndex(row), src.rowIndex(row))
		dst.rows[row] = sampleMaskRow{start: uint32(start), count: uint32(len(intervals) - start)}
		if len(intervals) > start {
			lastRow = row
		}
	}
	dst.intervals, dst.lastRow = intervals, lastRow
	maskAnd(dst.samples, src.samples)
}

func appendIntersectSampleIntervals(result, left, right []sampleInterval) []sampleInterval {
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		minimum := math.Max(left[leftIndex].left, right[rightIndex].left)
		maximum := math.Min(left[leftIndex].right, right[rightIndex].right)
		if maximum > minimum {
			result = append(result, sampleInterval{left: minimum, right: maximum})
		}
		if left[leftIndex].right < right[rightIndex].right {
			leftIndex++
		} else {
			rightIndex++
		}
	}
	return result
}

func intersectSampleIntervalCount(left, right []sampleInterval) int {
	count := 0
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		if math.Min(left[leftIndex].right, right[rightIndex].right) > math.Max(left[leftIndex].left, right[rightIndex].left) {
			count++
		}
		if left[leftIndex].right < right[rightIndex].right {
			leftIndex++
		} else {
			rightIndex++
		}
	}
	return count
}

func (m *sampleMask) row(py, sy int) []sampleInterval {
	if m == nil || py < m.bounds.Min.Y || py >= m.bounds.Max.Y || sy < 0 || sy >= antialiasSamples {
		return nil
	}
	return m.rowIndex((py-m.bounds.Min.Y)*antialiasSamples + sy)
}

func overlappingSampleIntervals(intervals []sampleInterval, left, right float64) []sampleInterval {
	start := sort.Search(len(intervals), func(index int) bool { return intervals[index].right > left })
	end := start
	for end < len(intervals) && intervals[end].left < right {
		end++
	}
	return intervals[start:end]
}

func rasterizeClipContours(bounds image.Rectangle, contours []contour, rule fillRule, budget *flattenBudget, auxiliary *precisionAuxiliaryBudget) (*sampleMask, error) {
	if bounds.Empty() || len(contours) == 0 {
		mask := newBudgetedSampleMask(bounds, auxiliary)
		return mask, mask.err
	}
	type edge struct{ a, b Point }
	var edges []edge
	for _, contour := range contours {
		if len(contour.points) < 2 {
			continue
		}
		for _, point := range contour.points {
			if !finitePoint(point) {
				return nil, fmt.Errorf("svg: clip path contains non-finite coordinates")
			}
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
	if len(edges) == 0 {
		mask := newBudgetedSampleMask(bounds, auxiliary)
		return mask, mask.err
	}
	if err := budget.spendRaster(len(edges), bounds.Dx(), bounds.Dy(), antialiasSamples); err != nil {
		return nil, err
	}
	mask := newBudgetedSampleMask(bounds, auxiliary)
	if mask.err != nil {
		return nil, mask.err
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
			slices.SortFunc(crossings, compareCrossings)
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
	if mask.err != nil {
		return nil, mask.err
	}
	return mask, nil
}

func addSampleInterval(mask *sampleMask, py, sy int, left, right float64) {
	if mask == nil || mask.err != nil || py < mask.bounds.Min.Y || py >= mask.bounds.Max.Y || sy < 0 || sy >= antialiasSamples {
		return
	}
	if right < left {
		left, right = right, left
	}
	minimum := math.Max(left, float64(mask.bounds.Min.X))
	maximum := math.Min(right, float64(mask.bounds.Max.X))
	if maximum <= minimum {
		return
	}
	rowIndex := (py-mask.bounds.Min.Y)*antialiasSamples + sy
	intervals := mask.rowIndex(rowIndex)
	value := sampleInterval{left: minimum, right: maximum}
	if len(intervals) == 0 {
		if rowIndex > mask.lastRow {
			if !mask.ensureIntervalCapacity(len(mask.intervals) + 1) {
				return
			}
			start := len(mask.intervals)
			mask.intervals = append(mask.intervals, value)
			mask.rows[rowIndex] = sampleMaskRow{start: uint32(start), count: 1}
			mask.lastRow = rowIndex
		} else if !mask.replaceRowWithUnion(rowIndex, value) {
			return
		}
	} else if minimum >= intervals[len(intervals)-1].left {
		if minimum <= intervals[len(intervals)-1].right {
			intervals[len(intervals)-1].right = math.Max(intervals[len(intervals)-1].right, maximum)
		} else if int(mask.rows[rowIndex].start+mask.rows[rowIndex].count) == len(mask.intervals) {
			if !mask.ensureIntervalCapacity(len(mask.intervals) + 1) {
				return
			}
			mask.intervals = append(mask.intervals, value)
			mask.rows[rowIndex].count++
			mask.lastRow = max(mask.lastRow, rowIndex)
		} else if !mask.replaceRowWithUnion(rowIndex, value) {
			return
		}
	} else {
		// Callers naturally emit sorted intervals; retain a correct fallback for
		// any future out-of-order producer.
		if !mask.replaceRowWithUnion(rowIndex, value) {
			return
		}
	}
	start := int(math.Floor((minimum - float64(mask.bounds.Min.X)) * antialiasSamples))
	end := int(math.Ceil((maximum - float64(mask.bounds.Min.X)) * antialiasSamples))
	start = max(0, start)
	end = min(mask.bounds.Dx()*antialiasSamples, end)
	if start >= end {
		return
	}
	row := (py - mask.bounds.Min.Y) * mask.bounds.Dx()
	firstPixel, firstSubpixel := start/antialiasSamples, start%antialiasSamples
	lastPixel, lastSubpixel := (end-1)/antialiasSamples, (end-1)%antialiasSamples
	shift := uint(sy * antialiasSamples)
	if firstPixel == lastPixel {
		bits := uint16(1<<uint(lastSubpixel-firstSubpixel+1)) - 1
		mask.samples[row+firstPixel] |= bits << (shift + uint(firstSubpixel))
		return
	}
	firstBits := uint16(1<<uint(antialiasSamples-firstSubpixel)) - 1
	mask.samples[row+firstPixel] |= firstBits << (shift + uint(firstSubpixel))
	fullPlane := uint16(1<<antialiasSamples-1) << shift
	for pixel := firstPixel + 1; pixel < lastPixel; pixel++ {
		mask.samples[row+pixel] |= fullPlane
	}
	lastBits := uint16(1<<uint(lastSubpixel+1)) - 1
	mask.samples[row+lastPixel] |= lastBits << shift
}

func (m *sampleMask) replaceRowWithUnion(rowIndex int, value sampleInterval) bool {
	oldRow := m.rowIndex(rowIndex)
	right := [...]sampleInterval{value}
	newRowCount := unionSampleIntervalCount(oldRow, right[:])
	total := len(m.intervals) - len(oldRow) + newRowCount
	intervals, ok := m.allocateIntervalReplacement(total)
	if !ok {
		return false
	}
	lastRow := -1
	for row := range m.rows {
		start := len(intervals)
		current := m.rowIndex(row)
		if row == rowIndex {
			intervals = appendUnionSampleIntervals(intervals, current, right[:])
		} else {
			intervals = append(intervals, current...)
		}
		m.rows[row] = sampleMaskRow{start: uint32(start), count: uint32(len(intervals) - start)}
		if len(intervals) > start {
			lastRow = row
		}
	}
	m.intervals, m.lastRow = intervals, lastRow
	return true
}

func compareCrossings(a, b crossing) int {
	if a.x < b.x {
		return -1
	}
	if a.x > b.x {
		return 1
	}
	return 0
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
