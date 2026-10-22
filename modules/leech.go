package modules

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/helper/uploader"
	"azml-go/types"
	"azml-go/web"

	"github.com/gotd/td/tg"
)

type LeechModule struct {
	rawAPI    *tg.Client
	sender    *telegram.MessageSender
	uploader  *uploader.TelegramUploader
	userMap   *types.SafeUserMap
	taskMgr   *ext.TaskManager
	statusMod *StatusModule
}

func NewLeechModule(rawAPI *tg.Client, sender *telegram.MessageSender, uploader *uploader.TelegramUploader, userMap *types.SafeUserMap, statusMod *StatusModule) *LeechModule {
	return &LeechModule{
		rawAPI:    rawAPI,
		sender:    sender,
		uploader:  uploader,
		userMap:   userMap,
		taskMgr:   ext.GetTaskManager(),
		statusMod: statusMod,
	}
}

func (m *LeechModule) HandleLeech(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, isZip, isUnzip bool) {
	cfg := config.Get()

	// Access control & token check
	if msgText, btn := ext.CheckingAccess(msg.UserID, false, cfg.BotToken, m.userMap); msgText != "" {
		var markup tg.ReplyMarkupClass
		if btn != nil {
			markup = btn.BuildMenu(1)
		}
		_, _ = m.sender.SendMessage(ctx, peer, msgText, msg.MessageID, markup)
		return
	}

	opt, link := parseLeechArgs(msg.Text)
	if isZip {
		opt.IsZip = true
	}
	if isUnzip {
		opt.IsExtract = true
	}

	if link == "" && msg.IsReply && msg.ReplyText != "" {
		link = strings.TrimSpace(msg.ReplyText)
	}

	// Bulk Leech Handling (-b / -bulk)
	if strings.Contains(msg.Text, "-b") || strings.Contains(msg.Text, "-bulk") {
		var rawLinks []string
		sourceText := ""
		if msg.IsReply && msg.ReplyText != "" {
			sourceText = msg.ReplyText
		} else if link != "" {
			sourceText = link
		}

		for _, l := range strings.Split(sourceText, "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "http") || strings.HasPrefix(l, "magnet:") {
				rawLinks = append(rawLinks, l)
			}
		}

		if len(rawLinks) == 0 {
			_, _ = m.sender.SendMessage(ctx, peer, "<b>Reply to a message with links separated by new line for bulk leeching!</b>", msg.MessageID, nil)
			return
		}

		bulkLinks := rawLinks
		parts := strings.Fields(msg.Text)
		for idx, p := range parts {
			if (p == "-b" || p == "-bulk") && idx+1 < len(parts) && strings.Contains(parts[idx+1], ":") {
				rangeParts := strings.Split(parts[idx+1], ":")
				start, _ := strconv.Atoi(rangeParts[0])
				end, _ := strconv.Atoi(rangeParts[1])
				if start < len(rawLinks) {
					if end > 0 && end <= len(rawLinks) {
						bulkLinks = rawLinks[start:end]
					} else {
						bulkLinks = rawLinks[start:]
					}
				}
				break
			}
		}

		_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("📦 <b>Bulk Leech Initiated:</b> <code>%d links</code>", len(bulkLinks)), msg.MessageID, nil)

		go func() {
			for _, bLink := range bulkLinks {
				subTaskDir := filepath.Join(cfg.DownloadDir, fmt.Sprintf("%d_%s", msg.UserID, ext.RandomHex(4)))
				_ = os.MkdirAll(subTaskDir, 0755)

				nID, _ := m.sender.SendMessage(context.Background(), peer, fmt.Sprintf("<i>Initiating Leech:</i> <code>%s</code>", truncateString(bLink, 40)), msg.MessageID, nil)
				if nID > 0 && m.statusMod != nil {
					m.statusMod.SetStatusMessage(msg.ChatID, nID, peer)
				}
				m.processLeechPipeline(peer, msg, opt, bLink, subTaskDir, nID)
				time.Sleep(2 * time.Second)
			}
		}()
		return
	}

	// Multi-Message Sequential Leech (-i <count>)
	if opt.Multi > 1 && msg.IsReply && msg.ReplyToID > 0 {
		multiCount := opt.Multi
		baseReplyID := msg.ReplyToID
		go func() {
			for idx := 1; idx < multiCount; idx++ {
				time.Sleep(3 * time.Second)
				targetID := baseReplyID + idx
				res, err := m.rawAPI.MessagesGetMessages(context.Background(), []tg.InputMessageClass{
					&tg.InputMessageID{ID: targetID},
				})
				if err != nil {
					log.Printf("[LEECH -i] Failed to fetch message ID %d: %v", targetID, err)
					continue
				}

				var targetMsg *tg.Message
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

				if targetMsg == nil {
					continue
				}

				nextMsgCtx := &types.MessageContext{
					ChatID:     msg.ChatID,
					MessageID:  targetMsg.ID,
					UserID:     msg.UserID,
					SenderName: msg.SenderName,
					Text:       targetMsg.Message,
					HasMedia:   targetMsg.Media != nil,
					ReplyToID:  targetID,
					IsReply:    true,
				}

				log.Printf("[LEECH -i] Sequentially triggering download for message ID %d (part %d/%d)", targetID, idx+1, multiCount)
				m.HandleLeech(context.Background(), peer, nextMsgCtx, isZip, isUnzip)
			}
		}()
	}

	if link == "" && !msg.HasMedia {
		helpMsg := "<b>Send link along with command or reply to a link/media!</b>\n" +
			"<b>Usage:</b> <code>/leech [link] [flags]</code>\n" +
			"<b>Flags:</b>\n" +
			"<code>-b</code> Bulk Leech (reply to message with links)\n" +
			"<code>-n</code> Custom Name\n" +
			"<code>-z</code> Zip\n" +
			"<code>-e</code> Extract / Unzip\n" +
			"<code>-ss</code> Screenshots (e.g. <code>-ss 5</code>)\n" +
			"<code>-d</code> Upload as Document\n" +
			"<code>-ud</code> Dump Channel ID\n" +
			"<code>-st</code> Extract Streams"
		_, _ = m.sender.SendMessage(ctx, peer, helpMsg, msg.MessageID, nil)
		return
	}

	// Create user download folder
	taskDir := filepath.Join(cfg.DownloadDir, fmt.Sprintf("%d_%s", msg.UserID, ext.RandomHex(4)))
	_ = os.MkdirAll(taskDir, 0755)

	notifyID, _ := m.sender.SendMessage(ctx, peer, "<i>Initiating Leech Task...</i>", msg.MessageID, nil)
	if notifyID > 0 && m.statusMod != nil {
		m.statusMod.SetStatusMessage(msg.ChatID, notifyID, peer)
	}

	go m.processLeechPipeline(peer, msg, opt, link, taskDir, notifyID)
}

