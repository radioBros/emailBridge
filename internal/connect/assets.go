package connect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/attachments"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// AssetStore persists brandingAssets documents.
type AssetStore interface {
	InsertAsset(ctx context.Context, a *BrandingAsset) error
	GetAsset(ctx context.Context, id string) (*BrandingAsset, error)
	ListAssets(ctx context.Context, org string, kind AssetKind) ([]BrandingAsset, error)
	DeleteAsset(ctx context.Context, org, id string) error
	AssetQuota(ctx context.Context, org string) (count int, bytes int64, err error)
}

// ObjectPutter is the subset of objstore.Store used for branding.
type ObjectPutter interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, meta objstore.ObjectMeta) error
	Get(ctx context.Context, key string) (io.ReadCloser, objstore.ObjectInfo, error)
	Delete(ctx context.Context, key string) error
	DeletePrefix(ctx context.Context, prefix string) (int, error)
}

// UploadAsset stores a validated image in object storage + Mongo.
func (s *Service) UploadAsset(ctx context.Context, kind AssetKind, raw []byte) (*BrandingAsset, error) {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	if !kind.Valid() {
		return nil, errs.New(errs.CodeValidationFailed, "kind must be logo, logoDark, favicon or image.")
	}
	if s.Assets == nil || s.Objects == nil {
		return nil, errs.New(errs.CodeServiceUnavailable, "Branding asset storage is not configured.")
	}
	img, err := processUpload(kind, raw)
	if err != nil {
		return nil, err
	}
	if err := s.scanIfNeeded(ctx, org, img.Bytes); err != nil {
		return nil, err
	}
	n, total, err := s.Assets.AssetQuota(ctx, org)
	if err != nil {
		return nil, err
	}
	if n >= maxAssetsPerOrg || total+int64(len(img.Bytes)) > maxAssetsBytes {
		return nil, errs.New(errs.CodeBrandingAssetQuota, "Organisation branding asset quota exceeded.")
	}
	id := store.NewID(store.PrefixBrandingAsset)
	key, err := objstore.BrandingKey(org, id)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.asset.key")
	}
	sum := sha256.Sum256(img.Bytes)
	etag := hex.EncodeToString(sum[:])
	if err := s.Objects.Put(ctx, key, bytes.NewReader(img.Bytes), int64(len(img.Bytes)), objstore.ObjectMeta{
		Organization: org, ContentType: img.ContentType,
	}); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.asset.put")
	}
	now := s.clock().UTC().Truncate(time.Millisecond)
	a := &BrandingAsset{
		ID: id, OrganizationID: org, Kind: kind, ContentType: img.ContentType,
		Width: img.Width, Height: img.Height, Size: int64(len(img.Bytes)),
		ObjectKey: key, ETag: etag, CreatedAt: now,
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		a.CreatedBy = c.Actor()
	}
	if err := s.Assets.InsertAsset(ctx, a); err != nil {
		_ = s.Objects.Delete(ctx, key)
		return nil, err
	}
	a.URL = s.publicAssetURL(id)
	if s.Audit != nil {
		_ = s.Audit.Record(ctx, "organization.brandingAsset.created", audit.Target{Type: "brandingAsset", ID: id}, audit.Changes{
			"kind": {After: string(kind)}, "size": {After: a.Size},
		})
	}
	return a, nil
}

// ListAssets returns the organisation's branding assets.
func (s *Service) ListAssets(ctx context.Context, kind AssetKind) (*BrandingAssetList, error) {
	if err := tenancy.RequireRead(ctx, "settings"); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	if s.Assets == nil {
		return &BrandingAssetList{Data: []BrandingAsset{}}, nil
	}
	items, err := s.Assets.ListAssets(ctx, org, kind)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].URL = s.publicAssetURL(items[i].ID)
		items[i].OrganizationID = ""
		items[i].ObjectKey = ""
		items[i].ETag = ""
		items[i].CreatedBy = ""
	}
	return &BrandingAssetList{Data: items}, nil
}

