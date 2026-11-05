package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type resourceSourceValidationError struct {
	hint  string
	index *int
}

func (e *resourceSourceValidationError) Error() string {
	return core.ErrOptionsInvalid.Error() + ": " + e.hint
}
func (e *resourceSourceValidationError) Unwrap() error { return core.ErrOptionsInvalid }

func writeResourceValidationError(w http.ResponseWriter, code string, err error) bool {
	field, hint, ok := core.ResourceValidationDetails(err)
	if ok {
		if field == "" {
			field = "resource_intent"
		}
		writeAPIError(w, 400, code, field+" "+hint, &field)
		return true
	}
	var source *resourceSourceValidationError
	if errors.As(err, &source) {
		field = "reference_ids"
		if source.index != nil {
			field += fmt.Sprintf(".%d", *source.index)
		}
		writeAPIError(w, 400, code, source.hint, &field)
		return true
	}
	return false
}
