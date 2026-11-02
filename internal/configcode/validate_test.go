package configcode

import (
	"fmt"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/webhook"
)

func TestValidateDocument(t *testing.T) {
	if err := validateDocument(nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := validateDocument(&Document{APIVersion: "other"}); err == nil {
		t.Fatal("apiVersion")
	}
	if err := validateDocument(&Document{Kind: "Other"}); err == nil {
		t.Fatal("kind")
	}
	if err := validateDocument(&Document{Spec: Spec{
		WebhookEndpoints: []EndpointSpec{{Name: ""}},
	}}); err == nil {
		t.Fatal("ep name")
	}
	if err := validateDocument(&Document{Spec: Spec{
		WebhookEndpoints: []EndpointSpec{
			{Name: "a", URL: "https://a"}, {Name: "a", URL: "https://b"},
		},
	}}); err == nil || !strings.Contains(err.Error(), "Duplicate") {
		t.Fatal(err)
	}
	if err := validateDocument(&Document{Spec: Spec{
		WebhookEndpoints: []EndpointSpec{{Name: "a", Secret: "inline"}},
	}}); err == nil {
		t.Fatal("inline secret")
	}
	if err := validateDocument(&Document{Spec: Spec{
		WebhookEndpoints: []EndpointSpec{{
			Name:    "a",
			Headers: []webhook.WebhookHeader{{Name: "H", Value: "plain", Secret: true}},
		}},
	}}); err == nil {
		t.Fatal("header secret")
	}
	if err := validateDocument(&Document{Spec: Spec{
		Routes: []RouteSpec{{Name: "r"}},
	}}); err == nil {
		t.Fatal("route endpoint")
	}
	if err := validateDocument(&Document{Spec: Spec{
		Routes: []RouteSpec{{Name: "r", Endpoint: "e"}, {Name: "r", Endpoint: "e"}},
	}}); err == nil {
		t.Fatal("dup route")
	}
	if err := validateDocument(&Document{Spec: Spec{
		Routes: []RouteSpec{{Name: "r", Endpoint: "e", Secret: "x"}},
	}}); err == nil {
		t.Fatal("route secret")
	}
	if err := validateDocument(&Document{Spec: Spec{
		APIKeys: []APIKeySpec{{Name: ""}},
	}}); err == nil {
		t.Fatal("key name")
	}
	if err := validateDocument(&Document{Spec: Spec{
		APIKeys: []APIKeySpec{{Name: "k"}, {Name: "k"}},
	}}); err == nil {
		t.Fatal("dup key")
	}
	big := make([]EndpointSpec, maxPerKind+1)
	for i := range big {
		big[i].Name = fmt.Sprintf("ep-%d", i)
	}
	if err := validateDocument(&Document{Spec: Spec{WebhookEndpoints: big}}); err == nil {
		t.Fatal("max len")
	}
	ok := &Document{
		APIVersion: APIVersion, Kind: KindOrg,
		Spec: Spec{
			WebhookEndpoints: []EndpointSpec{{Name: " a ", URL: "https://a", Secret: "${secret:S}"}},
			Routes:           []RouteSpec{{Name: " r ", Endpoint: " a ", Secret: "${FOO}"}},
			APIKeys:          []APIKeySpec{{Name: " k "}},
		},
	}
	if err := validateDocument(ok); err != nil {
		t.Fatal(err)
	}
	if ok.Spec.WebhookEndpoints[0].Name != "a" || ok.Spec.Routes[0].Endpoint != "a" {
		t.Fatalf("trim: %+v", ok.Spec)
	}
	if err := validateDocument(&Document{Spec: Spec{Templates: []map[string]any{{"name": "x"}}}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("templates: %v", err)
	}
	if err := validateDocument(&Document{Spec: Spec{SMTPGateways: []map[string]any{{"name": "x"}}}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("gateways: %v", err)
	}
	if err := validateDocument(&Document{Spec: Spec{SuppressionLists: []map[string]any{{"name": "x"}}}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("suppressions: %v", err)
	}
}
