//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package storage

import "os"

// Preserve ordinary writes on other platforms, but never sweep without locks.
const temporaryLockSupported = false
const temporaryNoFollow = 0

func lockTemporary(_ *os.File, _ bool) (bool, error) { return false, nil }
