package coolsvg

import (
	"image"
	"strings"
	"testing"
)

const benchmarkOrdinarySVG = `<svg viewBox="0 0 64 64">
<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="4" y1="4" x2="60" y2="60">
<stop stop-color="#ff7a00"/><stop offset=".5" stop-color="#21a179"/><stop offset="1" stop-color="#334dff"/>
</linearGradient></defs>
<rect x="3" y="3" width="58" height="58" rx="9" fill="url(#g)"/>
<path d="M8 48C14 12 50 12 56 48L32 19Z" fill="white" fill-opacity=".55"/>
<circle cx="32" cy="34" r="8" fill="#172a3a"/>
</svg>`

const benchmarkClippedSVG = `<svg viewBox="0 0 64 64">
<defs>
  <radialGradient id="g"><stop stop-color="#fff3b0"/><stop offset=".65" stop-color="#e09f3e"/><stop offset="1" stop-color="#9e2a2b"/></radialGradient>
  <clipPath id="outer"><circle cx="32" cy="32" r="28"/><rect x="2" y="27" width="60" height="10"/></clipPath>
  <clipPath id="inner"><path d="M7 32L32 7L57 32L32 57Z"/></clipPath>
</defs>
<g clip-path="url(#outer)" opacity=".72">
  <rect width="64" height="64" fill="url(#g)"/>
  <g clip-path="url(#inner)" transform="rotate(11 32 32)">
    <path d="M4 48C12 5 52 5 60 48L32 16Z" fill="#335c67"/>
    <circle cx="32" cy="34" r="12" fill="#fff" fill-opacity=".6"/>
  </g>
</g>
</svg>`

func BenchmarkRenderOrdinary(b *testing.B) {
	icon := benchmarkIcon(b, benchmarkOrdinarySVG, 256, 256)
	destination := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := icon.Render(destination); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderClipped(b *testing.B) {
	icon := benchmarkIcon(b, benchmarkClippedSVG, 256, 256)
	destination := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := icon.Render(destination); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderRegion(b *testing.B) {
	icon := benchmarkIcon(b, benchmarkClippedSVG, 4096, 4096)
	region := image.Rect(1984, 1984, 2112, 2112)
	destination := image.NewNRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := icon.RenderRegion(destination, region); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderCMYKRegion(b *testing.B) {
	icon := benchmarkIcon(b, benchmarkClippedSVG, 4096, 4096)
	region := image.Rect(1984, 1984, 2112, 2112)
	for _, benchmark := range []struct {
		name    string
		options CMYKOptions
	}{
		{name: "post_convert"},
		{name: "native", options: CMYKOptions{ProcessingMode: CMYKNative}},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			destination := image.NewCMYK(image.Rect(0, 0, region.Dx(), region.Dy()))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := icon.RenderCMYKRegion(destination, region, benchmark.options); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMaskOR(b *testing.B) {
	benchmarkMaskOperation(b, maskOr)
}

func BenchmarkMaskAND(b *testing.B) {
	benchmarkMaskOperation(b, maskAnd)
}

var benchmarkMaskSink uint16

func benchmarkMaskOperation(b *testing.B, operation func([]uint16, []uint16)) {
	for _, size := range []int{256, 4096, 65_536} {
		b.Run(maskBenchmarkName(size), func(b *testing.B) {
			destination := make([]uint16, size)
			source := make([]uint16, size)
			for i := range destination {
				destination[i] = uint16(i*17) ^ 0x5a5a
				source[i] = uint16(i*29) ^ 0xa5a5
			}
			b.SetBytes(int64(size * 4))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				operation(destination, source)
			}
			benchmarkMaskSink = destination[size/2]
		})
	}
}

func maskBenchmarkName(size int) string {
	switch size {
	case 256:
		return "256_values"
	case 4096:
		return "4K_values"
	default:
		return "64K_values"
	}
}

func benchmarkIcon(b *testing.B, source string, width, height int) *SvgIcon {
	b.Helper()
	icon, err := ReadIconStream(strings.NewReader(source), StrictErrorMode)
	if err != nil {
		b.Fatal(err)
	}
	icon.SetTarget(0, 0, float64(width), float64(height))
	return icon
}
