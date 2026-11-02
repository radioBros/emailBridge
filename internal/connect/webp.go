package connect

import (
	"bytes"
	"encoding/binary"
)

// stripWebPMetadata drops EXIF and XMP chunks from a WebP RIFF container and
// clears the matching VP8X feature flags. Other chunks are preserved.
func stripWebPMetadata(raw []byte) []byte {
	if len(raw) < 12 || !bytes.HasPrefix(raw, []byte("RIFF")) || !bytes.Equal(raw[8:12], []byte("WEBP")) {
		return raw
	}
	var body []byte
	changed := false
	off := 12
	for off+8 <= len(raw) {
		fourCC := raw[off : off+4]
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		total := 8 + size
		if size%2 == 1 {
			total++ // pad byte
		}
		if off+total > len(raw) {
			body = append(body, raw[off:]...)
			break
		}
		payload := raw[off+8 : off+8+size]
		name := string(fourCC)
		switch name {
		case "EXIF", "XMP ":
			changed = true
			off += total
			continue
		case "VP8X":
			if len(payload) >= 1 {
				cp := append([]byte(nil), payload...)
				cleared := cp[0] &^ (0x10 | 0x20) // EXIF | XMP flags (VP8X bits 4–5)
				if cleared != cp[0] {
					cp[0] = cleared
					changed = true
				}
				body = append(body, buildChunk(fourCC, cp)...)
				off += total
				continue
			}
		}
		body = append(body, raw[off:off+total]...)
		off += total
	}
	if !changed {
		return raw
	}
	out := make([]byte, 0, 12+len(body))
	out = append(out, 'R', 'I', 'F', 'F')
	var sizeBuf [4]byte
	// Branding uploads are capped well below 4 GiB.
	binary.LittleEndian.PutUint32(sizeBuf[:], uint32(4+len(body))) //nolint:gosec // G115: bounded input
	out = append(out, sizeBuf[:]...)
	out = append(out, 'W', 'E', 'B', 'P')
	out = append(out, body...)
	return out
}

func buildChunk(fourCC, payload []byte) []byte {
	size := len(payload)
	total := 8 + size
	if size%2 == 1 {
		total++
	}
	out := make([]byte, total)
	copy(out[0:4], fourCC)
	binary.LittleEndian.PutUint32(out[4:8], uint32(size)) //nolint:gosec // G115: branding payload size
	copy(out[8:], payload)
	return out
}
