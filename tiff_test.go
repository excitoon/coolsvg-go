package coolsvg

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"strings"
	"testing"

	ximagetiff "golang.org/x/image/tiff"
)

func TestEncodeTIFFRGBA8DeflateRoundTripAndTags(t *testing.T) {
	t.Parallel()

	bounds := image.Rect(10, 20, 13, 22)
	source := image.NewNRGBA(bounds)
	values := []color.NRGBA{
		{R: 1, G: 2, B: 3, A: 4}, {R: 40, G: 50, B: 60, A: 70}, {R: 250, G: 200, B: 150, A: 100},
		{R: 9, G: 8, B: 7, A: 6}, {R: 80, G: 90, B: 100, A: 110}, {R: 255, G: 128, B: 0, A: 255},
	}
	for index, value := range values {
		source.SetNRGBA(bounds.Min.X+index%bounds.Dx(), bounds.Min.Y+index/bounds.Dx(), value)
	}
	var encoded bytes.Buffer
	err := EncodeTIFF(&encoded, source, &TIFFOptions{BitsPerSample: 8, RowsPerStrip: 1})
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseTestTIFF(t, encoded.Bytes())
	if parsed.big || parsed.width != 3 || parsed.height != 2 {
		t.Fatalf("TIFF layout = big:%v %dx%d", parsed.big, parsed.width, parsed.height)
	}
	assertTestTIFFValues(t, parsed, tiffTagBitsPerSample, []uint64{8, 8, 8, 8})
	assertTestTIFFValues(t, parsed, tiffTagCompression, []uint64{8})
	assertTestTIFFValues(t, parsed, tiffTagPhotometricInterpretation, []uint64{2})
	assertTestTIFFValues(t, parsed, tiffTagPredictor, []uint64{2})
	assertTestTIFFValues(t, parsed, tiffTagExtraSamples, []uint64{2})
	assertTestTIFFValues(t, parsed, tiffTagRowsPerStrip, []uint64{1})
	if len(parsed.stripOffsets) != 2 {
		t.Fatalf("strip count = %d, want 2", len(parsed.stripOffsets))
	}
	for index, want := range values {
		got := [4]uint16{
			parsed.sample(index%3, index/3, 0), parsed.sample(index%3, index/3, 1),
			parsed.sample(index%3, index/3, 2), parsed.sample(index%3, index/3, 3),
		}
		if got != [4]uint16{uint16(want.R), uint16(want.G), uint16(want.B), uint16(want.A)} {
			t.Fatalf("pixel %d = %v, want %#v", index, got, want)
		}
	}

	decoded, err := ximagetiff.Decode(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("x/image/tiff round trip: %v", err)
	}
	for index, want := range values {
		x, y := index%3, index/3
		got := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
		if got != want {
			t.Fatalf("decoded pixel (%d,%d) = %#v, want %#v", x, y, got, want)
		}
	}
}

func TestEncodeTIFFAutoBitDepthUsesRecognizedStoragePrecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		image image.Image
		bits  uint64
	}{
		{name: "alpha8", image: image.NewAlpha(image.Rect(0, 0, 1, 1)), bits: 8},
		{name: "ycbcr8", image: image.NewYCbCr(image.Rect(0, 0, 1, 1), image.YCbCrSubsampleRatio444), bits: 8},
		{name: "nycbcra8", image: image.NewNYCbCrA(image.Rect(0, 0, 1, 1), image.YCbCrSubsampleRatio444), bits: 8},
		{name: "alpha16", image: image.NewAlpha16(image.Rect(0, 0, 1, 1)), bits: 16},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output := &testMemoryWriteSeeker{}
			if err := EncodeTIFF(output, test.image, nil); err != nil {
				t.Fatal(err)
			}
			parsed := parseTestTIFF(t, output.data)
			values := testTIFFValues(t, parsed, tiffTagBitsPerSample)
			for _, got := range values {
				if got != test.bits {
					t.Fatalf("BitsPerSample = %v, want all %d", values, test.bits)
				}
			}
		})
	}
}

func TestEncodeTIFFCMYKConversionUsesStraightColorChannels(t *testing.T) {
	t.Parallel()

	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 0})
	source.SetNRGBA(1, 0, color.NRGBA{R: 40, G: 120, B: 200, A: 96})
	output := &testMemoryWriteSeeker{}
	if err := EncodeTIFF(output, source, &TIFFOptions{
		Format: TIFFPixelFormatCMYK, BitsPerSample: 8, Compression: TIFFCompressionNone,
	}); err != nil {
		t.Fatal(err)
	}
	parsed := parseTestTIFF(t, output.data)
	for x, rgb := range [][3]uint8{{255, 0, 0}, {40, 120, 200}} {
		c, m, y, k := color.RGBToCMYK(rgb[0], rgb[1], rgb[2])
		want := [4]uint16{uint16(c), uint16(m), uint16(y), uint16(k)}
		got := [4]uint16{
			parsed.sample(x, 0, 0), parsed.sample(x, 0, 1),
			parsed.sample(x, 0, 2), parsed.sample(x, 0, 3),
		}
		if got != want {
			t.Fatalf("CMYK pixel %d = %v, want %v", x, got, want)
		}
	}
}

