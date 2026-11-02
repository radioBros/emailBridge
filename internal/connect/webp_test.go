package connect

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// webpWithGPSExif builds a minimal VP8X WebP that carries an EXIF chunk with a
// GPS IFD marker (and optional XMP), with both VP8X feature flags set.
func webpWithGPSExif(withXMP bool) []byte {
	// VP8X payload: flags | reserved(3) | canvas w-1 (24) | canvas h-1 (24)
	flags := byte(0x10) // EXIF
	if withXMP {
		flags |= 0x20
	}
	vp8xPayload := []byte{
		flags, 0, 0, 0,
		9, 0, 0, // width-1 = 9 → 10
		19, 0, 0, // height-1 = 19 → 20
	}
	vp8x := buildChunk([]byte("VP8X"), vp8xPayload)

	// Tiny EXIF with "GPS" ASCII so the strip test can assert removal.
	exifPayload := []byte("Exif\x00\x00MM\x00*\x00\x00\x00\x08GPS")
	exif := buildChunk([]byte("EXIF"), exifPayload)

	var body []byte
	body = append(body, vp8x...)
	body = append(body, exif...)
	if withXMP {
		xmp := buildChunk([]byte("XMP "), []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF/></x:xmpmeta>`))
		body = append(body, xmp...)
	}

	out := make([]byte, 0, 12+len(body))
	out = append(out, 'R', 'I', 'F', 'F')
	var sizeBuf [4]byte
	binary.LittleEndian.PutUint32(sizeBuf[:], uint32(4+len(body)))
	out = append(out, sizeBuf[:]...)
	out = append(out, 'W', 'E', 'B', 'P')
	out = append(out, body...)
	return out
}

func TestStripWebPMetadata_dropsEXIFAndXMP(t *testing.T) {
	raw := webpWithGPSExif(true)
	if !bytes.Contains(raw, []byte("EXIF")) || !bytes.Contains(raw, []byte("GPS")) {
		t.Fatal("fixture missing EXIF/GPS")
	}
	if !bytes.Contains(raw, []byte("XMP ")) {
		t.Fatal("fixture missing XMP")
	}
	if raw[20]&0x10 == 0 || raw[20]&0x20 == 0 {
		t.Fatalf("VP8X flags not set: %02x", raw[20])
	}

	out := stripWebPMetadata(raw)
	if bytes.Contains(out, []byte("EXIF")) || bytes.Contains(out, []byte("GPS")) {
		t.Fatal("EXIF/GPS still present")
	}
	if bytes.Contains(out, []byte("XMP ")) || bytes.Contains(out, []byte("xmpmeta")) {
		t.Fatal("XMP still present")
	}
	if !bytes.HasPrefix(out, []byte("RIFF")) || !bytes.Equal(out[8:12], []byte("WEBP")) {
		t.Fatal("not a WebP after strip")
	}
	if out[20]&0x10 != 0 || out[20]&0x20 != 0 {
		t.Fatalf("VP8X EXIF/XMP flags still set: %02x", out[20])
	}
	wantSize := uint32(4 + (len(out) - 12))
	gotSize := binary.LittleEndian.Uint32(out[4:8])
	if gotSize != wantSize {
		t.Fatalf("RIFF size %d want %d", gotSize, wantSize)
	}
	w, h, err := webpDims(out)
	if err != nil || w != 10 || h != 20 {
		t.Fatalf("dims %d×%d %v", w, h, err)
	}
}

func TestStripWebPMetadata_idempotentAndPassthrough(t *testing.T) {
	clean := webpWithGPSExif(false)
	once := stripWebPMetadata(clean)
	twice := stripWebPMetadata(once)
	if !bytes.Equal(once, twice) {
		t.Fatal("second strip changed bytes")
	}
	if !bytes.Equal(stripWebPMetadata([]byte("not-webp")), []byte("not-webp")) {
		t.Fatal("non-webp should pass through")
	}
	got, err := processUpload(AssetKindLogo, webpWithGPSExif(true))
	if err != nil || got.ContentType != "image/webp" {
		t.Fatalf("%+v %v", got, err)
	}
	if bytes.Contains(got.Bytes, []byte("GPS")) || bytes.Contains(got.Bytes, []byte("EXIF")) {
		t.Fatal("processUpload left EXIF in WebP")
	}
}
