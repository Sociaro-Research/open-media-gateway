package api

import (
	"context"
	"io"
	"sync"
)

// A delegated read must actively interrupt an opened storage reader when its
// authority or connection context changes. Existing local/S3 readers support
// concurrent Close; cancellation alone would not unblock a stalled ReadSeeker.
func closeCompanyMedia(ctx context.Context, reader io.Closer) func() {
	if _, delegated := companyCustomerFromContext(ctx); !delegated {
		return func() { _ = reader.Close() }
	}
	var once sync.Once
	closeReader := func() { once.Do(func() { _ = reader.Close() }) }
	stop := context.AfterFunc(ctx, closeReader)
	return func() { stop(); closeReader() }
}
