package modules

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

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
	resID, err := m.sender.SendMessage(ctx, peer, "<i>Pinging...</i>", msg.MessageID, nil)
	if err == nil && resID > 0 {
		diff := time.Since(start).Milliseconds()
		_ = m.sender.EditMessage(ctx, peer, resID, fmt.Sprintf("🏓 <b>Pong!</b> <code>%d ms</code>", diff), nil)
	}
}

func (m *HelpModule) getStatsView(key string, targetUID int64) (string, tg.ReplyMarkupClass) {
	btn := telegram.NewButtonMaker()
	var msg string

	if key == "home" {
		btn.IButton("Bot Stats", fmt.Sprintf("stats %d stbot", targetUID))
		btn.IButton("OS Stats", fmt.Sprintf("stats %d stsys", targetUID))
		btn.IButton("Repo Stats", fmt.Sprintf("stats %d strepo", targetUID))
		btn.IButton("Bot Limits", fmt.Sprintf("stats %d botlimits", targetUID))
		btn.IButton("Close", fmt.Sprintf("stats %d close", targetUID))
		msg = "⌬ <b><i>Bot & OS Statistics!</i></b>"
	} else {
		btn.IButton("Back", fmt.Sprintf("stats %d home", targetUID))
		btn.IButton("Close", fmt.Sprintf("stats %d close", targetUID))

		switch key {
		case "stbot":
			msg = ext.RenderBotStats(m.startTime)
		case "stsys":
			msg = ext.RenderSysStats()
		case "strepo":
			msg = ext.RenderRepoStats()
		case "botlimits":
			msg = ext.RenderBotLimits()
		default:
			msg = "⌬ <b><i>Bot & OS Statistics!</i></b>"
		}
	}

	return msg, btn.BuildMenu(2)
}

func (m *HelpModule) HandleStats(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	text, markup := m.getStatsView("home", msg.UserID)
	_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, markup)
}

func (m *HelpModule) HandleStatsCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 {
		return
	}

	var targetUID int64
	var key string

	if parts[0] == "wzmlx" {
		targetUID, _ = strconv.ParseInt(parts[1], 10, 64)
		if parts[2] == "close" {
			key = "close"
		} else if len(parts) >= 4 && parts[2] == "stats" {
			key = parts[3]
		}
	} else if parts[0] == "stats" {
		targetUID, _ = strconv.ParseInt(parts[1], 10, 64)
		key = parts[2]
	}

	if targetUID != 0 && targetUID != fromUID && !telegram.IsOwner(fromUID) {
		_ = m.sender.AnswerCallback(ctx, queryID, "This menu is not for you!", true)
		return
	}

	if key == "close" {
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed!", false)
		return
	}

	text, markup := m.getStatsView(key, targetUID)
	_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
	_ = m.sender.AnswerCallback(ctx, queryID, "", false)
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
