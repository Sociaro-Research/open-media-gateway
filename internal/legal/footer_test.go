package legal

import (
	"strings"
	"testing"
)

func TestFooterValidationAndNeutralDefault(t *testing.T) {
	for _, holder := range []string{"", "Example company", "<img src=x onerror=alert(1)>", strings.Repeat("é", 100)} {
		if err := (Footer{CopyrightHolder: holder, Version: 1}).Validate(); err != nil {
			t.Fatal(holder, err)
		}
	}
	for _, f := range []Footer{{Version: 0}, {CopyrightHolder: strings.Repeat("é", 101), Version: 1}, {CopyrightHolder: "a\nb", Version: 1}, {CopyrightHolder: "a\u0085b", Version: 1}, {CopyrightHolder: string([]byte{255}), Version: 1}} {
		if f.Validate() == nil {
			t.Fatal("invalid holder accepted", f)
		}
	}
	if (Footer{}).DisplayHolder() != DefaultCopyrightHolder || (Footer{CopyrightHolder: "Custom operator"}).DisplayHolder() != "Custom operator" {
		t.Fatal("footer fallback")
	}
}

func TestFooterRejectsUnicodeLineSeparators(t *testing.T) {
	for _, separator := range []string{"\u2028", "\u2029"} {
		t.Run(separator, func(t *testing.T) {
			if (Footer{CopyrightHolder: "First" + separator + "Second", Version: 1}).Validate() == nil {
				t.Fatal("single-line copyright holder accepted a Unicode line separator")
			}
		})
	}
}
