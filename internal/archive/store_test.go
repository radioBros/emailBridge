package archive

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/objstore"
)

func TestArchiveKeyShape(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	key, err := objstore.ArchiveKey("org_a", "acc_b", "msg_c", at)
	if err != nil {
		t.Fatal(err)
	}
	want := "archive/org_a/acc_b/2026/03/msg_c.eml"
	if key != want {
		t.Fatalf("got %q want %q", key, want)
	}
}
