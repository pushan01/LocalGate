package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	adapterbundle "localgate/adapters"
	"localgate/internal/approval"
	conversationcap "localgate/internal/capabilities/conversation"
	"localgate/internal/capabilities/filesystem"
	"localgate/internal/capabilities/meta"
	notificationcap "localgate/internal/capabilities/notification"
	relaycap "localgate/internal/capabilities/relay"
	"localgate/internal/capabilities/shell"
	subagentcap "localgate/internal/capabilities/subagent"
	taskcap "localgate/internal/capabilities/task"
	toolcap "localgate/internal/capabilities/toolaccess"
	"localgate/internal/config"
	"localgate/internal/conversationhandoff"
	"localgate/internal/core"
	"localgate/internal/deployment"
	"localgate/internal/download"
	"localgate/internal/dynamic"
	"localgate/internal/filebrowser"
	"localgate/internal/fileexport"
	"localgate/internal/invocation"
	mcptools "localgate/internal/mcp/tools"
	"localgate/internal/mcprelay"
	notify "localgate/internal/notification"
	"localgate/internal/persist"
	"localgate/internal/static"
	"localgate/internal/subagent"
	opencligpt "localgate/internal/subagent/providers/opencli_chatgpt"
	opencligemini "localgate/internal/subagent/providers/opencli_gemini"
	"localgate/internal/toolaccess"
	"localgate/internal/upgrade"
	"localgate/internal/upload"
)

const (
	downstreamMCPStartupTimeout = 5 * time.Second
	logStoreRetention           = 48 * time.Hour
	logStoreSweepInterval       = time.Hour
)

var ErrApprovalPolicyDenied = errors.New("tool execution denied by approval policy")

type App struct {
	Sink                *core.EventSink
	Tasks               *core.Manager
	Sessions            *core.SessionManager
	Subagents           *subagent.Manager
	SubagentConfig      config.SubagentsConfig
	ConversationHandoff *conversationhandoff.Service
	Root                config.Root
	Registry            *core.Registry
	Approvals           *approval.Service
	Approval            config.ApprovalConfig
	Admin               config.AdminConfig
	Downloads           *download.Service
	Uploads             *upload.Service
	Exports             *fileexport.Service
	FileExports         config.FileExports
	FileBrowser         config.FileBrowserConfig
	FileBrowserService  *filebrowser.Service
	Notifications       *notify.Service
	NotificationConfig  config.NotificationsConfig
	ToolAccess          *toolaccess.Service
	ToolArguments       config.ToolArguments
	ToolHTTPMode        config.ToolHTTPMode
	StaticHosting       config.StaticHosting
	StaticService       *static.Service
	DynamicHosting      config.DynamicHosting
	DynamicService      *dynamic.Service
	DeploymentManager   *deployment.Manager
	HTTPBaseURL         string
	StoreBase           string
	taskStore           *persist.Store
	Upgrades            *upgrade.Service
	Relays              map[string]*mcprelay.Client
	RelayErrors         map[string]string
	stopJanitor         context.CancelFunc
	approvalMu          sync.Mutex
	approvalGates       map[string]func()
}

