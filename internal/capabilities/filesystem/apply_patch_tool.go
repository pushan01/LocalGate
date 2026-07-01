package filesystem

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/security"
)

type applyPatchTool struct{ fsToolBase }

type patchOp struct {
	action string
	path   string
	lines  []string
	hunks  []patchHunk
}

type patchHunk struct {
	lines []patchLine
}

type patchLine struct {
	kind byte
	text string
}

type stagedPatchFile struct {
	action  string
	path    string
	absPath string
	content []byte
}

func newApplyPatchTool(ws config.Workspace) core.Tool {
	return applyPatchTool{fsToolBase: fsToolBase{ws: ws}}
}

func (t applyPatchTool) Name() string { return "apply_patch" }
func (t applyPatchTool) Description() string {
	return "Apply a patch to one or more files atomically inside the workspace. Use a single patch string that starts with *** Begin Patch and ends with *** End Patch. Supported operations are *** Add File:, *** Update File:, and *** Delete File:."
}
func (t applyPatchTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"patch": map[string]any{
				"type": "string",
				"description": "Patch document. Required envelope: *** Begin Patch ... *** End Patch. Supported file ops: *** Add File: <path>, *** Update File: <path>, *** Delete File: <path>. Add-file contents must use only + lines. Update hunks must start with @@ and then use patch lines prefixed with space (context), - (remove), or + (add). *** End of File is optional. Multiple file ops may appear in one patch and are applied atomically. Example:\n*** Begin Patch\n*** Update File: /abs/path/to/file.txt\n@@\n-old line\n+new line\n*** End Patch",
			},
		},
		"required": []string{"patch"},
	}
}

func (t applyPatchTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	patchText, _ := args["patch"].(string)
	if strings.TrimSpace(patchText) == "" {
		return nil, errors.New("patch required")
	}
	ops, err := parsePatchDocument(patchText)
	if err != nil {
		return nil, err
	}
	staged, err := t.prepare(ops)
	if err != nil {
		return nil, err
	}
	if err := commitStagedFiles(staged); err != nil {
		return nil, err
	}
	files := make([]map[string]any, 0, len(staged))
	result := map[string]any{
		"files": files,
	}
	for _, item := range staged {
		files = append(files, map[string]any{
			"path":   item.absPath,
			"action": item.action,
		})
		switch item.action {
		case "add":
			result["added"] = intFromAny(result["added"]) + 1
		case "update":
			result["updated"] = intFromAny(result["updated"]) + 1
		case "delete":
			result["deleted"] = intFromAny(result["deleted"]) + 1
		}
	}
	return result, nil
}

func (t applyPatchTool) prepare(ops []patchOp) ([]stagedPatchFile, error) {
	seen := make(map[string]struct{}, len(ops))
	staged := make([]stagedPatchFile, 0, len(ops))
	for _, op := range ops {
		dec, err := validatePatchPath(op, t.ws)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[dec.Path]; ok {
			return nil, fmt.Errorf("duplicate patch target: %s", op.path)
		}
		seen[dec.Path] = struct{}{}
		switch op.action {
		case "add":
			content := composeContent(op.lines, true)
			staged = append(staged, stagedPatchFile{
				action:  op.action,
				path:    op.path,
				absPath: dec.Path,
				content: []byte(content),
			})
		case "update":
			current, err := os.ReadFile(dec.Path)
			if err != nil {
				return nil, err
			}
			updated, err := applyHunksToContent(string(current), op.hunks)
			if err != nil {
				return nil, fmt.Errorf("update %s: %w", op.path, err)
			}
			staged = append(staged, stagedPatchFile{
				action:  op.action,
				path:    op.path,
				absPath: dec.Path,
				content: []byte(updated),
			})
		case "delete":
			staged = append(staged, stagedPatchFile{
				action:  op.action,
				path:    op.path,
				absPath: dec.Path,
			})
		default:
			return nil, fmt.Errorf("unsupported operation: %s", op.action)
		}
	}
	return staged, nil
}

func validatePatchPath(op patchOp, ws config.Workspace) (security.Decision, error) {
	allowNonExist := op.action == "add"
	targetPath := resolveWorkspacePath(op.path, ws)
	if fi, err := os.Lstat(targetPath); err == nil && (fi.Mode()&os.ModeSymlink) != 0 {
		return security.Decision{}, errors.New("path not allowed: symlink")
	}
	dec := security.ValidatePath(targetPath, ws, security.Options{FollowSymlink: true, AllowNonExist: allowNonExist})
	if !dec.Allowed {
		return dec, fmt.Errorf("path not allowed: %s", dec.Reason)
	}
	fi, err := os.Lstat(dec.Path)
	switch op.action {
	case "add":
		if err == nil {
			if (fi.Mode() & os.ModeSymlink) != 0 {
				return dec, errors.New("path not allowed: symlink")
			}
			return dec, fmt.Errorf("add %s: file already exists", op.path)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return dec, err
		}
	case "update":
		if err != nil {
			return dec, err
		}
		if fi.IsDir() {
			return dec, fmt.Errorf("update %s: target is a directory", op.path)
		}
		if (fi.Mode() & os.ModeSymlink) != 0 {
			return dec, errors.New("path not allowed: symlink")
		}
	case "delete":
		if err != nil {
			return dec, err
		}
		if fi.IsDir() {
			return dec, fmt.Errorf("delete %s: target is a directory", op.path)
		}
		if (fi.Mode() & os.ModeSymlink) != 0 {
			return dec, errors.New("path not allowed: symlink")
		}
	}
	return dec, nil
}

