package integrations

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type fakeEndpoints struct {
	mu      sync.Mutex
	next    int
	items   map[string]*epRec
	created []string
}

type epRec struct {
	ID      string
	Name    string
	URL     string
	Enabled bool
	Version int64
	Events  []string
}

func newFakeEndpoints() *fakeEndpoints {
	return &fakeEndpoints{items: map[string]*epRec{}}
}

func (f *fakeEndpoints) Create(_ context.Context, spec EndpointSpec) (*CreatedEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := "whe_" + strings.Repeat("a", 25) + fmt.Sprintf("%d", f.next%10)
	rec := &epRec{ID: id, Name: spec.Name, URL: spec.URL, Enabled: true, Version: 1, Events: spec.Events}
	f.items[id] = rec
	f.created = append(f.created, id)
	return &CreatedEndpoint{ID: id, Version: 1}, nil
}

func (f *fakeEndpoints) UpdateURL(_ context.Context, id, url string, _ int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.items[id]
	if rec == nil {
		return 0, errNotFound("endpoint")
	}
	rec.URL = url
	rec.Version++
	return rec.Version, nil
}

func (f *fakeEndpoints) SetEnabled(_ context.Context, id string, enabled bool, _ int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.items[id]
	if rec == nil {
		return 0, errNotFound("endpoint")
	}
	rec.Enabled = enabled
	rec.Version++
	return rec.Version, nil
}

func (f *fakeEndpoints) Delete(_ context.Context, id string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, id)
	return nil
}

type fakeRoutes struct {
	mu    sync.Mutex
	next  int
	items map[string]*rtRec
}

type rtRec struct {
	ID, Name, EndpointID, Filter, Map string
	Enabled                           bool
	Version                           int64
}

func newFakeRoutes() *fakeRoutes {
	return &fakeRoutes{items: map[string]*rtRec{}}
}

func (f *fakeRoutes) Create(_ context.Context, spec RouteSpec) (*CreatedRoute, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := "rte_" + strings.Repeat("b", 25) + fmt.Sprintf("%d", f.next%10)
	f.items[id] = &rtRec{
		ID: id, Name: spec.Name, EndpointID: spec.EndpointID,
		Filter: spec.Filter, Map: spec.Map, Enabled: true, Version: 1,
	}
	return &CreatedRoute{ID: id, Version: 1}, nil
}

func (f *fakeRoutes) SetEnabled(_ context.Context, id string, enabled bool, _ int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.items[id]
	if rec == nil {
		return 0, errNotFound("route")
	}
	rec.Enabled = enabled
	rec.Version++
	return rec.Version, nil
}

func (f *fakeRoutes) Delete(_ context.Context, id string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, id)
	return nil
}

type fakeSettings struct {
	mu       sync.Mutex
	values   map[string]string
	switches map[string]bool
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{values: map[string]string{}, switches: map[string]bool{}}
}

func (f *fakeSettings) Apply(_ context.Context, values map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range values {
		f.values[k] = v
	}
	return nil
}

func (f *fakeSettings) Clear(_ context.Context, keys []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.values, k)
	}
	return nil
}

func (f *fakeSettings) SetSwitch(_ context.Context, name string, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switches[name] = on
	return nil
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

func errNotFound(what string) error { return simpleErr(what + " not found") }
