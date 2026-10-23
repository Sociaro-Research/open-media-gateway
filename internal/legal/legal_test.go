package legal

import (
	"strings"
	"testing"
)

func TestDocumentValidation(t *testing.T) {
	for _, text := range []string{"", "Line one\n\nLine two", "<script>alert(1)</script>", strings.Repeat("x", MaxTextBytes)} {
		if err := (Document{Text: text, Version: 1}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []Document{{Version: 0}, {Text: "bad\x00", Version: 1}, {Text: strings.Repeat("é", MaxTextBytes), Version: 1}, {Text: string([]byte{255}), Version: 1}} {
		if d.Validate() == nil {
			t.Fatal("invalid accepted")
		}
	}
	if ValidKind("other") {
		t.Fatal("unknown document")
	}
}
