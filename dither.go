package coolsvg

import (
	"fmt"
	"image"
	"math"
	"sync"
)

// DitherMode selects destination quantization behavior.
type DitherMode uint8

const (
	// DitherNone rounds each sample independently to the destination depth.
	DitherNone DitherMode = iota
	// DitherBayer8x8 applies an ordered 8x8 Bayer threshold. Its phase is based
	// on global coordinates, so independently rendered tiles are identical to
	// the corresponding portion of a full render.
	DitherBayer8x8
	// DitherFloydSteinberg diffuses floating-point residuals in serpentine
	// scanline order. Consecutive full-width bands must share a state.
	DitherFloydSteinberg
)

// DitherOptions controls color-channel quantization from the renderer's
// floating-point working surface to an 8- or 16-bit destination. RGBA alpha is
// always quantized independently with nearest rounding.
type DitherOptions struct {
	Mode DitherMode
	// CoordinateOffset is added to source coordinates for ordered-dither phase
	// and Floyd-Steinberg continuity. RenderRegion already uses global source
	// coordinates, so it normally remains zero.
	CoordinateOffset image.Point
	// State carries Floyd-Steinberg residuals across consecutive full-width
	// bands. When nil, a render operation creates a private state. A state is
	// mutable, must not be used concurrently, and must be Reset before starting
	// a different image or x-range.
	State *FloydSteinbergState
}

// FloydSteinbergState carries floating-point diffusion residuals between
// consecutive full-width scanline bands. A state must not be copied after
// first use.
type FloydSteinbergState struct {
	mu          sync.Mutex
	initialized bool
	minimumX    int
	maximumX    int
	nextY       int
	channels    int
	targetBits  int
	current     []float64
	next        []float64
}

// Reset discards all accumulated diffusion residuals.
func (s *FloydSteinbergState) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = false
	s.minimumX, s.maximumX, s.nextY = 0, 0, 0
	s.channels, s.targetBits = 0, 0
	s.current, s.next = nil, nil
}

func (s *FloydSteinbergState) begin(bounds image.Rectangle, channels, targetBits int) error {
	s.mu.Lock()
	if bounds.Empty() {
		s.mu.Unlock()
		return fmt.Errorf("svg: Floyd-Steinberg band %v is empty", bounds)
	}
	if !s.initialized {
		maximum := int(^uint(0) >> 1)
		if channels <= 0 || bounds.Dx() > maximum/channels {
			s.mu.Unlock()
			return fmt.Errorf("svg: Floyd-Steinberg band %v is too wide", bounds)
		}
		length := bounds.Dx() * channels
		if length > precisionMaxSurfaceBytes/16 {
			s.mu.Unlock()
			return fmt.Errorf("svg: Floyd-Steinberg state for band %v exceeds %d MiB", bounds, precisionMaxSurfaceBytes>>20)
		}
		s.initialized = true
		s.minimumX, s.maximumX = bounds.Min.X, bounds.Max.X
		s.nextY = bounds.Min.Y
		s.channels, s.targetBits = channels, targetBits
		s.current = make([]float64, length)
		s.next = make([]float64, length)
	}
	if bounds.Min.X != s.minimumX || bounds.Max.X != s.maximumX {
		s.mu.Unlock()
		return fmt.Errorf("svg: Floyd-Steinberg band x-range %v does not match [%d,%d)", bounds, s.minimumX, s.maximumX)
	}
	if bounds.Min.Y != s.nextY {
		s.mu.Unlock()
		return fmt.Errorf("svg: Floyd-Steinberg band starts at y=%d, want consecutive y=%d", bounds.Min.Y, s.nextY)
	}
	if channels != s.channels || targetBits != s.targetBits {
		s.mu.Unlock()
		return fmt.Errorf("svg: Floyd-Steinberg band format changed from %d channels/%d bits to %d channels/%d bits", s.channels, s.targetBits, channels, targetBits)
	}
	return nil
}

func offsetDitherBounds(bounds image.Rectangle, offset image.Point) (image.Rectangle, error) {
	add := func(value, delta int) (int, bool) {
		if delta > 0 && value > math.MaxInt-delta {
			return 0, false
		}
		if delta < 0 && value < math.MinInt-delta {
			return 0, false
		}
		return value + delta, true
	}
	minimumX, minXOK := add(bounds.Min.X, offset.X)
	maximumX, maxXOK := add(bounds.Max.X, offset.X)
	minimumY, minYOK := add(bounds.Min.Y, offset.Y)
	maximumY, maxYOK := add(bounds.Max.Y, offset.Y)
	if !minXOK || !maxXOK || !minYOK || !maxYOK {
		return image.Rectangle{}, fmt.Errorf("dither coordinate offset %v overflows bounds %v", offset, bounds)
	}
	return image.Rect(minimumX, minimumY, maximumX, maximumY), nil
}

