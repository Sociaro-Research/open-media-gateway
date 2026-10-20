//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package storage

import (
	"errors"
	"os"
	"syscall"
)

const temporaryLockSupported = true
const temporaryNoFollow = syscall.O_NOFOLLOW

func lockTemporary(file *os.File, nonblocking bool) (bool, error) {
	flags := syscall.LOCK_EX
	if nonblocking {
		flags |= syscall.LOCK_NB
	}
	for {
		err := syscall.Flock(int(file.Fd()), flags)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return err == nil, err
	}
}
