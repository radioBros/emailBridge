package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	TypeHubSpot   = "hubspot"
	TypePipedrive = "pipedrive"
)

const defaultCRMFilter = `type in ["message.received", "message.sent"]`

// HubSpot returns the HubSpot CRM connector.
func HubSpot() Def {
	return Def{
		ID: TypeHubSpot, Version: "1.1.0", Name: "HubSpot", Category: CategoryCRM,
		Icon: "mdi-salesforce", Color: "#FF7A59",
		Description: "Log received/sent mail as HubSpot email engagements on the contact matched by address. Addresses and subjects leave EmailBridge.",
		DocsPath:    "/docs/integrations/hubspot",
		ConfigSchema: map[string]any{
			"type":     "object",
			"required": []any{"accessToken"},
			"properties": map[string]any{
				"accessToken": map[string]any{"type": "string", "writeOnly": true, "title": "Private app access token"},
				"portalId":    map[string]any{"type": "string", "title": "Portal ID"},
				"filter":      map[string]any{"type": "string", "title": "Event filter CEL", "default": defaultCRMFilter},
				"baseUrl":     map[string]any{"type": "string", "format": "uri", "title": "API base URL override"},
			},
		},
		SecretFields: []string{"accessToken"},
		Capabilities: []string{CapTest},
		Settings:     map[string]string{"integrations.hubspot.portalId": "{{config.portalId}}"},
		Endpoints: []EndpointTmpl{{
			Name: "HubSpot CRM", Kind: KindIntegration,
			SinkConfig: map[string]string{"instanceId": "{{instance.id}}"}, PayloadText: true,
		}},
		Routes: []RouteTmpl{{
			Name: "Log mail", EndpointIndex: 0, Filter: "{{config.filter}}",
		}},
		TestFunc: HTTPTester("HubSpot", func(cfg map[string]any, secrets map[string]string) (string, string, map[string]string, []byte, error) {
			tok := secrets["accessToken"]
			if tok == "" {
				return "", "", nil, nil, fmt.Errorf("accessToken required")
			}
			base := firstNonEmpty(strCfg(cfg, "baseUrl"), "https://api.hubapi.com")
			return http.MethodGet, strings.TrimRight(base, "/") + "/crm/v3/objects/contacts?limit=1", map[string]string{
				"Authorization": "Bearer " + tok,
			}, nil, nil
		}),
	}
}

// Pipedrive returns the Pipedrive CRM connector.
func Pipedrive() Def {
	return Def{
		ID: TypePipedrive, Version: "1.1.0", Name: "Pipedrive", Category: CategoryCRM,
		Icon: "mdi-handshake", Color: "#017737",
		Description: "Log received/sent mail as Pipedrive notes on the person matched by address. Addresses and subjects leave EmailBridge.",
		DocsPath:    "/docs/integrations/pipedrive",
		ConfigSchema: map[string]any{
			"type":     "object",
			"required": []any{"apiToken", "companyDomain"},
			"properties": map[string]any{
				"apiToken":      map[string]any{"type": "string", "writeOnly": true, "title": "API token"},
				"companyDomain": map[string]any{"type": "string", "title": "Company domain"},
				"filter":        map[string]any{"type": "string", "title": "Event filter CEL", "default": defaultCRMFilter},
				"baseUrl":       map[string]any{"type": "string", "format": "uri", "title": "API base URL override"},
			},
		},
		SecretFields: []string{"apiToken"},
		Capabilities: []string{CapTest},
		Settings:     map[string]string{"integrations.pipedrive.companyDomain": "{{config.companyDomain}}"},
		Endpoints: []EndpointTmpl{{
			Name: "Pipedrive CRM", Kind: KindIntegration,
			SinkConfig: map[string]string{"instanceId": "{{instance.id}}"}, PayloadText: true,
		}},
		Routes: []RouteTmpl{{
			Name: "Log mail", EndpointIndex: 0, Filter: "{{config.filter}}",
		}},
		TestFunc: HTTPTester("Pipedrive", func(cfg map[string]any, secrets map[string]string) (string, string, map[string]string, []byte, error) {
			tok, dom := secrets["apiToken"], strCfg(cfg, "companyDomain")
			if tok == "" || dom == "" {
				return "", "", nil, nil, fmt.Errorf("apiToken and companyDomain required")
			}
			base := firstNonEmpty(strCfg(cfg, "baseUrl"), fmt.Sprintf("https://%s.pipedrive.com", dom))
			u := strings.TrimRight(base, "/") + "/api/v1/users/me?api_token=" + url.QueryEscape(tok)
			return http.MethodGet, u, nil, nil, nil
		}),
	}
}

