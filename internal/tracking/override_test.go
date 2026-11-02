package tracking

import "testing"

func TestEffectiveTracking(t *testing.T) {
	off := false
	on := true
	st := TrackState{Opens: true, Clicks: true}
	o, c := EffectiveTracking(st, &off, &off)
	if o || c {
		t.Fatal("should turn off")
	}
	o, c = EffectiveTracking(st, &on, nil)
	if !o || !c {
		t.Fatal("true override cannot force beyond switch; switch already on")
	}
	stOff := TrackState{Opens: false, Clicks: false}
	o, c = EffectiveTracking(stOff, &on, &on)
	if o || c {
		t.Fatal("cannot force on when switch off")
	}
	locked := TrackState{Opens: true, OpensLocked: true}
	o, _ = EffectiveTracking(locked, &off, nil)
	if !o {
		t.Fatal("locked ignores off")
	}
}

func TestPrepareListIDOnly(t *testing.T) {
	res, err := Prepare([]byte("k"), PrepareInput{
		OrgID: "org_1", OutboundID: "out_1", Recipient: "a@b.test",
		ListID: "news.example.com", HTML: "<p>x</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Headers["List-Id"] != "<news.example.com>" {
		t.Fatalf("%+v", res.Headers)
	}
}
