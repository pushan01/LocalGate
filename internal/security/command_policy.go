package security

import (
	"strings"

	"localgate/internal/config"
)

type CmdDecision struct {
	Allowed bool
	Reason  string
	Command string
}

// ValidateCommand applies a simple allowlist policy against the first argv token.
func ValidateCommand(cmd string, ws config.Root) CmdDecision {
	trim := strings.TrimSpace(cmd)
	if trim == "" {
		return CmdDecision{Allowed: false, Reason: "empty"}
	}
	if !ws.AllowShellControl {
		if strings.ContainsAny(trim, ";\n\r") || strings.Contains(trim, "&&") || strings.Contains(trim, "||") {
			return CmdDecision{Allowed: false, Reason: "shell_control"}
		}
	}
	if !ws.AllowShellPipe && containsStandalone(trim, '|', "||") {
		return CmdDecision{Allowed: false, Reason: "shell_pipe"}
	}
	if !ws.AllowShellBackground && containsStandalone(trim, '&', "&&") {
		return CmdDecision{Allowed: false, Reason: "shell_background"}
	}
	if !ws.AllowShellSubstitution && (strings.Contains(trim, "`") || strings.Contains(trim, "$(")) {
		return CmdDecision{Allowed: false, Reason: "shell_substitution"}
	}
	if !ws.AllowShellRedirect && (strings.Contains(trim, "<") || strings.Contains(trim, ">")) {
		return CmdDecision{Allowed: false, Reason: "shell_redirect"}
	}
	first := firstToken(trim)

	// Exact-match denylist on the first token (takes precedence over allowlist).
	for _, deny := range ws.CommandDenylist {
		if deny != "" && sameCommandToken(first, deny) {
			return CmdDecision{Allowed: false, Reason: "in_denylist", Command: trim}
		}
	}

	if len(ws.CommandAllowlist) == 0 {
		return CmdDecision{Allowed: false, Reason: "no_allowlist"}
	}
	for _, allow := range ws.CommandAllowlist {
		// Special case: "*" means allow any command name (still subject to shell meta checks and denylist).
		if allow == "*" {
			return CmdDecision{Allowed: true, Command: trim}
		}
		if sameCommandToken(first, allow) {
			return CmdDecision{Allowed: true, Command: trim}
		}
	}
	return CmdDecision{Allowed: false, Reason: "not_in_allowlist", Command: trim}
}

func ValidateArgvExecutable(executable string, ws config.Root) CmdDecision {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return CmdDecision{Allowed: false, Reason: "empty"}
	}
	for _, deny := range ws.CommandDenylist {
		if deny != "" && sameCommandToken(executable, deny) {
			return CmdDecision{Allowed: false, Reason: "in_denylist", Command: executable}
		}
	}
	if len(ws.CommandAllowlist) == 0 {
		return CmdDecision{Allowed: false, Reason: "no_allowlist"}
	}
	for _, allow := range ws.CommandAllowlist {
		if allow == "*" || sameCommandToken(executable, allow) {
			return CmdDecision{Allowed: true, Command: executable}
		}
	}
	return CmdDecision{Allowed: false, Reason: "not_in_allowlist", Command: executable}
}

func firstToken(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func containsStandalone(s string, ch byte, doubled string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ch {
			continue
		}
		if doubled != "" && i+1 < len(s) && s[i:i+2] == doubled {
			i++
			continue
		}
		return true
	}
	return false
}
