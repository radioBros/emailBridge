package throttle

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
)

// sending.throttle is declared for the global, organisation and account scopes: an account's own switch must
// win over the organisation (the gate used to read the organisation document only).
func TestThrottleSwitchResolvesPerAccount(t *testing.T) {
	reg := features.DefaultRegistry()
	sv := func(b bool) map[string]settings.SwitchValue {
		return map[string]settings.SwitchValue{switchThrottle: {Value: b}}
	}
	cases := []struct {
		name    string
		global  settings.Doc
		org     settings.Doc
		account map[string]any
		want    bool
	}{
		{"default on", settings.Doc{}, settings.Doc{}, nil, true},
		{"organisation off", settings.Doc{}, settings.Doc{Switches: sv(false)}, nil, false},
		{"account off over organisation on", settings.Doc{}, settings.Doc{Switches: sv(true)}, map[string]any{switchThrottle: false}, false},
		{"account on over organisation off", settings.Doc{}, settings.Doc{Switches: sv(false)}, map[string]any{switchThrottle: true}, true},
		{"global off", settings.Doc{Switches: sv(false)}, settings.Doc{}, nil, false},
		{"organisation on over global off", settings.Doc{Switches: sv(false)}, settings.Doc{Switches: sv(true)}, nil, true},
		{"non-bool account value ignored", settings.Doc{}, settings.Doc{Switches: sv(false)}, map[string]any{switchThrottle: "yes"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := configFromDocs(c.global, c.org, c.account, reg, "production").ThrottleOn; got != c.want {
				t.Fatalf("ThrottleOn = %v, want %v", got, c.want)
			}
		})
	}
}
