package catalogsync

import (
	"runtime"
	"strings"
	"testing"
)

// Keep the ignored-end-tag case bounded: 10,000 tokens are about 50 KiB of
// source HTML. A vulnerable parser copies tens of MiB while coalescing them.
// Match the upstream regression's generous allocation budget, not wall time.
func TestHTMLCatalogueTextCoalescingAllocations(t *testing.T) {
	const tokens = 10000
	const budget = 5 << 20
	repeated := strings.Repeat("a</x>", tokens)
	const header = "<tr><th>Model ID</th><th>Capabilities</th></tr>"
	const rowStart = "<tr><td>qwen-image-2.1-pro</td><td>"
	for _, tc := range []struct {
		name, source, want string
	}{
		{"cell text", "<table>" + header + rowStart + repeated + "</td></tr></table>", strings.Repeat("a", tokens)},
		{"foster parent text", "<table>" + repeated + header + rowStart + "retained capability</td></tr></table>", "retained capability"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte(tc.source)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			entries, _, err := Parse(Source{Parser: "alibaba-image-models"}, input)
			runtime.ReadMemStats(&after)
			allocated := after.TotalAlloc - before.TotalAlloc
			t.Logf("input_bytes=%d allocated_bytes=%d budget_bytes=%d", len(input), allocated, budget)
			if err != nil || len(entries) != 1 || entries[0].ID != "qwen-image-2.1-pro" || entries[0].Metadata == nil {
				t.Fatalf("bounded source lost its model: entries=%+v err=%v", entries, err)
			}
			facts := entries[0].Metadata.Facts
			if len(facts) != 1 || facts[0].Label != "Provider: Capabilities" || facts[0].Value != tc.want {
				t.Fatal("text coalescing changed the model capability")
			}
			if allocated > budget {
				t.Fatalf("coalescing %d text tokens allocated %d bytes, budget %d", tokens, allocated, budget)
			}
		})
	}
}
