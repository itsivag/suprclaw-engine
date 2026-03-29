package gateway

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/itsivag/suprclaw/pkg/agent"
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/channels"
	_ "github.com/itsivag/suprclaw/pkg/channels/discord"
	_ "github.com/itsivag/suprclaw/pkg/channels/irc"
	_ "github.com/itsivag/suprclaw/pkg/channels/line"
	_ "github.com/itsivag/suprclaw/pkg/channels/matrix"
	_ "github.com/itsivag/suprclaw/pkg/channels/slack"
	_ "github.com/itsivag/suprclaw/pkg/channels/supr"
	_ "github.com/itsivag/suprclaw/pkg/channels/telegram"
	_ "github.com/itsivag/suprclaw/pkg/channels/whatsapp"
	_ "github.com/itsivag/suprclaw/pkg/channels/whatsapp_native"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/cron"
	"github.com/itsivag/suprclaw/pkg/devices"
	"github.com/itsivag/suprclaw/pkg/health"
	"github.com/itsivag/suprclaw/pkg/heartbeat"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/media"
	"github.com/itsivag/suprclaw/pkg/providers"
	"github.com/itsivag/suprclaw/pkg/state"
	"github.com/itsivag/suprclaw/pkg/tools"
	"github.com/itsivag/suprclaw/pkg/voice"
)

const (
	serviceShutdownTimeout  = 30 * time.Second
	providerReloadTimeout   = 30 * time.Second
	gracefulShutdownTimeout = 15 * time.Second
)

type services struct {
	CronService      *cron.CronService
	HeartbeatService *heartbeat.HeartbeatService
	MediaStore       media.MediaStore
	ChannelManager   *channels.Manager
	DeviceService    *devices.Service
	HealthServer     *health.Server
	configPath       string
}

type startupBlockedProvider struct {
	reason string
}

func (p *startupBlockedProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return nil, fmt.Errorf("%s", p.reason)
}

func (p *startupBlockedProvider) GetDefaultModel() string {
	return ""
}

// Run starts the gateway runtime using the configuration loaded from configPath.
func Run(debug bool, configPath string, allowEmptyStartup bool) error {
	if debug {
		logger.SetLevel(logger.DEBUG)
		fmt.Println("🔍 Debug mode enabled")
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}

	provider, modelID, err := createStartupProvider(cfg, allowEmptyStartup)
	if err != nil {
		return fmt.Errorf("error creating provider: %w", err)
	}

	if modelID != "" {
		cfg.Agents.Defaults.ModelName = modelID
	}

	msgBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(cfg, msgBus, provider)

	fmt.Println("\n📦 Agent Status:")
	startupInfo := agentLoop.GetStartupInfo()
	toolsInfo := startupInfo["tools"].(map[string]any)
	skillsInfo := startupInfo["skills"].(map[string]any)
	fmt.Printf("  • Tools: %d loaded\n", toolsInfo["count"])
	fmt.Printf("  • Skills: %d/%d available\n", skillsInfo["available"], skillsInfo["total"])

	logger.InfoCF("agent", "Agent initialized",
		map[string]any{
			"tools_count":      toolsInfo["count"],
			"skills_total":     skillsInfo["total"],
			"skills_available": skillsInfo["available"],
		})

	runningServices, err := setupAndStartServices(cfg, agentLoop, msgBus, configPath)
	if err != nil {
		return err
	}

	fmt.Printf("✓ Gateway started on %s:%d\n", cfg.Gateway.Host, cfg.Gateway.Port)
	fmt.Println("Press Ctrl+C to stop")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go agentLoop.Run(ctx)

	var configReloadChan <-chan *config.Config
	stopWatch := func() {}
	if cfg.Gateway.HotReload {
		configReloadChan, stopWatch = setupConfigWatcherPolling(configPath, debug)
		logger.Info("Config hot reload enabled")
	}
	defer stopWatch()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-sigChan:
			logger.Info("Shutting down...")
			shutdownGateway(runningServices, agentLoop, provider, true)
			return nil
		case newCfg := <-configReloadChan:
			err := handleConfigReload(ctx, agentLoop, newCfg, &provider, runningServices, msgBus, allowEmptyStartup)
			if err != nil {
				logger.Errorf("Config reload failed: %v", err)
			}
		}
	}
}

