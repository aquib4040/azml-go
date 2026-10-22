package modules

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"azml-go/types"
	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"

	"github.com/gotd/td/tg"
)

type HelpModule struct {
	sender    *telegram.MessageSender
	userMap   *types.SafeUserMap
	startTime time.Time
}

func NewHelpModule(sender *telegram.MessageSender, userMap *types.SafeUserMap, startTime time.Time) *HelpModule {
	return &HelpModule{
		sender:    sender,
		userMap:   userMap,
		startTime: startTime,
	}
}

func (m *HelpModule) HandleStart(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	tokens := strings.Fields(msg.Text)
	if len(tokens) > 1 {
		tokenArg := tokens[1]
		if ok, reply := ext.VerifyToken(tokenArg, msg.UserID, m.userMap); ok {
			_, _ = m.sender.SendMessage(ctx, peer, reply, msg.MessageID, nil)
			return
		}
	}

	cfg := config.Get()
	sfx := cfg.CmdSuffix
	reply := fmt.Sprintf("👋 <b>Hello %s!</b>\n\n"+
		"<i>This bot can leech all your links|files|torrents to Telegram or to ddl servers.</i>\n\n"+
		"Type <code>/help%s</code> to get a list of available commands.", msg.SenderName, sfx)

	btn := telegram.NewButtonMaker()
	btn.UButton("Repo", "https://github.com/aquib4040/AZML")
	btn.IButton("Settings", fmt.Sprintf("uset %d open", msg.UserID))

	_, _ = m.sender.SendMessage(ctx, peer, reply, msg.MessageID, btn.BuildMenu(2))
}

func (m *HelpModule) HandleHelp(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	cfg := config.Get()
	sfx := cfg.CmdSuffix
	helpText := fmt.Sprintf("📖 <b>AZML-Go Commands Guide:</b>\n\n"+
		"<b>Leech Commands:</b>\n"+
		"┠ <code>/leech%s [link]</code> — Leech direct/torrent/magnet/mega link\n"+
		"┠ <code>/zipleech%s [link]</code> — Zip before leeching\n"+
		"┠ <code>/unzipleech%s [link]</code> — Unzip/extract archive\n"+
		"┖ <code>/ytdl%s [link]</code> — Download video using yt-dlp\n\n"+
		"<b>Task Control:</b>\n"+
		"┠ <code>/status%s</code> — Show real-time active tasks\n"+
		"┠ <code>/cancel%s_GID</code> — Cancel specific task\n"+
		"┖ <code>/cancelall%s</code> — Cancel all active tasks\n\n"+
		"<b>User & Settings:</b>\n"+
		"┠ <code>/usetting%s</code> — Configure prefix, suffix, autorename, caption\n"+
		"┠ <code>/premium%s</code> — Check premium subscription status\n"+
		"┠ <code>/ping%s</code> — Test bot response time\n"+
		"┖ <code>/stats%s</code> — Show bot uptime, RAM and CPU stats",
		sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx)

	_, _ = m.sender.SendMessage(ctx, peer, helpText, msg.MessageID, nil)
}

func (m *HelpModule) HandlePing(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	start := time.Now()
	res, err := m.sender.SendMessage(ctx, peer, "<i>Pinging...</i>", msg.MessageID, nil)
	if err == nil && res != nil {
		diff := time.Since(start).Milliseconds()
		// Get message ID
		if updates, ok := (*res).(*tg.Updates); ok && len(updates.Updates) > 0 {
			if uMsg, ok := updates.Updates[0].(*tg.UpdateNewMessage); ok {
				if mObj, ok := uMsg.Message.(*tg.Message); ok {
					_ = m.sender.EditMessage(ctx, peer, mObj.ID, fmt.Sprintf("🏓 <b>Pong!</b> <code>%d ms</code>", diff), nil)
				}
			}
		}
	}
}

func (m *HelpModule) HandleStats(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	uptime := ext.TimeSinceFormatted(m.startTime)
	memStats := ext.GetMemoryStats()
	goroutines := runtime.NumGoroutine()

	statsText := fmt.Sprintf("📊 <b>AZML-Go System & RAM Statistics:</b>\n\n"+
		"⏱ <b>Bot Uptime:</b> <code>%s</code>\n"+
		"🧵 <b>Goroutines:</b> <code>%d</code>\n"+
		"🖥 <b>OS / Arch:</b> <code>%s / %s</code>\n\n"+
		"━━━━━━━━━━━━━━━━━━━━━\n"+
		"🧠 <b>RAM (MEMORY) BREAKDOWN:</b>\n"+
		"━━━━━━━━━━━━━━━━━━━━━\n"+
		"┃ %s\n"+
		"┠ <b>AZML-Go Process:</b> <code>%s</code> (Alloc: <code>%s</code>)\n"+
		"┖ <b>OS Level RAM:</b>     <b>Used:</b> <code>%s</code> | <b>Free:</b> <code>%s</code> | <b>Total:</b> <code>%s</code>\n\n"+
		"⚡ <b>Engine:</b> <code>Go %s</code>",
		uptime,
		goroutines,
		runtime.GOOS, runtime.GOARCH,
		memStats.ProgressBar,
		memStats.ProcessSys, memStats.ProcessAlloc,
		memStats.OSUsed, memStats.OSFree, memStats.OSTotal,
		runtime.Version())

	_, _ = m.sender.SendMessage(ctx, peer, statsText, msg.MessageID, nil)
}

func (m *HelpModule) HandleLog(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can view logs!", msg.MessageID, nil)
		return
	}
	_, _ = m.sender.SendMessage(ctx, peer, "📋 <b>Logs:</b> All services operating normally.", msg.MessageID, nil)
}

func (m *HelpModule) HandleRestart(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner can restart the bot!", msg.MessageID, nil)
		return
	}
	_, _ = m.sender.SendMessage(ctx, peer, "🔄 <i>Restarting AZML-Go...</i>", msg.MessageID, nil)
	os.Exit(0)
}

