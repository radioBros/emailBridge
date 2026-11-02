package content

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"golang.org/x/net/html"
)

func TestStripTagsFallback(t *testing.T) {
	got := stripTagsFallback(`Hello <b>x</b> &amp; <script>y</script>z`)
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "z") || strings.Contains(got, "<") {
		t.Fatalf("%q", got)
	}
}

func TestMapAttrs_srcsetAndEmpty(t *testing.T) {
	opts := HTMLOptions{
		MapCID:       func(cid string) string { return "https://a/" + cid },
		RemoteImages: RemoteAllow,
	}
	got := SanitizeHTML(`<img srcset="cid:pic@x 1x, https://other 2x" alt="p">`, opts)
	if !strings.Contains(got.HTML, "https://a/pic@x") {
		t.Fatalf("srcset cid: %s", got.HTML)
	}
	got2 := SanitizeHTML(`<img srcset="" alt="e">`, HTMLOptions{RemoteImages: RemoteBlock})
	_ = got2
	got3 := SanitizeHTML(`<img srcset="cid:missing@x">`, HTMLOptions{
		MapCID: func(string) string { return "" },
	})
	if strings.Contains(got3.HTML, "src=") {
		t.Fatalf("empty map should drop: %s", got3.HTML)
	}
}

func TestMapOneURL_branches(t *testing.T) {
	if mapOneURL("cid:ab", HTMLOptions{}) != "" {
		t.Fatal("nil MapCID")
	}
	if mapOneURL("cid: ab ", HTMLOptions{MapCID: func(c string) string { return "https://x/" + c }}) != "https://x/ab" {
		t.Fatal("cid trim")
	}
	// url.Parse may accept odd schemes; just exercise the path.
	_ = mapOneURL("://bad", HTMLOptions{RemoteImages: RemoteAllow})
	if mapOneURL("//cdn.example/x.png", HTMLOptions{RemoteImages: RemoteAllow}) != "//cdn.example/x.png" {
		t.Fatal("protocol-relative allow")
	}
	if mapOneURL("//cdn.example/x.png", HTMLOptions{RemoteImages: RemoteBlock}) != "" {
		t.Fatal("protocol-relative block")
	}
	proxied := mapOneURL("//cdn.example/x.png", HTMLOptions{
		RemoteImages: RemoteProxy,
		ProxyURL:     func(u string) string { return "https://p/" + u },
	})
	if !strings.Contains(proxied, "https://p/https://cdn.example/x.png") {
		t.Fatalf("proxy: %q", proxied)
	}
	if mapOneURL("https://x", HTMLOptions{RemoteImages: RemoteProxy}) != "" {
		t.Fatal("nil ProxyURL")
	}
	if mapOneURL("mailto:a@b.com", HTMLOptions{RemoteImages: RemoteBlock}) != "mailto:a@b.com" {
		t.Fatal("mailto passthrough")
	}
	if mapOneURL("   ", HTMLOptions{}) != "" {
		t.Fatal("blank")
	}
}

func TestTruncateUTF8_multibyte(t *testing.T) {
	s := "abc\u00e9xyz" // é is 2 bytes
	got := truncateUTF8(s, 4)
	if !utf8Valid(got) || len(got) > 4 {
		t.Fatalf("%q len=%d", got, len(got))
	}
	if truncateUTF8("hi", 10) != "hi" {
		t.Fatal("short")
	}
}

func utf8Valid(s string) bool {
	return strings.ToValidUTF8(s, "") == s
}

func TestFindBody_nil(t *testing.T) {
	if findBody(&html.Node{Type: html.TextNode, Data: "x"}) != nil {
		t.Fatal("expected nil")
	}
}

func TestRewriteImageURLs_roundTrip(t *testing.T) {
	out := rewriteImageURLs(`<p>no images</p>`, HTMLOptions{})
	if !strings.Contains(out, "no images") {
		t.Fatalf("%s", out)
	}
}

