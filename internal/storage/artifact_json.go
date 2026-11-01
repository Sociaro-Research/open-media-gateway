package storage

import (
	"bufio"
	"errors"
	"io"
)

// Validate the staged JSON grammar without decoding values or retaining a JSON
// string/array in memory. Buffers stay fixed-size even for a single huge string.
func validateArtifactJSON(r *bufio.Reader) error {
	p := artifactJSON{r: r}
	if err := p.value(0); err != nil {
		return err
	}
	p.space()
	if _, err := r.Peek(1); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

type artifactJSON struct{ r *bufio.Reader }

var errArtifactJSON = errors.New("invalid JSON")

func (p *artifactJSON) peek() byte {
	b, err := p.r.Peek(1)
	if err != nil {
		return 0
	}
	return b[0]
}
func (p *artifactJSON) space() {
	for {
		switch p.peek() {
		case ' ', '\t', '\r', '\n':
			_, _ = p.r.ReadByte()
		default:
			return
		}
	}
}
func (p *artifactJSON) take(expected byte) error {
	b, err := p.r.ReadByte()
	if err != nil || b != expected {
		return errArtifactJSON
	}
	return nil
}
func (p *artifactJSON) value(depth int) error {
	if depth > 10000 {
		return errArtifactJSON
	}
	p.space()
	switch p.peek() {
	case '"':
		return p.string()
	case '{':
		if depth >= 10000 {
			return errArtifactJSON
		}
		_, _ = p.r.ReadByte()
		p.space()
		if p.peek() == '}' {
			_, _ = p.r.ReadByte()
			return nil
		}
		for {
			if err := p.string(); err != nil {
				return err
			}
			p.space()
			if err := p.take(':'); err != nil {
				return err
			}
			if err := p.value(depth + 1); err != nil {
				return err
			}
			p.space()
			if p.peek() == '}' {
				_, _ = p.r.ReadByte()
				return nil
			}
			if err := p.take(','); err != nil {
				return err
			}
			p.space()
		}
	case '[':
		if depth >= 10000 {
			return errArtifactJSON
		}
		_, _ = p.r.ReadByte()
		p.space()
		if p.peek() == ']' {
			_, _ = p.r.ReadByte()
			return nil
		}
		for {
			if err := p.value(depth + 1); err != nil {
				return err
			}
			p.space()
			if p.peek() == ']' {
				_, _ = p.r.ReadByte()
				return nil
			}
			if err := p.take(','); err != nil {
				return err
			}
		}
	case 't':
		return p.literal("true")
	case 'f':
		return p.literal("false")
	case 'n':
		return p.literal("null")
	default:
		return p.number()
	}
}
func (p *artifactJSON) literal(value string) error {
	for i := range value {
		if err := p.take(value[i]); err != nil {
			return err
		}
	}
	return nil
}
func (p *artifactJSON) string() error {
	if err := p.take('"'); err != nil {
		return err
	}
	for {
		b, err := p.r.ReadByte()
		if err != nil {
			return errArtifactJSON
		}
		if b == '"' {
			return nil
		}
		if b < 0x20 {
			return errArtifactJSON
		}
		if b != '\\' {
			continue
		}
		b, err = p.r.ReadByte()
		if err != nil {
			return errArtifactJSON
		}
		switch b {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			for i := 0; i < 4; i++ {
				h, err := p.r.ReadByte()
				if err != nil || !((h >= '0' && h <= '9') || (h >= 'a' && h <= 'f') || (h >= 'A' && h <= 'F')) {
					return errArtifactJSON
				}
			}
		default:
			return errArtifactJSON
		}
	}
}
func (p *artifactJSON) digits() bool {
	count := 0
	for b := p.peek(); b >= '0' && b <= '9'; b = p.peek() {
		_, _ = p.r.ReadByte()
		count++
	}
	return count > 0
}
func (p *artifactJSON) number() error {
	if p.peek() == '-' {
		_, _ = p.r.ReadByte()
	}
	b := p.peek()
	if b == '0' {
		_, _ = p.r.ReadByte()
	} else if b >= '1' && b <= '9' {
		p.digits()
	} else {
		return errArtifactJSON
	}
	if p.peek() == '.' {
		_, _ = p.r.ReadByte()
		if !p.digits() {
			return errArtifactJSON
		}
	}
	if p.peek() == 'e' || p.peek() == 'E' {
		_, _ = p.r.ReadByte()
		if p.peek() == '+' || p.peek() == '-' {
			_, _ = p.r.ReadByte()
		}
		if !p.digits() {
			return errArtifactJSON
		}
	}
	return nil
}