func New(cfg config.Config) (*App, error) {
	cfg.Deployments = config.NormalizeDeploymentsConfig(cfg.Deployments, cfg.HTTP.BaseURL)
	cfg.FileExports = config.NormalizeFileExportsConfig(cfg.FileExports, cfg.HTTP.BaseURL)
	cfg.Notifications = config.NormalizeNotificationsConfig(cfg.Notifications)
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
	_, _ = mainStore.PruneTasksBefore(time.Now().UTC().Add(-logStoreRetention))
	sink.SetWriter(mainStore)
	tm.SetTaskWriter(mainStore)
	if tasks, err := mainStore.LoadTasks(); err == nil && len(tasks) > 0 {
		tm.LoadHistory(tasks)
	}
	if err := tm.RecoverInterruptedTasks(); err != nil {
		return nil, fmt.Errorf("recover interrupted tasks: %w", err)
	}
	var subagentManager *subagent.Manager
	if cfg.Subagents.Enabled {
		manager, subagentErr := subagent.NewManager(
			subagent.NewStore(filepath.Join(storeBase, "subagents")),
			subagent.Options{
				DefaultProvider:            cfg.Subagents.DefaultProvider,
				MaxAgents:                  cfg.Subagents.MaxAgents,
				MaxConcurrency:             cfg.Subagents.MaxConcurrency,
				MaxPendingPerAgent:         cfg.Subagents.MaxPendingPerAgent,
				SameAgentPollInterval:      time.Duration(cfg.Subagents.SameAgentPollIntervalSeconds) * time.Second,
				DifferentAgentPollInterval: time.Duration(cfg.Subagents.DifferentAgentPollIntervalSeconds) * time.Second,
			},
		)
		if subagentErr != nil {
			return nil, fmt.Errorf("init subagents: %w", subagentErr)
		}
		if cfg.Subagents.DefaultProvider == opencligemini.Name || cfg.Subagents.Providers.OpenCLIGemini.Enabled {
			providerConfig := cfg.Subagents.Providers.OpenCLIGemini
			adapterExecutable := strings.TrimSpace(providerConfig.Executable)
			if adapterExecutable == "" {
				adapterExecutable, subagentErr = adapterbundle.Materialize(filepath.Join(storeBase, "runtime"))
				if subagentErr != nil {
					return nil, fmt.Errorf("materialize %s adapter: %w", opencligemini.Name, subagentErr)
				}
			}
			artifactDir, artifactDirErr := filepath.Abs(filepath.Join(storeBase, "subagents", "artifacts"))
			if artifactDirErr != nil {
				return nil, fmt.Errorf("resolve %s artifact directory: %w", opencligemini.Name, artifactDirErr)
			}
			provider, providerErr := opencligemini.New(opencligemini.Config{
				Executable:        adapterExecutable,
				BrowserExecutable: providerConfig.BrowserExecutable,
				ArtifactDir:       artifactDir,
				DefaultModel:      providerConfig.DefaultModel,
				Timeout:           time.Duration(providerConfig.TimeoutSeconds) * time.Second,
				SubmitInterval:    time.Duration(providerConfig.SubmitIntervalSeconds) * time.Second,
			})
			if providerErr != nil {
				return nil, fmt.Errorf("init %s provider: %w", opencligemini.Name, providerErr)
			}
			if providerErr := manager.RegisterProvider(opencligemini.Name, provider); providerErr != nil {
				return nil, fmt.Errorf("register %s provider: %w", opencligemini.Name, providerErr)
			}
		}
		subagentManager = manager
	}
	chatGPTUsedBySubagents := cfg.Subagents.Enabled &&
		(cfg.Subagents.DefaultProvider == opencligpt.Name || cfg.Subagents.Providers.OpenCLIChatGPT.Enabled)
	needChatGPTProvider := chatGPTUsedBySubagents || cfg.ConversationHandoff.Enabled
	var chatGPTProvider *opencligpt.Provider
	if needChatGPTProvider {
		providerConfig := cfg.Subagents.Providers.OpenCLIChatGPT
		adapterExecutable := ""
		if chatGPTUsedBySubagents {
			materialized, materializeErr := adapterbundle.Materialize(filepath.Join(storeBase, "runtime"))
			if materializeErr != nil {
				return nil, fmt.Errorf("materialize %s adapter: %w", opencligpt.Name, materializeErr)
			}
			adapterExecutable = materialized
		}
		provider, providerErr := opencligpt.New(opencligpt.Config{
			Executable:        providerConfig.Executable,
			AdapterExecutable: adapterExecutable,
			Timeout:           time.Duration(providerConfig.TimeoutSeconds) * time.Second,
			SubmitInterval:    time.Duration(providerConfig.SubmitIntervalSeconds) * time.Second,
			DefaultProject:    providerConfig.DefaultProject,
		})
		if providerErr != nil {
			return nil, fmt.Errorf("init %s provider: %w", opencligpt.Name, providerErr)
		}
		chatGPTProvider = provider
		if chatGPTUsedBySubagents {
			if providerErr := subagentManager.RegisterProvider(opencligpt.Name, provider); providerErr != nil {
				return nil, fmt.Errorf("register %s provider: %w", opencligpt.Name, providerErr)
			}
		}
	}
	var conversationHandoffSvc *conversationhandoff.Service
	if cfg.ConversationHandoff.Enabled {
		conversationHandoffSvc = conversationhandoff.NewService(cfg.ConversationHandoff.Provider, chatGPTProvider)
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
	var exportSvc *fileexport.Service
	if cfg.FileExports.Enabled {
		exportSvc, err = fileexport.NewService(fileexport.Options{
			BaseURL:    cfg.FileExports.BaseURL,
			DefaultTTL: time.Duration(cfg.FileExports.DefaultTTLSeconds) * time.Second,
			MaxTTL:     time.Duration(cfg.FileExports.MaxTTLSeconds) * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("init file exports: %w", err)
		}
	}
	toolAccessSvc := toolaccess.NewService()
	upgradeSvc := upgrade.NewService(filepath.Join(storeBase, "upgrades"), cfg.Upgrade.Token, cfg.Runtime)
	notificationSvc, err := notify.NewService(cfg.Notifications)
	if err != nil {
		return nil, fmt.Errorf("init notifications: %w", err)
	}
	if notificationSvc.SystemEnabled() {
		approvalSvc.SetObserver(func(req approval.Request) {
			if shouldSendApprovalNotification(req) {
				notificationSvc.SendAsync(approvalNotification(req))
			}
		})
	}

	var fileBrowserSvc *filebrowser.Service
	if cfg.FileBrowser.Enabled {
		fileBrowserSvc = filebrowser.NewService(cfg.FileBrowser, cfg.Root)
	}
	ownsDynamicRuntime := cfg.Runtime.Mode != "mcp_stdio"
	dynamicSvc, err := dynamic.NewService(filepath.Join(storeBase, "dynamic"), cfg.Deployments.Dynamic, cfg.Root, ownsDynamicRuntime)
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
			Action: event.Action, Tool: tool, Requester: requester,
			Transport: event.Operation.Transport, MCPSessionID: event.Operation.MCPSessionID,
			SessionMode: event.Operation.SessionMode, SessionIDSource: event.Operation.SessionIDSource,
			RequestID: event.Operation.RequestID,
			Error:     event.Error, Details: details, CreatedAt: event.CreatedAt,
		})
		if event.Error != "" && notificationSvc.SystemEnabled() {
			notificationSvc.SendAsync(notify.Message{
				Title: "LocalGate deployment event",
				Body:  fmt.Sprintf("%s: %s\n%s", event.Name, event.Action, event.Error),
				Group: "localgate-deployment",
			})
		}
	})
	deploymentManager := deployment.NewManager(staticSvc, dynamicSvc, "")
	dynamicRoutingConfig := dynamicSvc.Config()
	deploymentManager.ConfigureAppHostingWithSuffixes(
		dynamicRoutingConfig.PublicBaseURL,
		dynamicRoutingConfig.PrivateBaseURL,
		dynamicRoutingConfig.DefaultAccess,
		cfg.Deployments.Static.DomainSuffix,
		dynamicRoutingConfig.DomainSuffix,
	)
	deploymentManager.ConfigureAppRouting(cfg.HTTP.BaseURL, dynamicRoutingConfig.ReservedNames)
	reg := core.NewRegistry(sink)
	reg.Register(meta.NewStartupInstructionsTool(cfg.Root, reg, meta.StartupPromptOptions{
		BaseURL:          cfg.HTTP.BaseURL,
		ToolHTTPMode:     cfg.ToolHTTPMode.Enabled,
		EnableBase64:     cfg.ToolArguments.EnableBase64,
		GlobalSkillRoots: cfg.Skills.GlobalRoots,
		MCPServers:       cfg.MCPServers,
	}))
	reg.Register(meta.NewGetSkillTool(dynamicRoutingConfig))
	for _, t := range filesystem.NewTools(cfg.Root, cfg.Skills.GlobalRoots) {
		reg.Register(t)
	}
	reg.Register(filesystem.NewCreateDownloadTokenTool(cfg.Root, cfg.HTTP.BaseURL, downloadSvc))
	reg.Register(filesystem.NewCreateUploadTokenTool(cfg.Root, cfg.HTTP.BaseURL, uploadSvc))
	if exportSvc != nil {
		reg.Register(filesystem.NewExportFileTool(cfg.Root, cfg.FileExports, exportSvc))
	}
	reg.Register(shell.NewTool(cfg.Root, cfg.DefaultTimeoutMs))
	reg.Register(shell.NewArgvTool(cfg.Root, cfg.DefaultTimeoutMs))
	reg.Register(shell.NewExecCommandTool(cfg.Root, sessions))
	reg.Register(shell.NewWriteStdinTool(cfg.Root, sessions))
	reg.Register(shell.NewReadThreadTerminalTool(cfg.Root, sessions))
	reg.Register(shell.NewCloseSessionTool(cfg.Root, sessions))
	for _, tool := range taskcap.NewTools(tm, sink) {
		reg.Register(tool)
	}
	if subagentManager != nil {
		for _, tool := range subagentcap.NewTools(subagentManager) {
			reg.Register(tool)
		}
		if exportSvc != nil {
			reg.Register(subagentcap.NewArtifactGetTool(subagentManager, cfg.FileExports, exportSvc))
		}
	}
	if conversationHandoffSvc != nil {
		reg.Register(conversationcap.NewHandoffTool(conversationHandoffSvc))
	}
	if notificationSvc.ToolEnabled() {
		reg.Register(notificationcap.NewTool(notificationSvc))
	}
	reg.Register(toolcap.NewGetToolSchemaTool(reg))
	deployRequiresApproval := approvalConfig.Enabled && (cfg.Deployments.Static.RequireApproval || cfg.Deployments.Dynamic.RequireApproval)
	reg.Register(mcptools.NewCoreDeployToolWithManager(deploymentManager, cfg.Root, approvalSvc, deployRequiresApproval))
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
	for _, name := range config.DefaultHiddenTools {
		reg.Hide(name)
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
			for _, tool := range relaycap.NewTools(srv.Name, client) {
				reg.Register(tool)
			}
		}
	}

	app := &App{
		Sink:                sink,
		Tasks:               tm,
		Sessions:            sessions,
		Subagents:           subagentManager,
		SubagentConfig:      cfg.Subagents,
		ConversationHandoff: conversationHandoffSvc,
		Root:                cfg.Root,
		Registry:            reg,
		Approvals:           approvalSvc,
		Approval:            approvalConfig,
		Admin:               adminConfig,
		Downloads:           downloadSvc,
		Uploads:             uploadSvc,
		Exports:             exportSvc,
		FileExports:         cfg.FileExports,
		FileBrowser:         cfg.FileBrowser,
		FileBrowserService:  fileBrowserSvc,
		Notifications:       notificationSvc,
		NotificationConfig:  cfg.Notifications,
		ToolAccess:          toolAccessSvc,
		ToolArguments:       cfg.ToolArguments,
		ToolHTTPMode:        cfg.ToolHTTPMode,
		StaticHosting:       cfg.Deployments.Static,
		StaticService:       staticSvc,
		DynamicHosting:      cfg.Deployments.Dynamic,
		DynamicService:      dynamicSvc,
		DeploymentManager:   deploymentManager,
		HTTPBaseURL:         cfg.HTTP.BaseURL,
		StoreBase:           storeBase,
		taskStore:           mainStore,
		Upgrades:            upgradeSvc,
		Relays:              relays,
		RelayErrors:         relayErrors,
		approvalGates:       map[string]func(){},
	}
	if cfg.SessionCleanup.SweepIntervalMs > 0 || logStoreSweepInterval > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		app.stopJanitor = cancel
		if cfg.SessionCleanup.SweepIntervalMs > 0 {
			go app.runSessionJanitor(ctx, time.Duration(cfg.SessionCleanup.SweepIntervalMs)*time.Millisecond)
		}
		go app.runLogStoreJanitor(ctx, logStoreSweepInterval, logStoreRetention)
	}
	return app, nil
}