func createStartupProvider(
	cfg *config.Config,
	allowEmptyStartup bool,
) (providers.LLMProvider, string, error) {
	modelName := cfg.Agents.Defaults.GetModelName()
	if modelName == "" && allowEmptyStartup {
		reason := "no default model configured; gateway started in limited mode"
		fmt.Printf("⚠ Warning: %s\n", reason)
		logger.WarnCF("gateway", "Gateway started without default model", map[string]any{
			"limited_mode": true,
		})
		return &startupBlockedProvider{reason: reason}, "", nil
	}

	return providers.CreateProvider(cfg)
}

func setupAndStartServices(
	cfg *config.Config,
	agentLoop *agent.AgentLoop,
	msgBus *bus.MessageBus,
	configPath string,
) (*services, error) {
	runningServices := &services{configPath: configPath}

	execTimeout := time.Duration(cfg.Tools.Cron.ExecTimeoutMinutes) * time.Minute
	var err error
	runningServices.CronService, err = setupCronTool(
		agentLoop,
		msgBus,
		cfg.WorkspacePath(),
		cfg.Agents.Defaults.RestrictToWorkspace,
		execTimeout,
		cfg,
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up cron service: %w", err)
	}
	if err = runningServices.CronService.Start(); err != nil {
		return nil, fmt.Errorf("error starting cron service: %w", err)
	}
	fmt.Println("✓ Cron service started")

	runningServices.MediaStore = media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{
		Enabled:  cfg.Tools.MediaCleanup.Enabled,
		MaxAge:   time.Duration(cfg.Tools.MediaCleanup.MaxAge) * time.Minute,
		Interval: time.Duration(cfg.Tools.MediaCleanup.Interval) * time.Minute,
	})
	if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
		fms.Start()
	}

	runningServices.ChannelManager, err = channels.NewManager(cfg, msgBus, runningServices.MediaStore)
	if err != nil {
		if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
			fms.Stop()
		}
		return nil, fmt.Errorf("error creating channel manager: %w", err)
	}

	agentLoop.SetChannelManager(runningServices.ChannelManager)
	agentLoop.SetMediaStore(runningServices.MediaStore)

	if transcriber := voice.DetectTranscriber(cfg); transcriber != nil {
		agentLoop.SetTranscriber(transcriber)
		logger.InfoCF("voice", "Transcription enabled (agent-level)", map[string]any{"provider": transcriber.Name()})
	}

	enabledChannels := runningServices.ChannelManager.GetEnabledChannels()
	if len(enabledChannels) > 0 {
		fmt.Printf("✓ Channels enabled: %s\n", enabledChannels)
	} else {
		fmt.Println("⚠ Warning: No channels enabled")
	}

	addr := fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port)
	runningServices.HealthServer = health.NewServer(cfg.Gateway.Host, cfg.Gateway.Port)
	runningServices.ChannelManager.SetupHTTPServer(addr, runningServices.HealthServer)
	registerRuntimeHealthChecks(runningServices.HealthServer, agentLoop, runningServices.ChannelManager)

	if cfg.Gateway.RemoteAdminControl {
		adminH := newAdminHandler(configPath, runningServices.CronService, cfg.Gateway.AdminSecret, agentLoop)
		adminH.registerRoutes(runningServices.ChannelManager.Mux())
		fmt.Printf("✓ Admin API enabled at http://%s:%d/api/admin/*\n", cfg.Gateway.Host, cfg.Gateway.Port)
	}

	if err = runningServices.ChannelManager.StartAll(context.Background()); err != nil {
		return nil, fmt.Errorf("error starting channels: %w", err)
	}

	fmt.Printf("✓ Health endpoints available at http://%s:%d/health and /ready\n", cfg.Gateway.Host, cfg.Gateway.Port)

	stateManager := state.NewManager(cfg.WorkspacePath())
	runningServices.DeviceService = devices.NewService(devices.Config{
		Enabled:    cfg.Devices.Enabled,
		MonitorUSB: cfg.Devices.MonitorUSB,
	}, stateManager)
	runningServices.DeviceService.SetBus(msgBus)
	if err = runningServices.DeviceService.Start(context.Background()); err != nil {
		logger.ErrorCF("device", "Error starting device service", map[string]any{"error": err.Error()})
	} else if cfg.Devices.Enabled {
		fmt.Println("✓ Device event service started")
	}

	if cfg.Heartbeat.Enabled {
		runningServices.HeartbeatService = heartbeat.NewHeartbeatService(
			cfg.Heartbeat,
			cfg.WorkspacePath(),
			agentLoop,
			msgBus,
			stateManager,
		)
		if err = runningServices.HeartbeatService.Start(); err != nil {
			return nil, fmt.Errorf("error starting heartbeat service: %w", err)
		}
		fmt.Println("✓ Heartbeat service started")
	}

	return runningServices, nil
}

