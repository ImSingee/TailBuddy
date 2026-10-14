package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// A small original three-dot node glyph, drawn at 2x for the menu bar.
func trayPNG() []byte {
	im := image.NewNRGBA(image.Rect(0, 0, 36, 36))
	for y := 0; y < 36; y++ {
		for x := 0; x < 36; x++ {
			inside := false
			for _, p := range [][2]int{{9, 10}, {26, 10}, {18, 26}} {
				dx, dy := x-p[0], y-p[1]
				if dx*dx+dy*dy <= 25 {
					inside = true
				}
			}
			if (y >= 9 && y <= 11 && x >= 9 && x <= 26) || (x >= 12 && x <= 14 && y >= 12 && y <= 23) || (x >= 22 && x <= 24 && y >= 12 && y <= 23) {
				inside = true
			}
			if inside {
				im.SetNRGBA(x, y, color.NRGBA{A: 255})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	return b.Bytes()
}
