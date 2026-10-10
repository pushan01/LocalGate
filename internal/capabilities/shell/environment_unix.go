//go:build !windows

package shell

import "os"

func normalizeEnvKey(key string) string { return key }

func trustedBaseEnvironment() map[string]string {
	base := map[string]string{}
	if value, ok := os.LookupEnv("PATH"); ok {
		base["PATH"] = value
	}
	return base
}
