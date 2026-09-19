// Command mkicon renders the product site's icon files from the master PNG in
// assets/icon: favicon.ico, an Apple touch icon, and the PNGs the page header
// and web manifest use. It is a build-time tool; run it with
// "go generate ./internal/icon" whenever the master icon changes.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"github.com/spilloid/spoolsmith/internal/icon"
)

// touchBackground is the site's paper colour. iOS fills a touch icon's
// transparency with black, which would swallow the dark outline, so that one
// file is flattened onto the page background instead.
var touchBackground = color.NRGBA{R: 0xf8, G: 0xf7, B: 0xf0, A: 0xff}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mkicon:", err)
		os.Exit(1)
	}
}

func run() error {
	srcPath := flag.String("src", "", "path to the square master icon PNG")
	siteDir := flag.String("site", "", "site directory to write into (docs)")
	flag.Parse()
	if *srcPath == "" || *siteDir == "" {
		flag.Usage()
		return fmt.Errorf("-src and -site are required")
	}

	file, err := os.Open(*srcPath)
	if err != nil {
		return err
	}
	defer file.Close()
	source, err := png.Decode(file)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", *srcPath, err)
	}

	entries, err := icon.Entries(source, icon.FaviconSizes)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*siteDir, "favicon.ico"), icon.ICO(entries), 0o644); err != nil {
		return err
	}

	for name, size := range map[string]int{"icon-192.png": 192, "icon-512.png": 512} {
		scaled, err := icon.Downscale(source, size)
		if err != nil {
			return err
		}
		if err := writePNG(filepath.Join(*siteDir, "img", name), scaled); err != nil {
			return err
		}
	}

	touch, err := icon.Downscale(source, 180)
	if err != nil {
		return err
	}
	flat := image.NewNRGBA(touch.Bounds())
	draw.Draw(flat, flat.Bounds(), image.NewUniform(touchBackground), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), touch, image.Point{}, draw.Over)
	return writePNG(filepath.Join(*siteDir, "apple-touch-icon.png"), flat)
}

func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
