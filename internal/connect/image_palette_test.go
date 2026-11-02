package connect

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestProcessUploadFormats(t *testing.T) {
	// JPEG
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 10, B: 10, A: 255})
		}
	}
	var jbuf bytes.Buffer
	if err := jpeg.Encode(&jbuf, img, nil); err != nil {
		t.Fatal(err)
	}
	got, err := processUpload(AssetKindLogo, jbuf.Bytes())
	if err != nil || got.ContentType != "image/jpeg" {
		t.Fatalf("%+v %v", got, err)
	}

	// GIF
	var gbuf bytes.Buffer
	if err := gif.Encode(&gbuf, img, nil); err != nil {
		t.Fatal(err)
	}
	got, err = processUpload(AssetKindImage, gbuf.Bytes())
	if err != nil || got.ContentType != "image/gif" {
		t.Fatalf("%+v %v", got, err)
	}

	// ICO header
	ico := []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 16, 16}
	ico = append(ico, make([]byte, 40)...)
	got, err = processUpload(AssetKindFavicon, ico)
	if err != nil || got.ContentType != "image/x-icon" {
		t.Fatalf("%+v %v", got, err)
	}

	// favicon too large
	big := make([]byte, maxFaviconBytes+10)
	copy(big, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	_, err = processUpload(AssetKindFavicon, big)
	if errs.CodeOf(err) != errs.CodePayloadTooLarge {
		t.Fatal(err)
	}
}

func TestValidatePaletteKeys(t *testing.T) {
	if err := validatePalette(&BrandingPalette{Light: map[string]string{"nope": "#FFFFFF"}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validatePalette(&BrandingPalette{Light: map[string]string{"primary": "red"}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	ok := &BrandingPalette{Light: map[string]string{
		"primary": "#166534", "on-primary": "#FFFFFF",
		"surface": "#FFFFFF", "on-surface": "#1E2433",
	}}
	if err := validatePalette(ok); err != nil {
		t.Fatal(err)
	}
	p := paletteFromBrand(map[string]any{"palette": map[string]any{
		"light": map[string]any{"primary": "#166534"},
	}})
	if p == nil || p.Light["primary"] != "#166534" {
		t.Fatal(p)
	}
	merged := mergePalette(nil, ok)
	if asStringMap(merged["light"])["primary"] != "#166534" {
		t.Fatal(merged)
	}
}

func TestWebPDims(t *testing.T) {
	raw := make([]byte, 30)
	copy(raw[0:], []byte("RIFF"))
	copy(raw[8:], []byte("WEBPVP8 "))
	raw[26], raw[27] = 10, 0
	raw[28], raw[29] = 20, 0
	w, h, err := webpDims(raw)
	if err != nil || w != 10 || h != 20 {
		t.Fatalf("%d %d %v", w, h, err)
	}
	got, err := processUpload(AssetKindLogo, raw)
	if err != nil || got.ContentType != "image/webp" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAssetKindValid(t *testing.T) {
	if !AssetKindLogo.Valid() || AssetKind("x").Valid() {
		t.Fatal()
	}
}