// DeleteAsset removes an asset that is not referenced by branding.
func (s *Service) DeleteAsset(ctx context.Context, id string) error {
	if err := tenancy.Require(ctx, tenancy.PermSettingsWrite); err != nil {
		return err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	if !store.IsID(store.PrefixBrandingAsset, id) {
		return errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	if s.Assets == nil {
		return errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	a, err := s.Assets.GetAsset(ctx, id)
	if err != nil {
		return err
	}
	if a.OrganizationID != org {
		return errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	if s.Store != nil {
		o, err := s.Store.GetOrg(ctx, org)
		if err == nil && assetReferenced(o.Branding, id) {
			return errs.New(errs.CodeBrandingAssetInUse, "Unset the branding reference before deleting this asset.")
		}
	}
	if s.Objects != nil && a.ObjectKey != "" {
		_ = s.Objects.Delete(ctx, a.ObjectKey)
	}
	if err := s.Assets.DeleteAsset(ctx, org, id); err != nil {
		return err
	}
	if s.Audit != nil {
		_ = s.Audit.Record(ctx, "organization.brandingAsset.deleted", audit.Target{Type: "brandingAsset", ID: id}, nil)
	}
	return nil
}

// OpenPublicAsset streams a branding asset for the public endpoint.
func (s *Service) OpenPublicAsset(ctx context.Context, id string) (*BrandingAsset, io.ReadCloser, error) {
	if err := s.takePublicRate("branding-asset:"+clientIP(ctx), 120); err != nil {
		return nil, nil, err
	}
	if !store.IsID(store.PrefixBrandingAsset, id) {
		return nil, nil, errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	if s.Assets == nil || s.Objects == nil {
		return nil, nil, errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	a, err := s.Assets.GetAsset(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	rc, _, err := s.Objects.Get(ctx, a.ObjectKey)
	if err != nil {
		return nil, nil, errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	return a, rc, nil
}

// EraseOrgObjects deletes S3 branding objects for an organisation (org erasure hook).
func (s *Service) EraseOrgObjects(ctx context.Context, orgID string) error {
	if s.Objects == nil {
		return nil
	}
	prefix, err := objstore.BrandingPrefix(orgID)
	if err != nil {
		return err
	}
	_, err = s.Objects.DeletePrefix(ctx, prefix)
	return err
}

func (s *Service) publicAssetURL(id string) string {
	base := strings.TrimRight(s.PublicURL, "/")
	path := "/v1/public/branding/assets/" + id
	if base == "" {
		return path
	}
	return base + path
}

func (s *Service) requireAsset(ctx context.Context, org, id string, want AssetKind) error {
	if id == "" {
		return nil
	}
	if !store.IsID(store.PrefixBrandingAsset, id) {
		return errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	if s.Assets == nil {
		return errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	a, err := s.Assets.GetAsset(ctx, id)
	if err != nil {
		return err
	}
	if a.OrganizationID != org {
		return errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	if a.Kind != want {
		return errs.New(errs.CodeBrandingAssetKind, "Asset kind does not match the branding field.")
	}
	return nil
}

func (s *Service) scanIfNeeded(ctx context.Context, org string, raw []byte) error {
	if !s.avOn(ctx, org) || s.Scanner == nil {
		return nil
	}
	r := s.Scanner.Scan(ctx, bytes.NewReader(raw), int64(len(raw)))
	if r.Status == attachments.ScanInfected {
		return errs.New(errs.CodeAttachmentInfected, "The uploaded file failed the virus scan.")
	}
	return nil
}

func (s *Service) avOn(ctx context.Context, org string) bool {
	if s.AVEnabled != nil {
		return s.AVEnabled(ctx, org)
	}
	reg := s.Registry
	if reg == nil {
		return false
	}
	sw, ok := reg.Get(switchAntivirus)
	if !ok {
		return false
	}
	v, _ := features.Resolve(sw, s.Env, features.Values{})
	return v.Bool != nil && *v.Bool
}

func assetReferenced(brand map[string]any, id string) bool {
	if brand == nil {
		return false
	}
	for _, k := range []string{"logoAssetId", "logoDarkAssetId", "faviconAssetId"} {
		if v, ok := brand[k].(string); ok && v == id {
			return true
		}
	}
	return false
}

func clientIP(ctx context.Context) string {
	if c, ok := tenancy.CallerFrom(ctx); ok && c.UserID != "" {
		return c.UserID
	}
	return "anon"
}
