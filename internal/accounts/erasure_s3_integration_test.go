//go:build integration

package accounts

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/test/containers"
)

// TestIntegrationErasureDeletesAllObjects: outbound/archive prefixes and sandbox captures (keyed by
// capture ID, not by account) are gone after erasure; another account's objects stay.
func TestIntegrationErasureDeletesAllObjects(t *testing.T) {
	svc, ctx, org := testSvc(t)
	ep, ak, sk := containers.S3(t)
	st, err := objstore.Open(objstore.Options{Endpoint: "http://" + ep, Region: "us-east-1", AccessKey: ak, SecretKey: sk, Bucket: "eb-erasure", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	svc.Objects = st
	a, err := svc.Create(ctx, sampleCreate("erase-s3"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.Create(ctx, sampleCreate("keep-s3"))
	if err != nil {
		t.Fatal(err)
	}
	put := func(key string) {
		if err := st.Put(ctx, key, bytes.NewReader([]byte("x")), 1, objstore.ObjectMeta{Organization: org}); err != nil {
			t.Fatal(err)
		}
	}
	mine := []string{
		fmt.Sprintf("outbound/%s/%s/m1.eml", org, a.ID),
		fmt.Sprintf("archive/%s/%s/2026/10/msg1.eml", org, a.ID),
		fmt.Sprintf("sandbox/%s/cap_mine.eml", org),
	}
	theirs := []string{fmt.Sprintf("sandbox/%s/cap_theirs.eml", org), fmt.Sprintf("outbound/%s/%s/m2.eml", org, other.ID)}
	for _, k := range append(append([]string{}, mine...), theirs...) {
		put(k)
	}
	_, _ = svc.DB.Collection("capturedMessages").InsertMany(ctx, []any{
		bson.M{"_id": "cap_mine", "organizationId": org, "accountId": a.ID, "rawRef": mine[2]},
		bson.M{"_id": "cap_theirs", "organizationId": org, "accountId": other.ID, "rawRef": theirs[0]},
	})
	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); svc.Jobs.Run(rctx, 1) }()
	defer func() { cancel(); <-done }()
	j, err := svc.Jobs.Create(ctx, "erasure", map[string]string{"accountId": a.ID, "organizationId": org})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, svc, ctx, j.ID, jobs.Succeeded)
	for _, k := range mine {
		if _, err := st.Stat(context.Background(), k); err == nil {
			t.Errorf("%s survived erasure", k)
		}
	}
	for _, k := range theirs {
		if _, err := st.Stat(context.Background(), k); err != nil {
			t.Errorf("%s of another account was deleted: %v", k, err)
		}
	}
}
