package dynamic

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"localgate/internal/config"
)

func TestDynamicChildSleeper(t *testing.T) {
	if os.Getenv("LOCALGATE_DYNAMIC_CHILD_HELPER") != "1" {
		return
	}
	if path := os.Getenv("LOCALGATE_DYNAMIC_CHILD_PID_FILE"); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	time.Sleep(time.Minute)
}

func TestDynamicHelperProcess(t *testing.T) {
	if os.Getenv("LOCALGATE_DYNAMIC_HELPER") != "1" {
		return
	}
	host := os.Getenv("HOST")
	port := os.Getenv("PORT")
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if target := os.Getenv("LOCALGATE_DYNAMIC_HEALTH_REDIRECT"); target != "" {
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: first\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(75 * time.Millisecond)
		_, _ = io.WriteString(w, "data: second\n\n")
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking unavailable", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		key := r.Header.Get("Sec-WebSocket-Key")
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
		_ = rw.Flush()
		_, _ = io.Copy(conn, conn)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(os.Stdout, "request %s %s\n", r.Method, r.URL.RequestURI())
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.Header().Set("X-Helper-Host", r.Host)
		w.Header().Set("X-Seen-XFF", r.Header.Get("X-Forwarded-For"))
		w.Header().Set("X-Seen-Forwarded", r.Header.Get("Forwarded"))
		w.Header().Set("X-Seen-Proto", r.Header.Get("X-Forwarded-Proto"))
		w.Header().Set("X-Seen-Real-IP", r.Header.Get("X-Real-IP"))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, r.Method+" "+r.URL.RequestURI())
	})
	if value := os.Getenv("LOCALGATE_DYNAMIC_EXIT_AFTER_MS"); value != "" {
		if delay, err := strconv.Atoi(value); err == nil {
			exitCode, _ := strconv.Atoi(os.Getenv("LOCALGATE_DYNAMIC_EXIT_CODE"))
			go func() { time.Sleep(time.Duration(delay) * time.Millisecond); os.Exit(exitCode) }()
		}
	}
	if pidFile := os.Getenv("LOCALGATE_DYNAMIC_CHILD_PID_FILE"); pidFile != "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDynamicChildSleeper$")
		cmd.Env = append(os.Environ(), "LOCALGATE_DYNAMIC_CHILD_HELPER=1", "LOCALGATE_DYNAMIC_CHILD_PID_FILE="+pidFile)
		if err := cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
		}
	}
	fmt.Fprintln(os.Stdout, "helper-started")
	if err := http.ListenAndServe(host+":"+port, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func newDynamicTestService(t *testing.T, managedRoot string) (*Service, string, string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "app")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.txt"), []byte("dynamic fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DynamicHosting{
		Enabled: true, ControlBaseURL: "https://control.test", PublicBaseURL: "https://apps.test",
		PrivateBaseURL: "https://private.apps.test", DefaultAccess: "public", AllowedExecutables: []string{executable},
		EnvAllowlist: []string{"LOCALGATE_DYNAMIC_HELPER"}, MaxApps: 5,
		MaxFiles: 100, MaxTotalBytes: 10 << 20, MaxFileBytes: 1 << 20,
		StartupTimeoutSeconds: 5, ShutdownTimeoutSeconds: 2,
		RestartPolicy: "never", RestartMaxAttempts: 1, RestartBackoffSeconds: 0,
		MaxLogBytes: 1 << 20,
	}
	service, err := NewService(managedRoot, cfg, map[string]config.Workspace{
		"ws": {ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	return service, source, executable
}

func deployTestApplication(t *testing.T, service *Service, source, executable string) DeployResult {
	t.Helper()
	result, err := service.Deploy(context.Background(), DeployRequest{
		WorkspaceID: "ws", Name: "demo", Source: source,
		Runtime: RuntimeSpec{
			Argv: []string{executable, "-test.run=^TestDynamicHelperProcess$"},
			Env:  map[string]string{"LOCALGATE_DYNAMIC_HELPER": "1"},
		},
		Options: DeployOptions{PreserveSource: true, HealthPath: "/healthz", RestartPolicy: "never"},
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	return result
}

func proxyRequest(t *testing.T, service *Service, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://demo.apps.test"+path, nil)
	req.Host = "demo.apps.test"
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)
	return w
}

func TestDynamicDeploymentLifecycleAndProxy(t *testing.T) {
	managedRoot := filepath.Join(t.TempDir(), "managed")
	service, source, executable := newDynamicTestService(t, managedRoot)
	t.Cleanup(func() { _ = service.Close() })
	result := deployTestApplication(t, service, source, executable)
	if result.Deployment.Status != "active" || result.Deployment.Health != "healthy" {
		t.Fatalf("unexpected deployment: %#v", result.Deployment)
	}
	if result.Deployment.Hostname != "demo.apps.test" || result.Deployment.URL != "https://demo.apps.test/" {
		t.Fatalf("unexpected routing metadata: %#v", result.Deployment)
	}

	w := proxyRequest(t, service, http.MethodPost, "/hello?value=1")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "POST /hello?value=1") {
		t.Fatalf("proxy response: code=%d body=%q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Helper-Host"); got != "demo.apps.test" {
		t.Fatalf("backend did not receive original host: %q", got)
	}

	logs, err := service.Logs("demo", "stdout", 64<<10)
	if err != nil || !strings.Contains(logs.Data, "helper-started") {
		t.Fatalf("logs: %#v err=%v", logs, err)
	}

	if _, err := service.Restart(context.Background(), "demo"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if w := proxyRequest(t, service, http.MethodGet, "/after-restart"); w.Code != http.StatusOK {
		t.Fatalf("proxy after restart: %d %s", w.Code, w.Body.String())
	}
	if _, err := service.Stop(context.Background(), "demo"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if w := proxyRequest(t, service, http.MethodGet, "/stopped"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while stopped, got %d", w.Code)
	}
	if _, err := service.Start(context.Background(), "demo"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if w := proxyRequest(t, service, http.MethodGet, "/started"); w.Code != http.StatusOK {
		t.Fatalf("proxy after start: %d %s", w.Code, w.Body.String())
	}
	if err := service.Delete(context.Background(), "demo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if w := proxyRequest(t, service, http.MethodGet, "/deleted"); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}
}

func TestDynamicRoutingMetadataIsDerivedFromCurrentConfig(t *testing.T) {
	managedRoot := filepath.Join(t.TempDir(), "managed")
	if err := os.MkdirAll(managedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	registry := `{
  "deployments": {
    "demo": {
      "name": "demo",
      "type": "dynamic",
      "workspace_id": "ws",
      "hostname": "demo.old.test",
      "url": "https://demo.old.test/",
      "active_release": "release-a",
      "desired_state": "stopped",
      "status": "stopped",
      "health": "stopped",
      "argv": ["unused"],
      "working_dir": ".",
      "health_path": "/healthz",
      "restart_policy": "never",
      "created_at": "2026-01-01T00:00:00Z",
      "updated_at": "2026-01-01T00:00:00Z",
      "file_count": 1,
      "total_bytes": 2
    }
  }
}`
	if err := os.WriteFile(filepath.Join(managedRoot, "deployments.json"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(managedRoot, config.DynamicHosting{
		Enabled: true, PublicBaseURL: "http://apps.new.test", PrivateBaseURL: "http://private.apps.new.test", DefaultAccess: "public",
	}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	view, err := service.Get("demo")
	if err != nil {
		t.Fatal(err)
	}
	if view.Hostname != "demo.apps.new.test" || view.URL != "http://demo.apps.new.test/" {
		t.Fatalf("routing metadata was not derived from current config: %#v", view)
	}

	service.mu.RLock()
	deployment := service.deployments["demo"]
	service.mu.RUnlock()
	if err := service.updateDeployment(deployment); err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(filepath.Join(managedRoot, "deployments.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), `"hostname"`) || strings.Contains(string(persisted), `"url"`) {
		t.Fatalf("derived routing metadata must not be persisted: %s", persisted)
	}

	oldReq := httptest.NewRequest(http.MethodGet, "http://demo.old.test/", nil)
	oldW := httptest.NewRecorder()
	service.ServeHTTP(oldW, oldReq)
	if oldW.Code != http.StatusNotFound {
		t.Fatalf("old configured host should not route, got %d", oldW.Code)
	}
	newReq := httptest.NewRequest(http.MethodGet, "http://demo.apps.new.test/", nil)
	newW := httptest.NewRecorder()
	service.ServeHTTP(newW, newReq)
	if newW.Code != http.StatusServiceUnavailable {
		t.Fatalf("current configured host should resolve the stopped deployment, got %d", newW.Code)
	}
}

func TestDynamicDeploymentRecovery(t *testing.T) {
	managedRoot := filepath.Join(t.TempDir(), "managed")
	service, source, executable := newDynamicTestService(t, managedRoot)
	deployTestApplication(t, service, source, executable)
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, _, _ := newDynamicTestServiceAtExistingWorkspace(t, managedRoot, filepath.Dir(source), executable)
	defer recovered.Close()
	deadline := time.Now().Add(8 * time.Second)
	for {
		w := proxyRequest(t, recovered, http.MethodGet, "/recovered")
		if w.Code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery did not become healthy: code=%d body=%s", w.Code, w.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func newDynamicTestServiceAtExistingWorkspace(t *testing.T, managedRoot, workspaceRoot, executable string) (*Service, string, string) {
	t.Helper()
	cfg := config.DynamicHosting{
		Enabled: true, ControlBaseURL: "https://control.test", PublicBaseURL: "https://apps.test",
		PrivateBaseURL: "https://private.apps.test", DefaultAccess: "public", AllowedExecutables: []string{executable},
		EnvAllowlist: []string{"LOCALGATE_DYNAMIC_HELPER"}, MaxApps: 5,
		MaxFiles: 100, MaxTotalBytes: 10 << 20, MaxFileBytes: 1 << 20,
		StartupTimeoutSeconds: 5, ShutdownTimeoutSeconds: 2,
		RestartPolicy: "never", RestartMaxAttempts: 1, MaxLogBytes: 1 << 20,
	}
	service, err := NewService(managedRoot, cfg, map[string]config.Workspace{
		"ws": {ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	return service, workspaceRoot, executable
}

func TestDynamicDeploymentRejectsExecutableAndSecretEnv(t *testing.T) {
	service, source, executable := newDynamicTestService(t, filepath.Join(t.TempDir(), "managed"))
	defer service.Close()
	_, err := service.Deploy(context.Background(), DeployRequest{
		WorkspaceID: "ws", Name: "denied", Source: source,
		Runtime: RuntimeSpec{Argv: []string{"not-allowed"}},
		Options: DeployOptions{PreserveSource: true},
	})
	if ErrorCode(err) != "deployment_executable_denied" {
		t.Fatalf("unexpected executable error: %v", err)
	}
	service.config.EnvAllowlist = append(service.config.EnvAllowlist, "API_TOKEN")
	_, err = service.Deploy(context.Background(), DeployRequest{
		WorkspaceID: "ws", Name: "secret", Source: source,
		Runtime: RuntimeSpec{Argv: []string{executable}, Env: map[string]string{"API_TOKEN": strconv.Itoa(1)}},
		Options: DeployOptions{PreserveSource: true},
	})
	if ErrorCode(err) != "deployment_env_denied" {
		t.Fatalf("unexpected env error: %v", err)
	}
}

func TestOnFailureDoesNotRestartCleanExit(t *testing.T) {
	service, source, executable := newDynamicTestService(t, filepath.Join(t.TempDir(), "managed"))
	t.Cleanup(func() { _ = service.Close() })
	service.config.EnvAllowlist = append(service.config.EnvAllowlist, "LOCALGATE_DYNAMIC_EXIT_AFTER_MS")
	service.config.RestartMaxAttempts = 3
	_, err := service.Deploy(context.Background(), DeployRequest{
		WorkspaceID: "ws", Name: "clean-exit", Source: source,
		Runtime: RuntimeSpec{Argv: []string{executable, "-test.run=^TestDynamicHelperProcess$"}, Env: map[string]string{
			"LOCALGATE_DYNAMIC_HELPER": "1", "LOCALGATE_DYNAMIC_EXIT_AFTER_MS": "500",
		}},
		Options: DeployOptions{PreserveSource: true, HealthPath: "/healthz", RestartPolicy: "on-failure"},
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, getErr := service.Get("clean-exit")
		if getErr != nil {
			t.Fatal(getErr)
		}
		if view.Status == StatusStopped {
			if view.DesiredState != DesiredStopped || view.RestartCount != 0 {
				t.Fatalf("unexpected clean exit state: %#v", view)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clean exit did not settle: %#v", view)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestHealthCheckDoesNotFollowRedirect(t *testing.T) {
	var externalHits atomic.Int64
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		externalHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer external.Close()
	service, source, executable := newDynamicTestService(t, filepath.Join(t.TempDir(), "managed"))
	t.Cleanup(func() { _ = service.Close() })
	service.config.EnvAllowlist = append(service.config.EnvAllowlist, "LOCALGATE_DYNAMIC_HEALTH_REDIRECT")
	_, err := service.Deploy(context.Background(), DeployRequest{
		WorkspaceID: "ws", Name: "redirect", Source: source,
		Runtime: RuntimeSpec{Argv: []string{executable, "-test.run=^TestDynamicHelperProcess$"}, Env: map[string]string{
			"LOCALGATE_DYNAMIC_HELPER": "1", "LOCALGATE_DYNAMIC_HEALTH_REDIRECT": external.URL,
		}},
		Options: DeployOptions{PreserveSource: true, HealthPath: "/healthz", RestartPolicy: "never"},
	})
	if err != nil {
		t.Fatalf("redirect health deploy: %v", err)
	}
	if hits := externalHits.Load(); hits != 0 {
		t.Fatalf("health check followed external redirect: %d hits", hits)
	}
}

func TestProxyRebuildsForwardedHeadersAndLimitsBody(t *testing.T) {
	service, source, executable := newDynamicTestService(t, filepath.Join(t.TempDir(), "managed"))
	t.Cleanup(func() { _ = service.Close() })
	service.config.MaxRequestBodyBytes = 4
	deployTestApplication(t, service, source, executable)

	req := httptest.NewRequest(http.MethodGet, "http://demo.apps.test/headers", nil)
	req.Host = "demo.apps.test"
	req.Header.Set("Forwarded", "for=attacker")
	req.Header.Set("X-Forwarded-For", "attacker")
	req.Header.Set("X-Forwarded-Proto", "javascript")
	req.Header.Set("X-Real-IP", "attacker")
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("header proxy: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Seen-Forwarded") != "" {
		t.Fatalf("Forwarded header was trusted: %q", w.Header().Get("X-Seen-Forwarded"))
	}
	if strings.Contains(w.Header().Get("X-Seen-XFF"), "attacker") {
		t.Fatalf("XFF was trusted: %q", w.Header().Get("X-Seen-XFF"))
	}
	if w.Header().Get("X-Seen-Proto") != "https" {
		t.Fatalf("unexpected forwarded proto: %q", w.Header().Get("X-Seen-Proto"))
	}
	if strings.Contains(w.Header().Get("X-Seen-Real-IP"), "attacker") {
		t.Fatalf("real ip was trusted: %q", w.Header().Get("X-Seen-Real-IP"))
	}

	large := httptest.NewRequest(http.MethodPost, "http://demo.apps.test/upload", strings.NewReader("0123456789"))
	large.Host = "demo.apps.test"
	largeW := httptest.NewRecorder()
	service.ServeHTTP(largeW, large)
	if largeW.Code == http.StatusOK {
		t.Fatalf("oversized body was accepted: %s", largeW.Body.String())
	}
}

func TestLogRotationAndReleaseRetention(t *testing.T) {
	managedRoot := filepath.Join(t.TempDir(), "managed")
	service, source, executable := newDynamicTestService(t, managedRoot)
	t.Cleanup(func() { _ = service.Close() })
	service.config.MaxLogBytes = 96
	service.config.MaxLogFiles = 2
	service.config.ReleaseRetention = 2
	result := deployTestApplication(t, service, source, executable)
	for index := 0; index < 20; index++ {
		path := fmt.Sprintf("/log-line-%02d-abcdefghijklmnopqrstuvwxyz", index)
		if w := proxyRequest(t, service, http.MethodGet, path); w.Code != http.StatusOK {
			t.Fatalf("proxy %d: %d", index, w.Code)
		}
	}
	logRoot := filepath.Join(managedRoot, "apps", "demo", "releases", result.Deployment.ReleaseID)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(logRoot, "stdout.log.1")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rotated stdout.log.1 was not created")
		}
		time.Sleep(25 * time.Millisecond)
	}
	for index := 0; index < 3; index++ {
		_, err := service.Deploy(context.Background(), DeployRequest{
			WorkspaceID: "ws", Name: "demo", Source: source,
			Runtime: RuntimeSpec{Argv: []string{executable, "-test.run=^TestDynamicHelperProcess$"}, Env: map[string]string{"LOCALGATE_DYNAMIC_HELPER": "1"}},
			Options: DeployOptions{Overwrite: true, PreserveSource: true, HealthPath: "/healthz", RestartPolicy: "never"},
		})
		if err != nil {
			t.Fatalf("overwrite %d: %v", index, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(managedRoot, "apps", "demo", "releases"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".stage-") {
			count++
		}
	}
	if count > 2 {
		t.Fatalf("release retention exceeded: %d releases", count)
	}
}

func TestStopPersistsIntentBeforeTerminatingProcess(t *testing.T) {
	service, source, executable := newDynamicTestService(t, filepath.Join(t.TempDir(), "managed"))
	t.Cleanup(func() { _ = service.Close() })
	deployTestApplication(t, service, source, executable)
	service.store.saveHook = func(*registry) error { return fmt.Errorf("injected registry failure") }
	if _, err := service.Stop(context.Background(), "demo"); err == nil {
		t.Fatal("expected stop persistence failure")
	}
	service.store.saveHook = nil
	if w := proxyRequest(t, service, http.MethodGet, "/still-running"); w.Code != http.StatusOK {
		t.Fatalf("process was terminated before stop intent persisted: %d %s", w.Code, w.Body.String())
	}
	if _, err := service.Stop(context.Background(), "demo"); err != nil {
		t.Fatalf("stop after recovery: %v", err)
	}
}

func TestReleaseHistoryAndRollback(t *testing.T) {
	managedRoot := filepath.Join(t.TempDir(), "managed")
	service, source, executable := newDynamicTestService(t, managedRoot)
	t.Cleanup(func() { _ = service.Close() })
	service.config.ReleaseRetention = 3

	first := deployTestApplication(t, service, source, executable)
	if err := os.WriteFile(filepath.Join(source, "version.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := service.Deploy(context.Background(), DeployRequest{
		WorkspaceID: "ws", Name: "demo", Source: source,
		Runtime: RuntimeSpec{
			Argv: []string{executable, "-test.run=^TestDynamicHelperProcess$"},
			Env:  map[string]string{"LOCALGATE_DYNAMIC_HELPER": "1"},
		},
		Options: DeployOptions{Overwrite: true, PreserveSource: true, HealthPath: "/healthz", RestartPolicy: "never"},
	})
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if second.Deployment.ReleaseID == first.Deployment.ReleaseID {
		t.Fatal("overwrite did not create a new release")
	}

	releases, err := service.Releases("demo")
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	if len(releases) < 2 {
		t.Fatalf("expected retained release history, got %#v", releases)
	}
	foundFirst, foundSecondActive := false, false
	for _, release := range releases {
		if release.ID == first.Deployment.ReleaseID {
			foundFirst = true
		}
		if release.ID == second.Deployment.ReleaseID && release.Active {
			foundSecondActive = true
		}
	}
	if !foundFirst || !foundSecondActive {
		t.Fatalf("unexpected release history: %#v", releases)
	}

	rolledBack, err := service.Rollback(context.Background(), "demo", first.Deployment.ReleaseID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolledBack.ReleaseID != first.Deployment.ReleaseID || rolledBack.Status != StatusActive {
		t.Fatalf("unexpected rollback result: %#v", rolledBack)
	}
	if w := proxyRequest(t, service, http.MethodGet, "/after-rollback"); w.Code != http.StatusOK {
		t.Fatalf("proxy after rollback: %d %s", w.Code, w.Body.String())
	}
}

func TestOnFailureStopsAfterRestartExhaustion(t *testing.T) {
	service, source, executable := newDynamicTestService(t, filepath.Join(t.TempDir(), "managed"))
	t.Cleanup(func() { _ = service.Close() })
	service.config.EnvAllowlist = append(service.config.EnvAllowlist, "LOCALGATE_DYNAMIC_EXIT_AFTER_MS", "LOCALGATE_DYNAMIC_EXIT_CODE")
	service.config.RestartMaxAttempts = 1
	service.config.RestartBackoffSeconds = 0
	_, err := service.Deploy(context.Background(), DeployRequest{
		WorkspaceID: "ws", Name: "crash-loop", Source: source,
		Runtime: RuntimeSpec{Argv: []string{executable, "-test.run=^TestDynamicHelperProcess$"}, Env: map[string]string{
			"LOCALGATE_DYNAMIC_HELPER": "1", "LOCALGATE_DYNAMIC_EXIT_AFTER_MS": "250", "LOCALGATE_DYNAMIC_EXIT_CODE": "3",
		}},
		Options: DeployOptions{PreserveSource: true, HealthPath: "/healthz", RestartPolicy: "on-failure"},
	})
	if err != nil {
		t.Fatalf("deploy crash loop: %v", err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		view, getErr := service.Get("crash-loop")
		if getErr != nil {
			t.Fatal(getErr)
		}
		if view.Status == StatusCrashed && view.RestartCount == 1 && strings.Contains(view.LastError, "restart attempts exhausted") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restart exhaustion did not settle: %#v", view)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestProxySupportsSSEAndWebSocketUpgrade(t *testing.T) {
	service, source, executable := newDynamicTestService(t, filepath.Join(t.TempDir(), "managed"))
	t.Cleanup(func() { _ = service.Close() })
	deployTestApplication(t, service, source, executable)
	frontend := httptest.NewServer(service)
	defer frontend.Close()

	req, err := http.NewRequest(http.MethodGet, frontend.URL+"/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "demo.apps.test"
	resp, err := frontend.Client().Do(req)
	if err != nil {
		t.Fatalf("SSE request: %v", err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(data), "data: first") || !strings.Contains(string(data), "data: second") {
		t.Fatalf("SSE response=%q err=%v", data, err)
	}

	address := strings.TrimPrefix(frontend.URL, "http://")
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial websocket frontend: %v", err)
	}
	defer conn.Close()
	key := base64.StdEncoding.EncodeToString([]byte("localgate-websocket"))
	_, _ = fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: demo.apps.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", key)
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read websocket upgrade: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected websocket status: %s", response.Status)
	}
	_ = response.Body.Close()
}

func TestReleaseMetadataIsWritten(t *testing.T) {
	managedRoot := filepath.Join(t.TempDir(), "managed")
	service, source, executable := newDynamicTestService(t, managedRoot)
	t.Cleanup(func() { _ = service.Close() })
	result := deployTestApplication(t, service, source, executable)
	path := filepath.Join(managedRoot, "apps", "demo", "releases", result.Deployment.ReleaseID, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read release metadata: %v", err)
	}
	for _, expected := range []string{"\"name\": \"demo\"", "\"release_id\"", "\"executable\""} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("metadata missing %s: %s", expected, data)
		}
	}
}

func TestLifecycleEventsAreEmitted(t *testing.T) {
	service, source, executable := newDynamicTestService(t, filepath.Join(t.TempDir(), "managed"))
	t.Cleanup(func() { _ = service.Close() })
	events := make(chan Event, 32)
	service.SetEventSink(func(event Event) { events <- event })
	deployTestApplication(t, service, source, executable)
	if _, err := service.Stop(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"deployment_staging_started": false,
		"deployment_health_passed":   false,
		"deployment_activated":       false,
		"deployment_stopped":         false,
		"deployment_deleted":         false,
	}
	deadline := time.After(3 * time.Second)
	for {
		complete := true
		for _, seen := range wanted {
			complete = complete && seen
		}
		if complete {
			return
		}
		select {
		case event := <-events:
			if _, ok := wanted[event.Action]; ok {
				wanted[event.Action] = true
			}
		case <-deadline:
			t.Fatalf("missing lifecycle events: %#v", wanted)
		}
	}
}
