package connect

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestUploadAssetForbiddenAndQuota(t *testing.T) {
	s := NewService(newMemStore(), nil)
	s.Assets = newMemAssets()
	s.Objects = newMemObjects()
	if _, err := s.UploadAsset(context.Background(), AssetKindLogo, tinyPNG(t)); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal(err)
	}
	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	s.Store = st
	ctx := adminCtx(org)
	// fill quota count
	assets := s.Assets.(*memAssets)
	for i := 0; i < maxAssetsPerOrg; i++ {
		id := store.NewID(store.PrefixBrandingAsset)
		_ = assets.InsertAsset(ctx, &BrandingAsset{ID: id, OrganizationID: org, Kind: AssetKindImage, Size: 10})
	}
	_, err := s.UploadAsset(ctx, AssetKindLogo, tinyPNG(t))
	if errs.CodeOf(err) != errs.CodeBrandingAssetQuota {
		t.Fatal(err)
	}
}

func TestRequireAssetKindMismatch(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	s := NewService(st, nil)
	s.Assets = newMemAssets()
	s.Objects = newMemObjects()
	ctx := adminCtx(org)
	a, err := s.UploadAsset(ctx, AssetKindImage, tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.requireAsset(ctx, org, a.ID, AssetKindLogo); errs.CodeOf(err) != errs.CodeBrandingAssetKind {
		t.Fatal(err)
	}
	viewer := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleViewer,
	})
	if _, err := s.ListAssets(viewer, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadAsset(viewer, AssetKindLogo, tinyPNG(t)); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}
}

func TestModuleHandlersBrandingAssets(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	s := NewService(st, nil)
	s.Assets = newMemAssets()
	s.Objects = newMemObjects()
	s.PublicURL = "https://eb.example"
	m := &Module{svc: s}
	ctx := adminCtx(org)
	a, err := s.UploadAsset(ctx, AssetKindLogo, tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	list, err := m.listAssets(ctx, &listAssetsIn{})
	if err != nil || len(list.Body.Data) != 1 {
		t.Fatal(err)
	}
	stream, err := m.publicAsset(context.Background(), &publicAssetIn{ID: a.ID})
	if err != nil || stream == nil {
		t.Fatal(err)
	}
	if _, err := m.deleteAsset(ctx, &deleteAssetIn{ID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.publicAsset(context.Background(), &publicAssetIn{ID: "bas_notreal0000000000000000"}); errs.CodeOf(err) != errs.CodeNotFound {
		// invalid ULID / not found
		if errs.CodeOf(err) == "" {
			t.Fatal(err)
		}
	}
}
