package providerconfig

import (
	"bytes"
	"context"
	"errors"
	"image/png"
)

const MaxLogoBytes = 1 << 20
const MaxLogoDimension = 2048

// Logos are stored separately so replacing artwork cannot overwrite credentials.
type LogoRepository interface {
	Logo(context.Context, string) ([]byte, error)
	SaveLogo(context.Context, string, []byte) error
}

func ValidateLogo(data []byte) error {
	// Type before size, so a large JPG is told PNG only (GW-350).
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return errors.New("logo must be a PNG image; other formats are unsupported")
	}
	if len(data) > MaxLogoBytes {
		return errors.New("logo must be at most ≈1.05 MB (1048576 bytes)")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid PNG image; choose a complete, valid PNG file")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > MaxLogoDimension || config.Height > MaxLogoDimension {
		return errors.New("PNG dimensions must be between 1 and 2048 pixels per side")
	}
	_, err = png.Decode(bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid PNG image; choose a complete, valid PNG file")
	}
	return nil
}
