package coolsvg

import (
	"image"
	"image/color"
	"io"
	"testing"
)

const benchmarkHighPrecisionGradientSVG = `<svg viewBox="0 0 512 256">
<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="512" y2="256">
<stop stop-color="#061a40"/><stop offset=".29" stop-color="#00b4d8"/>
<stop offset=".63" stop-color="#ffd166"/><stop offset="1" stop-color="#ef476f"/>
</linearGradient></defs>
<rect width="512" height="256" fill="url(#g)"/>
</svg>`

var benchmarkPrecisionSink uint64

func BenchmarkRender64Gradient(b *testing.B) {
	const width, height = 512, 256
	icon := benchmarkIcon(b, benchmarkHighPrecisionGradientSVG, width, height)
	destination := image.NewNRGBA64(image.Rect(0, 0, width, height))
	b.SetBytes(width * height * 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := icon.Render64(destination, RenderOptions{}); err != nil {
			b.Fatal(err)
		}
	}
	pixel := destination.NRGBA64At(width/2, height/2)
	benchmarkPrecisionSink = uint64(pixel.R)<<48 | uint64(pixel.G)<<32 | uint64(pixel.B)<<16 | uint64(pixel.A)
}

func BenchmarkQuantizeRGBA64Bands(b *testing.B) {
	const width, height, bandHeight = 512, 256, 32
	bounds := image.Rect(0, 0, width, height)
	source := image.NewNRGBA64(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			source.SetNRGBA64(x, y, color.NRGBA64{
				R: uint16((x*193 + y*977) & 0xffff),
				G: uint16((x*701 + y*313) & 0xffff),
				B: uint16((x*431 + y*587) & 0xffff),
				A: 0xffff,
			})
		}
	}
	type imageBand struct {
		source      *image.NRGBA64
		destination *image.NRGBA
	}
	for _, benchmark := range []struct {
		name string
		mode DitherMode
	}{
		{name: "Bayer8x8", mode: DitherBayer8x8},
		{name: "FloydSteinbergContinuous", mode: DitherFloydSteinberg},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			destination := image.NewNRGBA(bounds)
			bands := make([]imageBand, 0, height/bandHeight)
			for minimumY := 0; minimumY < height; minimumY += bandHeight {
				band := image.Rect(0, minimumY, width, min(height, minimumY+bandHeight))
				bands = append(bands, imageBand{
					source:      source.SubImage(band).(*image.NRGBA64),
					destination: destination.SubImage(band).(*image.NRGBA),
				})
			}
			options := DitherOptions{Mode: benchmark.mode}
			if benchmark.mode == DitherFloydSteinberg {
				options.State = &FloydSteinbergState{}
			}
			b.SetBytes(width * height * 12)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Treat every iteration as the next vertical segment of one image.
				// This keeps Floyd-Steinberg residuals continuous across both bands
				// and benchmark iterations without timing a state reset.
				options.CoordinateOffset.Y = i * height
				for _, band := range bands {
					if err := QuantizeRGBA64(band.destination, band.source, options); err != nil {
						b.Fatal(err)
					}
				}
			}
			benchmarkPrecisionSink = uint64(destination.NRGBAAt(width/2, height/2).R)
		})
	}
}

func BenchmarkEncodeTIFFStreaming(b *testing.B) {
	const width, height = 512, 256
	source := benchmarkTIFFGradientSource{bounds: image.Rect(0, 0, width, height)}
	options := &TIFFOptions{
		Format:        TIFFPixelFormatRGB,
		BitsPerSample: 16,
		Compression:   TIFFCompressionDeflate,
		RowsPerStrip:  32,
	}
	b.SetBytes(width * height * 3 * 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// io.Discard is deliberately non-seekable, selecting the bounded
		// two-pass streaming path without retaining encoded output.
		if err := EncodeTIFFStrips(io.Discard, &source, options); err != nil {
			b.Fatal(err)
		}
	}
}

type benchmarkTIFFGradientSource struct {
	bounds image.Rectangle
}

func (s *benchmarkTIFFGradientSource) TIFFBounds() image.Rectangle { return s.bounds }
func (s *benchmarkTIFFGradientSource) TIFFFormat() TIFFPixelFormat { return TIFFPixelFormatRGB }
func (s *benchmarkTIFFGradientSource) TIFFSampleDepth() int        { return 16 }

func (s *benchmarkTIFFGradientSource) TIFFReadStrip(rect image.Rectangle, destination []uint32) error {
	offset := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		localY := y - s.bounds.Min.Y
		for x := rect.Min.X; x < rect.Max.X; x++ {
			localX := x - s.bounds.Min.X
			destination[offset+0] = uint32((localX*193 + localY*977) & 0xffff)
			destination[offset+1] = uint32((localX*701 + localY*313) & 0xffff)
			destination[offset+2] = uint32((localX*431 + localY*587) & 0xffff)
			offset += 3
		}
	}
	return nil
}
