# Vendored `mailgun/raymond/v2`

| | |
| --- | --- |
| Upstream | `github.com/mailgun/raymond/v2` |
| Version | `v2.0.48` |
| Licence | MIT (see `LICENSE`) |
| Status | Upstream archived; no maintained Handlebars library for Go (PLAN.md decision) |

## Changes from upstream

1. **Import paths** rewritten from `github.com/mailgun/raymond/v2…` to
   `github.com/radiobros/emailbridge/internal/templates/hbs…`. No separate Go module.
2. **`io/ioutil`** → `os` (`ReadFile`) to satisfy modern Go / golangci.
3. **`gopkg.in/yaml.v2`** in `mustache_test.go` → `go.yaml.in/yaml/v3` (already in the
   EmailBridge module; avoids adding a new dependency).
4. **`Template.NoEscape`** — when true, `{{…}}` interpolations are not HTML-escaped
   (used for text parts). `{{{…}}}` and `SafeString` unchanged.
5. **`Template.Strict`** — when true, a missing variable in a mustache interpolation
   errors with `Missing required variable: <path>` instead of rendering empty.
6. `Clone()` copies `NoEscape` and `Strict`.

`logrus` is kept (already an indirect dependency of EmailBridge) for the package logger.
