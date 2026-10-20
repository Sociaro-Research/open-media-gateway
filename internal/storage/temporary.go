package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type TemporaryCleanup struct {
	Scanned            int
	Removed            int
	RemovedDirectories int
	RemovedBytes       int64
	SkippedActive      int
	More               bool
}

type temporaryDirectory struct {
	name string
	file *os.File
}

type temporaryScanner struct {
	mu    sync.Mutex
	stack []temporaryDirectory
}

func (s *temporaryScanner) reset() {
	for _, directory := range s.stack {
		_ = directory.file.Close()
	}
	s.stack = nil
}

// The guard closes the create-before-lock race across processes. A sweep holds
// the same guard; each published temporary file is already individually locked.
func (s *Local) createTemporary(directory, kind string) (*os.File, error) {
	if !temporaryLockSupported {
		pattern := "." + kind + "-*"
		if kind == "media" {
			pattern += ".part"
		}
		return os.CreateTemp(directory, pattern)
	}
	guard, err := os.Open(s.root)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	if _, err := lockTemporary(guard, false); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(directory, "."+kind+"-v2-*.part")
	if err != nil {
		return nil, err
	}
	if _, err := lockTemporary(file, false); err != nil {
		_ = os.Remove(file.Name())
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (m *Manager) CleanupTemporaryFiles(ctx context.Context) (TemporaryCleanup, error) {
	result, localErr := m.local.CleanupTemporaryFiles(ctx)
	staging, stagingErr := m.staging.CleanupTemporaryFiles(ctx)
	result.Scanned += staging.Scanned
	result.Removed += staging.Removed
	result.RemovedDirectories += staging.RemovedDirectories
	result.RemovedBytes += staging.RemovedBytes
	result.SkippedActive += staging.SkippedActive
	result.More = result.More || staging.More
	return result, errors.Join(localErr, stagingErr)
}

// CleanupTemporaryFiles removes unpublished files whose writers no longer own
// their kernel locks. It allocates no disk space, reads directories incrementally
// and resumes large trees on the next pass. No database or published file changes.
func (s *Local) CleanupTemporaryFiles(ctx context.Context) (TemporaryCleanup, error) {
	return s.cleanupTemporaryFiles(ctx, 10_000, 5*time.Second)
}

func (s *Local) cleanupTemporaryFiles(ctx context.Context, limit int, budget time.Duration) (result TemporaryCleanup, err error) {
	if !temporaryLockSupported {
		return result, nil
	}
	s.temporary.mu.Lock()
	defer s.temporary.mu.Unlock()
	if err := ctx.Err(); err != nil {
		s.temporary.reset()
		return result, err
	}
	root, err := os.OpenRoot(s.root)
	if errors.Is(err, os.ErrNotExist) {
		s.temporary.reset()
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer root.Close()
	// Lock the directory inode: no guard-file allocation on a full filesystem.
	guard, err := root.Open(".")
	if err != nil {
		return result, err
	}
	defer guard.Close()
	locked, err := lockTemporary(guard, true)
	if err != nil || !locked {
		result.More = !locked
		return result, err
	}
	if len(s.temporary.stack) == 0 {
		file, err := root.Open(".")
		if err != nil {
			return result, err
		}
		s.temporary.stack = append(s.temporary.stack, temporaryDirectory{".", file})
	}
	deadline := time.Now().Add(budget)
	var failures []error
	for len(s.temporary.stack) > 0 && result.Scanned < limit && time.Now().Before(deadline) {
		if len(failures) >= 32 {
			break
		}
		if err := ctx.Err(); err != nil {
			s.temporary.reset()
			return result, err
		}
		directory := s.temporary.stack[len(s.temporary.stack)-1]
		entries, readErr := directory.file.ReadDir(1)
		if len(entries) == 0 {
			_ = directory.file.Close()
			s.temporary.stack = s.temporary.stack[:len(s.temporary.stack)-1]
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				failures = append(failures, readErr)
			}
			continue
		}
		result.Scanned++
		entry := entries[0]
		name := filepath.Join(directory.name, entry.Name())
		if s.stagingOnly {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), ".s3-v2-") && temporaryNumber(strings.TrimPrefix(entry.Name(), ".s3-v2-")) {
				if err := removeStaging(root, name, &result); err != nil {
					failures = append(failures, err)
				}
			}
			continue
		}
		if entry.IsDir() {
			if len(s.temporary.stack) >= 32 {
				failures = append(failures, errors.New("temporary cleanup directory depth exceeds 32"))
				continue
			}
			file, openErr := root.OpenFile(name, os.O_RDONLY|temporaryNoFollow, 0)
			if openErr == nil {
				s.temporary.stack = append(s.temporary.stack, temporaryDirectory{name, file})
			} else if !errors.Is(openErr, os.ErrNotExist) {
				failures = append(failures, openErr)
			}
			continue
		}
		managed, legacy := temporaryName(entry.Name())
		if !managed || !entry.Type().IsRegular() {
			continue
		}
		if err := s.removeTemporary(root, name, legacy, &result); err != nil {
			failures = append(failures, err)
		}
	}
	result.More = len(s.temporary.stack) != 0
	return result, errors.Join(failures...)
}

func temporaryName(name string) (managed, legacy bool) {
	for _, kind := range []string{"media", "reference"} {
		if suffix, ok := strings.CutPrefix(name, "."+kind+"-v2-"); ok && strings.HasSuffix(suffix, ".part") && temporaryNumber(strings.TrimSuffix(suffix, ".part")) {
			return true, false
		}
		if suffix, ok := strings.CutPrefix(name, "."+kind+"-"); ok {
			if kind == "media" && !strings.HasSuffix(suffix, ".part") {
				continue
			}
			if temporaryNumber(strings.TrimSuffix(suffix, ".part")) {
				return true, true
			}
		}
	}
	return false, false
}

func temporaryNumber(value string) bool {
	if value == "" {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func (s *Local) removeTemporary(root *os.Root, name string, legacy bool, result *TemporaryCleanup) error {
	file, err := root.OpenFile(name, os.O_RDWR|temporaryNoFollow, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	locked, err := lockTemporary(file, true)
	if err != nil {
		return err
	}
	if !locked {
		result.SkippedActive++
		return nil
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return err
	}
	// Older releases did not lock their temporary files. Leave a conservative
	// upgrade window; old workers must be stopped before introducing this version.
	legacyAge := max(24*time.Hour, s.client.Timeout+time.Hour)
	if legacy && time.Since(info.ModTime()) < legacyAge {
		return nil
	}
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) || !current.Mode().IsRegular() {
		return nil
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove temporary file: %w", err)
	}
	result.Removed++
	result.RemovedBytes += info.Size()
	return nil
}
