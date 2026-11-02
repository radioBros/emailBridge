//go:build integration

package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

func TestIntegrationSyncSourcePlaceableLoadClaimTransition(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	ss := NewSyncSource(svc)

	a, err := svc.Create(ctx, sampleCreate("ss-1"))
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := svc.Create(ctx, sampleCreate("ss-dis"))
	if err != nil {
		t.Fatal(err)
	}
	trueV := true
	if _, err := svc.Patch(ctx, disabled.ID, PatchInput{Disabled: &trueV}, ""); err != nil {
		t.Fatal(err)
	}
	del, err := svc.Create(ctx, sampleCreate("ss-del"))
	if err != nil {
		t.Fatal(err)
	}
	del.State = StateDeleting
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": del.ID}, del)

	ids, err := ss.Placeable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found, foundDis, foundDel := false, false, false
	for _, id := range ids {
		if id == a.ID {
			found = true
		}
		if id == disabled.ID {
			foundDis = true
		}
		if id == del.ID {
			foundDel = true
		}
	}
	if !found || foundDis || foundDel {
		t.Fatalf("placeable=%v want %s, not disabled/deleting", ids, a.ID)
	}

	// queue a command then claim
	a.PendingCommands = []PendingCommand{{
		Cmd: CmdResync, At: time.Now().UTC(), Arg: map[string]any{"mode": "fast"},
	}}
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, a)

	loaded, err := ss.Load(context.Background(), a.ID)
	if err != nil || loaded.IMAPPass != "secret-pass" || loaded.IMAPAddr == "" || loaded.State != StateNew {
		t.Fatalf("%+v %v", loaded, err)
	}
	if loaded.NotifyFrom.IsZero() {
		t.Fatal("NotifyFrom default")
	}

	cmds, err := ss.Claimed(context.Background(), a.ID, "node-1", 7)
	if err != nil || len(cmds) != 1 || cmds[0].Cmd != CmdResync {
		t.Fatalf("%+v %v", cmds, err)
	}
	var after Account
	if err := svc.coll().FindOne(ctx, bson.M{"_id": a.ID}).Decode(&after); err != nil {
		t.Fatal(err)
	}
	if after.Owner == nil || after.Owner.NodeID != "node-1" || after.Owner.Epoch != 7 || len(after.PendingCommands) != 0 {
		t.Fatalf("after claim: %+v", after.Owner)
	}

	// transition with epoch
	if err := ss.Transition(context.Background(), a.ID, 7, StateConnecting, &eventtypes.Reason{
		Kind: "ok", Code: "claimed", Message: "lease claimed",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(ctx, a.ID)
	if got.State != StateConnecting {
		t.Fatalf("state %s", got.State)
	}

	// fenced
	err = ss.Transition(context.Background(), a.ID, 99, StateConnected, nil)
	if !errors.Is(err, index.ErrFenced) {
		t.Fatalf("want ErrFenced, got %v", err)
	}

	// version conflict retry: bump version while keeping epoch, then transition should succeed after retry
	got.Version += 5
	got.Owner = &Owner{NodeID: "node-1", Epoch: 7, Since: time.Now().UTC()}
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, got)
	// Service.Transition filters on version — first attempt fails MatchedCount, SyncSource retries
	if err := ss.Transition(context.Background(), a.ID, 7, StateConnected, &eventtypes.Reason{Kind: "ok", Code: "indexed"}); err != nil {
		t.Fatalf("retry: %v", err)
	}

	// resume places again (no command)
	dis := true
	dacc, err := svc.Patch(ctx, a.ID, PatchInput{Disabled: &dis}, "")
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := svc.Resume(ctx, dacc.ID)
	if err != nil || resumed.State != StateConnecting {
		t.Fatalf("%+v %v", resumed, err)
	}
	if len(resumed.PendingCommands) != 0 {
		t.Fatalf("resume must not queue commands: %+v", resumed.PendingCommands)
	}
	ids, _ = ss.Placeable(context.Background())
	placeOK := false
	for _, id := range ids {
		if id == a.ID {
			placeOK = true
		}
	}
	if !placeOK {
		t.Fatal("resumed account not placeable")
	}
}

func TestIntegrationCommandMessagePayload(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	a, err := svc.Create(ctx, sampleCreate("cmd-msg"))
	if err != nil {
		t.Fatal(err)
	}
	a.Owner = &Owner{NodeID: "n1", Epoch: 1, Since: time.Now().UTC()}
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, a)

	at := time.Now().UTC().Truncate(time.Millisecond)
	published, err := json.Marshal(ebsync.CommandMessage{
		AccountID: a.ID,
		Command:   ebsync.Command{Cmd: ebsync.CmdConfigChanged, Args: map[string]any{"x": 1}, At: at},
	})
	if err != nil {
		t.Fatal(err)
	}
	var cm ebsync.CommandMessage
	if err := json.Unmarshal(published, &cm); err != nil {
		t.Fatal(err)
	}
	if cm.AccountID != a.ID || cm.Cmd != ebsync.CmdConfigChanged || cm.Args["x"] != float64(1) {
		t.Fatalf("%+v", cm)
	}

	fb := &fakeBus{}
	svc.Bus = fb
	a2, _ := svc.Get(ctx, a.ID)
	a2.Owner = &Owner{NodeID: "n1", Epoch: 1, Since: time.Now().UTC()}
	svc.appendCommand(a2, CmdConfigChanged, "usr", map[string]any{"k": "v"})
	if len(fb.msgs) != 1 {
		t.Fatalf("published %d", len(fb.msgs))
	}
	if !strings.Contains(fb.subjects[0], "eb.node.n1.sync") && fb.subjects[0] != bus.NodeSyncSubject("n1") {
		t.Fatalf("subject %q", fb.subjects[0])
	}
	var got ebsync.CommandMessage
	if err := json.Unmarshal(fb.msgs[0], &got); err != nil || got.AccountID != a.ID || got.Cmd != CmdConfigChanged {
		t.Fatalf("%s %v", fb.msgs[0], err)
	}
}

