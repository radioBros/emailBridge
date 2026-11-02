package imapc

import (
	"strings"
	"testing"
)

func TestXOAuth2Client(t *testing.T) {
	c := NewXOAuth2Client("user@ex.com", "tok123")
	mech, ir, err := c.Start()
	if err != nil || mech != XOAuth2 {
		t.Fatal(mech, err)
	}
	s := string(ir)
	if !strings.Contains(s, "user=user@ex.com") || !strings.Contains(s, "auth=Bearer tok123") {
		t.Fatal(s)
	}
	if _, err := c.Next([]byte(`{"status":"invalid_token"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Next(nil); err == nil {
		t.Fatal("expected error on second Next")
	}
}
