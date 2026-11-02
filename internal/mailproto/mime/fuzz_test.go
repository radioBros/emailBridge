package mime

import (
	"testing"
)

func FuzzParseHeaders(f *testing.F) {
	f.Add([]byte("From: a@b.com\r\nSubject: hi\r\n\r\nbody"))
	f.Add([]byte("From: a@b.com\nTo: c@d.com\n\n"))
	f.Add([]byte(""))
	f.Add([]byte("Bad\r\n\r\n"))
	f.Add([]byte("X-A: 1\r\n X-cont\r\n\r\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = ParseHeaders(raw)
	})
}

func FuzzBuildRaw(f *testing.F) {
	f.Add([]byte("From: a@b.com\r\nTo: c@d.com\r\nMessage-ID: <x@y>\r\n\r\nbody"))
	f.Add([]byte("From: a@b.com\r\nTo: c@d.com\r\nBcc: s@t.com\r\n\r\nbody"))
	f.Add([]byte(""))
	f.Add([]byte("Not a message"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = BuildRaw(raw, nil)
		_, _ = BuildRaw(raw, &Envelope{From: "a@b.com", Recipients: []string{"c@d.com"}})
	})
}
