package submit

import (
	"bytes"
	"strings"
)

// sandboxRewriteHeader marks a message whose recipients the sandbox replaced (S6 §7).
const sandboxRewriteHeader = "X-EmailBridge-Sandbox"

// headerAddressedRaw prepares the stored raw message for a send API that delivers to the recipients in the
// MIME headers instead of an SMTP envelope (Gmail API, Microsoft Graph). Two things differ from SMTP:
//
//   - Bcc recipients live only in the envelope (never in the stored headers), so they are added as a Bcc
//     header — the provider removes it before delivery.
//   - A sandbox rewrite changes only RCPT TO for SMTP; here the To/Cc/Bcc headers themselves must be replaced,
//     or the real recipients would receive the message.
func headerAddressedRaw(raw []byte, bcc, rewriteTo []string) []byte {
	if len(rewriteTo) == 0 && len(bcc) == 0 {
		return raw
	}
	head, body, nl := splitHeader(raw)
	var out bytes.Buffer
	out.Grow(len(raw) + 128)
	if len(rewriteTo) > 0 {
		out.WriteString("To: " + strings.Join(rewriteTo, ", ") + nl)
		out.WriteString(sandboxRewriteHeader + ": recipients rewritten" + nl)
		out.Write(dropHeaders(head, "to", "cc", "bcc"))
	} else {
		out.WriteString("Bcc: " + strings.Join(bcc, ", ") + nl)
		out.Write(dropHeaders(head, "bcc"))
	}
	out.Write(body)
	return out.Bytes()
}

// splitHeader returns the header block (with its trailing line break), the rest (blank line + body) and the
// line break the message uses.
func splitHeader(raw []byte) (head, rest []byte, nl string) {
	nl = "\r\n"
	i := bytes.Index(raw, []byte("\r\n\r\n"))
	if j := bytes.Index(raw, []byte("\n\n")); j >= 0 && (i < 0 || j < i) {
		i, nl = j, "\n"
	}
	if i < 0 {
		return raw, nil, nl
	}
	cut := i + len(nl)
	return raw[:cut], raw[cut:], nl
}

// dropHeaders removes the named header fields (case-insensitive), including their folded continuation lines.
func dropHeaders(head []byte, names ...string) []byte {
	var out bytes.Buffer
	skip := false
	for _, line := range bytes.SplitAfter(head, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' { // continuation of the previous field
			if !skip {
				out.Write(line)
			}
			continue
		}
		skip = false
		if name, _, ok := bytes.Cut(line, []byte(":")); ok {
			n := strings.ToLower(strings.TrimSpace(string(name)))
			for _, d := range names {
				if n == d {
					skip = true
					break
				}
			}
		}
		if !skip {
			out.Write(line)
		}
	}
	return out.Bytes()
}
