package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/image/tiff"
)

const gradientSVG = `<svg viewBox="0 0 31 9" xmlns="http://www.w3.org/2000/svg">
<defs><linearGradient id="g" x1="0" y1="0" x2="100%" y2="0">
<stop offset="0" stop-color="#07192b" stop-opacity="0.25"/>
<stop offset="1" stop-color="#e6c4a2" stop-opacity="0.9"/>
</linearGradient></defs><rect width="31" height="9" fill="url(#g)"/></svg>`

func TestRunRendersStandardInput(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "icon.png")
	source := strings.NewReader(`<svg viewBox="0 0 2 2"><rect width="2" height="2" fill="#123456"/></svg>`)
	var stderr bytes.Buffer
	if err := run([]string{"-strict", "-width", "4", "-height", "6", "-o", output, "-"}, source, &stderr); err != nil {
		t.Fatalf("run: %v (%s)", err, stderr.String())
	}
	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 4 || img.Bounds().Dy() != 6 {
		t.Fatalf("PNG bounds = %v", img.Bounds())
	}
}

func TestRunOutputDepthAndDitherMatrix(t *testing.T) {
	formats := []struct {
		name      string
		extension string
	}{
		{name: "png", extension: ".png"},
		{name: "tiff", extension: ".tiff"},
	}
	dithers := []string{"none", "bayer", "floyd-steinberg"}
	for _, format := range formats {
		for _, depth := range []int{8, 16} {
			for _, dither := range dithers {
				name := fmt.Sprintf("%s/%d/%s", format.name, depth, dither)
				t.Run(name, func(t *testing.T) {
					output := filepath.Join(t.TempDir(), "matrix"+format.extension)
					err := run([]string{
						"-strict", "-format", format.name,
						"-bit-depth", strconv.Itoa(depth), "-dither", dither,
						"-o", output, "-",
					}, strings.NewReader(gradientSVG), io.Discard)
					if err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(output)
					if err != nil {
						t.Fatal(err)
					}
					if format.name == "png" {
						assertPNGHeader(t, data, 31, 9, depth, 6)
					} else {
						assertTIFFHeader(t, data, 31, 9, depth, 2, 4)
					}
				})
			}
		}
	}
}

func TestRunTIFFCompressionAndColorModels(t *testing.T) {
	tests := []struct {
		name        string
		arguments   []string
		compression uint16
		photometric uint16
		samples     uint16
	}{
		{
			name: "rgba-deflate", arguments: []string{"-tiff-compression", "deflate"},
			compression: 8, photometric: 2, samples: 4,
		},
		{
			name: "rgb-none", arguments: []string{"-tiff-compression", "none", "-color-model", "rgb"},
			compression: 1, photometric: 2, samples: 3,
		},
		{
			name: "cmyk-post-convert", arguments: []string{"-color-model", "cmyk", "-cmyk-processing", "post-convert"},
			compression: 8, photometric: 5, samples: 4,
		},
		{
			name: "cmyk-native", arguments: []string{"-color-model", "cmyk", "-cmyk-processing", "native", "-dither", "bayer"},
			compression: 8, photometric: 5, samples: 4,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, depth := range []int{8, 16} {
				t.Run(strconv.Itoa(depth), func(t *testing.T) {
					output := filepath.Join(t.TempDir(), "color.tiff")
					arguments := []string{"-strict", "-format", "tiff", "-bit-depth", strconv.Itoa(depth), "-o", output}
					arguments = append(arguments, test.arguments...)
					arguments = append(arguments, "-")
					if err := run(arguments, strings.NewReader(gradientSVG), io.Discard); err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(output)
					if err != nil {
						t.Fatal(err)
					}
					assertTIFFHeader(t, data, 31, 9, depth, test.photometric, test.samples)
					if got := singleTIFFShort(t, data, 259); got != test.compression {
						t.Fatalf("TIFF compression = %d, want %d", got, test.compression)
					}
				})
			}
		})
	}
}

func TestRunPNG16RetainsMoreThanEightBits(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "precision.png")
	if err := run([]string{"-strict", "-bit-depth", "16", "-o", output, "-"}, strings.NewReader(gradientSVG), io.Discard); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	for y := decoded.Bounds().Min.Y; y < decoded.Bounds().Max.Y; y++ {
		for x := decoded.Bounds().Min.X; x < decoded.Bounds().Max.X; x++ {
			pixel := color.NRGBA64Model.Convert(decoded.At(x, y)).(color.NRGBA64)
			if pixel.R%0x101 != 0 || pixel.G%0x101 != 0 || pixel.B%0x101 != 0 || pixel.A%0x101 != 0 {
				return
			}
		}
	}
	t.Fatal("16-bit PNG contains only replicated 8-bit samples")
}

