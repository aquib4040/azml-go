package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"azml-go/bot"
	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
	"azml-go/helper/pool"
	"azml-go/helper/telegram"
	"azml-go/modules"
	"azml-go/web"

	"github.com/gotd/td/tg"
)

func setupEnvironment() {
	currentPath := os.Getenv("PATH")
	aptBinDirs := []string{"/app/.apt/usr/bin", "/app/.apt/bin", "/usr/local/bin", "/usr/bin", "/bin"}
	for _, dir := range aptBinDirs {
		if !strings.Contains(currentPath, dir) {
			currentPath = dir + string(os.PathListSeparator) + currentPath
		}
	}
	_ = os.Setenv("PATH", currentPath)

	currentLD := os.Getenv("LD_LIBRARY_PATH")
	aptLibDirs := []string{"/app/.apt/usr/lib", "/app/.apt/usr/lib/x86_64-linux-gnu", "/app/.apt/lib/x86_64-linux-gnu"}
	for _, dir := range aptLibDirs {
		if !strings.Contains(currentLD, dir) {
			currentLD = dir + string(os.PathListSeparator) + currentLD
		}
	}
	_ = os.Setenv("LD_LIBRARY_PATH", currentLD)
}

func main() {
	setupEnvironment()

	// Proactive container memory ceiling - 400MB soft target for Heroku 512MB dynos
	debug.SetMemoryLimit(400 * 1024 * 1024)

	// Immediately reclaim and release freed memory back to the OS
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			debug.FreeOSMemory()
		}
	}()

	log.Println("Starting AZML-Go [Dynamic Auto Memory Engine]...")

	// Load configuration
	cfg, err := config.LoadConfig("config.env")
	if err != nil {
		log.Printf("Warning: error loading config: %v", err)
	}

	// Automated Storage Cleanup: clean stale downloads on boot and start periodic cleaner
	ext.CleanDownloadsDir()
	ext.StartPeriodicStorageCleaner()

	// Initialize commands with CMD_SUFFIX
	telegram.InitCommands()

	// Initialize Database (MongoDB or local JSON fallback)
	ext.InitDB()
	ext.LoadShorteners()

	// Ensure Aria2c is started with 16 connections
	ariaClient := downloader.GetAria2Client()
	_ = ariaClient.EnsureStarted()

	// Start Base URL Web Selector Server
	web.StartWebServer()

	// Create Bot Application with gotd MTProto client
	app := bot.NewBotApp()

	// Initialize Multi-Bot SessionPool if worker bot tokens are configured
	var sessPool *pool.SessionPool
	if len(cfg.WorkerBotTokens) > 0 {
		sessionsDir := filepath.Join(cfg.DownloadDir, "gotd_sessions")
		_ = os.MkdirAll(sessionsDir, 0777)
		sessPool = pool.NewSessionPool(sessionsDir)
		log.Printf("[Multi-Bot Pool] Initialized SessionPool with %d worker bot tokens. Warming up...", len(cfg.WorkerBotTokens))
		sessPool.WarmupTokens(cfg.TelegramAPI, cfg.TelegramHash, cfg.WorkerBotTokens, cfg.DumpChatID)
		app.SetSessionPool(sessPool)
	} else {
		log.Println("[Multi-Bot Pool] No BOT_TOKENS / MULTI_TOKENS configured (Single Bot Mode active)")
	}

	// Instantiate and wire modules
	sender := app.GetSender()
	uploader := app.GetUploader()
	userMap := app.GetUserMap()
	startTime := app.GetStartTime()
	ext.SetBotStartTime(startTime)

	if sessPool != nil {
		uploader.SetSessionPool(sessPool, cfg.DumpChatID)
	}

	statusMod := modules.NewStatusModule(sender)
	leechMod := modules.NewLeechModule(app.GetRawAPI(), app.GetUserRawAPI(), sender, uploader, userMap, statusMod, sessPool, cfg.DumpChatID)
	ytdlMod := modules.NewYtdlModule(sender, uploader, userMap, statusMod)
	cancelMod := modules.NewCancelModule(sender, userMap, statusMod)
	settingsMod := modules.NewSettingsModule(app.GetRawAPI(), sender, userMap)
	botSetMod := modules.NewBotSettingsModule(sender, userMap)
	authMod := modules.NewAuthorizeModule(sender, userMap)
	helpMod := modules.NewHelpModule(sender, userMap, startTime)
	speedMod := modules.NewSpeedtestModule(sender)
	miMod := modules.NewMediaInfoModule(sender)
	bcMod := modules.NewBroadcastModule(sender, userMap)
	imdbMod := modules.NewImdbModule(sender)
	animeMod := modules.NewAnimeModule(sender)

	app.SetLeechHandler(leechMod.HandleLeech)
	app.SetYtdlHandler(ytdlMod.HandleYtdl)
	app.SetStatusHandlers(statusMod.HandleStatus, statusMod.HandleCallback)
	app.SetCancelHandlers(cancelMod.HandleCancel, cancelMod.HandleCancelAll, cancelMod.HandleCallback)
	app.SetSettingsHandlers(settingsMod.HandleSettings, settingsMod.HandleCallback, settingsMod.HandleSettingUpdated)
	app.SetAutoRenameHandler(settingsMod.HandleAutoRename)
	app.SetBotSettingsHandlers(botSetMod.HandleBotSettings, botSetMod.HandleCallback)
	app.SetBotSettingsInputHandlers(botSetMod.HandleSessionValue, botSetMod.HandleSessionCancelled)
	app.SetAuthorizeHandlers(
		authMod.HandleAuthorize,
		authMod.HandleUnauthorize,
		authMod.HandleAddSudo,
		authMod.HandleRmSudo,
		authMod.HandleAddPremium,
		authMod.HandleRmPremium,
		authMod.HandleCheckPremium,
	)
	app.SetHelpHandlers(
		helpMod.HandleStart,
		helpMod.HandleHelp,
		helpMod.HandlePing,
		helpMod.HandleStats,
		helpMod.HandleLog,
		helpMod.HandleRestart,
		helpMod.HandleStatsCallback,
	)
	app.SetExtraHandlers(
		speedMod.HandleSpeedtest,
		miMod.HandleMediaInfo,
		bcMod.HandleBroadcast,
		imdbMod.HandleImdb,
		animeMod.HandleAnime,
	)
	app.SetSelectHandlers(
		func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
			modules.HandleMegaSelectCallback(ctx, app.GetRawAPI(), sender, peer, queryID, msgID, data, fromUID)
		},
		func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
			modules.HandleGDriveSelectCallback(ctx, app.GetRawAPI(), sender, peer, queryID, msgID, data, fromUID)
		},
	)

	// Context with graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	defer func() {
		if sessPool != nil {
			log.Println("[Multi-Bot Pool] Stopping all worker bot sessions...")
			sessPool.StopAll()
		}
	}()

	if err := app.Start(ctx); err != nil && err != context.Canceled {
		log.Fatalf("Fatal error running AZML-Go: %v", err)
	}
}