func TestEncodeTIFFPreservesPrecisionAcrossColorModelConversion(t *testing.T) {
	t.Parallel()

	rgb := image.NewNRGBA64(image.Rect(0, 0, 1, 1))
	rgb.SetNRGBA64(0, 0, color.NRGBA64{R: 0x1234, G: 0x89ab, B: 0xcdef, A: 0xffff})
	cmykOutput := &testMemoryWriteSeeker{}
	if err := EncodeTIFF(cmykOutput, rgb, &TIFFOptions{
		Format: TIFFPixelFormatCMYK, Compression: TIFFCompressionNone,
	}); err != nil {
		t.Fatal(err)
	}
	cmykParsed := parseTestTIFF(t, cmykOutput.data)
	separated := deviceRGBToCMYK64(0x1234, 0x89ab, 0xcdef)
	gotCMYK := [4]uint16{
		cmykParsed.sample(0, 0, 0), cmykParsed.sample(0, 0, 1),
		cmykParsed.sample(0, 0, 2), cmykParsed.sample(0, 0, 3),
	}
	if want := [4]uint16{separated.C, separated.M, separated.Y, separated.K}; gotCMYK != want {
		t.Fatalf("16-bit RGB to CMYK = %v, want %v", gotCMYK, want)
	}
	assertTestTIFFValues(t, cmykParsed, tiffTagBitsPerSample, []uint64{16, 16, 16, 16})

	cmyk := NewCMYK64(image.Rect(0, 0, 1, 1))
	cmyk.SetCMYK64(0, 0, CMYK64Color{C: 0x1234, M: 0x4567, Y: 0x89ab, K: 0x1357})
	rgbOutput := &testMemoryWriteSeeker{}
	if err := EncodeTIFF(rgbOutput, cmyk, &TIFFOptions{
		Format: TIFFPixelFormatRGB, Compression: TIFFCompressionNone,
	}); err != nil {
		t.Fatal(err)
	}
	rgbParsed := parseTestTIFF(t, rgbOutput.data)
	converted := color.NRGBA64Model.Convert(cmyk.At(0, 0)).(color.NRGBA64)
	gotRGB := [3]uint16{rgbParsed.sample(0, 0, 0), rgbParsed.sample(0, 0, 1), rgbParsed.sample(0, 0, 2)}
	if want := [3]uint16{converted.R, converted.G, converted.B}; gotRGB != want {
		t.Fatalf("16-bit CMYK to RGB = %v, want %v", gotRGB, want)
	}
	assertTestTIFFValues(t, rgbParsed, tiffTagBitsPerSample, []uint64{16, 16, 16})
}

func TestEncodeTIFFRGB16BigEndianPackBits(t *testing.T) {
	t.Parallel()

	source := image.NewNRGBA64(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			source.SetNRGBA64(x, y, color.NRGBA64{
				R: uint16(1000 + x*5000 + y*700), G: uint16(60000 - x*3000 - y*900),
				B: uint16(x*1111 + y*3333), A: 0xffff,
			})
		}
	}
	var encoded bytes.Buffer
	err := EncodeTIFF(&encoded, source, &TIFFOptions{
		Format: TIFFPixelFormatRGB, BitsPerSample: 16, Compression: TIFFCompressionPackBits,
		ByteOrder: TIFFByteOrderBig, RowsPerStrip: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseTestTIFF(t, encoded.Bytes())
	if parsed.order != binary.BigEndian || parsed.samples != 3 || parsed.bits != 16 {
		t.Fatalf("format order=%T samples=%d bits=%d", parsed.order, parsed.samples, parsed.bits)
	}
	assertTestTIFFValues(t, parsed, tiffTagCompression, []uint64{32773})
	if _, ok := parsed.fields[tiffTagPredictor]; ok {
		t.Fatal("default PackBits output unexpectedly has a predictor")
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			want := source.NRGBA64At(x, y)
			got := [3]uint16{parsed.sample(x, y, 0), parsed.sample(x, y, 1), parsed.sample(x, y, 2)}
			if got != [3]uint16{want.R, want.G, want.B} {
				t.Fatalf("pixel (%d,%d) = %v, want %#v", x, y, got, want)
			}
		}
	}
}

