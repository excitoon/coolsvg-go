package coolsvg

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
)

const (
	tiffTypeByte      = 1
	tiffTypeASCII     = 2
	tiffTypeShort     = 3
	tiffTypeLong      = 4
	tiffTypeUndefined = 7
	tiffTypeLong8     = 16
)

const (
	tiffTagImageWidth                = 256
	tiffTagImageLength               = 257
	tiffTagBitsPerSample             = 258
	tiffTagCompression               = 259
	tiffTagPhotometricInterpretation = 262
	tiffTagStripOffsets              = 273
	tiffTagSamplesPerPixel           = 277
	tiffTagRowsPerStrip              = 278
	tiffTagStripByteCounts           = 279
	tiffTagPlanarConfiguration       = 284
	tiffTagPredictor                 = 317
	tiffTagInkSet                    = 332
	tiffTagNumberOfInks              = 334
	tiffTagExtraSamples              = 338
	tiffTagICCProfile                = 34675
)

type tiffIFDEntry struct {
	tag   uint16
	type_ uint16
	count uint64
	data  []byte
}

func tiffHeaderSize(big bool) uint64 {
	if big {
		return 16
	}
	return 8
}

func writeTIFFHeader(w io.Writer, order binary.ByteOrder, big bool, ifdOffset uint64) error {
	size := tiffHeaderSize(big)
	header := make([]byte, int(size))
	if order == binary.LittleEndian {
		copy(header[0:2], "II")
	} else {
		copy(header[0:2], "MM")
	}
	if big {
		order.PutUint16(header[2:4], 43)
		order.PutUint16(header[4:6], 8)
		order.PutUint16(header[6:8], 0)
		order.PutUint64(header[8:16], ifdOffset)
	} else {
		if ifdOffset > math.MaxUint32 {
			return fmt.Errorf("%w: IFD offset %d", ErrTIFFClassicTooLarge, ifdOffset)
		}
		order.PutUint16(header[2:4], 42)
		order.PutUint32(header[4:8], uint32(ifdOffset))
	}
	return writeTIFFAll(w, header)
}

func buildTIFFIFD(order binary.ByteOrder, big bool, ifdOffset uint64, entries []tiffIFDEntry) ([]byte, error) {
	entries = append([]tiffIFDEntry(nil), entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].tag < entries[j].tag })
	for index := 1; index < len(entries); index++ {
		if entries[index-1].tag == entries[index].tag {
			return nil, fmt.Errorf("coolsvg: duplicate TIFF tag %d", entries[index].tag)
		}
	}
	if big {
		if uint64(len(entries)) > math.MaxUint64/20 {
			return nil, fmt.Errorf("coolsvg: too many BigTIFF entries")
		}
	} else if len(entries) > math.MaxUint16 {
		return nil, fmt.Errorf("%w: too many IFD entries", ErrTIFFClassicTooLarge)
	}

	inlineSize, entrySize := 4, 12
	baseSize := uint64(2 + len(entries)*entrySize + 4)
	if big {
		inlineSize, entrySize = 8, 20
		baseSize = uint64(8 + len(entries)*entrySize + 8)
	}
	if baseSize > uint64(math.MaxInt) {
		return nil, fmt.Errorf("coolsvg: TIFF IFD is too large")
	}
	metadata := make([]byte, int(baseSize))
	if big {
		order.PutUint64(metadata[0:8], uint64(len(entries)))
	} else {
		order.PutUint16(metadata[0:2], uint16(len(entries)))
	}

	for index, entry := range entries {
		typeSize, ok := tiffTypeSize(entry.type_)
		if !ok {
			return nil, fmt.Errorf("coolsvg: invalid TIFF field type %d", entry.type_)
		}
		if entry.count != 0 && entry.count > math.MaxUint64/uint64(typeSize) {
			return nil, fmt.Errorf("coolsvg: TIFF tag %d data length overflows", entry.tag)
		}
		dataSize := entry.count * uint64(typeSize)
		if dataSize != uint64(len(entry.data)) {
			return nil, fmt.Errorf("coolsvg: TIFF tag %d has %d bytes, want %d", entry.tag, len(entry.data), dataSize)
		}
		if !big && entry.count > math.MaxUint32 {
			return nil, fmt.Errorf("%w: TIFF tag %d count %d", ErrTIFFClassicTooLarge, entry.tag, entry.count)
		}

		entryOffset := 2 + index*entrySize
		if big {
			entryOffset = 8 + index*entrySize
		}
		order.PutUint16(metadata[entryOffset:entryOffset+2], entry.tag)
		order.PutUint16(metadata[entryOffset+2:entryOffset+4], entry.type_)
		valueOffset := entryOffset + 8
		if big {
			order.PutUint64(metadata[entryOffset+4:entryOffset+12], entry.count)
			valueOffset = entryOffset + 12
		} else {
			order.PutUint32(metadata[entryOffset+4:entryOffset+8], uint32(entry.count))
		}

		if len(entry.data) <= inlineSize {
			copy(metadata[valueOffset:valueOffset+inlineSize], entry.data)
			continue
		}
		alignment := uint64(typeSize)
		if alignment > 8 {
			alignment = 8
		}
		metadataEnd, ok := addTIFFUint64(ifdOffset, uint64(len(metadata)))
		if !ok {
			return nil, fmt.Errorf("coolsvg: TIFF metadata offset overflows 64 bits")
		}
		absolute, ok := alignTIFFOffset(metadataEnd, alignment)
		if !ok {
			return nil, fmt.Errorf("coolsvg: TIFF metadata alignment overflows 64 bits")
		}
		padding := absolute - metadataEnd
		if padding > uint64(math.MaxInt-len(metadata)) || len(entry.data) > math.MaxInt-len(metadata)-int(padding) {
			return nil, fmt.Errorf("coolsvg: TIFF metadata is too large")
		}
		metadata = append(metadata, make([]byte, int(padding))...)
		dataOffset, ok := addTIFFUint64(ifdOffset, uint64(len(metadata)))
		if !ok {
			return nil, fmt.Errorf("coolsvg: TIFF tag %d data offset overflows 64 bits", entry.tag)
		}
		if !big && dataOffset > math.MaxUint32 {
			return nil, fmt.Errorf("%w: TIFF tag %d data offset %d", ErrTIFFClassicTooLarge, entry.tag, dataOffset)
		}
		if big {
			order.PutUint64(metadata[valueOffset:valueOffset+8], dataOffset)
		} else {
			order.PutUint32(metadata[valueOffset:valueOffset+4], uint32(dataOffset))
		}
		metadata = append(metadata, entry.data...)
	}
	return metadata, nil
}

