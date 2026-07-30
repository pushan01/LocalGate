package fileio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrTargetChanged = errors.New("target changed during atomic write")
var ErrTooLarge = errors.New("content exceeds maximum size")

type AtomicWriteOptions struct {
	Mode          os.FileMode
	PreserveMode  bool
	CreateParents bool
	MaxBytes      int64
}

func WriteFileAtomic(path string, content []byte, opts AtomicWriteOptions) (int64, error) {
	return WriteReaderAtomic(path, bytes.NewReader(content), opts)
}

func WriteReaderAtomic(path string, src io.Reader, opts AtomicWriteOptions) (int64, error) {
	if src == nil {
		return 0, errors.New("source reader required")
	}
	path = filepath.Clean(path)
	if opts.Mode == 0 {
		opts.Mode = 0o644
	}

	initial, initialErr := os.Lstat(path)
	initialExists := initialErr == nil
	if initialErr != nil && !errors.Is(initialErr, os.ErrNotExist) {
		return 0, initialErr
	}
	if initialExists {
		if initial.Mode()&os.ModeSymlink != 0 {
			return 0, errors.New("target is a symlink")
		}
		if !initial.Mode().IsRegular() {
			return 0, errors.New("target is not a regular file")
		}
		if opts.PreserveMode {
			opts.Mode = initial.Mode().Perm()
		}
	}

	dir := filepath.Dir(path)
	if opts.CreateParents {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
	}
	tmp, err := os.CreateTemp(dir, ".localgate-write-*")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	reader := src
	if opts.MaxBytes > 0 {
		reader = io.LimitReader(src, opts.MaxBytes+1)
	}
	written, err := io.Copy(tmp, reader)
	if err != nil {
		return written, fmt.Errorf("write temporary file: %w", err)
	}
	if opts.MaxBytes > 0 && written > opts.MaxBytes {
		return written, fmt.Errorf("%w: %d > %d", ErrTooLarge, written, opts.MaxBytes)
	}
	if err := tmp.Chmod(opts.Mode.Perm()); err != nil {
		return written, err
	}
	if err := tmp.Sync(); err != nil {
		return written, err
	}
	if err := tmp.Close(); err != nil {
		return written, err
	}

	current, currentErr := os.Lstat(path)
	if initialExists {
		if currentErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(initial, current) {
			return written, ErrTargetChanged
		}
	} else if currentErr == nil {
		return written, ErrTargetChanged
	} else if !errors.Is(currentErr, os.ErrNotExist) {
		return written, currentErr
	}

	if initialExists {
		if err := os.Rename(tmpPath, path); err != nil {
			return written, err
		}
	} else {
		if err := os.Link(tmpPath, path); err != nil {
			if errors.Is(err, os.ErrExist) {
				return written, ErrTargetChanged
			}
			return written, err
		}
		_ = os.Remove(tmpPath)
	}
	if dirFile, openErr := os.Open(dir); openErr == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return written, nil
}
