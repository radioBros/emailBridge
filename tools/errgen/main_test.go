package main

import (
	"bytes"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestGeneratorMatchesCommitted(t *testing.T) {
	root := findTestRoot(t)
	yamlPath := filepath.Join(root, "internal", "errs", "codes.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc fileDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	goSrc, err := format.Source(genGo(doc.Codes))
	if err != nil {
		t.Fatal(err)
	}
	mdSrc := genMD(doc.Codes)

	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "codes_gen.go"), goSrc, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "problems.md"), mdSrc, 0o600); err != nil {
		t.Fatal(err)
	}

	wantGo, err := os.ReadFile(filepath.Join(root, "internal", "errs", "codes_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	wantMD, err := os.ReadFile(filepath.Join(root, "docs", "api", "problems.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(goSrc, wantGo) {
		t.Fatal("codes_gen.go is stale; run go run ./tools/errgen")
	}
	if !bytes.Equal(mdSrc, wantMD) {
		t.Fatal("problems.md is stale; run go run ./tools/errgen")
	}

	cmd := exec.Command("go", "run", "./tools/errgen", "-check")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("errgen -check: %v\n%s", err, out)
	}
}

func findTestRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
