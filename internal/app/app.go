package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"localgate/internal/approval"
	"localgate/internal/capabilities/filesystem"
	"localgate/internal/capabilities/meta"
	"localgate/internal/capabilities/shell"
	toolcap "localgate/internal/capabilities/toolaccess"
	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/deployment"
	"localgate/internal/download"
	"localgate/internal/dynamic"
	"localgate/internal/invocation"
	mcptools "localgate/internal/mcp/tools"
	"localgate/internal/mcprelay"
	"localgate/internal/persist"
	"localgate/internal/static"
	"localgate/internal/toolaccess"
	"localgate/internal/upgrade"
	"localgate/internal/upload"
)

const downstreamMCPStartupTimeout = 5 * time.Second

var ErrApprovalPolicyDenied = errors.New("tool execution denied by approval policy")

type App struct {
	Sink              *core.EventSink
	Tasks             *core.Manager
	Sessions          *core.SessionManager
	Workspaces        map[string]config.Workspace
	Registries        map[string]*core.Registry
	Approvals         *approval.Service
	Approval          config.ApprovalConfig
	Admin             config.AdminConfig
	Downloads         *download.Service
	Uploads           *upload.Service
	ToolAccess        *toolaccess.Service
	ToolArguments     config.ToolArguments
	ToolHTTPMode      config.ToolHTTPMode
	StaticHosting     config.StaticHosting
	StaticService     *static.Service
	DynamicHosting    config.DynamicHosting
	DynamicService    *dynamic.Service
	DeploymentManager *deployment.Manager
	HTTPBaseURL       string
	StoreBase         string
	Upgrades          *upgrade.Service
	Relays            map[string]*mcprelay.Client
	RelayErrors       map[string]string
	DefaultWSID       string
	stopJanitor       context.CancelFunc
	approvalMu        sync.Mutex
	approvalGates     map[string]func()
}

