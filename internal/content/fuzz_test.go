package content

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzSanitizeHTML(f *testing.F) {
	seeds := []string{
		"",
		"<p>hi</p>",
		`<img src="cid:x@y">`,
		`<a href="javascript:alert(1)">x</a>`,
		`<script>alert(1)</script>`,
		string(mustRead(f, "html", "safe.html")),
		string(mustRead(f, "html", "xss.html")),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 8192 {
			raw = raw[:8192]
		}
		_ = SanitizeHTML(raw, HTMLOptions{
			MaxBytes:     2048,
			MapCID:       func(cid string) string { return "https://x/" + cid },
			RemoteImages: RemoteBlock,
		})
		_ = SanitizeHTML(raw, HTMLOptions{RemoteImages: RemoteAllow, MaxBytes: 1024})
		_ = SanitizeHTML(raw, HTMLOptions{
			RemoteImages: RemoteProxy,
			ProxyURL:     func(u string) string { return "https://p/?u=" + u },
			MaxBytes:     512,
		})
	})
}

func FuzzParseCalendar(f *testing.F) {
	f.Add(mustRead(f, "calendar", "meeting.ics"))
	f.Add(mustRead(f, "calendar", "allday.ics"))
	f.Add([]byte(""))
	f.Add([]byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"))
	f.Add([]byte("not calendar"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 8192 {
			raw = raw[:8192]
		}
		_, _ = ParseCalendar(raw)
	})
}

func FuzzReplyText(f *testing.F) {
	for _, name := range []string{"basic.txt", "italian.txt", "outlook.txt", "signature.txt"} {
		f.Add(string(mustRead(f, "reply", name)))
	}
	f.Add("")
	f.Add("> only quote\n")
	f.Add("Hi\n\n-- \nSig")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 8192 {
			s = s[:8192]
		}
		_ = ReplyText(s)
		_ = ReplyTextFromHTML(s)
	})
}

func FuzzThreadID(f *testing.F) {
	f.Add("<a@b>", "<c@d>", "<a@b> <c@d>", "Re: Hello")
	f.Add("", "", "", "")
	f.Add("id@x", "id@x", "", "Fwd: Hi")
	f.Fuzz(func(t *testing.T, mid, irt, refs, subj string) {
		if len(mid) > 512 {
			mid = mid[:512]
		}
		if len(irt) > 512 {
			irt = irt[:512]
		}
		if len(refs) > 2048 {
			refs = refs[:2048]
		}
		if len(subj) > 2048 {
			subj = subj[:2048]
		}
		var list []string
		if refs != "" {
			list = splitRefTokens(refs)
		}
		id := ThreadID(mid, irt, list, subj)
		if len(id) != 24 {
			t.Fatalf("len=%d id=%q", len(id), id)
		}
		_ = NormalizeSubject(subj)
	})
}

func splitRefTokens(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func mustRead(t testing.TB, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
