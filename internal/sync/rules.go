package sync

import (
	"math/rand/v2"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
)

// reportable is the no-replay rule (S5 §5): message.received only for a folder that already had a
// baseline before this pass, a UID at or above the stored uidNext, and an internal date not before the
// account's notifyFrom and not older than the catch-up window.
func reportable(hadBaseline bool, storedUIDNext, uid uint32, internalDate, notifyFrom, now time.Time, catchupMax time.Duration) bool {
	if !hadBaseline || uid < storedUIDNext {
		return false
	}
	if !notifyFrom.IsZero() && internalDate.Before(notifyFrom) {
		return false
	}
	if catchupMax > 0 && internalDate.Before(now.Add(-catchupMax)) {
		return false
	}
	return true
}

// watched filters the server's folders by the account's paths (S5 §4): ["*"] (or empty) = every folder
// except \All and \Flagged virtual folders and non-selectable ones; otherwise exact paths or path.Match
// patterns.
func watched(list []imapc.MailboxInfo, paths []string) []imapc.MailboxInfo {
	all := len(paths) == 0 || slices.Contains(paths, "*")
	var out []imapc.MailboxInfo
	for _, m := range list {
		if noSelect(m) {
			continue
		}
		if all {
			if m.SpecialUse == `\All` || m.SpecialUse == `\Flagged` {
				continue
			}
			out = append(out, m)
			continue
		}
		for _, p := range paths {
			if p == m.Name || strings.EqualFold(p, "INBOX") && strings.EqualFold(m.Name, "INBOX") {
				out = append(out, m)
				break
			}
			if ok, _ := path.Match(p, m.Name); ok {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

func noSelect(m imapc.MailboxInfo) bool {
	for _, a := range m.Attrs {
		if strings.EqualFold(a, `\Noselect`) || strings.EqualFold(a, `\NonExistent`) {
			return true
		}
	}
	return false
}

// idleFolders picks the folders to IDLE on: the account's idlePaths (default INBOX) that exist and are
// watched, at most max (main connection + subconnections).
func idleFolders(w []imapc.MailboxInfo, idlePaths []string, maxConns int) []string {
	if len(idlePaths) == 0 {
		idlePaths = []string{"INBOX"}
	}
	var out []string
	for _, p := range idlePaths {
		for _, m := range w {
			if m.Name == p || strings.EqualFold(p, "INBOX") && strings.EqualFold(m.Name, "INBOX") {
				out = append(out, m.Name)
				break
			}
		}
		if maxConns > 0 && len(out) >= maxConns {
			break
		}
	}
	return out
}

// backoff is full-jitter exponential backoff between min and max for attempt (1-based); errors of kind
// rate_limited wait at least 5 minutes, auth failures at least 5 minutes, TLS/config failures 15 (S5 §2, §7).
func backoff(attempt int, minD, maxD time.Duration, kind errs.Kind, rnd func(int64) int64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	ceiling := minD << min(attempt-1, 20)
	if ceiling <= 0 || ceiling > maxD {
		ceiling = maxD
	}
	d := time.Duration(rnd(int64(ceiling-minD)+1)) + minD
	floor := time.Duration(0)
	switch kind {
	case errs.KindAuth, errs.KindRateLimited:
		floor = 5 * time.Minute
	case errs.KindConfig:
		floor = 15 * time.Minute
	}
	return max(d, floor)
}

func randInt64(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return rand.Int64N(n) //nolint:gosec // G404: jitter, not security
}

// failureState maps a connection error to the account state (S5 §7).
func failureState(err error) string {
	switch errs.KindOf(err) {
	case errs.KindAuth:
		return StateAuthError
	default:
		return StateConnectError
	}
}

// flagsEqual compares flag sets ignoring order and \Recent (a session flag).
func flagsEqual(a, b []string) bool {
	na, nb := normFlags(a), normFlags(b)
	return slices.Equal(na, nb)
}

func normFlags(f []string) []string {
	out := make([]string, 0, len(f))
	for _, x := range f {
		if strings.EqualFold(x, `\Recent`) {
			continue
		}
		out = append(out, x)
	}
	slices.Sort(out)
	return out
}

// flagDiff returns added and removed flags from old to cur.
func flagDiff(old, cur []string) (added, removed []string) {
	o, c := normFlags(old), normFlags(cur)
	for _, f := range c {
		if !slices.Contains(o, f) {
			added = append(added, f)
		}
	}
	for _, f := range o {
		if !slices.Contains(c, f) {
			removed = append(removed, f)
		}
	}
	return orEmpty(added), orEmpty(removed)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
