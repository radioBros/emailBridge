package configcode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// HashPlan returns "sha256:" + hex of the canonical plan body (prune + actions).
func HashPlan(prune bool, actions []Action) (string, error) {
	body := struct {
		Prune   bool     `json:"prune"`
		Actions []Action `json:"actions"`
	}{Prune: prune, Actions: normalizeActions(actions)}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func normalizeActions(in []Action) []Action {
	out := make([]Action, len(in))
	for i, a := range in {
		out[i] = a
		if a.Changes != nil {
			out[i].Changes = sortChanges(a.Changes)
		}
	}
	return out
}

func sortChanges(m map[string]any) map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(m))
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}
