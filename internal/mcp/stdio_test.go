package mcp

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/core"
)

func TestStdioToolsCallAndLogs(t *testing.T) {
	cfg := config.Config{
		LogStore: t.TempDir(),
		Workspaces: []config.Workspace{
			{ID: "ws", CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t"}}})
	_ = readResp()

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	if bytes.Contains(mustJSON(r2), []byte("list_directory")) || bytes.Contains(mustJSON(r2), []byte("search_files")) || bytes.Contains(mustJSON(r2), []byte("run_argv")) || bytes.Contains(mustJSON(r2), []byte("create_upload_token")) || bytes.Contains(mustJSON(r2), []byte("create_download_token")) {
		t.Fatalf("default hidden tools should not appear in tools/list: %v", r2)
	}
	result2, _ := r2["result"].(map[string]any)
	tools2, _ := result2["tools"].([]any)
	wantDescriptions := map[string]bool{
		"task_status": false, "task_logs": false, "task_cancel": false,
		"task_result": false, "wait_task_finish": false,
	}
	for _, raw := range tools2 {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		if _, ok := wantDescriptions[name]; !ok {
			continue
		}
		description, _ := tool["description"].(string)
		if strings.TrimSpace(description) == "" {
			t.Fatalf("task tool %s has empty description: %v", name, tool)
		}
		wantDescriptions[name] = true
	}
	for name, found := range wantDescriptions {
		if !found {
			t.Fatalf("missing task tool %s in tools/list", name)
		}
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "run_command", "arguments": map[string]any{"command": "echo hi"}}})
	r3 := readResp()
	res3, _ := r3["result"].(map[string]any)
	content, _ := res3["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("missing content")
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var payload map[string]any
	_ = json.Unmarshal([]byte(text), &payload)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id in payload: %v", payload)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		writeLine(map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "task_logs", "arguments": map[string]any{"task_id": taskID, "cursor": 0}}})
		r4 := readResp()
		res4, _ := r4["result"].(map[string]any)
		c4, _ := res4["content"].([]any)
		if len(c4) == 0 {
			continue
		}
		f4, _ := c4[0].(map[string]any)
		t4, _ := f4["text"].(string)
		if bytes.Contains([]byte(t4), []byte("hi")) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioApplyPatchTool(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cfg := config.Config{
		LogStore: t.TempDir(),
		Workspaces: []config.Workspace{
			{ID: "ws", Roots: []string{root}, CWD: root, CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	if !bytes.Contains(mustJSON(r2), []byte("apply_patch")) {
		t.Fatalf("missing apply_patch in tools/list: %v", r2)
	}

	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		"-hello",
		"+world",
		"*** End Patch",
	}, "\n")
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "apply_patch", "arguments": map[string]any{"patch": patch}}})
	r3 := readResp()
	if !bytes.Contains(mustJSON(r3), []byte(`"updated":1`)) {
		t.Fatalf("unexpected apply_patch response: %v", r3)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(b) != "world\n" {
		t.Fatalf("unexpected content: %q", string(b))
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioGetStartupInstructionsTool(t *testing.T) {
	cfg := config.Config{
		LogStore: t.TempDir(),
		Workspaces: []config.Workspace{
			{ID: "ws", CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	if !bytes.Contains(mustJSON(r2), []byte("get_startup_instructions")) {
		t.Fatalf("missing get_startup_instructions in tools/list: %v", r2)
	}
	if !bytes.Contains(mustJSON(r2), []byte("get_tool_schema")) {
		t.Fatalf("missing get_tool_schema in tools/list: %v", r2)
	}
	if bytes.Contains(mustJSON(r2), []byte("get_temporary_access_token")) {
		t.Fatalf("get_temporary_access_token should not appear in legacy mode: %v", r2)
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "get_startup_instructions", "arguments": map[string]any{}}})
	r3 := readResp()
	if !bytes.Contains(mustJSON(r3), []byte(`"prompt"`)) {
		t.Fatalf("unexpected get_startup_instructions response: %v", r3)
	}
	if !bytes.Contains(mustJSON(r3), []byte(`"tools"`)) {
		t.Fatalf("missing tools list in get_startup_instructions response: %v", r3)
	}
	if !bytes.Contains(mustJSON(r3), []byte("run_command")) {
		t.Fatalf("missing rendered tool name: %v", r3)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioUploadFileMetadata(t *testing.T) {
	cfg := config.Config{
		LogStore: t.TempDir(),
		Workspaces: []config.Workspace{
			{ID: "ws", Roots: []string{t.TempDir()}, CWD: t.TempDir(), CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	result, _ := r2["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	found := false
	for _, item := range tools {
		m, _ := item.(map[string]any)
		if m["name"] != "upload_file" {
			continue
		}
		meta, _ := m["_meta"].(map[string]any)
		params, _ := meta["openai/fileParams"].([]any)
		if len(params) != 1 || params[0] != "file" {
			t.Fatalf("unexpected upload_file metadata: %#v", m)
		}
		found = true
	}
	if !found {
		t.Fatalf("upload_file not found in tools/list: %#v", r2)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioToolsCallAcceptsBase64ArgumentsWhenEnabled(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cfg := config.Config{
		LogStore:      t.TempDir(),
		ToolArguments: config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{
			{ID: "ws", Roots: []string{root}, CWD: root, CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	raw, _ := json.Marshal(map[string]any{"path": target})
	encoded := base64.StdEncoding.EncodeToString(raw)
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "read_file", "arguments": encoded}})
	r2 := readResp()
	if !bytes.Contains(mustJSON(r2), []byte(`"content":"hello"`)) {
		t.Fatalf("unexpected base64 tools/call response: %v", r2)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioUploadFileKeepsOriginalSchemaInBase64Mode(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		LogStore:      t.TempDir(),
		ToolArguments: config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{
			{ID: "ws", Roots: []string{root}, CWD: root, CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	result, _ := r2["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	found := false
	for _, item := range tools {
		m, _ := item.(map[string]any)
		if m["name"] != "upload_file" {
			continue
		}
		schema, _ := m["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["base64data"]; ok {
			t.Fatalf("upload_file should not expose base64data schema: %#v", m)
		}
		if _, ok := props["file"]; !ok {
			t.Fatalf("upload_file should preserve file property: %#v", m)
		}
		if _, ok := props["path"]; !ok {
			t.Fatalf("upload_file should preserve path property: %#v", m)
		}
		found = true
	}
	if !found {
		t.Fatalf("upload_file not found in tools/list: %#v", r2)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioStartupInstructionsKeepsOriginalSchemaInBase64Mode(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		LogStore:      t.TempDir(),
		ToolArguments: config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{
			{ID: "ws", Roots: []string{root}, CWD: root, CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	result, _ := r2["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	found := false
	for _, item := range tools {
		m, _ := item.(map[string]any)
		if m["name"] != "get_startup_instructions" {
			continue
		}
		schema, _ := m["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if len(props) != 0 {
			t.Fatalf("get_startup_instructions should preserve original empty schema: %#v", m)
		}
		if _, ok := props["base64data"]; ok {
			t.Fatalf("get_startup_instructions should not expose base64data schema: %#v", m)
		}
		found = true
	}
	if !found {
		t.Fatalf("get_startup_instructions not found in tools/list: %#v", r2)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioGetToolSchemaKeepsOriginalSchemaInBase64Mode(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		LogStore:      t.TempDir(),
		HTTP:          config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		ToolHTTPMode:  config.ToolHTTPMode{Enabled: true, AccessTokenTTLSeconds: 600},
		ToolArguments: config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{
			{ID: "ws", Roots: []string{root}, CWD: root, CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	result, _ := r2["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	found := false
	for _, item := range tools {
		m, _ := item.(map[string]any)
		if m["name"] != "get_tool_schema" {
			continue
		}
		schema, _ := m["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["base64data"]; ok {
			t.Fatalf("get_tool_schema should not expose base64data schema: %#v", m)
		}
		if _, ok := props["tool"]; !ok {
			t.Fatalf("get_tool_schema should preserve tool property: %#v", m)
		}
		found = true
	}
	if !found {
		t.Fatalf("get_tool_schema not found in tools/list: %#v", r2)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioToolsCallRejectsBase64ArgumentsWhenDisabled(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cfg := config.Config{
		LogStore: t.TempDir(),
		Workspaces: []config.Workspace{
			{ID: "ws", Roots: []string{root}, CWD: root, CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	raw, _ := json.Marshal(map[string]any{"path": target})
	encoded := base64.StdEncoding.EncodeToString(raw)
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "read_file", "arguments": encoded}})
	r2 := readResp()
	if !bytes.Contains(mustJSON(r2), []byte("tool arguments must be a JSON object")) {
		t.Fatalf("unexpected base64 rejection response: %v", r2)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioToolHTTPModeOnlyListsThreeTools(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		LogStore: t.TempDir(),
		HTTP:     config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		ToolHTTPMode: config.ToolHTTPMode{
			Enabled:               true,
			AccessTokenTTLSeconds: 600,
		},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	body := mustJSON(r2)
	if !bytes.Contains(body, []byte("get_startup_instructions")) || !bytes.Contains(body, []byte("get_temporary_access_token")) || !bytes.Contains(body, []byte("get_tool_schema")) {
		t.Fatalf("missing minimal tools: %v", r2)
	}
	if bytes.Contains(body, []byte("task_status")) || bytes.Contains(body, []byte("run_command")) {
		t.Fatalf("tool http mode should not expose admin or shell tools in MCP: %v", r2)
	}

	application.Close()
	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioDefaultHiddenToolsNotListed(t *testing.T) {
	cfg := config.Config{
		LogStore: t.TempDir(),
		Workspaces: []config.Workspace{
			{ID: "ws", CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	if bytes.Contains(mustJSON(r2), []byte("list_directory")) || bytes.Contains(mustJSON(r2), []byte("search_files")) || bytes.Contains(mustJSON(r2), []byte("run_argv")) {
		t.Fatalf("default hidden tools should not appear in tools/list: %v", r2)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioSessionTools(t *testing.T) {
	cfg := config.Config{
		LogStore: t.TempDir(),
		Workspaces: []config.Workspace{
			{ID: "ws", CommandAllowlist: []string{"echo", "cat", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r2 := readResp()
	if !bytes.Contains(mustJSON(r2), []byte("exec_command")) || !bytes.Contains(mustJSON(r2), []byte("write_stdin")) || !bytes.Contains(mustJSON(r2), []byte("read_thread_terminal")) {
		t.Fatalf("missing session tools: %v", r2)
	}

	writeLine(map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "exec_command",
			"arguments": map[string]any{
				"cmd":               "cat",
				"yield_time_ms":     50,
				"max_output_tokens": 20,
			},
		},
	})
	r3 := readResp()
	res3, _ := r3["result"].(map[string]any)
	sc3, _ := res3["structuredContent"].(map[string]any)
	sessionID, _ := sc3["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("missing session_id: %v", sc3)
	}

	writeLine(map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "write_stdin",
			"arguments": map[string]any{
				"session_id":        sessionID,
				"chars":             "hello\n",
				"yield_time_ms":     100,
				"max_output_tokens": 20,
			},
		},
	})
	r4 := readResp()
	if !bytes.Contains(mustJSON(r4), []byte("hello")) {
		t.Fatalf("expected hello in stdin response: %v", r4)
	}

	writeLine(map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "read_thread_terminal",
			"arguments": map[string]any{
				"session_id":        sessionID,
				"max_output_tokens": 20,
			},
		},
	})
	r5 := readResp()
	if !bytes.Contains(mustJSON(r5), []byte("hello")) {
		t.Fatalf("expected hello in terminal response: %v", r5)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func TestStdioWaitTaskFinish(t *testing.T) {
	cfg := config.Config{
		LogStore: t.TempDir(),
		Workspaces: []config.Workspace{
			{ID: "ws", CommandAllowlist: []string{"echo", "sh"}},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	s := &Server{App: application, Name: "localgate", Version: "dev", WSID: "ws"}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(inR, outW)
	}()

	br := bufio.NewReader(outR)
	writeLine := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = inW.Write(append(b, '\n'))
	}
	readResp := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	_ = readResp()
	writeLine(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "run_command", "arguments": map[string]any{"command": "echo hi"}}})
	r2 := readResp()
	res2, _ := r2["result"].(map[string]any)
	content2, _ := res2["content"].([]any)
	first2, _ := content2[0].(map[string]any)
	text2, _ := first2["text"].(string)
	var payload2 map[string]any
	_ = json.Unmarshal([]byte(text2), &payload2)
	taskID, _ := payload2["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id: %v", payload2)
	}

	writeLine(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "wait_task_finish", "arguments": map[string]any{"task_id": taskID, "timeout_ms": 2000}}})
	r3 := readResp()
	result3, _ := r3["result"].(map[string]any)
	sc3, _ := result3["structuredContent"].(map[string]any)
	if ready, _ := sc3["ready"].(bool); !ready {
		t.Fatalf("expected ready=true: %v", sc3)
	}
	if sc3["status"] != string(core.StatusSucceeded) {
		t.Fatalf("unexpected status: %v", sc3)
	}
	if _, ok := sc3["result"]; ok {
		t.Fatalf("wait_task_finish should not return result: %v", sc3)
	}

	_ = inW.Close()
	_ = outW.Close()
	_ = outR.Close()
	_ = inR.Close()
	_ = <-done
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