func resolveWorkspacePath(p string, ws config.Workspace) string {
	if filepath.IsAbs(p) {
		return p
	}
	if ws.CWD != "" {
		return filepath.Join(ws.CWD, p)
	}
	if len(ws.Roots) > 0 {
		return filepath.Join(ws.Roots[0], p)
	}
	return p
}

func parsePatchDocument(text string) ([]patchOp, error) {
	lines, err := scanPatchLines(text)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 || lines[0] != "*** Begin Patch" {
		return nil, errors.New("invalid patch header")
	}
	var ops []patchOp
	for i := 1; i < len(lines); {
		line := lines[i]
		if line == "*** End Patch" {
			return ops, nil
		}
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			op, next, err := parseAddOperation(lines, i)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
			i = next
		case strings.HasPrefix(line, "*** Update File: "):
			op, next, err := parseUpdateOperation(lines, i)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
			i = next
		case strings.HasPrefix(line, "*** Delete File: "):
			op, next, err := parseDeleteOperation(lines, i)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
			i = next
		default:
			return nil, fmt.Errorf("unexpected patch line: %s", line)
		}
	}
	return nil, errors.New("missing *** End Patch")
}

func parseAddOperation(lines []string, start int) (patchOp, int, error) {
	path := strings.TrimSpace(strings.TrimPrefix(lines[start], "*** Add File: "))
	if path == "" {
		return patchOp{}, 0, errors.New("add file path required")
	}
	op := patchOp{action: "add", path: path}
	i := start + 1
	for i < len(lines) {
		line := lines[i]
		if isPatchBoundary(line) {
			break
		}
		if line == "*** End of File" {
			i++
			continue
		}
		if !strings.HasPrefix(line, "+") {
			return patchOp{}, 0, fmt.Errorf("invalid add line for %s: %s", path, line)
		}
		op.lines = append(op.lines, line[1:])
		i++
	}
	if len(op.lines) == 0 {
		return patchOp{}, 0, fmt.Errorf("add %s: content required", path)
	}
	return op, i, nil
}

func parseUpdateOperation(lines []string, start int) (patchOp, int, error) {
	path := strings.TrimSpace(strings.TrimPrefix(lines[start], "*** Update File: "))
	if path == "" {
		return patchOp{}, 0, errors.New("update file path required")
	}
	op := patchOp{action: "update", path: path}
	i := start + 1
	var current *patchHunk
	seenHeader := false
	for i < len(lines) {
		line := lines[i]
		if isPatchBoundary(line) {
			break
		}
		if line == "*** End of File" {
			i++
			continue
		}
		if strings.HasPrefix(line, "@@") {
			seenHeader = true
			if current == nil {
				current = &patchHunk{}
			} else if len(current.lines) > 0 {
				op.hunks = append(op.hunks, *current)
				current = &patchHunk{}
			}
			i++
			continue
		}
		if current == nil {
			return patchOp{}, 0, fmt.Errorf("update %s: missing hunk header", path)
		}
		if line == "" {
			return patchOp{}, 0, fmt.Errorf("update %s: invalid empty patch line", path)
		}
		switch line[0] {
		case ' ', '+', '-':
			current.lines = append(current.lines, patchLine{kind: line[0], text: line[1:]})
		default:
			return patchOp{}, 0, fmt.Errorf("update %s: invalid patch line: %s", path, line)
		}
		i++
	}
	if current != nil && len(current.lines) > 0 {
		op.hunks = append(op.hunks, *current)
	}
	if !seenHeader || len(op.hunks) == 0 {
		return patchOp{}, 0, fmt.Errorf("update %s: at least one hunk required", path)
	}
	return op, i, nil
}

func parseDeleteOperation(lines []string, start int) (patchOp, int, error) {
	path := strings.TrimSpace(strings.TrimPrefix(lines[start], "*** Delete File: "))
	if path == "" {
		return patchOp{}, 0, errors.New("delete file path required")
	}
	return patchOp{action: "delete", path: path}, start + 1, nil
}

func isPatchBoundary(line string) bool {
	return line == "*** End Patch" ||
		strings.HasPrefix(line, "*** Add File: ") ||
		strings.HasPrefix(line, "*** Update File: ") ||
		strings.HasPrefix(line, "*** Delete File: ")
}

