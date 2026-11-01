package core

import "testing"

func TestTypedTextListsRejectRawObjectsAndOversizeItems(t *testing.T) {
	fields := []ParameterField{{Key: "audience", Type: "text_list", MaxItems: 2, MaxLength: 3}}
	for _, tc := range []struct {
		v     any
		valid bool
	}{{[]string{"abc", "界"}, true}, {[]any{"abc", "界"}, true}, {[]any{"abc", 3}, false}, {[]string{"a", "b", "c"}, false}, {[]string{"abcd"}, false}, {map[string]any{"provider": "payload"}, false}} {
		got, err := NormalizeParameters(fields, map[string]any{"audience": tc.v})
		if (err == nil) != tc.valid {
			t.Fatal(tc, got, err)
		}
	}
}
