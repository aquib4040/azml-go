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

	tdDownloader "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

type SettingsModule struct {
	rawAPI  *tg.Client
	sender  *telegram.MessageSender
	userMap *types.SafeUserMap
}

func NewSettingsModule(rawAPI *tg.Client, sender *telegram.MessageSender, userMap *types.SafeUserMap) *SettingsModule {
	return &SettingsModule{
		rawAPI:  rawAPI,
		sender:  sender,
		userMap: userMap,
	}
}

func (m *SettingsModule) HandleSettings(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	u := m.userMap.GetOrCreate(msg.UserID)

	parts := strings.Fields(msg.Text)
	isThumbCmd := false
	if len(parts) >= 1 && strings.Contains(strings.ToLower(parts[0]), "setthumb") {
		isThumbCmd = true
	} else if len(parts) >= 2 {
		subCmd := strings.ToLower(parts[1])
		if subCmd == "thumb" || subCmd == "setthumb" {
			isThumbCmd = true
		}
	}

	if isThumbCmd {
		if msg.IsReply && msg.ReplyToID != 0 && m.rawAPI != nil {
			res, err := m.rawAPI.MessagesGetMessages(ctx, []tg.InputMessageClass{
				&tg.InputMessageID{ID: msg.ReplyToID},
			})
			var targetMsg *tg.Message
			if err == nil {
				switch mRes := res.(type) {
				case *tg.MessagesMessages:
					if len(mRes.Messages) > 0 {
						targetMsg, _ = mRes.Messages[0].(*tg.Message)
					}
				case *tg.MessagesMessagesSlice:
					if len(mRes.Messages) > 0 {
						targetMsg, _ = mRes.Messages[0].(*tg.Message)
					}
				case *tg.MessagesChannelMessages:
					if len(mRes.Messages) > 0 {
						targetMsg, _ = mRes.Messages[0].(*tg.Message)
					}
				}
			}
			if targetMsg != nil && targetMsg.Media != nil {
				if photoMedia, ok := targetMsg.Media.(*tg.MessageMediaPhoto); ok {
					if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
						_ = os.MkdirAll("Thumbnails", 0755)
						destThumb := fmt.Sprintf("Thumbnails/%d.jpg", msg.UserID)
						thumbSize := "m"
						if len(photo.Sizes) > 0 {
							switch s := photo.Sizes[len(photo.Sizes)-1].(type) {
							case *tg.PhotoSize:
								thumbSize = s.Type
							case *tg.PhotoSizeProgressive:
								thumbSize = s.Type
							}
						}
						dl := tdDownloader.NewDownloader()
						loc := photo.AsInputPhotoFileLocation(thumbSize)
						if loc != nil {
							_, err := dl.Download(m.rawAPI, loc).ToPath(ctx, destThumb)
							if err == nil {
								u.CustomThumb = destThumb
								_ = ext.DB.SaveUser(u)
								ext.ClearPendingThumbSession(msg.UserID)
								_, _ = m.sender.SendMessage(ctx, peer, "✅ <b>Custom thumbnail saved from replied photo!</b> It will be attached to all your future leeched files.", msg.MessageID, nil)
								return
							}
						}
					}
				} else if docMedia, ok := targetMsg.Media.(*tg.MessageMediaDocument); ok {
					if doc, ok := docMedia.Document.(*tg.Document); ok {
						_ = os.MkdirAll("Thumbnails", 0755)
						destThumb := fmt.Sprintf("Thumbnails/%d.jpg", msg.UserID)
						dl := tdDownloader.NewDownloader()
						loc := doc.AsInputDocumentFileLocation("")
						if loc != nil {
							_, err := dl.Download(m.rawAPI, loc).ToPath(ctx, destThumb)
							if err == nil {
								u.CustomThumb = destThumb
								_ = ext.DB.SaveUser(u)
								ext.ClearPendingThumbSession(msg.UserID)
								_, _ = m.sender.SendMessage(ctx, peer, "✅ <b>Custom thumbnail saved from replied document!</b> It will be attached to all your future leeched files.", msg.MessageID, nil)
								return
							}
						}
					}
				}
			}
		}

		// Activate 60s listener
		ext.SetPendingThumbSession(msg.UserID, msg.ChatID, msg.MessageID, 60*time.Second)
		promptText := "📸 <b>Send Custom Thumbnail</b>\n\n" +
			"Please send a <b>photo</b> or image document within the next <b>60 seconds</b>.\n\n" +
			"<i>Once received, it will be automatically saved as your custom thumbnail.</i>\n" +
			"<i>Send /cancel to abort.</i>"
		_, _ = m.sender.SendMessage(ctx, peer, promptText, msg.MessageID, nil)
		return
	}

	if len(parts) >= 2 {
		subCmd := strings.ToLower(parts[1])
		switch subCmd {
		case "prefix":
			if len(parts) >= 3 {
				u.Prefix = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>Prefix</b> set to: <code>%s</code>", u.Prefix), msg.MessageID, nil)
				return
			}
		case "suffix":
			if len(parts) >= 3 {
				u.Suffix = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>Suffix</b> set to: <code>%s</code>", u.Suffix), msg.MessageID, nil)
				return
			}
		case "caption":
			if len(parts) >= 3 {
				u.Caption = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>Caption</b> set to: <code>%s</code>", u.Caption), msg.MessageID, nil)
				return
			}
		case "autorename":
			if len(parts) >= 3 {
				u.RemnameAuto = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>AutoRename template</b> set to: <code>%s</code>", u.RemnameAuto), msg.MessageID, nil)
				return
			}
		case "split", "splitsize":
			if len(parts) >= 3 {
				sizeStr := strings.ToUpper(parts[2])
				maxSplit := config.GetMaxSplitSize()
				var bytes int64
				if strings.HasSuffix(sizeStr, "GB") {
					val, _ := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "GB"), 64)
					bytes = int64(val * 1024 * 1024 * 1024)
				} else if strings.HasSuffix(sizeStr, "MB") {
					val, _ := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "MB"), 64)
					bytes = int64(val * 1024 * 1024)
				} else {
					bytes, _ = strconv.ParseInt(sizeStr, 10, 64)
				}
				if bytes > maxSplit || bytes <= 0 {
					bytes = maxSplit
				}
				u.SplitSize = bytes
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>Split Size</b> set to: <code>%s</code>", ext.GetReadableFileSize(u.SplitSize)), msg.MessageID, nil)
				return
			}
		case "dump":
			if len(parts) >= 3 {
				id, _ := strconv.ParseInt(parts[2], 10, 64)
				u.DumpChatID = id
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>Dump Channel ID</b> set to: <code>%d</code>", u.DumpChatID), msg.MessageID, nil)
				return
			}
		case "meta", "metadata":
			if len(parts) >= 3 {
				if u.Metadata == nil {
					u.Metadata = make(map[string]string)
				}
				u.Metadata["all"] = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>Metadata (All)</b> set to: <code>%s</code>", u.Metadata["all"]), msg.MessageID, nil)
				return
			}
		case "title":
			if len(parts) >= 3 {
				if u.Metadata == nil {
					u.Metadata = make(map[string]string)
				}
				u.Metadata["title"] = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>Metadata Title</b> set to: <code>%s</code>", u.Metadata["title"]), msg.MessageID, nil)
				return
			}
		case "artist", "author":
			if len(parts) >= 3 {
				if u.Metadata == nil {
					u.Metadata = make(map[string]string)
				}
				u.Metadata["artist"] = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✅ <b>Metadata Artist</b> set to: <code>%s</code>", u.Metadata["artist"]), msg.MessageID, nil)
				return
			}
		case "autothumb", "auto_thumb":
			u.AutoThumb = !u.AutoThumb
			_ = ext.DB.SaveUser(u)
			st := "Disabled"
			if u.AutoThumb {
				st = "Enabled"
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("🖼 <b>Auto Thumbnail</b> is now: <b>%s</b>", st), msg.MessageID, nil)
			return
		case "asdoc":
			u.AsDoc = !u.AsDoc
			_ = ext.DB.SaveUser(u)
			st := "Disabled"
			if u.AsDoc {
				st = "Enabled"
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("📄 <b>Upload As Document</b> is now: <b>%s</b>", st), msg.MessageID, nil)
			return
		case "equalsplits":
			u.EqualSplits = !u.EqualSplits
			_ = ext.DB.SaveUser(u)
			st := "Disabled"
			if u.EqualSplits {
				st = "Enabled"
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✂️ <b>Equal Splits</b> is now: <b>%s</b>", st), msg.MessageID, nil)
			return
		}
	}

	text, btn := m.buildMainMenu(u, msg.SenderName)
	_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, btn)
}

