package coolsvg

import (
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
)

type tiffJob struct {
	source       TIFFStripSource
	bounds       image.Rectangle
	width        int
	height       int
	format       TIFFPixelFormat
	sourceDepth  int
	bits         int
	samples      int
	rowsPerStrip int
	stripCount   int
	compression  TIFFCompression
	deflateLevel int
	predictor    TIFFPredictor
	order        binary.ByteOrder
	dither       DitherOptions
	iccProfile   []byte
}

type tiffStreamPlan struct {
	big       bool
	metadata  []byte
	padding   int
	offsets   []uint64
	fileSize  uint64
	ifdOffset uint64
}

func (j *tiffJob) encodeSeekable(w io.WriteSeeker, base int64, variant TIFFVariant) error {
	big := variant == TIFFVariantBigTIFF
	var expectedCounts []uint64
	if variant != TIFFVariantBigTIFF {
		worstCounts, err := j.maximumStripCounts()
		if err != nil {
			return err
		}
		fits, err := j.classicSeekableFits(worstCounts)
		if err != nil && !errors.Is(err, ErrTIFFClassicTooLarge) {
			return err
		}
		if variant == TIFFVariantAuto {
			big = !fits
		} else if !fits {
			if j.compression == TIFFCompressionNone {
				if err != nil {
					return err
				}
				return ErrTIFFClassicTooLarge
			}
			// A highly compressible image may still fit classic TIFF even when a
			// conservative compression bound does not. Count it without retaining
			// encoded strips before rejecting the forced classic request.
			state, err := j.passState(true)
			if err != nil {
				return err
			}
			expectedCounts, err = j.encodeStrips(io.Discard, state)
			if err != nil {
				return fmt.Errorf("coolsvg: count TIFF strips: %w", err)
			}
			fits, err = j.classicSeekableFits(expectedCounts)
			if err != nil && !errors.Is(err, ErrTIFFClassicTooLarge) {
				return err
			}
			if !fits {
				if err != nil {
					return err
				}
				return ErrTIFFClassicTooLarge
			}
		}
	}

	headerSize := tiffHeaderSize(big)
	position := &tiffPositionWriter{writer: w}
	if err := writeTIFFHeader(position, j.order, big, 0); err != nil {
		return fmt.Errorf("coolsvg: write TIFF header: %w", err)
	}
	state, err := j.passState(false)
	if err != nil {
		return err
	}
	counts, err := j.encodeStrips(position, state)
	if err != nil {
		return fmt.Errorf("coolsvg: write TIFF strips: %w", err)
	}
	if len(expectedCounts) != 0 {
		if err := compareTIFFCounts(expectedCounts, counts); err != nil {
			return err
		}
	}
	offsets, err := cumulativeTIFFOffsets(headerSize, counts)
	if err != nil {
		return err
	}
	alignment := uint64(2)
	if big {
		alignment = 8
	}
	ifdOffset, ok := alignTIFFOffset(position.position, alignment)
	if !ok {
		return fmt.Errorf("coolsvg: TIFF IFD alignment overflows 64 bits")
	}
	if padding := ifdOffset - position.position; padding != 0 {
		if err := writeTIFFAll(position, make([]byte, int(padding))); err != nil {
			return fmt.Errorf("coolsvg: align TIFF IFD: %w", err)
		}
	}
	entries, err := j.ifdEntries(big, offsets, counts)
	if err != nil {
		return err
	}
	metadata, err := buildTIFFIFD(j.order, big, ifdOffset, entries)
	if err != nil {
		return err
	}
	fileSize, ok := addTIFFUint64(ifdOffset, uint64(len(metadata)))
	if !ok {
		return fmt.Errorf("coolsvg: BigTIFF file size overflows 64 bits")
	}
	if !big && fileSize > math.MaxUint32 {
		return fmt.Errorf("%w: file size %d", ErrTIFFClassicTooLarge, fileSize)
	}
	if err := writeTIFFAll(position, metadata); err != nil {
		return fmt.Errorf("coolsvg: write TIFF IFD: %w", err)
	}

	if fileSize > math.MaxInt64 || base > math.MaxInt64-int64(fileSize) {
		return fmt.Errorf("coolsvg: TIFF seek offset exceeds int64")
	}
	pointerPosition := base + 4
	if big {
		pointerPosition = base + 8
	}
	if _, err := w.Seek(pointerPosition, io.SeekStart); err != nil {
		return fmt.Errorf("coolsvg: seek to TIFF header pointer: %w", err)
	}
	pointer := make([]byte, 4)
	if big {
		pointer = make([]byte, 8)
		j.order.PutUint64(pointer, ifdOffset)
	} else {
		j.order.PutUint32(pointer, uint32(ifdOffset))
	}
	if err := writeTIFFAll(w, pointer); err != nil {
		return fmt.Errorf("coolsvg: patch TIFF header pointer: %w", err)
	}
	if _, err := w.Seek(base+int64(fileSize), io.SeekStart); err != nil {
		return fmt.Errorf("coolsvg: restore TIFF output position: %w", err)
	}
	j.commitPassState(state)
	return nil
}

