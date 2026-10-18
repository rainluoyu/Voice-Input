package branding

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// Icon is drawn in code and shared by the tray and executable resources.
func Icon() image.Image {
	im := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	blue := color.NRGBA{R: 34, G: 96, B: 181, A: 255}
	for x := 4; x < 28; x++ {
		h := []int{6, 12, 22, 16, 8}[(x-4)/5]
		if (x-4)%5 < 3 {
			for y := 16 - h/2; y < 16+h/2; y++ {
				im.SetNRGBA(x, y, blue)
			}
		}
	}
	return im
}
func PNG() []byte { var out bytes.Buffer; _ = png.Encode(&out, Icon()); return out.Bytes() }