func New(cfg config.Config) (*App, error) {
	cfg.Deployments = config.NormalizeDeploymentsConfig(cfg.Deployments, cfg.HTTP.BaseURL)
	adminConfig := config.NormalizeAdminConfig(cfg.Admin, cfg.HTTP)
	approvalConfig := adminConfig.Approval
	sink := core.NewEventSink()
	tm := core.NewManagerWithOptions(sink, cfg.MaxConcurrency)
	sessions := core.NewSessionManager()
	sessions.SetCleanupOptions(core.SessionCleanupOptions{
		FinishedTTL: time.Duration(cfg.SessionCleanup.FinishedTTLSeconds) * time.Second,
		IdleTTL:     time.Duration(cfg.SessionCleanup.IdleTTLSeconds) * time.Second,
		MaxSessions: cfg.SessionCleanup.MaxSessions,
	})
	storeBase := cfg.LogStore
	if storeBase == "" {
		storeBase = ".localgate"
	}
	mainStore := persist.NewStore(storeBase)
	sink.SetWriter(mainStore)
	tm.SetTaskWriter(mainStore)
	if tasks, err := mainStore.LoadTasks(); err == nil && len(tasks) > 0 {
		tm.LoadHistory(tasks)
	}
	if err := tm.RecoverInterruptedTasks(); err != nil {
		return nil, fmt.Errorf("recover interrupted tasks: %w", err)
	}
	approvalSvc := approval.NewServiceWithOptions(approval.NewStore(filepath.Join(storeBase, "approval")), approval.Options{
		BaseURL:      adminConfig.BaseURL,
		ApprovalPath: "/admin/approvals/",
		TTL:          time.Duration(approvalConfig.DefaultTTLSeconds) * time.Second,
	})
	resolvedApprovals, err := approvalSvc.ReconcilePendingOnStartup("system:restart")
	if err != nil {
		return nil, fmt.Errorf("reconcile pending approvals: %w", err)
	}
	for _, req := range resolvedApprovals {
		if strings.TrimSpace(req.TaskID) == "" {
			continue
		}
		if err := tm.CancelRecoveredApproval(req.TaskID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return nil, fmt.Errorf("cancel recovered approval task %s: %w", req.TaskID, err)
		}
	}
	staticSvc := static.NewServiceAtWithLimits(filepath.Join(storeBase, "static"), static.Limits{
		MaxFiles:      cfg.Deployments.Static.MaxFiles,
		MaxTotalBytes: cfg.Deployments.Static.MaxTotalBytes,
		MaxFileBytes:  cfg.Deployments.Static.MaxFileBytes,
	})
	downloadSvc := download.NewService()
	uploadSvc := upload.NewService()
	toolAccessSvc := toolaccess.NewService()
	upgradeSvc := upgrade.NewService(filepath.Join(storeBase, "upgrades"), cfg.Upgrade.Token, cfg.Runtime)

	wss := make(map[string]config.Workspace, len(cfg.Workspaces))
	defaultWS := ""
	for _, ws := range cfg.Workspaces {
		if ws.ID == "" {
			return nil, errors.New("workspace id empty")
		}
		if defaultWS == "" {
			defaultWS = ws.ID
		}
		wss[ws.ID] = ws
	}
	ownsDynamicRuntime := cfg.Runtime.Mode != "mcp_stdio"
	dynamicSvc, err := dynamic.NewService(filepath.Join(storeBase, "dynamic"), cfg.Deployments.Dynamic, wss, ownsDynamicRuntime)
	if err != nil {
		return nil, fmt.Errorf("init dynamic hosting: %w", err)
	}
	dynamicSvc.SetEventSink(func(event dynamic.Event) {
		details := map[string]any{
			"type": dynamic.Type, "name": event.Name, "release_id": event.ReleaseID,
			"executable": event.Executable, "hostname": event.Hostname,
			"health_path": event.HealthPath, "restart_count": event.RestartCount,
		}
		for key, value := range event.Details {
			details[key] = value
		}
		requester := strings.TrimSpace(event.Operation.Requester)
		if requester == "" {
			requester = "system"
		}
		tool := strings.TrimSpace(event.Operation.Tool)
		if tool == "" {
			tool = "dynamic_supervisor"
		}
		operationID := event.Operation.TaskID
		if operationID == "" {
			operationID = "dynamic:" + event.Name + ":" + event.Action
		}
		_, _ = approvalSvc.RecordAudit(approval.AuditRecord{
			ApprovalID: event.Operation.ApprovalID, OperationID: operationID, TaskID: event.Operation.TaskID,
			Action: event.Action, Tool: tool, WorkspaceID: event.WorkspaceID, Requester: requester,
			Transport: event.Operation.Transport, MCPSessionID: event.Operation.MCPSessionID,
			SessionMode: event.Operation.SessionMode, SessionIDSource: event.Operation.SessionIDSource,
			RequestID: event.Operation.RequestID,
			Error:     event.Error, Details: details, CreatedAt: event.CreatedAt,
		})
	})
	deploymentManager := deployment.NewManager(staticSvc, dynamicSvc, "")
	dynamicRoutingConfig := dynamicSvc.Config()
	deploymentManager.ConfigureAppHosting(dynamicRoutingConfig.PublicBaseURL, dynamicRoutingConfig.PrivateBaseURL, dynamicRoutingConfig.DefaultAccess)
	deploymentManager.ConfigureAppRouting(cfg.HTTP.BaseURL, dynamicRoutingConfig.ReservedNames)
	regs := make(map[string]*core.Registry, len(cfg.Workspaces))
	for _, ws := range cfg.Workspaces {
		reg := core.NewRegistry(sink)
		reg.Register(meta.NewStartupInstructionsTool(ws, reg, meta.StartupPromptOptions{
			BaseURL:      cfg.HTTP.BaseURL,
			ToolHTTPMode: cfg.ToolHTTPMode.Enabled,
			EnableBase64: cfg.ToolArguments.EnableBase64,
		}))
		for _, t := range filesystem.NewTools(ws) {
			reg.Register(t)
		}
		reg.Register(filesystem.NewCreateDownloadTokenTool(ws, cfg.HTTP.BaseURL, downloadSvc))
		reg.Register(filesystem.NewCreateUploadTokenTool(ws, cfg.HTTP.BaseURL, uploadSvc))
		reg.Register(shell.NewTool(ws, cfg.DefaultTimeoutMs))
		reg.Register(shell.NewArgvTool(ws, cfg.DefaultTimeoutMs))
		reg.Register(shell.NewExecCommandTool(ws, sessions))
		reg.Register(shell.NewWriteStdinTool(sessions))
		reg.Register(shell.NewReadThreadTerminalTool(sessions))
		reg.Register(toolcap.NewGetToolSchemaTool(reg))
		deployRequiresApproval := approvalConfig.Enabled && (cfg.Deployments.Static.RequireApproval || cfg.Deployments.Dynamic.RequireApproval)
		reg.Register(mcptools.NewCoreDeployToolWithManager(deploymentManager, ws, approvalSvc, deployRequiresApproval))
		reg.Register(mcptools.NewCoreDeleteDeploymentToolWithManager(deploymentManager, approvalSvc, deployRequiresApproval))
		reg.Register(mcptools.NewCoreSetDeploymentAccessToolWithManager(deploymentManager, deployRequiresApproval))
		reg.Register(mcptools.NewCoreListDeploymentsToolWithManager(deploymentManager))
		if cfg.Deployments.Dynamic.Enabled {
			for _, tool := range mcptools.NewCoreDynamicDeploymentToolsWithManager(deploymentManager, approvalSvc) {
				reg.Register(tool)
			}
		}
		if cfg.ToolHTTPMode.Enabled {
			ttl := time.Duration(cfg.ToolHTTPMode.AccessTokenTTLSeconds) * time.Second
			reg.Register(toolcap.NewGetTemporaryAccessTokenTool(cfg.HTTP.BaseURL, ttl, toolAccessSvc))
		}
		for _, name := range config.MergeHiddenTools(ws.HiddenTools) {
			reg.Hide(name)
		}
		regs[ws.ID] = reg
	}

	relays := make(map[string]*mcprelay.Client, len(cfg.MCPServers))
	relayErrors := make(map[string]string)
	if !cfg.ToolHTTPMode.Enabled {
		for _, srv := range cfg.MCPServers {
			startCtx, cancel := context.WithTimeout(context.Background(), downstreamMCPStartupTimeout)
			client, err := mcprelay.Start(startCtx, srv)
			cancel()
			if err != nil {
				relayErrors[srv.Name] = err.Error()
				continue
			}
			relays[srv.Name] = client
		}
	}

	app := &App{
		Sink:              sink,
		Tasks:             tm,
		Sessions:          sessions,
		Workspaces:        wss,
		Registries:        regs,
		Approvals:         approvalSvc,
		Approval:          approvalConfig,
		Admin:             adminConfig,
		Downloads:         downloadSvc,
		Uploads:           uploadSvc,
		ToolAccess:        toolAccessSvc,
		ToolArguments:     cfg.ToolArguments,
		ToolHTTPMode:      cfg.ToolHTTPMode,
		StaticHosting:     cfg.Deployments.Static,
		StaticService:     staticSvc,
		DynamicHosting:    cfg.Deployments.Dynamic,
		DynamicService:    dynamicSvc,
		DeploymentManager: deploymentManager,
		HTTPBaseURL:       cfg.HTTP.BaseURL,
		StoreBase:         storeBase,
		Upgrades:          upgradeSvc,
		Relays:            relays,
		RelayErrors:       relayErrors,
		DefaultWSID:       defaultWS,
		approvalGates:     map[string]func(){},
	}
	if cfg.SessionCleanup.SweepIntervalMs > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		app.stopJanitor = cancel
		go app.runSessionJanitor(ctx, time.Duration(cfg.SessionCleanup.SweepIntervalMs)*time.Millisecond)
	}
	return app, nil
}

