package msgauth

// Source identifies how auth results were obtained.
type Source string

const (
	SourceHeader Source = "header"
	SourceVerify Source = "verify"
)

// Item is one method result in authResults (S3 §3.5): { result, domain, selector? }.
type Item struct {
	Result   string `json:"result" doc:"Verdict: pass, fail, softfail, neutral, none, temperror or permerror" example:"pass"`
	Domain   string `json:"domain,omitempty" doc:"Domain that was checked" example:"example.com"`
	Selector string `json:"selector,omitempty" doc:"DKIM selector (DKIM only)" example:"s2026"`
}

// Results is the authResults object on message.received (S3 §3.5).
type Results struct {
	SPF    Item   `json:"spf" doc:"SPF verdict for the envelope sender"`
	DKIM   []Item `json:"dkim" doc:"One verdict per DKIM signature"`
	DMARC  Item   `json:"dmarc" doc:"DMARC verdict for the From domain"`
	ARC    Item   `json:"arc" doc:"ARC chain verdict"`
	Source Source `json:"source" doc:"header: taken from the receiving server's Authentication-Results; verify: computed by EmailBridge" example:"header"`
}

// emptyResults returns a Results with none values and the given source.
func emptyResults(src Source) *Results {
	none := Item{Result: "none"}
	return &Results{
		SPF:    none,
		DKIM:   []Item{},
		DMARC:  none,
		ARC:    none,
		Source: src,
	}
}

// normalizeResult lowercases and maps known Authentication-Results values to the S3 set.
func normalizeResult(v string) string {
	switch v {
	case "pass", "fail", "none", "neutral", "softfail", "temperror", "permerror":
		return v
	case "hardfail":
		return "fail"
	case "policy":
		return "fail"
	default:
		if v == "" {
			return "none"
		}
		return v
	}
}