func (s *FloydSteinbergState) end(nextY int) {
	s.nextY = nextY
	s.mu.Unlock()
}

var bayer8x8 = [64]uint8{
	0, 48, 12, 60, 3, 51, 15, 63,
	32, 16, 44, 28, 35, 19, 47, 31,
	8, 56, 4, 52, 11, 59, 7, 55,
	40, 24, 36, 20, 43, 27, 39, 23,
	2, 50, 14, 62, 1, 49, 13, 61,
	34, 18, 46, 30, 33, 17, 45, 29,
	10, 58, 6, 54, 9, 57, 5, 53,
	42, 26, 38, 22, 41, 25, 37, 21,
}

func validateDither(options DitherOptions) error {
	if options.Mode > DitherFloydSteinberg {
		return fmt.Errorf("svg: unsupported dither mode %d", options.Mode)
	}
	return nil
}

func quantizeNearest(value float64, targetBits int) uint16 {
	maximum := float64((uint32(1) << targetBits) - 1)
	value = math.Max(0, math.Min(1, value))
	return uint16(math.Floor(value*maximum + 0.5))
}

func quantizeBayer(value float64, targetBits, x, y int) uint16 {
	maximum := float64((uint32(1) << targetBits) - 1)
	value = math.Max(0, math.Min(1, value))
	rank := float64(bayer8x8[(y&7)*8+(x&7)])
	offset := (rank+0.5)/64 - 0.5
	quantized := math.Floor(value*maximum + 0.5 + offset)
	return uint16(math.Max(0, math.Min(maximum, quantized)))
}

func (s *FloydSteinbergState) quantizeRow(samples []float64, y int, targetBits int, output []uint16) {
	width, channels := s.maximumX-s.minimumX, s.channels
	maximum := float64((uint32(1) << targetBits) - 1)
	leftToRight := y&1 == 0
	start, end, step := 0, width, 1
	if !leftToRight {
		start, end, step = width-1, -1, -1
	}
	for column := start; column != end; column += step {
		for channel := 0; channel < channels; channel++ {
			index := column*channels + channel
			adjusted := math.Max(0, math.Min(1, samples[index]+s.current[index]))
			quantized := math.Floor(adjusted*maximum + 0.5)
			output[index] = uint16(quantized)
			errorValue := adjusted - quantized/maximum
			forward := column + step
			if forward >= 0 && forward < width {
				s.current[forward*channels+channel] += errorValue * 7 / 16
			}
			backwardNext := column - step
			if backwardNext >= 0 && backwardNext < width {
				s.next[backwardNext*channels+channel] += errorValue * 3 / 16
			}
			s.next[index] += errorValue * 5 / 16
			forwardNext := column + step
			if forwardNext >= 0 && forwardNext < width {
				s.next[forwardNext*channels+channel] += errorValue / 16
			}
		}
	}
	s.current, s.next = s.next, s.current
	clear(s.next)
}

func isolateFloydSteinbergState(options DitherOptions) (DitherOptions, *FloydSteinbergState) {
	if options.Mode != DitherFloydSteinberg {
		return options, nil
	}
	if options.State == nil {
		options.State = &FloydSteinbergState{}
		return options, nil
	}
	external := options.State
	options.State = cloneFloydSteinbergState(external)
	return options, external
}

func cloneFloydSteinbergState(source *FloydSteinbergState) *FloydSteinbergState {
	source.mu.Lock()
	defer source.mu.Unlock()
	return &FloydSteinbergState{
		initialized: source.initialized,
		minimumX:    source.minimumX,
		maximumX:    source.maximumX,
		nextY:       source.nextY,
		channels:    source.channels,
		targetBits:  source.targetBits,
		current:     append([]float64(nil), source.current...),
		next:        append([]float64(nil), source.next...),
	}
}

func commitFloydSteinbergState(destination, source *FloydSteinbergState) {
	if destination == nil || source == nil {
		return
	}
	snapshot := cloneFloydSteinbergState(source)
	destination.mu.Lock()
	defer destination.mu.Unlock()
	destination.initialized = snapshot.initialized
	destination.minimumX, destination.maximumX, destination.nextY = snapshot.minimumX, snapshot.maximumX, snapshot.nextY
	destination.channels, destination.targetBits = snapshot.channels, snapshot.targetBits
	destination.current = snapshot.current
	destination.next = snapshot.next
}
