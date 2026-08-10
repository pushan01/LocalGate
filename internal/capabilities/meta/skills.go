package meta

import (
	"bytes"
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

type skill struct {
	Name                   string
	Description            string
	FilePath               string
	DisableModelInvocation bool
}

func loadAllSkills(cwd string) ([]string, []skill) {
	var allSkills []skill
	seen := make(map[string]bool)
	var roots []string

	addSkills := func(dir string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		roots = append(roots, dir)
		for _, s := range loadSkills(dir) {
			if !seen[s.Name] {
				seen[s.Name] = true
				allSkills = append(allSkills, s)
			}
		}
	}

	if homeDir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDir) != "" {
		addSkills(filepath.Join(homeDir, ".agents", "skills"))
	}
	if strings.TrimSpace(cwd) != "" {
		addSkills(filepath.Join(cwd, ".agents", "skills"))
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
		name = filepath.Base(filepath.Dir(path))
	}

	return &skill{
		Name:                   name,
		Description:            strings.TrimSpace(fm.Description),
		FilePath:               path,
		DisableModelInvocation: fm.DisableModelInvocation,
	}
}

func visibleSkillsForPrompt(cwd string) ([]string, []map[string]any) {
	roots, loaded := loadAllSkills(cwd)
	visible := make([]map[string]any, 0, len(loaded))
	for _, s := range loaded {
		if s.DisableModelInvocation {
			continue
		}
		visible = append(visible, map[string]any{
			"name":        s.Name,
			"description": summarizeSkillDescription(s.Description),
			"location":    s.FilePath,
		})
	}
	slices.SortFunc(visible, func(a, b map[string]any) int {
		return strings.Compare(fmtString(a["name"]), fmtString(b["name"]))
	})
	return roots, visible
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