func (j *tiffJob) encodeStreaming(w io.Writer, variant TIFFVariant) error {
	var counts []uint64
	var err error
	if j.compression == TIFFCompressionNone {
		counts, err = j.rawStripCounts()
	} else {
		state, stateErr := j.passState(true)
		if stateErr != nil {
			return stateErr
		}
		counts, err = j.encodeStrips(io.Discard, state)
	}
	if err != nil {
		return fmt.Errorf("coolsvg: count TIFF strips: %w", err)
	}

	var plan tiffStreamPlan
	switch variant {
	case TIFFVariantBigTIFF:
		plan, err = j.planStreaming(true, counts)
	case TIFFVariantClassic:
		plan, err = j.planStreaming(false, counts)
	default:
		plan, err = j.planStreaming(false, counts)
		if errors.Is(err, ErrTIFFClassicTooLarge) {
			plan, err = j.planStreaming(true, counts)
		}
	}
	if err != nil {
		return err
	}
	if err := writeTIFFHeader(w, j.order, plan.big, plan.ifdOffset); err != nil {
		return fmt.Errorf("coolsvg: write TIFF header: %w", err)
	}
	if err := writeTIFFAll(w, plan.metadata); err != nil {
		return fmt.Errorf("coolsvg: write TIFF IFD: %w", err)
	}
	if plan.padding != 0 {
		if err := writeTIFFAll(w, make([]byte, plan.padding)); err != nil {
			return fmt.Errorf("coolsvg: align TIFF strips: %w", err)
		}
	}
	state, err := j.passState(false)
	if err != nil {
		return err
	}
	writtenCounts, err := j.encodeStrips(w, state)
	if err != nil {
		return fmt.Errorf("coolsvg: write TIFF strips: %w", err)
	}
	if err := compareTIFFCounts(counts, writtenCounts); err != nil {
		return err
	}
	j.commitPassState(state)
	return nil
}