func (m *LeechModule) processLeechPipeline(peer tg.InputPeerClass, msg *types.MessageContext, opt types.LeechOptions, link, taskDir string, notifyID int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer os.RemoveAll(taskDir)

	cfg := config.Get()
	user := m.userMap.Get(msg.UserID)
	isPremium := telegram.IsPremium(msg.UserID, m.userMap)

	var downloadedPath string
	var dlErr error

	// 0. Quota and Task Limits Pre-check
	if limitErr := ext.CheckTaskLimits(msg.UserID, 0, "leech", opt.IsExtract, opt.IsZip, 0); limitErr != "" {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "❌ <b>Limit Exceeded:</b>\n"+limitErr, nil)
		return
	}

	// Try DDL API for Telegram files first to download via 16-connection Aria2c
	if cfg.DDLAPI != "" {
		var tgChannelID int64
		var tgMsgID int
		if cid, mid, ok := ext.ParseTelegramLink(link); ok {
			tgChannelID = cid
			if tgChannelID == 0 {
				tgChannelID = msg.ChatID
			}
			tgMsgID = mid
		} else if msg.IsReply && msg.ReplyToID != 0 {
			tgChannelID = msg.ChatID
			tgMsgID = msg.ReplyToID
		}

		if tgMsgID != 0 {
			if ddlURL, err := ext.TryDDLAPI(ctx, tgChannelID, tgMsgID); err == nil && ddlURL != "" {
				link = ddlURL
			}
		}
	}

	// Try Direct Link Generator for host URLs (Mediafire, Pixeldrain, Solidfiles)
	if ext.IsURL(link) {
		if directURL, err := ext.GenerateDirectLink(link); err == nil && directURL != "" {
			link = directURL
		}
	}

	taskUID := fmt.Sprintf("%d_%s", msg.UserID, ext.RandomHex(3))

	displayName := opt.CustomName
	if displayName == "" {
		if ext.IsMagnet(link) {
			displayName = "Torrent/Magnet"
		} else if ext.IsURL(link) {
			displayName = filepath.Base(link)
			if strings.Contains(displayName, "?") {
				displayName = strings.Split(displayName, "?")[0]
			}
		} else if msg.FileName != "" {
			displayName = msg.FileName
		}
	}
	displayName = filepath.Base(displayName)

	uTask := ext.NewUnifiedTask(taskUID, displayName, msg.FileSize, msg.ChatID, msg.UserID, notifyID, cancel)
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

	// 0. Queue Check for Download (Sleep v0 Engine)
	queuedDL, qErr := ext.AcquireDownloadQueue(ctx, taskUID, displayName, msg.FileSize, msg.ChatID, msg.UserID, notifyID, cancel)
	if qErr != nil {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "❌ <i>Download task cancelled while in queue</i>", nil)
		return
	}
	if queuedDL {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "<i>Starting download from queue...</i>", nil)
	}

	// 1. Download Phase
	if downloader.IsGDriveLink(link) {
		gdrive := downloader.GetGDriveClient()
		fileID := downloader.ExtractGDriveID(link)
		if fileID == "" {
			_ = m.sender.EditMessage(ctx, peer, notifyID, "<b>Google Drive Error:</b> Unable to extract file/folder ID", nil)
			return
		}

		info, err := gdrive.GetFileInfo(fileID)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, notifyID, fmt.Sprintf("<b>Google Drive Error:</b> %v", err), nil)
			return
		}

		if info.IsDir {
			files, fErr := gdrive.GetFolderFiles(fileID, "")
			if fErr != nil || len(files) == 0 {
				_ = m.sender.EditMessage(ctx, peer, notifyID, "<b>Google Drive Error:</b> No files found in folder", nil)
				return
			}

			filesToDownload := files
			if opt.Select {
				uidStr := fmt.Sprintf("%d", notifyID)
				doneChan := RegisterGDriveSelectCache(uidStr, files, msg.UserID)
				buttons := BuildGDriveSelectionButtons(uidStr)
				_ = m.sender.EditMessage(ctx, peer, notifyID, "Your Google Drive download paused. Choose files from selection web link then press Done Selecting button.", buttons)

				select {
				case ok := <-doneChan:
					if !ok {
						_ = m.sender.EditMessage(ctx, peer, notifyID, "❌ Google Drive file selection cancelled by user", nil)
						return
					}
					selectedIDs := web.GDriveStore.ReadSelection(uidStr)
					var filtered []downloader.GDriveFile
					for _, f := range files {
						if !f.IsDir && containsString(selectedIDs, f.ID) {
							filtered = append(filtered, f)
						}
					}
					if len(filtered) == 0 {
						_ = m.sender.EditMessage(ctx, peer, notifyID, "❌ No files selected. Download cancelled.", nil)
						return
					}
					filesToDownload = filtered
				case <-ctx.Done():
					return
				}
			}

			_ = m.sender.EditMessage(ctx, peer, notifyID, fmt.Sprintf("<i>Downloading %d Google Drive files...</i>", len(filesToDownload)), nil)
			for _, f := range filesToDownload {
				if f.IsDir {
					continue
				}
				subDir := filepath.Join(taskDir, f.Path)
				_ = os.MkdirAll(subDir, 0755)
				_ = gdrive.DownloadFile(ctx, f.ID, subDir, f.Name, nil)
			}
			downloadedPath = taskDir
		} else {
			fName := info.Name
			if opt.CustomName != "" {
				fName = opt.CustomName
			}
			dlErr = gdrive.DownloadFile(ctx, fileID, taskDir, fName, nil)
			downloadedPath = filepath.Join(taskDir, fName)
		}
	} else if ext.IsMegaLink(link) {
		megaDl := downloader.NewMegaDownloader()
		if opt.Select {
			uidStr := fmt.Sprintf("%d", notifyID)
			mockFiles := []downloader.MegaFileInfo{
				{ID: "0", Name: opt.CustomName, Size: 0, Selected: true},
			}
			doneChan := RegisterMegaSelectCache(uidStr, mockFiles, msg.UserID)
			buttons := BuildMegaSelectionButtons(uidStr)
			_ = m.sender.EditMessage(ctx, peer, notifyID, "Your MEGA download paused. Choose files from selection web link then press Done Selecting button.", buttons)

			select {
			case ok := <-doneChan:
				if !ok {
					_ = m.sender.EditMessage(ctx, peer, notifyID, "❌ MEGA file selection cancelled by user", nil)
					return
				}
			case <-ctx.Done():
				return
			}
		}
		downloadedPath, dlErr = megaDl.Download(ctx, link, taskDir, nil)
	} else if ext.IsMagnet(link) || strings.HasSuffix(link, ".torrent") || ext.IsURL(link) {
		// All torrents, magnets, and DDL links are routed through Aria2c with 16 connections
		aria := downloader.GetAria2Client()
		gid, err := aria.AddURI(link, taskDir, opt.CustomName, nil)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, notifyID, fmt.Sprintf("<b>Download Error:</b> %v", err), nil)
			return
		}

		if opt.Select {
			cfg := config.Get()
			baseURL := cfg.BaseURL
			if baseURL == "" {
				baseURL = fmt.Sprintf("http://localhost:%d", cfg.BaseURLPort)
			}
			b := telegram.NewButtonMaker()
			b.UButton("Select Files", fmt.Sprintf("%s/app/files/%s", strings.TrimSuffix(baseURL, "/"), gid))
			_ = m.sender.EditMessage(ctx, peer, notifyID, "Your torrent download paused. Choose files from selection web link then press Confirm on web page.", b.BuildMenu(1))
		}

		task := downloader.NewAria2DownloadTask(gid, opt.CustomName, taskDir, notifyID, msg.ChatID, msg.UserID)
		downloadedPath, dlErr = task.PollUntilComplete(ctx, func(cur, total int64) {
			uTask.SetProgress(cur, total, task.Speed(), task.ETA())
			if task.Name() != "" {
				uTask.SetName(task.Name())
			}
		})
	} else {
		// Fallback Direct HTTP
		directDl := downloader.NewDirectDownloader()
		dest := filepath.Join(taskDir, "downloaded_file")
		if opt.CustomName != "" {
			dest = filepath.Join(taskDir, opt.CustomName)
		}
		dlErr = directDl.Download(ctx, link, dest, nil)
		downloadedPath = dest
	}

	if dlErr != nil {
		ext.ReleaseDownload(taskUID)
		_ = m.sender.EditMessage(ctx, peer, notifyID, fmt.Sprintf("<b>Download Failed:</b> %v", dlErr), nil)
		return
	}
	ext.ReleaseDownload(taskUID)
	displayName = filepath.Base(downloadedPath)
	uTask.SetName(displayName)
	if fi, err := os.Stat(downloadedPath); err == nil && !fi.IsDir() {
		uTask.SetSize(fi.Size())
		uTask.SetProgress(fi.Size(), fi.Size(), 0, "-")
	}

	// 2. Post-processing: Forceful Archive Extraction (/leech -e, -extract, -uz)
	// Reads binary magic bytes (PK, 7z, Rar!, etc.) even if the file extension was disguised or missed
	if opt.IsExtract {
		uTask.SetStatus(types.StatusExtracting, "7-Zip")
		isArch, archType := ext.IsArchive(downloadedPath)
		if isArch {
			log.Printf("[LEECH -e] Detected archive format '%s' via binary header for: %s", archType, downloadedPath)
			extractDir := filepath.Join(taskDir, "extracted")
			if outDir, err := ext.ForceExtractArchive(downloadedPath, extractDir, ""); err == nil {
				_ = os.Remove(downloadedPath)
				downloadedPath = outDir
			} else {
				log.Printf("[LEECH -e] Extraction error: %v, continuing with original file", err)
			}
		} else if fi, err := os.Stat(downloadedPath); err == nil && fi.IsDir() {
			_ = filepath.Walk(downloadedPath, func(p string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					if isA, aType := ext.IsArchive(p); isA {
						log.Printf("[LEECH -e] Extracting embedded %s archive: %s", aType, p)
						extDir := filepath.Join(filepath.Dir(p), "extracted")
						if _, eErr := ext.ForceExtractArchive(p, extDir, ""); eErr == nil {
							_ = os.Remove(p)
						}
					}
				}
				return nil
			})
		}
	} else if opt.IsZip {
		uTask.SetStatus(types.StatusArchiving, "7-Zip")
		// Compression requested (-z / -zip)
		zipTarget := filepath.Join(taskDir, filepath.Base(downloadedPath)+".zip")
		if err := ext.CompressToZip(downloadedPath, zipTarget, ""); err == nil {
			_ = os.RemoveAll(downloadedPath)
			downloadedPath = zipTarget
		}
	}

	// 3. Post-processing: Stream extraction if requested
	if opt.ExtractStrm {
		strmDir := filepath.Join(taskDir, "extracted_streams")
		if outputs, err := ext.ExtractStreams(downloadedPath, strmDir, []string{"video", "audio", "subtitle"}); err == nil && len(outputs) > 0 {
			downloadedPath = outputs[0]
		}
	}

	// Custom FFmpeg commands (-ff)
	if opt.FFmpegKey != "" && cfg.EnableFFmpegCmds {
		uTask.SetStatus(types.StatusFFmpeg, "FFmpeg")
		rawCmd := opt.FFmpegKey
		if user != nil && user.FFmpegCommands != nil {
			if preset, ok := user.FFmpegCommands[opt.FFmpegKey]; ok {
				rawCmd = preset
			}
		}
		outFFmpeg := filepath.Join(taskDir, "ff_"+filepath.Base(downloadedPath))
		if fErr := ext.ExecuteCustomFFmpeg(downloadedPath, outFFmpeg, rawCmd, cfg.FFmpegEncoding); fErr == nil {
			_ = os.Remove(downloadedPath)
			downloadedPath = outFFmpeg
		} else {
			log.Printf("[FFMPEG] Custom ffmpeg error: %v", fErr)
		}
	}

	// 3. Post-processing: Metadata editing
	if user != nil && len(user.Metadata) > 0 {
		uTask.SetStatus(types.StatusFFmpeg, "FFmpeg")
		outMeta := filepath.Join(taskDir, "meta_"+filepath.Base(downloadedPath))
		if err := ext.EditMetadata(downloadedPath, outMeta, user.Metadata); err == nil {
			_ = os.Remove(downloadedPath)
			downloadedPath = outMeta
		}
	}

	// 4. File Renaming (AutoRename, Regex, Prefix, Suffix, Caption)
	finalName, caption := ext.FormatFilename(filepath.Base(downloadedPath), user, downloadedPath)
	if opt.CustomName != "" {
		finalName = opt.CustomName
	}
	// Auto conversion of symbols like / and \ so no new directory gets created
	finalName = ext.SanitizeFilename(finalName)
	uTask.SetName(finalName)
	renamedPath := filepath.Join(taskDir, finalName)
	if renamedPath != downloadedPath {
		_ = os.Rename(downloadedPath, renamedPath)
		downloadedPath = renamedPath
	}

	// 5. Screenshots Generation
	var ssLinks []string
	if opt.Screenshots > 0 || cfg.ScreenshotsMode {
		ssCount := opt.Screenshots
		if ssCount <= 0 {
			ssCount = 4
		}
		ssLinks, _ = ext.TakeScreenshots(downloadedPath, ssCount)
	}

	// 6. Thumbnail & HD Cover Generation (Custom Thumb, TMDB Auto Poster, or FFmpeg snapshot)
	customThumb := ""
	if user != nil && user.CustomThumb != "" {
		customThumb = user.CustomThumb
	}

	autoThumbEnabled := cfg.AutoThumbnail
	if user != nil && user.AutoThumb {
		autoThumbEnabled = true
	}

	if customThumb == "" && autoThumbEnabled {
		if tmdbThumb, err := ext.GetAutoThumbnail(ctx, finalName, opt.AsDoc, cfg.LeechFilenameRemnameRegex); err == nil && tmdbThumb != "" {
			customThumb = tmdbThumb
		}
	}

	thumbPath := ext.GenerateThumbnail(downloadedPath, customThumb)
	defer func() {
		if thumbPath != "" && thumbPath != customThumb {
			_ = os.Remove(thumbPath)
		}
	}()

	// 7. File Splitting (1.91GB standard, 3.91GB for Telegram Premium userbot)
	uTask.SetStatus(types.StatusSplitting, "7-Zip")
	maxSplit := config.GetMaxSplitSize()
	splitSize := cfg.LeechSplitSize
	if user != nil && user.SplitSize > 0 {
		splitSize = user.SplitSize
	}
	if splitSize > maxSplit || splitSize <= 0 {
		splitSize = maxSplit
	}

	splitDir := filepath.Join(taskDir, "splits")
	splitParts, splitErr := ext.SplitFile(downloadedPath, splitSize, splitDir)
	if splitErr != nil || len(splitParts) == 0 {
		splitParts = []string{downloadedPath}
	}

	// 8. Upload Phase via MTProto streaming
	fi, _ := os.Stat(downloadedPath)
	var uploadSize int64
	if fi != nil {
		uploadSize = fi.Size()
	}

	// Queue Check for Upload (Sleep v0 Engine)
	queuedUP, qUpErr := ext.AcquireUploadQueue(ctx, taskUID, displayName, uploadSize, msg.ChatID, msg.UserID, notifyID, cancel)
	if qUpErr != nil {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "❌ <i>Upload task cancelled while in queue</i>", nil)
		return
	}
	defer ext.ReleaseUpload(taskUID)

	if queuedUP {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "<i>Starting upload from queue...</i>", nil)
	}

	_ = m.sender.EditMessage(ctx, peer, notifyID, "<i>Uploading to Telegram...</i>", nil)

	uTask.SetStatus(types.StatusUploading, "MTProto")
	uTask.SetSize(uploadSize)
	uTask.SetProgress(0, uploadSize, 0, "-")

	dumpID := cfg.LeechLogID
	if opt.UserDump != 0 {
		dumpID = opt.UserDump
	} else if user != nil && user.DumpChatID != 0 {
		dumpID = user.DumpChatID
	}

	for idx, part := range splitParts {
		partCaption := caption
		if len(splitParts) > 1 {
			partCaption = fmt.Sprintf("<b>Part %d/%d</b>\n%s", idx+1, len(splitParts), caption)
		}
		if len(ssLinks) > 0 {
			partCaption += fmt.Sprintf("\n\n📸 <a href=\"%s\">Screenshots</a>", ssLinks[0])
		}

		upOpt := uploader.UploadOptions{
			FilePath:   part,
			FileName:   filepath.Base(part),
			Caption:    partCaption,
			ThumbPath:  thumbPath,
			CoverPath:  customThumb,
			AsDoc:      opt.AsDoc || cfg.AsDocument,
			DumpChatID: dumpID,
			LogChatID:  cfg.LeechLogID,
			IsPremium:  isPremium,
			OnProgress: func(curr, total int64, speed float64, eta string) {
				uTask.SetProgress(curr, total, speed, eta)
			},
		}

		_, upErr := m.uploader.UploadFile(ctx, peer, upOpt)
		if upErr != nil {
			_ = m.sender.EditMessage(ctx, peer, notifyID, fmt.Sprintf("<b>Upload Failed:</b> %v", upErr), nil)
			return
		}
	}

	// Record daily usage upon successful upload
	ext.RecordDailyUsage(msg.UserID, uploadSize)

	if m.statusMod != nil {
		m.statusMod.RemoveStatusMessage(msg.ChatID)
	}
	if notifyID > 0 {
		_ = m.sender.DeleteMessage(ctx, peer, []int{notifyID})
	}
}

