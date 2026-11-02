package rules

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

func isInboxFn(_ ...ref.Val) ref.Val {
	return types.Bool(computeIsInbox(getData()))
}

func computeIsInbox(data map[string]any) bool {
	mailbox, _ := data["mailbox"].(map[string]any)
	if mailbox == nil {
		mailbox = map[string]any{}
	}
	path := stringField(mailbox, "path")
	if path == "" {
		path = stringField(data, "path")
	}
	if strings.EqualFold(path, "INBOX") {
		return true
	}
	special := stringField(mailbox, "specialUse")
	if strings.EqualFold(special, `\Inbox`) || strings.EqualFold(special, "inbox") {
		return true
	}
	wk := stringField(mailbox, "wellKnownName")
	if strings.EqualFold(wk, "inbox") {
		return true
	}
	for _, l := range labelsFromData(data) {
		if l == `\Inbox` || strings.EqualFold(l, "INBOX") || l == "INBOX" {
			return true
		}
	}
	return false
}

func inFolderFn(v ref.Val) ref.Val {
	want := strings.TrimSpace(string(v.(types.String)))
	data := getData()
	mailbox, _ := data["mailbox"].(map[string]any)
	if mailbox == nil {
		mailbox = map[string]any{}
	}
	path := stringField(mailbox, "path")
	if path == "" {
		path = stringField(data, "path")
	}
	if strings.EqualFold(path, want) {
		return types.Bool(true)
	}
	special := stringField(mailbox, "specialUse")
	if strings.EqualFold(special, want) || strings.EqualFold(strings.TrimPrefix(special, `\`), strings.TrimPrefix(want, `\`)) {
		return types.Bool(true)
	}
	wk := stringField(mailbox, "wellKnownName")
	if strings.EqualFold(wk, want) {
		return types.Bool(true)
	}
	for _, l := range labelsFromData(data) {
		if l == want || strings.EqualFold(l, want) {
			return types.Bool(true)
		}
	}
	return types.Bool(false)
}

func hasLabelFn(v ref.Val) ref.Val {
	name := string(v.(types.String))
	for _, l := range labelsFromData(getData()) {
		if name != "" && name[0] == '\\' {
			if l == name {
				return types.Bool(true)
			}
			continue
		}
		if strings.EqualFold(l, name) {
			return types.Bool(true)
		}
	}
	return types.Bool(false)
}

func labelsFn(_ ...ref.Val) ref.Val {
	return types.DefaultTypeAdapter.NativeToValue(labelsFromData(getData()))
}

func textFn(_ ...ref.Val) ref.Val {
	return types.String(computeText(getData(), getBag().textMax))
}

func computeText(data map[string]any, max int) string {
	text, _ := data["text"].(map[string]any)
	plain := ""
	htmlBody := ""
	if text != nil {
		plain, _ = text["plain"].(string)
		htmlBody, _ = text["html"].(string)
	}
	if plain == "" {
		if s, ok := data["textPlain"].(string); ok {
			plain = s
		}
	}
	if htmlBody == "" {
		if s, ok := data["textHtml"].(string); ok {
			htmlBody = s
		}
	}
	out := plain
	if out == "" && htmlBody != "" {
		out = stripHTML(htmlBody)
	}
	return capRunes(out, max)
}

func textContainsFn(v ref.Val) ref.Val {
	needle := strings.ToLower(string(v.(types.String)))
	hay := strings.ToLower(computeText(getData(), getBag().textMax))
	return types.Bool(needle != "" && strings.Contains(hay, needle))
}

func textMatchesFn(v ref.Val) ref.Val {
	pat := string(v.(types.String))
	re, err := regexp.Compile(pat)
	if err != nil {
		return types.Bool(false)
	}
	return types.Bool(re.MatchString(computeText(getData(), getBag().textMax)))
}

func subjectFn(_ ...ref.Val) ref.Val {
	return types.String(stringField(getData(), "subject"))
}

func subjectContainsFn(v ref.Val) ref.Val {
	needle := strings.ToLower(string(v.(types.String)))
	return types.Bool(needle != "" && strings.Contains(strings.ToLower(stringField(getData(), "subject")), needle))
}

func subjectMatchesFn(v ref.Val) ref.Val {
	re, err := regexp.Compile(string(v.(types.String)))
	if err != nil {
		return types.Bool(false)
	}
	return types.Bool(re.MatchString(stringField(getData(), "subject")))
}

func hasAttachmentsFn(_ ...ref.Val) ref.Val {
	return types.Bool(len(attachmentMaps(getData())) > 0)
}

func attachmentsFn(_ ...ref.Val) ref.Val {
	return types.DefaultTypeAdapter.NativeToValue(attachmentMaps(getData()))
}

func attachmentMaps(data map[string]any) []map[string]any {
	raw, ok := data["attachments"]
	if !ok || raw == nil {
		return nil
	}
	var list []any
	switch v := raw.(type) {
	case []any:
		list = v
	case []map[string]any:
		out := make([]map[string]any, 0, len(v))
		for _, m := range v {
			out = append(out, normalizeAtt(m))
		}
		return out
	default:
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out = append(out, normalizeAtt(m))
		}
	}
	return out
}

func normalizeAtt(m map[string]any) map[string]any {
	size := int64(0)
	switch s := m["size"].(type) {
	case int64:
		size = s
	case int:
		size = int64(s)
	case float64:
		size = int64(s)
	}
	inline, _ := m["inline"].(bool)
	return map[string]any{
		"filename":    stringField(m, "filename"),
		"contentType": strings.ToLower(stringField(m, "contentType")),
		"size":        size,
		"contentId":   stringField(m, "contentId"),
		"inline":      inline,
	}
}

func hasAttachmentFn(v ref.Val) ref.Val {
	pat := string(v.(types.String))
	for _, a := range attachmentMaps(getData()) {
		fn, _ := a["filename"].(string)
		if matchGlobOrRe2(pat, fn) {
			return types.Bool(true)
		}
	}
	return types.Bool(false)
}

func attachmentTypesFn(_ ...ref.Val) ref.Val {
	seen := map[string]struct{}{}
	var out []string
	for _, a := range attachmentMaps(getData()) {
		ct, _ := a["contentType"].(string)
		if ct == "" {
			continue
		}
		if _, ok := seen[ct]; ok {
			continue
		}
		seen[ct] = struct{}{}
		out = append(out, ct)
	}
	return types.DefaultTypeAdapter.NativeToValue(out)
}

func fromFn(_ ...ref.Val) ref.Val {
	return types.String(firstAddress(getData()["from"]))
}

func fromDomainFn(_ ...ref.Val) ref.Val {
	addr := firstAddress(getData()["from"])
	i := strings.LastIndex(addr, "@")
	if i < 0 || i == len(addr)-1 {
		return types.String("")
	}
	return types.String(strings.ToLower(addr[i+1:]))
}

func toAddressesFn(_ ...ref.Val) ref.Val {
	return types.DefaultTypeAdapter.NativeToValue(addressList(getData()["to"]))
}

func ccAddressesFn(_ ...ref.Val) ref.Val {
	return types.DefaultTypeAdapter.NativeToValue(addressList(getData()["cc"]))
}

func inDomainsFn(v ref.Val) ref.Val {
	want := map[string]struct{}{}
	// A CEL list literal is a traits.Lister of ref.Val (not a Go []string): iterate it.
	if lister, ok := v.(traits.Lister); ok {
		it := lister.Iterator()
		for it.HasNext() == types.True {
			if d, ok := it.Next().(types.String); ok {
				want[strings.ToLower(strings.TrimSpace(string(d)))] = struct{}{}
			}
		}
	}
	check := func(addr string) bool {
		i := strings.LastIndex(addr, "@")
		if i < 0 {
			return false
		}
		_, ok := want[strings.ToLower(addr[i+1:])]
		return ok
	}
	data := getData()
	if check(firstAddress(data["from"])) {
		return types.Bool(true)
	}
	for _, a := range addressList(data["to"]) {
		if check(a) {
			return types.Bool(true)
		}
	}
	for _, a := range addressList(data["cc"]) {
		if check(a) {
			return types.Bool(true)
		}
	}
	return types.Bool(false)
}

func replyToFn(_ ...ref.Val) ref.Val {
	data := getData()
	if s := firstAddress(data["replyTo"]); s != "" {
		return types.String(s)
	}
	vals := getBag().headers["reply-to"]
	if len(vals) > 0 {
		return types.String(vals[0])
	}
	return types.String("")
}

var reReplySubject = regexp.MustCompile(`(?i)^(re|fw|fwd)\s*:`)

func isReplyFn(_ ...ref.Val) ref.Val {
	h := getBag().headers
	if len(h["in-reply-to"]) > 0 || len(h["references"]) > 0 {
		return types.Bool(true)
	}
	return types.Bool(reReplySubject.MatchString(stringField(getData(), "subject")))
}

func isForwardFn(_ ...ref.Val) ref.Val {
	subj := stringField(getData(), "subject")
	if regexp.MustCompile(`(?i)^(fwd|fw)\s*:`).MatchString(subj) {
		return types.Bool(true)
	}
	if b, ok := getData()["forwarded"].(bool); ok && b {
		return types.Bool(true)
	}
	return types.Bool(false)
}

func isAutoReplyFn(_ ...ref.Val) ref.Val {
	if b, ok := getData()["isAutoReply"].(bool); ok && b {
		return types.Bool(true) // classified by sync (RFC 3834, inbound.bounceDetection)
	}
	h := getBag().headers
	if v := firstHeader(h, "auto-submitted"); v != "" && !strings.EqualFold(v, "no") {
		return types.Bool(true)
	}
	if firstHeader(h, "x-autoreply") != "" || firstHeader(h, "x-autorespond") != "" {
		return types.Bool(true)
	}
	prec := strings.ToLower(firstHeader(h, "precedence"))
	switch prec {
	case "bulk", "auto_reply", "junk", "list":
		return types.Bool(true)
	}
	return types.Bool(false)
}

func isBounceFn(_ ...ref.Val) ref.Val {
	bag := getBag()
	if bag.eventType == "message.bounced" {
		return types.Bool(true)
	}
	data := bag.data
	switch v := data["bounce"].(type) { // message.received.bounce (P5.4) when bounce detection is on
	case map[string]any:
		if kind, _ := v["kind"].(string); kind != "" && kind != "complaint" && kind != "challenge_response" {
			return types.Bool(true)
		}
	case bool:
		if v {
			return types.Bool(true)
		}
	}
	h := bag.headers
	if firstHeader(h, "x-failed-recipients") != "" {
		return types.Bool(true)
	}
	ct := strings.ToLower(firstHeader(h, "content-type"))
	if strings.Contains(ct, "multipart/report") && strings.Contains(ct, "delivery-status") {
		return types.Bool(true)
	}
	from := strings.ToLower(firstAddress(data["from"]))
	return types.Bool(strings.HasPrefix(from, "mailer-daemon@") || strings.HasPrefix(from, "postmaster@"))
}

func firstHeader(h map[string][]string, name string) string {
	vals := h[name]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func spfFn(_ ...ref.Val) ref.Val {
	return types.String(authResult("spf"))
}
func dkimFn(_ ...ref.Val) ref.Val {
	return types.String(authResult("dkim"))
}
func dmarcFn(_ ...ref.Val) ref.Val {
	return types.String(authResult("dmarc"))
}

// authResult returns the result word of message.received.authResults.<key> ("pass", "fail", …; "" when
// absent). spf/dmarc are objects {result, domain}; dkim is a list — "pass" when any signature passed, else
// the first result. A plain string value (older events, hand-written test events) is returned as is.
func authResult(key string) string {
	ar, _ := getData()["authResults"].(map[string]any)
	if ar == nil {
		return ""
	}
	switch v := ar[key].(type) {
	case string:
		return v
	case map[string]any:
		s, _ := v["result"].(string)
		return s
	case []any:
		first := ""
		for i, x := range v {
			m, _ := x.(map[string]any)
			r, _ := m["result"].(string)
			if strings.EqualFold(r, "pass") {
				return r
			}
			if i == 0 {
				first = r
			}
		}
		return first
	}
	return ""
}

func spamScoreFn(_ ...ref.Val) ref.Val {
	data := getData()
	if spam, ok := data["spam"].(map[string]any); ok { // message.received.spam (Rspamd, P8.3)
		if f, ok := spam["score"].(float64); ok {
			return types.Double(f)
		}
	}
	switch v := data["spamScore"].(type) {
	case float64:
		return types.Double(v)
	case int:
		return types.Double(float64(v))
	case int64:
		return types.Double(float64(v))
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return types.Double(f)
		}
	}
	return types.Double(0)
}

func pecTypeFn(_ ...ref.Val) ref.Val {
	data := getData()
	if s := stringField(data, "pecType"); s != "" {
		return types.String(s)
	}
	if pec, ok := data["pec"].(map[string]any); ok {
		return types.String(stringField(pec, "type"))
	}
	return types.String("")
}

func isPecReceiptFn(_ ...ref.Val) ref.Val {
	if pec, ok := getData()["pec"].(map[string]any); ok && stringField(pec, "kind") == "receipt" {
		return types.Bool(true)
	}
	t := strings.ToLower(string(pecTypeFn().(types.String)))
	switch t {
	case "accettazione", "avvenuta-consegna", "presa-in-carico", "errore-consegna",
		"acceptance", "delivery", "delivery-receipt", "receipt":
		return types.Bool(true)
	}
	h := getBag().headers
	return types.Bool(firstHeader(h, "x-ricevuta") != "")
}

func localTimeFn(v ref.Val) ref.Val {
	tz := string(v.(types.String))
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	t := getBag().eventTime.In(loc)
	return types.Timestamp{Time: t}
}

func hourOfFn(v ref.Val) ref.Val {
	t, ok := v.(types.Timestamp)
	if !ok {
		return types.Int(0)
	}
	return types.Int(t.Hour())
}

func weekdayOfFn(v ref.Val) ref.Val {
	t, ok := v.(types.Timestamp)
	if !ok {
		return types.Int(0)
	}
	return types.Int(int(t.Weekday())) // 0=Sunday
}

func businessHoursFn(args ...ref.Val) ref.Val {
	if len(args) != 3 {
		return types.Bool(false)
	}
	tz := string(args[0].(types.String))
	window := string(args[1].(types.String))
	days := string(args[2].(types.String))
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return types.Bool(false)
	}
	t := getBag().eventTime.In(loc)
	if !weekdayIn(days, t.Weekday()) {
		return types.Bool(false)
	}
	startM, endM, ok := parseHourWindow(window)
	if !ok {
		return types.Bool(false)
	}
	mins := t.Hour()*60 + t.Minute()
	return types.Bool(mins >= startM && mins < endM)
}

func weekdayIn(spec string, d time.Weekday) bool {
	spec = strings.TrimSpace(spec)
	names := map[string]time.Weekday{
		"sun": time.Sunday, "sunday": time.Sunday,
		"mon": time.Monday, "monday": time.Monday,
		"tue": time.Tuesday, "tuesday": time.Tuesday,
		"wed": time.Wednesday, "wednesday": time.Wednesday,
		"thu": time.Thursday, "thursday": time.Thursday,
		"fri": time.Friday, "friday": time.Friday,
		"sat": time.Saturday, "saturday": time.Saturday,
	}
	if strings.Contains(spec, "-") {
		parts := strings.SplitN(spec, "-", 2)
		if len(parts) != 2 {
			return false
		}
		a, ok1 := names[strings.ToLower(strings.TrimSpace(parts[0]))]
		b, ok2 := names[strings.ToLower(strings.TrimSpace(parts[1]))]
		if !ok1 || !ok2 {
			return false
		}
		for x := a; ; x = (x + 1) % 7 {
			if x == d {
				return true
			}
			if x == b {
				break
			}
		}
		return false
	}
	for _, p := range strings.Split(spec, ",") {
		if wd, ok := names[strings.ToLower(strings.TrimSpace(p))]; ok && wd == d {
			return true
		}
	}
	return false
}

func parseHourWindow(s string) (start, end int, ok bool) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	parse := func(p string) (int, bool) {
		p = strings.TrimSpace(p)
		hhmm := strings.Split(p, ":")
		if len(hhmm) != 2 {
			return 0, false
		}
		h, err1 := strconv.Atoi(hhmm[0])
		m, err2 := strconv.Atoi(hhmm[1])
		if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return 0, false
		}
		return h*60 + m, true
	}
	start, ok1 := parse(parts[0])
	end, ok2 := parse(parts[1])
	return start, end, ok1 && ok2
}

func inListFn(nameVal, valueVal ref.Val) ref.Val {
	name := strings.ToLower(string(nameVal.(types.String)))
	val := strings.ToLower(strings.TrimSpace(string(valueVal.(types.String))))
	set := getBag().lists[name]
	if set == nil {
		// also try original case key
		set = getBag().lists[string(nameVal.(types.String))]
	}
	_, ok := set[val]
	return types.Bool(ok)
}
