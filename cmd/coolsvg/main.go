// Command coolsvg rasterizes SVG files to PNG or TIFF.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/excitoon/coolsvg-go"
)

type outputFormat uint8

const (
	formatAuto outputFormat = iota
	formatPNG
	formatTIFF
)

type outputColorModel uint8

const (
	colorRGBA outputColorModel = iota
	colorRGB
	colorCMYK
)

type commandOptions struct {
	input           string
	output          string
	format          outputFormat
	width           int
	height          int
	strict          bool
	bitDepth        int
	dither          coolsvg.DitherMode
	tiffCompression coolsvg.TIFFCompression
	colorModel      outputColorModel
	cmykProcessing  coolsvg.CMYKProcessingMode
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "coolsvg:", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdin io.Reader, stderr io.Writer) error {
	options, err := parseCommandOptions(arguments, stderr)
	if err != nil {
		return err
	}

	mode := coolsvg.WarnErrorMode
	if options.strict {
		mode = coolsvg.StrictErrorMode
	}
	icon, err := readIcon(options.input, stdin, mode)
	if err != nil {
		return err
	}

	if options.width == 0 {
		options.width, err = viewBoxPixels(icon.ViewBox.W, "width")
		if err != nil {
			return err
		}
	}
	if options.height == 0 {
		options.height, err = viewBoxPixels(icon.ViewBox.H, "height")
		if err != nil {
			return err
		}
	}
	if options.width <= 0 || options.height <= 0 {
		return fmt.Errorf("output dimensions must be positive")
	}

	bounds := image.Rect(0, 0, options.width, options.height)
	icon.SetTarget(0, 0, float64(options.width), float64(options.height))
	if options.format == formatPNG {
		if err := validatePNGRasterSize(bounds, options.bitDepth); err != nil {
			return err
		}
	}
	return writeAtomically(options.output, func(destination io.Writer) error {
		switch options.format {
		case formatPNG:
			return encodePNG(destination, icon, bounds, options)
		case formatTIFF:
			return encodeTIFF(destination, icon, bounds, options)
		default:
			return fmt.Errorf("internal error: unresolved output format")
		}
	})
}

