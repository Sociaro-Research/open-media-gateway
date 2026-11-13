package identity

import "testing"

func TestUnreadableName(t *testing.T) {
	for _, name := range []string{"Project", "Проект", "项目", "🙂", "👨\u200d👩\u200d👧", "مشروع", "\u200fمشروع", "שלום", "e\u0301", "a\u200bb", "Ünïcödé 2026"} {
		if UnreadableName(name) {
			t.Errorf("rejected visible name %q", name)
		}
	}
	for _, name := range []string{"", " ", "\u00a0\u3000", "\u200b", "\u200b\u200b", "\u200e", "\u200f", "\ufeff", "\u00ad", "\u2060", "\u202eevil", "name\u202a", "\u2066x\u2069", "\u202dQA"} {
		if !UnreadableName(name) {
			t.Errorf("accepted unreadable name %q", name)
		}
	}
}
