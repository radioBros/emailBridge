package logging

import "testing"

func TestSensitiveKeyExported(t *testing.T) {
	for key, want := range map[string]bool{"token": true, "clientSecret": true, "api_key": true, "password": true,
		"secretPreview": false, "name": false, "code": false} {
		if SensitiveKey(key) != want {
			t.Errorf("SensitiveKey(%q) != %v", key, want)
		}
	}
	if Redacted != "[redacted]" {
		t.Fatal(Redacted)
	}
}
