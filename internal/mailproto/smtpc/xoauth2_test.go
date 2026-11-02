package smtpc

import (
	"strings"
	"testing"
)

func TestXOAuth2Client(t *testing.T) {
	c := NewXOAuth2Client("u", "t")
	mech, ir, err := c.Start()
	if err != nil || mech != XOAuth2 {
		t.Fatal(mech, err)
	}
	if !strings.Contains(string(ir), "auth=Bearer t") {
		t.Fatal(string(ir))
	}
	_, _ = c.Next(nil)
}
