package lockfile

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// Temporary probe: cross-checks collateLess against a Node-generated corpus
// when SKILLM_COLLATE_FUZZ points at it. Not part of the regular suite.
func TestCollateFuzzAgainstNode(t *testing.T) {
	path := os.Getenv("SKILLM_COLLATE_FUZZ")
	if path == "" {
		t.Skip("no corpus")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c struct{ Input, Sorted []string }
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	got := append([]string(nil), c.Input...)
	sort.SliceStable(got, func(i, j int) bool { return collateLess(got[i], got[j]) })
	mism := 0
	for i := range got {
		if got[i] != c.Sorted[i] {
			mism++
			if mism <= 10 {
				t.Errorf("pos %d: got %q want %q", i, got[i], c.Sorted[i])
			}
		}
	}
	if mism > 0 {
		t.Fatalf("%d/%d positions differ", mism, len(got))
	}
}