func parseCommandOptions(arguments []string, stderr io.Writer) (commandOptions, error) {
	flags := flag.NewFlagSet("coolsvg", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("o", "", "output path (default output.png, or output.tiff for TIFF)")
	formatName := flags.String("format", "auto", "output format: auto, png, or tiff")
	width := flags.Int("width", 0, "output width in pixels (defaults to viewBox width)")
	height := flags.Int("height", 0, "output height in pixels (defaults to viewBox height)")
	strict := flags.Bool("strict", false, "reject unsupported SVG elements")
	bitDepth := flags.Int("bit-depth", 8, "samples per channel: 8 or 16")
	ditherName := flags.String("dither", "none", "quantization: none, bayer, or floyd-steinberg")
	compressionName := flags.String("tiff-compression", "deflate", "TIFF compression: deflate or none")
	colorModelName := flags.String("color-model", "rgba", "output color model: rgba, rgb, or cmyk")
	cmykProcessingName := flags.String("cmyk-processing", "post-convert", "CMYK processing: post-convert or native")
	if err := flags.Parse(arguments); err != nil {
		return commandOptions{}, err
	}
	if flags.NArg() != 1 {
		return commandOptions{}, fmt.Errorf("usage: coolsvg [options] input.svg")
	}
	if *width < 0 || *height < 0 {
		return commandOptions{}, fmt.Errorf("output dimensions cannot be negative")
	}
	if *bitDepth != 8 && *bitDepth != 16 {
		return commandOptions{}, fmt.Errorf("bit depth must be 8 or 16, got %d", *bitDepth)
	}

	requestedFormat, err := parseOutputFormat(*formatName)
	if err != nil {
		return commandOptions{}, err
	}
	if *output == "" {
		if requestedFormat == formatTIFF {
			*output = "output.tiff"
		} else {
			*output = "output.png"
		}
	}
	resolvedFormat, err := resolveOutputFormat(requestedFormat, *output)
	if err != nil {
		return commandOptions{}, err
	}

	dither, err := parseDither(*ditherName)
	if err != nil {
		return commandOptions{}, err
	}
	compression, err := parseTIFFCompression(*compressionName)
	if err != nil {
		return commandOptions{}, err
	}
	model, err := parseColorModel(*colorModelName)
	if err != nil {
		return commandOptions{}, err
	}
	processing, err := parseCMYKProcessing(*cmykProcessingName)
	if err != nil {
		return commandOptions{}, err
	}

	explicit := make(map[string]bool)
	flags.Visit(func(value *flag.Flag) { explicit[value.Name] = true })
	if resolvedFormat != formatTIFF && explicit["tiff-compression"] {
		return commandOptions{}, fmt.Errorf("-tiff-compression requires TIFF output")
	}
	if model == colorCMYK && resolvedFormat != formatTIFF {
		return commandOptions{}, fmt.Errorf("CMYK output requires TIFF format")
	}
	if model != colorCMYK && explicit["cmyk-processing"] {
		return commandOptions{}, fmt.Errorf("-cmyk-processing requires -color-model=cmyk")
	}

	return commandOptions{
		input:           flags.Arg(0),
		output:          *output,
		format:          resolvedFormat,
		width:           *width,
		height:          *height,
		strict:          *strict,
		bitDepth:        *bitDepth,
		dither:          dither,
		tiffCompression: compression,
		colorModel:      model,
		cmykProcessing:  processing,
	}, nil
}

func parseOutputFormat(value string) (outputFormat, error) {
	switch strings.ToLower(value) {
	case "auto":
		return formatAuto, nil
	case "png":
		return formatPNG, nil
	case "tiff", "tif":
		return formatTIFF, nil
	default:
		return formatAuto, fmt.Errorf("unsupported format %q (want auto, png, or tiff)", value)
	}
}

func resolveOutputFormat(requested outputFormat, output string) (outputFormat, error) {
	extension := strings.ToLower(filepath.Ext(output))
	var inferred outputFormat
	switch extension {
	case ".png":
		inferred = formatPNG
	case ".tif", ".tiff":
		inferred = formatTIFF
	}
	if requested == formatAuto {
		if inferred == formatAuto {
			return formatAuto, fmt.Errorf("cannot infer output format from %q; use -format", output)
		}
		return inferred, nil
	}
	if inferred != formatAuto && inferred != requested {
		return formatAuto, fmt.Errorf("output extension %q conflicts with -format=%s", extension, requested)
	}
	return requested, nil
}

func (format outputFormat) String() string {
	switch format {
	case formatPNG:
		return "png"
	case formatTIFF:
		return "tiff"
	default:
		return "auto"
	}
}

func parseDither(value string) (coolsvg.DitherMode, error) {
	switch strings.ToLower(value) {
	case "none":
		return coolsvg.DitherNone, nil
	case "bayer":
		return coolsvg.DitherBayer8x8, nil
	case "floyd-steinberg":
		return coolsvg.DitherFloydSteinberg, nil
	default:
		return coolsvg.DitherNone, fmt.Errorf("unsupported dither %q (want none, bayer, or floyd-steinberg)", value)
	}
}

func parseTIFFCompression(value string) (coolsvg.TIFFCompression, error) {
	switch strings.ToLower(value) {
	case "deflate":
		return coolsvg.TIFFCompressionDeflate, nil
	case "none":
		return coolsvg.TIFFCompressionNone, nil
	default:
		return coolsvg.TIFFCompressionDefault, fmt.Errorf("unsupported TIFF compression %q (want deflate or none)", value)
	}
}

func parseColorModel(value string) (outputColorModel, error) {
	switch strings.ToLower(value) {
	case "rgba":
		return colorRGBA, nil
	case "rgb":
		return colorRGB, nil
	case "cmyk":
		return colorCMYK, nil
	default:
		return colorRGBA, fmt.Errorf("unsupported color model %q (want rgba, rgb, or cmyk)", value)
	}
}

func parseCMYKProcessing(value string) (coolsvg.CMYKProcessingMode, error) {
	switch strings.ToLower(value) {
	case "post-convert":
		return coolsvg.CMYKPostConvert, nil
	case "native":
		return coolsvg.CMYKNative, nil
	default:
		return coolsvg.CMYKPostConvert, fmt.Errorf("unsupported CMYK processing %q (want post-convert or native)", value)
	}
}

func readIcon(path string, stdin io.Reader, mode coolsvg.ErrorMode) (*coolsvg.SvgIcon, error) {
	if path == "-" {
		return coolsvg.ReadIconStream(stdin, mode)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	icon, readErr := coolsvg.ReadIconStream(file, mode)
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return icon, nil
}

func viewBoxPixels(value float64, dimension string) (int, error) {
	value = math.Ceil(value)
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value >= float64(maxInt()) {
		return 0, fmt.Errorf("viewBox %s %v cannot be used as an output dimension", dimension, value)
	}
	return int(value), nil
}

func maxInt() int { return int(^uint(0) >> 1) }

func encodePNG(destination io.Writer, icon *coolsvg.SvgIcon, bounds image.Rectangle, options commandOptions) error {
	renderOptions := coolsvg.RenderOptions{Dither: coolsvg.DitherOptions{Mode: options.dither}}
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if options.bitDepth == 16 {
		image64 := image.NewNRGBA64(bounds)
		if options.colorModel == colorRGB {
			stddraw.Draw(image64, bounds, image.NewUniform(color.White), image.Point{}, stddraw.Src)
		}
		if err := icon.RenderWithOptions(image64, renderOptions); err != nil {
			return err
		}
		return encoder.Encode(destination, image64)
	}
	image8 := image.NewNRGBA(bounds)
	if options.colorModel == colorRGB {
		stddraw.Draw(image8, bounds, image.NewUniform(color.White), image.Point{}, stddraw.Src)
	}
	if err := icon.RenderWithOptions(image8, renderOptions); err != nil {
		return err
	}
	return encoder.Encode(destination, image8)
}

func encodeTIFF(destination io.Writer, icon *coolsvg.SvgIcon, bounds image.Rectangle, options commandOptions) error {
	format := coolsvg.TIFFPixelFormatRGBA
	switch options.colorModel {
	case colorRGB:
		format = coolsvg.TIFFPixelFormatRGB
	case colorCMYK:
		format = coolsvg.TIFFPixelFormatCMYK
	}
	return icon.EncodeTIFF(destination, bounds, &coolsvg.TIFFRenderOptions{
		TIFFOptions: coolsvg.TIFFOptions{
			Format:        format,
			BitsPerSample: options.bitDepth,
			Compression:   options.tiffCompression,
			Dither:        coolsvg.DitherOptions{Mode: options.dither},
		},
		CMYKOptions: coolsvg.CMYKOptions{ProcessingMode: options.cmykProcessing},
	})
}

const maxPNGRasterBytes = uint64(512 << 20)

func validatePNGRasterSize(bounds image.Rectangle, bitDepth int) error {
	bytesPerPixel := uint64(4)
	if bitDepth == 16 {
		bytesPerPixel = 8
	}
	width, height := bounds.Dx(), bounds.Dy()
	if width < 0 || height < 0 || uint64(width) > ^uint64(0)/bytesPerPixel {
		return fmt.Errorf("PNG raster dimensions %v are too large", bounds)
	}
	rowBytes := uint64(width) * bytesPerPixel
	if height != 0 && rowBytes > ^uint64(0)/uint64(height) {
		return fmt.Errorf("PNG raster dimensions %v are too large", bounds)
	}
	rasterBytes := rowBytes * uint64(height)
	if rasterBytes > maxPNGRasterBytes {
		return fmt.Errorf("PNG raster requires %d MiB, exceeding the %d MiB safety limit; use streaming TIFF output", (rasterBytes+(1<<20)-1)>>20, maxPNGRasterBytes>>20)
	}
	return nil
}

func writeAtomically(path string, write func(io.Writer) error) (err error) {
	directory := filepath.Dir(path)
	temporaryDirectory, err := os.MkdirTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary output directory: %w", err)
	}
	temporaryPath := filepath.Join(temporaryDirectory, "output")
	temporary, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		_ = os.Remove(temporaryDirectory)
		return fmt.Errorf("create temporary output: %w", err)
	}
	if information, statErr := os.Stat(path); statErr == nil {
		if chmodErr := temporary.Chmod(information.Mode().Perm()); chmodErr != nil {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
			_ = os.Remove(temporaryDirectory)
			return fmt.Errorf("preserve output permissions: %w", chmodErr)
		}
	} else if !os.IsNotExist(statErr) {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		_ = os.Remove(temporaryDirectory)
		return fmt.Errorf("inspect existing output: %w", statErr)
	}
	closed := false
	committed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		if !committed {
			_ = os.Remove(temporaryPath)
		}
		_ = os.Remove(temporaryDirectory)
	}()

	if err := write(temporary); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary output: %w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("close temporary output: %w", err)
	}
	closed = true
	if err := replaceOutput(temporaryPath, path); err != nil {
		return fmt.Errorf("replace output: %w", err)
	}
	committed = true
	return nil
}
