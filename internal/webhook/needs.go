package webhook

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/store"
)

// routesCollection is internal/rules' route collection (read here without importing rules).
const routesCollection = "routes"

// Needs is what message.received must carry for an organisation: the union of the payload options of its
// enabled endpoints and routes (S5 §4.1 "payload needs", S7 §4). The sync engine fetches only that.
type Needs struct {
	Headers     []string // lower-case; "*" = all
	Text        bool
	TextMax     int
	Attachments bool
	// WebSafeHTML and CalendarEvents are the P4 payload options (S7 §4).
	WebSafeHTML    bool
	CalendarEvents bool
	ReplyText      bool
}

// NeedsResolver computes Needs from the database with a short per-organisation cache. It only reads, so it
// works on any node (sync nodes need it; the webhook service may not run there).
type NeedsResolver struct {
	DB  *store.DB
	TTL time.Duration // default 30 s

	mu    sync.Mutex
	cache map[string]cachedNeeds
}

type cachedNeeds struct {
	n  Needs
	at time.Time
}

// Needs returns the organisation's payload needs.
func (r *NeedsResolver) Needs(ctx context.Context, organizationID string) (Needs, error) {
	ttl := r.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	r.mu.Lock()
	if c, ok := r.cache[organizationID]; ok && time.Since(c.at) < ttl {
		r.mu.Unlock()
		return c.n, nil
	}
	r.mu.Unlock()
	var all []PayloadOptions
	cur, err := r.DB.Collection(EndpointsCollection).Find(ctx, bson.M{"organizationId": organizationID, "enabled": true},
		options.Find().SetProjection(bson.M{"payload": 1}))
	if err != nil {
		return Needs{}, err
	}
	var eps []struct {
		Payload PayloadOptions `bson:"payload"`
	}
	if err := cur.All(ctx, &eps); err != nil {
		return Needs{}, err
	}
	for _, e := range eps {
		all = append(all, e.Payload)
	}
	rcur, err := r.DB.Collection(routesCollection).Find(ctx, bson.M{"organizationId": organizationID, "enabled": true},
		options.Find().SetProjection(bson.M{"payload": 1, "filter": 1, "map": 1}))
	if err != nil {
		return Needs{}, err
	}
	var routes []struct {
		Payload map[string]any `bson:"payload"`
		Filter  string         `bson:"filter"`
		Map     string         `bson:"map"`
	}
	if err := rcur.All(ctx, &routes); err != nil {
		return Needs{}, err
	}
	var exprHints []PayloadOptions
	for _, rt := range routes {
		if len(rt.Payload) > 0 {
			b, err := json.Marshal(rt.Payload)
			if err == nil {
				var p PayloadOptions
				if json.Unmarshal(b, &p) == nil {
					all = append(all, p)
				}
			}
		}
		exprHints = append(exprHints, hintToPayload(rules.NeedsFromExpr(rt.Filter, rt.Map)))
	}
	// W73: endpoint CEL rules also imply needs
	epCur, err := r.DB.Collection(EndpointsCollection).Find(ctx, bson.M{"organizationId": organizationID, "enabled": true},
		options.Find().SetProjection(bson.M{"rule.filter": 1, "rule.map": 1}))
	if err == nil {
		var withRule []struct {
			Rule *struct {
				Filter string `bson:"filter"`
				Map    string `bson:"map"`
			} `bson:"rule"`
		}
		if epCur.All(ctx, &withRule) == nil {
			for _, e := range withRule {
				if e.Rule == nil {
					continue
				}
				exprHints = append(exprHints, hintToPayload(rules.NeedsFromExpr(e.Rule.Filter, e.Rule.Map)))
			}
		}
	}
	all = append(all, exprHints...)
	n := union(all)
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]cachedNeeds{}
	}
	r.cache[organizationID] = cachedNeeds{n: n, at: time.Now()}
	r.mu.Unlock()
	return n, nil
}

func hintToPayload(h rules.NeedsHint) PayloadOptions {
	p := PayloadOptions{}
	if h.Text {
		p.Text = "plain"
	}
	p.Attachments = h.Attachments
	p.Headers = append([]string(nil), h.Headers...)
	return p
}

func union(list []PayloadOptions) Needs {
	var n Needs
	for _, p := range list {
		if p.Text != "" && p.Text != "none" {
			n.Text = true
			n.TextMax = max(n.TextMax, p.TextMax)
		}
		n.Attachments = n.Attachments || p.Attachments
		n.WebSafeHTML = n.WebSafeHTML || p.WebSafeHTML
		n.CalendarEvents = n.CalendarEvents || p.CalendarEvents
		n.ReplyText = n.ReplyText || p.ReplyText
		for _, h := range p.Headers {
			h = strings.ToLower(strings.TrimSpace(h))
			if h != "" && !slices.Contains(n.Headers, h) {
				n.Headers = append(n.Headers, h)
			}
		}
	}
	if slices.Contains(n.Headers, "*") {
		n.Headers = []string{"*"}
	}
	slices.Sort(n.Headers)
	return n
}
