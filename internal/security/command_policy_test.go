package security

import (
	"testing"

	"localgate/internal/config"
)

func TestValidateCommand(t *testing.T) {
	ws := config.Workspace{ID: "ws", CommandAllowlist: []string{"echo", "git", "go"}}
	if d := ValidateCommand("echo hi", ws); !d.Allowed {
		t.Fatalf("expected allowed")
	}
	if d := ValidateCommand("echo hi; rm -rf /", ws); d.Allowed || d.Reason != "shell_control" {
		t.Fatalf("expected denied for shell control, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	if d := ValidateCommand("rm -rf /", ws); d.Allowed {
		t.Fatalf("expected denied")
	}
	ws2 := config.Workspace{ID: "ws2"}
	if d := ValidateCommand("echo hi", ws2); d.Allowed {
		t.Fatalf("expected denied when no allowlist")
	}

	wsAll := config.Workspace{ID: "ws3", CommandAllowlist: []string{"*"}}
	if d := ValidateCommand("rm -rf /", wsAll); !d.Allowed {
		t.Fatalf("expected allowed with allowlist=*")
	}

	wsAllDeny := config.Workspace{ID: "ws4", CommandAllowlist: []string{"*"}, CommandDenylist: []string{"rm"}}
	if d := ValidateCommand("rm -rf /", wsAllDeny); d.Allowed || d.Reason != "in_denylist" {
		t.Fatalf("expected denied by denylist, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
}

func TestValidateCommandShellMetaFlags(t *testing.T) {
	base := config.Workspace{ID: "ws", CommandAllowlist: []string{"*"}}

	if d := ValidateCommand("echo hi | cat", base); d.Allowed || d.Reason != "shell_pipe" {
		t.Fatalf("expected shell_pipe denial, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	if d := ValidateCommand("echo hi > out.txt", base); d.Allowed || d.Reason != "shell_redirect" {
		t.Fatalf("expected shell_redirect denial, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	if d := ValidateCommand("echo hi && ls", base); d.Allowed || d.Reason != "shell_control" {
		t.Fatalf("expected shell_control denial, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	if d := ValidateCommand("sleep 1 &", base); d.Allowed || d.Reason != "shell_background" {
		t.Fatalf("expected shell_background denial, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	if d := ValidateCommand("echo $(pwd)", base); d.Allowed || d.Reason != "shell_substitution" {
		t.Fatalf("expected shell_substitution denial, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}

	pipeOK := base
	pipeOK.AllowShellPipe = true
	if d := ValidateCommand("echo hi | cat", pipeOK); !d.Allowed {
		t.Fatalf("expected pipe allowed when configured, got reason=%q", d.Reason)
	}

	redirectOK := base
	redirectOK.AllowShellRedirect = true
	if d := ValidateCommand("echo hi > out.txt", redirectOK); !d.Allowed {
		t.Fatalf("expected redirect allowed when configured, got reason=%q", d.Reason)
	}

	controlOK := base
	controlOK.AllowShellControl = true
	if d := ValidateCommand("echo hi && ls", controlOK); !d.Allowed {
		t.Fatalf("expected control allowed when configured, got reason=%q", d.Reason)
	}

	backgroundOK := base
	backgroundOK.AllowShellBackground = true
	if d := ValidateCommand("sleep 1 &", backgroundOK); !d.Allowed {
		t.Fatalf("expected background allowed when configured, got reason=%q", d.Reason)
	}

	subOK := base
	subOK.AllowShellSubstitution = true
	if d := ValidateCommand("echo $(pwd)", subOK); !d.Allowed {
		t.Fatalf("expected substitution allowed when configured, got reason=%q", d.Reason)
	}
}