func stopAndCleanupServices(runningServices *services, shutdownTimeout time.Duration) {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if runningServices.ChannelManager != nil {
		runningServices.ChannelManager.StopAll(shutdownCtx)
	}
	if runningServices.DeviceService != nil {
		runningServices.DeviceService.Stop()
	}
	if runningServices.CronService != nil {
		runningServices.CronService.Stop()
	}
	if runningServices.HeartbeatService != nil {
		runningServices.HeartbeatService.Stop()
	}
	if runningServices.MediaStore != nil {
		if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
			fms.Stop()
		}
	}
}

func shutdownGateway(
	runningServices *services,
	agentLoop *agent.AgentLoop,
	provider providers.LLMProvider,
	fullShutdown bool,
) {
	if cp, ok := provider.(providers.StatefulProvider); ok && fullShutdown {
		cp.Close()
	}

	stopAndCleanupServices(runningServices, gracefulShutdownTimeout)

	agentLoop.Stop()
	agentLoop.Close()

	logger.Info("✓ Gateway stopped")
}

func handleConfigReload(
	ctx context.Context,
	al *agent.AgentLoop,
	newCfg *config.Config,
	providerRef *providers.LLMProvider,
	runningServices *services,
	msgBus *bus.MessageBus,
	allowEmptyStartup bool,
) error {
	logger.Info("🔄 Config file changed, reloading...")

	newModel := newCfg.Agents.Defaults.ModelName
	if newModel == "" {
		newModel = newCfg.Agents.Defaults.Model
	}

	logger.Infof(" New model is '%s', recreating provider...", newModel)

	logger.Info("  Stopping all services...")
	stopAndCleanupServices(runningServices, serviceShutdownTimeout)

	newProvider, newModelID, err := createStartupProvider(newCfg, allowEmptyStartup)
	if err != nil {
		logger.Errorf("  ⚠ Error creating new provider: %v", err)
		logger.Warn("  Attempting to restart services with old provider and config...")
		if restartErr := restartServices(al, runningServices, msgBus); restartErr != nil {
			logger.Errorf("  ⚠ Failed to restart services: %v", restartErr)
		}
		return fmt.Errorf("error creating new provider: %w", err)
	}

	if newModelID != "" {
		newCfg.Agents.Defaults.ModelName = newModelID
	}

	reloadCtx, reloadCancel := context.WithTimeout(context.Background(), providerReloadTimeout)
	defer reloadCancel()

	if err := al.ReloadProviderAndConfig(reloadCtx, newProvider, newCfg); err != nil {
		logger.Errorf("  ⚠ Error reloading agent loop: %v", err)
		if cp, ok := newProvider.(providers.StatefulProvider); ok {
			cp.Close()
		}
		logger.Warn("  Attempting to restart services with old provider and config...")
		if restartErr := restartServices(al, runningServices, msgBus); restartErr != nil {
			logger.Errorf("  ⚠ Failed to restart services: %v", restartErr)
		}
		return fmt.Errorf("error reloading agent loop: %w", err)
	}

	*providerRef = newProvider

	logger.Info("  Restarting all services with new configuration...")
	if err := restartServices(al, runningServices, msgBus); err != nil {
		logger.Errorf("  ⚠ Error restarting services: %v", err)
		return fmt.Errorf("error restarting services: %w", err)
	}

	logger.Info("  ✓ Provider, configuration, and services reloaded successfully (thread-safe)")
	return nil
}

