package gmail

import "strings"

// SystemLabel describes a Gmail system label mapped to a mailbox.
type SystemLabel struct {
	ID         string
	Path       string
	SpecialUse string
}

// SystemMailboxes is the Gmail system label → mailbox map (P6.2 §3.2).
var SystemMailboxes = []SystemLabel{
	{ID: "INBOX", Path: "INBOX", SpecialUse: `\Inbox`},
	{ID: "SENT", Path: "[Gmail]/Sent Mail", SpecialUse: `\Sent`},
	{ID: "DRAFT", Path: "[Gmail]/Drafts", SpecialUse: `\Drafts`},
	{ID: "TRASH", Path: "[Gmail]/Trash", SpecialUse: `\Trash`},
	{ID: "SPAM", Path: "[Gmail]/Spam", SpecialUse: `\Junk`},
}

var systemByID = func() map[string]SystemLabel {
	m := map[string]SystemLabel{}
	for _, s := range SystemMailboxes {
		m[s.ID] = s
	}
	return m
}()

var systemByPath = func() map[string]SystemLabel {
	m := map[string]SystemLabel{}
	for _, s := range SystemMailboxes {
		m[s.Path] = s
	}
	return m
}()

// PathForLabel returns the mailbox path for a system label id, or "".
func PathForLabel(labelID string) string {
	if s, ok := systemByID[labelID]; ok {
		return s.Path
	}
	return ""
}

// LabelForPath returns the system label id for a mailbox path, or "".
func LabelForPath(path string) string {
	if s, ok := systemByPath[path]; ok {
		return s.ID
	}
	return ""
}

// PrimaryMailbox picks the watched system mailbox for a message's label set.
func PrimaryMailbox(labelIDs, watchedPaths []string) (path, specialUse, labelID string) {
	watched := watchedSet(watchedPaths)
	// Prefer INBOX when present and watched.
	order := []string{"INBOX", "SENT", "DRAFT", "TRASH", "SPAM"}
	set := map[string]bool{}
	for _, id := range labelIDs {
		set[id] = true
	}
	for _, id := range order {
		if !set[id] {
			continue
		}
		s := systemByID[id]
		if watched[s.Path] || watched["*"] {
			return s.Path, s.SpecialUse, id
		}
	}
	return "", "", ""
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

// FlagsFromLabels maps Gmail system labels to IMAP-style flags.
func FlagsFromLabels(labelIDs []string) []string {
	unread := false
	starred := false
	draft := false
	for _, id := range labelIDs {
		switch id {
		case "UNREAD":
			unread = true
		case "STARRED":
			starred = true
		case "DRAFT":
			draft = true
		}
	}
	var flags []string
	if !unread {
		flags = append(flags, `\Seen`)
	}
	if starred {
		flags = append(flags, `\Flagged`)
	}
	if draft {
		flags = append(flags, `\Draft`)
	}
	if flags == nil {
		flags = []string{}
	}
	return flags
}

// isUserLabelID reports whether id is a Gmail user label (not system / category / flag).
func isUserLabelID(id string) bool {
	if _, sys := systemByID[id]; sys {
		return false
	}
	switch id {
	case "UNREAD", "STARRED", "IMPORTANT", "CHAT":
		return false
	}
	return !strings.HasPrefix(id, "CATEGORY_")
}

// UserLabelPath is the mailbox path for a user label (P6.2 follow-up: Label/Name).
func UserLabelPath(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return "Label/" + name
}

// UserLabels returns non-system, non-flag label names/ids suitable for event.labels.
// labelNames maps id → display name; when missing, the id is used.
func UserLabels(labelIDs []string, labelNames map[string]string) []string {
	var out []string
	for _, id := range labelIDs {
		if !isUserLabelID(id) {
			continue
		}
		name := id
		if labelNames != nil {
			if n := labelNames[id]; n != "" {
				name = n
			}
		}
		out = append(out, name)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// FolderPaths lists every watched mailbox path a message belongs to (system + Label/Name).
func FolderPaths(labelIDs []string, labelNames map[string]string, watchedPaths []string) []SystemLabel {
	watched := watchedSet(watchedPaths)
	var out []SystemLabel
	seen := map[string]bool{}
	for _, s := range SystemMailboxes {
		if !hasLabel(labelIDs, s.ID) {
			continue
		}
		if !watched[s.Path] && !watched["*"] {
			continue
		}
		out = append(out, s)
		seen[s.Path] = true
	}
	for _, id := range labelIDs {
		if !isUserLabelID(id) {
			continue
		}
		name := id
		if labelNames != nil && labelNames[id] != "" {
			name = labelNames[id]
		}
		path := UserLabelPath(name)
		if path == "" || seen[path] {
			continue
		}
		if !watched[path] && !watched["*"] {
			continue
		}
		out = append(out, SystemLabel{ID: id, Path: path})
		seen[path] = true
	}
	return out
}

func hasLabel(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
