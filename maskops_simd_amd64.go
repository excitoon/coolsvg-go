//go:build goexperiment.simd && amd64

package coolsvg

import "simd/archsimd"

func maskOr(dst, src []uint16) {
	i := 0
	if archsimd.X86.AVX2() {
		for ; i+16 <= len(dst); i += 16 {
			archsimd.LoadUint16x16Slice(dst[i:]).Or(archsimd.LoadUint16x16Slice(src[i:])).StoreSlice(dst[i:])
		}
	} else if archsimd.X86.AVX() {
		for ; i+8 <= len(dst); i += 8 {
			archsimd.LoadUint16x8Slice(dst[i:]).Or(archsimd.LoadUint16x8Slice(src[i:])).StoreSlice(dst[i:])
		}
	}
	maskOrScalar(dst[i:], src[i:])
}

func maskAnd(dst, src []uint16) {
	i := 0
	if archsimd.X86.AVX2() {
		for ; i+16 <= len(dst); i += 16 {
			archsimd.LoadUint16x16Slice(dst[i:]).And(archsimd.LoadUint16x16Slice(src[i:])).StoreSlice(dst[i:])
		}
	} else if archsimd.X86.AVX() {
		for ; i+8 <= len(dst); i += 8 {
			archsimd.LoadUint16x8Slice(dst[i:]).And(archsimd.LoadUint16x8Slice(src[i:])).StoreSlice(dst[i:])
		}
	}
	maskAndScalar(dst[i:], src[i:])
}
