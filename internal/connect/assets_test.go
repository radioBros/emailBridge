package connect

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"sync"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type memAssets struct {
	mu   sync.Mutex
	byID map[string]*BrandingAsset
}

func newMemAssets() *memAssets {
	return &memAssets{byID: map[string]*BrandingAsset{}}
}

func (m *memAssets) InsertAsset(_ context.Context, a *BrandingAsset) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *a
	m.byID[a.ID] = &cp
	return nil
}

func (m *memAssets) GetAsset(_ context.Context, id string) (*BrandingAsset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.byID[id]
	if !ok {
		return nil, errs.New(errs.CodeNotFound, "missing")
	}
	cp := *a
	return &cp, nil
}

func (m *memAssets) ListAssets(_ context.Context, org string, kind AssetKind) ([]BrandingAsset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BrandingAsset
	for _, a := range m.byID {
		if a.OrganizationID != org {
			continue
		}
		if kind != "" && a.Kind != kind {
			continue
		}
		out = append(out, *a)
	}
	return out, nil
}

func (m *memAssets) DeleteAsset(_ context.Context, org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.byID[id]
	if !ok || a.OrganizationID != org {
		return errs.New(errs.CodeNotFound, "missing")
	}
	delete(m.byID, id)
	return nil
}

func (m *memAssets) AssetQuota(_ context.Context, org string) (int, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	var total int64
	for _, a := range m.byID {
		if a.OrganizationID == org {
			n++
			total += a.Size
		}
	}
	return n, total, nil
}

type memObjects struct {
	mu   sync.Mutex
	data map[string][]byte
	meta map[string]objstore.ObjectMeta
}

func newMemObjects() *memObjects {
	return &memObjects{data: map[string][]byte{}, meta: map[string]objstore.ObjectMeta{}}
}

func (m *memObjects) Put(_ context.Context, key string, r io.Reader, size int64, meta objstore.ObjectMeta) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = b
	m.meta[key] = meta
	_ = size
	return nil
}

func (m *memObjects) Get(_ context.Context, key string) (io.ReadCloser, objstore.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return nil, objstore.ObjectInfo{}, objstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), objstore.ObjectInfo{Key: key, Size: int64(len(b)), ContentType: m.meta[key].ContentType}, nil
}

func (m *memObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	delete(m.meta, key)
	return nil
}

func (m *memObjects) DeletePrefix(_ context.Context, prefix string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(m.data, k)
			delete(m.meta, k)
			n++
		}
	}
	return n, nil
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 20, G: 100, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func adminCtx(org string) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleAdmin,
	})
}

func TestUploadListDeleteAsset(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "Acme", Version: 1}
	assets := newMemAssets()
	objs := newMemObjects()
	s := NewService(st, nil)
	s.Assets = assets
	s.Objects = objs
	s.PublicURL = "https://eb.example"
	ctx := adminCtx(org)

	a, err := s.UploadAsset(ctx, AssetKindLogo, tinyPNG(t))
	if err != nil || a.ID == "" || a.Kind != AssetKindLogo || a.URL == "" {
		t.Fatalf("%+v %v", a, err)
	}
	list, err := s.ListAssets(ctx, AssetKindLogo)
	if err != nil || len(list.Data) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	id := a.ID
	logoID := id
	_, _, err = s.UpdateBranding(ctx, BrandingUpdate{LogoAssetID: &logoID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAsset(ctx, id); errs.CodeOf(err) != errs.CodeBrandingAssetInUse {
		t.Fatalf("want in_use, got %v", err)
	}
	empty := ""
	_, _, err = s.UpdateBranding(ctx, BrandingUpdate{LogoAssetID: &empty}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAsset(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestUploadRejectsSVGAndCrossTenant(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	other := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	st.orgs[other] = &orgDoc{ID: other, Name: "B", Version: 1}
	s := NewService(st, nil)
	s.Assets = newMemAssets()
	s.Objects = newMemObjects()
	ctx := adminCtx(org)
	_, err := s.UploadAsset(ctx, AssetKindLogo, []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	if errs.CodeOf(err) != errs.CodeBrandingAssetType {
		t.Fatalf("%v", err)
	}
	a, err := s.UploadAsset(ctx, AssetKindImage, tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := adminCtx(other)
	if err := s.DeleteAsset(otherCtx, a.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("%v", err)
	}
	bad := a.ID
	_, _, err = s.UpdateBranding(otherCtx, BrandingUpdate{LogoAssetID: &bad}, "")
	if errs.CodeOf(err) != errs.CodeNotFound && errs.CodeOf(err) != errs.CodeBrandingAssetKind {
		// wrong kind (image vs logo) or not found — both ok for cross-tenant
		if errs.CodeOf(err) == "" {
			t.Fatal(err)
		}
	}
}

func TestPaletteContrastAndLegacyPrimary(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	s := NewService(st, nil)
	ctx := adminCtx(org)
	bad := &BrandingPalette{Light: map[string]string{
		"surface": "#FFFFFF", "on-surface": "#EEEEEE",
	}}
	_, _, err := s.UpdateBranding(ctx, BrandingUpdate{Palette: bad}, "")
	if errs.CodeOf(err) != errs.CodeBrandingPaletteContrast {
		t.Fatalf("%v", err)
	}
	col := "#166534"
	b, _, err := s.UpdateBranding(ctx, BrandingUpdate{PrimaryColor: &col}, "")
	if err != nil || b.PrimaryColor != "#166534" || b.Palette == nil || b.Palette.Light["primary"] != "#166534" {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestProcessUploadAndPublicOpen(t *testing.T) {
	_, err := processUpload(AssetKindLogo, []byte("not-an-image"))
	if errs.CodeOf(err) != errs.CodeBrandingAssetType {
		t.Fatal(err)
	}
	img, err := processUpload(AssetKindFavicon, tinyPNG(t))
	if err != nil || img.ContentType != "image/png" {
		t.Fatal(err)
	}
	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	s := NewService(st, nil)
	s.Assets = newMemAssets()
	s.Objects = newMemObjects()
	s.PublicURL = "https://eb.example"
	a, err := s.UploadAsset(adminCtx(org), AssetKindFavicon, tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	doc, rc, err := s.OpenPublicAsset(context.Background(), a.ID)
	if err != nil || doc.ID != a.ID {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if len(got) == 0 {
		t.Fatal("empty body")
	}
	if err := s.EraseOrgObjects(context.Background(), org); err != nil {
		t.Fatal(err)
	}
}

func TestDetectImageTypes(t *testing.T) {
	jpeg := append([]byte{0xff, 0xd8, 0xff, 0xe0}, make([]byte, 12)...)
	if ct, err := detectImageType(jpeg); err != nil || ct != "image/jpeg" {
		t.Fatalf("%s %v", ct, err)
	}
	webp := make([]byte, 30)
	copy(webp[0:], []byte("RIFF"))
	copy(webp[8:], []byte("WEBPVP8 "))
	if ct, err := detectImageType(webp); err != nil || ct != "image/webp" {
		t.Fatalf("%s %v", ct, err)
	}
}
