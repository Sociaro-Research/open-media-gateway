package providerconfig

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestLogoValidation(t *testing.T) {
	rgba := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	rgba.SetNRGBA(0, 0, color.NRGBA{255, 255, 255, 0})
	rgba.SetNRGBA(1, 0, color.NRGBA{255, 255, 255, 127})
	rgba.SetNRGBA(2, 0, color.NRGBA{255, 255, 255, 255})
	opaque := image.NewNRGBA(rgba.Bounds())
	for x := 0; x < 3; x++ {
		opaque.SetNRGBA(x, 0, color.NRGBA{255, 255, 255, 255})
	}
	palette := image.NewPaletted(rgba.Bounds(), color.Palette{color.NRGBA{20, 30, 40, 0}, color.NRGBA{90, 80, 70, 128}, color.NRGBA{40, 50, 60, 255}})
	palette.Pix = []byte{0, 1, 2}
	for _, tc := range []struct {
		name string
		img  image.Image
		want string
	}{
		{"transparent RGBA", rgba, ""}, {"palette tRNS", palette, ""}, {"opaque RGB", opaque, "transparency"},
		{"oversized dimensions", image.NewNRGBA(image.Rect(0, 0, 2049, 1)), "dimensions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := png.Encode(&b, tc.img); err != nil {
				t.Fatal(err)
			}
			err := ValidateLogo(b.Bytes())
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		data []byte
		want string
	}{
		{[]byte(`<svg onload="alert(1)"/>`), "unsupported"},
		{[]byte("\x89PNG\r\n\x1a\nmalformed"), "invalid PNG"},
		{make([]byte, MaxLogoBytes+1), "1.05 MB"},
	} {
		if err := ValidateLogo(tc.data); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatal(err)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, rgba)
	if err := ValidateLogo(b.Bytes()[:len(b.Bytes())-10]); err == nil || !strings.Contains(err.Error(), "invalid PNG") {
		t.Fatal("truncated PNG accepted", err)
	}
}
