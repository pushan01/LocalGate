package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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
		configPath = flag.String("config", "./localgate.yaml", "config file path")
		bind       = flag.String("bind", "", "override bind address")
		printCfg   = flag.Bool("print-config", false, "print loaded config summary and exit")
		mcpMode    = flag.Bool("mcp", false, "run MCP stdio server")
		showVer    = flag.Bool("version", false, "print version and exit")
		upgradeRun = flag.Bool("upgrade-runner", false, "internal detached upgrade runner")
		upgradeSt  = flag.String("upgrade-state", "", "internal upgrade state file")
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
		Path:         *configPath,
		PathProvided: flag.Lookup("config") != nil && *configPath != "./localgate.yaml",
		Bind:         *bind,
	})
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	if cfg.Runtime.Bind == "" {
		cfg.Runtime.Bind = cfg.Bind
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
		s, err := mcpgo.New(application, "localgate", Version, application.DefaultWSID)
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

	api := &httpapi.API{App: application, DefaultWSID: application.DefaultWSID}
	adminBaseURL := ""
	if cfg.Admin.Enabled {
		adminBaseURL = cfg.Admin.BaseURL
	}

	srv := server.New(server.Options{
		Bind:    cfg.Bind,
		Version: Version,
		Auth: server.AuthOptions{
			Enabled:     cfg.HTTPAuth.Enabled,
			BearerToken: cfg.HTTPAuth.BearerToken,
			AllowPaths:  cfg.HTTPAuth.AllowPaths,
		},
		AdminBaseURL:   adminBaseURL,
		ControlBaseURL: cfg.HTTP.BaseURL,
		CORS: server.CORSOptions{
			Enabled:        cfg.HTTP.AllowCORS,
			AllowedOrigins: cfg.HTTP.AllowedOrigins,
		},
		HostRouting: server.HostRoutingOptions{
			Enabled:      cfg.DynamicHosting.Enabled,
			ControlHosts: cfg.DynamicHosting.ControlHosts,
			AppDomain:    cfg.DynamicHosting.AppDomain,
			AppHandler:   application.DynamicService,
			AdminBaseURL: adminBaseURL,
		},
		Register: func(mux *http.ServeMux) {
			api.Register(mux)
			// Mount MCP Streamable-HTTP at /mcp
			if s, err := mcpgo.New(application, "localgate", Version, application.DefaultWSID); err == nil {
				s.MountHTTP(mux, "/mcp", mcpgo.HTTPOptions{
					Stateless: cfg.MCPHTTP.Stateless,
				})
			}
		},
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil {
			logger.Error("server stopped with error", "err", err)
			os.Exit(1)
		}
	}
}
