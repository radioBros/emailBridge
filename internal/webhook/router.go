package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/store"
)

type deliveryJob struct {
	DeliveryID string `json:"deliveryId"`
}

// RouteEvent matches direct endpoints and CEL routes, inserts de-duplicated delivery records, then
// publishes their IDs to the delivery stream. It runs under a consumer span of the event's trace.
func (s *Service) RouteEvent(ctx context.Context, event events.Event) error {
	if event.Type == events.EndpointTest {
		return nil
	}
	ctx, span := logging.StartSpan(ctx, "webhook.route", event.TraceParent, trace.SpanKindConsumer,
		attribute.String("eb.event.type", event.Type))
	defer span.End()
	if err := s.routeEvent(ctx, event); err != nil {
		span.SetStatus(codes.Error, "routing failed")
		return err
	}
	return nil
}

// routeEvent applies, per enabled endpoint of the organisation (W73 §1.1): event subscription → structural
// filter → endpoint CEL filter → endpoint map; and every enabled route (S8) whose filter matches. An endpoint
// with a CEL rule ("rule mode") gets no direct delivery when a route targeting it matched (§1.2).
//
// Endpoint rules are evaluated whatever the rules.enabled switch says (that switch governs routes): an endpoint
// filter restricts what the endpoint receives, so it must never be skipped and fail open.
func (s *Service) routeEvent(ctx context.Context, event events.Event) error {
	endpoints, err := s.Repo.enabledEndpoints(ctx, event.EBOrg)
	if err != nil {
		return err
	}
	byID := make(map[string]*Endpoint, len(endpoints))
	anyRule := false
	for i := range endpoints {
		byID[endpoints[i].ID] = &endpoints[i]
		anyRule = anyRule || endpoints[i].Rule.HasCEL()
	}
	routesOn := s.RulesEnabled == nil || s.RulesEnabled(ctx, event.EBOrg)

	var in *ruleActivation
	if s.Engine != nil && (routesOn || anyRule) {
		if in, err = s.activation(ctx, event); err != nil {
			return err
		}
	}

	// Routes are evaluated first (rule-mode endpoints need to know whether a route matched) but enqueued
	// after the direct deliveries, keeping the S7 order: direct, then routes.
	routedWithCEL := map[string]bool{} // endpoint IDs a route matched (no double delivery)
	var routed []routedDelivery
	if routesOn && in != nil {
		if routed, err = s.matchRoutes(ctx, event, byID, in, routedWithCEL); err != nil {
			return err
		}
	}

	for i := range endpoints {
		ep := &endpoints[i]
		if !endpointMatches(ep, event) {
			continue
		}
		var mapped any
		mappedSet := false
		if ep.Rule.HasCEL() {
			if routedWithCEL[ep.ID] {
				continue // a route already owns this (event, endpoint)
			}
			if in == nil { // no engine: fail closed rather than deliver unfiltered
				s.observeRule(event.EBOrg, "endpoint", ep.ID, event.ID, false, "rules engine unavailable", "")
				continue
			}
			ok, m, set := s.evalEndpointRule(ctx, event, ep, in)
			if !ok {
				continue
			}
			mapped, mappedSet = m, set
		}
		mapped, mappedSet = s.maybeAIEnrich(ctx, event, ep, mapped, mappedSet)
		if err := s.enqueueDelivery(ctx, event, ep, "", mapped, mappedSet); err != nil {
			return err
		}
	}
	for _, d := range routed {
		mapped, mappedSet := s.maybeAIEnrich(ctx, event, d.endpoint, d.mapped, d.mappedSet)
		if err := s.enqueueDelivery(ctx, event, d.endpoint, d.routeID, mapped, mappedSet); err != nil {
			return err
		}
	}
	return nil
}

// routedDelivery is a matched route waiting to be enqueued.
type routedDelivery struct {
	endpoint  *Endpoint
	routeID   string
	mapped    any
	mappedSet bool
}

// ruleActivation is the CEL input shared by every route and endpoint rule of one event.
type ruleActivation struct {
	event   map[string]any
	account map[string]any
	env     map[string]string
	lists   map[string]map[string]struct{}
}

