package adminapi

import (
	"net/http"
	"time"

	"localgate/internal/approval"
	"localgate/internal/core"
	"localgate/internal/deployment"
	"localgate/internal/server"
)

func (a *API) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	createdAfter := time.Now().UTC().Add(-24 * time.Hour)
	page, err := a.App.Tasks.List(core.TaskFilter{CreatedAfter: createdAfter, Limit: 10})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "overview_tasks_failed", err.Error(), nil)
		return
	}
	taskStats := map[string]int{
		"total_24h":  a.App.Tasks.Count(core.TaskFilter{CreatedAfter: createdAfter}),
		"running":    a.App.Tasks.Count(core.TaskFilter{Status: core.StatusRunning}),
		"queued":     a.App.Tasks.Count(core.TaskFilter{Status: core.StatusQueued}) + a.App.Tasks.Count(core.TaskFilter{Status: core.StatusAwaitingApproval}),
		"failed_24h": a.App.Tasks.Count(core.TaskFilter{Status: core.StatusFailed, CreatedAfter: createdAfter}) + a.App.Tasks.Count(core.TaskFilter{Status: core.StatusTimeout, CreatedAfter: createdAfter}),
	}
	approvals, _ := a.App.Approvals.List("")
	pending := 0
	for _, request := range approvals {
		if request.Status == approval.StatusPending {
			pending++
		}
	}
	deployments, _ := a.App.DeploymentManager.List(r.Context())
	deploymentStats := map[string]int{"total": len(deployments), "dynamic_running": 0, "failed": 0}
	for _, item := range deployments {
		if item.Type == deployment.TypeDynamic && item.Status == "running" {
			deploymentStats["dynamic_running"]++
		}
		if item.Status == "failed" {
			deploymentStats["failed"]++
		}
	}
	recent := make([]taskListItem, 0, len(page.Items))
	for _, task := range page.Items {
		recent = append(recent, summarizeTask(task))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"tasks": taskStats, "approvals": map[string]int{"pending": pending},
		"deployments": deploymentStats, "recent_tasks": recent,
	})
}
