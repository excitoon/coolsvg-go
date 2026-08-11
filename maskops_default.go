//go:build !goexperiment.simd || !amd64

package coolsvg

func maskOr(dst, src []uint16) {
	maskOrScalar(dst, src)
}

func maskAnd(dst, src []uint16) {
	maskAndScalar(dst, src)
}