type Submission struct {
	Task     *core.Task
	Approval *approval.Request
}

func (a *App) CreateToolTask(wsid, requester, tool string, args map[string]any) (*core.Task, error) {
	return a.CreateToolTaskWithMeta(invocation.Meta{
		WorkspaceID: wsid,
		Requester:   requester,
		Transport:   invocation.TransportREST,
	}, tool, args)
}

func (a *App) CreateToolTaskWithApproval(wsid, requester, tool string, args map[string]any, approvalID string) (*core.Task, error) {
	return a.CreateToolTaskWithMetaAndApproval(invocation.Meta{
		WorkspaceID: wsid,
		Requester:   requester,
		Transport:   invocation.TransportREST,
	}, tool, args, approvalID)
}

func (a *App) CreateToolTaskWithMeta(meta invocation.Meta, tool string, args map[string]any) (*core.Task, error) {
	return a.createToolTask(meta, tool, args, "", true)
}

func (a *App) CreateToolTaskWithMetaAndApproval(meta invocation.Meta, tool string, args map[string]any, approvalID string) (*core.Task, error) {
	return a.createToolTask(meta, tool, args, approvalID, strings.TrimSpace(approvalID) != "")
}

func (a *App) createToolTask(meta invocation.Meta, tool string, args map[string]any, approvalID string, authorized bool) (*core.Task, error) {
	meta = meta.Normalize()
	wsid := meta.WorkspaceID
	if wsid == "" {
		wsid = a.DefaultWSID
		meta.WorkspaceID = wsid
	}
	requester := meta.Requester
	reg, ok := a.Registries[wsid]
	if !ok {
		return nil, errors.New("workspace not found")
	}
	run := func(ctx context.Context, taskID string) (int, error) {
		res, err := reg.Invoke(ctx, tool, core.InvokeContext{
			TaskID:      taskID,
			WorkspaceID: wsid,
			Requester:   requester,
			ApprovalID:  approvalID,
			Transport:   meta.Transport, MCPSessionID: meta.MCPSessionID,
			SessionMode: meta.SessionMode, SessionIDSource: meta.SessionIDSource, RequestID: meta.RequestID,
			Authorized: authorized,
			Sink:       a.Sink,
		}, args)
		if res != nil {
			_ = a.Tasks.SetResult(taskID, res)
		}
		exit := 0
		if res != nil {
			if v, ok := res["exit_code"].(int); ok {
				exit = v
			} else if v, ok := res["exit_code"].(int64); ok {
				exit = int(v)
			} else if v, ok := res["exit_code"].(float64); ok {
				exit = int(v)
			}
		}
		return exit, err
	}
	task := a.Tasks.CreateTask(core.Task{
		Transport: meta.Transport, MCPSessionID: meta.MCPSessionID,
		SessionMode: meta.SessionMode, SessionIDSource: meta.SessionIDSource, RequestID: meta.RequestID,
		Requester:   requester,
		WorkspaceID: wsid,
		Tool:        tool,
		ArgsDigest:  approval.Digest(wsid, tool, requester, args),
		ApprovalID:  approvalID,
	}, run)
	return task, nil
}

