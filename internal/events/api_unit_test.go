package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/settings"
)

type fakeSwitches struct {
	body settings.SettingsBody
	err  error
}

func (f fakeSwitches) Effective(context.Context, string) (settings.SettingsBody, error) {
	return f.body, f.err
}

func TestSSEEnabledDefaults(t *testing.T) {
	a := &api{o: APIOptions{}}
	if !a.sseEnabled(context.Background(), "org_x") {
		t.Fatal("default on")
	}
	a.o.SSEEnabled = func(context.Context, string) bool { return false }
	if a.sseEnabled(context.Background(), "org_x") {
		t.Fatal("override off")
	}
	a.o.SSEEnabled = nil
	a.o.Settings = fakeSwitches{err: errors.New("boom")}
	if !a.sseEnabled(context.Background(), "org") {
		t.Fatal("error → on")
	}
	a.o.Settings = fakeSwitches{body: settings.SettingsBody{Switches: map[string]settings.SettingValue{}}}
	if !a.sseEnabled(context.Background(), "org") {
		t.Fatal("missing switch → on")
	}
	a.o.Settings = fakeSwitches{body: settings.SettingsBody{Switches: map[string]settings.SettingValue{
		"events.sse": {Value: "nope"},
	}}}
	if !a.sseEnabled(context.Background(), "org") {
		t.Fatal("non-bool → on")
	}
	a.o.Settings = fakeSwitches{body: settings.SettingsBody{Switches: map[string]settings.SettingValue{
		"events.sse": {Value: false},
	}}}
	if a.sseEnabled(context.Background(), "org") {
		t.Fatal("false")
	}
	a.o.Settings = fakeSwitches{body: settings.SettingsBody{Switches: map[string]settings.SettingValue{
		"events.sse": {Value: true},
	}}}
	if !a.sseEnabled(context.Background(), "org") {
		t.Fatal("true")
	}
}

func TestListQueryPageAndIDCursor(t *testing.T) {
	base := bson.M{"organizationId": "org_1"}
	f, opts, err := listQuery(base, "time", -1, 10, "", 2)
	if err != nil || f == nil || opts == nil {
		t.Fatalf("%v %v", f, err)
	}
	tok := encodeListCursor(listCursor{ID: "evt_b", Field: "_id", Desc: true})
	f, opts, err = listQuery(base, "_id", -1, 5, tok, 0)
	if err != nil || opts == nil {
		t.Fatalf("%v", err)
	}
	and, ok := f["$and"].([]bson.M)
	if !ok || len(and) != 2 {
		t.Fatalf("%#v", f)
	}
	bad := encodeListCursor(listCursor{ID: "evt_b", Field: "time", Desc: false})
	if _, _, err := listQuery(base, "time", -1, 5, bad, 0); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("mismatched cursor: %v", err)
	}
	if _, _, err := listQuery(base, "time", -1, 5, "%%%", 0); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("junk cursor: %v", err)
	}
	tok = encodeListCursor(listCursor{ID: "evt_b", Key: 123, Field: "time", Desc: true})
	if _, _, err := listQuery(base, "time", -1, 5, tok, 0); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad time key: %v", err)
	}
	tok = encodeListCursor(listCursor{ID: "evt_b", Key: time.Now().UTC().Format(time.RFC3339Nano), Field: "time", Desc: true})
	f, _, err = listQuery(base, "time", -1, 5, tok, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f["$and"]; !ok {
		t.Fatalf("%#v", f)
	}
}

func TestEventSortKeyDefault(t *testing.T) {
	d := doc{ID: "evt_1", Time: time.Unix(1, 0).UTC()}
	if eventSortKey(d, "_id") != "evt_1" {
		t.Fatal("id key")
	}
	if eventSortKey(d, "time") != d.Time.Format(time.RFC3339Nano) {
		t.Fatal("time key")
	}
}

func TestNewStreamLimiterDefault(t *testing.T) {
	l := newStreamLimiter(0)
	if l.max != MaxStreamsPerOrg {
		t.Fatalf("max %d", l.max)
	}
}

func TestDecodeEmptyCursor(t *testing.T) {
	raw := encodeListCursor(listCursor{})
	if _, err := decodeListCursor(raw); err == nil {
		t.Fatal("empty fields")
	}
}
