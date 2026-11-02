package exports

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
)

const maxGunzipBytes = 512 << 20 // 512 MiB hard cap against decompression bombs

// gunzipConcat decompresses one or more concatenated gzip members (RFC 1952).
func gunzipConcat(b []byte) ([]byte, error) {
	var out bytes.Buffer
	r := bytes.NewReader(b)
	for r.Len() > 0 {
		zr, err := gzip.NewReader(r)
		if err != nil {
			if errors.Is(err, io.EOF) && out.Len() > 0 {
				break
			}
			return nil, err
		}
		zr.Multistream(false)
		if _, err := io.Copy(&out, io.LimitReader(zr, maxGunzipBytes+1)); err != nil {
			_ = zr.Close()
			return nil, err
		}
		if out.Len() > maxGunzipBytes {
			_ = zr.Close()
			return nil, errors.New("exports: gunzip too large")
		}
		if err := zr.Close(); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	}
	return out.Bytes(), nil
}
