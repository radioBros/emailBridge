//go:build integration

package objstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/radiobros/emailbridge/internal/objstore"
)

func TestIntegrationGetMissingAndEmptyPrefix(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, _, err := s.Get(ctx, "no/such/key")
	if !errors.Is(err, objstore.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	n, err := s.DeletePrefix(ctx, "nothing-here/")
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// Put without content-type default
	key := "outbound/org_z/acc_z/out_z.eml"
	body := []byte("x")
	if err := s.Put(ctx, key, bytes.NewReader(body), 1, objstore.ObjectMeta{Organization: "org_z", Account: "acc_z"}); err != nil {
		t.Fatal(err)
	}
	rc, info, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	_, _ = io.Copy(io.Discard, rc)
	if info.Organization != "org_z" {
		t.Fatalf("%+v", info)
	}
	if err := s.List(ctx, "outbound/org_z/", func(objstore.ObjectInfo) error {
		return errors.New("stop")
	}); err == nil || err.Error() != "stop" {
		t.Fatalf("%v", err)
	}
}