func scanPatchLines(text string) ([]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 8*1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func applyHunksToContent(content string, hunks []patchHunk) (string, error) {
	origLines, trailingNewline := splitContent(content)
	cursor := 0
	out := make([]string, 0, len(origLines))
	for _, hunk := range hunks {
		before, after := materializeHunk(hunk)
		matchAt, err := findHunkStart(origLines, cursor, before)
		if err != nil {
			return "", err
		}
		out = append(out, origLines[cursor:matchAt]...)
		out = append(out, after...)
		cursor = matchAt + len(before)
	}
	out = append(out, origLines[cursor:]...)
	return composeContent(out, trailingNewline), nil
}

func materializeHunk(h patchHunk) (before []string, after []string) {
	for _, line := range h.lines {
		switch line.kind {
		case ' ':
			before = append(before, line.text)
			after = append(after, line.text)
		case '-':
			before = append(before, line.text)
		case '+':
			after = append(after, line.text)
		}
	}
	return before, after
}

func findHunkStart(lines []string, cursor int, before []string) (int, error) {
	if len(before) == 0 {
		if cursor > len(lines) {
			return 0, errors.New("hunk context mismatch")
		}
		return cursor, nil
	}
	for i := cursor; i+len(before) <= len(lines); i++ {
		if matchesAt(lines, i, before) {
			return i, nil
		}
	}
	return 0, errors.New("hunk context mismatch")
}

func matchesAt(lines []string, start int, want []string) bool {
	if start+len(want) > len(lines) {
		return false
	}
	for i := range want {
		if lines[start+i] != want[i] {
			return false
		}
	}
	return true
}

func splitContent(content string) ([]string, bool) {
	if content == "" {
		return nil, false
	}
	trailingNewline := strings.HasSuffix(content, "\n")
	if trailingNewline {
		content = strings.TrimSuffix(content, "\n")
	}
	if content == "" {
		if trailingNewline {
			return []string{""}, true
		}
		return nil, false
	}
	return strings.Split(content, "\n"), trailingNewline
}

func composeContent(lines []string, trailingNewline bool) string {
	if len(lines) == 0 {
		return ""
	}
	text := strings.Join(lines, "\n")
	if trailingNewline {
		return text + "\n"
	}
	return text
}

func commitStagedFiles(staged []stagedPatchFile) error {
	type cleanupFn func() error
	var rollback []cleanupFn
	var finalize []cleanupFn
	for _, item := range staged {
		undos, commits, err := commitOne(item)
		if err != nil {
			for i := len(rollback) - 1; i >= 0; i-- {
				_ = rollback[i]()
			}
			return err
		}
		for _, fn := range undos {
			rollback = append(rollback, cleanupFn(fn))
		}
		for _, fn := range commits {
			finalize = append(finalize, cleanupFn(fn))
		}
	}
	for _, fn := range finalize {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

func commitOne(item stagedPatchFile) ([]func() error, []func() error, error) {
	switch item.action {
	case "add":
		if err := os.MkdirAll(filepath.Dir(item.absPath), 0o755); err != nil {
			return nil, nil, err
		}
		tempPath, err := writeTempFile(item.absPath, item.content)
		if err != nil {
			return nil, nil, err
		}
		if err := os.Rename(tempPath, item.absPath); err != nil {
			_ = os.Remove(tempPath)
			return nil, nil, err
		}
		return []func() error{
			func() error { return os.Remove(item.absPath) },
		}, nil, nil
	case "update":
		backup, err := reserveTempPath(item.absPath)
		if err != nil {
			return nil, nil, err
		}
		tempPath, err := writeTempFile(item.absPath, item.content)
		if err != nil {
			_ = os.Remove(backup)
			return nil, nil, err
		}
		if err := os.Rename(item.absPath, backup); err != nil {
			_ = os.Remove(tempPath)
			_ = os.Remove(backup)
			return nil, nil, err
		}
		if err := os.Rename(tempPath, item.absPath); err != nil {
			_ = os.Rename(backup, item.absPath)
			_ = os.Remove(tempPath)
			return nil, nil, err
		}
		return []func() error{
				func() error {
					_ = os.Remove(item.absPath)
					return os.Rename(backup, item.absPath)
				},
			}, []func() error{
				func() error { return os.Remove(backup) },
			}, nil
	case "delete":
		backup, err := reserveTempPath(item.absPath)
		if err != nil {
			return nil, nil, err
		}
		if err := os.Rename(item.absPath, backup); err != nil {
			_ = os.Remove(backup)
			return nil, nil, err
		}
		return []func() error{
				func() error { return os.Rename(backup, item.absPath) },
			}, []func() error{
				func() error { return os.Remove(backup) },
			}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported operation: %s", item.action)
	}
}

func writeTempFile(target string, content []byte) (string, error) {
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".localgate-applypatch-*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := bytes.NewReader(content).WriteTo(f); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Chmod(0o644); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func reserveTempPath(target string) (string, error) {
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".localgate-applypatch-backup-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}
