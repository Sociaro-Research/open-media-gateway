package legal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const DefaultCopyrightHolder = "[Your company name]"
const MaxCopyrightHolderBytes = 200
const MaxFooterRequestBytes = 6*MaxCopyrightHolderBytes + 1024

// Footer is independent of invoice identity, appearance and document drafts.
type Footer struct {
	CopyrightHolder string `json:"copyright_holder"`
	Version         int64  `json:"version"`
}

type FooterReader interface {
	LegalFooter(context.Context) (Footer, error)
}
type FooterStore interface {
	FooterReader
	SaveLegalFooter(context.Context, string, Footer) (Footer, error)
}

func (f Footer) Validate() error {
	if f.Version < 1 {
		return errors.New("version must be positive; load the footer settings and submit their current version")
	}
	if !utf8.ValidString(f.CopyrightHolder) {
		return errors.New("copyright_holder must use valid UTF-8; correct the text encoding before saving")
	}
	if len(f.CopyrightHolder) > MaxCopyrightHolderBytes {
		return fmt.Errorf("copyright_holder is %d UTF-8 bytes; the limit is %d bytes; shorten the text before saving", len(f.CopyrightHolder), MaxCopyrightHolderBytes)
	}
	if strings.IndexFunc(f.CopyrightHolder, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
	}) >= 0 {
		return errors.New("copyright_holder must be a single line; remove control characters and line separators before saving")
	}
	return nil
}

func (f Footer) DisplayHolder() string {
	if strings.TrimSpace(f.CopyrightHolder) == "" {
		return DefaultCopyrightHolder
	}
	return f.CopyrightHolder
}
