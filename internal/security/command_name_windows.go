//go:build windows

package security

import "strings"

func sameCommandToken(a, b string) bool { return strings.EqualFold(a, b) }