func (j *tiffJob) planStreaming(big bool, counts []uint64) (tiffStreamPlan, error) {
	headerSize := tiffHeaderSize(big)
	dummyOffsets := make([]uint64, len(counts))
	entries, err := j.ifdEntries(big, dummyOffsets, counts)
	if err != nil {
		return tiffStreamPlan{}, err
	}
	metadata, err := buildTIFFIFD(j.order, big, headerSize, entries)
	if err != nil {
		return tiffStreamPlan{}, err
	}
	alignment := uint64(2)
	if big {
		alignment = 8
	}
	metadataEnd, ok := addTIFFUint64(headerSize, uint64(len(metadata)))
	if !ok {
		return tiffStreamPlan{}, fmt.Errorf("coolsvg: TIFF metadata size overflows")
	}
	dataStart, ok := alignTIFFOffset(metadataEnd, alignment)
	if !ok {
		return tiffStreamPlan{}, fmt.Errorf("coolsvg: TIFF strip alignment overflows 64 bits")
	}
	offsets, err := cumulativeTIFFOffsets(dataStart, counts)
	if err != nil {
		return tiffStreamPlan{}, err
	}
	entries, err = j.ifdEntries(big, offsets, counts)
	if err != nil {
		return tiffStreamPlan{}, err
	}
	finalMetadata, err := buildTIFFIFD(j.order, big, headerSize, entries)
	if err != nil {
		return tiffStreamPlan{}, err
	}
	if len(finalMetadata) != len(metadata) {
		return tiffStreamPlan{}, fmt.Errorf("coolsvg: internal TIFF metadata layout changed")
	}
	fileSize := dataStart
	for _, count := range counts {
		fileSize, ok = addTIFFUint64(fileSize, count)
		if !ok {
			return tiffStreamPlan{}, fmt.Errorf("coolsvg: BigTIFF file size overflows 64 bits")
		}
	}
	if !big && fileSize > math.MaxUint32 {
		return tiffStreamPlan{}, fmt.Errorf("%w: file size %d", ErrTIFFClassicTooLarge, fileSize)
	}
	return tiffStreamPlan{
		big:       big,
		metadata:  finalMetadata,
		padding:   int(dataStart - metadataEnd),
		offsets:   offsets,
		fileSize:  fileSize,
		ifdOffset: headerSize,
	}, nil
}

func (j *tiffJob) classicSeekableFits(counts []uint64) (bool, error) {
	if uint64(j.width) > math.MaxUint32 || uint64(j.height) > math.MaxUint32 || uint64(j.rowsPerStrip) > math.MaxUint32 {
		return false, fmt.Errorf("%w: image dimensions or rows per strip", ErrTIFFClassicTooLarge)
	}
	offsets, err := cumulativeTIFFOffsets(tiffHeaderSize(false), counts)
	if err != nil {
		return false, err
	}
	ifdOffset := tiffHeaderSize(false)
	for _, count := range counts {
		var ok bool
		ifdOffset, ok = addTIFFUint64(ifdOffset, count)
		if !ok {
			return false, fmt.Errorf("%w: strip offsets overflow 64 bits", ErrTIFFClassicTooLarge)
		}
	}
	ifdOffset, ok := alignTIFFOffset(ifdOffset, 2)
	if !ok {
		return false, fmt.Errorf("%w: IFD alignment overflows 64 bits", ErrTIFFClassicTooLarge)
	}
	entries, err := j.ifdEntries(false, offsets, counts)
	if err != nil {
		return false, err
	}
	metadata, err := buildTIFFIFD(j.order, false, ifdOffset, entries)
	if err != nil {
		return false, err
	}
	fileSize, ok := addTIFFUint64(ifdOffset, uint64(len(metadata)))
	if !ok || fileSize > math.MaxUint32 {
		return false, fmt.Errorf("%w: file size exceeds 32 bits", ErrTIFFClassicTooLarge)
	}
	return true, nil
}

func (j *tiffJob) maximumStripCounts() ([]uint64, error) {
	raw, err := j.rawStripCounts()
	if err != nil {
		return nil, err
	}
	if j.compression == TIFFCompressionNone {
		return raw, nil
	}
	lineBytes := uint64(j.width) * uint64(j.samples) * uint64(j.bits/8)
	counts := make([]uint64, len(raw))
	for index, rawCount := range raw {
		switch j.compression {
		case TIFFCompressionPackBits:
			rows := uint64(j.rowsInStrip(index))
			perRow := lineBytes + (lineBytes+127)/128
			if rows != 0 && perRow > math.MaxUint64/rows {
				return nil, fmt.Errorf("coolsvg: TIFF PackBits bound overflows")
			}
			counts[index] = perRow * rows
		case TIFFCompressionDeflate:
			// Go's Deflate writer selects stored blocks when Huffman coding would
			// expand excessively. This deliberately loose bound also covers zlib
			// framing and per-block overhead.
			overhead := rawCount/16 + 128
			var ok bool
			counts[index], ok = addTIFFUint64(rawCount, overhead)
			if !ok {
				return nil, fmt.Errorf("coolsvg: TIFF Deflate bound overflows")
			}
		}
	}
	return counts, nil
}

