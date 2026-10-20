package core

import (
	"fmt"
	"time"
)

// ValidateTimeoutSeconds prevents duration overflow without imposing a product cap.
func ValidateTimeoutSeconds(seconds *int64) error {
	if seconds != nil && (*seconds <= 0 || *seconds > int64((1<<63-1)/time.Second)) {
		return fmt.Errorf("timeout_seconds must be a positive integer no greater than %d", int64((1<<63-1)/time.Second))
	}
	return nil
}