func (s *Service) activation(ctx context.Context, event events.Event) (*ruleActivation, error) {
	eventMap, err := eventActivation(event)
	if err != nil {
		return nil, err
	}
	account, env, err := s.ruleInputs(ctx, event)
	if err != nil {
		return nil, err
	}
	in := &ruleActivation{event: eventMap, account: account, env: env}
	if s.Rules != nil && s.Rules.Lists != nil {
		// A failed list load retries the event: evaluating inList() against an empty snapshot would
		// silently skip deliveries.
		if in.lists, err = s.Rules.Lists.Snapshot(ctx, event.EBOrg); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// matchRoutes evaluates every enabled route of the organisation and returns the matched deliveries.
func (s *Service) matchRoutes(ctx context.Context, event events.Event, byID map[string]*Endpoint, in *ruleActivation,
	routedWithCEL map[string]bool) ([]routedDelivery, error) {
	routes, err := s.Repo.enabledRoutes(ctx, event.EBOrg)
	if err != nil {
		return nil, err
	}
	var out []routedDelivery
	for i := range routes {
		route := &routes[i]
		endpoint := byID[route.EndpointID]
		if endpoint == nil {
			continue
		}
		program, cerr := s.Engine.Cached(route.ID, route.CurrentVersion, route.Filter, route.Map)
		if cerr != nil {
			s.writeRuleError(ctx, route, event, "compile", cerr)
			s.observeRule(event.EBOrg, "route", route.ID, event.ID, false, cerr.Error(), "")
			continue
		}
		matched, mapped, _, evalErr := s.Engine.EvalWithLists(ctx, program, in.event, in.account, in.env, in.lists)
		if evalErr != nil {
			phase := "filter"
			if matched && strings.TrimSpace(route.Map) != "" {
				phase = "map"
			}
			s.writeRuleError(ctx, route, event, phase, evalErr)
			s.observeRule(event.EBOrg, "route", route.ID, event.ID, false, evalErr.Error(), "")
			continue
		}
		s.observeRule(event.EBOrg, "route", route.ID, event.ID, matched, "", "")
		if !matched {
			continue
		}
		if endpoint.Rule.HasCEL() {
			routedWithCEL[endpoint.ID] = true
		}
		out = append(out, routedDelivery{endpoint: endpoint, routeID: route.ID, mapped: mapped,
			mappedSet: strings.TrimSpace(route.Map) != ""})
	}
	return out, nil
}

// evalEndpointRule runs the endpoint filter (and map when matched). ok=false skips the direct delivery.
func (s *Service) evalEndpointRule(ctx context.Context, event events.Event, ep *Endpoint, in *ruleActivation) (ok bool, mapped any, mappedSet bool) {
	filter, mapExpr, version := ruleProgram(ep.Rule)
	program, err := s.Engine.Cached("endpoint:"+ep.ID, version, filter, mapExpr)
	if err != nil {
		s.observeRule(event.EBOrg, "endpoint", ep.ID, event.ID, false, err.Error(), "")
		return false, nil, false
	}
	matched, out, _, err := s.Engine.EvalWithLists(ctx, program, in.event, in.account, in.env, in.lists)
	if err != nil {
		s.observeRule(event.EBOrg, "endpoint", ep.ID, event.ID, false, err.Error(), "")
		return false, nil, false
	}
	s.observeRule(event.EBOrg, "endpoint", ep.ID, event.ID, matched, "", "")
	if !matched {
		return false, nil, false
	}
	return true, out, strings.TrimSpace(mapExpr) != ""
}

func (s *Service) observeRule(org, kind, id, eventID string, matched bool, errMsg, reason string) {
	if s.Rules != nil && s.Rules.Stats != nil {
		s.Rules.Stats.Observe(org, kind, id, eventID, matched, errMsg, reason)
	}
}

func (s *Service) enqueueDelivery(
	ctx context.Context,
	event events.Event,
	endpoint *Endpoint,
	routeID string,
	mapped any,
	mappedSet bool,
) error {
	now := s.now()
	delivery := &Delivery{
		ID: storeDeliveryID(), OrganizationID: event.EBOrg, AccountID: event.EBAccount,
		EventID: event.ID, EventType: event.Type, EndpointID: endpoint.ID, RouteID: routeID,
		State: StatePending, Attempts: []DeliveryAttempt{}, CreatedAt: now, UpdatedAt: now,
		ExpireAt: now.Add(s.retention()), Version: 1,
	}
	if mappedSet {
		raw, err := json.Marshal(mapped)
		if err != nil {
			return err
		}
		if err := s.sealMappedBody(ctx, delivery, string(raw)); err != nil {
			return err
		}
	}
	inserted, err := s.Repo.insertDelivery(ctx, delivery)
	if err != nil {
		return err
	}
	if !inserted {
		delivery, err = s.Repo.deliveryByIdentity(ctx, event.ID, endpoint.ID, routeID)
		if err != nil {
			return err
		}
	}
	body, err := json.Marshal(deliveryJob{DeliveryID: delivery.ID})
	if err != nil {
		return err
	}
	partition := bus.DeliveryPartition(endpoint.ID, event.EBAccount, s.partitions())
	if s.Publisher == nil {
		return errorsNewPublisher()
	}
	_, err = s.Publisher.Publish(ctx, bus.DeliverySubject(partition), body, delivery.ID, nil)
	if err != nil {
		return err
	}
	logCtx := logging.With(ctx, func(ids *logging.IDs) {
		ids.OrganizationID, ids.AccountID = event.EBOrg, event.EBAccount
		ids.EventID, ids.DeliveryID = event.ID, delivery.ID
	})
	s.log().InfoContext(logCtx, "webhook delivery queued",
		"endpointId", endpoint.ID,
		"eventType", event.Type,
		"routeId", routeID,
	)
	return nil
}

func (s *Service) ruleInputs(ctx context.Context, event events.Event) (map[string]any, map[string]string, error) {
	account := map[string]any{}
	env := map[string]string{}
	if s.Rules == nil {
		return account, env, nil
	}
	if s.Rules.AccountLoader != nil && event.EBAccount != "" {
		loaded, err := s.Rules.AccountLoader(ctx, event.EBOrg, event.EBAccount)
		if err != nil {
			return nil, nil, err
		}
		if loaded != nil {
			account = loaded
		}
	}
	if s.Rules.EnvLoader != nil {
		loaded, _, err := s.Rules.EnvLoader(ctx, event.EBOrg)
		if err != nil {
			return nil, nil, err
		}
		if loaded != nil {
			env = loaded
		}
	}
	return account, env, nil
}

func (s *Service) writeRuleError(ctx context.Context, route *rules.Route, event events.Event, phase string, err error) {
	if s.Rules != nil {
		_ = s.Rules.WriteRuleError(ctx, rules.RuleError{
			OrganizationID: route.OrganizationID,
			RouteID:        route.ID,
			Version:        route.CurrentVersion,
			Phase:          phase,
			Message:        err.Error(),
			EventID:        event.ID,
			PayloadExcerpt: fmt.Sprintf(`{"id":%q,"type":%q,"subject":%q}`, event.ID, event.Type, event.Subject),
		})
	}
	s.log().WarnContext(ctx, "webhook route skipped",
		"routeId", route.ID,
		"eventId", event.ID,
		"phase", phase,
		"kind", errs.KindOf(err),
		"code", errs.CodeOf(err),
	)
}

func eventActivation(event events.Event) (map[string]any, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// maybeAIEnrich adds data.ai when the endpoint asks (fail-open). Returns a mapped body so
// delivery uses the enriched payload without changing the deliver path (W52).
func (s *Service) maybeAIEnrich(
	ctx context.Context,
	event events.Event,
	endpoint *Endpoint,
	mapped any,
	mappedSet bool,
) (any, bool) {
	if endpoint == nil || !endpoint.Payload.AIEnrichment || s.AIEnricher == nil {
		return mapped, mappedSet
	}
	if event.Type != events.MessageReceived {
		return mapped, mappedSet
	}
	var envelope map[string]any
	if mappedSet {
		switch m := mapped.(type) {
		case map[string]any:
			envelope = cloneAnyMap(m)
		default:
			raw, err := json.Marshal(mapped)
			if err != nil {
				return mapped, mappedSet
			}
			if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
				return mapped, mappedSet
			}
		}
	} else {
		body, err := buildBody(event, nil, false, endpoint.Payload)
		if err != nil {
			return mapped, mappedSet
		}
		if json.Unmarshal(body, &envelope) != nil || envelope == nil {
			return mapped, mappedSet
		}
	}
	data, _ := envelope["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
		envelope["data"] = data
	}
	ai := s.AIEnricher(ctx, event.EBOrg, event.EBAccount, data)
	if len(ai) > 0 {
		data["ai"] = ai
	}
	return envelope, true
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func storeDeliveryID() string {
	return store.NewID(store.PrefixDelivery)
}

func errorsNewPublisher() error {
	return fmt.Errorf("webhook: publisher not configured")
}