func TestRunPNGColorModels(t *testing.T) {
	source := `<svg viewBox="0 0 2 2"><rect width="2" height="2" fill="#336699" fill-opacity="0.5"/></svg>`
	for _, depth := range []int{8, 16} {
		for _, test := range []struct {
			model     string
			colorType byte
		}{
			{model: "rgba", colorType: 6},
			{model: "rgb", colorType: 2},
		} {
			t.Run(fmt.Sprintf("%s/%d", test.model, depth), func(t *testing.T) {
				output := filepath.Join(t.TempDir(), "color.png")
				if err := run([]string{
					"-strict", "-bit-depth", strconv.Itoa(depth), "-color-model", test.model,
					"-o", output, "-",
				}, strings.NewReader(source), io.Discard); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(output)
				if err != nil {
					t.Fatal(err)
				}
				assertPNGHeader(t, data, 2, 2, depth, test.colorType)
			})
		}
	}
}

func TestRunDitherModesAreDeterministicAndDistinct(t *testing.T) {
	t.Parallel()

	source := `<svg viewBox="0 0 64 8"><defs><linearGradient id="g"><stop offset="0" stop-color="#000000"/><stop offset="1" stop-color="#010101"/></linearGradient></defs><rect width="64" height="8" fill="url(#g)"/></svg>`
	results := make(map[string][]byte)
	for _, mode := range []string{"none", "bayer", "floyd-steinberg"} {
		var first []byte
		for pass := 0; pass < 2; pass++ {
			output := filepath.Join(t.TempDir(), mode+".png")
			if err := run([]string{"-strict", "-width", "64", "-height", "8", "-dither", mode, "-o", output, "-"}, strings.NewReader(source), io.Discard); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if pass == 0 {
				first = data
			} else if !bytes.Equal(first, data) {
				t.Fatalf("%s output is not deterministic", mode)
			}
		}
		results[mode] = first
	}
	if bytes.Equal(results["none"], results["bayer"]) {
		t.Error("none and Bayer outputs are identical")
	}
	if bytes.Equal(results["none"], results["floyd-steinberg"]) {
		t.Error("none and Floyd-Steinberg outputs are identical")
	}
}

func TestParseCommandOptionsInfersFormats(t *testing.T) {
	tests := []struct {
		name       string
		arguments  []string
		wantFormat outputFormat
		wantOutput string
	}{
		{name: "default", arguments: []string{"input.svg"}, wantFormat: formatPNG, wantOutput: "output.png"},
		{name: "explicit-tiff-default-name", arguments: []string{"-format", "tiff", "input.svg"}, wantFormat: formatTIFF, wantOutput: "output.tiff"},
		{name: "png-uppercase", arguments: []string{"-o", "image.PNG", "input.svg"}, wantFormat: formatPNG, wantOutput: "image.PNG"},
		{name: "tif-uppercase", arguments: []string{"-o", "image.TIF", "input.svg"}, wantFormat: formatTIFF, wantOutput: "image.TIF"},
		{name: "tiff-uppercase", arguments: []string{"-o", "image.TIFF", "input.svg"}, wantFormat: formatTIFF, wantOutput: "image.TIFF"},
		{name: "explicit-unrecognized-extension", arguments: []string{"-format", "png", "-o", "image.bin", "input.svg"}, wantFormat: formatPNG, wantOutput: "image.bin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseCommandOptions(test.arguments, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if options.format != test.wantFormat || options.output != test.wantOutput {
				t.Fatalf("format/output = %s/%q, want %s/%q", options.format, options.output, test.wantFormat, test.wantOutput)
			}
		})
	}
}

func TestParseCommandOptionsRejectsInvalidCombinations(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		contains  string
	}{
		{name: "format", arguments: []string{"-format", "gif", "input.svg"}, contains: "unsupported format"},
		{name: "bit-depth", arguments: []string{"-bit-depth", "12", "input.svg"}, contains: "bit depth must be 8 or 16"},
		{name: "dither", arguments: []string{"-dither", "random", "input.svg"}, contains: "unsupported dither"},
		{name: "compression", arguments: []string{"-tiff-compression", "lzw", "-format", "tiff", "input.svg"}, contains: "unsupported TIFF compression"},
		{name: "color-model", arguments: []string{"-color-model", "lab", "input.svg"}, contains: "unsupported color model"},
		{name: "processing", arguments: []string{"-color-model", "cmyk", "-format", "tiff", "-cmyk-processing", "relative", "input.svg"}, contains: "unsupported CMYK processing"},
		{name: "unknown-auto-extension", arguments: []string{"-o", "image.bin", "input.svg"}, contains: "cannot infer output format"},
		{name: "extension-conflict", arguments: []string{"-format", "png", "-o", "image.tiff", "input.svg"}, contains: "conflicts"},
		{name: "png-compression", arguments: []string{"-tiff-compression", "none", "-o", "image.png", "input.svg"}, contains: "requires TIFF"},
		{name: "png-cmyk", arguments: []string{"-color-model", "cmyk", "-o", "image.png", "input.svg"}, contains: "CMYK output requires TIFF"},
		{name: "processing-with-rgba", arguments: []string{"-cmyk-processing", "native", "-o", "image.tiff", "input.svg"}, contains: "requires -color-model=cmyk"},
		{name: "negative-size", arguments: []string{"-width", "-1", "input.svg"}, contains: "cannot be negative"},
		{name: "missing-input", arguments: nil, contains: "usage"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseCommandOptions(test.arguments, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want substring %q", err, test.contains)
			}
		})
	}
}

