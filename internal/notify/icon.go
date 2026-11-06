package notify

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/draw"
	"image/png"

	"github.com/godbus/dbus/v5"
)

//go:generate rsvg-convert --width=128 --height=128 --output=logo.png ../../assets/tequilashot-mark-inverse.svg

//go:embed logo.png
var logoPNG []byte

type notificationImage struct {
	Width         int32
	Height        int32
	Rowstride     int32
	HasAlpha      bool
	BitsPerSample int32
	Channels      int32
	Data          []byte
}

func logoData() (dbus.Variant, error) {
	logo, err := png.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		return dbus.Variant{}, fmt.Errorf("decode notification logo: %w", err)
	}
	pixels := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	draw.Draw(pixels, pixels.Bounds(), logo, logo.Bounds().Min, draw.Src)
	return dbus.MakeVariant(notificationImage{
		Width: 128, Height: 128, Rowstride: 512,
		HasAlpha: true, BitsPerSample: 8, Channels: 4,
		Data: pixels.Pix,
	}), nil
}
