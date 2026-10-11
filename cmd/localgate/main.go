package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/httpapi"
	"localgate/internal/mcpgo"
	"localgate/internal/server"
	"localgate/internal/upgrade"
)

var Version = "dev"

func main() {
	var (
		configPath             = flag.String("config", "./localgate.yaml", "config file path")
		bind                   = flag.String("bind", "", "override control-plane bind address")
		adminBind              = flag.String("admin-bind", "", "override admin service bind address")
		publicDeploymentsBind  = flag.String("public-deployments-bind", "", "override public deployments bind address")
		privateDeploymentsBind = flag.String("private-deployments-bind", "", "override private deployments bind address")
		printCfg               = flag.Bool("print-config", false, "print loaded config summary and exit")
		mcpMode                = flag.Bool("mcp", false, "run MCP stdio server")
		showVer                = flag.Bool("version", false, "print version and exit")
		upgradeRun             = flag.Bool("upgrade-runner", false, "internal detached upgrade runner")
		upgradeSt              = flag.String("upgrade-state", "", "internal upgrade state file")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(Version)
		return
	}

	logOut := os.Stdout
	if *mcpMode {
		logOut = os.Stderr
	}
	logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if *upgradeRun {
		if strings.TrimSpace(*upgradeSt) == "" {
			logger.Error("missing upgrade state path")
			os.Exit(1)
		}
		svc := upgrade.NewService(filepath.Dir(filepath.Dir(*upgradeSt)), strings.TrimSpace(os.Getenv("LOCALGATE_UPGRADE_TOKEN")), config.Runtime{})
		if err := svc.RunFromStatePath(*upgradeSt); err != nil {
			logger.Error("upgrade runner failed", "err", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load(config.LoadOptions{
		Path:                   *configPath,
		PathProvided:           flag.Lookup("config") != nil && *configPath != "./localgate.yaml",
		Bind:                   *bind,
		AdminBind:              *adminBind,
		PublicDeploymentsBind:  *publicDeploymentsBind,
		PrivateDeploymentsBind: *privateDeploymentsBind,
	})
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	if cfg.Runtime.Bind == "" {
		cfg.Runtime.Bind = cfg.Bind
	}
	if cfg.Runtime.AdminBind == "" && cfg.Admin.Enabled {
		cfg.Runtime.AdminBind = cfg.Admin.Bind
	}
	if cfg.Runtime.PublicDeploymentBind == "" && cfg.Deployments.PublicBaseURL != "" {
		cfg.Runtime.PublicDeploymentBind = cfg.Deployments.PublicBind
	}
	if cfg.Runtime.PrivateDeploymentBind == "" && cfg.Deployments.PrivateBaseURL != "" {
		cfg.Runtime.PrivateDeploymentBind = cfg.Deployments.PrivateBind
	}
	if cfg.Runtime.ConfigPath == "" {
		cfg.Runtime.ConfigPath = *configPath
	}
	if cfg.Runtime.Bin == "" {
		if exePath, err := os.Executable(); err == nil {
			cfg.Runtime.Bin = exePath
		}
	}
	if *mcpMode {
		cfg.Runtime.Mode = "mcp_stdio"
	}

	logger.Info("config loaded", "summary", cfg.SafeSummary())
	if cfg.Admin.Enabled && !cfg.HTTPAuth.Enabled {
		logger.Warn("admin console is enabled without HTTP bearer authentication", "admin_base_url", cfg.Admin.BaseURL)
	}

	if *printCfg {
		return
	}

	application, err := app.New(cfg)
	if err != nil {
		logger.Error("failed to init app", "err", err)
		os.Exit(1)
	}
	defer application.Close()

	if *mcpMode {
		s, err := mcpgo.New(application, "localgate", Version)
		if err != nil {
			logger.Error("failed to init mcp-go server", "err", err)
			os.Exit(1)
		}
		if err := s.ServeStdio(); err != nil {
			logger.Error("mcp stdio server error", "err", err)
			os.Exit(1)
		}
		return
	}

	api := &httpapi.API{App: application}
	var publicAuthPrefixes []string
	if cfg.FileExports.Enabled && strings.TrimSpace(cfg.FileExports.Bind) == "" {
		publicAuthPrefixes = []string{"/system/exports/"}
	}
	controlSrv := server.New(server.Options{
		Bind:    cfg.Bind,
		Version: Version,
		Auth: server.AuthOptions{
			Enabled:           cfg.HTTPAuth.Enabled,
			BearerToken:       cfg.HTTPAuth.BearerToken,
			AllowPaths:        cfg.HTTPAuth.AllowPaths,
			AllowPathPrefixes: publicAuthPrefixes,
		},
		ControlBaseURL: cfg.HTTP.BaseURL,
		CORS: server.CORSOptions{
			Enabled:        cfg.HTTP.AllowCORS,
			AllowedOrigins: cfg.HTTP.AllowedOrigins,
		},
		Register: func(mux *http.ServeMux) {
			api.RegisterControl(mux)
			// Mount MCP Streamable-HTTP at /mcp
			if s, err := mcpgo.New(application, "localgate", Version); err == nil {
				s.MountHTTP(mux, "/mcp", mcpgo.HTTPOptions{
					Stateless: cfg.MCPHTTP.Stateless,
				})
			}
		},
	})
	var fileExportSrv *server.DeploymentServer
	if cfg.FileExports.Enabled && strings.TrimSpace(cfg.FileExports.Bind) != "" {
		// Reuse the minimal HTTP server: only the explicitly registered export
		// handler is reachable; no control-plane or MCP endpoints exist here.
		exportMux := http.NewServeMux()
		api.RegisterFileExports(exportMux)
		fileExportSrv = server.NewDeployment(server.DeploymentOptions{
			Bind: cfg.FileExports.Bind, Handler: exportMux,
		})
	}

	var adminSrv *server.Server
	if cfg.Admin.Enabled {
		adminSrv = server.NewAdmin(server.AdminOptions{
			Bind:    cfg.Admin.Bind,
			BaseURL: cfg.Admin.BaseURL,
			Auth: server.AuthOptions{
				Enabled:     cfg.HTTPAuth.Enabled,
				BearerToken: cfg.HTTPAuth.BearerToken,
				AllowPaths:  cfg.HTTPAuth.AllowPaths,
			},
			CORS: server.CORSOptions{
				Enabled:        cfg.HTTP.AllowCORS,
				AllowedOrigins: cfg.HTTP.AllowedOrigins,
			},
			Register: func(mux *http.ServeMux) {
				api.RegisterAdmin(mux)
			},
		})
	}

	var publicDeploymentSrv *server.DeploymentServer
	var privateDeploymentSrv *server.DeploymentServer
	if application.DeploymentManager.AppHostingEnabled() {
		publicDeploymentSrv = server.NewDeployment(server.DeploymentOptions{
			Bind:    cfg.Deployments.PublicBind,
			Handler: application.DeploymentManager.HandlerForAccess("public"),
		})
		privateDeploymentSrv = server.NewDeployment(server.DeploymentOptions{
			Bind:    cfg.Deployments.PrivateBind,
			Handler: application.DeploymentManager.HandlerForAccess("private"),
		})
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	type serverResult struct {
		name string
		err  error
	}
	errCh := make(chan serverResult, 5)
	go func() {
		errCh <- serverResult{name: "control", err: controlSrv.Start()}
	}()
	if fileExportSrv != nil {
		go func() {
			errCh <- serverResult{name: "file_export", err: fileExportSrv.Start()}
		}()
	}
	if adminSrv != nil {
		go func() {
			errCh <- serverResult{name: "admin", err: adminSrv.Start()}
		}()
	}
	if publicDeploymentSrv != nil {
		go func() {
			errCh <- serverResult{name: "public_deployment", err: publicDeploymentSrv.Start()}
		}()
	}
	if privateDeploymentSrv != nil {
		go func() {
			errCh <- serverResult{name: "private_deployment", err: privateDeploymentSrv.Start()}
		}()
	}

	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if fileExportSrv != nil {
			_ = fileExportSrv.Shutdown(shutdownCtx)
		}
		if adminSrv != nil {
			_ = adminSrv.Shutdown(shutdownCtx)
		}
		if publicDeploymentSrv != nil {
			_ = publicDeploymentSrv.Shutdown(shutdownCtx)
		}
		if privateDeploymentSrv != nil {
			_ = privateDeploymentSrv.Shutdown(shutdownCtx)
		}
		_ = controlSrv.Shutdown(shutdownCtx)
	}

	select {
	case <-ctx.Done():
		shutdown()
	case result := <-errCh:
		shutdown()
		if result.err != nil && !errors.Is(result.err, http.ErrServerClosed) {
			logger.Error("server stopped with error", "server", result.name, "err", result.err)
			os.Exit(1)
		}
	}
}
