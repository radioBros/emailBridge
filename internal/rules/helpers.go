package rules

import (
	"strings"
	"sync"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// evalHeaders holds data.headers for the custom header helpers during one Eval
// (same approach as the SP5 spike).
var evalHeaders sync.Map // key "h" → headerBag

type headerBag struct {
	m map[string][]string
}

func setEvalHeaders(data map[string]any) {
	h := map[string][]string{}
	raw, ok := data["headers"]
	if !ok || raw == nil {
		evalHeaders.Store("h", headerBag{m: h})
		return
	}
	switch v := raw.(type) {
	case map[string][]string:
		for k, vv := range v {
			h[strings.ToLower(k)] = vv
		}
	case map[string]any:
		for k, vv := range v {
			switch s := vv.(type) {
			case []string:
				h[strings.ToLower(k)] = s
			case []any:
				out := make([]string, 0, len(s))
				for _, x := range s {
					if ss, ok := x.(string); ok {
						out = append(out, ss)
					}
				}
				h[strings.ToLower(k)] = out
			}
		}
	}
	evalHeaders.Store("h", headerBag{m: h})
}

func getHeaders() map[string][]string {
	v, ok := evalHeaders.Load("h")
	if !ok {
		return map[string][]string{}
	}
	return v.(headerBag).m
}

func domainFn(v ref.Val) ref.Val {
	addr := addressString(v.Value())
	i := strings.LastIndex(addr, "@")
	if i < 0 || i == len(addr)-1 {
		return types.String("")
	}
	return types.String(strings.ToLower(addr[i+1:]))
}

func addressString(v any) string {
	switch a := v.(type) {
	case string:
		return a
	case map[string]any:
		if s, ok := a["address"].(string); ok {
			return s
		}
	case types.String:
		return string(a)
	}
	return ""
}

func addressesFn(v ref.Val) ref.Val {
	lister, ok := v.(traits.Lister)
	if !ok {
		return types.DefaultTypeAdapter.NativeToValue([]string{})
	}
	n := int(lister.Size().(types.Int))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, addressString(lister.Get(types.Int(i)).Value()))
	}
	return types.DefaultTypeAdapter.NativeToValue(out)
}

type runtimeCostEstimator struct{}

func (runtimeCostEstimator) CallCost(function, overloadID string, args []ref.Val, result ref.Val) *uint64 {
	switch function {
	case "header", "hasHeader", "domain", "isInbox", "from", "fromDomain", "subject",
		"spf", "dkim", "dmarc", "spamScore", "pecType", "isPecReceipt",
		"isReply", "isForward", "isAutoReply", "isBounce", "hasAttachments", "replyTo":
		c := uint64(2)
		return &c
	case "headerValues", "addresses", "labels", "toAddresses", "ccAddresses",
		"inFolder", "hasLabel", "textContains", "subjectContains", "inList", "inDomains":
		c := uint64(5)
		return &c
	case "text", "textMatches", "subjectMatches", "attachments", "hasAttachment",
		"attachmentTypes", "localTime", "businessHours", "merge", "pick", "omit":
		c := uint64(20)
		return &c
	}
	return nil
}