func (m *SettingsModule) buildMainMenu(u *types.UserSettings, name string) (string, tg.ReplyMarkupClass) {
	if name == "" {
		name = "User"
	}
	text := fmt.Sprintf("⚙️ <b>User Settings for <a href=\"tg://user?id=%d\">%s</a></b> (<code>%d</code>)\n\n"+
		"<i>Select a category below to configure your preferences:</i>",
		u.UserID, name, u.UserID)

	btn := telegram.NewButtonMaker()
	btn.IButton("📥 Leech Settings", fmt.Sprintf("userset %d leech", u.UserID))
	btn.IButton("🏷 Metadata Settings", fmt.Sprintf("userset %d metadata", u.UserID))
	btn.IButton("🌐 Universal Settings", fmt.Sprintf("userset %d universal", u.UserID))
	btn.IButton("❌ Close", fmt.Sprintf("userset %d close", u.UserID))

	return text, btn.BuildMenu(1)
}

func (m *SettingsModule) buildLeechMenu(u *types.UserSettings) (string, tg.ReplyMarkupClass) {
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
	split := ext.GetReadableFileSize(config.GetMaxSplitSize()) + " (Default)"
	if u.SplitSize > 0 {
		split = ext.GetReadableFileSize(u.SplitSize)
	}

	text := fmt.Sprintf("➲ <b><u>Leech Settings for <a href=\"tg://user?id=%d\">%d</a></u></b>\n\n"+
		"┠ <b>Prefix:</b> <code>%s</code>\n"+
		"┠ <b>Suffix:</b> <code>%s</code>\n"+
		"┠ <b>AutoRename Template:</b> <code>%s</code>\n"+
		"┠ <b>Caption:</b> <code>%s</code>\n"+
		"┠ <b>Custom Thumbnail:</b> <code>%s</code>\n"+
		"┠ <b>TMDB Auto Thumb:</b> <code>%s</code>\n"+
		"┠ <b>Split Size:</b> <code>%s</code>\n"+
		"┖ <b>Dump Channel:</b> <code>%d</code>\n\n"+
		"<i>Click buttons below to toggle or configure!</i>",
		u.UserID, u.UserID, prefix, suffix, autoRename, caption, thumb, autoThumbStatus, split, u.DumpChatID)

	btn := telegram.NewButtonMaker()
	btn.IButton("Set Prefix", fmt.Sprintf("userset %d prefix", u.UserID))
	btn.IButton("Set Suffix", fmt.Sprintf("userset %d suffix", u.UserID))
	btn.IButton("AutoRename Tmpl", fmt.Sprintf("userset %d autorename", u.UserID))
	btn.IButton("Set Caption", fmt.Sprintf("userset %d caption", u.UserID))
	btn.IButton("Custom Thumb", fmt.Sprintf("userset %d thumb", u.UserID))
	if u.AutoThumb {
		btn.IButton("Disable TMDB Thumb", fmt.Sprintf("userset %d toggle_autothumb", u.UserID))
	} else {
		btn.IButton("Enable TMDB Thumb", fmt.Sprintf("userset %d toggle_autothumb", u.UserID))
	}
	btn.IButton("Set Split Size", fmt.Sprintf("userset %d split", u.UserID))
	btn.IButton("Set Dump ID", fmt.Sprintf("userset %d dump", u.UserID))
	btn.IButton("🔙 Back", fmt.Sprintf("userset %d main", u.UserID))
	btn.IButton("❌ Close", fmt.Sprintf("userset %d close", u.UserID))

	return text, btn.BuildMenu(2)
}

