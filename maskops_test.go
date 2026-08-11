package coolsvg

import (
	"slices"
	"testing"
)

func TestMaskOperationsHandleVectorTails(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 1, 7, 8, 15, 16, 17, 31, 32, 33, 257} {
		destination := make([]uint16, size)
		source := make([]uint16, size)
		wantOr := make([]uint16, size)
		wantAnd := make([]uint16, size)
		for i := range destination {
			destination[i] = uint16(i*1777) ^ 0x5aa5
			source[i] = uint16(i*2999) ^ 0xa55a
			wantOr[i] = destination[i] | source[i]
			wantAnd[i] = destination[i] & source[i]
		}

		got := slices.Clone(destination)
		maskOr(got, source)
		if !slices.Equal(got, wantOr) {
			t.Fatalf("OR size %d = %#v, want %#v", size, got, wantOr)
		}
		got = slices.Clone(destination)
		maskAnd(got, source)
		if !slices.Equal(got, wantAnd) {
			t.Fatalf("AND size %d = %#v, want %#v", size, got, wantAnd)
		}
	}
}
