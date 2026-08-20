package meta

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type skillFrontmatter struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
}

const skillPromptDescriptionMaxRunes = 180

//go:embed builtin_skills/*/SKILL.md
var builtinSkillsFS embed.FS

type skill struct {
	Name                   string
	Description            string
	FilePath               string
	Source                 string
	Content                string
	DisableModelInvocation bool
}

func loadAllSkills(cwd string) ([]string, []skill) {
	var allSkills []skill
	seen := make(map[string]bool)
	var roots []string

	addSkills := func(dir, source string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		roots = append(roots, dir)
		for _, s := range loadSkills(dir) {
			s.Source = source
			if !seen[s.Name] {
				seen[s.Name] = true
				allSkills = append(allSkills, s)
			}
		}
	}

	if homeDir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDir) != "" {
		addSkills(filepath.Join(homeDir, ".agents", "skills"), "global")
	}
	if strings.TrimSpace(cwd) != "" {
		addSkills(filepath.Join(cwd, ".agents", "skills"), "workspace")
	}

	return roots, allSkills
}

func loadSkills(dir string) []skill {
	return loadSkillsInternal(dir, true)
}

func loadSkillsInternal(dir string, includeRootFiles bool) []skill {
	var skills []skill
	entries, err := os.ReadDir(dir)
	if err != nil {
		return skills
	}

	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() == "SKILL.md" {
			path := filepath.Join(dir, entry.Name())
			if s := parseSkillFile(path); s != nil {
				skills = append(skills, *s)
			}
			return skills
		}
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if isFollowLinkDir(path, entry) {
			if entry.Name() == "node_modules" {
				continue
			}
			skills = append(skills, loadSkillsInternal(path, false)...)
			continue
		}
		if includeRootFiles && strings.HasSuffix(entry.Name(), ".md") {
			if s := parseSkillFile(path); s != nil {
				skills = append(skills, *s)
			}
		}
	}

	return skills
}

func isFollowLinkDir(path string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func parseSkillFile(path string) *skill {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseSkillContent(content, filepath.Base(filepath.Dir(path)), path)
}

func parseSkillContent(content []byte, fallbackName, path string) *skill {

	content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
	parts := bytes.SplitN(content, []byte("---\n"), 3)
	if len(parts) < 3 || len(bytes.TrimSpace(parts[0])) > 0 {
		return nil
	}

	var fm skillFrontmatter
	if err := yaml.Unmarshal(parts[1], &fm); err != nil {
		return nil
	}

	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}

	return &skill{
		Name:                   name,
		Description:            strings.TrimSpace(fm.Description),
		FilePath:               path,
		Content:                strings.TrimSpace(string(parts[2])),
		DisableModelInvocation: fm.DisableModelInvocation,
	}
}

func visibleSkillsForPrompt(cwd string) ([]string, []map[string]any) {
	roots, loaded := loadAllSkills(cwd)
	visible := make([]map[string]any, 0, len(loaded))
	reserved := builtinSkillNames()
	for _, s := range loaded {
		if s.DisableModelInvocation {
			continue
		}
		if _, ok := reserved[s.Name]; ok {
			continue
		}
		visible = append(visible, map[string]any{
			"name":        s.Name,
			"description": summarizeSkillDescription(s.Description),
			"location":    s.FilePath,
			"source":      s.Source,
			"loader":      "read_file",
		})
	}
	slices.SortFunc(visible, func(a, b map[string]any) int {
		return strings.Compare(fmtString(a["name"]), fmtString(b["name"]))
	})
	return roots, visible
}

func loadBuiltinSkills() []skill {
	paths, err := fs.Glob(builtinSkillsFS, "builtin_skills/*/SKILL.md")
	if err != nil {
		return nil
	}
	slices.Sort(paths)
	loaded := make([]skill, 0, len(paths))
	for _, path := range paths {
		content, err := builtinSkillsFS.ReadFile(path)
		if err != nil {
			continue
		}
		fallback := filepath.Base(filepath.Dir(path))
		s := parseSkillContent(content, fallback, "builtin://"+fallback+"/SKILL.md")
		if s == nil {
			continue
		}
		s.Source = "builtin"
		loaded = append(loaded, *s)
	}
	return loaded
}

