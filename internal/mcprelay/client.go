package mcprelay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"localgate/internal/config"
)

const stderrTailLimit = 8 * 1024

type ToolMeta struct {
	Name        string
	Description string
	InputSchema any
}

type Client struct {
	name string
	cmd  *exec.Cmd

	stdin io.WriteCloser

	callMu  sync.Mutex
	respMu  sync.Mutex
	waiters map[string]chan rpcResponse
	nextID  int64

	toolsMu sync.RWMutex
	tools   []ToolMeta

	stderrMu sync.Mutex
	stderr   bytes.Buffer

	done    chan struct{}
	waitErr error
	waitMu  sync.RWMutex

	closeOnce sync.Once
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErrorWire   `json:"error,omitempty"`
}

type rpcErrorWire struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *RPCError
}

func Start(ctx context.Context, cfg config.MCPServer) (*Client, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = mergeEnv(cfg.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if cfg.CWD != "" {
		cmd.Dir = cfg.CWD
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe for %s: %w", cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe for %s: %w", cfg.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe for %s: %w", cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start downstream %s: %w", cfg.Name, err)
	}

	c := &Client{
		name:    cfg.Name,
		cmd:     cmd,
		stdin:   stdin,
		waiters: make(map[string]chan rpcResponse),
		done:    make(chan struct{}),
	}
	go c.readLoop(stdout)
	go c.captureStderr(stderr)
	go c.waitLoop()

	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize downstream %s: %w", cfg.Name, err)
	}
	if err := c.sendNotification("notifications/initialized", map[string]any{}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("send initialized notification to %s: %w", cfg.Name, err)
	}
	if _, err := c.ListTools(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("load tools for %s: %w", cfg.Name, err)
	}
	return c, nil
}

func (c *Client) Name() string { return c.name }

func (c *Client) Tools() []ToolMeta {
	c.toolsMu.RLock()
	defer c.toolsMu.RUnlock()
	out := make([]ToolMeta, len(c.tools))
	copy(out, c.tools)
	return out
}

func (c *Client) ListTools(ctx context.Context) ([]ToolMeta, error) {
	var result mcp.ListToolsResult
	if err := c.sendRequest(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	tools := make([]ToolMeta, 0, len(result.Tools))
	for _, tool := range result.Tools {
		schema, err := normalizeToolSchema(tool)
		if err != nil {
			return nil, fmt.Errorf("normalize input schema for downstream tool %s/%s: %w", c.name, tool.Name, err)
		}
		tools = append(tools, ToolMeta{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}
	c.toolsMu.Lock()
	c.tools = tools
	c.toolsMu.Unlock()
	return tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	var result mcp.CallToolResult
	if err := c.sendRequest(ctx, "tools/call", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-c.done:
			case <-time.After(300 * time.Millisecond):
				_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
				<-c.done
			}
		} else {
			<-c.done
		}
		closeErr = c.exitError()
	})
	return closeErr
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "localgate-relay",
			"version": "dev",
		},
	}
	return c.sendRequest(ctx, "initialize", params, &struct{}{})
}

func (c *Client) sendNotification(method string, params any) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	if err := c.ensureRunning(); err != nil {
		return err
	}
	payload, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := c.stdin.Write(payload); err != nil {
		return c.wrapError(err)
	}
	return nil
}

func (c *Client) sendRequest(ctx context.Context, method string, params any, out any) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.ensureRunning(); err != nil {
		return err
	}

	c.nextID++
	id := c.nextID
	idKey := fmt.Sprintf("%d", id)
	waiter := make(chan rpcResponse, 1)

	c.respMu.Lock()
	c.waiters[idKey] = waiter
	c.respMu.Unlock()
	defer func() {
		c.respMu.Lock()
		delete(c.waiters, idKey)
		c.respMu.Unlock()
	}()

	payload, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := c.stdin.Write(payload); err != nil {
		return c.wrapError(err)
	}

	select {
	case resp := <-waiter:
		if resp.Error != nil {
			return resp.Error
		}
		if out == nil || len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode downstream %s response for %s: %w", c.name, method, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.wrapError(c.exitError())
	}
}

func (c *Client) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var env rpcEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		if len(env.ID) == 0 {
			continue
		}
		idKey := strings.TrimSpace(string(env.ID))
		resp := rpcResponse{Result: env.Result}
		if env.Error != nil {
			resp.Error = &RPCError{Code: env.Error.Code, Message: env.Error.Message}
		}
		c.respMu.Lock()
		waiter := c.waiters[idKey]
		c.respMu.Unlock()
		if waiter != nil {
			waiter <- resp
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		c.waitMu.Lock()
		if c.waitErr == nil {
			c.waitErr = fmt.Errorf("read downstream stdout for %s: %w", c.name, err)
		}
		c.waitMu.Unlock()
	}
}

func (c *Client) captureStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 512*1024)
	for scanner.Scan() {
		c.appendStderr(scanner.Bytes())
		c.appendStderr([]byte("\n"))
	}
}

func (c *Client) waitLoop() {
	waitErr := c.cmd.Wait()
	c.waitMu.Lock()
	if c.waitErr == nil {
		c.waitErr = waitErr
	}
	c.waitMu.Unlock()
	close(c.done)
}

func (c *Client) ensureRunning() error {
	select {
	case <-c.done:
		return c.wrapError(c.exitError())
	default:
		return nil
	}
}

func (c *Client) exitError() error {
	c.waitMu.RLock()
	defer c.waitMu.RUnlock()
	return c.waitErr
}

func (c *Client) wrapError(err error) error {
	if err == nil {
		err = errors.New("downstream exited")
	}
	stderr := c.stderrTail()
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w (stderr: %s)", err, stderr)
}

func (c *Client) stderrTail() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return strings.TrimSpace(c.stderr.String())
}

func (c *Client) appendStderr(chunk []byte) {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	if len(chunk) >= stderrTailLimit {
		c.stderr.Reset()
		c.stderr.Write(chunk[len(chunk)-stderrTailLimit:])
		return
	}
	if c.stderr.Len()+len(chunk) > stderrTailLimit {
		overflow := c.stderr.Len() + len(chunk) - stderrTailLimit
		buf := c.stderr.Bytes()
		c.stderr.Reset()
		c.stderr.Write(buf[overflow:])
	}
	c.stderr.Write(chunk)
}

func mergeEnv(extra map[string]string) []string {
	envMap := make(map[string]string, len(extra))
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		envMap[parts[0]] = parts[1]
	}
	for k, v := range extra {
		envMap[k] = v
	}
	out := make([]string, 0, len(envMap))
	for k, v := range envMap {
		out = append(out, k+"="+v)
	}
	return out
}

func normalizeToolSchema(tool mcp.Tool) (any, error) {
	if len(tool.RawInputSchema) > 0 {
		var schema any
		if err := json.Unmarshal(tool.RawInputSchema, &schema); err != nil {
			return nil, err
		}
		return coerceToolSchemaObject(schema), nil
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return nil, err
	}
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return coerceToolSchemaObject(schema), nil
}

func coerceToolSchemaObject(schema any) map[string]any {
	if m, ok := schema.(map[string]any); ok {
		out := cloneMap(m)
		if _, ok := out["type"]; !ok || out["type"] == "" || out["type"] == nil {
			out["type"] = "object"
		}
		if _, ok := out["properties"]; !ok || out["properties"] == nil {
			out["properties"] = map[string]any{}
		}
		if _, ok := out["required"]; !ok || out["required"] == nil {
			out["required"] = []string{}
		}
		return out
	}
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
