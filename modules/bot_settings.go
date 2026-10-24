package modules

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
	"azml-go/helper/ext"
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

var configVarKeys = []string{
	"OWNER_USERNAME",
	"LEECH_LOG_ID",
	"STATUS_UPDATE_INTERVAL",
	"STATUS_LIMIT",
	"LEECH_SPLIT_SIZE",
	"AS_DOCUMENT",
	"AUTO_THUMBNAIL",
	"EQUAL_SPLITS",
	"MEDIA_GROUP",
	"LEECH_FILENAME_PREFIX",
	"LEECH_FILENAME_SUFFIX",
	"LEECH_FILENAME_CAPTION",
	"TORRENT_TIMEOUT",
	"BASE_URL",
	"BOT_MAX_TASKS",
	"USER_MAX_TASKS",
	"LEECH_LIMIT",
	"TORRENT_LIMIT",
	"DIRECT_LIMIT",
	"YTDLP_LIMIT",
	"MEGA_LIMIT",
	"DAILY_LEECH_LIMIT",
	"DAILY_TASK_LIMIT",
	"AUTO_DELETE_MESSAGE_DURATION",
}

var defaultVarValues = map[string]string{
	"OWNER_USERNAME":              "issei_senpai",
	"LEECH_LOG_ID":                "0",
	"STATUS_UPDATE_INTERVAL":       "10",
	"STATUS_LIMIT":                 "4",
	"LEECH_SPLIT_SIZE":             "0",
	"AS_DOCUMENT":                  "false",
	"AUTO_THUMBNAIL":               "false",
	"EQUAL_SPLITS":                 "false",
	"MEDIA_GROUP":                  "false",
	"LEECH_FILENAME_PREFIX":        "",
	"LEECH_FILENAME_SUFFIX":        "",
	"LEECH_FILENAME_CAPTION":       "",
	"TORRENT_TIMEOUT":              "0",
	"BASE_URL":                     "",
	"BOT_MAX_TASKS":                "0",
	"USER_MAX_TASKS":               "0",
	"LEECH_LIMIT":                  "0",
	"TORRENT_LIMIT":                "0",
	"DIRECT_LIMIT":                 "0",
	"YTDLP_LIMIT":                  "0",
	"MEGA_LIMIT":                   "0",
	"DAILY_LEECH_LIMIT":            "0",
	"DAILY_TASK_LIMIT":             "0",
	"AUTO_DELETE_MESSAGE_DURATION": "60",
}

func isBoolVar(key string) bool {
	switch key {
	case "AS_DOCUMENT", "AUTO_THUMBNAIL", "EQUAL_SPLITS", "MEDIA_GROUP":
		return true
	default:
		return false
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
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 1)
}

func (m *BotSettingsModule) buildMainMenu() (string, *telegram.ButtonMaker) {
	text := fmt.Sprintf("⌬ <b><u>%s</u> :</b>\n\n<i>Select a category below to view or edit configuration:</i>", telegram.ToSmallCaps("Bot Settings"))
	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("◈ "+telegram.ToSmallCaps("Config Variables"), "botset var 0", "primary")
	btn.Row()
	btn.IButtonStyled("📁 "+telegram.ToSmallCaps("Private Files"), "botset private", "primary")
	btn.Row()
	btn.IButtonStyled("📥 "+telegram.ToSmallCaps("Aria2c Settings"), "botset aria", "primary")
	btn.Row()
	btn.IButtonStyled("🔗 "+telegram.ToSmallCaps("Shortener Settings"), "botset shortener", "primary")
	btn.Row()
	btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "botset close", "danger")
	btn.Row()
	return text, btn
}