func TestRunRejectsUnsafePNGRasterBeforeCreatingOutput(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	output := filepath.Join(directory, "huge.png")
	err := run([]string{"-width", "100000", "-height", "100000", "-o", output, "-"}, strings.NewReader(`<svg viewBox="0 0 1 1"/>`), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "use streaming TIFF") {
		t.Fatalf("error = %v", err)
	}
	assertNoOutputOrTemporaries(t, output)
}

func TestRunRenderFailureLeavesNoOutputOrTemporary(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "broken.tiff")
	source := `<svg viewBox="0 0 2 2"><rect width="not-a-length" height="2"/></svg>`
	err := run([]string{"-strict", "-format", "tiff", "-o", output, "-"}, strings.NewReader(source), io.Discard)
	if err == nil {
		t.Fatal("run unexpectedly succeeded")
	}
	assertNoOutputOrTemporaries(t, output)
}

func TestRunStreamsVeryWideTIFF(t *testing.T) {
	output := filepath.Join(t.TempDir(), "wide.tiff")
	source := `<svg viewBox="0 0 1 1"><defs><linearGradient id="g"><stop stop-color="#123456"/><stop offset="1" stop-color="#edcba9"/></linearGradient></defs><rect width="1" height="1" fill="url(#g)"/></svg>`
	if err := run([]string{"-strict", "-format", "tiff", "-width", "100000", "-height", "2", "-o", output, "-"}, strings.NewReader(source), io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	assertTIFFHeader(t, data, 100000, 2, 8, 2, 4)
}

func TestWriteAtomicallyPreservesDestinationAndCleansTemporaryOnFailure(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	output := filepath.Join(directory, "result.png")
	original := []byte("original")
	if err := os.WriteFile(output, original, 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("encode failed")
	err := writeAtomically(output, func(destination io.Writer) error {
		if _, err := destination.Write([]byte("partial")); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want %v", err, sentinel)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("destination = %q, want %q", data, original)
	}
	assertNoTemporaries(t, output)
}

func TestWriteAtomicallyReplacesExistingDestination(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "result.bin")
	if err := os.WriteFile(output, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(output, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeAtomically(output, func(destination io.Writer) error {
		_, err := destination.Write([]byte("new"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("destination = %q", data)
	}
	if runtime.GOOS != "windows" {
		information, err := os.Stat(output)
		if err != nil {
			t.Fatal(err)
		}
		if got := information.Mode().Perm(); got != 0o640 {
			t.Fatalf("replacement permissions = %#o, want 0640", got)
		}
	}
	assertNoTemporaries(t, output)
}

func assertPNGHeader(t *testing.T, data []byte, width, height, depth int, colorType byte) {
	t.Helper()
	if len(data) < 26 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) || string(data[12:16]) != "IHDR" {
		t.Fatal("invalid PNG header")
	}
	if got := int(binary.BigEndian.Uint32(data[16:20])); got != width {
		t.Fatalf("PNG width = %d, want %d", got, width)
	}
	if got := int(binary.BigEndian.Uint32(data[20:24])); got != height {
		t.Fatalf("PNG height = %d, want %d", got, height)
	}
	if got := int(data[24]); got != depth {
		t.Fatalf("PNG bit depth = %d, want %d", got, depth)
	}
	if got := data[25]; got != colorType {
		t.Fatalf("PNG color type = %d, want %d", got, colorType)
	}
}

func assertTIFFHeader(t *testing.T, data []byte, width, height, depth int, photometric, samples uint16) {
	t.Helper()
	if photometric == 5 {
		if got := singleTIFFUnsigned(t, data, 256); got != uint32(width) {
			t.Fatalf("TIFF width = %d, want %d", got, width)
		}
		if got := singleTIFFUnsigned(t, data, 257); got != uint32(height) {
			t.Fatalf("TIFF height = %d, want %d", got, height)
		}
	} else {
		configuration, err := tiff.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode TIFF config: %v", err)
		}
		if configuration.Width != width || configuration.Height != height {
			t.Fatalf("TIFF dimensions = %dx%d, want %dx%d", configuration.Width, configuration.Height, width, height)
		}
	}
	bits := classicTIFFShorts(t, data, 258)
	if len(bits) != int(samples) {
		t.Fatalf("TIFF BitsPerSample count = %d, want %d", len(bits), samples)
	}
	for _, value := range bits {
		if value != uint16(depth) {
			t.Fatalf("TIFF BitsPerSample = %v, want all %d", bits, depth)
		}
	}
	if got := singleTIFFShort(t, data, 262); got != photometric {
		t.Fatalf("TIFF photometric = %d, want %d", got, photometric)
	}
	if got := singleTIFFShort(t, data, 277); got != samples {
		t.Fatalf("TIFF samples per pixel = %d, want %d", got, samples)
	}
}

func singleTIFFShort(t *testing.T, data []byte, tag uint16) uint16 {
	t.Helper()
	values := classicTIFFShorts(t, data, tag)
	if len(values) != 1 {
		t.Fatalf("TIFF tag %d values = %v, want one", tag, values)
	}
	return values[0]
}

func singleTIFFUnsigned(t *testing.T, data []byte, wantedTag uint16) uint32 {
	t.Helper()
	order, ifdOffset := classicTIFFDirectory(t, data)
	entryCount := int(order.Uint16(data[ifdOffset : ifdOffset+2]))
	for index := 0; index < entryCount; index++ {
		offset := ifdOffset + 2 + index*12
		if offset+12 > len(data) {
			t.Fatal("truncated TIFF IFD")
		}
		entry := data[offset : offset+12]
		if order.Uint16(entry[:2]) != wantedTag {
			continue
		}
		if count := order.Uint32(entry[4:8]); count != 1 {
			t.Fatalf("TIFF tag %d count = %d, want one", wantedTag, count)
		}
		switch valueType := order.Uint16(entry[2:4]); valueType {
		case 3:
			return uint32(order.Uint16(entry[8:10]))
		case 4:
			return order.Uint32(entry[8:12])
		default:
			t.Fatalf("TIFF tag %d type = %d, want SHORT or LONG", wantedTag, valueType)
		}
	}
	t.Fatalf("TIFF tag %d is missing", wantedTag)
	return 0
}

func classicTIFFShorts(t *testing.T, data []byte, wantedTag uint16) []uint16 {
	t.Helper()
	order, ifdOffset := classicTIFFDirectory(t, data)
	entryCount := int(order.Uint16(data[ifdOffset : ifdOffset+2]))
	for index := 0; index < entryCount; index++ {
		offset := ifdOffset + 2 + index*12
		if offset+12 > len(data) {
			t.Fatal("truncated TIFF IFD")
		}
		entry := data[offset : offset+12]
		if order.Uint16(entry[:2]) != wantedTag {
			continue
		}
		if valueType := order.Uint16(entry[2:4]); valueType != 3 {
			t.Fatalf("TIFF tag %d type = %d, want SHORT", wantedTag, valueType)
		}
		count := int(order.Uint32(entry[4:8]))
		start := offset + 8
		if count > 2 {
			start = int(order.Uint32(entry[8:12]))
		}
		if count < 0 || start < 0 || start+count*2 > len(data) {
			t.Fatalf("invalid TIFF tag %d value offset", wantedTag)
		}
		values := make([]uint16, count)
		for valueIndex := range values {
			values[valueIndex] = order.Uint16(data[start+valueIndex*2 : start+valueIndex*2+2])
		}
		return values
	}
	t.Fatalf("TIFF tag %d is missing; present bytes prefix %v", wantedTag, data[:min(len(data), 32)])
	return nil
}

func classicTIFFDirectory(t *testing.T, data []byte) (binary.ByteOrder, int) {
	t.Helper()
	if len(data) < 8 {
		t.Fatal("truncated TIFF header")
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		t.Fatal("invalid TIFF byte order")
	}
	if order.Uint16(data[2:4]) != 42 {
		t.Fatalf("test expects classic TIFF, header magic is %d", order.Uint16(data[2:4]))
	}
	ifdOffset := int(order.Uint32(data[4:8]))
	if ifdOffset < 0 || ifdOffset+2 > len(data) {
		t.Fatal("invalid TIFF IFD offset")
	}
	return order, ifdOffset
}

func assertNoOutputOrTemporaries(t *testing.T, output string) {
	t.Helper()
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output stat error = %v, want not exist", err)
	}
	assertNoTemporaries(t, output)
}

func assertNoTemporaries(t *testing.T, output string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(output), "."+filepath.Base(output)+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(matches, []string(nil)) {
		t.Fatalf("temporary outputs remain: %v", matches)
	}
}
