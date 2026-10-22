package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"azml-go/bot"
	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/modules"
	"azml-go/web"

	"github.com/gotd/td/tg"
)

func main() {
	// Automatic dynamic memory management - unrestricted memory ceiling
	debug.SetMemoryLimit(-1) // No artificial limit, let Go expand dynamically as needed

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
	if _, err := config.LoadConfig("config.env"); err != nil {
		log.Printf("Warning: error loading config: %v", err)
	}

	// Automated Storage Cleanup: clean stale downloads on boot and start periodic cleaner
	ext.CleanDownloadsDir()
	ext.StartPeriodicStorageCleaner()

	// Initialize commands with CMD_SUFFIX
	telegram.InitCommands()

	// Initialize Database (MongoDB or local JSON fallback)
	ext.InitDB()

	// Ensure Aria2c is started with 16 connections
	ariaClient := downloader.GetAria2Client()
	_ = ariaClient.EnsureStarted()

	// Start Base URL Web Selector Server
	web.StartWebServer()

	// Create Bot Application with gotd MTProto client
	app := bot.NewBotApp()

	// Instantiate and wire modules
	sender := app.GetSender()
	uploader := app.GetUploader()
	userMap := app.GetUserMap()
	startTime := app.GetStartTime()
	ext.SetBotStartTime(startTime)

	statusMod := modules.NewStatusModule(sender)
	leechMod := modules.NewLeechModule(app.GetRawAPI(), sender, uploader, userMap, statusMod)
	ytdlMod := modules.NewYtdlModule(sender, uploader, userMap, statusMod)
	cancelMod := modules.NewCancelModule(sender, userMap)
	settingsMod := modules.NewSettingsModule(sender, userMap)
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
	app.SetSettingsHandlers(settingsMod.HandleSettings, settingsMod.HandleCallback)
	app.SetBotSettingsHandlers(botSetMod.HandleBotSettings, botSetMod.HandleCallback)
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

	if err := app.Start(ctx); err != nil && err != context.Canceled {
		log.Fatalf("Fatal error running AZML-Go: %v", err)
	}
}

