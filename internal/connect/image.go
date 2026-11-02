package connect

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"github.com/radiobros/emailbridge/internal/errs"
)

const (
	maxLogoBytes    = 2 << 20
	maxFaviconBytes = 256 << 10
	maxLogoDim      = 4096
	maxFaviconDim   = 512
)

type detectedImage struct {
	ContentType string
	Width       int
	Height      int
	Bytes       []byte
}

func processUpload(kind AssetKind, raw []byte) (*detectedImage, error) {
	maxBytes := int64(maxLogoBytes)
	maxDim := maxLogoDim
	if kind == AssetKindFavicon {
		maxBytes = maxFaviconBytes
		maxDim = maxFaviconDim
	}
	if int64(len(raw)) > maxBytes {
		return nil, errs.New(errs.CodePayloadTooLarge, "The image exceeds the size limit for this kind.")
	}
	ct, err := detectImageType(raw)
	if err != nil {
		return nil, err
	}
	switch ct {
	case "image/png", "image/jpeg", "image/gif":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return nil, errs.New(errs.CodeBrandingAssetType, "The file is not a valid image.")
		}
		if cfg.Width > maxDim || cfg.Height > maxDim {
			return nil, errs.New(errs.CodeBrandingAssetDimensions, "The image dimensions are too large.")
		}
		out, w, h, err := reencode(raw, ct)
		if err != nil {
			return nil, err
		}
		return &detectedImage{ContentType: ct, Width: w, Height: h, Bytes: out}, nil
	case "image/webp":
		w, h, err := webpDims(raw)
		if err != nil {
			return nil, errs.New(errs.CodeBrandingAssetType, "The file is not a valid WebP image.")
		}
		if w > maxDim || h > maxDim {
			return nil, errs.New(errs.CodeBrandingAssetDimensions, "The image dimensions are too large.")
		}
		return &detectedImage{ContentType: ct, Width: w, Height: h, Bytes: stripWebPMetadata(raw)}, nil
	case "image/x-icon":
		w, h := icoDims(raw)
		if w > maxDim || h > maxDim {
			return nil, errs.New(errs.CodeBrandingAssetDimensions, "The image dimensions are too large.")
		}
		return &detectedImage{ContentType: ct, Width: w, Height: h, Bytes: raw}, nil
	default:
		return nil, errs.New(errs.CodeBrandingAssetType, "Only PNG, JPEG, WebP, GIF and ICO are allowed (no SVG).")
	}
}

func detectImageType(raw []byte) (string, error) {
	if len(raw) < 12 {
		return "", errs.New(errs.CodeBrandingAssetType, "Only PNG, JPEG, WebP, GIF and ICO are allowed (no SVG).")
	}
	// SVG / XML — refuse explicitly.
	trim := bytes.TrimLeft(raw, " \t\r\n")
	if bytes.HasPrefix(trim, []byte("<svg")) || bytes.HasPrefix(trim, []byte("<?xml")) ||
		bytes.HasPrefix(bytes.ToLower(trim), []byte("<!doctype svg")) {
		return "", errs.New(errs.CodeBrandingAssetType, "SVG is not allowed for branding assets.")
	}
	if bytes.HasPrefix(raw, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return "image/png", nil
	}
	if bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff}) {
		return "image/jpeg", nil
	}
	if bytes.HasPrefix(raw, []byte("GIF87a")) || bytes.HasPrefix(raw, []byte("GIF89a")) {
		return "image/gif", nil
	}
	if bytes.HasPrefix(raw, []byte("RIFF")) && bytes.Equal(raw[8:12], []byte("WEBP")) {
		return "image/webp", nil
	}
	if bytes.HasPrefix(raw, []byte{0x00, 0x00, 0x01, 0x00}) || bytes.HasPrefix(raw, []byte{0x00, 0x00, 0x02, 0x00}) {
		return "image/x-icon", nil
	}
	return "", errs.New(errs.CodeBrandingAssetType, "Only PNG, JPEG, WebP, GIF and ICO are allowed (no SVG).")
}

func reencode(raw []byte, ct string) ([]byte, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, 0, 0, errs.New(errs.CodeBrandingAssetType, "The file is not a valid image.")
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var buf bytes.Buffer
	switch ct {
	case "image/png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, 0, 0, errs.Wrap(err, errs.CodeInternalError, "connect.image.png")
		}
		return buf.Bytes(), w, h, nil
	case "image/jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return nil, 0, 0, errs.Wrap(err, errs.CodeInternalError, "connect.image.jpeg")
		}
		return buf.Bytes(), w, h, nil
	case "image/gif":
		// Keep original GIF bytes after decode-validated dims (stdlib encode loses animation).
		return raw, w, h, nil
	default:
		return raw, w, h, nil
	}
}

func webpDims(raw []byte) (int, int, error) {
	if len(raw) < 30 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	chunk := string(raw[12:16])
	switch chunk {
	case "VP8 ":
		if len(raw) < 30 {
			return 0, 0, io.ErrUnexpectedEOF
		}
		w := int(binary.LittleEndian.Uint16(raw[26:28])) & 0x3fff
		h := int(binary.LittleEndian.Uint16(raw[28:30])) & 0x3fff
		return w, h, nil
	case "VP8L":
		if len(raw) < 25 {
			return 0, 0, io.ErrUnexpectedEOF
		}
		bits := binary.LittleEndian.Uint32(raw[21:25])
		w := int(bits&0x3fff) + 1
		h := int((bits>>14)&0x3fff) + 1
		return w, h, nil
	case "VP8X":
		if len(raw) < 30 {
			return 0, 0, io.ErrUnexpectedEOF
		}
		w := 1 + (int(raw[24]) | int(raw[25])<<8 | int(raw[26])<<16)
		h := 1 + (int(raw[27]) | int(raw[28])<<8 | int(raw[29])<<16)
		return w, h, nil
	default:
		return 0, 0, io.ErrUnexpectedEOF
	}
}

func icoDims(raw []byte) (int, int) {
	if len(raw) < 8 {
		return 0, 0
	}
	w := int(raw[6])
	h := int(raw[7])
	if w == 0 {
		w = 256
	}
	if h == 0 {
		h = 256
	}
	return w, h
}
