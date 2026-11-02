package accounts

import (
	"testing"
	"time"

	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

func TestSyncConfigFieldsRoundTripShape(t *testing.T) {
	from := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	cfg := SyncConfig{
		SyncFrom:   &from,
		SpecialUse: map[string]string{"sent": "Sent Items", "trash": "Deleted"},
	}
	acc := &ebsync.Account{SyncFrom: from, SpecialUse: cfg.SpecialUse}
	if acc.SyncFrom.IsZero() || acc.SpecialUse["sent"] != "Sent Items" {
		t.Fatalf("%+v", acc)
	}
}
