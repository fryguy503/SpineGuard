// Regenerate the app icon with: go run ./scripts/icon
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

type point struct{ x, y float64 }

func inside(x, y float64, poly []point) bool {
	hit := false
	j := len(poly) - 1
	for i, p := range poly {
		q := poly[j]
		if (p.y > y) != (q.y > y) && x < (q.x-p.x)*(y-p.y)/(q.y-p.y)+p.x {
			hit = !hit
		}
		j = i
	}
	return hit
}
func main() {
	const size = 1024
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	shield := []point{{512, 176}, {798, 282}, {780, 568}, {710, 728}, {512, 858}, {314, 728}, {244, 568}, {226, 282}}
	left := []point{{332, 336}, {482, 366}, {498, 390}, {498, 638}, {470, 612}, {332, 582}}
	right := []point{{526, 390}, {542, 366}, {692, 336}, {692, 582}, {554, 612}, {526, 638}}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			r, g, b, a := 0., 0., 0., 0.
			for sy := 0; sy < 2; sy++ {
				for sx := 0; sx < 2; sx++ {
					px, py := float64(x)+float64(sx)/2, float64(y)+float64(sy)/2
					dx := math.Max(148-px, math.Max(px-876, 0))
					dy := math.Max(148-py, math.Max(py-876, 0))
					c := color.NRGBA{16, 25, 39, 255}
					if dx*dx+dy*dy > 148*148 {
						c.A = 0
					}
					if inside(px, py, shield) {
						t := py / 1024
						c = color.NRGBA{uint8(76 - 25*t), uint8(220 - 30*t), uint8(188 + 25*t), 255}
					}
					if inside(px, py, left) || inside(px, py, right) {
						c = color.NRGBA{12, 40, 51, 255}
					}
					r += float64(c.R)
					g += float64(c.G)
					b += float64(c.B)
					a += float64(c.A)
				}
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(r / 4), uint8(g / 4), uint8(b / 4), uint8(a / 4)})
		}
	}
	if err := os.MkdirAll("build", 0755); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create("build/appicon.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}
