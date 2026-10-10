//go:build !windows

package security

func sameCommandToken(a, b string) bool { return a == b }