func (j *tiffJob) rawStripCounts() ([]uint64, error) {
	lineBytes := uint64(j.width) * uint64(j.samples) * uint64(j.bits/8)
	counts := make([]uint64, j.stripCount)
	for index := range counts {
		rows := uint64(j.rowsInStrip(index))
		if rows != 0 && lineBytes > math.MaxUint64/rows {
			return nil, fmt.Errorf("coolsvg: TIFF strip byte count overflows")
		}
		counts[index] = lineBytes * rows
	}
	return counts, nil
}

func (j *tiffJob) rowsInStrip(index int) int {
	first := index * j.rowsPerStrip
	return min(j.rowsPerStrip, j.height-first)
}

func (j *tiffJob) ifdEntries(big bool, offsets, counts []uint64) ([]tiffIFDEntry, error) {
	if len(offsets) != j.stripCount || len(counts) != j.stripCount {
		return nil, fmt.Errorf("coolsvg: internal TIFF strip table length mismatch")
	}
	width, err := tiffScalarEntry(j.order, big, tiffTagImageWidth, uint64(j.width))
	if err != nil {
		return nil, err
	}
	height, err := tiffScalarEntry(j.order, big, tiffTagImageLength, uint64(j.height))
	if err != nil {
		return nil, err
	}
	rows, err := tiffScalarEntry(j.order, big, tiffTagRowsPerStrip, uint64(j.rowsPerStrip))
	if err != nil {
		return nil, err
	}
	bits := make([]uint16, j.samples)
	for index := range bits {
		bits[index] = uint16(j.bits)
	}
	compression := uint16(1)
	switch j.compression {
	case TIFFCompressionDeflate:
		compression = 8
	case TIFFCompressionPackBits:
		compression = 32773
	}
	photometric := uint16(2)
	if j.format == TIFFPixelFormatCMYK {
		photometric = 5
	}
	var offsetData, countData []byte
	if big {
		offsetData = tiffLong8Data(j.order, offsets)
		countData = tiffLong8Data(j.order, counts)
	} else {
		offsetData, err = tiffLongData(j.order, offsets)
		if err != nil {
			return nil, err
		}
		countData, err = tiffLongData(j.order, counts)
		if err != nil {
			return nil, err
		}
	}
	arrayType := uint16(tiffTypeLong)
	if big {
		arrayType = tiffTypeLong8
	}
	entries := []tiffIFDEntry{
		width,
		height,
		{tag: tiffTagBitsPerSample, type_: tiffTypeShort, count: uint64(j.samples), data: tiffShortData(j.order, bits...)},
		{tag: tiffTagCompression, type_: tiffTypeShort, count: 1, data: tiffShortData(j.order, compression)},
		{tag: tiffTagPhotometricInterpretation, type_: tiffTypeShort, count: 1, data: tiffShortData(j.order, photometric)},
		{tag: tiffTagStripOffsets, type_: arrayType, count: uint64(len(offsets)), data: offsetData},
		{tag: tiffTagSamplesPerPixel, type_: tiffTypeShort, count: 1, data: tiffShortData(j.order, uint16(j.samples))},
		rows,
		{tag: tiffTagStripByteCounts, type_: arrayType, count: uint64(len(counts)), data: countData},
		{tag: tiffTagPlanarConfiguration, type_: tiffTypeShort, count: 1, data: tiffShortData(j.order, 1)},
	}
	if j.predictor == TIFFPredictorHorizontal {
		entries = append(entries, tiffIFDEntry{tag: tiffTagPredictor, type_: tiffTypeShort, count: 1, data: tiffShortData(j.order, 2)})
	}
	if j.format == TIFFPixelFormatRGBA {
		entries = append(entries, tiffIFDEntry{tag: tiffTagExtraSamples, type_: tiffTypeShort, count: 1, data: tiffShortData(j.order, 2)})
	}
	if j.format == TIFFPixelFormatCMYK {
		entries = append(entries,
			tiffIFDEntry{tag: tiffTagInkSet, type_: tiffTypeShort, count: 1, data: tiffShortData(j.order, 1)},
			tiffIFDEntry{tag: tiffTagNumberOfInks, type_: tiffTypeShort, count: 1, data: tiffShortData(j.order, 4)},
		)
	}
	if len(j.iccProfile) != 0 {
		entries = append(entries, tiffIFDEntry{tag: tiffTagICCProfile, type_: tiffTypeUndefined, count: uint64(len(j.iccProfile)), data: j.iccProfile})
	}
	return entries, nil
}