func (a *App) SubmitToolTask(wsid, requester, tool string, args map[string]any) (Submission, error) {
	return a.SubmitToolTaskWithMeta(invocation.Meta{
		WorkspaceID: wsid,
		Requester:   requester,
		Transport:   invocation.TransportREST,
	}, tool, args)
}

func (a *App) SubmitToolTaskWithMeta(meta invocation.Meta, tool string, args map[string]any) (Submission, error) {
	meta = meta.Normalize()
	wsid := meta.WorkspaceID
	if wsid == "" {
		wsid = a.DefaultWSID
		meta.WorkspaceID = wsid
	}
	requester := meta.Requester
	if a.Approvals != nil && a.Approval.Enabled {
		ws, ok := a.Workspaces[wsid]
		if !ok {
			return Submission{}, errors.New("workspace not found")
		}
		needApproval := false
		reason := ""
		risk := approvalRisk(tool)
		if effect, ruleID, ruleRisk, matched := a.matchApprovalRule(tool); matched {
			switch effect {
			case "deny":
				return Submission{}, fmt.Errorf("%w: rule %s", ErrApprovalPolicyDenied, ruleID)
			case "allow":
				needApproval = false
				reason = ""
			case "require_approval":
				needApproval = true
				reason = "approval.rule." + ruleID
				if strings.TrimSpace(ruleRisk) != "" {
					risk = ruleRisk
				}
			}
			if needApproval && isDynamicMutation(tool, args) {
				if tool == "deploy_tool" || tool == "delete_deployment" {
					risk = "critical"
				}
			}
		} else if isDynamicMutation(tool, args) && a.DynamicHosting.RequireApproval {
			needApproval = true
			reason = "approval.policy.dynamic_" + tool
			risk = "critical"
			if tool == "restart_deployment" || tool == "start_deployment" || tool == "stop_deployment" || tool == "rollback_deployment" {
				risk = "high"
			}
		} else if (tool == "deploy_tool" || tool == "delete_deployment" || tool == "set_deployment_access") && a.StaticHosting.RequireApproval {
			needApproval = true
			reason = "approval.policy.static_deploy"
			if tool == "delete_deployment" {
				reason = "approval.policy.static_delete"
			} else if tool == "set_deployment_access" {
				reason = "approval.policy.deployment_access"
			}
		} else if need, policyReason := a.Approvals.RequiresApproval(tool, ws, args); need {
			needApproval = true
			reason = policyReason
		}
		if needApproval {
			req, err := a.Approvals.Create(approval.Request{
				Tool:        tool,
				WorkspaceID: wsid,
				Requester:   requester,
				Transport:   meta.Transport, MCPSessionID: meta.MCPSessionID,
				SessionMode: meta.SessionMode, SessionIDSource: meta.SessionIDSource, RequestID: meta.RequestID,
				Execution: approval.ExecutionAppTool,
				Reason:    reason,
				Summary:   approvalSummary(tool, args),
				RiskLevel: risk,
				Args:      args,
			})
			if err != nil {
				return Submission{}, err
			}
			reg := a.Registries[wsid]
			run := func(ctx context.Context, taskID string) (int, error) {
				res, invokeErr := reg.Invoke(ctx, tool, core.InvokeContext{
					TaskID:      taskID,
					WorkspaceID: wsid,
					Requester:   requester,
					ApprovalID:  req.ID,
					Transport:   meta.Transport, MCPSessionID: meta.MCPSessionID,
					SessionMode: meta.SessionMode, SessionIDSource: meta.SessionIDSource, RequestID: meta.RequestID,
					Authorized: true,
					Sink:       a.Sink,
				}, args)
				if res != nil {
					_ = a.Tasks.SetResult(taskID, res)
				}
				if invokeErr != nil {
					return -1, invokeErr
				}
				return 0, nil
			}
			task, release := a.Tasks.CreateGatedTask(core.Task{
				Transport: meta.Transport, MCPSessionID: meta.MCPSessionID,
				SessionMode: meta.SessionMode, SessionIDSource: meta.SessionIDSource, RequestID: meta.RequestID,
				Requester:   requester,
				WorkspaceID: wsid,
				Tool:        tool,
				ArgsDigest:  req.ArgsDigest,
				ApprovalID:  req.ID,
				ApprovalURL: req.ApprovalURL,
				ExpiresAt:   req.ExpiresAt,
			}, run)
			bound, err := a.Approvals.BindTask(req.ID, task.ID)
			if err != nil {
				_ = a.Tasks.Cancel(task.ID)
				return Submission{}, err
			}
			a.approvalMu.Lock()
			a.approvalGates[req.ID] = release
			a.approvalMu.Unlock()
			go a.expireApproval(bound)
			return Submission{Task: task, Approval: &bound}, nil
		}
	}
	task, err := a.createToolTask(meta, tool, args, "", true)
	if err != nil {
		return Submission{}, err
	}
	return Submission{Task: task}, nil
}

