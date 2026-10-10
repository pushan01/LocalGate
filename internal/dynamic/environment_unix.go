//go:build !windows

package dynamic

import "os"

func platformProcessEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.TempDir(),
	}
}