func (j *tiffJob) passState(_ bool) (*FloydSteinbergState, error) {
	if j.dither.Mode != DitherFloydSteinberg || j.sourceDepth <= j.bits {
		return nil, nil
	}
	if j.dither.State == nil {
		return &FloydSteinbergState{}, nil
	}
	return cloneFloydSteinbergState(j.dither.State), nil
}

func (j *tiffJob) commitPassState(state *FloydSteinbergState) {
	if state == nil || j.dither.State == nil || j.dither.Mode != DitherFloydSteinberg || j.sourceDepth <= j.bits {
		return
	}
	commitFloydSteinbergState(j.dither.State, state)
}

func (j *tiffJob) encodeStrips(w io.Writer, state *FloydSteinbergState) ([]uint64, error) {
	quantizer, err := newTIFFQuantizer(j, state)
	if err != nil {
		return nil, err
	}
	defer quantizer.close()
	maxRows := min(j.rowsPerStrip, j.height)
	maximumSamples, ok := checkedMulInt(j.width, maxRows, j.samples)
	if !ok {
		return nil, fmt.Errorf("coolsvg: TIFF source strip is too large")
	}
	stripSamples := make([]uint32, maximumSamples)
	lineLength, ok := checkedMulInt(j.width, j.samples, j.bits/8)
	if !ok {
		return nil, fmt.Errorf("coolsvg: TIFF scanline is too large")
	}
	line := make([]byte, lineLength)
	values := make([]uint16, j.width*j.samples)
	var colorValues []float64
	var quantizedColors []uint16
	if quantizer.mode == DitherFloydSteinberg {
		colorValues = make([]float64, j.width*quantizer.colorChannels)
		quantizedColors = make([]uint16, j.width*quantizer.colorChannels)
	}
	var compressor *zlib.Writer
	counts := make([]uint64, j.stripCount)
	for strip := 0; strip < j.stripCount; strip++ {
		minimumY := j.bounds.Min.Y + strip*j.rowsPerStrip
		maximumY := j.bounds.Max.Y
		if remaining := j.bounds.Max.Y - minimumY; j.rowsPerStrip < remaining {
			maximumY = minimumY + j.rowsPerStrip
		}
		rect := image.Rect(j.bounds.Min.X, minimumY, j.bounds.Max.X, maximumY)
		length, ok := checkedMulInt(rect.Dx(), rect.Dy(), j.samples)
		if !ok {
			return nil, fmt.Errorf("coolsvg: TIFF source strip %v is too large", rect)
		}
		samples := stripSamples[:length]
		if err := j.source.TIFFReadStrip(rect, samples); err != nil {
			return nil, fmt.Errorf("read source strip %v: %w", rect, err)
		}
		counter := &tiffCountWriter{writer: w}
		if err := j.encodeStrip(counter, rect, samples, line, values, colorValues, quantizedColors, quantizer, &compressor); err != nil {
			return nil, fmt.Errorf("encode strip %d: %w", strip, err)
		}
		counts[strip] = counter.count
	}
	return counts, nil
}

