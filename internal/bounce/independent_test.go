package bounce

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Independent corpus (W32): blind check against samples not authored with the classifier.
func TestIndependentCorpusAccuracy(t *testing.T) {
	dir := filepath.Join("testdata_independent")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read independent corpus: %v", err)
	}

	type counts struct{ ok, total int }
	perKind := map[Kind]*counts{}
	// confusion[expected][got]
	confusion := map[Kind]map[Kind]int{}
	var total, ok int
	var hardPred, hardTP, hardExp int
	var misses []string

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
		if confusion[exp.Kind] == nil {
			confusion[exp.Kind] = map[Kind]int{}
		}
		confusion[exp.Kind][got.Kind]++

		if got.Kind == exp.Kind {
			c.ok++
			ok++
		} else {
			miss := fmt.Sprintf("%s: expected kind=%s bt=%q got kind=%s bt=%q",
				base, exp.Kind, exp.BounceType, got.Kind, got.BounceType)
			misses = append(misses, miss)
			t.Logf("MISS %s", miss)
		}

		expBounce := exp.Kind == KindDSNBounce || exp.Kind == KindHeuristicBounce
		if expBounce && exp.BounceType == BounceHard {
			hardExp++
		}
		isBounce := got.Kind == KindDSNBounce || got.Kind == KindHeuristicBounce
		if isBounce && got.BounceType == BounceHard {
			hardPred++
			if expBounce && exp.BounceType == BounceHard {
				hardTP++
			} else {
				t.Logf("HARD-FP %s: predicted hard bounce, label kind=%s bt=%s", base, exp.Kind, exp.BounceType)
			}
		}
	}

	if total != 120 {
		t.Fatalf("independent corpus size %d != 120", total)
	}

	acc := float64(ok) / float64(total)
	t.Logf("overall accuracy: %.4f (%d/%d)", acc, ok, total)

	kinds := make([]string, 0, len(perKind))
	for k := range perKind {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for _, ks := range kinds {
		k := Kind(ks)
		c := perKind[k]
		t.Logf("  %s: %.4f (%d/%d)", k, float64(c.ok)/float64(c.total), c.ok, c.total)
	}

	if hardPred == 0 {
		t.Fatalf("no hard-bounce predictions; cannot compute precision")
	}
	hardPrec := float64(hardTP) / float64(hardPred)
	t.Logf("hard-bounce precision: %.4f (%d/%d)", hardPrec, hardTP, hardPred)
	if hardExp == 0 {
		t.Fatalf("no hard-bounce expectations; cannot compute recall")
	}
	hardRec := float64(hardTP) / float64(hardExp)
	t.Logf("hard-bounce recall: %.4f (%d/%d)", hardRec, hardTP, hardExp)

	allKinds := []Kind{
		KindDSNBounce, KindHeuristicBounce, KindComplaint,
		KindAutoReply, KindChallengeResponse, KindNone,
	}
	t.Log("confusion matrix (rows=expected, cols=got):")
	header := "expected\\got"
	for _, g := range allKinds {
		header += fmt.Sprintf("\t%s", g)
	}
	t.Log(header)
	for _, expK := range allKinds {
		row := string(expK)
		for _, gotK := range allKinds {
			n := 0
			if confusion[expK] != nil {
				n = confusion[expK][gotK]
			}
			row += fmt.Sprintf("\t%d", n)
		}
		t.Log(row)
	}

	if len(misses) > 0 {
		t.Logf("misclassified (%d):", len(misses))
		for _, m := range misses {
			t.Logf("  %s", m)
		}
	}

	if acc < 0.90 {
		t.Fatalf("overall accuracy %.4f < 0.90", acc)
	}
	if hardPrec < 0.97 {
		t.Fatalf("hard-bounce precision %.4f < 0.97", hardPrec)
	}
}
