package storage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const stagingOwnerName = ".owner.lock"

func stagingRoot() string { return filepath.Join(os.TempDir(), "gateway-s3-staging-v2") }

// Hold ownership through both download and S3 upload, including the interval
// when the local staging file has already been renamed to its final name.
func (s *S3) stagingDirectory() (string, func(), error) {
	if !temporaryLockSupported {
		dir, err := os.MkdirTemp("", "gateway-s3-*")
		return dir, func() { _ = os.RemoveAll(dir) }, err
	}
	parent := s.stagingPath
	if parent == "" {
		parent = stagingRoot()
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", nil, err
	}
	guard, err := os.OpenFile(parent, os.O_RDONLY|temporaryNoFollow, 0)
	if err != nil {
		return "", nil, err
	}
	defer guard.Close()
	if _, err := lockTemporary(guard, false); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(parent, ".s3-v2-*")
	if err != nil {
		return "", nil, err
	}
	owner, err := os.OpenFile(filepath.Join(dir, stagingOwnerName), os.O_RDWR|os.O_CREATE|os.O_EXCL|temporaryNoFollow, 0o600)
	if err == nil {
		_, err = lockTemporary(owner, false)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		if owner != nil {
			_ = owner.Close()
		}
		return "", nil, err
	}
	return dir, func() {
		_ = os.RemoveAll(dir)
		_ = owner.Close()
	}, nil
}

func removeStaging(root *os.Root, name string, result *TemporaryCleanup) error {
	directory, err := root.OpenRoot(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	owner, err := directory.OpenFile(stagingOwnerName, os.O_RDWR|temporaryNoFollow, 0)
	if err == nil {
		defer owner.Close()
		locked, lockErr := lockTemporary(owner, true)
		if lockErr != nil {
			return lockErr
		}
		if !locked {
			result.SkippedActive++
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Missing ownership means death between mkdir and owner-file creation. The
	// parent directory lock prevents observing a live writer in that window.
	var bytes int64
	files, entries := 0, 0
	err = fs.WalkDir(directory.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		if entries > 32 {
			return errors.New("unexpectedly large S3 staging directory; refusing cleanup")
		}
		if entry.Type().IsRegular() && name != stagingOwnerName {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			bytes += info.Size()
			files++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := root.RemoveAll(name); err != nil {
		return err
	}
	result.Removed += files
	result.RemovedDirectories++
	result.RemovedBytes += bytes
	return nil
}