func tiffTypeSize(type_ uint16) (int, bool) {
	switch type_ {
	case tiffTypeByte, tiffTypeASCII, tiffTypeUndefined:
		return 1, true
	case tiffTypeShort:
		return 2, true
	case tiffTypeLong:
		return 4, true
	case tiffTypeLong8:
		return 8, true
	default:
		return 0, false
	}
}

func tiffShortData(order binary.ByteOrder, values ...uint16) []byte {
	data := make([]byte, len(values)*2)
	for index, value := range values {
		order.PutUint16(data[index*2:index*2+2], value)
	}
	return data
}

func tiffLongData(order binary.ByteOrder, values []uint64) ([]byte, error) {
	data := make([]byte, len(values)*4)
	for index, value := range values {
		if value > math.MaxUint32 {
			return nil, fmt.Errorf("%w: value %d", ErrTIFFClassicTooLarge, value)
		}
		order.PutUint32(data[index*4:index*4+4], uint32(value))
	}
	return data, nil
}

func tiffLong8Data(order binary.ByteOrder, values []uint64) []byte {
	data := make([]byte, len(values)*8)
	for index, value := range values {
		order.PutUint64(data[index*8:index*8+8], value)
	}
	return data
}

func tiffScalarEntry(order binary.ByteOrder, big bool, tag uint16, value uint64) (tiffIFDEntry, error) {
	if value <= math.MaxUint32 {
		data, err := tiffLongData(order, []uint64{value})
		return tiffIFDEntry{tag: tag, type_: tiffTypeLong, count: 1, data: data}, err
	}
	if !big {
		return tiffIFDEntry{}, fmt.Errorf("%w: TIFF tag %d value %d", ErrTIFFClassicTooLarge, tag, value)
	}
	return tiffIFDEntry{tag: tag, type_: tiffTypeLong8, count: 1, data: tiffLong8Data(order, []uint64{value})}, nil
}

func alignTIFFOffset(value, alignment uint64) (uint64, bool) {
	if alignment <= 1 {
		return value, true
	}
	remainder := value % alignment
	if remainder == 0 {
		return value, true
	}
	return addTIFFUint64(value, alignment-remainder)
}

func writeTIFFAll(w io.Writer, data []byte) error {
	for len(data) != 0 {
		written, err := w.Write(data)
		if written > 0 {
			data = data[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