func (m *BotSettingsModule) buildVarsMenu(page int) (string, *telegram.ButtonMaker) {
	cfgMap := config.GetConfigMap()
	keys := configVarKeys

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
	sb.WriteString(fmt.Sprintf("⌬ <b><u>%s</u></b> | <b>Page: %d/%d</b>\n\n", telegram.ToSmallCaps("Config Variables"), page+1, totalPages))

	btn := telegram.NewButtonMaker()
	for i := start; i < end; i++ {
		k := keys[i]
		v := cfgMap[k]
		if v == "" {
			v = "Not Set"
		}
		sb.WriteString(fmt.Sprintf("┠ <b>%s:</b> <code>%s</code>\n", k, v))

		btn.IButtonStyled("◈ "+telegram.ToSmallCaps(k), fmt.Sprintf("botset editvar %s %d", k, page), "primary")
		if (i-start)%2 == 1 || i == end-1 {
			btn.Row()
		}
	}
	sb.WriteString("\n<i>Tap on a variable to view, toggle, or edit its value in-place.</i>")

	// Page jump buttons (AZML style numbered jump bar, up to 6 per row)
	for p := 0; p < totalPages; p++ {
		if p == page {
			btn.IButtonStyled(fmt.Sprintf("« %d »", p+1), fmt.Sprintf("botset var %d", p), "success")
		} else {
			btn.IButtonStyled(fmt.Sprintf("%d", p+1), fmt.Sprintf("botset var %d", p), "primary")
		}
		if (p+1)%6 == 0 || p == totalPages-1 {
			btn.Row()
		}
	}

	btn.IButtonStyled("« "+telegram.ToSmallCaps("Back"), "botset back", "primary")
	btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "botset close", "danger")
	btn.Row()

	return sb.String(), btn
}

func (m *BotSettingsModule) buildVarDetailMenu(key string, page int, editing bool) (string, *telegram.ButtonMaker) {
	cfgMap := config.GetConfigMap()
	v, exists := cfgMap[key]
	if !exists || v == "" {
		v = "Not Set"
	}

	btn := telegram.NewButtonMaker()

	if isBoolVar(key) {
		text := fmt.Sprintf("⌬ <b><u>%s</u> :</b>\n\n"+
			"┠ <b>Variable:</b> <code>%s</code>\n"+
			"┖ <b>Current Value:</b> <code>%s</code>\n\n"+
			"<i>Choose a valid value for the above Var:</i>",
			telegram.ToSmallCaps("Boolean Configuration"), key, v)

		btn.IButtonStyled("🟢 "+telegram.ToSmallCaps("True"), fmt.Sprintf("botset boolvar %s true %d", key, page), "success")
		btn.IButtonStyled("🔴 "+telegram.ToSmallCaps("False"), fmt.Sprintf("botset boolvar %s false %d", key, page), "danger")
		btn.Row()
		btn.IButtonStyled("« "+telegram.ToSmallCaps("Back"), fmt.Sprintf("botset var %d", page), "primary")
		btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "botset close", "danger")
		btn.Row()
		return text, btn
	}

	if editing {
		text := fmt.Sprintf("⌬ <b><u>%s</u> :</b>\n\n"+
			"┠ <b>Variable:</b> <code>%s</code>\n"+
			"┖ <b>Current Value:</b> <code>%s</code>\n\n"+
			"<i>Send a valid value for the above Var in chat.</i> <b>Timeout:</b> 60 sec\n"+
			"<i>Send /cancel to abort.</i>",
			telegram.ToSmallCaps("Edit Variable"), key, v)

		btn.IButtonStyled("🛑 "+telegram.ToSmallCaps("Cancel Edit"), fmt.Sprintf("botset editvar %s %d", key, page), "danger")
		btn.IButtonStyled("« "+telegram.ToSmallCaps("Back"), fmt.Sprintf("botset var %d", page), "primary")
		btn.Row()
		return text, btn
	}

	text := fmt.Sprintf("⌬ <b><u>%s</u> :</b>\n\n"+
		"┠ <b>Variable:</b> <code>%s</code>\n"+
		"┖ <b>Current Value:</b> <code>%s</code>\n\n"+
		"<i>Click <b>Edit Value</b> to update this setting directly:</i>",
		telegram.ToSmallCaps("Variable Details"), key, v)

	btn.IButtonStyled("✏️ "+telegram.ToSmallCaps("Edit Value"), fmt.Sprintf("botset editvar_prompt %s %d", key, page), "success")
	btn.IButtonStyled("🔄 "+telegram.ToSmallCaps("Reset"), fmt.Sprintf("botset resetvar %s %d", key, page), "danger")
	btn.Row()
	btn.IButtonStyled("« "+telegram.ToSmallCaps("Back"), fmt.Sprintf("botset var %d", page), "primary")
	btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "botset close", "danger")
	btn.Row()

	return text, btn
}

