package sinks

import (
	"net/url"
	"regexp"
	"strings"
)

var secretKV = regexp.MustCompile(`(?i)(password|secret|secretaccesskey|pass)\s*[=:]\s*([^\s,;"]+)`)

// safeErr wraps err so Error() never includes credentials or URL userinfo.
func safeErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return &redactedError{op: op, msg: redactSecrets(err.Error())}
}

type redactedError struct {
	op  string
	msg string
}

func (e *redactedError) Error() string {
	if e.op == "" {
		return e.msg
	}
	return e.op + ": " + e.msg
}

func redactSecrets(s string) string {
	out := secretKV.ReplaceAllString(s, "${1}=[redacted]")
	for _, prefix := range []string{"nats://", "tls://", "ws://", "wss://", "http://", "https://"} {
		start := 0
		for {
			i := strings.Index(out[start:], prefix)
			if i < 0 {
				break
			}
			i += start
			rest := out[i:]
			end := len(rest)
			for j, r := range rest {
				if r == ' ' || r == '"' || r == '\'' || r == ')' || r == ',' || r == '\n' || r == '\t' {
					end = j
					break
				}
			}
			candidate := rest[:end]
			if u, err := url.Parse(candidate); err == nil && u.Host != "" {
				if u.User != nil {
					u.User = nil
					out = out[:i] + u.String() + rest[end:]
					start = i + len(u.String())
					continue
				}
			}
			start = i + len(prefix)
		}
	}
	return out
}
