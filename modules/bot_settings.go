package modules

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"azml-go/config"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type BotSettingsModule struct {
	sender  *telegram.MessageSender
	userMap *types.SafeUserMap
}

func NewBotSettingsModule(sender *telegram.MessageSender, userMap *types.SafeUserMap) *BotSettingsModule {
	return &BotSettingsModule{
		sender:  sender,
		userMap: userMap,
	}
}

func (m *BotSettingsModule) HandleBotSettings(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can access Bot Settings!", msg.MessageID, nil)
		return
	}

	parts := strings.Fields(msg.Text)
	if len(parts) >= 3 {
		key := strings.ToUpper(parts[1])
		val := strings.Join(parts[2:], " ")
		if err := config.UpdateConfigVar(key, val); err == nil {
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>%s</b> has been set to: <code>%s</code>", key, val), msg.MessageID, nil)
			return
		}
	}

	text, btn := m.buildMainMenu()
	_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, btn.BuildMenu(2))
}

func (m *BotSettingsModule) buildMainMenu() (string, *telegram.ButtonMaker) {
	text := "🛠 <b>Bot Settings:</b>\n<i>Select a category to view or edit configuration:</i>"
	btn := telegram.NewButtonMaker()
	btn.IButton("⚙️ Config Variables", "botset var 0")
	btn.IButton("🌐 Base URL & Selection", "botset baseurl")
	btn.IButton("📥 Aria2c Settings", "botset aria")
	btn.IButton("🔗 Shortener Settings", "botset shortener")
	btn.IButton("📁 Private Files", "botset private")
	btn.IButton("❌ Close", "botset close")
	return text, btn
}