func (j *tiffJob) encodeStrip(w io.Writer, rect image.Rectangle, samples []uint32, line []byte, values []uint16, colorValues []float64, quantizedColors []uint16, quantizer *tiffQuantizer, reusableCompressor **zlib.Writer) error {
	encodeRows := func(destination io.Writer, packBits bool) error {
		rowSamples := j.width * j.samples
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			offset := (y - rect.Min.Y) * rowSamples
			if err := quantizer.scanline(samples[offset:offset+rowSamples], y, line, values, colorValues, quantizedColors); err != nil {
				return err
			}
			if packBits {
				if err := writeTIFFPackBits(destination, line); err != nil {
					return err
				}
			} else if err := writeTIFFAll(destination, line); err != nil {
				return err
			}
		}
		return nil
	}
	switch j.compression {
	case TIFFCompressionNone:
		return encodeRows(w, false)
	case TIFFCompressionPackBits:
		return encodeRows(w, true)
	case TIFFCompressionDeflate:
		compressor := *reusableCompressor
		if compressor == nil {
			var err error
			compressor, err = zlib.NewWriterLevel(w, j.deflateLevel)
			if err != nil {
				return err
			}
			*reusableCompressor = compressor
		} else {
			compressor.Reset(w)
		}
		encodeErr := encodeRows(compressor, false)
		closeErr := compressor.Close()
		if encodeErr != nil {
			return encodeErr
		}
		return closeErr
	default:
		return fmt.Errorf("unsupported compression %d", j.compression)
	}
}

type tiffQuantizer struct {
	job           *tiffJob
	mode          DitherMode
	state         *FloydSteinbergState
	stateBegun    bool
	nextY         int
	colorChannels int
	sourceMaximum uint64
	ditherBounds  image.Rectangle
}

func newTIFFQuantizer(job *tiffJob, state *FloydSteinbergState) (*tiffQuantizer, error) {
	colorChannels := job.samples
	if job.format == TIFFPixelFormatRGBA {
		colorChannels = 3
	}
	maximum := uint64(math.MaxUint32)
	if job.sourceDepth < 32 {
		maximum = (uint64(1) << job.sourceDepth) - 1
	}
	quantizer := &tiffQuantizer{
		job:           job,
		mode:          job.dither.Mode,
		state:         state,
		colorChannels: colorChannels,
		sourceMaximum: maximum,
	}
	if job.sourceDepth <= job.bits {
		quantizer.mode = DitherNone
		quantizer.state = nil
	}
	if quantizer.mode != DitherNone {
		effective, err := offsetDitherBounds(job.bounds, job.dither.CoordinateOffset)
		if err != nil {
			return nil, fmt.Errorf("coolsvg: TIFF %w", err)
		}
		quantizer.ditherBounds = effective
	}
	if quantizer.mode == DitherFloydSteinberg {
		if err := state.begin(quantizer.ditherBounds, colorChannels, job.bits); err != nil {
			return nil, err
		}
		quantizer.stateBegun = true
		quantizer.nextY = quantizer.ditherBounds.Min.Y
	}
	return quantizer, nil
}

func (q *tiffQuantizer) close() {
	if q.stateBegun {
		q.state.end(q.nextY)
		q.stateBegun = false
	}
}