func restartServices(
	al *agent.AgentLoop,
	runningServices *services,
	msgBus *bus.MessageBus,
) error {
	cfg := al.GetConfig()

	execTimeout := time.Duration(cfg.Tools.Cron.ExecTimeoutMinutes) * time.Minute
	var err error
	runningServices.CronService, err = setupCronTool(
		al,
		msgBus,
		cfg.WorkspacePath(),
		cfg.Agents.Defaults.RestrictToWorkspace,
		execTimeout,
		cfg,
	)
	if err != nil {
		return fmt.Errorf("error restarting cron service: %w", err)
	}
	if err = runningServices.CronService.Start(); err != nil {
		return fmt.Errorf("error restarting cron service: %w", err)
	}
	fmt.Println("  ✓ Cron service restarted")

	runningServices.MediaStore = media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{
		Enabled:  cfg.Tools.MediaCleanup.Enabled,
		MaxAge:   time.Duration(cfg.Tools.MediaCleanup.MaxAge) * time.Minute,
		Interval: time.Duration(cfg.Tools.MediaCleanup.Interval) * time.Minute,
	})
	if fms, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
		fms.Start()
	}
	al.SetMediaStore(runningServices.MediaStore)

	runningServices.ChannelManager, err = channels.NewManager(cfg, msgBus, runningServices.MediaStore)
	if err != nil {
		return fmt.Errorf("error recreating channel manager: %w", err)
	}
	al.SetChannelManager(runningServices.ChannelManager)

	enabledChannels := runningServices.ChannelManager.GetEnabledChannels()
	if len(enabledChannels) > 0 {
		fmt.Printf("  ✓ Channels enabled: %s\n", enabledChannels)
	} else {
		fmt.Println("  ⚠ Warning: No channels enabled")
	}

	addr := fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port)
	runningServices.HealthServer = health.NewServer(cfg.Gateway.Host, cfg.Gateway.Port)
	runningServices.ChannelManager.SetupHTTPServer(addr, runningServices.HealthServer)
	registerRuntimeHealthChecks(runningServices.HealthServer, al, runningServices.ChannelManager)

	if cfg.Gateway.RemoteAdminControl {
		adminH := newAdminHandler(runningServices.configPath, runningServices.CronService, cfg.Gateway.AdminSecret, al)
		adminH.registerRoutes(runningServices.ChannelManager.Mux())
	}

	if err = runningServices.ChannelManager.StartAll(context.Background()); err != nil {
		return fmt.Errorf("error restarting channels: %w", err)
	}
	fmt.Printf(
		"  ✓ Channels restarted, health endpoints at http://%s:%d/health and ready\n",
		cfg.Gateway.Host,
		cfg.Gateway.Port,
	)

	stateManager := state.NewManager(cfg.WorkspacePath())
	runningServices.DeviceService = devices.NewService(devices.Config{
		Enabled:    cfg.Devices.Enabled,
		MonitorUSB: cfg.Devices.MonitorUSB,
	}, stateManager)
	runningServices.DeviceService.SetBus(msgBus)
	if err := runningServices.DeviceService.Start(context.Background()); err != nil {
		logger.WarnCF("device", "Failed to restart device service", map[string]any{"error": err.Error()})
	} else if cfg.Devices.Enabled {
		fmt.Println("  ✓ Device event service restarted")
	}

	transcriber := voice.DetectTranscriber(cfg)
	al.SetTranscriber(transcriber)
	if transcriber != nil {
		logger.InfoCF("voice", "Transcription re-enabled (agent-level)", map[string]any{"provider": transcriber.Name()})
	} else {
		logger.InfoCF("voice", "Transcription disabled", nil)
	}

	if cfg.Heartbeat.Enabled {
		runningServices.HeartbeatService = heartbeat.NewHeartbeatService(
			cfg.Heartbeat,
			cfg.WorkspacePath(),
			al,
			msgBus,
			stateManager,
		)
		if err := runningServices.HeartbeatService.Start(); err != nil {
			logger.WarnCF("heartbeat", "Failed to restart heartbeat service", map[string]any{"error": err.Error()})
		} else {
			fmt.Println("  ✓ Heartbeat service restarted")
		}
	} else {
		runningServices.HeartbeatService = nil
	}

	return nil
}

