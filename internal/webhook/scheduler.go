package webhook

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
)

const maxOrderingQueue = 1_000

type scheduler struct {
	mu        sync.Mutex
	endpoints map[string]*endpointSlots
	keys      map[string]*orderingKey
}

type endpointSlots struct {
	sem chan struct{}
}

type orderingKey struct {
	mu      sync.Mutex
	waiting int
}

func newScheduler() *scheduler {
	return &scheduler{endpoints: map[string]*endpointSlots{}, keys: map[string]*orderingKey{}}
}

func (s *scheduler) run(
	ctx context.Context,
	endpointID, accountID, ordering string,
	maxConcurrency int,
	work func() error,
) error {
	if maxConcurrency <= 0 {
		maxConcurrency = 8
	}
	slots := s.endpoint(endpointID, maxConcurrency)
	var key *orderingKey
	keyName := endpointID + "/" + accountID
	if ordering == OrderingStrict {
		var ok bool
		key, ok = s.enterKey(keyName)
		if !ok {
			return bus.DelayRetry(time.Second, errors.New("webhook: ordering queue full"))
		}
		defer s.leaveKey(keyName, key)
		key.mu.Lock()
		defer key.mu.Unlock()
	}
	select {
	case slots.sem <- struct{}{}:
		defer func() { <-slots.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return work()
}

func (s *scheduler) endpoint(id string, limit int) *endpointSlots {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gate := s.endpoints[id]; gate != nil {
		return gate
	}
	gate := &endpointSlots{sem: make(chan struct{}, limit)}
	s.endpoints[id] = gate
	return gate
}

func (s *scheduler) enterKey(name string) (*orderingKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.keys[name]
	if key == nil {
		key = &orderingKey{}
		s.keys[name] = key
	}
	if key.waiting >= maxOrderingQueue {
		return key, false
	}
	key.waiting++
	return key, true
}

func (s *scheduler) leaveKey(name string, key *orderingKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key.waiting--
	if key.waiting == 0 {
		delete(s.keys, name)
	}
}