// MailEngagement is the CRM log payload.
type MailEngagement struct {
	Address   string
	Subject   string
	Body      string
	Direction string
}

// HubSpotLogMail finds a contact by email and creates an email engagement.
func HubSpotLogMail(ctx context.Context, client *http.Client, baseURL, token string, eng MailEngagement) (string, error) {
	if token == "" || eng.Address == "" {
		return "", fmt.Errorf("hubspot: token and address required")
	}
	base := firstNonEmpty(baseURL, "https://api.hubapi.com")
	if client == nil {
		client = http.DefaultClient
	}
	searchBody, _ := json.Marshal(map[string]any{
		"filterGroups": []map[string]any{{
			"filters": []map[string]any{{"propertyName": "email", "operator": "EQ", "value": eng.Address}},
		}},
		"limit": 1,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/crm/v3/objects/contacts/search", bytes.NewReader(searchBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	_ = res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("hubspot search: HTTP %d", res.StatusCode)
	}
	var found struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	_ = json.Unmarshal(raw, &found)
	if len(found.Results) == 0 {
		return "", fmt.Errorf("hubspot: contact not found")
	}
	engBody, _ := json.Marshal(map[string]any{
		"properties": map[string]any{
			"hs_timestamp": "0", "hs_email_subject": eng.Subject, "hs_email_text": eng.Body,
		},
		"associations": []map[string]any{{
			"to":    map[string]string{"id": found.Results[0].ID},
			"types": []map[string]string{{"associationCategory": "HUBSPOT_DEFINED", "associationTypeId": "198"}},
		}},
	})
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/crm/v3/objects/emails", bytes.NewReader(engBody))
	if err != nil {
		return "", err
	}
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	res2, err := client.Do(req2)
	if err != nil {
		return "", err
	}
	raw2, _ := io.ReadAll(io.LimitReader(res2.Body, 1<<20))
	_ = res2.Body.Close()
	if res2.StatusCode < 200 || res2.StatusCode >= 300 {
		return "", fmt.Errorf("hubspot engage: HTTP %d: %s", res2.StatusCode, truncate(string(raw2), 200))
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw2, &created)
	return created.ID, nil
}

// PipedriveLogMail finds a person by email and adds a note.
func PipedriveLogMail(ctx context.Context, client *http.Client, baseURL, apiToken, companyDomain string, eng MailEngagement) (string, error) {
	if apiToken == "" || eng.Address == "" {
		return "", fmt.Errorf("pipedrive: token and address required")
	}
	base := firstNonEmpty(baseURL, fmt.Sprintf("https://%s.pipedrive.com", companyDomain))
	if client == nil {
		client = http.DefaultClient
	}
	q := url.Values{"term": {eng.Address}, "fields": {"email"}, "api_token": {apiToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/v1/persons/search?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	_ = res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("pipedrive search: HTTP %d", res.StatusCode)
	}
	var found struct {
		Data struct {
			Items []struct {
				Item struct {
					ID int `json:"id"`
				} `json:"item"`
			} `json:"items"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &found)
	if len(found.Data.Items) == 0 {
		return "", fmt.Errorf("pipedrive: person not found")
	}
	noteBody, _ := json.Marshal(map[string]any{
		"content":   fmt.Sprintf("%s: %s\n\n%s", eng.Direction, eng.Subject, eng.Body),
		"person_id": found.Data.Items[0].Item.ID,
	})
	u := strings.TrimRight(base, "/") + "/api/v1/notes?api_token=" + url.QueryEscape(apiToken)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(noteBody))
	if err != nil {
		return "", err
	}
	req2.Header.Set("Content-Type", "application/json")
	res2, err := client.Do(req2)
	if err != nil {
		return "", err
	}
	raw2, _ := io.ReadAll(io.LimitReader(res2.Body, 1<<20))
	_ = res2.Body.Close()
	if res2.StatusCode < 200 || res2.StatusCode >= 300 {
		return "", fmt.Errorf("pipedrive note: HTTP %d: %s", res2.StatusCode, truncate(string(raw2), 200))
	}
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw2, &created)
	return fmt.Sprintf("%d", created.Data.ID), nil
}
