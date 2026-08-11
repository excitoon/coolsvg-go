// Command coolsvg rasterizes an SVG file to PNG.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"

	"github.com/excitoon/coolsvg-go"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "coolsvg:", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdin io.Reader, stderr io.Writer) error {
	flags := flag.NewFlagSet("coolsvg", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("o", "output.png", "output PNG path")
	width := flags.Int("width", 0, "output width in pixels (defaults to viewBox width)")
	height := flags.Int("height", 0, "output height in pixels (defaults to viewBox height)")
	strict := flags.Bool("strict", false, "reject unsupported SVG elements")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: coolsvg [options] input.svg")
	}

	input := stdin
	if flags.Arg(0) != "-" {
		file, err := os.Open(flags.Arg(0))
		if err != nil {
			return err
		}
		defer file.Close()
		input = file
	}
	mode := coolsvg.WarnErrorMode
	if *strict {
		mode = coolsvg.StrictErrorMode
	}
	icon, err := coolsvg.ReadIconStream(input, mode)
	if err != nil {
		return err
	}
	if *width == 0 {
		*width = int(math.Ceil(icon.ViewBox.W))
	}
	if *height == 0 {
		*height = int(math.Ceil(icon.ViewBox.H))
	}
	if *width <= 0 || *height <= 0 {
		return fmt.Errorf("output dimensions must be positive")
	}

	destination := image.NewNRGBA(image.Rect(0, 0, *width, *height))
	icon.SetTarget(0, 0, float64(*width), float64(*height))
	if err := icon.Render(destination); err != nil {
		return err
	}
	file, err := os.Create(*output)
	if err != nil {
		return err
	}
	if err := png.Encode(file, destination); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
