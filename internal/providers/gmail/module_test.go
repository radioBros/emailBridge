package gmail

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestMigration(t *testing.T) {
	m := Migration(34)
	if m.Number != 34 || m.Name == "" || m.Up == nil {
		t.Fatalf("%+v", m)
	}
}

func TestNewStateViaMemory(t *testing.T) {
	ms := &MemoryState{}
	acc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization)}
	ctx := context.Background()
	st, err := ms.Load(ctx, acc)
	if err != nil || st.UIDValidity == 0 || st.AccountID != acc.ID {
		t.Fatalf("%v %+v", err, st)
	}
	st.HistoryID = "9"
	_ = ms.Save(ctx, st)
	st2, _ := ms.Load(ctx, acc)
	if st2.HistoryID != "9" {
		t.Fatal(st2.HistoryID)
	}
}
