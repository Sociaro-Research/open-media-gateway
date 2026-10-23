// Package legal stores installation-wide plain-text document overrides.
package legal

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxTextBytes = 64 * 1024
const MaxRequestBytes = 6*MaxTextBytes + 1024 // JSON may escape every character.

type Document struct {
	Text    string `json:"text"`
	Version int64  `json:"version"`
}
type Store interface {
	LegalDocument(context.Context, string) (Document, error)
	SaveLegalDocument(context.Context, string, string, Document) (Document, error)
}

func ValidKind(kind string) bool { return kind == "privacy" || kind == "terms" }
func (d Document) Validate() error {
	if d.Version < 1 {
		return errors.New("version is required")
	}
	if !utf8.ValidString(d.Text) || len(d.Text) > MaxTextBytes || strings.IndexFunc(d.Text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 {
		return errors.New("document must be plain text up to 64 KiB without control characters")
	}
	return nil
}