func TestCalendarTime_utcAndBadTZ(t *testing.T) {
	p := ical.NewProp(ical.PropDateTimeStart)
	p.Value = "20240101T120000Z"
	ct, err := calendarTime(p)
	if err != nil || ct == nil || ct.TimeZone != "UTC" || ct.AllDay {
		t.Fatalf("%+v %v", ct, err)
	}
	p2 := ical.NewProp(ical.PropDateTimeStart)
	p2.Params.Set(ical.ParamTimezoneID, "Nowhere/Invalid")
	p2.Value = "20240101T120000"
	ct2, err := calendarTime(p2)
	if err != nil || ct2 == nil {
		t.Fatalf("%+v %v", ct2, err)
	}
	// go-ical cannot load the TZID → UTC fallback
	if ct2.TimeZone != "UTC" {
		t.Fatalf("tz %q", ct2.TimeZone)
	}
	if ct, err := calendarTime(nil); err != nil || ct != nil {
		t.Fatal("nil prop")
	}
}

func TestParseCalendar_durationEndAndStatus(t *testing.T) {
	ics := []byte(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
METHOD:CANCEL
BEGIN:VEVENT
UID:dur@example.com
DTSTART:20240301T100000Z
DURATION:PT1H
SUMMARY:Dur
STATUS:CANCELLED
SEQUENCE:1
ORGANIZER:mailto:org@example.com
END:VEVENT
END:VCALENDAR
`)
	evs, err := ParseCalendar(ics)
	if err != nil || len(evs) != 1 {
		t.Fatalf("%v %v", evs, err)
	}
	if evs[0].DTEnd == nil || evs[0].Status != "CANCELLED" {
		t.Fatalf("%+v", evs[0])
	}
	if evs[0].DTEnd.DateTime.Sub(evs[0].DTStart.DateTime) != time.Hour {
		t.Fatalf("duration end %v → %v", evs[0].DTStart.DateTime, evs[0].DTEnd.DateTime)
	}
	if evs[0].Organizer == nil || evs[0].Organizer.Address != "org@example.com" {
		t.Fatalf("org %+v", evs[0].Organizer)
	}
}

func TestParseCalendar_noEvents(t *testing.T) {
	ics := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nEND:VCALENDAR\r\n")
	evs, err := ParseCalendar(ics)
	if err != nil || len(evs) != 0 {
		t.Fatalf("%v %v", evs, err)
	}
}

func TestParseCalendar_invalidStatusStillMaps(t *testing.T) {
	ics := []byte(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:x@y
DTSTART:20240101T000000Z
SUMMARY:S
STATUS:WEIRD
END:VEVENT
END:VCALENDAR
`)
	evs, err := ParseCalendar(ics)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("%+v", evs)
	}
	// Status() errors → we clear status
	if evs[0].Status != "" {
		t.Fatalf("status %q", evs[0].Status)
	}
}

func TestReplyText_collapseAndSpanish(t *testing.T) {
	in := "A\n\n\n\n\nB\n\nEl 1 ene 2024 Juan escribió:\nquoted"
	got := ReplyText(in)
	if got != "A\n\nB" {
		t.Fatalf("%q", got)
	}
}

func TestHtmlToText_scriptSkipped(t *testing.T) {
	got := htmlToText(`<html><head><style>x</style></head><body><p>Hi</p><script>bad</script><br><div>There</div></body></html>`)
	if strings.Contains(got, "bad") || !strings.Contains(got, "Hi") {
		t.Fatalf("%q", got)
	}
}

func TestMapPerson_rsvpFalse(t *testing.T) {
	p := ical.NewProp(ical.PropAttendee)
	p.Value = "mailto:a@b.com"
	p.Params.Set(ical.ParamRSVP, "FALSE")
	cp := mapPerson(*p)
	if cp.RSVP {
		t.Fatal("rsvp")
	}
}