func approvalNotification(req approval.Request) notify.Message {
	title := "LocalGate approval required"
	if req.Status != approval.StatusPending {
		title = "LocalGate approval " + string(req.Status)
	}
	bodyParts := []string{}
	if strings.TrimSpace(req.Summary) != "" {
		bodyParts = append(bodyParts, req.Summary)
	}
	if strings.TrimSpace(req.Tool) != "" {
		bodyParts = append(bodyParts, "Tool: "+req.Tool)
	}
	if strings.TrimSpace(req.RiskLevel) != "" {
		bodyParts = append(bodyParts, "Risk: "+req.RiskLevel)
	}
	if len(bodyParts) == 0 {
		bodyParts = append(bodyParts, "Approval status changed")
	}
	return notify.Message{
		Title: title,
		Body:  strings.Join(bodyParts, "\n"),
		Group: "localgate-approval",
		URL:   req.ApprovalURL,
	}
}

func shouldSendApprovalNotification(req approval.Request) bool {
	switch req.Status {
	case approval.StatusPending, approval.StatusRejected, approval.StatusCanceled, approval.StatusExpired:
		return true
	default:
		return false
	}
}

type Submission struct {
	Task     *core.Task
	Approval *approval.Request
}

type fileBrowserPayloadStage struct {
	ID   string
	Path string
}

