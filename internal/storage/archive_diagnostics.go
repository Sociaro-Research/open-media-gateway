package storage

import (
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/soundoverride"
	"os"
	"syscall"
)

const archiveRecoveryAction = ". The provider may already have completed the generation; check the original task before creating another paid generation."

// archiveDiagnostic is constructed only from local constants, numeric limits
// and the existing sanitized S3 classification, never an arbitrary error string.
type archiveDiagnostic struct {
	message string
	cause   error
}

func (e archiveDiagnostic) Error() string { return e.message }
func (e archiveDiagnostic) Unwrap() error { return e.cause }

func oversizedResult(limit int64) error {
	return archiveDiagnostic{message: fmt.Sprintf("provider result exceeds STORAGE_MAX_BYTES (%d bytes); ask an operator to review the configured result-size limit", limit)}
}

// PublicArchiveError is the final worker boundary for result-store plugins.
// Unknown filesystem/reader/plugin errors cannot become public task diagnostics.
func PublicArchiveError(err error) string {
	var safe archiveDiagnostic
	var source sourceOpenError
	message := "result archival failed; ask an operator to check result storage, provider download availability and worker health"
	switch {
	case errors.Is(err, soundoverride.ErrProcessing):
		message = soundoverride.ErrProcessing.Error()
	case errors.Is(err, ErrPrivateEndpoint):
		message = "result storage endpoint was blocked by outbound policy; ask an operator to review the exact trusted endpoint allowlist"
	case errors.Is(err, ErrEndpointDNS):
		message = "result storage hostname could not be resolved; ask an operator to verify DNS and connectivity from Gateway and worker"
	case errors.As(err, &safe):
		message = safe.Error()
	case errors.As(err, &source):
		message = source.Error() + "; ask an operator to check provider download availability and outbound connectivity"
	case errors.Is(err, os.ErrPermission):
		message = "result storage permission denied; ask an operator to check worker storage ownership and write permissions"
	case errors.Is(err, syscall.ENOSPC):
		message = "result storage is full; ask an operator to free space or expand the configured storage"

	}
	return message + archiveRecoveryAction
}
