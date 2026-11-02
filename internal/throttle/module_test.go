package throttle

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/features"
)

func TestModuleStartWithoutBus(t *testing.T) {
	m := &Module{Registry: features.DefaultRegistry(), Env: "test"}
	a := app.New(app.Options{NodeID: "n1"})
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Gate() == nil || m.Gate().Counter == nil || m.Gate().PreSend == nil {
		t.Fatal("gate incomplete")
	}
	_ = m.Stop(context.Background())
	if m.Name() != "throttle" {
		t.Fatal(m.Name())
	}
	if len(m.Roles()) != 2 {
		t.Fatal(m.Roles())
	}
}

func TestBusCounterAndParseInt(t *testing.T) {
	mem := NewMemoryCounter()
	bc := BusCounter{
		AddFn: mem.Add,
		GetFn: mem.Get,
	}
	n, err := bc.Add(context.Background(), "x", 2, 0)
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	n, err = bc.Get(context.Background(), "x")
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	v, err := ParseIntBytes([]byte("42"))
	if err != nil || v != 42 {
		t.Fatal(v, err)
	}
}

func TestParseLimitsMap(t *testing.T) {
	l := ParseLimits(map[string]any{"domainPerMinute": float64(7)})
	if l.DomainPerMinute != 7 {
		t.Fatal(l)
	}
}
