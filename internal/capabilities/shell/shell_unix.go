//go:build !windows

package shell

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func commandShell(command string) (string, []string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil, errors.New("command required")
	}
	return "/bin/sh", []string{"-c", command}, nil
}

func shellCommand(shell string, login bool, command string) (string, []string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil, errors.New("cmd required")
	}
	resolvedShell, err := resolveTrustedShell(shell)
	if err != nil {
		return "", nil, err
	}
	switch filepath.Base(resolvedShell) {
	case "sh":
		if login {
			return "", nil, fmt.Errorf("login shell not supported for %s", resolvedShell)
		}
		return resolvedShell, []string{"-c", command}, nil
	case "bash", "zsh":
		if login {
			return resolvedShell, []string{"-lc", command}, nil
		}
		return resolvedShell, []string{"-c", command}, nil
	default:
		return "", nil, fmt.Errorf("unsupported shell: %s", resolvedShell)
	}
}

func resolveTrustedShell(shell string) (string, error) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		shell = "/bin/sh"
	}
	if !filepath.IsAbs(shell) {
		return "", fmt.Errorf("shell must be an absolute trusted path: %s", shell)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(shell))
	if err != nil {
		return "", fmt.Errorf("resolve shell %s: %w", shell, err)
	}
	trusted := []string{
		"/bin/sh", "/bin/bash", "/bin/zsh",
		"/usr/bin/sh", "/usr/bin/bash", "/usr/bin/zsh",
	}
	for _, candidate := range trusted {
		candidateResolved, candidateErr := filepath.EvalSymlinks(candidate)
		if candidateErr == nil && resolved == candidateResolved {
			return candidateResolved, nil
		}
	}
	return "", fmt.Errorf("shell is not a trusted system shell: %s", shell)
}
