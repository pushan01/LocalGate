package deployment

import (
	"context"
	"errors"
	"html/template"
	"net/http"
)

type deploymentListView struct {
	Access string
	Count  int
	Items  []View
}

var deploymentListTemplate = template.Must(template.New("deployment-list").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>LocalGate {{.Access}} deployments</title><style>:root{color-scheme:light dark;font-family:system-ui,sans-serif}body{margin:0;background:#f4f6f8;color:#17202a}main{width:min(1050px,calc(100% - 32px));margin:48px auto}header{display:flex;justify-content:space-between;align-items:end;gap:20px}.muted{color:#667085}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(290px,1fr));gap:16px;margin-top:24px}.card{display:flex;flex-direction:column;padding:20px;border:1px solid #d8dee6;border-radius:16px;background:#fff}.row{display:flex;justify-content:space-between;gap:12px}.badge{font-size:12px;font-weight:700;text-transform:uppercase}.name{overflow-wrap:anywhere}dl{display:grid;grid-template-columns:auto 1fr;gap:7px 12px;font-size:14px}dt{color:#667085}dd{margin:0;overflow-wrap:anywhere}.open{margin-top:auto;padding:10px 12px;border-radius:9px;background:#101828;color:#fff;text-align:center;text-decoration:none;font-weight:700}.empty{margin-top:24px;padding:40px;border:1px dashed #98a2b3;border-radius:16px;text-align:center}@media(prefers-color-scheme:dark){body{background:#0c111d;color:#f2f4f7}.card{background:#161b26;border-color:#344054}.muted,dt{color:#98a2b3}.open{background:#f2f4f7;color:#101828}}</style></head><body><main><header><div><h1>{{.Access}} deployments</h1><p class="muted">View LocalGate deployments exposed through this {{.Access}} listener.</p></div><strong>{{.Count}} total</strong></header>{{if .Items}}<section class="grid">{{range .Items}}<article class="card"><div class="row"><span class="badge">{{.Type}}</span><span class="badge">{{.Status}}</span></div><h2 class="name">{{.Site}}</h2><dl>{{if eq .Type "static"}}<dt>Index</dt><dd>{{.Index}}</dd><dt>SPA</dt><dd>{{.SPA}}</dd>{{else}}<dt>Health</dt><dd>{{.Health}}</dd><dt>Hostname</dt><dd>{{.Hostname}}</dd><dt>Release</dt><dd>{{.ReleaseID}}</dd>{{end}}<dt>Access</dt><dd>{{.Access}}</dd><dt>Updated</dt><dd>{{.UpdatedAt}}</dd><dt>URL</dt><dd>{{.URL}}</dd></dl><a class="open" href="{{.URL}}" target="_blank" rel="noopener noreferrer">Open deployment</a></article>{{end}}</section>{{else}}<div class="empty">No {{.Access}} deployments yet.</div>{{end}}</main></body></html>`))

func (m *Manager) ListByAccess(ctx context.Context, access string) ([]View, error) {
	var ok bool
	access, ok = strictAccess(access)
	if !ok {
		return nil, errors.New("access must be public or private")
	}
	items, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]View, 0, len(items))
	for _, item := range items {
		if normalizeAccess(item.Access, AccessPublic) == access {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (m *Manager) serveDeploymentList(access string, w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := m.ListByAccess(r.Context(), access)
	if err != nil {
		http.Error(w, "deployment list failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := deploymentListTemplate.Execute(w, deploymentListView{Access: access, Count: len(items), Items: items}); err != nil {
		http.Error(w, "deployment list failed", http.StatusInternalServerError)
	}
}
