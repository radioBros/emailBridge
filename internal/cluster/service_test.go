package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestClusterAndDrainWithBus(t *testing.T) {
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	ctx := context.Background()
	b.SetAccountsProvider(func() int { return 4 })
	b.SetPartitionLoad("evt", 3)
	_ = b.RegisterNode(ctx, bus.NodeInfo{NodeID: "n1", Roles: []string{"api", "sync"}, Version: "v", StartedAt: time.Now().UTC()})
	_ = b.RegisterNode(ctx, bus.NodeInfo{NodeID: "n2", Roles: []string{"webhook"}, Version: "v", StartedAt: time.Now().UTC()})
	_, _ = b.Claim(ctx, scheduler.LeaderKey, []byte("n1"), 15*time.Second)

	svc := &Service{Bus: b, Log: nil}
	view, err := svc.Cluster(ctx)
	if err != nil || view.Leader != "n1" || view.NodeCount != 2 {
		t.Fatalf("%+v %v", view, err)
	}
	var self NodeView
	for _, n := range view.Nodes {
		if n.ID == "n1" {
			self = n
		}
	}
	if self.Accounts != 4 || self.Partitions["evt"] != 3 {
		t.Fatalf("load: %+v", self)
	}
	if view.Totals.Accounts < 4 {
		t.Fatalf("totals: %+v", view.Totals)
	}

	got := make(chan struct{}, 1)
	unsub, _ := b.SubscribeCore(bus.ClusterDrainSubject("n2"), func(string, []byte) { got <- struct{}{} })
	defer func() { _ = unsub() }()
	if err := svc.Drain(ctx, "n2"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no drain publish")
	}
	if errs.CodeOf(svc.Drain(ctx, "nope")) != errs.CodeNotFound {
		t.Fatal("expected not found")
	}
}

func TestPatchQueueUnknown(t *testing.T) {
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	svc := &Service{Bus: b}
	paused := true
	if _, err := svc.PatchQueue(context.Background(), "MISSING", &paused, nil); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestRBACPermissions(t *testing.T) {
	owner := tenancy.Caller{Kind: tenancy.CallerUser, InstanceOwner: true, Role: tenancy.RoleViewer}
	admin := tenancy.Caller{Kind: tenancy.CallerUser, Role: tenancy.RoleAdmin, OrganizationID: "org"}
	if !owner.Allows(tenancy.PermClusterRead) || !owner.Allows(tenancy.PermClusterOperate) {
		t.Fatal("instance owner")
	}
	if admin.Allows(tenancy.PermClusterRead) || admin.Allows(tenancy.PermClusterOperate) {
		t.Fatal("org admin must not manage cluster")
	}
	if err := tenancy.Require(tenancy.WithCaller(context.Background(), admin), tenancy.PermClusterRead); err == nil {
		t.Fatal("require")
	}
}
