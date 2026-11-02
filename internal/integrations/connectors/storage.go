package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

const (
	TypeGDrive   = "gdrive"
	TypeOneDrive = "onedrive"
)

const defaultStorageFilter = `type in ["message.received", "message.sent"]`

// GDrive returns the Google Drive storage connector.
func GDrive() Def {
	return Def{
		ID: TypeGDrive, Version: "1.1.0", Name: "Google Drive", Category: CategoryStorage,
		Icon: "mdi-google-drive", Color: "#4285F4",
		Description: "Export attachments to Google Drive using a pasted OAuth refresh token. File bytes leave EmailBridge to Google.",
		DocsPath:    "/docs/integrations/gdrive",
		ConfigSchema: map[string]any{
			"type":     "object",
			"required": []any{"clientId", "clientSecret", "refreshToken"},
			"properties": map[string]any{
				"folderId":     map[string]any{"type": "string", "title": "Folder ID"},
				"clientId":     map[string]any{"type": "string", "title": "OAuth client ID"},
				"clientSecret": map[string]any{"type": "string", "writeOnly": true, "title": "OAuth client secret"},
				"refreshToken": map[string]any{"type": "string", "writeOnly": true, "title": "Refresh token"},
				"accessToken":  map[string]any{"type": "string", "writeOnly": true, "title": "Access token (optional cache)"},
				"tokenUrl":     map[string]any{"type": "string", "format": "uri", "title": "Token URL override"},
				"filter":       map[string]any{"type": "string", "title": "Event filter CEL", "default": defaultStorageFilter},
				"baseUrl":      map[string]any{"type": "string", "format": "uri", "title": "API base URL override"},
			},
		},
		SecretFields: []string{"clientSecret", "refreshToken", "accessToken"},
		Capabilities: []string{CapTest},
		Settings:     map[string]string{"integrations.gdrive.folderId": "{{config.folderId}}"},
		Endpoints: []EndpointTmpl{{
			Name: "Google Drive upload", Kind: KindIntegration,
			SinkConfig: map[string]string{"instanceId": "{{instance.id}}"}, PayloadAttachments: true,
		}},
		Routes: []RouteTmpl{{
			Name: "Upload attachments", EndpointIndex: 0, Filter: "{{config.filter}}",
		}},
		TestFunc: HTTPTester("Google Drive", func(cfg map[string]any, secrets map[string]string) (string, string, map[string]string, []byte, error) {
			tok, err := RefreshOAuthToken(context.Background(), nil, OAuthTokenInput{
				TokenURL: strCfg(cfg, "tokenUrl"), ClientID: strCfg(cfg, "clientId"),
				ClientSecret: secrets["clientSecret"], RefreshToken: secrets["refreshToken"],
				AccessToken: secrets["accessToken"], Provider: TypeGDrive,
			})
			if err != nil {
				return "", "", nil, nil, err
			}
			base := firstNonEmpty(strCfg(cfg, "baseUrl"), "https://www.googleapis.com")
			return http.MethodGet, strings.TrimRight(base, "/") + "/drive/v3/about?fields=user", map[string]string{
				"Authorization": "Bearer " + tok,
			}, nil, nil
		}),
	}
}