func (q *tiffQuantizer) scanline(samples []uint32, y int, line []byte, values []uint16, colorValues []float64, quantizedColors []uint16) error {
	j := q.job
	ditherY := q.ditherBounds.Min.Y + (y - j.bounds.Min.Y)
	for index, sample := range samples {
		if uint64(sample) > q.sourceMaximum {
			return fmt.Errorf("source sample %d at row %d index %d exceeds %d-bit range", sample, y, index, j.sourceDepth)
		}
	}
	if j.sourceDepth == j.bits {
		for index, sample := range samples {
			values[index] = uint16(sample)
		}
	} else if q.mode == DitherFloydSteinberg {
		for x := 0; x < j.width; x++ {
			for channel := 0; channel < q.colorChannels; channel++ {
				sourceIndex := x*j.samples + channel
				colorValues[x*q.colorChannels+channel] = float64(samples[sourceIndex]) / float64(q.sourceMaximum)
			}
		}
		q.state.quantizeRow(colorValues, ditherY, j.bits, quantizedColors)
		q.nextY = ditherY + 1
	}
	if j.sourceDepth != j.bits {
		for x := 0; x < j.width; x++ {
			for channel := 0; channel < j.samples; channel++ {
				index := x*j.samples + channel
				isAlpha := j.format == TIFFPixelFormatRGBA && channel == 3
				normalized := float64(samples[index]) / float64(q.sourceMaximum)
				switch {
				case q.mode == DitherFloydSteinberg && !isAlpha:
					values[index] = quantizedColors[x*q.colorChannels+channel]
				case q.mode == DitherBayer8x8 && !isAlpha:
					globalX := q.ditherBounds.Min.X + x
					values[index] = quantizeBayer(normalized, j.bits, globalX, ditherY)
				default:
					values[index] = quantizeNearest(normalized, j.bits)
				}
			}
		}
	}

	previous := [4]uint16{}
	byteOffset := 0
	for x := 0; x < j.width; x++ {
		for channel := 0; channel < j.samples; channel++ {
			value := values[x*j.samples+channel]
			encoded := value
			if j.predictor == TIFFPredictorHorizontal {
				encoded = value - previous[channel]
				previous[channel] = value
			}
			if j.bits == 8 {
				line[byteOffset] = byte(encoded)
				byteOffset++
			} else {
				j.order.PutUint16(line[byteOffset:byteOffset+2], encoded)
				byteOffset += 2
			}
		}
	}
	return nil
}

func writeTIFFPackBits(w io.Writer, data []byte) error {
	for index := 0; index < len(data); {
		run := 1
		for index+run < len(data) && run < 128 && data[index+run] == data[index] {
			run++
		}
		if run >= 3 {
			packet := [2]byte{byte(int8(1 - run)), data[index]}
			if err := writeTIFFAll(w, packet[:]); err != nil {
				return err
			}
			index += run
			continue
		}
		literalStart := index
		index += run
		for index < len(data) && index-literalStart < 128 {
			nextRun := 1
			for index+nextRun < len(data) && nextRun < 128 && data[index+nextRun] == data[index] {
				nextRun++
			}
			if nextRun >= 3 {
				break
			}
			if index-literalStart+nextRun > 128 {
				break
			}
			index += nextRun
		}
		length := index - literalStart
		control := []byte{byte(length - 1)}
		if err := writeTIFFAll(w, control); err != nil {
			return err
		}
		if err := writeTIFFAll(w, data[literalStart:index]); err != nil {
			return err
		}
	}
	return nil
}

type tiffCountWriter struct {
	writer io.Writer
	count  uint64
}

func (w *tiffCountWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	if written > 0 {
		if uint64(written) > math.MaxUint64-w.count {
			return written, fmt.Errorf("coolsvg: TIFF byte count overflows")
		}
		w.count += uint64(written)
	}
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	return written, err
}

type tiffPositionWriter struct {
	writer   io.Writer
	position uint64
}

func (w *tiffPositionWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	if written > 0 {
		if uint64(written) > math.MaxUint64-w.position {
			return written, fmt.Errorf("coolsvg: TIFF output position overflows")
		}
		w.position += uint64(written)
	}
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	return written, err
}

func cumulativeTIFFOffsets(start uint64, counts []uint64) ([]uint64, error) {
	offsets := make([]uint64, len(counts))
	position := start
	for index, count := range counts {
		offsets[index] = position
		var ok bool
		position, ok = addTIFFUint64(position, count)
		if !ok {
			return nil, fmt.Errorf("coolsvg: TIFF strip offsets overflow 64 bits")
		}
	}
	return offsets, nil
}

func compareTIFFCounts(expected, actual []uint64) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("coolsvg: TIFF source changed between passes: %d strips became %d", len(expected), len(actual))
	}
	for index := range expected {
		if expected[index] != actual[index] {
			return fmt.Errorf("coolsvg: TIFF source changed between passes: strip %d size %d became %d", index, expected[index], actual[index])
		}
	}
	return nil
}

func addTIFFUint64(left, right uint64) (uint64, bool) {
	if right > math.MaxUint64-left {
		return 0, false
	}
	return left + right, true
}