func parseLeechArgs(text string) (types.LeechOptions, string) {
	var opt types.LeechOptions
	tokens := strings.Fields(text)
	if len(tokens) <= 1 {
		return opt, ""
	}

	var link string
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		switch tok {
		case "-z", "-zip":
			opt.IsZip = true
		case "-e", "-extract", "-uz", "-unzip":
			opt.IsExtract = true
		case "-d", "-asdoc":
			opt.AsDoc = true
		case "-st", "-streams":
			opt.ExtractStrm = true
		case "-n", "-name":
			if i+1 < len(tokens) {
				opt.CustomName = tokens[i+1]
				i++
			}
		case "-ss", "-screenshots":
			if i+1 < len(tokens) {
				opt.Screenshots, _ = strconv.Atoi(tokens[i+1])
				i++
			}
		case "-ud", "-dump":
			if i+1 < len(tokens) {
				opt.UserDump, _ = strconv.ParseInt(tokens[i+1], 10, 64)
				i++
			}
		case "-ff":
			if i+1 < len(tokens) {
				opt.FFmpegKey = tokens[i+1]
				i++
			}
		case "-i":
			if i+1 < len(tokens) {
				opt.Multi, _ = strconv.Atoi(tokens[i+1])
				i++
			}
		case "-s", "-select":
			opt.Select = true
		default:
			if strings.HasPrefix(tok, "http") || strings.HasPrefix(tok, "magnet:") {
				link = tok
			}
		}
	}
	return opt, link
}

