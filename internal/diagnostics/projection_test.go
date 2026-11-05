package diagnostics

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRegistryProjectsConcurrentReadersWithBoundedInput(t *testing.T) {
	var r Registry
	r.Register("fixture", func(raw string) (string, bool) { return "trusted", raw == "legacy" })
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Register(fmt.Sprint(i), func(string) (string, bool) { return "", false })
			if safe, ok := r.Project("legacy"); !ok || safe != "trusted" {
				t.Error("concurrent projector lost")
			}
		}(i)
	}
	wg.Wait()
	if _, ok := r.Project(strings.Repeat("x", MaxStoredMessageBytes+1)); ok {
		t.Fatal("oversized stored message accepted")
	}
}

func TestExactVocabularyIsClonedAndCannotEchoSuffixes(t *testing.T) {
	table := map[string]string{"historical-fixture": "trusted-fixture"}
	RegisterExact("test-cloned-vocabulary", table)
	table["historical-fixture"] = "private-token"
	if safe, ok := Project("historical-fixture"); !ok || safe != "trusted-fixture" {
		t.Fatal("mutable source poisoned projector")
	}
	if _, ok := Project("historical-fixture private-token"); ok {
		t.Fatal("arbitrary suffix accepted")
	}
}
