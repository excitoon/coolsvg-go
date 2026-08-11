package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
