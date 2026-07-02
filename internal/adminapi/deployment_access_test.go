package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/deployment"
)

func TestAdminDeploymentAccessMutation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.Deployments.PublicBaseURL = "https://public.test"
	cfg.Deployments.PrivateBaseURL = "https://private.test"
	cfg.Deployments.DefaultAccess = deployment.AccessPublic
	cfg.Workspaces = []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}}

	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	workspace := cfg.Workspaces[0]
	if _, err := application.DeploymentManager.Deploy(context.Background(), deployment.DeployRequest{
		Type: deployment.TypeStatic, Name: "demo", Source: source, Workspace: workspace,
		Options: deployment.DeployOptions{PreserveSource: true, Access: deployment.AccessPublic},
	}); err != nil {
		t.Fatalf("deploy static site: %v", err)
	}

	mux := http.NewServeMux()
	(&API{App: application, DefaultWSID: "ws", BaseURL: "https://admin.test"}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/deployments/static/demo/set-private", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.TaskID == "" {
		t.Fatalf("missing task id: %s", w.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		task, err := application.Tasks.Get(body.TaskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task.Status == core.StatusSucceeded {
			break
		}
		if task.Status == core.StatusFailed || task.Status == core.StatusCanceled || task.Status == core.StatusTimeout {
			t.Fatalf("access task ended as %s: %s", task.Status, task.ErrorMsg)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for access task, status=%s", task.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	items, err := application.DeploymentManager.List(context.Background())
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(items) != 1 || items[0].Access != deployment.AccessPrivate || items[0].URL != "https://demo.private.test/" {
		t.Fatalf("unexpected deployment after access change: %#v", items)
	}
}
