// Command genicon installs the application icon everywhere it is needed.
//
// The source is scripts/icon/appicon.png, which is tracked. Both other homes
// for it are not: build/ is git-ignored and wails rewrites parts of it, and
// plan/ stays on the working machine. So the icon is copied out from here
// rather than kept where the build tools can lose it.
//
// It also deletes build/windows/icon.ico. Wails builds the .ico from
// appicon.png only when the .ico does not already exist — it never notices the
// source changed — so a stale one is how a new icon silently never reaches the
// title bar while every build reports success.
//
//	go run ./scripts/genicon
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

// wailsIconSize is what wails wants for appicon.png.
const wailsIconSize = 1024

func main() {
	source := filepath.Join("scripts", "icon", "appicon.png")

	src, err := readPNG(source)
	if err != nil {
		fail(err)
	}

	bounds := src.Bounds()
	fmt.Printf("source %s (%dx%d)\n", source, bounds.Dx(), bounds.Dy())

	icon := src
	if bounds.Dx() != wailsIconSize || bounds.Dy() != wailsIconSize {
		icon = resize(src, wailsIconSize)
		fmt.Printf("scaled to %dx%d\n", wailsIconSize, wailsIconSize)
	}

	if err := writePNG(filepath.Join("build", "appicon.png"), icon); err != nil {
		fail(err)
	}

	// The frontend shows the same icon in the header and the About dialog.
	if err := writePNG(filepath.Join("frontend", "src", "assets", "appicon.png"), src); err != nil {
		fail(err)
	}

	ico := filepath.Join("build", "windows", "icon.ico")
	switch err := os.Remove(ico); {
	case err == nil:
		fmt.Printf("removed %s so wails rebuilds it from this image\n", ico)
	case os.IsNotExist(err):
	default:
		fail(err)
	}
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

func writePNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	return nil
}

// resize scales an image to a square of the given size, sampling four source
// pixels per destination pixel. Enough for a flat illustration going up by a
// small factor, and it keeps the alpha channel that gives the icon its shape.
func resize(src image.Image, size int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(dst, dst.Bounds(), image.Transparent, image.Point{}, draw.Src)

	b := src.Bounds()
	scaleX := float64(b.Dx()) / float64(size)
	scaleY := float64(b.Dy()) / float64(size)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := (float64(x) + 0.5) * scaleX
			sy := (float64(y) + 0.5) * scaleY
			dst.Set(x, y, bilinear(src, b, sx, sy))
		}
	}
	return dst
}

func bilinear(src image.Image, b image.Rectangle, x, y float64) color.RGBA {
	x0, y0 := int(x), int(y)
	fx, fy := x-float64(x0), y-float64(y0)

	at := func(px, py int) (r, g, bl, a float64) {
		px = clamp(px, b.Min.X, b.Max.X-1)
		py = clamp(py, b.Min.Y, b.Max.Y-1)
		cr, cg, cb, ca := src.At(px, py).RGBA()
		return float64(cr), float64(cg), float64(cb), float64(ca)
	}

	r00, g00, b00, a00 := at(x0, y0)
	r10, g10, b10, a10 := at(x0+1, y0)
	r01, g01, b01, a01 := at(x0, y0+1)
	r11, g11, b11, a11 := at(x0+1, y0+1)

	mix := func(v00, v10, v01, v11 float64) uint8 {
		top := v00*(1-fx) + v10*fx
		bot := v01*(1-fx) + v11*fx
		return uint8((top*(1-fy) + bot*fy) / 257)
	}

	return color.RGBA{
		R: mix(r00, r10, r01, r11),
		G: mix(g00, g10, g01, g11),
		B: mix(b00, b10, b01, b11),
		A: mix(a00, a10, a01, a11),
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genicon:", err)
	os.Exit(1)
}
