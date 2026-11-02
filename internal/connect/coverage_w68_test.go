package connect

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/attachments"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestRepoAssetCRUDAndMigration(t *testing.T) {
	db := testMongo(t)
	ctx := context.Background()
	mig := MigrationAssets(44)
	if mig.Number != 44 || mig.Up == nil {
		t.Fatal(mig)
	}
	if err := mig.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	r := &Repo{DB: db}
	org := store.NewID(store.PrefixOrganization)
	id := store.NewID(store.PrefixBrandingAsset)
	a := &BrandingAsset{
		ID: id, OrganizationID: org, Kind: AssetKindLogo, ContentType: "image/png",
		Width: 8, Height: 8, Size: 42, ObjectKey: "branding/" + org + "/" + id, ETag: "abc",
		CreatedAt: time.Now().UTC(),
	}
	if err := r.InsertAsset(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetAsset(ctx, id)
	if err != nil || got.Kind != AssetKindLogo {
		t.Fatal(err, got)
	}
	if _, err := r.GetAsset(ctx, store.NewID(store.PrefixBrandingAsset)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	list, err := r.ListAssets(ctx, org, AssetKindLogo)
	if err != nil || len(list) != 1 {
		t.Fatal(err, list)
	}
	listAll, err := r.ListAssets(ctx, org, "")
	if err != nil || len(listAll) != 1 {
		t.Fatal(err)
	}
	n, total, err := r.AssetQuota(ctx, org)
	if err != nil || n != 1 || total != 42 {
		t.Fatalf("%d %d %v", n, total, err)
	}
	if err := r.DeleteAsset(ctx, org, id); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteAsset(ctx, org, id); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
}

func TestWebPDimsVariantsAndICO(t *testing.T) {
	// VP8L (webpDims gates on len≥30 before switching)
	raw := make([]byte, 30)
	copy(raw[0:], []byte("RIFF"))
	copy(raw[8:], []byte("WEBPVP8L"))
	bits := uint32((9) | (19 << 14)) // 10×20
	binary.LittleEndian.PutUint32(raw[21:25], bits)
	w, h, err := webpDims(raw)
	if err != nil || w != 10 || h != 20 {
		t.Fatalf("VP8L %d×%d %v", w, h, err)
	}
	// VP8X
	rawX := make([]byte, 30)
	copy(rawX[0:], []byte("RIFF"))
	copy(rawX[8:], []byte("WEBPVP8X"))
	rawX[24], rawX[25], rawX[26] = 9, 0, 0
	rawX[27], rawX[28], rawX[29] = 19, 0, 0
	w, h, err = webpDims(rawX)
	if err != nil || w != 10 || h != 20 {
		t.Fatalf("VP8X %d×%d %v", w, h, err)
	}
	if _, _, err := webpDims([]byte("RIFF....WEBPXXXX")); err == nil {
		t.Fatal("want eof on unknown chunk")
	}
	if _, _, err := webpDims(make([]byte, 10)); err == nil {
		t.Fatal("short")
	}
	w, h = icoDims([]byte{0, 0, 1, 0, 1, 0, 0, 0})
	if w != 256 || h != 256 {
		t.Fatalf("ico 0→256: %d×%d", w, h)
	}
	// oversized webp
	big := make([]byte, 30)
	copy(big[0:], []byte("RIFF"))
	copy(big[8:], []byte("WEBPVP8 "))
	binary.LittleEndian.PutUint16(big[26:28], 5000)
	binary.LittleEndian.PutUint16(big[28:30], 5000)
	if _, err := processUpload(AssetKindLogo, big); errs.CodeOf(err) != errs.CodeBrandingAssetDimensions {
		t.Fatal(err)
	}
}

func TestUploadAVInfectedAndAvOn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		buf := make([]byte, 16)
		_, _ = io.ReadFull(c, buf[:10])
		for {
			var hdr [4]byte
			if _, err := io.ReadFull(c, hdr[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint32(hdr[:])
			if n == 0 {
				_, _ = io.WriteString(c, "stream: Eicar-Test-Signature FOUND\x00")
				return
			}
			_, _ = io.CopyN(io.Discard, c, int64(n))
		}
	}()

	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	s := NewService(st, nil)
	s.Assets = newMemAssets()
	s.Objects = newMemObjects()
	s.AVEnabled = func(context.Context, string) bool { return true }
	s.Scanner = &attachments.Scanner{Addr: ln.Addr().String(), Timeout: 2 * time.Second, MaxSize: maxLogoBytes}
	if _, err := s.UploadAsset(adminCtx(org), AssetKindLogo, tinyPNG(t)); errs.CodeOf(err) != errs.CodeAttachmentInfected {
		t.Fatal(err)
	}

	s2 := NewService(st, nil)
	s2.Registry = features.DefaultRegistry()
	s2.Env = "production"
	if s2.avOn(context.Background(), org) {
		t.Fatal("default antivirus should be off")
	}
	if err := s2.EraseOrgObjects(context.Background(), org); err != nil {
		t.Fatal(err)
	}
}

func TestBrandingAssetURLsAndPaletteDark(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	s := NewService(st, nil)
	s.Assets = newMemAssets()
	s.Objects = newMemObjects()
	s.PublicURL = "https://eb.test"
	ctx := adminCtx(org)

	logo, err := s.UploadAsset(ctx, AssetKindLogo, tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	dark, err := s.UploadAsset(ctx, AssetKindLogoDark, tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	fav, err := s.UploadAsset(ctx, AssetKindFavicon, tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	lid, did, fid := logo.ID, dark.ID, fav.ID
	b, ver, err := s.UpdateBranding(ctx, BrandingUpdate{
		LogoAssetID: &lid, LogoDarkAssetID: &did, FaviconAssetID: &fid,
		Palette: &BrandingPalette{
			Dark: map[string]string{
				"primary": "#166534", "on-primary": "#FFFFFF",
				"surface": "#121212", "on-surface": "#EEEEEE",
				"background": "#000000", "on-background": "#FFFFFF",
			},
		},
	}, "")
	if err != nil || ver < 2 {
		t.Fatal(err, ver)
	}
	if b.LogoURL == "" || b.LogoDarkURL == "" || b.FaviconURL == "" {
		t.Fatalf("%+v", b)
	}
	got, _, err := s.GetBranding(ctx)
	if err != nil || got.LogoAssetID != lid {
		t.Fatal(err, got)
	}
	bad := &BrandingPalette{Dark: map[string]string{
		"surface": "#FFFFFF", "on-surface": "#EEEEEE", // poor contrast
	}}
	if _, _, err := s.UpdateBranding(ctx, BrandingUpdate{Palette: bad}, ""); errs.CodeOf(err) != errs.CodeBrandingPaletteContrast {
		t.Fatal(err)
	}
	empty := ""
	if _, _, err := s.UpdateBranding(ctx, BrandingUpdate{LogoAssetID: &empty, LogoDarkAssetID: &empty, FaviconAssetID: &empty}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenPublicAsset(context.Background(), "not-an-id"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, _, err := s.OpenPublicAsset(context.Background(), store.NewID(store.PrefixBrandingAsset)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if err := s.requireAsset(ctx, org, "", AssetKindLogo); err != nil {
		t.Fatal(err)
	}
	if err := s.requireAsset(ctx, org, "bas_nope", AssetKindLogo); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	list, err := s.ListAssets(ctx, "")
	if err != nil || len(list.Data) < 3 {
		t.Fatal(err, list)
	}
	s.Assets = nil
	emptyList, err := s.ListAssets(ctx, "")
	if err != nil || len(emptyList.Data) != 0 {
		t.Fatal(err)
	}
}

func TestPublicAssetStreamBody(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	s := NewService(st, nil)
	s.Assets = newMemAssets()
	s.Objects = newMemObjects()
	a, err := s.UploadAsset(adminCtx(org), AssetKindImage, tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	m := &Module{svc: s}
	stream, err := m.publicAsset(context.Background(), &publicAssetIn{ID: a.ID})
	if err != nil || stream == nil || stream.Body == nil {
		t.Fatal(err)
	}
	hw := &humaWriter{h: map[string]string{}, buf: &bytes.Buffer{}}
	stream.Body(hw)
	if hw.status != http.StatusOK || hw.buf.Len() == 0 || hw.h["ETag"] == "" {
		t.Fatalf("status=%d len=%d hdr=%+v", hw.status, hw.buf.Len(), hw.h)
	}
	if _, err := m.deleteAsset(adminCtx(org), &deleteAssetIn{ID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.deleteAsset(adminCtx(org), &deleteAssetIn{ID: a.ID}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
}

func TestSanitizeStylesAndSVGDetect(t *testing.T) {
	got := SanitizeHeadHTML(`<style>@import url("http://evil"); body{expression(alert(1))}</style>`)
	if !bytes.Contains([]byte(got), []byte("stripped")) && !bytes.Contains([]byte(got), []byte("/*stripped")) {
		if bytes.Contains([]byte(got), []byte("http://evil")) && bytes.Contains([]byte(got), []byte("expression")) {
			t.Fatalf("dangerous css kept: %s", got)
		}
	}
	// Direct scrubStyles paths (same package).
	_ = scrubStyles(`<style>@import url("https://ok/x.css"); color:red</style>`)
	_ = scrubStyles(`<style>@import url("http://bad");</style>`)
	_ = scrubStyles(`<style>behavior:url(x); -moz-binding:x; expression(1)</style>`)
	_ = scrubStyles(`<style`) // malformed open
	_ = scrubStyles(`no style here`)
	if attrValue(`<meta content = "x">`, "content") == "" {
		t.Fatal("attr with spaces")
	}
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	if _, err := processUpload(AssetKindLogo, svg); errs.CodeOf(err) != errs.CodeBrandingAssetType {
		t.Fatal(err)
	}
	detectSVG := []byte("  <svg>")
	if _, err := detectImageType(detectSVG); errs.CodeOf(err) != errs.CodeBrandingAssetType {
		t.Fatal(err)
	}
}

func TestClientIPAndUploadEdges(t *testing.T) {
	_ = clientIP(context.Background())
	org := store.NewID(store.PrefixOrganization)
	st := newMemStore()
	st.orgs[org] = &orgDoc{ID: org, Name: "A", Version: 1}
	s := NewService(st, nil)
	if _, err := s.UploadAsset(adminCtx(org), AssetKindLogo, tinyPNG(t)); errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatal(err) // Assets/Objects nil
	}
	s.Assets = newMemAssets()
	s.Objects = newMemObjects()
	if _, err := s.UploadAsset(adminCtx(org), AssetKind("nope"), tinyPNG(t)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	// mergeColourMap / paletteFromBrand edge
	p := mergePalette(map[string]any{"light": map[string]any{"primary": "#111111"}, "dark": "bad"}, &BrandingPalette{
		Light: map[string]string{"secondary": "#222222"},
		Dark:  map[string]string{"primary": "#333333"},
	})
	if asStringMap(p["light"])["secondary"] != "#222222" {
		t.Fatal(p)
	}
	_ = paletteFromBrand(map[string]any{"palette": "nope"})
	_ = paletteFromBrand(nil)
	_ = validatePalette(nil)
	_ = asAnyMap(nil)
	_ = asAnyMap(map[string]any{"a": 1})
}

// humaWriter is a minimal huma.Context for StreamResponse bodies.
type humaWriter struct {
	h      map[string]string
	buf    *bytes.Buffer
	status int
}

func (h *humaWriter) Operation() *huma.Operation                 { return nil }
func (h *humaWriter) Context() context.Context                   { return context.Background() }
func (h *humaWriter) TLS() *tls.ConnectionState                  { return nil }
func (h *humaWriter) Version() huma.ProtoVersion                 { return huma.ProtoVersion{} }
func (h *humaWriter) Method() string                             { return http.MethodGet }
func (h *humaWriter) Host() string                               { return "eb.test" }
func (h *humaWriter) RemoteAddr() string                         { return "127.0.0.1:1" }
func (h *humaWriter) URL() url.URL                               { return url.URL{} }
func (h *humaWriter) Param(string) string                        { return "" }
func (h *humaWriter) Query(string) string                        { return "" }
func (h *humaWriter) Header(string) string                       { return "" }
func (h *humaWriter) EachHeader(func(string, string))            {}
func (h *humaWriter) BodyReader() io.Reader                      { return nil }
func (h *humaWriter) GetMultipartForm() (*multipart.Form, error) { return nil, nil }
func (h *humaWriter) SetReadDeadline(time.Time) error            { return nil }
func (h *humaWriter) SetStatus(code int)                         { h.status = code }
func (h *humaWriter) Status() int                                { return h.status }
func (h *humaWriter) SetHeader(name, value string)               { h.h[name] = value }
func (h *humaWriter) AppendHeader(name, value string)            { h.h[name] += value }
func (h *humaWriter) BodyWriter() io.Writer                      { return h.buf }
