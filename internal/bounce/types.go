package bounce

// Kind is the top-level classification of an inbound message.
type Kind string

const (
	KindDSNBounce         Kind = "dsn_bounce"
	KindHeuristicBounce   Kind = "heuristic_bounce"
	KindComplaint         Kind = "complaint"
	KindAutoReply         Kind = "auto_reply"
	KindChallengeResponse Kind = "challenge_response"
	KindNone              Kind = "none"
)

// BounceType is hard (permanent) or soft (temporary).
type BounceType string

const (
	BounceHard BounceType = "hard"
	BounceSoft BounceType = "soft"
)

// Recipient is one per-recipient DSN or heuristic result.
type Recipient struct {
	Address    string     `json:"address"`
	Action     string     `json:"action,omitempty"`
	Status     string     `json:"status,omitempty"`
	Diagnostic string     `json:"diagnostic,omitempty"`
	BounceType BounceType `json:"bounceType,omitempty"`
}

// Result is the classification of one inbound message.
type Result struct {
	Kind              Kind        `json:"kind"`
	BounceType        BounceType  `json:"bounceType,omitempty"`
	FeedbackType      string      `json:"feedbackType,omitempty"`
	OriginalMessageID string      `json:"originalMessageId,omitempty"`
	OutboundID        string      `json:"outboundId,omitempty"`
	FinalRecipients   []string    `json:"finalRecipients,omitempty"`
	Recipients        []Recipient `json:"recipients,omitempty"`
	IsAutoReply       bool        `json:"isAutoReply,omitempty"`
	MTA               string      `json:"mta,omitempty"`
	Confidence        float64     `json:"confidence"`
}

// Expected is the corpus label file shape (testdata/corpus/*.json).
type Expected struct {
	Kind       Kind       `json:"kind"`
	BounceType BounceType `json:"bounceType,omitempty"`
}
