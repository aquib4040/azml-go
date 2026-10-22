package modules

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type SettingsModule struct {
	sender  *telegram.MessageSender
	userMap *types.SafeUserMap
}

func NewSettingsModule(sender *telegram.MessageSender, userMap *types.SafeUserMap) *SettingsModule {
	return &SettingsModule{
		sender:  sender,
		userMap: userMap,
	}
}

func (m *SettingsModule) HandleSettings(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	u := m.userMap.GetOrCreate(msg.UserID)

	parts := strings.Fields(msg.Text)
	if len(parts) >= 2 {
		subCmd := strings.ToLower(parts[1])
		switch subCmd {
		case "auto_thumb", "autothumb":
			u.AutoThumb = !u.AutoThumb
			_ = ext.DB.SaveUser(u)
			status := "Disabled"
			if u.AutoThumb {
				status = "Enabled"
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("🖼 <b>Auto Thumbnail</b> is now: <b>%s</b>", status), msg.MessageID, nil)
			return
		case "prefix":
			if len(parts) >= 3 {
				u.Prefix = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("Prefix set to: <code>%s</code>", u.Prefix), msg.MessageID, nil)
				return
			}
		case "suffix":
			if len(parts) >= 3 {
				u.Suffix = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("Suffix set to: <code>%s</code>", u.Suffix), msg.MessageID, nil)
				return
			}
		case "caption":
			if len(parts) >= 3 {
				u.Caption = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("Caption set to: <code>%s</code>", u.Caption), msg.MessageID, nil)
				return
			}
		case "autorename":
			if len(parts) >= 3 {
				u.RemnameAuto = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("AutoRename template set to: <code>%s</code>", u.RemnameAuto), msg.MessageID, nil)
				return
			}
		}
	}

	text, btn := m.buildSettingsMenu(u)
	_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, btn.BuildMenu(2))
}

func (m *SettingsModule) buildSettingsMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	prefix := u.Prefix
	if prefix == "" {
		prefix = "Not Set"
	}
	suffix := u.Suffix
	if suffix == "" {
		suffix = "Not Set"
	}
	autoRename := u.RemnameAuto
	if autoRename == "" {
		autoRename = "Not Set"
	}
	caption := u.Caption
	if caption == "" {
		caption = "Default"
	}
	thumb := "Not Set"
	if u.CustomThumb != "" {
		thumb = "Configured"
	}
	autoThumbStatus := "Disabled"
	if u.AutoThumb {
		autoThumbStatus = "Enabled"
	}
	split := "2 GB (Default)"
	if u.SplitSize > 0 {
		split = ext.GetReadableFileSize(u.SplitSize)
	}

	text := fmt.Sprintf("⚙️ <b>User Settings for</b> <code>%d</code>\n\n"+
		"┠ <b>Prefix:</b> <code>%s</code>\n"+
		"┠ <b>Suffix:</b> <code>%s</code>\n"+
		"┠ <b>AutoRename:</b> <code>%s</code>\n"+
		"┠ <b>Method:</b> <code>%s</code>\n"+
		"┠ <b>Caption:</b> <code>%s</code>\n"+
		"┠ <b>Custom Thumb:</b> <code>%s</code>\n"+
		"┠ <b>TMDB Auto Thumb:</b> <code>%s</code>\n"+
		"┠ <b>Split Size:</b> <code>%s</code>\n"+
		"┖ <b>Dump Channel:</b> <code>%d</code>\n\n"+
		"<i>Click buttons below to toggle or send new settings!</i>",
		u.UserID, prefix, suffix, autoRename, u.RenameMethod, caption, thumb, autoThumbStatus, split, u.DumpChatID)

	btn := telegram.NewButtonMaker()
	btn.IButton("Set Prefix", fmt.Sprintf("uset %d prefix", u.UserID))
	btn.IButton("Set Suffix", fmt.Sprintf("uset %d suffix", u.UserID))
	btn.IButton("AutoRename Tmpl", fmt.Sprintf("uset %d autorename", u.UserID))
	btn.IButton("Toggle Method", fmt.Sprintf("uset %d toggle_method", u.UserID))
	btn.IButton("Set Caption", fmt.Sprintf("uset %d caption", u.UserID))
	if u.AutoThumb {
		btn.IButton("Disable Auto Thumb", fmt.Sprintf("uset %d auto_thumb", u.UserID))
	} else {
		btn.IButton("Enable Auto Thumb", fmt.Sprintf("uset %d auto_thumb", u.UserID))
	}
	btn.IButton("Set Split Size", fmt.Sprintf("uset %d split", u.UserID))
	btn.IButton("Set Dump ID", fmt.Sprintf("uset %d dump", u.UserID))
	btn.IButton("Back", fmt.Sprintf("uset %d back", u.UserID))
	btn.IButton("Close", fmt.Sprintf("uset %d close", u.UserID))

	return text, btn
}

func (m *SettingsModule) HandleCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUserID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 {
		return
	}

	targetUID, _ := strconv.ParseInt(parts[1], 10, 64)
	if targetUID != fromUserID && !telegram.IsOwner(fromUserID) {
		_ = m.sender.AnswerCallback(ctx, queryID, "This menu is not for you!", true)
		return
	}

	action := parts[2]
	u := m.userMap.GetOrCreate(targetUID)

	switch action {
	case "open":
		text, btn := m.buildSettingsMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "close":
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		_ = m.sender.AnswerCallback(ctx, queryID, "Settings closed", false)
	case "back":
		cfg := config.Get()
		sfx := cfg.CmdSuffix
		reply := fmt.Sprintf("👋 <b>Hello!</b>\n\n"+
			"<i>This bot can leech all your links|files|torrents to Telegram or to ddl servers.</i>\n\n"+
			"Type <code>/help%s</code> to get a list of available commands.", sfx)
		btn := telegram.NewButtonMaker()
		btn.UButton("Repo", "https://github.com/aquib4040/AZML")
		btn.IButton("Settings", fmt.Sprintf("uset %d open", targetUID))
		_ = m.sender.EditMessage(ctx, peer, msgID, reply, btn.BuildMenu(2))
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "toggle_method":
		if u.RenameMethod == "auto" {
			u.RenameMethod = "regex"
		} else {
			u.RenameMethod = "auto"
		}
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildSettingsMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Method changed to %s", u.RenameMethod), false)
	case "auto_thumb":
		u.AutoThumb = !u.AutoThumb
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildSettingsMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, btn.BuildMenu(2))
		status := "Disabled"
		if u.AutoThumb {
			status = "Enabled"
		}
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Auto Thumbnail is now %s", status), false)
	default:
		helpText := fmt.Sprintf("To set %s, reply with your value!\nExample: /usetting %s [value]", action, action)
		_ = m.sender.AnswerCallback(ctx, queryID, helpText, true)
	}
}