func (m *BotSettingsModule) buildVarsMenu(page int) (string, *telegram.ButtonMaker) {
	cfgMap := config.GetConfigMap()
	var keys []string
	for k := range cfgMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	limit := 8
	totalPages := (len(keys) + limit - 1) / limit
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * limit
	end := start + limit
	if end > len(keys) {
		end = len(keys)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⚙️ <b>Config Variables</b> | <b>Page: %d/%d</b>\n\n", page+1, totalPages))

	btn := telegram.NewButtonMaker()
	for i := start; i < end; i++ {
		k := keys[i]
		v := cfgMap[k]
		sb.WriteString(fmt.Sprintf("┠ <b>%s:</b> <code>%s</code>\n", k, v))

		if v == "true" || v == "false" {
			btn.IButton(fmt.Sprintf("Toggle %s", k), fmt.Sprintf("botset toggle %s %d", k, page))
		} else {
			btn.IButton(k, fmt.Sprintf("botset edit %s %d", k, page))
		}
	}
	sb.WriteString("\n<i>Tap on a boolean to toggle it, or a variable to edit.</i>")

	if page > 0 {
		btn.IButton("◀️ Prev", fmt.Sprintf("botset var %d", page-1))
	}
	btn.IButton("🔙 Back", "botset back")
	if page < totalPages-1 {
		btn.IButton("Next ▶️", fmt.Sprintf("botset var %d", page+1))
	}

	return sb.String(), btn
}

func (m *BotSettingsModule) buildAriaMenu() (string, *telegram.ButtonMaker) {
	text := "📥 <b>Aria2c Performance Settings:</b>\n\n" +
		"┠ <b>max-connection-per-server:</b> <code>16</code> (Max)\n" +
		"┠ <b>split:</b> <code>16</code> (Max)\n" +
		"┠ <b>min-split-size:</b> <code>1M</code>\n" +
		"┠ <b>bt-max-peers:</b> <code>128</code>\n" +
		"┠ <b>disk-cache:</b> <code>64M</code>\n" +
		"┖ <b>allow-overwrite:</b> <code>true</code>\n\n" +
		"<i>Optimized for peak network throughput on free tier VPS servers.</i>"

	btn := telegram.NewButtonMaker()
	btn.IButton("🔙 Back", "botset back")
	btn.IButton("❌ Close", "botset close")
	return text, btn
}

func (m *BotSettingsModule) buildShortenerMenu() (string, *telegram.ButtonMaker) {
	cfg := config.Get()
	status := "Disabled"
	if cfg.ShortenerEnabled {
		status = "Enabled"
	}
	domain := cfg.Shortener
	if domain == "" {
		domain = "Not Set"
	}

	text := fmt.Sprintf("🔗 <b>Shortener Settings:</b>\n\n"+
		"┠ <b>Status:</b> <code>%s</code>\n"+
		"┠ <b>Domain:</b> <code>%s</code>\n"+
		"┠ <b>API Key:</b> <code>%s</code>\n"+
		"┠ <b>Token Timeout:</b> <code>%d sec</code>\n"+
		"┖ <b>Login Pass:</b> <code>%s</code>",
		status, domain, cfg.ShortenerAPI, cfg.TokenTimeout, cfg.LoginPass)

	btn := telegram.NewButtonMaker()
	btn.IButton("Toggle Shortener", "botset toggle SHORTENER_ENABLED 0")
	btn.IButton("🔙 Back", "botset back")
	btn.IButton("❌ Close", "botset close")
	return text, btn
}

func (m *BotSettingsModule) buildBaseURLMenu() (string, *telegram.ButtonMaker) {
	cfg := config.Get()
	bURL := cfg.BaseURL
	if bURL == "" {
		bURL = fmt.Sprintf("http://localhost:%d", cfg.BaseURLPort)
	}
	text := fmt.Sprintf("🌐 <b>Base URL & Web Selection Configuration:</b>\n\n"+
		"┠ <b>BASE_URL:</b> <code>%s</code>\n"+
		"┠ <b>BASE_URL_PORT:</b> <code>%d</code>\n"+
		"┖ <b>Web Selector:</b> <i>Active and listening on port %d</i>\n\n"+
		"<i>The Base URL is used for web file selection (Torrents, Magnets, Mega, and Google Drive).</i>",
		bURL, cfg.BaseURLPort, cfg.BaseURLPort)

	btn := telegram.NewButtonMaker()
	btn.IButton("Edit BASE_URL", "botset edit BASE_URL 0")
	btn.IButton("Edit BASE_URL_PORT", "botset edit BASE_URL_PORT 0")
	btn.IButton("🔙 Back", "botset back")
	btn.IButton("❌ Close", "botset close")
	return text, btn
}

func (m *BotSettingsModule) buildPrivateMenu() (string, *telegram.ButtonMaker) {
	text := "📁 <b>Private Files:</b>\n\n" +
		"<i>Send any of these private files as a document directly to the bot:</i>\n\n" +
		"┠ <code>token.pickle</code> — Google Drive OAuth token (automatically loaded)\n" +
		"┠ <code>config.env</code> — Bot environment variables (auto reloaded)\n" +
		"┠ <code>cookies.txt</code> — Netscape cookies for yt-dlp & aria2c\n" +
		"┖ <code>accounts.zip</code> — Service accounts archive\n\n" +
		"<b>Automatic Processing:</b>\n" +
		"When you share <code>token.pickle</code> to the bot, it is automatically saved to the root directory and Google Drive OAuth credentials are reloaded immediately."

	btn := telegram.NewButtonMaker()
	btn.IButton("🔙 Back", "botset back")
	btn.IButton("❌ Close", "botset close")
	return text, btn
}

func (m *BotSettingsModule) HandleCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	if !telegram.IsOwner(fromUID) && !telegram.IsSudo(fromUID, m.userMap) {
		_ = m.sender.AnswerCallback(ctx, queryID, "Only Owner/Sudo can access Bot Settings!", true)
		return
	}

	parts := strings.Fields(data)
	if len(parts) < 2 {
		return
	}

	action := parts[1]
	switch action {
	case "close":
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed", false)
	case "back":
		text, btn := m.buildMainMenu()
		_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "baseurl":
		text, btn := m.buildBaseURLMenu()
		_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "var":
		page := 0
		if len(parts) >= 3 {
			page, _ = strconv.Atoi(parts[2])
		}
		text, btn := m.buildVarsMenu(page)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "toggle":
		if len(parts) >= 3 {
			key := parts[2]
			cfgMap := config.GetConfigMap()
			curr := cfgMap[key] == "true"
			newVal := "false"
			if !curr {
				newVal = "true"
			}
			_ = config.UpdateConfigVar(key, newVal)

			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			text, btn := m.buildVarsMenu(page)
			_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("%s set to %s", key, newVal), false)
		}
	case "edit":
		if len(parts) >= 3 {
			key := parts[2]
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("To edit %s, send /bsetting %s [value]", key, key), true)
		}
	case "aria":
		text, btn := m.buildAriaMenu()
		_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "shortener":
		text, btn := m.buildShortenerMenu()
		_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "private":
		text, btn := m.buildPrivateMenu()
		_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	}
}
