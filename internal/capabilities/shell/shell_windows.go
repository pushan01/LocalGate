//go:build windows

package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func commandShell(command string) (string, []string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil, errors.New("command required")
	}
	shell, err := defaultWindowsShell()
	if err != nil {
		return "", nil, err
	}
	return windowsShellArgs(shell, command)
}

func shellCommand(shell string, login bool, command string) (string, []string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil, errors.New("cmd required")
	}
	if login {
		return "", nil, errors.New("login shell is not supported on windows")
	}
	resolvedShell, err := resolveTrustedShell(shell)
	if err != nil {
		return "", nil, err
	}
	return windowsShellArgs(resolvedShell, command)
}

func windowsShellArgs(shell, command string) (string, []string, error) {
	switch strings.ToLower(filepath.Base(shell)) {
	case "powershell.exe", "pwsh.exe":
		return shell, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command}, nil
	case "cmd.exe":
		return shell, []string{"/D", "/S", "/C", command}, nil
	default:
		return "", nil, fmt.Errorf("unsupported shell: %s", shell)
	}
}

func resolveTrustedShell(shell string) (string, error) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		return defaultWindowsShell()
	}
	if !filepath.IsAbs(shell) {
		return "", fmt.Errorf("shell must be an absolute trusted path: %s", shell)
	}
	resolved := filepath.Clean(shell)
	if evaluated, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = evaluated
	}
	for _, candidate := range trustedWindowsShells() {
		candidateResolved := filepath.Clean(candidate)
		if evaluated, err := filepath.EvalSymlinks(candidateResolved); err == nil {
			candidateResolved = evaluated
		}
		if strings.EqualFold(resolved, candidateResolved) {
			if info, err := os.Stat(candidateResolved); err == nil && !info.IsDir() {
				return candidateResolved, nil
			}
		}
	}
	return "", fmt.Errorf("shell is not a trusted system shell: %s", shell)
}

func defaultWindowsShell() (string, error) {
	for _, candidate := range trustedWindowsShells() {
		base := strings.ToLower(filepath.Base(candidate))
		if base != "powershell.exe" && base != "pwsh.exe" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return filepath.Clean(candidate), nil
		}
	}
	return "", errors.New("no trusted PowerShell executable found")
}

func trustedWindowsShells() []string {
	var shells []string
	if systemRoot := strings.TrimSpace(os.Getenv("SystemRoot")); systemRoot != "" {
		shells = append(shells,
			filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
			filepath.Join(systemRoot, "System32", "cmd.exe"),
		)
	}
	if programFiles := strings.TrimSpace(os.Getenv("ProgramFiles")); programFiles != "" {
		shells = append(shells, filepath.Join(programFiles, "PowerShell", "7", "pwsh.exe"))
	}
	return shells
}
