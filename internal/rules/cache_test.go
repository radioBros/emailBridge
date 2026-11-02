package rules

import "testing"

func TestProgramCacheLRU(t *testing.T) {
	e := testEngine(t, EngineOptions{CacheSize: 2})
	p1, err := e.Cached("rte_a", 1, "true", "")
	if err != nil || p1 == nil {
		t.Fatal(err)
	}
	p1b, err := e.Cached("rte_a", 1, "true", "")
	if err != nil || p1b != p1 {
		t.Fatal("expected cache hit")
	}
	_, _ = e.Cached("rte_b", 1, "true", "")
	_, _ = e.Cached("rte_c", 1, "true", "") // evicts oldest
	// rte_a may be gone
	p2, err := e.Cached("rte_a", 1, "type == \"message.received\"", "")
	if err != nil || p2 == nil {
		t.Fatal(err)
	}
	e.InvalidateCache("rte_a", 1)
	e.InvalidateRoute("rte_b")
	p3, _ := e.Cached("rte_a", 1, "true", "")
	if p3 == nil {
		t.Fatal("recompile")
	}
}
