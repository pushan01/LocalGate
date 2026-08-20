package httpapi

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"localgate/internal/core"
	"localgate/internal/deployment"
	"localgate/internal/invocation"
	"localgate/internal/server"
)

type deploymentOverviewItem struct {
	Type, Name, URL, Access, Status, Index, Hostname, Health, ReleaseID, UpdatedAt string
	SPA, CanStop, CanStart                                                         bool
	StopURL, StartURL, DeleteURL, AccessURL, AccessLabel                           string
}

type deploymentOverviewView struct {
	Count       int
	Manage      bool
	Notice      string
	NoticeKind  string
	Deployments []deploymentOverviewItem
}

var deploymentOverviewTemplate = template.Must(template.New("deployment-overview").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{if .Manage}}LocalGate Deployment Management{{else}}LocalGate Deployments{{end}}</title><style>
:root{color-scheme:light dark;font-family:Inter,ui-sans-serif,system-ui,sans-serif}*{box-sizing:border-box}body{margin:0;background:#f4f6f8;color:#17202a}button{font:inherit}main{width:min(1120px,calc(100% - 32px));margin:48px auto 72px}header{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:24px}h1{font-size:clamp(2rem,5vw,3.25rem);line-height:1;margin:0 0 10px}.subtitle{margin:0;color:#667085}.count{padding:8px 12px;border:1px solid #d0d5dd;border-radius:999px;background:#fff;font-weight:650}.notice{margin:0 0 20px;padding:14px 16px;border-radius:12px;border:1px solid #b2ccff;background:#eff8ff;color:#175cd3}.notice.error{border-color:#fecdca;background:#fef3f2;color:#b42318}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(310px,1fr));gap:16px}.card{display:flex;flex-direction:column;min-height:300px;padding:22px;border:1px solid #d8dee6;border-radius:18px;background:#fff;box-shadow:0 10px 30px rgba(16,24,40,.06)}.card-top{display:flex;align-items:center;justify-content:space-between;gap:12px}.name{margin:18px 0 4px;font-size:1.35rem;overflow-wrap:anywhere}.type,.status{display:inline-flex;border-radius:999px;padding:5px 9px;font-size:.78rem;font-weight:700;text-transform:uppercase}.type{background:#eef4ff;color:#3538cd}.status{background:#ecfdf3;color:#027a48}.status.stopped,.status.stopping{background:#f2f4f7;color:#475467}.status.failed{background:#fef3f2;color:#b42318}dl{display:grid;grid-template-columns:auto 1fr;gap:8px 12px;margin:18px 0 22px;font-size:.9rem}dt{color:#667085}dd{margin:0;overflow-wrap:anywhere}.actions{display:flex;flex-wrap:wrap;gap:9px;margin-top:auto;align-items:center}.actions form{margin:0}.button{display:inline-flex;justify-content:center;align-items:center;min-height:40px;padding:9px 13px;border:1px solid #d0d5dd;border-radius:10px;background:#fff;color:#344054;text-decoration:none;font-weight:700;cursor:pointer}.primary{background:#101828;color:#fff;border-color:#101828}.stop{color:#b54708;border-color:#fedf89;background:#fffaeb}.start{color:#027a48;border-color:#abefc6;background:#ecfdf3}details{width:100%;margin-top:8px;padding-top:12px;border-top:1px solid #eaecf0}summary{cursor:pointer;color:#b42318;font-weight:700}.danger{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:12px;padding:12px;border-radius:10px;background:#fef3f2;color:#7a271a;font-size:.86rem}.delete{background:#b42318;color:#fff;border-color:#b42318}.empty{padding:48px 24px;border:1px dashed #98a2b3;border-radius:18px;text-align:center;background:#fff;color:#667085}@media(max-width:620px){header,.danger{align-items:flex-start;flex-direction:column}}@media(prefers-color-scheme:dark){body{background:#0c111d;color:#f2f4f7}.subtitle,dt,.empty{color:#98a2b3}.count,.card,.empty,.button{background:#161b26;border-color:#344054;color:#f2f4f7}.danger{background:#3b1116;color:#fecaca}}
</style></head><body><main>
<header><div><h1>Deployment management</h1><p class="subtitle">Open, stop, start, and delete LocalGate deployments.</p></div><div class="count">{{.Count}} total</div></header>
{{if .Notice}}<div class="notice {{.NoticeKind}}" role="status">{{.Notice}}</div>{{end}}
{{if .Deployments}}<section class="grid">{{range .Deployments}}<article class="card">
<div class="card-top"><span class="type">{{.Type}}</span><span class="status {{.Status}}">{{.Status}}</span></div><h2 class="name">{{.Name}}</h2><dl>
{{if eq .Type "static"}}<dt>Index</dt><dd>{{.Index}}</dd><dt>SPA</dt><dd>{{if .SPA}}Enabled{{else}}Disabled{{end}}</dd>{{else}}<dt>Health</dt><dd>{{.Health}}</dd><dt>Hostname</dt><dd>{{.Hostname}}</dd><dt>Release</dt><dd>{{.ReleaseID}}</dd>{{end}}<dt>Access</dt><dd>{{.Access}}</dd><dt>Updated</dt><dd>{{.UpdatedAt}}</dd><dt>URL</dt><dd>{{.URL}}</dd></dl>
<div class="actions"><a class="button primary" href="{{.URL}}" target="_blank" rel="noopener noreferrer">Open deployment</a>
<form method="post" action="{{.AccessURL}}"><button class="button" type="submit">{{.AccessLabel}}</button></form>
{{if .CanStop}}<form method="post" action="{{.StopURL}}"><button class="button stop" type="submit">Stop service</button></form>{{end}}
{{if .CanStart}}<form method="post" action="{{.StartURL}}"><button class="button start" type="submit">Start service</button></form>{{end}}
<details><summary>Delete deployment</summary><div class="danger"><span>Remove the deployment and published files. Approval may be required.</span><form method="post" action="{{.DeleteURL}}"><input type="hidden" name="confirm" value="delete"><button class="button delete" type="submit">Delete permanently</button></form></div></details></div>
</article>{{end}}</section>{{else}}<section class="empty"><h2>No deployments yet</h2><p>Deploy a static or dynamic site with <code>deploy_tool</code>.</p></section>{{end}}
</main></body></html>`))

func (a *API) handleDeploymentOverview(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/deploy-mgr/actions/") {
		a.handleDeploymentManagementAction(w, r)
		return
	}
	if r.URL.Path != "/deploy-mgr" && r.URL.Path != "/deploy-mgr/" {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if a == nil || a.App == nil || a.App.DeploymentManager == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "deployment_overview_unavailable", "deployment service is not available", nil)
		return
	}
	if !server.RequestHostMatchesBaseURL(r.Host, a.App.Admin.BaseURL) {
		http.NotFound(w, r)
		return
	}
	deployments, err := a.App.DeploymentManager.List(r.Context())
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "deployment_overview_failed", err.Error(), nil)
		return
	}
	items := make([]deploymentOverviewItem, 0, len(deployments))
	for _, item := range deployments {
		isDynamic := item.Type == deployment.TypeDynamic
		canStart := isDynamic && (item.Status == "stopped" || item.Status == "failed")
		canStop := isDynamic && !canStart && item.Status != "deleting" && item.Status != "stopping"
		items = append(items, deploymentOverviewItem{
			Type: item.Type, Name: item.Site, URL: item.URL, Access: item.Access, Status: item.Status,
			Index: item.Index, SPA: item.SPA, Hostname: item.Hostname, Health: item.Health,
			ReleaseID: item.ReleaseID, UpdatedAt: item.UpdatedAt,
			CanStop: canStop, CanStart: canStart,
			StopURL:     deploymentActionURL(a.App.Admin.BaseURL, item.Type, item.Site, "stop"),
			StartURL:    deploymentActionURL(a.App.Admin.BaseURL, item.Type, item.Site, "start"),
			DeleteURL:   deploymentActionURL(a.App.Admin.BaseURL, item.Type, item.Site, "delete"),
			AccessURL:   deploymentActionURL(a.App.Admin.BaseURL, item.Type, item.Site, map[bool]string{true: "set-public", false: "set-private"}[item.Access == deployment.AccessPrivate]),
			AccessLabel: map[bool]string{true: "Make public", false: "Make private"}[item.Access == deployment.AccessPrivate],
		})
	}
	setDeploymentManagementHeaders(w)
	noticeKind := ""
	if r.URL.Query().Get("level") == "error" {
		noticeKind = "error"
	}
	if err := deploymentOverviewTemplate.Execute(w, deploymentOverviewView{
		Count: len(items), Manage: true, Notice: strings.TrimSpace(r.URL.Query().Get("notice")), NoticeKind: noticeKind, Deployments: items,
	}); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "deployment_overview_failed", err.Error(), nil)
	}
}

func (a *API) handleDeploymentManagementAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if a == nil || a.App == nil || a.App.DeploymentManager == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "deployment_management_unavailable", "deployment service is not available", nil)
		return
	}
	if !server.RequestHostMatchesBaseURL(r.Host, a.App.Admin.BaseURL) {
		http.NotFound(w, r)
		return
	}
	if !a.validateDeploymentManagementOrigin(w, r) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/deploy-mgr/actions/"), "/")
	if len(parts) != 3 {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	deploymentType, err := url.PathUnescape(parts[0])
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid deployment type", nil)
		return
	}
	name, err := url.PathUnescape(parts[1])
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid deployment name", nil)
		return
	}
	action, err := url.PathUnescape(parts[2])
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid deployment action", nil)
		return
	}
	if deploymentType != deployment.TypeStatic && deploymentType != deployment.TypeDynamic {
		server.WriteError(w, http.StatusBadRequest, "deployment_type_invalid", "unsupported deployment type", nil)
		return
	}
	var tool string
	switch action {
	case "delete":
		if err := r.ParseForm(); err != nil || r.Form.Get("confirm") != "delete" {
			server.WriteError(w, http.StatusBadRequest, "deployment_delete_confirmation_required", "deployment deletion must be confirmed", nil)
			return
		}
		tool = "delete_deployment"
	case "stop":
		if deploymentType != deployment.TypeDynamic {
			server.WriteError(w, http.StatusBadRequest, "deployment_action_invalid", "only dynamic deployments can be stopped", nil)
			return
		}
		tool = "stop_deployment"
	case "start":
		if deploymentType != deployment.TypeDynamic {
			server.WriteError(w, http.StatusBadRequest, "deployment_action_invalid", "only dynamic deployments can be started", nil)
			return
		}
		tool = "start_deployment"
	case "set-public", "set-private":
		tool = "set_deployment_access"
	default:
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	principal := server.PrincipalFromRequest(r)
	requester := strings.TrimSpace(principal.ID)
	if requester == "" {
		requester = "deployment-ui"
	}
	submission, err := a.App.SubmitToolTaskWithMeta(invocation.Meta{
		Transport: invocation.TransportAdmin, Requester: requester,
	}, tool, func() map[string]any {
		args := map[string]any{"type": deploymentType, "name": name}
		if action == "set-public" {
			args["access"] = deployment.AccessPublic
		} else if action == "set-private" {
			args["access"] = deployment.AccessPrivate
		}
		return args
	}())
	if err != nil {
		a.redirectDeploymentManagement(w, r, "error", "Operation failed: "+err.Error())
		return
	}
	if submission.Approval != nil {
		http.Redirect(w, r, submission.Approval.ApprovalURL, http.StatusSeeOther)
		return
	}
	task, waitErr := waitForDeploymentManagementTask(r, a, submission.Task.ID, 15*time.Second)
	if waitErr != nil {
		a.redirectDeploymentManagement(w, r, "error", "Operation failed: "+waitErr.Error())
		return
	}
	if task.Status == core.StatusSucceeded {
		a.redirectDeploymentManagement(w, r, "", deploymentActionSuccessMessage(action, deploymentType, name))
		return
	}
	message := strings.TrimSpace(task.ErrorMsg)
	if message == "" {
		message = "task ended with status " + string(task.Status)
	}
	a.redirectDeploymentManagement(w, r, "error", "Operation failed: "+message)
}
