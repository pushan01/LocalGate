//go:build windows

package shell

import (
	"os"
	"strings"
)

func normalizeEnvKey(key string) string { return strings.ToUpper(key) }

func trustedBaseEnvironment() map[string]string {
	base := map[string]string{}
	for _, key := range []string{"PATH", "PATHEXT", "SystemRoot", "WINDIR", "ComSpec", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			base[normalizeEnvKey(key)] = value
		}
	}
	return base
}