func (m *BotSettingsModule) HandleSessionValue(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key, val string, page int) {
	_ = config.UpdateConfigVar(key, val)
	text, btn := m.buildVarDetailMenu(key, page, false)
	text += "\n\n✅ <i>Value updated successfully!</i>"
	_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
}

func (m *BotSettingsModule) HandleSessionCancelled(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key string, page int) {
	text, btn := m.buildVarDetailMenu(key, page, false)
	text += "\n\n🛑 <i>Edit cancelled.</i>"
	_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
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
	btn.IButtonStyled("« "+telegram.ToSmallCaps("Back"), "botset back", "primary")
	btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "botset close", "danger")
	btn.Row()
	return text, btn
}

func (m *BotSettingsModule) buildPrivateMenu() (string, *telegram.ButtonMaker) {
	text := fmt.Sprintf("📁 <b><u>%s:</u></b>\n\n"+
		"<i>Send any of these private files as a document directly to the bot:</i>\n\n"+
		"┠ <code>token.pickle</code> — Google Drive OAuth token (automatically loaded)\n"+
		"┠ <code>config.env</code> — Bot environment variables (auto reloaded)\n"+
		"┠ <code>cookies.txt</code> — Netscape cookies for yt-dlp & aria2c\n"+
		"┖ <code>accounts.zip</code> — Service accounts archive\n\n"+
		"<b>Automatic Processing:</b>\n"+
		"When you share <code>token.pickle</code> to the bot, it is automatically saved to the root directory and credentials are reloaded immediately.",
		telegram.ToSmallCaps("Private Files"))

	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("« "+telegram.ToSmallCaps("Back"), "botset back", "primary")
	btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "botset close", "danger")
	btn.Row()
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
		ext.ClearPendingBotSettingSession(fromUID)
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed", false)

	case "back":
		ext.ClearPendingBotSettingSession(fromUID)
		text, btn := m.buildMainMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 1)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "var":
		ext.ClearPendingBotSettingSession(fromUID)
		page := 0
		if len(parts) >= 3 {
			page, _ = strconv.Atoi(parts[2])
		}
		text, btn := m.buildVarsMenu(page)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "editvar":
		ext.ClearPendingBotSettingSession(fromUID)
		if len(parts) >= 3 {
			key := parts[2]
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			text, btn := m.buildVarDetailMenu(key, page, false)
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
			_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		}

	case "editvar_prompt":
		if len(parts) >= 3 {
			key := parts[2]
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			ext.SetPendingBotSettingSession(fromUID, key, 0, msgID, page, 60*time.Second)
			text, btn := m.buildVarDetailMenu(key, page, true)
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
			_ = m.sender.AnswerCallback(ctx, queryID, "Send new value within 60s...", false)
		}

	case "boolvar":
		if len(parts) >= 4 {
			key := parts[2]
			val := parts[3]
			page := 0
			if len(parts) >= 5 {
				page, _ = strconv.Atoi(parts[4])
			}
			_ = config.UpdateConfigVar(key, val)
			text, btn := m.buildVarDetailMenu(key, page, false)
			text += fmt.Sprintf("\n\n✅ <i>%s set to %s!</i>", key, val)
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("%s set to %s", key, val), false)
		}

	case "resetvar":
		if len(parts) >= 3 {
			key := parts[2]
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			defVal := defaultVarValues[key]
			_ = config.UpdateConfigVar(key, defVal)
			text, btn := m.buildVarDetailMenu(key, page, false)
			text += fmt.Sprintf("\n\n🔄 <i>%s reset to default: %s</i>", key, defVal)
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("%s reset to default", key), false)
		}

	case "aria":
		text, btn := m.buildAriaMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "private":
		text, btn := m.buildPrivateMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "shortener":
		text, btn := m.buildShortenerMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "toggleshrt":
		cfg := config.Get()
		if cfg.ShortenerEnabled && cfg.TokenTimeout > 0 {
			_ = config.UpdateConfigVar("SHORTENER_ENABLED", "false")
			_ = m.sender.AnswerCallback(ctx, queryID, "Shortener Disabled!", false)
		} else {
			_ = config.UpdateConfigVar("SHORTENER_ENABLED", "true")
			if cfg.TokenTimeout <= 0 {
				_ = config.UpdateConfigVar("TOKEN_TIMEOUT", "86400")
			}
			_ = m.sender.AnswerCallback(ctx, queryID, "Shortener Enabled (24 Hours)!", false)
		}
		text, btn := m.buildShortenerMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "setshrt":
		if len(parts) >= 3 {
			sec := parts[2]
			_ = config.UpdateConfigVar("TOKEN_TIMEOUT", sec)
			_ = config.UpdateConfigVar("SHORTENER_ENABLED", "true")
			valInt, _ := strconv.ParseInt(sec, 10, 64)
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Validity set to %s", ext.GetReadableTime(valInt)), false)
		}
		text, btn := m.buildShortenerMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
	}
}

