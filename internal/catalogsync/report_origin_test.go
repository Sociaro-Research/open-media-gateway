package catalogsync

import (
	"encoding/json"
	"html"
	"strings"
	"testing"
)

func TestReportIdentifiesPersistedCheckAndRun(t *testing.T) {
	var bodies []string
	for _, tc := range []struct{ origin, id string }{
		{"https://first.example.test", "a7288140-f68c-41b7-8a76-f0da27f67bfd"},
		{"https://second.example.test", "90dc6175-541c-4c92-8c39-956b8021c3fc"},
	} {
		var run Run
		data := `{"check_origin":"` + tc.origin + `","id":"` + tc.id + `","provider":"fixture","status":"unchanged","version":6,"completed_at":"2026-11-05T01:00:00Z"}`
		if err := json.Unmarshal([]byte(data), &run); err != nil {
			t.Fatal(err)
		}
		_, text, markup := Report(run)
		for _, body := range []string{text, markup} {
			if !strings.Contains(body, "Check instance: "+tc.origin) || !strings.Contains(body, "Run ID: "+tc.id) {
				t.Errorf("report must correlate the stored checking instance and run: %s", body)
			}
		}
		bodies = append(bodies, text)
	}
	if bodies[0] == bodies[1] {
		t.Error("equivalent catalogue results from distinct instances/runs have indistinguishable reports")
	}
}

func TestReportCheckOriginValidationAndLegacy(t *testing.T) {
	for _, tc := range []struct{ name, origin, want string }{
		{"https", "https://console.example.test", "https://console.example.test"},
		{"canonical", "https://CONSOLE.Example.test:443/", "https://console.example.test"},
		{"trailing dot", "https://console.example.test./", "https://console.example.test"},
		{"idna", "https://bücher.example", "https://xn--bcher-kva.example"},
		{"ipv6", "https://[::1]:8443", "https://[::1]:8443"},
		{"development", "http://localhost:8080/", "http://localhost:8080"},
		{"legacy", "", ""},
		{"userinfo", "https://user:synthetic-secret@example.test", ""},
		{"query", "https://example.test?token=synthetic-secret", ""},
		{"empty query", "https://example.test?", ""},
		{"fragment", "https://example.test#synthetic-secret", ""},
		{"path", "https://example.test/private/provider", ""},
		{"http remote", "http://example.test", ""},
		{"bad scheme", "file:///private/provider", ""},
		{"control", "https://example.test\r\nBcc: synthetic@example.test", ""},
		{"unicode control", "https://ex\u0085ample.test", ""},
		{"bidi", "https://ex\u202eample.test", ""},
		{"encoded control", "https://example.test/%0Ainjection", ""},
		{"markup", "https://example.test/<script>synthetic</script>", ""},
		{"padding", " https://example.test", ""},
		{"invalid port", "https://example.test:65536", ""},
		{"oversized", "https://" + strings.Repeat("a", 301), ""},
		{"300 bytes", "https://" + strings.Repeat("Ａ", 63) + "." + strings.Repeat("Ｂ", 31) + "aaaa.test", "https://" + strings.Repeat("a", 63) + "." + strings.Repeat("b", 31) + "aaaa.test"},
		{"301 bytes", "https://" + strings.Repeat("Ａ", 63) + "." + strings.Repeat("Ｂ", 31) + "aaaa.testa", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := Run{ID: "a7288140-f68c-41b7-8a76-f0da27f67bfd", Provider: "fixture", CheckOrigin: tc.origin}
			_, text, markup := Report(run)
			want := tc.want
			if want == "" {
				want = "unknown (not recorded)"
			}
			for _, body := range []string{text, html.UnescapeString(markup)} {
				if !strings.Contains(body, "Check instance: "+want+"\nRun ID: "+run.ID+"\n") {
					t.Fatal("incorrect check attribution", body)
				}
				if tc.want == "" && tc.origin != "" && strings.Contains(body, tc.origin) {
					t.Fatal("invalid stored origin was reflected")
				}
			}
		})
	}
}

func TestReportAttributionPreservesEscapingAndResult(t *testing.T) {
	run := Run{ID: "a7288140-f68c-41b7-8a76-f0da27f67bfd", Provider: "<fixture>", Status: "changed", Version: 6, OpenWarnings: []Warning{{Key: "old", Reason: "<script>synthetic</script>", Occurrences: 3}}}
	subject, before, _ := Report(run)
	run.CheckOrigin = "https://console.example.test"
	gotSubject, after, markup := Report(run)
	if subject != gotSubject || strings.Replace(before, "unknown (not recorded)", run.CheckOrigin, 1) != after {
		t.Fatal("adding provenance changed result or subject")
	}
	if strings.Contains(markup, "<fixture>") || strings.Contains(markup, "<script>") || !strings.Contains(markup, "&lt;script&gt;") || !strings.Contains(after, "checks: 3") {
		t.Fatal("report escaping or existing warnings changed", markup)
	}
}
