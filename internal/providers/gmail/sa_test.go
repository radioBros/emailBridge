package gmail

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"

	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

func TestServiceAccount_token(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	pkJSON, _ := json.Marshal(string(pemBytes))
	sak, err := ParseServiceAccountKey([]byte(`{
		"client_email": "sa@project.iam.gserviceaccount.com",
		"private_key": ` + string(pkJSON) + `,
		"token_uri": "` + sim.URL() + `/token"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	src := &ServiceAccountSource{
		Key: sak, Subject: "user@ex.com",
		Client: &Client{TokenURL: sim.URL() + "/token", BaseURL: sim.URL()},
	}
	tok, user, err := src.AccessToken(context.Background(), "acc")
	if err != nil || tok == "" || user != "user@ex.com" {
		t.Fatalf("%v %q %q", err, tok, user)
	}
	c := &Client{BaseURL: sim.URL()}
	if _, err := c.GetProfile(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
}

func TestParseServiceAccountKey_invalid(t *testing.T) {
	if _, err := ParseServiceAccountKey([]byte(`{}`)); err == nil {
		t.Fatal("expected error")
	}
}
