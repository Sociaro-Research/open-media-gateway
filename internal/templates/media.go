package templates

import (
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

type InputMedia = referencevalidation.File

func ValidateFiles(model core.MediaModel, files []InputMedia, complete bool) error {
	if err := referencevalidation.ValidateFiles(model, files, complete); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}
func ValidateRequestFiles(model core.MediaModel, options core.MediaOptions, files []InputMedia, complete bool) error {
	if err := referencevalidation.ValidateRequestFiles(model, options, files, complete); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}