func TestEncodeTIFFRGB16BigEndianDeflatePredictor(t *testing.T) {
	t.Parallel()

	source := image.NewNRGBA64(image.Rect(0, 0, 7, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 7; x++ {
			source.SetNRGBA64(x, y, color.NRGBA64{
				R: uint16(100 + x*4000 + y*71), G: uint16(50000 - x*987 + y*333),
				B: uint16(1000 + x*x*777 + y*2222), A: 0xffff,
			})
		}
	}
	output := &testMemoryWriteSeeker{}
	if err := EncodeTIFF(output, source, &TIFFOptions{
		Format: TIFFPixelFormatRGB, BitsPerSample: 16, Compression: TIFFCompressionDeflate,
		Predictor: TIFFPredictorHorizontal, ByteOrder: TIFFByteOrderBig, RowsPerStrip: 2,
	}); err != nil {
		t.Fatal(err)
	}
	parsed := parseTestTIFF(t, output.data)
	assertTestTIFFValues(t, parsed, tiffTagPredictor, []uint64{2})
	for y := 0; y < 4; y++ {
		for x := 0; x < 7; x++ {
			want := source.NRGBA64At(x, y)
			got := [3]uint16{parsed.sample(x, y, 0), parsed.sample(x, y, 1), parsed.sample(x, y, 2)}
			if got != [3]uint16{want.R, want.G, want.B} {
				t.Fatalf("predictor pixel (%d,%d) = %v, want %#v", x, y, got, want)
			}
		}
	}
}

func TestEncodeTIFFCMYK64BigTIFFAndICCProfile(t *testing.T) {
	t.Parallel()

	source := NewCMYK64(image.Rect(5, 7, 7, 9))
	values := []CMYK64Color{
		{C: 1, M: 2, Y: 3, K: 4}, {C: 0x1111, M: 0x2222, Y: 0x3333, K: 0x4444},
		{C: 0xffff, M: 0, Y: 0xabcd, K: 0x9876}, {C: 17, M: 18, Y: 19, K: 20},
	}
	for index, value := range values {
		source.SetCMYK64(5+index%2, 7+index/2, value)
	}
	profile := []byte("test-icc-profile\x00\x01\x02")
	var encoded bytes.Buffer
	err := EncodeTIFF(&encoded, source, &TIFFOptions{
		Variant: TIFFVariantBigTIFF, Compression: TIFFCompressionNone, RowsPerStrip: 1,
		ICCProfile: profile, Dither: DitherOptions{Mode: DitherBayer8x8},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseTestTIFF(t, encoded.Bytes())
	if !parsed.big || parsed.bits != 16 || parsed.samples != 4 {
		t.Fatalf("CMYK BigTIFF = big:%v bits:%d samples:%d", parsed.big, parsed.bits, parsed.samples)
	}
	assertTestTIFFValues(t, parsed, tiffTagPhotometricInterpretation, []uint64{5})
	assertTestTIFFValues(t, parsed, tiffTagInkSet, []uint64{1})
	assertTestTIFFValues(t, parsed, tiffTagNumberOfInks, []uint64{4})
	if got := parsed.fields[tiffTagICCProfile].data; !bytes.Equal(got, profile) {
		t.Fatalf("ICC profile = %q, want %q", got, profile)
	}
	for index, want := range values {
		got := [4]uint16{
			parsed.sample(index%2, index/2, 0), parsed.sample(index%2, index/2, 1),
			parsed.sample(index%2, index/2, 2), parsed.sample(index%2, index/2, 3),
		}
		if got != [4]uint16{want.C, want.M, want.Y, want.K} {
			t.Fatalf("CMYK pixel %d = %v, want %#v; native 16-bit data must not be dithered", index, got, want)
		}
	}
}

func TestEncodeTIFFSeekableOnePassAndStreamingTwoPass(t *testing.T) {
	t.Parallel()

	bounds := image.Rect(30, 40, 39, 47)
	seekSource := &testTIFFStripSource{bounds: bounds, format: TIFFPixelFormatRGB, depth: 16}
	seekable := &testMemoryWriteSeeker{}
	options := &TIFFOptions{BitsPerSample: 8, RowsPerStrip: 2, Dither: DitherOptions{Mode: DitherFloydSteinberg}}
	if err := EncodeTIFFStrips(seekable, seekSource, options); err != nil {
		t.Fatal(err)
	}
	if seekSource.calls != 4 {
		t.Fatalf("seekable source calls = %d, want one per strip (4)", seekSource.calls)
	}
	seekParsed := parseTestTIFF(t, seekable.data)
	if seekParsed.ifdOffset <= seekParsed.stripOffsets[len(seekParsed.stripOffsets)-1] {
		t.Fatalf("seekable IFD offset %d is not after strip data %v", seekParsed.ifdOffset, seekParsed.stripOffsets)
	}

	streamSource := &testTIFFStripSource{bounds: bounds, format: TIFFPixelFormatRGB, depth: 16}
	var streaming bytes.Buffer
	if err := EncodeTIFFStrips(&streaming, streamSource, options); err != nil {
		t.Fatal(err)
	}
	if streamSource.calls != 8 {
		t.Fatalf("stream source calls = %d, want two per strip (8)", streamSource.calls)
	}
	streamParsed := parseTestTIFF(t, streaming.Bytes())
	if streamParsed.ifdOffset != 8 {
		t.Fatalf("streaming classic IFD offset = %d, want 8", streamParsed.ifdOffset)
	}
	if !bytes.Equal(seekParsed.pixels, streamParsed.pixels) {
		t.Fatal("seekable and non-seekable pixels differ")
	}
}

func TestTIFFFloydSteinbergContinuesAcrossStrips(t *testing.T) {
	t.Parallel()

	bounds := image.Rect(100, 200, 117, 209)
	encode := func(rows int) *testParsedTIFF {
		source := &testTIFFStripSource{bounds: bounds, format: TIFFPixelFormatRGBA, depth: 16}
		seeker := &testMemoryWriteSeeker{}
		err := EncodeTIFFStrips(seeker, source, &TIFFOptions{
			BitsPerSample: 8, RowsPerStrip: rows, Compression: TIFFCompressionNone,
			Dither: DitherOptions{Mode: DitherFloydSteinberg},
		})
		if err != nil {
			t.Fatal(err)
		}
		return parseTestTIFF(t, seeker.data)
	}
	oneRow := encode(1)
	allRows := encode(bounds.Dy())
	if !bytes.Equal(oneRow.pixels, allRows.pixels) {
		t.Fatal("Floyd-Steinberg residuals reset at a strip boundary")
	}
}

func TestTIFFBayerUsesGlobalCoordinates(t *testing.T) {
	t.Parallel()

	fullBounds := image.Rect(10, 20, 18, 24)
	fullSource := &testTIFFStripSource{bounds: fullBounds, format: TIFFPixelFormatRGB, depth: 16, constant: 0x8080, useConstant: true}
	fullOutput := &testMemoryWriteSeeker{}
	if err := EncodeTIFFStrips(fullOutput, fullSource, &TIFFOptions{
		BitsPerSample: 8, Compression: TIFFCompressionNone, Dither: DitherOptions{Mode: DitherBayer8x8},
	}); err != nil {
		t.Fatal(err)
	}
	full := parseTestTIFF(t, fullOutput.data)

	tileBounds := image.Rect(0, 0, 4, 4)
	tileSource := &testTIFFStripSource{bounds: tileBounds, format: TIFFPixelFormatRGB, depth: 16, constant: 0x8080, useConstant: true}
	tileOutput := &testMemoryWriteSeeker{}
	if err := EncodeTIFFStrips(tileOutput, tileSource, &TIFFOptions{
		BitsPerSample: 8, Compression: TIFFCompressionNone,
		Dither: DitherOptions{Mode: DitherBayer8x8, CoordinateOffset: image.Pt(12, 20)},
	}); err != nil {
		t.Fatal(err)
	}
	tile := parseTestTIFF(t, tileOutput.data)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			for channel := 0; channel < 3; channel++ {
				if got, want := tile.sample(x, y, channel), full.sample(x+2, y, channel); got != want {
					t.Fatalf("tile sample (%d,%d,%d) = %d, global full image = %d", x, y, channel, got, want)
				}
			}
		}
	}
}

func TestEncodeSVGToTIFFPreservesAlphaAndFlattensRGB(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 2 1"><rect width="2" height="1" fill="red" opacity=".5"/></svg>`)
	icon.SetTarget(0, 0, 2, 1)
	encode := func(format TIFFPixelFormat, background color.Color) *testParsedTIFF {
		output := &testMemoryWriteSeeker{}
		err := EncodeSVGToTIFF(output, icon, image.Rect(0, 0, 2, 1), &TIFFRenderOptions{
			TIFFOptions: TIFFOptions{Format: format, BitsPerSample: 8, Compression: TIFFCompressionNone},
			Background:  background,
		})
		if err != nil {
			t.Fatal(err)
		}
		return parseTestTIFF(t, output.data)
	}
	rgba := encode(TIFFPixelFormatAuto, nil)
	if rgba.samples != 4 || rgba.sample(0, 0, 0) < 250 || rgba.sample(0, 0, 3) < 126 || rgba.sample(0, 0, 3) > 129 {
		t.Fatalf("RGBA SVG pixel = [%d %d %d %d]", rgba.sample(0, 0, 0), rgba.sample(0, 0, 1), rgba.sample(0, 0, 2), rgba.sample(0, 0, 3))
	}
	rgb := encode(TIFFPixelFormatRGB, color.NRGBA{B: 255, A: 255})
	if rgb.samples != 3 || rgb.sample(0, 0, 0) < 126 || rgb.sample(0, 0, 0) > 129 || rgb.sample(0, 0, 2) < 126 || rgb.sample(0, 0, 2) > 129 {
		t.Fatalf("flattened RGB SVG pixel = [%d %d %d]", rgb.sample(0, 0, 0), rgb.sample(0, 0, 1), rgb.sample(0, 0, 2))
	}
}

func TestEncodeSVGToTIFFDithersFloatResidualsAt16Bit(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 64 8"><defs><linearGradient id="g"><stop stop-color="#123456"/><stop offset="1" stop-color="#edcba9"/></linearGradient></defs><rect width="64" height="8" fill="url(#g)"/></svg>`)
	icon.SetTarget(0, 0, 64, 8)
	encode := func(mode DitherMode, rows int) *testParsedTIFF {
		output := &testMemoryWriteSeeker{}
		err := EncodeSVGToTIFF(output, icon, image.Rect(0, 0, 64, 8), &TIFFRenderOptions{TIFFOptions: TIFFOptions{
			Format: TIFFPixelFormatRGB, BitsPerSample: 16, Compression: TIFFCompressionNone,
			RowsPerStrip: rows, Dither: DitherOptions{Mode: mode},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return parseTestTIFF(t, output.data)
	}
	nearest := encode(DitherNone, 8)
	bayer := encode(DitherBayer8x8, 8)
	floydOneRow := encode(DitherFloydSteinberg, 1)
	floydAllRows := encode(DitherFloydSteinberg, 8)
	if bytes.Equal(nearest.pixels, bayer.pixels) {
		t.Fatal("16-bit Bayer output ignored SVG floating-point residuals")
	}
	if bytes.Equal(nearest.pixels, floydOneRow.pixels) {
		t.Fatal("16-bit Floyd-Steinberg output ignored SVG floating-point residuals")
	}
	if !bytes.Equal(floydOneRow.pixels, floydAllRows.pixels) {
		t.Fatal("16-bit Floyd-Steinberg output changed with RowsPerStrip")
	}
}

func TestEncodeSVGToTIFFCMYKNative16TagsAndSamples(t *testing.T) {
	t.Parallel()

	icon := mustReadSVG(t, `<svg viewBox="0 0 16 2"><defs><linearGradient id="g"><stop stop-color="#ff2000"/><stop offset="1" stop-color="#0040ff"/></linearGradient></defs><rect width="16" height="2" fill="url(#g)"/></svg>`)
	icon.SetTarget(0, 0, 16, 2)
	output := &testMemoryWriteSeeker{}
	err := EncodeSVGToTIFF(output, icon, image.Rect(0, 0, 16, 2), &TIFFRenderOptions{
		TIFFOptions: TIFFOptions{
			Format: TIFFPixelFormatCMYK, BitsPerSample: 16, Compression: TIFFCompressionDeflate,
			RowsPerStrip: 1, Dither: DitherOptions{Mode: DitherBayer8x8},
		},
		CMYKOptions: CMYKOptions{ProcessingMode: CMYKNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseTestTIFF(t, output.data)
	assertTestTIFFValues(t, parsed, tiffTagPhotometricInterpretation, []uint64{5})
	assertTestTIFFValues(t, parsed, tiffTagBitsPerSample, []uint64{16, 16, 16, 16})
	assertTestTIFFValues(t, parsed, tiffTagInkSet, []uint64{1})
	assertTestTIFFValues(t, parsed, tiffTagNumberOfInks, []uint64{4})
	foundResidual := false
	for x := 1; x < 15; x++ {
		for channel := 0; channel < 4; channel++ {
			if parsed.sample(x, 0, channel)%0x101 != 0 {
				foundResidual = true
			}
		}
	}
	if !foundResidual {
		t.Fatal("native CMYK gradient was reduced to 8-bit-expanded samples before 16-bit TIFF output")
	}
}

func TestEncodeTIFFErrorsAreBoundedAndExplicit(t *testing.T) {
	t.Parallel()

	valid := &testTIFFStripSource{bounds: image.Rect(0, 0, 2, 2), format: TIFFPixelFormatRGB, depth: 8}
	if err := EncodeTIFFStrips(testShortWriter{}, valid, &TIFFOptions{Compression: TIFFCompressionNone}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer error = %v, want io.ErrShortWrite", err)
	}
	for _, compression := range []TIFFCompression{TIFFCompressionNone, TIFFCompressionPackBits} {
		err := EncodeTIFFStrips(io.Discard, valid, &TIFFOptions{Compression: compression, Predictor: TIFFPredictorHorizontal})
		if err == nil || !strings.Contains(err.Error(), "requires Deflate") {
			t.Fatalf("horizontal predictor with compression %d error = %v", compression, err)
		}
	}
	outOfRange := &testTIFFStripSource{
		bounds: image.Rect(0, 0, 1, 1), format: TIFFPixelFormatRGB, depth: 8, constant: 256, useConstant: true,
	}
	if err := EncodeTIFFStrips(io.Discard, outOfRange, &TIFFOptions{Compression: TIFFCompressionNone}); err == nil || !strings.Contains(err.Error(), "exceeds 8-bit range") {
		t.Fatalf("out-of-range source error = %v", err)
	}

	changing := &testTIFFStripSource{
		bounds: image.Rect(0, 0, 128, 1), format: TIFFPixelFormatRGB, depth: 8, changeAfter: 1,
	}
	var output bytes.Buffer
	err := EncodeTIFFStrips(&output, changing, &TIFFOptions{BitsPerSample: 8, Compression: TIFFCompressionDeflate})
	if err == nil || !strings.Contains(err.Error(), "source changed between passes") {
		t.Fatalf("nondeterministic source error = %v", err)
	}

	job := &tiffJob{
		width: 1, height: 1, rowsPerStrip: 1, stripCount: 1, samples: 3, bits: 8,
		format: TIFFPixelFormatRGB, compression: TIFFCompressionNone, order: binary.LittleEndian,
	}
	if fits, err := job.classicSeekableFits([]uint64{math.MaxUint32}); fits || !errors.Is(err, ErrTIFFClassicTooLarge) {
		t.Fatalf("classic overflow = fits:%v err:%v", fits, err)
	}
	invalidSpan := &testTIFFStripSource{
		bounds: image.Rectangle{Min: image.Pt(math.MinInt, 0), Max: image.Pt(1, 1)},
		format: TIFFPixelFormatRGB, depth: 8,
	}
	if err := EncodeTIFFStrips(io.Discard, invalidSpan, nil); err == nil || !strings.Contains(err.Error(), "unrepresentable dimension span") {
		t.Fatalf("dimension-span error = %v", err)
	}
	tooManyStrips := &testTIFFStripSource{
		bounds: image.Rect(0, 0, 1, maxTIFFStripCount+1), format: TIFFPixelFormatRGB, depth: 8,
	}
	if err := EncodeTIFFStrips(io.Discard, tooManyStrips, &TIFFOptions{RowsPerStrip: 1}); err == nil || !strings.Contains(err.Error(), "increase RowsPerStrip") {
		t.Fatalf("strip-table limit error = %v", err)
	}
	oversizedStrip := &testTIFFStripSource{
		bounds: image.Rect(0, 0, 100_000, 100), format: TIFFPixelFormatRGBA, depth: 8,
	}
	if err := EncodeTIFFStrips(io.Discard, oversizedStrip, &TIFFOptions{RowsPerStrip: 100}); err == nil || !strings.Contains(err.Error(), "strip source buffer exceeds") {
		t.Fatalf("strip-buffer limit error = %v", err)
	}
	overflowDither := &testTIFFStripSource{
		bounds: image.Rect(math.MaxInt-1, 0, math.MaxInt, 1), format: TIFFPixelFormatRGB, depth: 16,
	}
	if err := EncodeTIFFStrips(io.Discard, overflowDither, &TIFFOptions{
		BitsPerSample: 8, Compression: TIFFCompressionNone,
		Dither: DitherOptions{Mode: DitherBayer8x8, CoordinateOffset: image.Pt(2, 0)},
	}); err == nil || !strings.Contains(err.Error(), "dither coordinate offset") {
		t.Fatalf("dither-coordinate overflow error = %v", err)
	}
}

func TestTIFFFloydStateIsTransactional(t *testing.T) {
	t.Parallel()

	state := &FloydSteinbergState{}
	seed := &testTIFFStripSource{
		bounds: image.Rect(0, 0, 8, 2), format: TIFFPixelFormatRGB, depth: 16,
	}
	if err := EncodeTIFFStrips(&testMemoryWriteSeeker{}, seed, &TIFFOptions{
		BitsPerSample: 8, Compression: TIFFCompressionNone, RowsPerStrip: 1,
		Dither: DitherOptions{Mode: DitherFloydSteinberg, State: state},
	}); err != nil {
		t.Fatal(err)
	}
	wantCurrent := append([]float64(nil), state.current...)
	wantNext := append([]float64(nil), state.next...)
	wantNextY := state.nextY
	source := &testTIFFStripSource{
		bounds: image.Rect(0, 2, 8, 5), format: TIFFPixelFormatRGB, depth: 16, failCall: 2,
	}
	err := EncodeTIFFStrips(&testMemoryWriteSeeker{}, source, &TIFFOptions{
		BitsPerSample: 8, Compression: TIFFCompressionNone, RowsPerStrip: 1,
		Dither: DitherOptions{Mode: DitherFloydSteinberg, State: state},
	})
	if err == nil || !strings.Contains(err.Error(), "injected source failure") {
		t.Fatalf("source failure = %v", err)
	}
	if !state.initialized || state.nextY != wantNextY || !equalTIFFFloatSlices(state.current, wantCurrent) || !equalTIFFFloatSlices(state.next, wantNext) {
		t.Fatal("failed TIFF encode mutated caller Floyd-Steinberg state")
	}
	source.failCall = 0
	source.calls = 0
	if err := EncodeTIFFStrips(&testMemoryWriteSeeker{}, source, &TIFFOptions{
		BitsPerSample: 8, Compression: TIFFCompressionNone, RowsPerStrip: 1,
		Dither: DitherOptions{Mode: DitherFloydSteinberg, State: state},
	}); err != nil {
		t.Fatal(err)
	}
	if !state.initialized || state.nextY != 5 {
		t.Fatalf("successful TIFF encode state = initialized:%v nextY:%d", state.initialized, state.nextY)
	}
}

func equalTIFFFloatSlices(left, right []float64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestSvgTIFFAutoRowsBoundWideCMYKWorkingSet(t *testing.T) {
	t.Parallel()

	rows := svgTIFFRowsPerStrip(100_000, 100_000, 9)
	if rows < 1 || rows > 2 {
		t.Fatalf("100k-wide CMYK rows per strip = %d, want 1 or 2", rows)
	}
}

type testTIFFStripSource struct {
	bounds      image.Rectangle
	format      TIFFPixelFormat
	depth       int
	constant    uint32
	useConstant bool
	calls       int
	changeAfter int
	maximumRows int
	failCall    int
}

func (s *testTIFFStripSource) TIFFBounds() image.Rectangle { return s.bounds }
func (s *testTIFFStripSource) TIFFFormat() TIFFPixelFormat { return s.format }
func (s *testTIFFStripSource) TIFFSampleDepth() int        { return s.depth }

func (s *testTIFFStripSource) TIFFReadStrip(rect image.Rectangle, dst []uint32) error {
	s.calls++
	if s.failCall != 0 && s.calls == s.failCall {
		return errors.New("injected source failure")
	}
	s.maximumRows = max(s.maximumRows, rect.Dy())
	channels := tiffSamplesPerPixel(s.format)
	maximum := uint64(math.MaxUint32)
	if s.depth < 32 {
		maximum = (uint64(1) << s.depth) - 1
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			for channel := 0; channel < channels; channel++ {
				index := ((y-rect.Min.Y)*rect.Dx()+(x-rect.Min.X))*channels + channel
				if s.useConstant {
					dst[index] = s.constant
				} else if s.changeAfter != 0 {
					if s.calls > s.changeAfter {
						dst[index] = uint32((index*73 + 19) & int(maximum))
					} else {
						dst[index] = 0
					}
				} else {
					value := uint64((x-s.bounds.Min.X)*257+(y-s.bounds.Min.Y)*997+channel*1111) * 131
					dst[index] = uint32(value % (maximum + 1))
				}
			}
		}
	}
	return nil
}

type testShortWriter struct{}

func (testShortWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return len(data) - 1, nil
}

type testMemoryWriteSeeker struct {
	data     []byte
	position int64
}

func (w *testMemoryWriteSeeker) Write(data []byte) (int, error) {
	if w.position < 0 || w.position > int64(len(w.data)) {
		return 0, fmt.Errorf("invalid test seek position %d", w.position)
	}
	end := w.position + int64(len(data))
	if end > int64(len(w.data)) {
		w.data = append(w.data, make([]byte, int(end)-len(w.data))...)
	}
	copy(w.data[w.position:end], data)
	w.position = end
	return len(data), nil
}

func (w *testMemoryWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	position := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		position = w.position + offset
	case io.SeekEnd:
		position = int64(len(w.data)) + offset
	default:
		return 0, fmt.Errorf("invalid whence %d", whence)
	}
	if position < 0 {
		return 0, fmt.Errorf("negative seek position %d", position)
	}
	w.position = position
	return position, nil
}

type testTIFFField struct {
	type_ uint16
	count uint64
	data  []byte
}

type testParsedTIFF struct {
	data         []byte
	order        binary.ByteOrder
	big          bool
	ifdOffset    uint64
	fields       map[uint16]testTIFFField
	width        int
	height       int
	bits         int
	samples      int
	rowsPerStrip int
	compression  int
	predictor    int
	stripOffsets []uint64
	stripCounts  []uint64
	pixels       []byte
}

func parseTestTIFF(t *testing.T, data []byte) *testParsedTIFF {
	t.Helper()
	if len(data) < 8 {
		t.Fatalf("TIFF has only %d bytes", len(data))
	}
	parsed := &testParsedTIFF{data: data, fields: make(map[uint16]testTIFFField)}
	switch string(data[:2]) {
	case "II":
		parsed.order = binary.LittleEndian
	case "MM":
		parsed.order = binary.BigEndian
	default:
		t.Fatalf("invalid TIFF byte order %q", data[:2])
	}
	magic := parsed.order.Uint16(data[2:4])
	switch magic {
	case 42:
		parsed.ifdOffset = uint64(parsed.order.Uint32(data[4:8]))
	case 43:
		parsed.big = true
		if len(data) < 16 || parsed.order.Uint16(data[4:6]) != 8 || parsed.order.Uint16(data[6:8]) != 0 {
			t.Fatal("invalid BigTIFF header")
		}
		parsed.ifdOffset = parsed.order.Uint64(data[8:16])
	default:
		t.Fatalf("TIFF magic = %d", magic)
	}
	if parsed.ifdOffset >= uint64(len(data)) {
		t.Fatalf("IFD offset %d outside %d bytes", parsed.ifdOffset, len(data))
	}
	position := int(parsed.ifdOffset)
	entryCount, countSize, entrySize, inline := uint64(0), 2, 12, 4
	if parsed.big {
		if position+8 > len(data) {
			t.Fatal("truncated BigTIFF IFD count")
		}
		entryCount, countSize, entrySize, inline = parsed.order.Uint64(data[position:position+8]), 8, 20, 8
	} else {
		if position+2 > len(data) {
			t.Fatal("truncated TIFF IFD count")
		}
		entryCount = uint64(parsed.order.Uint16(data[position : position+2]))
	}
	position += countSize
	if entryCount > uint64((len(data)-position)/entrySize) {
		t.Fatalf("IFD entry count %d is truncated", entryCount)
	}
	for index := uint64(0); index < entryCount; index++ {
		entry := data[position : position+entrySize]
		position += entrySize
		tag := parsed.order.Uint16(entry[0:2])
		type_ := parsed.order.Uint16(entry[2:4])
		typeSize := testTIFFTypeSize(t, type_)
		var count, valueOffset uint64
		valuePosition := 8
		if parsed.big {
			count = parsed.order.Uint64(entry[4:12])
			valueOffset = parsed.order.Uint64(entry[12:20])
			valuePosition = 12
		} else {
			count = uint64(parsed.order.Uint32(entry[4:8]))
			valueOffset = uint64(parsed.order.Uint32(entry[8:12]))
		}
		if count != 0 && count > math.MaxUint64/uint64(typeSize) {
			t.Fatalf("tag %d data size overflows", tag)
		}
		length := count * uint64(typeSize)
		var fieldData []byte
		if length <= uint64(inline) {
			fieldData = append([]byte(nil), entry[valuePosition:valuePosition+int(length)]...)
		} else {
			if valueOffset > uint64(len(data)) || length > uint64(len(data))-valueOffset {
				t.Fatalf("tag %d data [%d,%d) outside file", tag, valueOffset, valueOffset+length)
			}
			fieldData = append([]byte(nil), data[valueOffset:valueOffset+length]...)
		}
		parsed.fields[tag] = testTIFFField{type_: type_, count: count, data: fieldData}
	}
	parsed.width = int(testTIFFValues(t, parsed, tiffTagImageWidth)[0])
	parsed.height = int(testTIFFValues(t, parsed, tiffTagImageLength)[0])
	bits := testTIFFValues(t, parsed, tiffTagBitsPerSample)
	parsed.bits = int(bits[0])
	parsed.samples = int(testTIFFValues(t, parsed, tiffTagSamplesPerPixel)[0])
	parsed.rowsPerStrip = int(testTIFFValues(t, parsed, tiffTagRowsPerStrip)[0])
	parsed.compression = int(testTIFFValues(t, parsed, tiffTagCompression)[0])
	if _, ok := parsed.fields[tiffTagPredictor]; ok {
		parsed.predictor = int(testTIFFValues(t, parsed, tiffTagPredictor)[0])
	} else {
		parsed.predictor = 1
	}
	parsed.stripOffsets = testTIFFValues(t, parsed, tiffTagStripOffsets)
	parsed.stripCounts = testTIFFValues(t, parsed, tiffTagStripByteCounts)
	parsed.decodePixels(t)
	return parsed
}

func (p *testParsedTIFF) decodePixels(t *testing.T) {
	t.Helper()
	if len(p.stripOffsets) != len(p.stripCounts) {
		t.Fatalf("strip offsets/counts = %d/%d", len(p.stripOffsets), len(p.stripCounts))
	}
	bytesPerSample := p.bits / 8
	lineLength := p.width * p.samples * bytesPerSample
	for strip := range p.stripOffsets {
		offset, count := p.stripOffsets[strip], p.stripCounts[strip]
		if offset > uint64(len(p.data)) || count > uint64(len(p.data))-offset {
			t.Fatalf("strip %d [%d,%d) outside file", strip, offset, offset+count)
		}
		compressed := p.data[offset : offset+count]
		var raw []byte
		switch p.compression {
		case 1:
			raw = append([]byte(nil), compressed...)
		case 8:
			reader, err := zlib.NewReader(bytes.NewReader(compressed))
			if err != nil {
				t.Fatalf("strip %d zlib: %v", strip, err)
			}
			raw, err = io.ReadAll(reader)
			closeErr := reader.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("strip %d zlib read/close: %v/%v", strip, err, closeErr)
			}
		case 32773:
			raw = unpackTestTIFFPackBits(t, compressed)
		default:
			t.Fatalf("unsupported test compression %d", p.compression)
		}
		rows := min(p.rowsPerStrip, p.height-strip*p.rowsPerStrip)
		if want := rows * lineLength; len(raw) != want {
			t.Fatalf("strip %d raw length = %d, want %d", strip, len(raw), want)
		}
		if p.predictor == 2 {
			for row := 0; row < rows; row++ {
				line := raw[row*lineLength : (row+1)*lineLength]
				for x := 1; x < p.width; x++ {
					for channel := 0; channel < p.samples; channel++ {
						index := (x*p.samples + channel) * bytesPerSample
						previous := index - p.samples*bytesPerSample
						if p.bits == 8 {
							line[index] += line[previous]
						} else {
							value := p.order.Uint16(line[index:index+2]) + p.order.Uint16(line[previous:previous+2])
							p.order.PutUint16(line[index:index+2], value)
						}
					}
				}
			}
		}
		p.pixels = append(p.pixels, raw...)
	}
	if want := p.height * lineLength; len(p.pixels) != want {
		t.Fatalf("decoded pixel bytes = %d, want %d", len(p.pixels), want)
	}
}

func (p *testParsedTIFF) sample(x, y, channel int) uint16 {
	bytesPerSample := p.bits / 8
	index := ((y*p.width+x)*p.samples + channel) * bytesPerSample
	if p.bits == 8 {
		return uint16(p.pixels[index])
	}
	return p.order.Uint16(p.pixels[index : index+2])
}

func testTIFFValues(t *testing.T, parsed *testParsedTIFF, tag uint16) []uint64 {
	t.Helper()
	field, ok := parsed.fields[tag]
	if !ok {
		t.Fatalf("missing TIFF tag %d", tag)
	}
	values := make([]uint64, field.count)
	switch field.type_ {
	case tiffTypeByte, tiffTypeUndefined:
		for index := range values {
			values[index] = uint64(field.data[index])
		}
	case tiffTypeShort:
		for index := range values {
			values[index] = uint64(parsed.order.Uint16(field.data[index*2 : index*2+2]))
		}
	case tiffTypeLong:
		for index := range values {
			values[index] = uint64(parsed.order.Uint32(field.data[index*4 : index*4+4]))
		}
	case tiffTypeLong8:
		for index := range values {
			values[index] = parsed.order.Uint64(field.data[index*8 : index*8+8])
		}
	default:
		t.Fatalf("unsupported test TIFF type %d for tag %d", field.type_, tag)
	}
	return values
}

func assertTestTIFFValues(t *testing.T, parsed *testParsedTIFF, tag uint16, want []uint64) {
	t.Helper()
	got := testTIFFValues(t, parsed, tag)
	if len(got) != len(want) {
		t.Fatalf("tag %d values = %v, want %v", tag, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("tag %d values = %v, want %v", tag, got, want)
		}
	}
}

func testTIFFTypeSize(t *testing.T, type_ uint16) int {
	t.Helper()
	switch type_ {
	case tiffTypeByte, tiffTypeASCII, tiffTypeUndefined:
		return 1
	case tiffTypeShort:
		return 2
	case tiffTypeLong:
		return 4
	case tiffTypeLong8:
		return 8
	default:
		t.Fatalf("unsupported TIFF field type %d", type_)
		return 0
	}
}

func unpackTestTIFFPackBits(t *testing.T, data []byte) []byte {
	t.Helper()
	var output []byte
	for index := 0; index < len(data); {
		control := int8(data[index])
		index++
		switch {
		case control >= 0:
			length := int(control) + 1
			if length > len(data)-index {
				t.Fatal("truncated PackBits literal")
			}
			output = append(output, data[index:index+length]...)
			index += length
		case control >= -127:
			if index >= len(data) {
				t.Fatal("truncated PackBits run")
			}
			length := 1 - int(control)
			for range length {
				output = append(output, data[index])
			}
			index++
		case control == -128:
		}
	}
	return output
}
