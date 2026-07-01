package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrSymlink     = errors.New("symbolic links are not allowed")
	ErrSpecialFile = errors.New("special files are not allowed")
	ErrFileLimit   = errors.New("file count limit exceeded")
	ErrSizeLimit   = errors.New("snapshot size limit exceeded")
	ErrEscape      = errors.New("snapshot destination escape")
)

type Limits struct {
	MaxFiles      int
	MaxTotalBytes int64
	MaxFileBytes  int64
}

type Stats struct {
	FileCount  int
	TotalBytes int64
}

type Options struct {
	Mode func(source os.FileMode, isDir bool) os.FileMode
}

func Copy(ctx context.Context, source, destination string, limits Limits, options Options) (Stats, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limits.MaxFiles <= 0 || limits.MaxTotalBytes <= 0 || limits.MaxFileBytes <= 0 {
		return Stats{}, errors.New("snapshot limits must be positive")
	}
	stats := Stats{}
	err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrSymlink, path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if !WithinRoot(target, destination) {
			return ErrEscape
		}
		if info.IsDir() {
			return os.MkdirAll(target, normalizeMode(options, info.Mode().Perm(), true))
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %s", ErrSpecialFile, path)
		}
		if stats.FileCount+1 > limits.MaxFiles {
			return fmt.Errorf("%w: maximum %d", ErrFileLimit, limits.MaxFiles)
		}
		if info.Size() > limits.MaxFileBytes {
			return fmt.Errorf("%w: file %s exceeds %d bytes", ErrSizeLimit, relative, limits.MaxFileBytes)
		}
		remaining := limits.MaxTotalBytes - stats.TotalBytes
		if remaining <= 0 || info.Size() > remaining {
			return fmt.Errorf("%w: total exceeds %d bytes", ErrSizeLimit, limits.MaxTotalBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		maxCopy := limits.MaxFileBytes
		if remaining < maxCopy {
			maxCopy = remaining
		}
		written, err := copyRegularFile(ctx, path, target, normalizeMode(options, info.Mode().Perm(), false), maxCopy)
		if err != nil {
			return err
		}
		stats.FileCount++
		stats.TotalBytes += written
		return nil
	})
	return stats, err
}

func normalizeMode(options Options, source os.FileMode, isDir bool) os.FileMode {
	if options.Mode != nil {
		return options.Mode(source, isDir).Perm()
	}
	return source.Perm()
}

func copyRegularFile(ctx context.Context, source, target string, mode os.FileMode, maxBytes int64) (int64, error) {
	in, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	remove := true
	defer func() {
		_ = out.Close()
		if remove {
			_ = os.Remove(target)
		}
	}()
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		n, readErr := in.Read(buffer)
		if n > 0 {
			if total+int64(n) > maxBytes {
				return total, fmt.Errorf("%w: file %s exceeds %d bytes", ErrSizeLimit, filepath.Base(source), maxBytes)
			}
			written, writeErr := out.Write(buffer[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return total, readErr
		}
	}
	if err := out.Sync(); err != nil {
		return total, err
	}
	if err := out.Close(); err != nil {
		return total, err
	}
	remove = false
	return total, nil
}

func WithinRoot(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func PathsOverlap(first, second string) bool {
	return WithinRoot(first, second) || WithinRoot(second, first)
}
