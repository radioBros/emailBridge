//go:build e2e

package p6

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

func tinyPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: 30, G: 120, B: 60, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func doMultipart(t *testing.T, c *harness.Client, method, path, contentType string, body []byte) harness.Resp {
	t.Helper()
	req, err := http.NewRequest(method, c.N.Base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", c.N.Base)
	req.Header.Set("X-Requested-With", "EmailBridge")
	if c.IP != "" {
		req.Header.Set("X-Forwarded-For", c.IP)
	}
	for k, v := range c.Hdr {
		req.Header.Set(k, v)
	}
	res, err := c.C.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := harness.Resp{Status: res.StatusCode, Header: res.Header, Body: raw}
	_ = json.Unmarshal(raw, &out.JSON)
	return out
}

func uploadBrandingAsset(t *testing.T, c *harness.Client, kind string, raw []byte) harness.Resp {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("kind", kind)
	part, err := w.CreateFormFile("file", "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(raw); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	return doMultipart(t, c, http.MethodPost, "/v1/connect/branding/assets", w.FormDataContentType(), body.Bytes())
}

func TestP6_BrandingAssets_UploadPublicDelete(t *testing.T) {
	f := startBase(t)
	enableOrgSwitch(t, f.Owner, "connect.hostedPage", true)
	png := tinyPNGBytes(t)

	up := uploadBrandingAsset(t, f.Owner, "logo", png)
	harness.ExpectStatus(t, up, http.StatusCreated)
	id := up.Str("id")
	if id == "" || !strings.HasPrefix(id, "bas_") {
		t.Fatalf("asset id: %s", up.Body)
	}
	url := up.Str("url")
	if !strings.Contains(url, "/v1/public/branding/assets/"+id) {
		t.Fatalf("url: %s", url)
	}

	list := f.Owner.Do(http.MethodGet, "/v1/connect/branding/assets", nil)
	harness.ExpectStatus(t, list, http.StatusOK)

	cur := f.Owner.Do(http.MethodGet, "/v1/connect/branding", nil)
	harness.ExpectStatus(t, cur, http.StatusOK)
	patched := withIfMatch(f.Owner, etagOf(cur), func() harness.Resp {
		return f.Owner.Do(http.MethodPatch, "/v1/connect/branding", map[string]any{
			"logoAssetId": id,
			"palette": map[string]any{
				"light": map[string]string{"primary": "#166534", "on-primary": "#FFFFFF"},
			},
		})
	})
	harness.ExpectStatus(t, patched, http.StatusOK)
	if patched.Str("logoAssetId") != id {
		t.Fatalf("logoAssetId: %s", patched.Body)
	}
	if !strings.Contains(patched.Str("logoUrl"), id) {
		t.Fatalf("logoUrl: %s", patched.Body)
	}

	delBusy := f.Owner.Do(http.MethodDelete, "/v1/connect/branding/assets/"+id, nil)
	if delBusy.Status != http.StatusConflict && problemCode(delBusy) != "branding_asset_in_use" {
		harness.ExpectProblem(t, delBusy, http.StatusConflict, "branding_asset_in_use")
	}

	pub := f.Node.NewClient()
	get := pub.Do(http.MethodGet, "/v1/public/branding/assets/"+id, nil)
	harness.ExpectStatus(t, get, http.StatusOK)
	if ct := get.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("content-type: %s", ct)
	}
	if get.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff missing")
	}
	if !strings.Contains(get.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("cache: %s", get.Header.Get("Cache-Control"))
	}

	link := f.Owner.Do(http.MethodPost, "/v1/connect-links/hosted", map[string]any{"ttlSeconds": 3600})
	harness.ExpectStatus(t, link, http.StatusCreated)
	token := tokenFromConnectURL(link.Str("url"))
	boot := pub.Do(http.MethodGet, "/v1/public/connect/"+token, nil)
	harness.ExpectStatus(t, boot, http.StatusOK)
	branding, _ := boot.JSON["branding"].(map[string]any)
	if branding == nil || fmt.Sprint(branding["logoUrl"]) == "" {
		t.Fatalf("bootstrap logo: %s", boot.Body)
	}

	// Cross-tenant: org B must not see or attach org A's asset.
	other, _ := orgBSession(t, f.Node, f.Owner)
	crossDel := other.Do(http.MethodDelete, "/v1/connect/branding/assets/"+id, nil)
	if crossDel.Status != http.StatusNotFound {
		harness.ExpectProblem(t, crossDel, http.StatusNotFound, "not_found")
	}
	crossPatch := other.Do(http.MethodPatch, "/v1/connect/branding", map[string]any{"logoAssetId": id})
	if crossPatch.Status == http.StatusOK && crossPatch.Str("logoAssetId") == id {
		t.Fatalf("cross-tenant referenced foreign asset")
	}

	cleared := withIfMatch(f.Owner, etagOf(patched), func() harness.Resp {
		return f.Owner.Do(http.MethodPatch, "/v1/connect/branding", map[string]any{
			"logoAssetId": "",
		})
	})
	harness.ExpectStatus(t, cleared, http.StatusOK)
	del := f.Owner.Do(http.MethodDelete, "/v1/connect/branding/assets/"+id, nil)
	harness.ExpectStatus(t, del, http.StatusNoContent)
	gone := pub.Do(http.MethodGet, "/v1/public/branding/assets/"+id, nil)
	if gone.Status != http.StatusNotFound {
		harness.ExpectProblem(t, gone, http.StatusNotFound, "not_found")
	}
}