func builtinSkillByName(name string) (skill, bool) {
	name = strings.TrimSpace(name)
	for _, s := range loadBuiltinSkills() {
		if s.Name == name {
			return s, true
		}
	}
	return skill{}, false
}

func builtinSkillNames() map[string]struct{} {
	names := make(map[string]struct{})
	for _, s := range loadBuiltinSkills() {
		names[s.Name] = struct{}{}
	}
	return names
}

func visibleBuiltinSkillsForPrompt(hasTools map[string]any, opts StartupPromptOptions) []map[string]any {
	visible := make([]map[string]any, 0)
	for _, s := range loadBuiltinSkills() {
		if s.DisableModelInvocation || !builtinSkillRelevant(s.Name, hasTools, opts) {
			continue
		}
		visible = append(visible, map[string]any{
			"name":        s.Name,
			"description": summarizeSkillDescription(s.Description),
			"location":    s.FilePath,
			"source":      "builtin",
			"loader":      "get_skill",
		})
	}
	slices.SortFunc(visible, func(a, b map[string]any) int {
		return strings.Compare(fmtString(a["name"]), fmtString(b["name"]))
	})
	return visible
}

func builtinSkillRelevant(name string, hasTools map[string]any, opts StartupPromptOptions) bool {
	switch name {
	case "localgate-deployment":
		return hasAnyVisibleTool(hasTools,
			"deploy_tool", "list_deployments", "delete_deployment", "set_deployment_access",
			"get_deployment", "get_deployment_logs", "start_deployment", "stop_deployment",
			"restart_deployment", "list_deployment_releases", "rollback_deployment",
		)
	case "localgate-file-transfer":
		return hasAnyVisibleTool(hasTools, "upload_file", "export_file", "create_upload_token", "create_download_token")
	case "localgate-filesystem":
		return hasAnyVisibleTool(hasTools, "write_file", "list_directory", "search_files", "run_argv")
	case "localgate-task-session":
		return hasAnyVisibleTool(hasTools,
			"run_command", "exec_command", "write_stdin", "read_thread_terminal", "close_session",
			"task_status", "task_logs", "task_cancel", "task_result", "wait_task_finish",
		) || !opts.ToolHTTPMode
	case "localgate-subagents":
		return hasAnyVisibleTool(hasTools, "subagent_spawn", "subagent_send", "subagent_poll", "subagent_wait", "subagent_get", "subagent_list", "subagent_close", "subagent_archive", "subagent_artifact_get")
	case "localgate-notification":
		return hasAnyVisibleTool(hasTools, "send_notification")
	case "localgate-tool-http":
		return opts.ToolHTTPMode
	case "localgate-mcp-relay":
		return !opts.ToolHTTPMode && len(opts.MCPServers) > 0
	default:
		return true
	}
}

func mergeSkillIndexes(builtin, external []map[string]any) []map[string]any {
	merged := make([]map[string]any, 0, len(builtin)+len(external))
	seen := make(map[string]bool, len(builtin)+len(external))
	for _, group := range [][]map[string]any{builtin, external} {
		for _, item := range group {
			name := fmtString(item["name"])
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			merged = append(merged, item)
		}
	}
	slices.SortFunc(merged, func(a, b map[string]any) int {
		return strings.Compare(fmtString(a["name"]), fmtString(b["name"]))
	})
	return merged
}

func summarizeSkillDescription(description string) string {
	summary := strings.Join(strings.Fields(description), " ")
	runes := []rune(summary)
	if len(runes) <= skillPromptDescriptionMaxRunes {
		return summary
	}
	const suffix = "..."
	limit := skillPromptDescriptionMaxRunes - len([]rune(suffix))
	return strings.TrimSpace(string(runes[:limit])) + suffix
}
