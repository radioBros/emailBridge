package store

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Prefix is the type prefix of a resource ID (S1 §1.1). The list is closed: add a prefix to S1 first.
type Prefix string

// Resource ID prefixes (S1 §1.1).
const (
	PrefixOrganization        Prefix = "org"
	PrefixUser                Prefix = "usr"
	PrefixMembership          Prefix = "mem"
	PrefixAPIKey              Prefix = "key"
	PrefixAccount             Prefix = "acc"
	PrefixMailbox             Prefix = "mbx"
	PrefixMessage             Prefix = "msg"
	PrefixOutbound            Prefix = "out"
	PrefixEvent               Prefix = "evt"
	PrefixEndpoint            Prefix = "whe"
	PrefixAttachment          Prefix = "att"
	PrefixDraft               Prefix = "drf"
	PrefixExport              Prefix = "exp"
	PrefixPECReceipt          Prefix = "rcp"
	PrefixDelivery            Prefix = "dlv"
	PrefixRoute               Prefix = "rte"
	PrefixRouteVersion        Prefix = "rtv"
	PrefixJob                 Prefix = "job"
	PrefixAudit               Prefix = "aud"
	PrefixCapture             Prefix = "cap"
	PrefixTemplate            Prefix = "tpl"
	PrefixGateway             Prefix = "gwy"
	PrefixSuppression         Prefix = "sup"
	PrefixDeliveryTest        Prefix = "dtt"
	PrefixOAuthApp            Prefix = "oap"
	PrefixOIDC                Prefix = "oidc"
	PrefixConnectLink         Prefix = "lnk"
	PrefixIntegration         Prefix = "int"
	PrefixLog                 Prefix = "log"
	PrefixRequest             Prefix = "req"
	PrefixDataKey             Prefix = "dek"
	PrefixBrandingAsset       Prefix = "bas" // W68 branding uploads
	PrefixRuleList            Prefix = "rls" // W73 named lists
	PrefixEndpointRuleVersion Prefix = "erv" // W73 endpoint rule versions
	PrefixNotification        Prefix = "ntf" // W75 system notifications
)

var knownPrefixes = map[Prefix]bool{
	PrefixOrganization: true, PrefixUser: true, PrefixMembership: true, PrefixAPIKey: true,
	PrefixAccount: true, PrefixMailbox: true, PrefixMessage: true, PrefixOutbound: true, PrefixEvent: true,
	PrefixEndpoint: true, PrefixAttachment: true, PrefixDraft: true, PrefixExport: true, PrefixPECReceipt: true,
	PrefixDelivery: true, PrefixRoute: true, PrefixRouteVersion: true, PrefixJob: true, PrefixAudit: true,
	PrefixCapture: true, PrefixTemplate: true, PrefixGateway: true, PrefixSuppression: true,
	PrefixDeliveryTest: true, PrefixOAuthApp: true, PrefixOIDC: true, PrefixConnectLink: true, PrefixIntegration: true,
	PrefixLog: true, PrefixRequest: true, PrefixDataKey: true, PrefixBrandingAsset: true,
	PrefixRuleList: true, PrefixEndpointRuleVersion: true, PrefixNotification: true,
}

// KnownPrefix reports whether p is in the closed prefix list.
func KnownPrefix(p Prefix) bool { return knownPrefixes[p] }

var (
	entropyMu sync.Mutex
	// Monotonic entropy keeps IDs created in the same millisecond strictly increasing on this node,
	// so _id order is creation order for cursor pagination (S1 §1.1).
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// NewID returns "<prefix>_<lowercase ULID>". It panics for an unknown prefix: that is a programming
// error caught by tests, never a runtime condition.
func NewID(p Prefix) string {
	return newIDAt(p, time.Now())
}

func newIDAt(p Prefix, t time.Time) string {
	if !knownPrefixes[p] {
		panic(fmt.Sprintf("store: unknown ID prefix %q (add it to S1 §1.1 and ids.go)", p))
	}
	entropyMu.Lock()
	id := ulid.MustNew(ulid.Timestamp(t), entropy)
	entropyMu.Unlock()
	return string(p) + "_" + strings.ToLower(id.String())
}

// ParseID splits an ID into its prefix and ULID and validates both.
func ParseID(s string) (Prefix, ulid.ULID, error) {
	p, rest, ok := strings.Cut(s, "_")
	if !ok || !knownPrefixes[Prefix(p)] {
		return "", ulid.ULID{}, fmt.Errorf("store: %q is not a valid ID (unknown prefix)", s)
	}
	if len(rest) != ulid.EncodedSize || rest != strings.ToLower(rest) {
		return "", ulid.ULID{}, fmt.Errorf("store: %q is not a valid ID (bad ULID part)", s)
	}
	u, err := ulid.ParseStrict(strings.ToUpper(rest))
	if err != nil {
		return "", ulid.ULID{}, fmt.Errorf("store: %q is not a valid ID: %w", s, err)
	}
	return Prefix(p), u, nil
}

// IsID reports whether s is a valid ID with prefix p.
func IsID(p Prefix, s string) bool {
	got, _, err := ParseID(s)
	return err == nil && got == p
}

// IDTime returns the creation time encoded in a valid ID.
func IDTime(s string) (time.Time, error) {
	_, u, err := ParseID(s)
	if err != nil {
		return time.Time{}, err
	}
	return ulid.Time(u.Time()), nil
}