func (m *BotSettingsModule) buildShortenerMenu() (string, *telegram.ButtonMaker) {
	cfg := config.Get()
	isActive := cfg.ShortenerEnabled && cfg.TokenTimeout > 0
	valStr := ext.GetReadableTime(cfg.TokenTimeout)
	if !isActive {
		valStr = "Disabled (None)"
	}

	stEmoji := "🔴 Disabled"
	if isActive {
		stEmoji = "🟢 Enabled"
	}

	shDomain := cfg.Shortener
	if shDomain == "" {
		shDomain = "Not Configured"
	}

	text := fmt.Sprintf("⌬ <b><u>%s :</u></b>\n\n"+
		"┎ <b>Shortener Access :</b> <code>%s</code>\n"+
		"┠ <b>Token Validity :</b> <code>%s</code>\n"+
		"┖ <b>Domain :</b> <code>%s</code>\n\n"+
		"➲ <b>Description:</b> <i>When enabled, users must generate a temporary token via shortener before leeching.</i>",
		telegram.ToSmallCaps("Shortener Settings"), stEmoji, valStr, shDomain)

	btn := telegram.NewButtonMaker()
	if isActive {
		btn.IButtonStyled("🔴 "+telegram.ToSmallCaps("Disable Shortener"), "botset toggleshrt", "danger")
	} else {
		btn.IButtonStyled("🟢 "+telegram.ToSmallCaps("Enable Shortener (24h)"), "botset toggleshrt", "success")
	}
	btn.Row()

	btn.IButtonStyled("⏱️ "+telegram.ToSmallCaps("24 Hours"), "botset setshrt 86400", "primary")
	btn.IButtonStyled("⏱️ "+telegram.ToSmallCaps("12 Hours"), "botset setshrt 43200", "primary")
	btn.IButtonStyled("⏱️ "+telegram.ToSmallCaps("6 Hours"), "botset setshrt 21600", "primary")
	btn.Row()

	btn.IButtonStyled("« "+telegram.ToSmallCaps("Back"), "botset back", "primary")
	btn.IButtonStyled("× "+telegram.ToSmallCaps("Close"), "botset close", "danger")
	btn.Row()

	return text, btn
}
