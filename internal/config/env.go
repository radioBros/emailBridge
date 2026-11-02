package config

import (
	"os"
	"strings"
)

// Env abstracts the process environment and file reads so tests can inject fakes.
type Env interface {
	// LookupEnv returns the value of key and whether it was set.
	LookupEnv(key string) (string, bool)
	// Environ returns KEY=VALUE entries (same shape as os.Environ).
	Environ() []string
	// ReadFile reads the named file's contents.
	ReadFile(path string) ([]byte, error)
}

type osEnv struct{}

func (osEnv) LookupEnv(key string) (string, bool) { return os.LookupEnv(key) }
func (osEnv) Environ() []string                   { return os.Environ() }
func (osEnv) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // G304: path comes from operator-controlled <KEY>_FILE
}

// MapEnv is an in-memory Env for tests.
type MapEnv struct {
	// Vars holds environment KEY → value pairs.
	Vars map[string]string
	// Files holds absolute path → file contents for <KEY>_FILE reads.
	Files map[string][]byte
}

// LookupEnv implements Env.
func (m MapEnv) LookupEnv(key string) (string, bool) {
	if m.Vars == nil {
		return "", false
	}
	v, ok := m.Vars[key]
	return v, ok
}

// Environ implements Env.
func (m MapEnv) Environ() []string {
	out := make([]string, 0, len(m.Vars))
	for k, v := range m.Vars {
		out = append(out, k+"="+v)
	}
	return out
}

// ReadFile implements Env.
func (m MapEnv) ReadFile(path string) ([]byte, error) {
	if m.Files == nil {
		return nil, os.ErrNotExist
	}
	b, ok := m.Files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return b, nil
}

// trimFileValue removes at most one trailing newline (\n or \r\n) per S9 §1.
func trimFileValue(b []byte) string {
	s := string(b)
	if strings.HasSuffix(s, "\r\n") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "\n") {
		return s[:len(s)-1]
	}
	return s
}
