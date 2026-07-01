package httpapi

import (
	"html/template"
	"net/http"

	"localgate/internal/server"
)

type deploymentListView struct {
	Count int
	Items []deploymentOverviewItem
}

var deploymentListTemplate = template.Must(template.New("deployment-list").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>LocalGate Deployments</title><style>:root{color-scheme:light dark;font-family:system-ui,sans-serif}body{margin:0;background:#f4f6f8;color:#17202a}main{width:min(1050px,calc(100% - 32px));margin:48px auto}header{display:flex;justify-content:space-between;align-items:end;gap:20px}.muted{color:#667085}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(290px,1fr));gap:16px;margin-top:24px}.card{display:flex;flex-direction:column;padding:20px;border:1px solid #d8dee6;border-radius:16px;background:#fff}.row{display:flex;justify-content:space-between;gap:12px}.badge{font-size:12px;font-weight:700;text-transform:uppercase}.name{overflow-wrap:anywhere}dl{display:grid;grid-template-columns:auto 1fr;gap:7px 12px;font-size:14px}dt{color:#667085}dd{margin:0;overflow-wrap:anywhere}.open{margin-top:auto;padding:10px 12px;border-radius:9px;background:#101828;color:#fff;text-align:center;text-decoration:none;font-weight:700}.empty{margin-top:24px;padding:40px;border:1px dashed #98a2b3;border-radius:16px;text-align:center}@media(prefers-color-scheme:dark){body{background:#0c111d;color:#f2f4f7}.card{background:#161b26;border-color:#344054}.muted,dt{color:#98a2b3}.open{background:#f2f4f7;color:#101828}}</style></head><body><main><header><div><h1>Deployments</h1><p class="muted">View applications currently published by LocalGate.</p></div><strong>{{.Count}} total</strong></header>{{if .Items}}<section class="grid">{{range .Items}}<article class="card"><div class="row"><span class="badge">{{.Type}}</span><span class="badge">{{.Status}}</span></div><h2 class="name">{{.Name}}</h2><dl>{{if eq .Type "static"}}<dt>Index</dt><dd>{{.Index}}</dd><dt>SPA</dt><dd>{{.SPA}}</dd>{{else}}<dt>Health</dt><dd>{{.Health}}</dd><dt>Hostname</dt><dd>{{.Hostname}}</dd><dt>Release</dt><dd>{{.ReleaseID}}</dd>{{end}}<dt>Updated</dt><dd>{{.UpdatedAt}}</dd><dt>URL</dt><dd>{{.URL}}</dd></dl><a class="open" href="{{.URL}}" target="_blank" rel="noopener noreferrer">Open deployment</a></article>{{end}}</section>{{else}}<div class="empty">No deployments yet.</div>{{end}}</main></body></html>`))

func (a *API) handleDeploymentList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/deploys" && r.URL.Path != "/deploys/" {
		http.NotFound(w, r)
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
	if !server.RequestHostMatchesBaseURL(r.Host, a.App.HTTPBaseURL) {
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
		items = append(items, deploymentOverviewItem{Type: item.Type, Name: item.Site, URL: item.URL, Status: item.Status, Index: item.Index, SPA: item.SPA, Hostname: item.Hostname, Health: item.Health, ReleaseID: item.ReleaseID, UpdatedAt: item.UpdatedAt})
	}
	setDeploymentManagementHeaders(w)
	if err := deploymentListTemplate.Execute(w, deploymentListView{Count: len(items), Items: items}); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "deployment_overview_failed", err.Error(), nil)
	}
}