func (a *App) matchApprovalRule(tool string) (effect, ruleID, risk string, matched bool) {
	for _, rule := range a.Approval.Rules {
		for _, candidate := range rule.Tools {
			if candidate == "*" || candidate == tool {
				return rule.Effect, rule.ID, rule.Risk, true
			}
		}
	}
	return "", "", "", false
}

func (a *App) expireApproval(req approval.Request) {
	if req.ExpiresAt.IsZero() {
		return
	}
	timer := time.NewTimer(time.Until(req.ExpiresAt))
	defer timer.Stop()
	<-timer.C
	if _, err := a.Approvals.Expire(req.ID); err != nil {
		return
	}
	_ = a.CancelApprovalTaskWithReason(req.ID, req.TaskID, "approval_expired", "approval expired before execution")
}

func (a *App) ReleaseApproval(approvalID string) error {
	a.approvalMu.Lock()
	release, ok := a.approvalGates[approvalID]
	if ok {
		delete(a.approvalGates, approvalID)
	}
	a.approvalMu.Unlock()
	if !ok {
		return errors.New("approval gate not found")
	}
	release()
	return nil
}

func (a *App) CancelApprovalTask(approvalID, taskID string) error {
	return a.CancelApprovalTaskWithReason(approvalID, taskID, "approval_canceled", "approval was canceled before execution")
}

func (a *App) CancelApprovalTaskWithReason(approvalID, taskID, code, message string) error {
	a.approvalMu.Lock()
	delete(a.approvalGates, approvalID)
	a.approvalMu.Unlock()
	if taskID == "" {
		return nil
	}
	return a.Tasks.CancelWithReason(taskID, code, message)
}

