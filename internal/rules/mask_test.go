package rules

import (
	"strings"
	"testing"
)

func TestMaskSecrets(t *testing.T) {
	mapped := map[string]any{
		"key":  "secret-key",
		"ok":   "public",
		"nest": map[string]any{"k": "secret-key"},
		"arr":  []any{"secret-key", "x"},
	}
	got := MaskSecrets(mapped, []string{"secret-key"}).(map[string]any)
	if got["key"] != secretMask || got["ok"] != "public" {
		t.Fatalf("%v", got)
	}
	nest := got["nest"].(map[string]any)
	if nest["k"] != secretMask {
		t.Fatal(nest)
	}
	arr := got["arr"].([]any)
	if arr[0] != secretMask || arr[1] != "x" {
		t.Fatal(arr)
	}
	if MaskSecrets(nil, nil) != nil {
		t.Fatal("nil")
	}
}

func TestSecretValuesFromEnv(t *testing.T) {
	got := SecretValuesFromEnv(map[string]string{"a": "1", "b": "2"}, []string{"b", "c"})
	if len(got) != 1 || got[0] != "2" {
		t.Fatal(got)
	}
}

func TestMaskString(t *testing.T) {
	if got := MaskString("token=abc end", []string{"abc"}); got != "token="+secretMask+" end" {
		t.Fatal(got)
	}
}

func TestMaskStringStrconvQuotedSecret(t *testing.T) {
	secret := "s3cr3tVALUE99"
	// CEL / strconv often quote the bad value: parsing "<secret>": invalid syntax
	errMsg := `strconv.Atoi: parsing "` + secret + `": invalid syntax`
	got := MaskString(errMsg, []string{secret})
	if strings.Contains(got, secret) {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.Contains(got, secretMask) {
		t.Fatalf("not masked: %q", got)
	}
}
