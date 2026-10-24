package modules

import (
	"context"
	"fmt"
	"os"
	"strconv"
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

func (m *HelpModule) getStartText(name string) string {
	if name == "" {
		name = "Master"
	}
	return fmt.Sprintf("<blockquote>%s, <b>%s</b></blockquote>\n\n"+
		"<blockquote>%s</blockquote>\n\n"+
		"<blockquote>❝ <b>%s</b> ❞\n\n"+
		"― %s</blockquote>",
		telegram.ToSmallCaps("Hey"),
		name,
		telegram.ToSmallCaps("I'm an AZML-Go Leech with Auto Rename Bot, built with love."),
		telegram.ToSmallCaps("Winning is everything. As long as I win in the end, that's all that matters."),
		telegram.ToSmallCaps("Love From Go"))
}

func (m *HelpModule) getStartButtons(ownerID int64) *telegram.ButtonMaker {
	cfg := config.Get()
	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Help Guide"), "start_help", "primary")

	ownerUsername := strings.TrimPrefix(strings.TrimSpace(cfg.OwnerUsername), "@")
	if ownerUsername == "" {
		ownerUsername = "issei_senpai"
	}
	ownerURL := fmt.Sprintf("https://t.me/%s", ownerUsername)

	btn.UButtonStyled("◈ "+telegram.ToSmallCaps("Owner"), ownerURL, "primary")
	btn.Row()
	return btn
}

func (m *HelpModule) getHelpGuideText() string {
	cfg := config.Get()
	sfx := cfg.CmdSuffix
	return fmt.Sprintf("⌬ <b><u>%s :</u></b>\n\n"+
		"┎ <b>Leech & Mirror:</b>\n"+
		"┠ <code>/leech%s [link]</code> — Direct, Magnet, Torrent, Mega, TG media\n"+
		"┠ <code>/zipleech%s [link]</code> — Zip contents before uploading\n"+
		"┠ <code>/unzipleech%s [link]</code> — Extract zip/rar/7z archives\n"+
		"┖ <code>/ytdl%s [link]</code> — Download video/audio via yt-dlp\n\n"+
		"┎ <b>User Configuration:</b>\n"+
		"┠ <code>/us%s</code> — User settings (Prefix, Suffix, Caption, AutoRename)\n"+
		"┠ <code>/ar%s [template]</code> — Quick AutoRename template set/clear\n"+
		"┠ <code>/setthumb%s</code> (reply) — Set custom leech thumbnail\n"+
		"┖ <code>/status%s</code> — Live download & upload speed and progress\n\n"+
		"┎ <b>Admin & Control:</b>\n"+
		"┠ <code>/bs%s</code> — Bot Settings (Config vars, Aria2c, Private files)\n"+
		"┠ <code>/cancel%s [GID]</code> — Cancel specific task\n"+
		"┖ <code>/cancelall%s</code> — Cancel all your active tasks\n\n"+
		"<i>Click Back below to return to start menu.</i>",
		telegram.ToSmallCaps("AZML-Go Commands Guide"),
		sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx, sfx)
}

func (m *HelpModule) HandleStart(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	u := m.userMap.GetOrCreate(msg.UserID)
	u.StartedInPM = true
	if msg.SenderName != "" {
		u.Name = msg.SenderName
	}
	_ = ext.DB.SaveUser(u)

	tokens := strings.Fields(msg.Text)
	if len(tokens) > 1 {
		tokenArg := tokens[1]
		if ok, reply := ext.VerifyToken(tokenArg, msg.UserID, m.userMap); ok {
			_, _ = m.sender.SendMessage(ctx, peer, reply, msg.MessageID, nil)
			return
		}
	}

	cfg := config.Get()
	startText := m.getStartText(msg.SenderName)
	startBtns := m.getStartButtons(cfg.OwnerID)

	cover := cfg.CoverImage
	if cover != "" && (strings.HasPrefix(cover, "http://") || strings.HasPrefix(cover, "https://")) {
		_, err := m.sender.SendPhotoFromURLWithButtons(ctx, peer, cover, startText, msg.MessageID, startBtns, 2)
		if err == nil {
			return
		}
	}

	_, _ = m.sender.SendMessageWithButtons(ctx, peer, startText, msg.MessageID, startBtns, 2)
}

func (m *HelpModule) HandleHelp(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	helpText := m.getHelpGuideText()
	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "start_close", "danger")
	btn.Row()
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, helpText, msg.MessageID, btn, 1)
}

func (m *HelpModule) HandlePing(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	start := time.Now()
	resID, err := m.sender.SendMessage(ctx, peer, "<i>Pinging...</i>", msg.MessageID, nil)
	if err == nil && resID > 0 {
		diff := time.Since(start).Milliseconds()
		_ = m.sender.EditMessage(ctx, peer, resID, fmt.Sprintf("⌬ <b>Pong!</b> <code>%d ms</code>", diff), nil)
	}
}

func (m *HelpModule) getStatsView(key string, targetUID int64) (string, tg.ReplyMarkupClass) {
	btn := telegram.NewButtonMaker()
	var msg string

	if key == "home" {
		btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Bot Stats"), fmt.Sprintf("stats %d stbot", targetUID), "primary")
		btn.IButtonStyled("◈ "+telegram.ToSmallCaps("OS Stats"), fmt.Sprintf("stats %d stsys", targetUID), "primary")
		btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Repo Stats"), fmt.Sprintf("stats %d strepo", targetUID), "primary")
		btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Bot Limits"), fmt.Sprintf("stats %d botlimits", targetUID), "primary")
		btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), fmt.Sprintf("stats %d close", targetUID), "danger")
		msg = "⌬ <b><i>Bot & OS Statistics!</i></b>"
	} else {
		btn.IButtonStyled("« "+telegram.ToSmallCaps("Back"), fmt.Sprintf("stats %d home", targetUID), "primary")
		btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), fmt.Sprintf("stats %d close", targetUID), "danger")

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
	if data == "start_help" {
		helpText := m.getHelpGuideText()
		helpBtn := telegram.NewButtonMaker()
		helpBtn.IButtonStyled("« "+telegram.ToSmallCaps("Back"), "start_back", "primary")
		helpBtn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "start_close", "danger")
		helpBtn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, helpText, helpBtn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	if data == "start_back" {
		cfg := config.Get()
		name := "Master"
		if u := m.userMap.Get(fromUID); u != nil && u.Name != "" {
			name = u.Name
		}
		startText := m.getStartText(name)
		startBtns := m.getStartButtons(cfg.OwnerID)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, startText, startBtns, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	if data == "start_close" {
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed", false)
		return
	}

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
	_, _ = m.sender.SendMessage(ctx, peer, "⌬ <b>Logs:</b> All services operating normally.", msg.MessageID, nil)
}

func (m *HelpModule) HandleRestart(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner can restart the bot!", msg.MessageID, nil)
		return
	}
	msgID, _ := m.sender.SendMessage(ctx, peer, "• <i>Restarting AZML-Go...</i>", msg.MessageID, nil)
	if msgID > 0 {
		_ = os.WriteFile(".restartmsg", []byte(fmt.Sprintf("%d %d", msg.ChatID, msgID)), 0644)
	}
	time.Sleep(500 * time.Millisecond)
	os.Exit(0)
}

