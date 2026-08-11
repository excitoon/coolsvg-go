package coolsvg

func maskOrScalar(dst, src []uint16) {
	for i := range dst {
		dst[i] |= src[i]
	}
}

func maskAndScalar(dst, src []uint16) {
	for i := range dst {
		dst[i] &= src[i]
	}
}
