package auth

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/thecodearcher/limen"

	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/pkg/limenmongo"
)

var updateSchema = flag.Bool("update", false, "rewrite pkg/limenmongo/schemas/limen-<version>.json from the pinned Limen")

// nopDB satisfies limen.DatabaseAdapter for limen.New, which never touches the database.
type nopDB struct{}

func (nopDB) Create(context.Context, limen.SchemaTableName, map[string]any) (limen.DatabaseResult, error) {
	return limen.DatabaseResult{}, nil
}
func (nopDB) FindOne(context.Context, limen.SchemaTableName, []limen.Where, []limen.OrderBy) (map[string]any, error) {
	return nil, limen.ErrRecordNotFound
}
func (nopDB) FindMany(context.Context, limen.SchemaTableName, []limen.Where, *limen.QueryOptions) ([]map[string]any, error) {
	return nil, nil
}
func (nopDB) Update(context.Context, limen.SchemaTableName, []limen.Where, map[string]any) (limen.DatabaseResult, error) {
	return limen.DatabaseResult{}, nil
}
func (nopDB) Delete(context.Context, limen.SchemaTableName, []limen.Where) (limen.DatabaseResult, error) {
	return limen.DatabaseResult{}, nil
}
func (nopDB) Exists(context.Context, limen.SchemaTableName, []limen.Where) (bool, error) {
	return false, nil
}
func (nopDB) Count(context.Context, limen.SchemaTableName, []limen.Where) (int64, error) {
	return 0, nil
}

func schemaTestOptions() Options {
	return Options{
		Auth:      config.AuthConfig{Secret: "0123456789abcdef0123456789abcdef", SessionTTL: 12 * time.Hour, SessionIdle: 2 * time.Hour, MFARequired: true},
		PublicURL: "https://mail.example.com",
		EmailOTP:  true,
	}
}

// generateSchema runs limen.New with the production configuration and the CLI option, which writes the
// discovered schema to ./.limen/schemas.json, in a temporary working directory.
func generateSchema(t *testing.T) []byte {
	t.Helper()
	t.Chdir(t.TempDir())
	o := schemaTestOptions()
	cfg := limenConfig(o, nopDB{}, limen.NewMemoryCacheStore(), []byte(o.Auth.Secret), callbacks{
		otp:   func(context.Context, string, string) {},
		reset: func(string, string) {},
	})
	cfg.CLI = &limen.CLIConfig{Enabled: true}
	if _, err := limen.New(cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(".limen", "schemas.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestLimenSchemaFileUpToDate fails when the embedded schema file no longer matches what the pinned Limen
// produces for EmailBridge's plugin set (a Limen upgrade or a plugin change): regenerate it with
// `go test ./internal/auth -run TestLimenSchemaFileUpToDate -update`.
func TestLimenSchemaFileUpToDate(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(wd, "..", "..", "pkg", "limenmongo", "schemas", "limen-"+limenmongo.LimenVersion+".json")
	got := generateSchema(t)
	if *updateSchema {
		if err := os.WriteFile(target, got, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", target)
		return
	}
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(limenmongo.EmbeddedSchemaJSON(), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("embedded Limen schema file is out of date; run: go test ./internal/auth -run TestLimenSchemaFileUpToDate -update")
	}
	if _, err := limenmongo.ParseSchema(got); err != nil {
		t.Fatal(err)
	}
}
