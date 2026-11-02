package events

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/store"
)

// TestEventExamples: every catalogue type has a documentation example (W70) of its own Go data type, with an
// event ID and a subject of the catalogue's prefix, and the generated schema carries it.
func TestEventExamples(t *testing.T) {
	files, err := SchemaFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, ct := range Types() {
		ex, ok := eventExamples[ct.Name]
		if !ok {
			t.Errorf("%s: no example in examples.go", ct.Name)
			continue
		}
		dt := reflect.TypeOf(ex.Data)
		if dt != ct.Data {
			t.Errorf("%s: example data is %v, want %v", ct.Name, dt, ct.Data)
		}
		if !store.IsID(store.PrefixEvent, ex.ID) {
			t.Errorf("%s: bad event ID %q", ct.Name, ex.ID)
		}
		if !store.IsID(ct.Subject, ex.Subject) && (ct.AltSubject == "" || !store.IsID(ct.AltSubject, ex.Subject)) {
			t.Errorf("%s: subject %q is not a %s_ ID", ct.Name, ex.Subject, ct.Subject)
		}
		var doc struct {
			Examples []map[string]any `json:"examples"`
		}
		name := ct.Name + ".v1.json"
		if err := json.Unmarshal(files[name], &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(doc.Examples) != 2 || doc.Examples[0]["type"] != ct.Name || doc.Examples[0]["data"] == nil {
			t.Errorf("%s: want [envelope, data] examples, got %d", name, len(doc.Examples))
			continue
		}
		src, _ := doc.Examples[0]["source"].(string)
		if acc, _ := doc.Examples[0]["ebaccount"].(string); acc != "" && !strings.HasSuffix(src, "/accounts/"+acc) {
			t.Errorf("%s: source %q does not name ebaccount %q", name, src, acc)
		}
	}
	for name := range eventExamples {
		if _, ok := Lookup(name); !ok {
			t.Errorf("examples.go has %s, which is not in the catalogue", name)
		}
	}
}