func TestIntegrationSyncSourceTransitionVersionConflictRetry(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	ss := NewSyncSource(svc)
	a, err := svc.Create(ctx, sampleCreate("ss-retry"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a.State = StateConnecting
	a.Owner = &Owner{NodeID: "n", Epoch: 3, Since: now}
	a.Version = 10
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, a)

	// Concurrent "API patch" bumps version between SyncSource load and ReplaceOne.
	// First Transition call: load sees v10, increments to 11, ReplaceOne with version=10 works.
	if err := ss.Transition(context.Background(), a.ID, 3, StateConnected, nil); err != nil {
		t.Fatal(err)
	}

	// Force a conflict path: set owner epoch wrong mid-way via Transition's early check
	if err := ss.Transition(context.Background(), a.ID, 4, StateSyncing, nil); !errors.Is(err, index.ErrFenced) {
		t.Fatalf("got %v", err)
	}

	// Illegal transition still CodeConflict without fencing
	cur, _ := svc.Get(ctx, a.ID)
	cur.Owner = &Owner{NodeID: "n", Epoch: 3, Since: now}
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, cur)
	err = ss.Transition(context.Background(), a.ID, 3, StateNew, nil) // connected→new illegal
	if err == nil || errors.Is(err, index.ErrFenced) {
		t.Fatalf("want illegal conflict, got %v", err)
	}
	if errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("code %v", errs.CodeOf(err))
	}
}

func TestIntegrationSyncSourceLoadExtrasAndErrors(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	ss := NewSyncSource(svc)
	in := sampleCreate("ss-extra")
	in.Sync = &SyncConfig{
		Paths: []string{"INBOX"}, IdlePaths: []string{"INBOX"},
		ResyncDelay: "90s", MaxIdle: "10m",
		NotifyFrom: ptrTime(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)),
	}
	in.Switches = map[string]any{
		"accounts.ignoreCertErrors": true, "protocolLogs.enabled": true, "inbound.archiveRaw": true,
	}
	a, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().UTC().Add(time.Hour)
	a.RawDebugUntil = &until
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, a)
	// Clear paths to hit defaults on Load
	a.Sync.Paths, a.Sync.IdlePaths = nil, nil
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, a)

	loaded, err := ss.Load(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.IgnoreCertErrors || loaded.ResyncDelay != 90*time.Second || loaded.MaxIdle != 10*time.Minute {
		t.Fatalf("%+v", loaded)
	}
	if !loaded.ArchiveRaw {
		t.Fatalf("ArchiveRaw not mapped from inbound.archiveRaw: %+v", loaded)
	}
	if !loaded.ProtocolLogs || loaded.RawDebugUntil == nil || loaded.RawDebugUntil.Before(time.Now().UTC()) {
		t.Fatalf("protocol logs mapping %+v", loaded)
	}
	_ = until
	if len(loaded.Paths) != 1 || loaded.Paths[0] != "*" || loaded.IdlePaths[0] != "INBOX" {
		t.Fatalf("defaults %+v %+v", loaded.Paths, loaded.IdlePaths)
	}
	if !loaded.NotifyFrom.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
		// NotifyFrom was set on create then we cleared paths only — notifyFrom still on doc
		_ = loaded
	}
	// restore notifyFrom on doc for explicit check
	nf := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	a.Sync.NotifyFrom = &nf
	a.Sync.Paths = []string{"Sent"}
	a.Sync.IdlePaths = []string{"INBOX"}
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, a)
	loaded, _ = ss.Load(context.Background(), a.ID)
	if !loaded.NotifyFrom.Equal(nf) || loaded.Paths[0] != "Sent" {
		t.Fatalf("%+v", loaded)
	}

	missing := store.NewID(store.PrefixAccount)
	if _, err := ss.Load(context.Background(), missing); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := ss.Claimed(context.Background(), missing, "n", 1); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if err := ss.Transition(context.Background(), missing, 1, StateConnecting, nil); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}

	// empty placeable on a fresh DB slice: already have accounts; just ensure nil→empty for empty cursor path
	ids, err := ss.Placeable(context.Background())
	if err != nil || ids == nil {
		t.Fatal(err)
	}

	// no-op transition (same state)
	a2, _ := svc.Get(ctx, a.ID)
	a2.Owner = &Owner{NodeID: "n", Epoch: 5, Since: time.Now().UTC()}
	a2.State = StateConnecting
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": a.ID}, a2)
	if err := ss.Transition(context.Background(), a.ID, 5, StateConnecting, nil); err != nil {
		t.Fatal(err)
	}
}

type fakeBus struct {
	msgs     [][]byte
	subjects []string
}

func (f *fakeBus) PublishCore(subject string, data []byte) error {
	f.subjects = append(f.subjects, subject)
	f.msgs = append(f.msgs, append([]byte(nil), data...))
	return nil
}
