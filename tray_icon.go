package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// A small built-in mark keeps the validation executable self-contained.
func trayIcon() ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := image.NewUniform(color.NRGBA{R: 30, G: 118, B: 210, A: 255})
	for _, r := range []image.Rectangle{
		image.Rect(4, 8, 26, 12), image.Rect(22, 4, 26, 16),
		image.Rect(6, 20, 28, 24), image.Rect(6, 16, 10, 28),
	} {
		draw.Draw(img, r, ink, image.Point{}, draw.Src)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
