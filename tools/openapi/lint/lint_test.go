package lint

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Environment knobs:
//
//	EB_OPENAPI_SPEC=<file>          document to lint (default api/openapi/openapi.yaml)
//	EB_EVENTS_DIR=<dir>             event schemas (default api/events)
//	EB_OPENAPI_LINT_TAGS=a,b        only operations whose first tag is listed (strict: lint.allow ignored)
//	EB_OPENAPI_LINT_PATHS=/v1/me,…  only operations whose path starts with a prefix (strict)
//	EB_OPENAPI_LINT_WRITE_ALLOW=1   rewrite lint.allow with the operations that fail today, then pass
//
// Without a filter, operations listed in lint.allow are expected to fail (a ratchet: the test fails when an
// allow-listed operation became clean, so the list only shrinks). Subtests are <tag>/<operationId>, so
// `go test ./tools/openapi/lint/ -run 'TestOperations/members/'` works too.

const allowFile = "lint.allow"

func repoPath(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return filepath.Join("..", "..", "..", def)
}

func csvEnv(name string) []string {
	var out []string
	for _, p := range strings.Split(os.Getenv(name), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func readAllow(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	f, err := os.Open(allowFile)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line != "" {
			out[line] = true
		}
	}
	return out
}

func selected(op Operation, tags, paths []string) bool {
	if len(tags) == 0 && len(paths) == 0 {
		return true
	}
	for _, t := range tags {
		if strings.EqualFold(t, op.Tag) {
			return true
		}
	}
	for _, p := range paths {
		if strings.HasPrefix(op.Path, p) {
			return true
		}
	}
	return false
}

func TestOperations(t *testing.T) {
	spec := repoPath("EB_OPENAPI_SPEC", "api/openapi/openapi.yaml")
	doc, err := LoadFile(spec)
	if err != nil {
		t.Fatalf("load %s: %v", spec, err)
	}
	ops := doc.Operations()
	tags, paths := csvEnv("EB_OPENAPI_LINT_TAGS"), csvEnv("EB_OPENAPI_LINT_PATHS")
	strict := len(tags) > 0 || len(paths) > 0

	if os.Getenv("EB_OPENAPI_LINT_WRITE_ALLOW") == "1" {
		var failing []string
		for _, op := range ops {
			if len(op.Findings) > 0 {
				failing = append(failing, op.ID)
			}
		}
		sort.Strings(failing)
		head := "# Operations whose documentation does not meet the W70 standard yet (ratchet: only ever remove lines).\n" +
			"# Regenerate with EB_OPENAPI_LINT_WRITE_ALLOW=1 go test ./tools/openapi/lint/ -run TestOperations\n"
		if err := os.WriteFile(allowFile, []byte(head+strings.Join(failing, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d operations to %s", len(failing), allowFile)
		return
	}

	allow := map[string]bool{}
	if !strict {
		allow = readAllow(t)
	}
	seen := map[string]bool{}
	count := 0
	for _, op := range ops {
		seen[op.ID] = true
		if !selected(op, tags, paths) {
			continue
		}
		count++
		tag := op.Tag
		if tag == "" {
			tag = "untagged"
		}
		t.Run(tag+"/"+op.ID, func(t *testing.T) {
			if allow[op.ID] {
				if len(op.Findings) == 0 {
					t.Errorf("%s %s is clean now: remove %s from %s", op.Method, op.Path, op.ID, allowFile)
				}
				return
			}
			for _, f := range op.Findings {
				t.Errorf("%s %s: %s", op.Method, op.Path, f)
			}
		})
	}
	if strict && count == 0 {
		t.Fatalf("no operation matches EB_OPENAPI_LINT_TAGS=%v EB_OPENAPI_LINT_PATHS=%v", tags, paths)
	}
	for id := range allow {
		if !seen[id] {
			t.Errorf("%s lists %s, which is not in the document", allowFile, id)
		}
	}
}

func TestEvents(t *testing.T) {
	dir := repoPath("EB_EVENTS_DIR", "api/events")
	schemas, err := LoadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) == 0 {
		t.Fatalf("no event schemas in %s", dir)
	}
	withExamples := 0
	for _, s := range schemas {
		if s.Examples > 0 {
			withExamples++
		}
	}
	if withExamples == 0 {
		t.Skip("TODO(W70-d3): no event schema carries `examples` yet — run `go run ./tools/eventschema` once the event types have examples; this check then becomes mandatory")
	}
	LintEvents(schemas)
	for _, s := range schemas {
		t.Run(s.File, func(t *testing.T) {
			for _, f := range s.Findings {
				t.Errorf("%s: %s", s.File, f)
			}
		})
	}
}