func (a *App) ApproveApproval(approvalID string, actor approval.Actor) (approval.Request, error) {
	req, err := a.Approvals.Get(approvalID)
	if err != nil {
		return approval.Request{}, err
	}
	if strings.TrimSpace(req.TaskID) == "" {
		return approval.Request{}, approval.ErrTaskIDRequired
	}
	task, err := a.Tasks.Get(req.TaskID)
	if err != nil {
		return approval.Request{}, fmt.Errorf("load approval task: %w", err)
	}
	updated, err := a.Approvals.ApproveBound(approvalID, actor, approval.Binding{
		ApprovalID:  approvalID,
		TaskID:      task.ID,
		WorkspaceID: task.WorkspaceID,
		Requester:   task.Requester,
		Tool:        task.Tool,
		ArgsDigest:  task.ArgsDigest,
		TaskStatus:  string(task.Status),
	})
	if err != nil {
		return approval.Request{}, err
	}
	if err := a.ReleaseApproval(approvalID); err != nil {
		_ = a.CancelApprovalTaskWithReason(approvalID, task.ID, "approval_release_failed", "approval was granted but the task gate could not be released")
		_, _ = a.Approvals.RecordAudit(approval.AuditRecord{
			ApprovalID:  approvalID,
			TaskID:      task.ID,
			Action:      "approval_release_failed",
			Actor:       actor.ID,
			AuthMethod:  actor.AuthMethod,
			Tool:        req.Tool,
			WorkspaceID: req.WorkspaceID,
			Requester:   req.Requester,
			Transport:   req.Transport, MCPSessionID: req.MCPSessionID,
			SessionMode: req.SessionMode, SessionIDSource: req.SessionIDSource, RequestID: req.RequestID,
			Reason: req.Reason,
			Error:  err.Error(),
		})
		return approval.Request{}, err
	}
	return updated, nil
}

func approvalSummary(tool string, args map[string]any) string {
	name, _ := args["name"].(string)
	typeName, _ := args["type"].(string)
	if typeName == "" {
		typeName = "static"
	}
	switch tool {
	case "deploy_tool":
		return "Deploy " + typeName + " site " + name
	case "delete_deployment":
		return "Delete " + typeName + " deployment " + name
	case "set_deployment_access":
		return "Set " + typeName + " deployment " + name + " access to " + fmt.Sprint(args["access"])
	case "restart_deployment", "start_deployment", "stop_deployment", "rollback_deployment":
		return strings.TrimSuffix(tool, "_deployment") + " dynamic deployment " + name
	default:
		return "Execute tool " + tool
	}
}

func isDynamicMutation(tool string, args map[string]any) bool {
	typeName, _ := args["type"].(string)
	switch tool {
	case "deploy_tool", "delete_deployment", "set_deployment_access":
		return typeName == "dynamic"
	case "restart_deployment", "start_deployment", "stop_deployment", "rollback_deployment":
		return true
	default:
		return false
	}
}

func approvalRisk(tool string) string {
	if tool == "delete_deployment" || tool == "set_deployment_access" {
		return "critical"
	}
	return "high"
}

func (a *App) InvokeTool(ctx context.Context, wsid, tool string, args map[string]any) (map[string]any, error) {
	reg, ok := a.Registries[wsid]
	if !ok {
		return nil, errors.New("workspace not found")
	}
	return reg.Invoke(ctx, tool, core.InvokeContext{WorkspaceID: wsid, Requester: "app", Sink: a.Sink}, args)
}

func (a *App) IsToolVisible(wsid, tool string) bool {
	reg, ok := a.Registries[wsid]
	if !ok {
		return false
	}
	return reg.IsVisible(tool)
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	if a.stopJanitor != nil {
		a.stopJanitor()
		a.stopJanitor = nil
	}
	if a.Tasks != nil {
		a.Tasks.Close()
	}
	var closeErrors []error
	if a.DynamicService != nil {
		if err := a.DynamicService.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close dynamic runtime: %w", err))
		}
	}
	for name, relay := range a.Relays {
		if relay == nil {
			delete(a.Relays, name)
			continue
		}
		if err := relay.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close relay %s: %w", name, err))
		}
		delete(a.Relays, name)
	}
	return errors.Join(closeErrors...)
}

func (a *App) runSessionJanitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.Sessions.Sweep(time.Now().UTC())
		}
	}
}