func (m *SettingsModule) buildMetadataMenu(u *types.UserSettings) (string, tg.ReplyMarkupClass) {
	metaAll := "Not Set"
	metaTitle := "Not Set"
	metaArtist := "Not Set"
	metaDesc := "Not Set"

	if u.Metadata != nil {
		if v, ok := u.Metadata["all"]; ok && v != "" {
			metaAll = v
		}
		if v, ok := u.Metadata["title"]; ok && v != "" {
			metaTitle = v
		}
		if v, ok := u.Metadata["artist"]; ok && v != "" {
			metaArtist = v
		}
		if v, ok := u.Metadata["description"]; ok && v != "" {
			metaDesc = v
		}
	}

	text := fmt.Sprintf("➲ <b><u>Metadata Settings for <a href=\"tg://user?id=%d\">%d</a></u></b>\n\n"+
		"┠ <b>Change All:</b> <code>%s</code>\n"+
		"┠ <b>Title:</b> <code>%s</code>\n"+
		"┠ <b>Artist / Author:</b> <code>%s</code>\n"+
		"┖ <b>Description:</b> <code>%s</code>\n\n"+
		"➲ <b>Description:</b> <i>Configure custom video metadata tags. Send <code>/us meta [text]</code> to change all tags at once, or use individual buttons.</i>",
		u.UserID, u.UserID, metaAll, metaTitle, metaArtist, metaDesc)

	btn := telegram.NewButtonMaker()
	btn.IButton("Change All", fmt.Sprintf("userset %d meta", u.UserID))
	btn.IButton("Set Title", fmt.Sprintf("userset %d title", u.UserID))
	btn.IButton("Set Artist", fmt.Sprintf("userset %d artist", u.UserID))
	btn.IButton("Set Description", fmt.Sprintf("userset %d desc", u.UserID))
	btn.IButton("🔙 Back", fmt.Sprintf("userset %d main", u.UserID))
	btn.IButton("❌ Close", fmt.Sprintf("userset %d close", u.UserID))

	return text, btn.BuildMenu(2)
}

