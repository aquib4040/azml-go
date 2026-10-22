package modules

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/helper/uploader"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type YtdlModule struct {
	sender    *telegram.MessageSender
	uploader  *uploader.TelegramUploader
	userMap   *types.SafeUserMap
	taskMgr   *ext.TaskManager
	statusMod *StatusModule
}

func NewYtdlModule(sender *telegram.MessageSender, uploader *uploader.TelegramUploader, userMap *types.SafeUserMap, statusMod *StatusModule) *YtdlModule {
	return &YtdlModule{
		sender:    sender,
		uploader:  uploader,
		userMap:   userMap,
		taskMgr:   ext.GetTaskManager(),
		statusMod: statusMod,
	}
}

func (m *YtdlModule) HandleYtdl(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	cfg := config.Get()

	// Access control
	if msgText, btn := ext.CheckingAccess(msg.UserID, false, cfg.BotToken, m.userMap); msgText != "" {
		var markup tg.ReplyMarkupClass
		if btn != nil {
			markup = btn.BuildMenu(1)
		}
		_, _ = m.sender.SendMessage(ctx, peer, msgText, msg.MessageID, markup)
		return
	}

	tokens := strings.Fields(msg.Text)
	if len(tokens) <= 1 && (!msg.IsReply || msg.ReplyText == "") {
		helpMsg := "<b>Send YouTube / media link to download!</b>\n" +
			"<b>Usage:</b> <code>/ytdl [link] [quality]</code>\n" +
			"<b>Examples:</b>\n" +
			"<code>/ytdl https://youtu.be/...</code>\n" +
			"<code>/ytdl https://youtu.be/... 720p</code>"
		_, _ = m.sender.SendMessage(ctx, peer, helpMsg, msg.MessageID, nil)
		return
	}

	link := ""
	customQuality := ""
	if len(tokens) > 1 {
		link = tokens[1]
	}
	if len(tokens) > 2 {
		customQuality = tokens[2]
	}
	if link == "" && msg.IsReply {
		link = strings.TrimSpace(msg.ReplyText)
	}

	taskDir := filepath.Join(cfg.DownloadDir, fmt.Sprintf("yt_%d_%s", msg.UserID, ext.RandomHex(4)))
	_ = os.MkdirAll(taskDir, 0755)

	notifyID, _ := m.sender.SendMessage(ctx, peer, "<i>Fetching media info with yt-dlp...</i>", msg.MessageID, nil)
	if notifyID > 0 && m.statusMod != nil {
		m.statusMod.SetStatusMessage(msg.ChatID, notifyID, peer)
	}

	go m.processYtdl(peer, msg, link, customQuality, taskDir, notifyID)
}

func (m *YtdlModule) processYtdl(peer tg.InputPeerClass, msg *types.MessageContext, link, customQuality, taskDir string, notifyID int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer os.RemoveAll(taskDir)

	cfg := config.Get()
	user := m.userMap.Get(msg.UserID)
	isPremium := telegram.IsPremium(msg.UserID, m.userMap)

	taskUID := fmt.Sprintf("yt_%d_%s", msg.UserID, ext.RandomHex(3))
	uTask := ext.NewUnifiedTask(taskUID, "yt-dlp Media", 0, msg.ChatID, msg.UserID, notifyID, cancel)
	uTask.SetEngine("yt-dlp")
	m.taskMgr.AddTask(uTask)
	defer func() {
		m.taskMgr.RemoveTask(taskUID)
		if m.statusMod != nil {
			if m.taskMgr.Count() == 0 {
				m.statusMod.DeleteAllMessages(context.Background())
			} else {
				go m.statusMod.UpdateAllMessages(context.Background(), true)
			}
		}
	}()

	yt := downloader.NewYtDlpDownloader()
	downloadedFile, err := yt.Download(ctx, link, taskDir, customQuality, func(curr, total int64, speed float64, eta string) {
		uTask.SetProgress(curr, total, speed, eta)
	})
	if err != nil {
		_ = m.sender.EditMessage(ctx, peer, notifyID, fmt.Sprintf("<b>yt-dlp Download Failed:</b> %v", err), nil)
		return
	}

	finalName, caption := ext.FormatFilename(filepath.Base(downloadedFile), user, downloadedFile)
	finalName = ext.SanitizeFilename(finalName)
	renamedPath := filepath.Join(taskDir, finalName)
	if renamedPath != downloadedFile {
		_ = os.Rename(downloadedFile, renamedPath)
		downloadedFile = renamedPath
	}

	if fi, sErr := os.Stat(downloadedFile); sErr == nil {
		uTask.SetName(finalName)
		uTask.SetSize(fi.Size())
	}
	uTask.SetStatus(types.StatusUploading, "Gotd Uploader")

	customThumb := ""
	if user != nil && user.CustomThumb != "" {
		customThumb = user.CustomThumb
	}

	autoThumbEnabled := cfg.AutoThumbnail
	if user != nil && user.AutoThumb {
		autoThumbEnabled = true
	}

	if customThumb == "" && autoThumbEnabled {
		if tmdbThumb, err := ext.GetAutoThumbnail(ctx, finalName, cfg.AsDocument, cfg.LeechFilenameRemnameRegex); err == nil && tmdbThumb != "" {
			customThumb = tmdbThumb
		}
	}

	thumbPath := ext.GenerateThumbnail(downloadedFile, customThumb)
	defer func() {
		if thumbPath != "" && thumbPath != customThumb {
			_ = os.Remove(thumbPath)
		}
	}()

	upOpt := uploader.UploadOptions{
		FilePath:   downloadedFile,
		FileName:   finalName,
		Caption:    caption,
		ThumbPath:  thumbPath,
		CoverPath:  customThumb,
		AsDoc:      cfg.AsDocument,
		DumpChatID: cfg.LeechLogID,
		LogChatID:  cfg.LeechLogID,
		IsPremium:  isPremium,
		OnProgress: func(curr, total int64, speed float64, eta string) {
			uTask.SetProgress(curr, total, speed, eta)
		},
	}

	_, upErr := m.uploader.UploadFile(ctx, peer, upOpt)
	if upErr != nil {
		log.Printf("[AZML-Go Ytdl] Upload error for %s: %v", finalName, upErr)
		_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("❌ <b>Upload Failed:</b> %v", upErr), msg.MessageID, nil)
		return
	}

	if m.statusMod != nil {
		m.statusMod.RemoveStatusMessage(msg.ChatID)
	}
	if notifyID > 0 {
		_ = m.sender.DeleteMessage(ctx, peer, []int{notifyID})
	}
}