func setupConfigWatcherPolling(configPath string, debug bool) (chan *config.Config, func()) {
	configChan := make(chan *config.Config, 1)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		lastModTime := getFileModTime(configPath)
		lastSize := getFileSize(configPath)

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				currentModTime := getFileModTime(configPath)
				currentSize := getFileSize(configPath)

				if currentModTime.After(lastModTime) || currentSize != lastSize {
					if debug {
						logger.Debugf("🔍 Config file change detected")
					}

					time.Sleep(500 * time.Millisecond)

					lastModTime = currentModTime
					lastSize = currentSize

					newCfg, err := config.LoadConfig(configPath)
					if err != nil {
						logger.Errorf("⚠ Error loading new config: %v", err)
						logger.Warn("  Using previous valid config")
						continue
					}

					if err := newCfg.ValidateModelList(); err != nil {
						logger.Errorf("  ⚠ New config validation failed: %v", err)
						logger.Warn("  Using previous valid config")
						continue
					}

					logger.Info("✓ Config file validated and loaded")

					select {
					case configChan <- newCfg:
					default:
						logger.Warn("⚠ Previous config reload still in progress, skipping")
					}
				}
			case <-stop:
				return
			}
		}
	}()

	stopFunc := func() {
		close(stop)
		wg.Wait()
	}

	return configChan, stopFunc
}

func registerRuntimeHealthChecks(hs *health.Server, al *agent.AgentLoop, cm *channels.Manager) {
	if hs == nil {
		return
	}

	hs.RegisterCheck("agent_registry", func() (bool, string) {
		if al == nil {
			return false, "agent loop unavailable"
		}
		count := len(al.GetRegistry().ListAgentIDs())
		if count == 0 {
			return false, "no agents registered"
		}
		return true, fmt.Sprintf("%d agents registered", count)
	})

	hs.RegisterCheck("runtime_reload", func() (bool, string) {
		if al == nil {
			return false, "agent loop unavailable"
		}
		if al.IsReloading() {
			return false, "reload in progress"
		}
		return true, "idle"
	})

	hs.RegisterCheck("channel_manager", func() (bool, string) {
		if cm == nil {
			return false, "channel manager unavailable"
		}
		enabled := cm.GetEnabledChannels()
		if len(enabled) == 0 {
			return false, "no enabled channels"
		}
		return true, strings.Join(enabled, ",")
	})
}

func getFileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func getFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func setupCronTool(
	agentLoop *agent.AgentLoop,
	msgBus *bus.MessageBus,
	workspace string,
	restrict bool,
	execTimeout time.Duration,
	cfg *config.Config,
) (*cron.CronService, error) {
	cronStorePath := filepath.Join(workspace, "cron", "jobs.json")

	cronService := cron.NewCronService(cronStorePath, nil)

	var cronTool *tools.CronTool
	if cfg.Tools.IsToolEnabled("cron") {
		// Extract primary agent identity for webhook sender fields.
		webhookAgentID, webhookAgentName := "", ""
		if def := agentLoop.GetRegistry().GetDefaultAgent(); def != nil {
			webhookAgentID = def.ID
			webhookAgentName = def.Name
			if webhookAgentName == "" {
				webhookAgentName = webhookAgentID
			}
		}
		webhookCfg := cron.WebhookConfig{
			Enabled:  cfg.Tools.Cron.Webhook.Enabled,
			Endpoint: cfg.Tools.Cron.Webhook.Endpoint,
			Secret:   cfg.Tools.Cron.Webhook.Secret,
			UserID:   cfg.Tools.Cron.Webhook.UserID,
		}

		var err error
		cronTool, err = tools.NewCronTool(
			cronService, agentLoop, msgBus, workspace, restrict, execTimeout, cfg,
			webhookCfg, webhookAgentID, webhookAgentName,
		)
		if err != nil {
			return nil, fmt.Errorf("critical error during CronTool initialization: %w", err)
		}

		agentLoop.RegisterTool(cronTool)
	}

	if cronTool != nil {
		cronService.SetOnJob(func(job *cron.CronJob) (string, error) {
			result := cronTool.ExecuteJob(context.Background(), job)
			return result, nil
		})
	}

	return cronService, nil
}
