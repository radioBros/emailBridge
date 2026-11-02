package deliverytest

import "strings"

// classifyFolder maps mailbox special-use / path to inbox|spam|other (P5.3).
func classifyFolder(path, specialUse string) string {
	su := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(specialUse), `\`))
	switch su {
	case "inbox":
		return FolderInbox
	case "junk", "spam":
		return FolderSpam
	}
	p := strings.ToLower(strings.TrimSpace(path))
	base := p
	if i := strings.LastIndexAny(p, "/."); i >= 0 {
		base = p[i+1:]
	}
	switch base {
	case "inbox":
		return FolderInbox
	case "junk", "spam", "bulk", "junk e-mail", "junk email":
		return FolderSpam
	}
	if strings.Contains(p, "junk") || strings.Contains(p, "spam") {
		return FolderSpam
	}
	if p == "inbox" || strings.HasSuffix(p, "/inbox") || strings.HasSuffix(p, ".inbox") {
		return FolderInbox
	}
	return FolderOther
}