// OneDrive returns the OneDrive/SharePoint storage connector.
func OneDrive() Def {
	return Def{
		ID: TypeOneDrive, Version: "1.1.0", Name: "OneDrive / SharePoint", Category: CategoryStorage,
		Icon: "mdi-microsoft-onedrive", Color: "#094AB2",
		Description: "Export attachments to OneDrive or SharePoint using a pasted OAuth refresh token. File bytes leave EmailBridge to Microsoft.",
		DocsPath:    "/docs/integrations/onedrive",
		ConfigSchema: map[string]any{
			"type":     "object",
			"required": []any{"clientId", "clientSecret", "refreshToken"},
			"properties": map[string]any{
				"driveId":      map[string]any{"type": "string", "title": "Drive ID (optional)"},
				"folderPath":   map[string]any{"type": "string", "title": "Folder path", "default": "/EmailBridge"},
				"clientId":     map[string]any{"type": "string", "title": "OAuth client ID"},
				"clientSecret": map[string]any{"type": "string", "writeOnly": true, "title": "OAuth client secret"},
				"refreshToken": map[string]any{"type": "string", "writeOnly": true, "title": "Refresh token"},
				"accessToken":  map[string]any{"type": "string", "writeOnly": true, "title": "Access token (optional cache)"},
				"tokenUrl":     map[string]any{"type": "string", "format": "uri", "title": "Token URL override"},
				"filter":       map[string]any{"type": "string", "title": "Event filter CEL", "default": defaultStorageFilter},
				"baseUrl":      map[string]any{"type": "string", "format": "uri", "title": "Graph base URL override"},
			},
		},
		SecretFields: []string{"clientSecret", "refreshToken", "accessToken"},
		Capabilities: []string{CapTest},
		Settings: map[string]string{
			"integrations.onedrive.driveId":    "{{config.driveId}}",
			"integrations.onedrive.folderPath": "{{config.folderPath}}",
		},
		Endpoints: []EndpointTmpl{{
			Name: "OneDrive upload", Kind: KindIntegration,
			SinkConfig: map[string]string{"instanceId": "{{instance.id}}"}, PayloadAttachments: true,
		}},
		Routes: []RouteTmpl{{
			Name: "Upload attachments", EndpointIndex: 0, Filter: "{{config.filter}}",
		}},
		TestFunc: HTTPTester("OneDrive", func(cfg map[string]any, secrets map[string]string) (string, string, map[string]string, []byte, error) {
			tok, err := RefreshOAuthToken(context.Background(), nil, OAuthTokenInput{
				TokenURL: strCfg(cfg, "tokenUrl"), ClientID: strCfg(cfg, "clientId"),
				ClientSecret: secrets["clientSecret"], RefreshToken: secrets["refreshToken"],
				AccessToken: secrets["accessToken"], Provider: TypeOneDrive,
			})
			if err != nil {
				return "", "", nil, nil, err
			}
			base := firstNonEmpty(strCfg(cfg, "baseUrl"), "https://graph.microsoft.com")
			return http.MethodGet, strings.TrimRight(base, "/") + "/v1.0/me/drive", map[string]string{
				"Authorization": "Bearer " + tok,
			}, nil, nil
		}),
	}
}

// UploadMeta describes a file to upload.
type UploadMeta struct {
	Name, ContentType, FolderID, FolderPath, DriveID string
}

// GDriveUpload creates a file in Google Drive (multipart).
func GDriveUpload(ctx context.Context, client *http.Client, baseURL, accessToken string, meta UploadMeta, data []byte) (string, error) {
	if accessToken == "" {
		return "", fmt.Errorf("gdrive: accessToken required")
	}
	base := firstNonEmpty(baseURL, "https://www.googleapis.com")
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	metaPart, err := w.CreatePart(map[string][]string{"Content-Type": {"application/json; charset=UTF-8"}})
	if err != nil {
		return "", err
	}
	md := map[string]any{"name": meta.Name}
	if meta.FolderID != "" {
		md["parents"] = []string{meta.FolderID}
	}
	if err := json.NewEncoder(metaPart).Encode(md); err != nil {
		return "", err
	}
	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	filePart, err := w.CreatePart(map[string][]string{"Content-Type": {ct}})
	if err != nil {
		return "", err
	}
	if _, err := filePart.Write(data); err != nil {
		return "", err
	}
	_ = w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(base, "/")+"/upload/drive/v3/files?uploadType=multipart", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("gdrive: HTTP %d: %s", res.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.ID, nil
}

// OneDriveUpload PUTs content to a drive path via Graph.
func OneDriveUpload(ctx context.Context, client *http.Client, baseURL, accessToken string, meta UploadMeta, data []byte) (string, error) {
	if accessToken == "" {
		return "", fmt.Errorf("onedrive: accessToken required")
	}
	base := firstNonEmpty(baseURL, "https://graph.microsoft.com")
	path := strings.Trim(meta.FolderPath, "/")
	if path == "" {
		path = "EmailBridge"
	}
	name := meta.Name
	if name == "" {
		name = "file.bin"
	}
	var endpoint string
	if meta.DriveID != "" {
		endpoint = fmt.Sprintf("%s/v1.0/drives/%s/root:/%s/%s:/content", strings.TrimRight(base, "/"), meta.DriveID, path, name)
	} else {
		endpoint = fmt.Sprintf("%s/v1.0/me/drive/root:/%s/%s:/content", strings.TrimRight(base, "/"), path, name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", ct)
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("onedrive: HTTP %d: %s", res.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.ID, nil
}