func (m *SettingsModule) buildUniversalMenu(u *types.UserSettings) (string, tg.ReplyMarkupClass) {
	autoThumb := "Disabled"
	if u.AutoThumb {
		autoThumb = "Enabled"
	}
	asDoc := "Disabled"
	if u.AsDoc {
		asDoc = "Enabled"
	}
	equalSplits := "Disabled"
	if u.EqualSplits {
		equalSplits = "Enabled"
	}

	text := fmt.Sprintf("➲ <b><u>Universal Settings for <a href=\"tg://user?id=%d\">%d</a></u></b>\n\n"+
		"┠ <b>Auto Thumbnail:</b> <code>%s</code>\n"+
		"┠ <b>Upload As Document:</b> <code>%s</code>\n"+
		"┖ <b>Equal Splits:</b> <code>%s</code>\n\n"+
		"<i>Click buttons below to toggle options:</i>",
		u.UserID, u.UserID, autoThumb, asDoc, equalSplits)

	btn := telegram.NewButtonMaker()
	btn.IButton("Toggle Auto Thumb", fmt.Sprintf("userset %d toggle_autothumb", u.UserID))
	btn.IButton("Toggle As Doc", fmt.Sprintf("userset %d toggle_asdoc", u.UserID))
	btn.IButton("Toggle Equal Splits", fmt.Sprintf("userset %d toggle_equalsplits", u.UserID))
	btn.IButton("🔙 Back", fmt.Sprintf("userset %d main", u.UserID))
	btn.IButton("❌ Close", fmt.Sprintf("userset %d close", u.UserID))

	return text, btn.BuildMenu(2)
}

