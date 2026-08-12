package coolsvg

import (
	"image"
	"testing"
)

func TestPrecisionPartitionsSkipIntegerAlignedTallBandEndpoints(t *testing.T) {
	const height = 262_144
	var work precisionPartitionWorkBudget
	var auxiliary precisionAuxiliaryBudget
	partitions := newPrecisionPartitions(image.Rect(0, 0, 1, height), nil, &work, &auxiliary)
	if partitions.err != nil {
		t.Fatal(partitions.err)
	}
	baseBytes := auxiliary.allocatedBytes
	for y := 0; y < height; y++ {
		partitions.add(y, 0, 1)
	}
	if partitions.err != nil {
		t.Fatal(partitions.err)
	}
	if work.endpoints != 0 {
		t.Fatalf("integer-aligned band consumed %d partition endpoints, want 0", work.endpoints)
	}
	if auxiliary.allocatedBytes != baseBytes {
		t.Fatalf("integer endpoints grew auxiliary storage from %d to %d bytes", baseBytes, auxiliary.allocatedBytes)
	}
}

func TestPrecisionPartitionRowsRespectSharedAuxiliaryBudget(t *testing.T) {
	auxiliary := precisionAuxiliaryBudget{allocatedBytes: maxPrecisionAuxiliaryBytes - precisionPartitionRowBytes + 1}
	partitions := newPrecisionPartitions(image.Rect(0, 0, 1, 1), nil, &precisionPartitionWorkBudget{}, &auxiliary)
	if partitions.err == nil {
		t.Fatal("partition row metadata exceeded the shared auxiliary budget without an error")
	}
}
