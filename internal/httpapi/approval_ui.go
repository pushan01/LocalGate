package httpapi

import (
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"localgate/internal/approval"
	"localgate/internal/server"
)

type approvalManagementItem struct {
	ID         string
	Status     approval.Status
	Summary    string
	Tool       string
	RiskLevel  string
	Requester  string
	TaskID     string
	CreatedAt  string
	ExpiresAt  string
	DecidedAt  string
	DetailURL  string
	ApproveURL string
	RejectURL  string
	Pending    bool
}

type approvalManagementView struct {
	Status       string
	Count        int
	PendingCount int
	Items        []approvalManagementItem
	FilterURLs   map[string]string
}

var approvalManagementTemplate = template.Must(template.New("approval-management").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>LocalGate Approval Management</title>
<style>
:root{color-scheme:light dark;font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}body{margin:0;background:#f5f6f8;color:#191b1f}.wrap{max-width:1180px;margin:0 auto;padding:32px 20px 60px}.top{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:22px}.top h1{margin:0;font-size:30px}.muted{color:#68707c}.filters{display:flex;flex-wrap:wrap;gap:8px;margin:18px 0}.filters a{text-decoration:none;color:#303743;background:#fff;border:1px solid #d8dce3;border-radius:999px;padding:8px 13px}.filters a.active{background:#191b1f;color:#fff;border-color:#191b1f}.summary{font-size:14px}.empty{background:#fff;border:1px solid #dde1e7;border-radius:14px;padding:36px;text-align:center}.cards{display:grid;gap:14px}.card{background:#fff;border:1px solid #dde1e7;border-radius:14px;padding:18px;box-shadow:0 2px 8px rgba(0,0,0,.035)}.row{display:flex;justify-content:space-between;gap:16px;align-items:flex-start}.title{font-size:17px;font-weight:700;margin:0 0 7px}.meta{display:flex;flex-wrap:wrap;gap:7px 14px;font-size:13px;color:#626b77}.badge{display:inline-block;padding:4px 9px;border-radius:999px;font-size:12px;font-weight:700;text-transform:uppercase}.pending{background:#fff2cc;color:#795700}.approved{background:#dcf5e7;color:#176b3a}.rejected,.canceled,.expired{background:#f5dfe2;color:#8c2430}.actions{display:flex;gap:8px;align-items:center;margin-top:16px}.actions a,.actions button{font:inherit;border-radius:9px;padding:8px 12px;border:1px solid #cfd4dc;background:#fff;color:#252a31;text-decoration:none;cursor:pointer}.actions button.approve{background:#176b3a;color:#fff;border-color:#176b3a}.actions button.reject{background:#fff;color:#8c2430;border-color:#d8aab0}.id{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;color:#7a828d;word-break:break-all;margin-top:10px}@media(prefers-color-scheme:dark){body{background:#111317;color:#eef0f3}.card,.empty,.filters a,.actions a,.actions button{background:#191c21;border-color:#343941;color:#eef0f3}.filters a.active{background:#eef0f3;color:#17191d}.muted,.meta,.id{color:#a8afb9}}
</style></head>
<body><main class="wrap">
<div class="top"><div><h1>Approval management</h1><div class="muted">Review and resolve LocalGate approval requests.</div></div><div class="summary"><strong>{{.PendingCount}}</strong> pending · <strong>{{.Count}}</strong> shown</div></div>
<nav class="filters">
<a href="{{index .FilterURLs "pending"}}" class="{{if eq .Status "pending"}}active{{end}}">Pending</a>
<a href="{{index .FilterURLs "all"}}" class="{{if eq .Status "all"}}active{{end}}">All</a>
<a href="{{index .FilterURLs "approved"}}" class="{{if eq .Status "approved"}}active{{end}}">Approved</a>
<a href="{{index .FilterURLs "rejected"}}" class="{{if eq .Status "rejected"}}active{{end}}">Rejected</a>
<a href="{{index .FilterURLs "canceled"}}" class="{{if eq .Status "canceled"}}active{{end}}">Canceled</a>
<a href="{{index .FilterURLs "expired"}}" class="{{if eq .Status "expired"}}active{{end}}">Expired</a>
</nav>
{{if .Items}}<section class="cards">{{range .Items}}
<article class="card"><div class="row"><div><p class="title">{{if .Summary}}{{.Summary}}{{else}}{{.Tool}}{{end}}</p><div class="meta"><span>Tool: <strong>{{.Tool}}</strong></span><span>Risk: <strong>{{.RiskLevel}}</strong></span>{{if .Requester}}<span>Requester: <strong>{{.Requester}}</strong></span>{{end}}<span>Created: {{.CreatedAt}}</span>{{if .ExpiresAt}}<span>Expires: {{.ExpiresAt}}</span>{{end}}{{if .DecidedAt}}<span>Decided: {{.DecidedAt}}</span>{{end}}</div></div><span class="badge {{.Status}}">{{.Status}}</span></div>
<div class="id">Approval {{.ID}}{{if .TaskID}} · Task {{.TaskID}}{{end}}</div>
<div class="actions"><a href="{{.DetailURL}}">View details</a>{{if .Pending}}<form method="post" action="{{.ApproveURL}}"><button class="approve" type="submit">Approve and execute</button></form><form method="post" action="{{.RejectURL}}"><button class="reject" type="submit">Reject</button></form>{{end}}</div></article>
{{end}}</section>{{else}}<div class="empty"><strong>No approval requests found.</strong><p class="muted">Choose another status filter or wait for a new request.</p></div>{{end}}
</main></body></html>`))

func (a *API) requireApprovalURL(w http.ResponseWriter, r *http.Request) bool {
	if a == nil || a.App == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "approval_service_unavailable", "approval service is not available", nil)
		return false
	}
	if !server.RequestHostMatchesBaseURL(r.Host, a.App.Admin.BaseURL) {
		http.NotFound(w, r)
		return false
	}
	return true
}

func (a *API) handleApprovalManagementUI(w http.ResponseWriter, r *http.Request) {
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	if status == "" {
		status = "pending"
	}
	valid := map[string]bool{"all": true, "pending": true, "approved": true, "rejected": true, "canceled": true, "expired": true}
	if !valid[status] {
		server.WriteError(w, http.StatusBadRequest, "approval_status_invalid", "invalid approval status", nil)
		return
	}
	requests, err := a.App.Approvals.List("")
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "approval_list_failed", err.Error(), nil)
		return
	}
	pendingCount := 0
	items := make([]approvalManagementItem, 0, len(requests))
	for _, request := range requests {
		if request.Status == approval.StatusPending {
			pendingCount++
		}
		if status != "all" && string(request.Status) != status {
			continue
		}
		detailURL := strings.TrimRight(strings.TrimSpace(a.App.Admin.BaseURL), "/") + "/approval-ui/" + url.PathEscape(request.ID)
		if strings.TrimSpace(request.ApprovalURL) != "" {
			detailURL = request.ApprovalURL
		}
		items = append(items, approvalManagementItem{
			ID: request.ID, Status: request.Status, Summary: request.Summary, Tool: request.Tool,
			RiskLevel: request.RiskLevel, Requester: request.Requester,
			TaskID: request.TaskID, CreatedAt: formatApprovalTime(request.CreatedAt),
			ExpiresAt: formatApprovalTime(request.ExpiresAt), DecidedAt: formatApprovalTime(request.DecidedAt),
			DetailURL:  detailURL,
			ApproveURL: approvalActionURL(a.App.Admin.BaseURL, request.ID, "approve"),
			RejectURL:  approvalActionURL(a.App.Admin.BaseURL, request.ID, "reject"),
			Pending:    request.Status == approval.StatusPending,
		})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	filters := map[string]string{}
	for _, filter := range []string{"all", "pending", "approved", "rejected", "canceled", "expired"} {
		filters[filter] = approvalManagementURL(a.App.Admin.BaseURL, filter)
	}
	setApprovalPageHeaders(w)
	if err := approvalManagementTemplate.Execute(w, approvalManagementView{
		Status: status, Count: len(items), PendingCount: pendingCount, Items: items, FilterURLs: filters,
	}); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "approval_page_failed", err.Error(), nil)
	}
}

func setApprovalPageHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

func approvalManagementURL(baseURL, status string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/approval-ui"
	if status == "" || status == "pending" {
		return base
	}
	return base + "?status=" + url.QueryEscape(status)
}

func formatApprovalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}
