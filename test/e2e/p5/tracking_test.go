//go:build e2e

package p5

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Per-message trackOpens/trackClicks off, on, cannot force on, and locked switch.
func TestP5_TrackOpensClicksOverrides(t *testing.T) {
	f := startFixture(t, fixtureOpts{
		events: []string{"message.sent", "tracking.opened", "tracking.clicked"},
	})
	enableOrgSwitch(t, f.Owner, "tracking.opens", true)
	enableOrgSwitch(t, f.Owner, "tracking.clicks", true)

	// Track off per message → no pixel / no /t/c/ rewrite.
	before := f.smtpCount()
	rOff := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "track-off",
		"html": htmlTrackBody(), "trackOpens": false, "trackClicks": false,
	}, nil)
	harness.ExpectStatus(t, rOff, http.StatusAccepted)
	if f.waitSent(rOff.Str("id"), 90*time.Second).Str("state") != "sent" {
		t.Fatalf("track-off not sent")
	}
	rawOff := f.waitSMTP(before+1, 30*time.Second)
	dataOff := string(unfoldMIME(rawOff[len(rawOff)-1].Data))
	if strings.Contains(dataOff, "/t/o/") || strings.Contains(dataOff, "/t/c/") {
		t.Fatalf("trackOpens/clicks false still rewrote MIME:\n%s", dataOff)
	}

	// Track on (defaults) → pixel + click rewrite; open/click emit events.
	before2 := f.smtpCount()
	f.Sink.Reset()
	rOn := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "track-on",
		"html": htmlTrackBody(),
	}, nil)
	harness.ExpectStatus(t, rOn, http.StatusAccepted)
	if f.waitSent(rOn.Str("id"), 90*time.Second).Str("state") != "sent" {
		t.Fatalf("track-on not sent")
	}
	rawOn := f.waitSMTP(before2+1, 30*time.Second)
	dataOn := rawOn[len(rawOn)-1].Data
	openTok := extractToken(dataOn, reOpenPixel)
	clickTok := extractToken(dataOn, reClick)
	if openTok == "" || clickTok == "" {
		t.Fatalf("missing track tokens open=%q click=%q\n%s", openTok, clickTok, dataOn)
	}
	ores := publicGET(t, f.Node.Base, "/t/o/"+openTok)
	defer func() { _ = ores.Body.Close() }()
	_ = readBody(ores)
	if ores.StatusCode != http.StatusOK {
		t.Fatalf("open pixel = %d", ores.StatusCode)
	}
	cres := publicGET(t, f.Node.Base, "/t/c/"+clickTok)
	defer func() { _ = cres.Body.Close() }()
	_ = readBody(cres)
	if cres.StatusCode != http.StatusFound {
		t.Fatalf("click = %d want 302", cres.StatusCode)
	}
	if loc := cres.Header.Get("Location"); loc != "https://dest.example/path" {
		t.Fatalf("click location=%q", loc)
	}
	_ = harness.WaitEvent(t, f.Sink, "tracking.opened", 45*time.Second)
	_ = harness.WaitEvent(t, f.Sink, "tracking.clicked", 45*time.Second)

	// Cannot force on when switches are off.
	f2 := startFixture(t, fixtureOpts{})
	enableOrgSwitch(t, f2.Owner, "tracking.opens", false)
	enableOrgSwitch(t, f2.Owner, "tracking.clicks", false)
	before3 := f2.smtpCount()
	rForce := f2.submit(map[string]any{
		"to": []map[string]any{{"address": f2.Email}}, "subject": "force-on",
		"html": htmlTrackBody(), "trackOpens": true, "trackClicks": true,
	}, nil)
	harness.ExpectStatus(t, rForce, http.StatusAccepted)
	_ = f2.waitSent(rForce.Str("id"), 90*time.Second)
	rawForce := f2.waitSMTP(before3+1, 30*time.Second)
	dataForce := string(unfoldMIME(rawForce[len(rawForce)-1].Data))
	if strings.Contains(dataForce, "/t/o/") || strings.Contains(dataForce, "/t/c/") {
		t.Fatalf("trackOpens=true forced pixel while switch off:\n%s", dataForce)
	}

	// Locked org opens=on → trackOpens=false ignored.
	f3 := startFixture(t, fixtureOpts{})
	lockOrgSwitch(t, f3.Owner, "tracking.opens", true)
	enableOrgSwitch(t, f3.Owner, "tracking.clicks", false)
	before4 := f3.smtpCount()
	rLock := f3.submit(map[string]any{
		"to": []map[string]any{{"address": f3.Email}}, "subject": "locked-on",
		"html": htmlTrackBody(), "trackOpens": false,
	}, nil)
	harness.ExpectStatus(t, rLock, http.StatusAccepted)
	_ = f3.waitSent(rLock.Str("id"), 90*time.Second)
	rawLock := f3.waitSMTP(before4+1, 30*time.Second)
	if extractToken(rawLock[len(rawLock)-1].Data, reOpenPixel) == "" {
		t.Fatalf("locked tracking.opens ignored trackOpens=false; MIME:\n%s", rawLock[len(rawLock)-1].Data)
	}
}

// listId alone sets List-Id without requiring tracking switches.
func TestP5_ListIdHeader(t *testing.T) {
	f := startFixture(t, fixtureOpts{})
	before := f.smtpCount()
	r := f.submit(map[string]any{
		"to": []map[string]any{{"address": f.Email}}, "subject": "listid-only",
		"text": "x", "listId": "news.example.com",
	}, nil)
	harness.ExpectStatus(t, r, http.StatusAccepted)
	_ = f.waitSent(r.Str("id"), 90*time.Second)
	raw := f.waitSMTP(before+1, 30*time.Second)
	if extractToken(raw[len(raw)-1].Data, reListID) != "news.example.com" {
		t.Fatalf("List-Id missing:\n%s", raw[len(raw)-1].Data)
	}
}
