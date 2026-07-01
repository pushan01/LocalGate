package dynamic

import (
	"context"
	"errors"
	"os"

	deploysnapshot "localgate/internal/deployment/snapshot"
)

type snapshotStats struct {
	FileCount  int
	TotalBytes int64
}

func copySnapshot(ctx context.Context, source, destination string, maxFiles int, maxTotalBytes, maxFileBytes int64) (snapshotStats, error) {
	stats, err := deploysnapshot.Copy(ctx, source, destination, deploysnapshot.Limits{
		MaxFiles: maxFiles, MaxTotalBytes: maxTotalBytes, MaxFileBytes: maxFileBytes,
	}, deploysnapshot.Options{Mode: func(mode os.FileMode, isDir bool) os.FileMode {
		if isDir {
			return 0o755
		}
		result := mode.Perm() & 0o755
		if result&0o400 == 0 {
			result |= 0o400
		}
		return result
	}})
	if err != nil {
		switch {
		case errors.Is(err, deploysnapshot.ErrSymlink), errors.Is(err, deploysnapshot.ErrSpecialFile), errors.Is(err, deploysnapshot.ErrEscape):
			return snapshotStats{}, errorf("deployment_source_invalid", "%v", err)
		case errors.Is(err, deploysnapshot.ErrFileLimit), errors.Is(err, deploysnapshot.ErrSizeLimit):
			return snapshotStats{}, errorf("deployment_snapshot_failed", "%v", err)
		default:
			return snapshotStats{}, err
		}
	}
	return snapshotStats{FileCount: stats.FileCount, TotalBytes: stats.TotalBytes}, nil
}

func withinRoot(path, root string) bool      { return deploysnapshot.WithinRoot(path, root) }
func pathsOverlap(first, second string) bool { return deploysnapshot.PathsOverlap(first, second) }
