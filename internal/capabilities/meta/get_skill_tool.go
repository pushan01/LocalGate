package meta

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"localgate/internal/config"
	"localgate/internal/core"
)

type GetSkillTool struct {
	Dynamic config.DynamicHosting
}

func NewGetSkillTool(dynamic config.DynamicHosting) core.Tool {
	return GetSkillTool{Dynamic: dynamic}
}

func (GetSkillTool) Name() string {
	return "get_skill"
}

func (GetSkillTool) Description() string {
	return "Load one embedded LocalGate built-in skill by name when startup instructions indicate it matches the task."
}

func (GetSkillTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Built-in skill name from get_startup_instructions, for example localgate-deployment.",
			},
		},
		"required": []string{"name"},
	}
}

func (GetSkillTool) ToolAnnotations() map[string]any {
	return map[string]any{"readOnlyHint": true}
}

func (t GetSkillTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = ic
	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("skill name is required")
	}
	s, ok := builtinSkillByName(name)
	if !ok {
		return nil, errors.New("built-in skill not found")
	}
	content := s.Content
	result := map[string]any{
		"name":        s.Name,
		"description": s.Description,
		"source":      "builtin",
	}
	if s.Name == "localgate-deployment" {
		deploymentContext := dynamicDeploymentSkillContext(t.Dynamic)
		content = appendDynamicDeploymentContext(content, deploymentContext)
		result["context"] = map[string]any{"dynamic_runtime": deploymentContext}
	}
	result["content"] = content
	return result, nil
}

func dynamicDeploymentSkillContext(cfg config.DynamicHosting) map[string]any {
	executables := make([]map[string]any, 0, len(cfg.AllowedExecutables))
	for _, configured := range cfg.AllowedExecutables {
		configured = strings.TrimSpace(configured)
		if configured == "" {
			continue
		}
		item := map[string]any{"configured": configured, "available": false}
		if resolved, err := exec.LookPath(configured); err == nil {
			if absolute, absErr := filepath.Abs(resolved); absErr == nil {
				resolved = absolute
			}
			if evaluated, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
				resolved = evaluated
			}
			item["available"] = true
			item["resolved"] = filepath.Clean(resolved)
		}
		executables = append(executables, item)
	}
	envAllowlist := append([]string(nil), cfg.EnvAllowlist...)
	slices.Sort(envAllowlist)
	return map[string]any{
		"enabled":                 cfg.Enabled,
		"allowed_executables":     executables,
		"env_allowlist":           envAllowlist,
		"startup_timeout_seconds": cfg.StartupTimeoutSeconds,
		"restart_policy":          cfg.RestartPolicy,
		"require_approval":        cfg.RequireApproval,
	}
}

func appendDynamicDeploymentContext(content string, runtime map[string]any) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(content))
	b.WriteString("\n\n## Current instance: dynamic runtime\n\n")
	enabled, _ := runtime["enabled"].(bool)
	if !enabled {
		b.WriteString("- Dynamic hosting is disabled on this LocalGate instance. Do not submit `type=dynamic` deployments.\n")
		return b.String()
	}
	b.WriteString("- Dynamic hosting is enabled.\n")
	b.WriteString("- `runtime.argv[0]` must use one of the configured executable entries below exactly; do not guess `npm`, `pnpm`, `uvicorn`, `gunicorn`, `go`, or a project-local binary unless it is listed.\n")
	if executables, ok := runtime["allowed_executables"].([]map[string]any); ok && len(executables) > 0 {
		for _, executable := range executables {
			configured := fmt.Sprint(executable["configured"])
			available, _ := executable["available"].(bool)
			if available {
				resolved := fmt.Sprint(executable["resolved"])
				if resolved != "" && resolved != configured {
					fmt.Fprintf(&b, "  - `%s` (available; resolves to `%s`)\n", markdownCode(configured), markdownCode(resolved))
				} else {
					fmt.Fprintf(&b, "  - `%s` (available)\n", markdownCode(configured))
				}
				continue
			}
			fmt.Fprintf(&b, "  - `%s` (configured but not currently resolvable; avoid until the LocalGate runtime environment is fixed)\n", markdownCode(configured))
		}
	}
	if envAllowlist, ok := runtime["env_allowlist"].([]string); ok {
		if len(envAllowlist) == 0 {
			b.WriteString("- No custom `runtime.env` keys are allowed on this instance.\n")
		} else {
			quoted := make([]string, 0, len(envAllowlist))
			for _, key := range envAllowlist {
				quoted = append(quoted, "`"+markdownCode(key)+"`")
			}
			fmt.Fprintf(&b, "- Allowed custom `runtime.env` keys: %s.\n", strings.Join(quoted, ", "))
		}
	}
	if timeout, ok := runtime["startup_timeout_seconds"].(int); ok && timeout > 0 {
		fmt.Fprintf(&b, "- Default startup timeout: %d seconds.\n", timeout)
	}
	if restartPolicy := strings.TrimSpace(fmt.Sprint(runtime["restart_policy"])); restartPolicy != "" {
		fmt.Fprintf(&b, "- Default restart policy: `%s`.\n", markdownCode(restartPolicy))
	}
	if requireApproval, _ := runtime["require_approval"].(bool); requireApproval {
		b.WriteString("- Dynamic deployment mutations require approval on this instance.\n")
	}
	return strings.TrimSpace(b.String())
}

func markdownCode(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
