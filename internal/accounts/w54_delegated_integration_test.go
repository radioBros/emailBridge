//go:build integration

package accounts

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationW54DelegatedRefAndDelete(t *testing.T) {
	svc, ctx, org := testSvc(t)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	now := time.Now().UTC().Truncate(time.Millisecond)
	parent := store.NewID(store.PrefixAccount)
	_, _ = svc.coll().InsertOne(ctx, bson.M{
		"_id": parent, "organizationId": org, "email": "p@ex.com", "name": "P", "state": StateConnected,
		"oauth": bson.M{"appId": store.NewID(store.PrefixOAuthApp)},
		"imap":  bson.M{"host": "h", "port": 993, "tls": TLSImplicit, "auth": bson.M{"user": "p"}},
		"smtp":  bson.M{"host": "h", "port": 465, "tls": TLSImplicit, "auth": bson.M{"user": "p"}, "policy": PolicyStandard},
		"sync":  defaultSync(), "createdAt": now, "updatedAt": now, "version": int64(1),
	})

	otherOrg := store.NewID(store.PrefixOrganization)
	foreign := store.NewID(store.PrefixAccount)
	_, _ = svc.coll().InsertOne(ctx, bson.M{
		"_id": foreign, "organizationId": otherOrg, "email": "f@ex.com", "oauth": bson.M{"appId": "oap_x"},
	})
	if errs.CodeOf(svc.validateDelegatedRef(cctx, org, "", &DelegatedRef{Account: foreign, User: "s@ex.com"})) != errs.CodeNotFound {
		t.Fatal("other org must 404")
	}

	child, err := svc.Create(cctx, CreateInput{
		Name: "Shared", Email: "shared@ex.com", Provider: ProviderGraph,
		Delegated:  &DelegatedRef{Account: parent, User: "shared@ex.com"},
		SkipVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.Delegated == nil || child.Delegated.Account != parent {
		t.Fatalf("%#v", child.Delegated)
	}
	if _, err := svc.Delete(cctx, parent, ""); errs.CodeOf(err) != errs.CodeDelegatedInUse {
		t.Fatalf("delete parent: %v", err)
	}
	if _, err := svc.Delete(cctx, child.ID, formatETag(child.Version)); err != nil {
		t.Fatal(err)
	}
}