func (m *SettingsModule) buildThumbnailMenu(u *types.UserSettings) (string, tg.ReplyMarkupClass) {
	status := "Not Set"
	exists := false
	if u.CustomThumb != "" {
		if _, err := os.Stat(u.CustomThumb); err == nil {
			status = "Configured"
			exists = true
		} else {
			status = "Missing on Disk"
		}
	}

	text := fmt.Sprintf("➲ <b><u>Thumbnail Settings for <a href=\"tg://user?id=%d\">%d</a></u></b>\n\n"+
		"┠ <b>Custom Thumbnail:</b> <code>%s</code>\n"+
		"┖ <b>Stored Path:</b> <code>%s</code>\n\n"+
		"<i>Custom thumbnail will be embedded into your leeched files.</i>\n\n"+
		"• Click <b>Add / Change Thumbnail</b> to activate 60s upload listener.\n"+
		"• Alternatively reply to a photo with <code>/us thumb</code>.",
		u.UserID, u.UserID, status, u.CustomThumb)

	btn := telegram.NewButtonMaker()
	if exists {
		btn.IButton("🖼 View Thumbnail", fmt.Sprintf("userset %d vthumb", u.UserID))
		btn.IButton("🗑 Delete Thumbnail", fmt.Sprintf("userset %d dthumb", u.UserID))
		btn.IButton("🔄 Change Thumbnail", fmt.Sprintf("userset %d cthumb", u.UserID))
	} else {
		btn.IButton("➕ Add Thumbnail", fmt.Sprintf("userset %d cthumb", u.UserID))
	}
	btn.IButton("🔙 Back", fmt.Sprintf("userset %d leech", u.UserID))
	btn.IButton("❌ Close", fmt.Sprintf("userset %d close", u.UserID))

	return text, btn.BuildMenu(2)
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
	case "main", "back", "open":
		text, markup := m.buildMainMenu(u, "")
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "leech":
		text, markup := m.buildLeechMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "metadata":
		text, markup := m.buildMetadataMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "universal":
		text, markup := m.buildUniversalMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "close":
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		_ = m.sender.AnswerCallback(ctx, queryID, "Settings closed", false)
	case "thumb":
		text, markup := m.buildThumbnailMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "cthumb":
		ext.SetPendingThumbSession(targetUID, 0, msgID, 60*time.Second)
		prompt := "📸 <b>Send Custom Thumbnail</b>\n\n" +
			"Please send a <b>photo</b> or image document within the next <b>60 seconds</b>.\n\n" +
			"<i>Once received, it will be automatically saved as your custom thumbnail.</i>"
		btn := telegram.NewButtonMaker()
		btn.IButton("🔙 Cancel", fmt.Sprintf("userset %d cancel_thumb", u.UserID))
		btn.IButton("❌ Close", fmt.Sprintf("userset %d close", u.UserID))
		_ = m.sender.EditMessage(ctx, peer, msgID, prompt, btn.BuildMenu(1))
		_ = m.sender.AnswerCallback(ctx, queryID, "Send your photo within 60s...", false)
	case "cancel_thumb":
		ext.ClearPendingThumbSession(targetUID)
		text, markup := m.buildThumbnailMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		_ = m.sender.AnswerCallback(ctx, queryID, "Thumbnail setting cancelled", false)
	case "vthumb":
		if u.CustomThumb == "" {
			_ = m.sender.AnswerCallback(ctx, queryID, "No thumbnail configured!", true)
			return
		}
		if _, err := os.Stat(u.CustomThumb); err != nil {
			_ = m.sender.AnswerCallback(ctx, queryID, "Thumbnail file not found on disk!", true)
			return
		}
		_, err := m.sender.SendLocalPhoto(ctx, peer, u.CustomThumb, "🖼 <b>Your Custom Thumbnail</b>", 0)
		if err != nil {
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Error sending thumbnail: %v", err), true)
		} else {
			_ = m.sender.AnswerCallback(ctx, queryID, "Thumbnail sent!", false)
		}
	case "dthumb":
		ext.ClearPendingThumbSession(targetUID)
		if u.CustomThumb != "" {
			_ = os.Remove(u.CustomThumb)
		}
		dest := fmt.Sprintf("Thumbnails/%d.jpg", targetUID)
		_ = os.Remove(dest)
		u.CustomThumb = ""
		_ = ext.DB.SaveUser(u)
		text, markup := m.buildThumbnailMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		_ = m.sender.AnswerCallback(ctx, queryID, "✅ Custom thumbnail deleted!", false)
	case "toggle_autothumb":
		u.AutoThumb = !u.AutoThumb
		_ = ext.DB.SaveUser(u)
		text, markup := m.buildLeechMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		st := "Disabled"
		if u.AutoThumb {
			st = "Enabled"
		}
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Auto Thumbnail is now %s", st), false)
	case "toggle_asdoc":
		u.AsDoc = !u.AsDoc
		_ = ext.DB.SaveUser(u)
		text, markup := m.buildUniversalMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		st := "Disabled"
		if u.AsDoc {
			st = "Enabled"
		}
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("As Document is now %s", st), false)
	case "toggle_equalsplits":
		u.EqualSplits = !u.EqualSplits
		_ = ext.DB.SaveUser(u)
		text, markup := m.buildUniversalMenu(u)
		_ = m.sender.EditMessage(ctx, peer, msgID, text, markup)
		st := "Disabled"
		if u.EqualSplits {
			st = "Enabled"
		}
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Equal Splits is now %s", st), false)
	default:
		helpText := fmt.Sprintf("To set %s, send: /us %s [value]", action, action)
		_ = m.sender.AnswerCallback(ctx, queryID, helpText, true)
	}
}
