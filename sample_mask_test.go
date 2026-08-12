package coolsvg

import (
	"image"
	"slices"
	"strings"
	"testing"
)

func TestSampleMaskUsesCompactLazyIntervalStorage(t *testing.T) {
	t.Parallel()

	const height = 4_096
	mask := newSampleMask(image.Rect(0, 0, 1, height))
	if mask.err != nil {
		t.Fatal(mask.err)
	}
	wantRows := height * antialiasSamples
	if len(mask.rows) != wantRows {
		t.Fatalf("row metadata length = %d, want %d", len(mask.rows), wantRows)
	}
	if len(mask.intervals) != 0 || cap(mask.intervals) != 0 {
		t.Fatalf("new mask allocated interval storage: len=%d cap=%d", len(mask.intervals), cap(mask.intervals))
	}
	wantBaseBytes := uint64(height*sampleMaskSampleBytes + wantRows*sampleMaskRowBytes)
	if mask.allocatedBytes != wantBaseBytes {
		t.Fatalf("compact base allocation = %d bytes, want %d", mask.allocatedBytes, wantBaseBytes)
	}

	for y := 0; y < height; y++ {
		addSampleInterval(mask, y, 0, 0, 1)
	}
	if mask.err != nil {
		t.Fatal(mask.err)
	}
	if len(mask.intervals) != height {
		t.Fatalf("stored intervals = %d, want %d", len(mask.intervals), height)
	}
}

func TestSampleMaskBooleanOperationsPreserveExactIntervals(t *testing.T) {
	t.Parallel()

	makeLeft := func() *sampleMask {
		mask := newSampleMask(image.Rect(0, 0, 1, 1))
		addSampleInterval(mask, 0, 0, .4, .6)
		addSampleInterval(mask, 0, 0, 0, .2) // Exercise the out-of-order fallback.
		return mask
	}
	makeRight := func() *sampleMask {
		mask := newSampleMask(image.Rect(0, 0, 1, 1))
		addSampleInterval(mask, 0, 0, .1, .5)
		addSampleInterval(mask, 0, 0, .7, .8)
		return mask
	}

	union := makeLeft()
	unionSamples(union, makeRight())
	wantUnion := []sampleInterval{{left: 0, right: .6}, {left: .7, right: .8}}
	if union.err != nil {
		t.Fatal(union.err)
	}
	if got := union.row(0, 0); !slices.Equal(got, wantUnion) {
		t.Fatalf("union intervals = %#v, want %#v", got, wantUnion)
	}

	intersection := makeLeft()
	intersectSamples(intersection, makeRight())
	wantIntersection := []sampleInterval{{left: .1, right: .2}, {left: .4, right: .5}}
	if intersection.err != nil {
		t.Fatal(intersection.err)
	}
	if got := intersection.row(0, 0); !slices.Equal(got, wantIntersection) {
		t.Fatalf("intersection intervals = %#v, want %#v", got, wantIntersection)
	}

	clearSampleMask(intersection)
	if len(intersection.intervals) != 0 || len(intersection.row(0, 0)) != 0 {
		t.Fatalf("cleared mask retained intervals: %#v", intersection.row(0, 0))
	}
}

func TestSampleMasksOverlapUsesExactIntervals(t *testing.T) {
	t.Parallel()

	bounds := image.Rect(0, 0, 1, 1)
	left := newSampleMask(bounds)
	right := newSampleMask(bounds)
	addSampleInterval(left, 0, 0, .01, .1)
	addSampleInterval(right, 0, 0, .15, .2)
	if left.samples[0]&right.samples[0] == 0 {
		t.Fatal("test intervals must share a conservative occupancy bit")
	}
	if sampleMasksOverlap(bounds, left, right, 0, 1) {
		t.Fatal("disjoint exact intervals reported as overlapping")
	}
	addSampleInterval(right, 0, 0, .05, .08)
	if !sampleMasksOverlap(bounds, left, right, 0, 1) {
		t.Fatal("intersecting exact intervals reported as disjoint")
	}
	if sampleMasksOverlap(bounds, left, right, 1, 2) {
		t.Fatal("intervals from an unselected sample plane reported as overlapping")
	}
}

func TestSampleMaskRejectsOverflowAndAllocationLimits(t *testing.T) {
	t.Parallel()

	maximum := int(^uint(0) >> 1)
	minimum := -maximum - 1
	overflow := newSampleMask(image.Rect(minimum, 0, maximum, 1))
	if overflow.err == nil || !strings.Contains(overflow.err.Error(), "bounds") || !strings.Contains(overflow.err.Error(), "overflow") {
		t.Fatalf("overflow error = %v", overflow.err)
	}
	large := newSampleMask(image.Rect(0, 0, maximum, 1))
	if large.err == nil || !(strings.Contains(large.err.Error(), "overflows") || strings.Contains(large.err.Error(), "exceeds")) {
		t.Fatalf("large base-storage error = %v", large.err)
	}

	limited := newSampleMask(image.Rect(0, 0, 32, 1))
	if limited.err != nil {
		t.Fatal(limited.err)
	}
	limited.maximumBytes = limited.allocatedBytes + 8*sampleMaskIntervalBytes
	for index := 0; index < 9; index++ {
		left := float64(index * 2)
		addSampleInterval(limited, 0, 0, left, left+1)
	}
	if limited.err == nil || !strings.Contains(limited.err.Error(), "sample mask storage exceeds") {
		t.Fatalf("per-mask allocation error = %v", limited.err)
	}

	shared := &precisionAuxiliaryBudget{allocatedBytes: maxPrecisionAuxiliaryBytes - 1}
	budgeted := newBudgetedSampleMask(image.Rect(0, 0, 1, 1), shared)
	if budgeted.err == nil || !strings.Contains(budgeted.err.Error(), "precision auxiliary storage exceeds 128 MiB") {
		t.Fatalf("shared auxiliary-budget error = %v", budgeted.err)
	}
}