func (a *App) stageFileBrowserPayload(content []byte) (fileBrowserPayloadStage, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fileBrowserPayloadStage{}, err
	}
	stageID := hex.EncodeToString(raw[:])
	dir := filepath.Join(a.StoreBase, "filebrowser", "staging")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fileBrowserPayloadStage{}, err
	}
	tmp, err := os.CreateTemp(dir, ".payload-*")
	if err != nil {
		return fileBrowserPayloadStage{}, err
	}
	tmpPath := tmp.Name()
	finalPath := filepath.Join(dir, stageID+".bin")
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fileBrowserPayloadStage{}, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fileBrowserPayloadStage{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fileBrowserPayloadStage{}, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fileBrowserPayloadStage{}, err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fileBrowserPayloadStage{}, err
	}
	return fileBrowserPayloadStage{ID: stageID, Path: finalPath}, nil
}

func (s fileBrowserPayloadStage) Read() ([]byte, error) {
	return os.ReadFile(s.Path)
}

func (s fileBrowserPayloadStage) Cleanup() error {
	if s.Path == "" {
		return nil
	}
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (a *App) SubmitFileBrowserSave(requester string, req filebrowser.SaveContentRequest) (Submission, error) {
	if a == nil || a.FileBrowserService == nil {
		return Submission{}, filebrowser.ErrDisabled
	}
	requester = strings.TrimSpace(requester)
	if requester == "" {
		requester = "admin"
	}
	args := fileBrowserSaveApprovalArgs(req)
	if a.FileBrowser.RequireApprovalForWrite {
		if a.Approvals == nil || !a.Approval.Enabled {
			return Submission{}, filebrowser.ErrApprovalRequired
		}
		stage, err := a.stageFileBrowserPayload([]byte(req.Content))
		if err != nil {
			return Submission{}, err
		}
		args["content_stage_id"] = stage.ID
		req.Content = ""
		reqApproval, err := a.Approvals.Create(approval.Request{
			Tool:      "file_browser.write",
			Requester: requester,
			Transport: invocation.TransportAdmin,
			Execution: approval.ExecutionAppTool,
			Reason:    "approval.policy.file_browser_write",
			Summary:   "Save file " + req.Path,
			RiskLevel: "high",
			Args:      args,
		})
		if err != nil {
			_ = stage.Cleanup()
			return Submission{}, err
		}
		run := func(ctx context.Context, taskID string) (int, error) {
			content, stageErr := stage.Read()
			if stageErr != nil {
				_ = stage.Cleanup()
				_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_write_failed", Tool: "file_browser.write", Requester: requester, Transport: invocation.TransportAdmin, Error: stageErr.Error(), Details: args})
				return -1, stageErr
			}
			defer func() { _ = stage.Cleanup() }()
			runReq := req
			runReq.Content = string(content)
			res, saveErr := a.FileBrowserService.SaveContentApproved(ctx, runReq)
			if saveErr != nil {
				_, _ = a.Approvals.RecordAudit(approval.AuditRecord{
					ApprovalID: reqApproval.ID,
					TaskID:     taskID,
					Action:     "file_browser_write_failed",
					Tool:       "file_browser.write",
					Requester:  requester,
					Transport:  invocation.TransportAdmin,
					Error:      saveErr.Error(),
					Details:    args,
				})
				return -1, saveErr
			}
			result := fileBrowserSaveResultMap(res)
			_ = a.Tasks.SetResult(taskID, result)
			_, _ = a.Approvals.RecordAudit(approval.AuditRecord{
				ApprovalID: reqApproval.ID,
				TaskID:     taskID,
				Action:     "file_browser_write_succeeded",
				Tool:       "file_browser.write",
				Requester:  requester,
				Transport:  invocation.TransportAdmin,
				Details:    result,
			})
			return 0, nil
		}
		task, release := a.Tasks.CreateGatedTask(core.Task{
			Transport:   invocation.TransportAdmin,
			Requester:   requester,
			Tool:        "file_browser.write",
			ArgsDigest:  reqApproval.ArgsDigest,
			ApprovalID:  reqApproval.ID,
			ApprovalURL: reqApproval.ApprovalURL,
			ExpiresAt:   reqApproval.ExpiresAt,
		}, run)
		bound, err := a.Approvals.BindTask(reqApproval.ID, task.ID)
		if err != nil {
			_ = a.Tasks.Cancel(task.ID)
			_ = stage.Cleanup()
			return Submission{}, err
		}
		a.approvalMu.Lock()
		a.approvalGates[reqApproval.ID] = release
		a.approvalMu.Unlock()
		go a.expireApproval(bound)
		return Submission{Task: task, Approval: &bound}, nil
	}
	res, err := a.FileBrowserService.SaveContent(context.Background(), req)
	if err != nil {
		return Submission{}, err
	}
	task := a.Tasks.CreateTask(core.Task{Transport: invocation.TransportAdmin, Requester: requester, Tool: "file_browser.write"}, func(ctx context.Context, taskID string) (int, error) {
		_ = a.Tasks.SetResult(taskID, fileBrowserSaveResultMap(res))
		_, _ = a.Approvals.RecordAudit(approval.AuditRecord{
			TaskID:    taskID,
			Action:    "file_browser_write_succeeded",
			Tool:      "file_browser.write",
			Requester: requester,
			Transport: invocation.TransportAdmin,
			Details:   fileBrowserSaveResultMap(res),
		})
		return 0, nil
	})
	return Submission{Task: task}, nil
}

func (a *App) SubmitFileBrowserMkdir(requester string, req filebrowser.MkdirRequest) (Submission, error) {
	if a == nil || a.FileBrowserService == nil {
		return Submission{}, filebrowser.ErrDisabled
	}
	requester = strings.TrimSpace(requester)
	if requester == "" {
		requester = "admin"
	}
	args := fileBrowserMkdirApprovalArgs(req)
	if a.FileBrowser.RequireApprovalForWrite {
		if a.Approvals == nil || !a.Approval.Enabled {
			return Submission{}, filebrowser.ErrApprovalRequired
		}
		reqApproval, err := a.Approvals.Create(approval.Request{
			Tool:      "file_browser.mkdir",
			Requester: requester,
			Transport: invocation.TransportAdmin,
			Execution: approval.ExecutionAppTool,
			Reason:    "approval.policy.file_browser_write",
			Summary:   "Create directory " + req.Path,
			RiskLevel: "high",
			Args:      args,
		})
		if err != nil {
			return Submission{}, err
		}
		run := func(ctx context.Context, taskID string) (int, error) {
			res, mkdirErr := a.FileBrowserService.MkdirApproved(ctx, req)
			if mkdirErr != nil {
				_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_mkdir_failed", Tool: "file_browser.mkdir", Requester: requester, Transport: invocation.TransportAdmin, Error: mkdirErr.Error(), Details: args})
				return -1, mkdirErr
			}
			result := fileBrowserMkdirResultMap(res)
			_ = a.Tasks.SetResult(taskID, result)
			_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_mkdir_succeeded", Tool: "file_browser.mkdir", Requester: requester, Transport: invocation.TransportAdmin, Details: result})
			return 0, nil
		}
		task, release := a.Tasks.CreateGatedTask(core.Task{Transport: invocation.TransportAdmin, Requester: requester, Tool: "file_browser.mkdir", ArgsDigest: reqApproval.ArgsDigest, ApprovalID: reqApproval.ID, ApprovalURL: reqApproval.ApprovalURL, ExpiresAt: reqApproval.ExpiresAt}, run)
		bound, err := a.Approvals.BindTask(reqApproval.ID, task.ID)
		if err != nil {
			_ = a.Tasks.Cancel(task.ID)
			return Submission{}, err
		}
		a.approvalMu.Lock()
		a.approvalGates[reqApproval.ID] = release
		a.approvalMu.Unlock()
		go a.expireApproval(bound)
		return Submission{Task: task, Approval: &bound}, nil
	}
	res, err := a.FileBrowserService.Mkdir(context.Background(), req)
	if err != nil {
		return Submission{}, err
	}
	task := a.Tasks.CreateTask(core.Task{Transport: invocation.TransportAdmin, Requester: requester, Tool: "file_browser.mkdir"}, func(ctx context.Context, taskID string) (int, error) {
		result := fileBrowserMkdirResultMap(res)
		_ = a.Tasks.SetResult(taskID, result)
		_, _ = a.Approvals.RecordAudit(approval.AuditRecord{TaskID: taskID, Action: "file_browser_mkdir_succeeded", Tool: "file_browser.mkdir", Requester: requester, Transport: invocation.TransportAdmin, Details: result})
		return 0, nil
	})
	return Submission{Task: task}, nil
}

func (a *App) SubmitFileBrowserUpload(requester string, req filebrowser.UploadRequest) (Submission, error) {
	if a == nil || a.FileBrowserService == nil {
		return Submission{}, filebrowser.ErrDisabled
	}
	requester = strings.TrimSpace(requester)
	if requester == "" {
		requester = "admin"
	}
	args := fileBrowserUploadApprovalArgs(req)
	if a.FileBrowser.RequireApprovalForWrite {
		if a.Approvals == nil || !a.Approval.Enabled {
			return Submission{}, filebrowser.ErrApprovalRequired
		}
		stage, err := a.stageFileBrowserPayload(req.Content)
		if err != nil {
			return Submission{}, err
		}
		args["content_stage_id"] = stage.ID
		req.Content = nil
		reqApproval, err := a.Approvals.Create(approval.Request{Tool: "file_browser.upload", Requester: requester, Transport: invocation.TransportAdmin, Execution: approval.ExecutionAppTool, Reason: "approval.policy.file_browser_write", Summary: "Upload file " + req.Path, RiskLevel: "high", Args: args})
		if err != nil {
			_ = stage.Cleanup()
			return Submission{}, err
		}
		run := func(ctx context.Context, taskID string) (int, error) {
			content, stageErr := stage.Read()
			if stageErr != nil {
				_ = stage.Cleanup()
				_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_upload_failed", Tool: "file_browser.upload", Requester: requester, Transport: invocation.TransportAdmin, Error: stageErr.Error(), Details: args})
				return -1, stageErr
			}
			defer func() { _ = stage.Cleanup() }()
			runReq := req
			runReq.Content = content
			res, uploadErr := a.FileBrowserService.UploadApproved(ctx, runReq)
			if uploadErr != nil {
				_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_upload_failed", Tool: "file_browser.upload", Requester: requester, Transport: invocation.TransportAdmin, Error: uploadErr.Error(), Details: args})
				return -1, uploadErr
			}
			result := fileBrowserSaveResultMap(res)
			_ = a.Tasks.SetResult(taskID, result)
			_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_upload_succeeded", Tool: "file_browser.upload", Requester: requester, Transport: invocation.TransportAdmin, Details: result})
			return 0, nil
		}
		task, release := a.Tasks.CreateGatedTask(core.Task{Transport: invocation.TransportAdmin, Requester: requester, Tool: "file_browser.upload", ArgsDigest: reqApproval.ArgsDigest, ApprovalID: reqApproval.ID, ApprovalURL: reqApproval.ApprovalURL, ExpiresAt: reqApproval.ExpiresAt}, run)
		bound, err := a.Approvals.BindTask(reqApproval.ID, task.ID)
		if err != nil {
			_ = a.Tasks.Cancel(task.ID)
			_ = stage.Cleanup()
			return Submission{}, err
		}
		a.approvalMu.Lock()
		a.approvalGates[reqApproval.ID] = release
		a.approvalMu.Unlock()
		go a.expireApproval(bound)
		return Submission{Task: task, Approval: &bound}, nil
	}
	res, err := a.FileBrowserService.Upload(context.Background(), req)
	if err != nil {
		return Submission{}, err
	}
	task := a.Tasks.CreateTask(core.Task{Transport: invocation.TransportAdmin, Requester: requester, Tool: "file_browser.upload"}, func(ctx context.Context, taskID string) (int, error) {
		result := fileBrowserSaveResultMap(res)
		_ = a.Tasks.SetResult(taskID, result)
		_, _ = a.Approvals.RecordAudit(approval.AuditRecord{TaskID: taskID, Action: "file_browser_upload_succeeded", Tool: "file_browser.upload", Requester: requester, Transport: invocation.TransportAdmin, Details: result})
		return 0, nil
	})
	return Submission{Task: task}, nil
}

func (a *App) SubmitFileBrowserDelete(requester string, req filebrowser.DeleteRequest) (Submission, error) {
	if a == nil || a.FileBrowserService == nil {
		return Submission{}, filebrowser.ErrDisabled
	}
	requester = strings.TrimSpace(requester)
	if requester == "" {
		requester = "admin"
	}
	args := fileBrowserDeleteApprovalArgs(req)
	if a.FileBrowser.RequireApprovalForDelete {
		if a.Approvals == nil || !a.Approval.Enabled {
			return Submission{}, filebrowser.ErrApprovalRequired
		}
		risk := "high"
		if req.Recursive {
			risk = "critical"
		}
		reqApproval, err := a.Approvals.Create(approval.Request{
			Tool:      "file_browser.delete",
			Requester: requester,
			Transport: invocation.TransportAdmin,
			Execution: approval.ExecutionAppTool,
			Reason:    "approval.policy.file_browser_delete",
			Summary:   "Delete " + req.Path,
			RiskLevel: risk,
			Args:      args,
		})
		if err != nil {
			return Submission{}, err
		}
		run := func(ctx context.Context, taskID string) (int, error) {
			res, deleteErr := a.FileBrowserService.DeleteApproved(ctx, req)
			if deleteErr != nil {
				_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_delete_failed", Tool: "file_browser.delete", Requester: requester, Transport: invocation.TransportAdmin, Error: deleteErr.Error(), Details: args})
				return -1, deleteErr
			}
			result := fileBrowserDeleteResultMap(res)
			_ = a.Tasks.SetResult(taskID, result)
			_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_delete_succeeded", Tool: "file_browser.delete", Requester: requester, Transport: invocation.TransportAdmin, Details: result})
			return 0, nil
		}
		task, release := a.Tasks.CreateGatedTask(core.Task{Transport: invocation.TransportAdmin, Requester: requester, Tool: "file_browser.delete", ArgsDigest: reqApproval.ArgsDigest, ApprovalID: reqApproval.ID, ApprovalURL: reqApproval.ApprovalURL, ExpiresAt: reqApproval.ExpiresAt}, run)
		bound, err := a.Approvals.BindTask(reqApproval.ID, task.ID)
		if err != nil {
			_ = a.Tasks.Cancel(task.ID)
			return Submission{}, err
		}
		a.approvalMu.Lock()
		a.approvalGates[reqApproval.ID] = release
		a.approvalMu.Unlock()
		go a.expireApproval(bound)
		return Submission{Task: task, Approval: &bound}, nil
	}
	res, err := a.FileBrowserService.Delete(context.Background(), req)
	if err != nil {
		return Submission{}, err
	}
	task := a.Tasks.CreateTask(core.Task{Transport: invocation.TransportAdmin, Requester: requester, Tool: "file_browser.delete"}, func(ctx context.Context, taskID string) (int, error) {
		result := fileBrowserDeleteResultMap(res)
		_ = a.Tasks.SetResult(taskID, result)
		_, _ = a.Approvals.RecordAudit(approval.AuditRecord{TaskID: taskID, Action: "file_browser_delete_succeeded", Tool: "file_browser.delete", Requester: requester, Transport: invocation.TransportAdmin, Details: result})
		return 0, nil
	})
	return Submission{Task: task}, nil
}

func (a *App) SubmitFileBrowserRename(requester string, req filebrowser.RenameRequest) (Submission, error) {
	if a == nil || a.FileBrowserService == nil {
		return Submission{}, filebrowser.ErrDisabled
	}
	requester = strings.TrimSpace(requester)
	if requester == "" {
		requester = "admin"
	}
	args := fileBrowserRenameApprovalArgs(req)
	if a.FileBrowser.RequireApprovalForWrite {
		if a.Approvals == nil || !a.Approval.Enabled {
			return Submission{}, filebrowser.ErrApprovalRequired
		}
		reqApproval, err := a.Approvals.Create(approval.Request{
			Tool:      "file_browser.rename",
			Requester: requester,
			Transport: invocation.TransportAdmin,
			Execution: approval.ExecutionAppTool,
			Reason:    "approval.policy.file_browser_write",
			Summary:   "Rename " + req.FromPath + " to " + req.ToPath,
			RiskLevel: "high",
			Args:      args,
		})
		if err != nil {
			return Submission{}, err
		}
		run := func(ctx context.Context, taskID string) (int, error) {
			res, renameErr := a.FileBrowserService.RenameApproved(ctx, req)
			if renameErr != nil {
				_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_rename_failed", Tool: "file_browser.rename", Requester: requester, Transport: invocation.TransportAdmin, Error: renameErr.Error(), Details: args})
				return -1, renameErr
			}
			result := fileBrowserRenameResultMap(res)
			_ = a.Tasks.SetResult(taskID, result)
			_, _ = a.Approvals.RecordAudit(approval.AuditRecord{ApprovalID: reqApproval.ID, TaskID: taskID, Action: "file_browser_rename_succeeded", Tool: "file_browser.rename", Requester: requester, Transport: invocation.TransportAdmin, Details: result})
			return 0, nil
		}
		task, release := a.Tasks.CreateGatedTask(core.Task{Transport: invocation.TransportAdmin, Requester: requester, Tool: "file_browser.rename", ArgsDigest: reqApproval.ArgsDigest, ApprovalID: reqApproval.ID, ApprovalURL: reqApproval.ApprovalURL, ExpiresAt: reqApproval.ExpiresAt}, run)
		bound, err := a.Approvals.BindTask(reqApproval.ID, task.ID)
		if err != nil {
			_ = a.Tasks.Cancel(task.ID)
			return Submission{}, err
		}
		a.approvalMu.Lock()
		a.approvalGates[reqApproval.ID] = release
		a.approvalMu.Unlock()
		go a.expireApproval(bound)
		return Submission{Task: task, Approval: &bound}, nil
	}
	res, err := a.FileBrowserService.Rename(context.Background(), req)
	if err != nil {
		return Submission{}, err
	}
	task := a.Tasks.CreateTask(core.Task{Transport: invocation.TransportAdmin, Requester: requester, Tool: "file_browser.rename"}, func(ctx context.Context, taskID string) (int, error) {
		result := fileBrowserRenameResultMap(res)
		_ = a.Tasks.SetResult(taskID, result)
		_, _ = a.Approvals.RecordAudit(approval.AuditRecord{TaskID: taskID, Action: "file_browser_rename_succeeded", Tool: "file_browser.rename", Requester: requester, Transport: invocation.TransportAdmin, Details: result})
		return 0, nil
	})
	return Submission{Task: task}, nil
}

func fileBrowserSaveApprovalArgs(req filebrowser.SaveContentRequest) map[string]any {
	sum := sha256.Sum256([]byte(req.Content))
	return map[string]any{
		"path":           req.Path,
		"etag":           req.ETag,
		"create":         req.Create,
		"overwrite":      req.Overwrite,
		"content_bytes":  len([]byte(req.Content)),
		"content_sha256": hex.EncodeToString(sum[:]),
	}
}

func fileBrowserUploadApprovalArgs(req filebrowser.UploadRequest) map[string]any {
	sum := sha256.Sum256(req.Content)
	return map[string]any{"path": req.Path, "etag": req.ETag, "create": req.Create, "overwrite": req.Overwrite, "content_bytes": len(req.Content), "content_sha256": hex.EncodeToString(sum[:])}
}

func fileBrowserMkdirApprovalArgs(req filebrowser.MkdirRequest) map[string]any {
	return map[string]any{"path": req.Path}
}

func fileBrowserMkdirResultMap(res filebrowser.MkdirResult) map[string]any {
	return map[string]any{"path": res.Path, "status": res.Status, "mod_time": res.ModTime}
}

func fileBrowserDeleteApprovalArgs(req filebrowser.DeleteRequest) map[string]any {
	return map[string]any{"path": req.Path, "recursive": req.Recursive}
}

func fileBrowserDeleteResultMap(res filebrowser.DeleteResult) map[string]any {
	return map[string]any{"path": res.Path, "type": res.Type, "status": res.Status, "recursive": res.Recursive, "deleted_at": res.DeletedAt}
}

func fileBrowserRenameApprovalArgs(req filebrowser.RenameRequest) map[string]any {
	return map[string]any{"from_path": req.FromPath, "to_path": req.ToPath}
}

func fileBrowserRenameResultMap(res filebrowser.RenameResult) map[string]any {
	return map[string]any{"from_path": res.FromPath, "to_path": res.ToPath, "type": res.Type, "status": res.Status, "mod_time": res.ModTime}
}

func fileBrowserSaveResultMap(res filebrowser.SaveContentResult) map[string]any {
	return map[string]any{
		"path":        res.Path,
		"status":      res.Status,
		"bytes":       res.Bytes,
		"etag":        res.ETag,
		"size":        res.Size,
		"mod_time":    res.ModTime,
		"created":     res.Created,
		"overwritten": res.Overwritten,
	}
}

func (a *App) CreateToolTask(requester, tool string, args map[string]any) (*core.Task, error) {
	return a.CreateToolTaskWithMeta(invocation.Meta{
		Requester: requester,
		Transport: invocation.TransportREST,
	}, tool, args)
}

func (a *App) CreateToolTaskWithApproval(requester, tool string, args map[string]any, approvalID string) (*core.Task, error) {
	return a.CreateToolTaskWithMetaAndApproval(invocation.Meta{
		Requester: requester,
		Transport: invocation.TransportREST,
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
	requester := meta.Requester
	reg := a.Registry
	if reg == nil {
		return nil, errors.New("tool registry is not available")
	}
	run := func(ctx context.Context, taskID string) (int, error) {
		res, err := reg.Invoke(ctx, tool, core.InvokeContext{
			TaskID: taskID, Requester: requester, ApprovalID: approvalID,
			Transport: meta.Transport, MCPSessionID: meta.MCPSessionID,
			SessionMode: meta.SessionMode, SessionIDSource: meta.SessionIDSource, RequestID: meta.RequestID,
			Authorized: authorized, Sink: a.Sink,
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
		Requester: requester, Tool: tool, ConcurrencyClass: toolTaskConcurrencyClass(tool), ArgsDigest: approval.Digest(tool, requester, args), ApprovalID: approvalID,
	}, run)
	return task, nil
}

func (a *App) SubmitToolTask(requester, tool string, args map[string]any) (Submission, error) {
	return a.SubmitToolTaskWithMeta(invocation.Meta{
		Requester: requester,
		Transport: invocation.TransportREST,
	}, tool, args)
}

func (a *App) SubmitToolTaskWithMeta(meta invocation.Meta, tool string, args map[string]any) (Submission, error) {
	meta = meta.Normalize()
	requester := meta.Requester
	if a.Approvals != nil && a.Approval.Enabled {
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
			if needApproval && isDynamicMutation(tool, args) && (tool == "deploy_tool" || tool == "delete_deployment") {
				risk = "critical"
			}
		} else if (tool == "export_file" || tool == "subagent_artifact_get") && a.FileExports.RequireApproval {
			needApproval = true
			reason = "approval.policy.file_export"
			risk = "high"
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
		} else if need, policyReason := a.Approvals.RequiresApproval(tool, a.Root, args); need {
			needApproval = true
			reason = policyReason
		}
		if needApproval {
			req, err := a.Approvals.Create(approval.Request{
				Tool: tool, Requester: requester,
				Transport: meta.Transport, MCPSessionID: meta.MCPSessionID,
				SessionMode: meta.SessionMode, SessionIDSource: meta.SessionIDSource, RequestID: meta.RequestID,
				Execution: approval.ExecutionAppTool, Reason: reason,
				Summary: approvalSummary(tool, args), RiskLevel: risk, Args: args,
			})
			if err != nil {
				return Submission{}, err
			}
			reg := a.Registry
			if reg == nil {
				return Submission{}, errors.New("tool registry is not available")
			}
			run := func(ctx context.Context, taskID string) (int, error) {
				res, invokeErr := reg.Invoke(ctx, tool, core.InvokeContext{
					TaskID: taskID, Requester: requester, ApprovalID: req.ID,
					Transport: meta.Transport, MCPSessionID: meta.MCPSessionID,
					SessionMode: meta.SessionMode, SessionIDSource: meta.SessionIDSource, RequestID: meta.RequestID,
					Authorized: true, Sink: a.Sink,
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
				Requester: requester, Tool: tool, ConcurrencyClass: toolTaskConcurrencyClass(tool), ArgsDigest: req.ArgsDigest,
				ApprovalID: req.ID, ApprovalURL: req.ApprovalURL, ExpiresAt: req.ExpiresAt,
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

func toolTaskConcurrencyClass(tool string) core.TaskConcurrencyClass {
	tool = strings.TrimSpace(tool)
	if strings.HasPrefix(tool, "subagent_") || tool == "conversation_handoff" {
		return core.TaskConcurrencyIsolated
	}
	return ""
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
		ApprovalID: approvalID,
		TaskID:     task.ID,
		Requester:  task.Requester,
		Tool:       task.Tool,
		ArgsDigest: task.ArgsDigest,
		TaskStatus: string(task.Status),
	})
	if err != nil {
		return approval.Request{}, err
	}
	if err := a.ReleaseApproval(approvalID); err != nil {
		_ = a.CancelApprovalTaskWithReason(approvalID, task.ID, "approval_release_failed", "approval was granted but the task gate could not be released")
		_, _ = a.Approvals.RecordAudit(approval.AuditRecord{
			ApprovalID: approvalID,
			TaskID:     task.ID,
			Action:     "approval_release_failed",
			Actor:      actor.ID,
			AuthMethod: actor.AuthMethod,
			Tool:       req.Tool,
			Requester:  req.Requester,
			Transport:  req.Transport, MCPSessionID: req.MCPSessionID,
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
	case "export_file":
		fileName, _ := args["file_name"].(string)
		path, _ := args["path"].(string)
		if strings.TrimSpace(fileName) != "" {
			return "Export local file " + path + " as " + fileName
		}
		return "Export local file " + path
	case "subagent_artifact_get":
		subagentID, _ := args["subagent_id"].(string)
		artifactID, _ := args["artifact_id"].(string)
		return "Export subagent artifact " + artifactID + " from " + subagentID
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

func (a *App) InvokeTool(ctx context.Context, tool string, args map[string]any) (map[string]any, error) {
	if a.Registry == nil {
		return nil, errors.New("tool registry is not available")
	}
	return a.Registry.Invoke(ctx, tool, core.InvokeContext{Requester: "app", Sink: a.Sink}, args)
}

func (a *App) IsToolVisible(tool string) bool {
	return a != nil && a.Registry != nil && a.Registry.IsVisible(tool)
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
	if a.Subagents != nil {
		if err := a.Subagents.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close subagents: %w", err))
		}
	}
	if a.Sessions != nil {
		if err := a.Sessions.CloseAll(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close sessions: %w", err))
		}
	}
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

func (a *App) runLogStoreJanitor(ctx context.Context, interval, retention time.Duration) {
	if a == nil || a.taskStore == nil || interval <= 0 || retention <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.pruneLogStore(now.UTC().Add(-retention))
		}
	}
}

func (a *App) pruneLogStore(cutoff time.Time) {
	if a == nil || a.taskStore == nil || cutoff.IsZero() {
		return
	}
	removed, _ := a.taskStore.PruneTasksBefore(cutoff)
	if len(removed) == 0 {
		return
	}
	a.Tasks.RemoveTerminalTasks(removed)
	a.Sink.RemoveTasks(removed)
}
