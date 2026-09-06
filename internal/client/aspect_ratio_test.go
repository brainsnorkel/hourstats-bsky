package client

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImageAspectRatio(t *testing.T) {
	t.Run("png carries canvas dimensions", func(t *testing.T) {
		ar := imageAspectRatio(encodePNG(t, 1200, 800))
		if ar == nil || ar.Width != 1200 || ar.Height != 800 {
			t.Fatalf("got %+v, want 1200x800", ar)
		}
	})

	t.Run("jpeg is decoded too", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 300, 200))
		img.Set(0, 0, color.White)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
		ar := imageAspectRatio(buf.Bytes())
		if ar == nil || ar.Width != 300 || ar.Height != 200 {
			t.Fatalf("got %+v, want 300x200", ar)
		}
	})

	t.Run("undecodable data leaves the embed without a hint", func(t *testing.T) {
		if ar := imageAspectRatio([]byte("not an image")); ar != nil {
			t.Fatalf("got %+v, want nil", ar)
		}
		if ar := imageAspectRatio(nil); ar != nil {
			t.Fatalf("got %+v, want nil", ar)
		}
	})
}
