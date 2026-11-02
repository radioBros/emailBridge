package graph

import "strings"

// WellKnownFolder maps a Graph wellKnownName to a mailbox path and special-use.
type WellKnownFolder struct {
	Known      string
	Path       string
	SpecialUse string
}

// WellKnownMailboxes is the Graph well-known → mailbox map (P6.3 §3.2).
var WellKnownMailboxes = []WellKnownFolder{
	{Known: "inbox", Path: "INBOX", SpecialUse: `\Inbox`},
	{Known: "sentitems", Path: "Sent Items", SpecialUse: `\Sent`},
	{Known: "drafts", Path: "Drafts", SpecialUse: `\Drafts`},
	{Known: "deleteditems", Path: "Deleted Items", SpecialUse: `\Trash`},
	{Known: "junkemail", Path: "Junk Email", SpecialUse: `\Junk`},
	{Known: "archive", Path: "Archive", SpecialUse: `\Archive`},
}

var byKnown = func() map[string]WellKnownFolder {
	m := map[string]WellKnownFolder{}
	for _, w := range WellKnownMailboxes {
		m[w.Known] = w
	}
	return m
}()

var byPath = func() map[string]WellKnownFolder {
	m := map[string]WellKnownFolder{}
	for _, w := range WellKnownMailboxes {
		m[strings.ToLower(w.Path)] = w
	}
	return m
}()

// PathForWellKnown returns the mailbox path for a well-known name, or "".
func PathForWellKnown(known string) string {
	if w, ok := byKnown[strings.ToLower(known)]; ok {
		return w.Path
	}
	return ""
}

// SpecialUseFor returns special-use for a well-known name or path.
func SpecialUseFor(knownOrPath string) string {
	k := strings.ToLower(knownOrPath)
	if w, ok := byKnown[k]; ok {
		return w.SpecialUse
	}
	if w, ok := byPath[k]; ok {
		return w.SpecialUse
	}
	return ""
}

// MailboxPath picks path + specialUse from a Graph folder.
func MailboxPath(wellKnown, displayName string) (path, specialUse string) {
	if wellKnown != "" {
		if w, ok := byKnown[strings.ToLower(wellKnown)]; ok {
			return w.Path, w.SpecialUse
		}
	}
	if displayName == "" {
		return "INBOX", `\Inbox`
	}
	return displayName, SpecialUseFor(displayName)
}

func watchedSet(paths []string) map[string]bool {
	out := map[string]bool{}
	if len(paths) == 0 {
		out["*"] = true
		return out
	}
	for _, p := range paths {
		out[p] = true
		if p == "*" {
			out["*"] = true
		}
	}
	return out
}

func isWatched(path string, watched map[string]bool) bool {
	return watched["*"] || watched[path]
}

// FlagsFromGraph maps Graph isRead / flag / isDraft to IMAP-style flags.
func FlagsFromGraph(isRead, flagged, isDraft bool) []string {
	var flags []string
	if isRead {
		flags = append(flags, `\Seen`)
	}
	if flagged {
		flags = append(flags, `\Flagged`)
	}
	if isDraft {
		flags = append(flags, `\Draft`)
	}
	if flags == nil {
		flags = []string{}
	}
	return flags
}
