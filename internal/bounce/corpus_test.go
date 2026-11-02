package bounce

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCorpusAccuracy(t *testing.T) {
	dir := filepath.Join("testdata", "corpus")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	type counts struct {
		ok, total int
	}
	perKind := map[Kind]*counts{}
	var total, ok int
	var hardPred, hardTP int

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".eml") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".eml")
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		labelRaw, err := os.ReadFile(filepath.Join(dir, base+".json"))
		if err != nil {
			t.Fatalf("missing label for %s: %v", base, err)
		}
		var exp Expected
		if err := json.Unmarshal(labelRaw, &exp); err != nil {
			t.Fatalf("label %s: %v", base, err)
		}
		got, err := Classify(raw)
		if err != nil {
			t.Fatalf("Classify %s: %v", base, err)
		}
		c := perKind[exp.Kind]
		if c == nil {
			c = &counts{}
			perKind[exp.Kind] = c
		}
		c.total++
		total++
		if got.Kind == exp.Kind {
			c.ok++
			ok++
		} else {
			t.Logf("MISS %s: want kind=%s got=%s bt=%s", base, exp.Kind, got.Kind, got.BounceType)
		}

		isBounce := got.Kind == KindDSNBounce || got.Kind == KindHeuristicBounce
		if isBounce && got.BounceType == BounceHard {
			hardPred++
			expBounce := exp.Kind == KindDSNBounce || exp.Kind == KindHeuristicBounce
			if expBounce && exp.BounceType == BounceHard {
				hardTP++
			} else {
				t.Logf("HARD-FP %s: predicted hard bounce, label kind=%s bt=%s", base, exp.Kind, exp.BounceType)
			}
		}
	}

	if total < 150 {
		t.Fatalf("corpus size %d < 150", total)
	}
	acc := float64(ok) / float64(total)
	t.Logf("overall accuracy: %.4f (%d/%d)", acc, ok, total)
	for k, c := range perKind {
		t.Logf("  %s: %.4f (%d/%d)", k, float64(c.ok)/float64(c.total), c.ok, c.total)
	}
	if hardPred == 0 {
		t.Fatalf("no hard-bounce predictions; cannot compute precision")
	}
	hardPrec := float64(hardTP) / float64(hardPred)
	t.Logf("hard-bounce precision: %.4f (%d/%d)", hardPrec, hardTP, hardPred)

	if acc < 0.95 {
		t.Fatalf("overall accuracy %.4f < 0.95", acc)
	}
	if hardPrec < 0.98 {
		t.Fatalf("hard-bounce precision %.4f < 0.98", hardPrec)
	}
}
