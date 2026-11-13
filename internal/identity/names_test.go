package identity

import "testing"

func TestUnreadableName(t *testing.T) {
	for _, name := range []string{"Project", "Проект", "项目", "🙂", "👨\u200d👩\u200d👧", "مشروع", "\u200fمشروع", "שלום", "e\u0301", "a\u200bb", "Ünïcödé 2026"} {
		if UnreadableName(name) {
			t.Errorf("rejected visible name %q", name)
		}
	}
	// Default-ignorable characters stay allowed next to visible text.
	for _, name := range []string{"❤️", "한글"} {
		if UnreadableName(name) {
			t.Errorf("rejected visible name %q", name)
		}
	}
	// The last four are default-ignorable without being format characters:
	// combining grapheme joiner, variation selector, Hangul fillers.
	for _, name := range []string{"", " ", "\u00a0\u3000", "\u200b", "\u200b\u200b", "\u200e", "\u200f", "\ufeff", "\u00ad", "\u2060", "\u202eevil", "name\u202a", "\u2066x\u2069", "\u202dQA", "\u034f", "\ufe0f", "\u3164", "\u115f\u1160"} {
		if !UnreadableName(name) {
			t.Errorf("accepted unreadable name %q", name)
		}
	}
}
