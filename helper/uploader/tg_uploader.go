package uploader

import (
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/pool"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var botAPICopyClient = &http.Client{Timeout: 15 * time.Second}

func randID() int64 {
	var b [8]byte
	_, _ = cryptoRand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]))
}

func copyMessageViaBotAPI(botToken string, toChatID int64, fromChatID int64, msgID int) bool {
	if botToken == "" || toChatID == 0 || fromChatID == 0 || msgID <= 0 {
		return false
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/copyMessage", botToken)
	payload := map[string]interface{}{
		"chat_id":      toChatID,
		"from_chat_id": fromChatID,
		"message_id":   msgID,
	}
	body, _ := json.Marshal(payload)
	resp, err := botAPICopyClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[AZML-Go Uploader] copyMessage HTTP error to %d: %v", toChatID, err)
		return false
	}
	defer resp.Body.Close()
	var res struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if json.NewDecoder(resp.Body).Decode(&res) == nil && res.Ok {
		log.Printf("[AZML-Go Uploader] copyMessage succeeded to %d without forward tag!", toChatID)
		return true
	}
	log.Printf("[AZML-Go Uploader] copyMessage failed to %d: %s", toChatID, res.Description)
	return false
}

func deleteMessageViaBotAPI(botToken string, chatID int64, msgID int) bool {
	if botToken == "" || chatID == 0 || msgID <= 0 {
		return false
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage", botToken)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": msgID,
	}
	body, _ := json.Marshal(payload)
	resp, err := botAPICopyClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var res struct {
		Ok bool `json:"ok"`
	}
	return json.NewDecoder(resp.Body).Decode(&res) == nil && res.Ok
}

type TelegramUploader struct {
	botClient  *tg.Client
	botUp      *uploader.Uploader
	userClient *tg.Client
	userUp     *uploader.Uploader
	sessPool   *pool.SessionPool
	dumpChatID int64
}

func NewTelegramUploader(botClient *tg.Client, userClient *tg.Client) *TelegramUploader {
	botUp := uploader.NewUploader(botClient).WithPartSize(512 * 1024).WithThreads(8)
	var userUp *uploader.Uploader
	if userClient != nil {
		userUp = uploader.NewUploader(userClient).WithPartSize(512 * 1024).WithThreads(8)
	}
	return &TelegramUploader{
		botClient:  botClient,
		botUp:      botUp,
		userClient: userClient,
		userUp:     userUp,
	}
}

func (u *TelegramUploader) SetSessionPool(sessPool *pool.SessionPool, dumpChatID int64) {
	u.sessPool = sessPool
	u.dumpChatID = dumpChatID
}

type UploadProgressFunc func(curr, total int64, speed float64, eta string)

type UploadOptions struct {
	FilePath   string
	FileName   string
	Caption    string
	ThumbPath  string
	CoverPath  string
	AsDoc      bool
	DumpChatID int64
	LogChatID  int64
	UserID     int64
	BotPM      bool
	IsPremium  bool
	GID        string
	OnProgress UploadProgressFunc
}

func isTransientUploadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	transientSubstrings := []string{
		"connection", "reset", "timeout", "deadline", "canceled",
		"engine was closed", "engine forcibly closed", "broken pipe",
		"write tcp", "read tcp", "eof", "wsarecv", "wsasend",
		"forcibly closed", "internal", "worker_busy", "rpc_call_fail",
		"msg_wait_failed", "transport is closing", "network is unreachable",
		"temporary failure", "handshake", "flood_wait", "i/o timeout",
		"broken network", "pipe", "host is down",
	}
	for _, sub := range transientSubstrings {
		if strings.Contains(errStr, sub) {
			return true
		}
	}
	return false
}

func saveBigFilePartWithRetry(ctx context.Context, client *tg.Client, fileID int64, partIndex int, totalParts int, bytes []byte) error {
	const maxAttempts = 25
	attempt := 0
	floodWaitAttempts := 0
	backoff := 1500 * time.Millisecond

	for attempt < maxAttempts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		partCtx, partCancel := context.WithTimeout(ctx, 90*time.Second)
		_, err := client.UploadSaveBigFilePart(partCtx, &tg.UploadSaveBigFilePartRequest{
			FileID:         fileID,
			FilePart:       partIndex,
			FileTotalParts: totalParts,
			Bytes:          bytes,
		})
		partCancel()
		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if d, ok := tgerr.AsFloodWait(err); ok {
			floodWaitAttempts++
			if floodWaitAttempts > 10 {
				return fmt.Errorf("flood wait exceeded for part %d: %w", partIndex, err)
			}
			waitTime := d
			if waitTime < 1*time.Second {
				waitTime = 1 * time.Second
			}
			log.Printf("[AZML-Go Uploader] FloodWait of %v on part %d/%d. Waiting...", waitTime, partIndex, totalParts)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waitTime + time.Duration(rand.Intn(500))*time.Millisecond):
			}
			continue
		}

		attempt++

		if isTransientUploadError(err) {
			jitter := time.Duration(rand.Intn(500)) * time.Millisecond
			sleepDuration := backoff + jitter
			if attempt > 2 {
				log.Printf("[AZML-Go Uploader] Warning: part %d/%d (attempt %d/%d) transient error: %v. Retrying in %v...",
					partIndex, totalParts, attempt, maxAttempts, err, sleepDuration.Round(time.Millisecond))
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDuration):
			}
			backoff = time.Duration(float64(backoff) * 1.5)
			if backoff > 12*time.Second {
				backoff = 12 * time.Second
			}
			continue
		}

		log.Printf("[AZML-Go Uploader] Fatal error uploading part %d: %v", partIndex, err)
		return err
	}
	return fmt.Errorf("exceeded max retries (%d) for part %d", maxAttempts, partIndex)
}

func (u *TelegramUploader) uploadVideoCover(ctx context.Context, client *tg.Client, peer tg.InputPeerClass, coverPath string) (tg.InputPhotoClass, error) {
	if coverPath == "" {
		return nil, nil
	}
	if _, err := os.Stat(coverPath); err != nil {
		return nil, err
	}

	coverUp := uploader.NewUploader(client).WithPartSize(512 * 1024)
	coverFile, err := coverUp.FromPath(ctx, coverPath)
	if err != nil {
		return nil, fmt.Errorf("failed to upload cover file: %w", err)
	}

	uploadReq := &tg.MessagesUploadMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaUploadedPhoto{
			File: coverFile,
		},
	}

	var mediaRes tg.MessageMediaClass
	var uploadErr error
	for attempt := 0; attempt < 5; attempt++ {
		mediaRes, uploadErr = client.MessagesUploadMedia(ctx, uploadReq)
		if uploadErr == nil {
			break
		}
		if d, ok := tgerr.AsFloodWait(uploadErr); ok {
			time.Sleep(d + 500*time.Millisecond)
			continue
		}
		var fallbackPeer tg.InputPeerClass = &tg.InputPeerSelf{}
		if client == u.botClient {
			fallbackPeer = &tg.InputPeerEmpty{}
		}
		if uploadReq.Peer != fallbackPeer {
			uploadReq.Peer = fallbackPeer
			continue
		}
		break
	}
	if uploadErr != nil {
		return nil, fmt.Errorf("messages.uploadMedia failed: %w", uploadErr)
	}

	if photoMedia, ok := mediaRes.(*tg.MessageMediaPhoto); ok {
		if p, ok := photoMedia.Photo.(*tg.Photo); ok {
			return p.AsInput(), nil
		}
	}

	return nil, fmt.Errorf("unexpected media response type %T", mediaRes)
}

func (u *TelegramUploader) UploadFile(ctx context.Context, peer tg.InputPeerClass, opt UploadOptions) (*tg.UpdatesClass, error) {
	fi, err := os.Stat(opt.FilePath)
	if err != nil {
		return nil, err
	}
	fileSize := fi.Size()

	// Max split limit check: 2GB (non-premium/bot) or 4GB (premium/user)
	maxLimit := int64(2097152000)
	if opt.IsPremium || u.userClient != nil {
		maxLimit = int64(4194304000)
	}
	if fileSize > maxLimit {
		return nil, fmt.Errorf("file size %s exceeds maximum upload limit", ext.GetReadableFileSize(fileSize))
	}

	const twoGB = int64(2097152000)
	var clientToUse *tg.Client
	var uploadBotSession *pool.BotSession

	defer func() {
		if uploadBotSession != nil {
			uploadBotSession.ReleaseUpload()
		}
	}()

	fromChatID := telegram.PeerToChatID(peer)
	isGroupChat := (fromChatID != opt.UserID)

	cfg := config.Get()
	effectiveLogChatID := opt.LogChatID
	if effectiveLogChatID == 0 && cfg != nil {
		effectiveLogChatID = cfg.LeechLogID
	}

	effectiveDumpChatID := opt.DumpChatID
	if effectiveDumpChatID == 0 {
		effectiveDumpChatID = u.dumpChatID
	}
	if effectiveDumpChatID == 0 && cfg != nil {
		effectiveDumpChatID = cfg.DumpChatID
	}

	var targetUploadChatID int64

	if fileSize > twoGB {
		if u.userClient == nil || u.userUp == nil {
			return nil, fmt.Errorf("file size %s exceeds 2GB limit; USER_SESSION_STRING is required to upload files > 2GB (up to 4GB)", ext.GetReadableFileSize(fileSize))
		}
		clientToUse = u.userClient
		if isGroupChat {
			if effectiveLogChatID != 0 {
				targetUploadChatID = effectiveLogChatID
			} else if opt.UserID != 0 {
				targetUploadChatID = opt.UserID
			} else {
				targetUploadChatID = fromChatID
			}
		} else {
			targetUploadChatID = opt.UserID
		}
	} else {
		// Concurrent Multi-Bot Upload Routing:
		// 1. If in a group chat, search for an available worker bot session that has posting rights
		// in this group chat. This load-balances concurrent episode uploads across distinct worker bots:
		// - Eliminates Telegram DC FloodWait 2s/3s on UploadSaveBigFilePart.
		// - Multiplies upload throughput (each bot achieves ~15-20 MB/s independently).
		if isGroupChat && fromChatID < 0 && u.sessPool != nil {
			if wBot := u.sessPool.FindBotForChat(ctx, fromChatID); wBot != nil {
				uploadBotSession = wBot
				clientToUse = wBot.API
				targetUploadChatID = fromChatID
				log.Printf("[AZML-Go Uploader] Worker Bot ...%s selected for group upload (target chat %d, active uploads: %d)",
					wBot.SafeSuffix(), targetUploadChatID, wBot.ActiveUploads.Load())
			}
		}

		// 2. If no worker bot found for group chat (or in PM), check if any worker bot can post directly in Leech Log ID
		if clientToUse == nil && effectiveLogChatID != 0 && u.sessPool != nil {
			if wBot := u.sessPool.FindBotForChat(ctx, effectiveLogChatID); wBot != nil {
				uploadBotSession = wBot
				clientToUse = wBot.API
				targetUploadChatID = effectiveLogChatID
				log.Printf("[AZML-Go Uploader] Worker Bot ...%s selected for leech log upload (target chat %d, active uploads: %d)",
					wBot.SafeSuffix(), targetUploadChatID, wBot.ActiveUploads.Load())
			}
		}

		// 3. Fallback: Main Bot handles upload directly to Leech Log ID or User PM
		if clientToUse == nil {
			clientToUse = u.botClient
			if effectiveLogChatID != 0 {
				targetUploadChatID = effectiveLogChatID
			} else if opt.UserID != 0 {
				targetUploadChatID = opt.UserID
			} else {
				targetUploadChatID = fromChatID
			}
			log.Printf("[AZML-Go Uploader] Main Bot selected for upload (target chat %d)", targetUploadChatID)
		}
	}

	uploadStart := time.Now()
	uploadName := opt.FileName
	if uploadName == "" {
		uploadName = filepath.Base(opt.FilePath)
	}

	gidStr := ""
	if opt.GID != "" {
		gidStr = fmt.Sprintf(" (GID: %s)", opt.GID)
	}
	log.Printf("[AZML-Go] Upload started: %s%s", uploadName, gidStr)

	tracker := &uploadProgressTracker{
		uploadStart: uploadStart,
		onProgress:  opt.OnProgress,
	}

	var uploadedFile tg.InputFileClass

	// For small files (< 10 MB), use standard uploader
	if fileSize < 10*1024*1024 {
		up := uploader.NewUploader(clientToUse).
			WithPartSize(512 * 1024).
			WithThreads(4).
			WithProgress(tracker)

		for attempt := 1; attempt <= 5; attempt++ {
			uploadedFile, err = up.FromPath(ctx, opt.FilePath)
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return nil, fmt.Errorf("upload cancelled: %v", ctx.Err())
			}
			log.Printf("[AZML-Go Uploader] Upload attempt %d failed for %s: %v. Retrying in 1s...", attempt, opt.FileName, err)
			time.Sleep(1 * time.Second)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to upload small file parts: %v", err)
		}
	} else {
		// Large file (>= 10 MB): Multi-worker chunk uploader with per-part retries (handles broken pipe seamlessly)
		partSize := int64(512 * 1024)
		totalParts := int32((fileSize + partSize - 1) / partSize)
		fileID := randID()

		f, openErr := os.Open(opt.FilePath)
		if openErr != nil {
			return nil, fmt.Errorf("failed to open file for parallel upload: %w", openErr)
		}
		defer f.Close()

		tasks := make(chan int32, totalParts)
		for i := int32(0); i < totalParts; i++ {
			tasks <- i
		}
		close(tasks)

		var uploadedBytes atomic.Int64
		var errOnce sync.Once
		var firstErr error

		numWorkers := 5
		if envVal := os.Getenv("UPLOAD_THREADS"); envVal != "" {
			if val, pErr := strconv.Atoi(envVal); pErr == nil && val > 0 {
				numWorkers = val
			}
		}
		if numWorkers > 8 {
			numWorkers = 8
		}
		if numWorkers < 2 {
			numWorkers = 2
		}
		if int(totalParts) < numWorkers {
			numWorkers = int(totalParts)
		}

		var wg sync.WaitGroup
		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				// Stagger worker start to avoid simultaneous burst on connection
				time.Sleep(time.Duration(workerID*25) * time.Millisecond)

				for partIndex := range tasks {
					if firstErr != nil || ctx.Err() != nil {
						return
					}
					off := int64(partIndex) * partSize
					pLen := partSize
					if off+pLen > fileSize {
						pLen = fileSize - off
					}
					buf := make([]byte, pLen)
					if _, rErr := f.ReadAt(buf, off); rErr != nil && rErr != io.EOF {
						errOnce.Do(func() {
							firstErr = fmt.Errorf("failed to read part %d: %w", partIndex, rErr)
						})
						return
					}

					saveErr := saveBigFilePartWithRetry(ctx, clientToUse, fileID, int(partIndex), int(totalParts), buf)
					if saveErr != nil {
						errOnce.Do(func() {
							firstErr = fmt.Errorf("UploadSaveBigFilePart failed for part %d: %w", partIndex, saveErr)
						})
						return
					}

					done := uploadedBytes.Add(int64(len(buf)))
					tracker.Report(done, fileSize)

					// Gentle 20ms pacing prevents Telegram DC from issuing FloodWait
					time.Sleep(20 * time.Millisecond)
				}
			}(w)
		}

		wg.Wait()
		if firstErr != nil {
			return nil, firstErr
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		uploadedFile = &tg.InputFileBig{
			ID:    fileID,
			Parts: int(totalParts),
			Name:  uploadName,
		}
	}

	var attributes []tg.DocumentAttributeClass
	extName := strings.ToLower(filepath.Ext(opt.FilePath))
	isVideo := isVideoExt(extName)
	isAudio := isAudioExt(extName) && !opt.AsDoc

	var thumbFile tg.InputFileClass
	// Upload Legacy Thumbnail (320x320)
	if opt.ThumbPath != "" {
		if thumbHandle, tErr := uploader.NewUploader(clientToUse).FromPath(ctx, opt.ThumbPath); tErr == nil {
			thumbFile = thumbHandle
		} else {
			log.Printf("[AZML-Go Uploader] Failed to upload thumb: %v", tErr)
		}
	}

	var videoCover tg.InputPhotoClass
	if isVideo {
		coverPath := opt.CoverPath
		if coverPath == "" {
			coverPath = opt.ThumbPath
		}
		if coverPath != "" {
			cov, cErr := u.uploadVideoCover(ctx, clientToUse, peer, coverPath)
			if cErr == nil && cov != nil {
				videoCover = cov
			} else if cErr != nil {
				log.Printf("[AZML-Go Uploader] Failed to upload video cover from %s: %v", coverPath, cErr)
			}
		}
	}

	attributes = append(attributes, &tg.DocumentAttributeFilename{
		FileName: uploadName,
	})

	mimeType := "application/octet-stream"
	if isVideo {
		mimeType = "video/mp4"
		if extName == ".mkv" {
			mimeType = "video/x-matroska"
		} else if extName == ".webm" {
			mimeType = "video/webm"
		}
		details, _ := ext.ProbeMedia(opt.FilePath)
		dur := 0
		w := 1280
		h := 720
		if details != nil {
			if details.Duration > 0 {
				dur = details.Duration
			}
			if details.Width > 0 && details.Height > 0 {
				w = details.Width
				h = details.Height
			}
		}
		attributes = append(attributes, &tg.DocumentAttributeVideo{
			SupportsStreaming: true,
			Duration:          float64(dur),
			W:                 w,
			H:                 h,
		})
	} else if isAudio {
		mimeType = "audio/mpeg"
		details, _ := ext.ProbeMedia(opt.FilePath)
		dur := 0
		title := uploadName
		performer := "AZML"
		if details != nil {
			dur = details.Duration
			if details.Title != "" {
				title = details.Title
			}
			if details.Artist != "" {
				performer = details.Artist
			}
		}
		attributes = append(attributes, &tg.DocumentAttributeAudio{
			Duration:  dur,
			Title:     title,
			Performer: performer,
		})
	}

	inputMedia := &tg.InputMediaUploadedDocument{
		File:       uploadedFile,
		MimeType:   mimeType,
		Attributes: attributes,
		ForceFile:  opt.AsDoc,
	}
	if thumbFile != nil {
		inputMedia.SetThumb(thumbFile)
	}
	if videoCover != nil {
		inputMedia.SetVideoCover(videoCover)
	}


	plainCaption, captionEntities := telegram.ParseFormatting(opt.Caption)
	if len(plainCaption) > 1024 {
		plainCaption = plainCaption[:1020] + "..."
		plainCaption, captionEntities = telegram.ParseFormatting(plainCaption)
	}
	uploadPeer := peer
	actualDestChatID := fromChatID

	// Destination routing:
	// Set uploadPeer to the chat that matches targetUploadChatID
	if targetUploadChatID == effectiveLogChatID && effectiveLogChatID != 0 {
		chID := getChannelID(effectiveLogChatID)
		var hash int64
		if uploadBotSession != nil {
			if inChan, err := uploadBotSession.ResolveChannel(ctx, effectiveLogChatID); err == nil && inChan != nil {
				hash = inChan.AccessHash
			}
		} else {
			hash = telegram.ResolveChannelAccessHash(ctx, clientToUse, effectiveLogChatID)
			if hash == 0 {
				hash = telegram.GetChannelAccessHash(chID)
			}
		}
		if hash != 0 {
			uploadPeer = &tg.InputPeerChannel{
				ChannelID:  chID,
				AccessHash: hash,
			}
			actualDestChatID = effectiveLogChatID
			log.Printf("[AZML-Go Uploader] Uploading directly to Leech Log channel %d", effectiveLogChatID)
		}
	} else if targetUploadChatID == effectiveDumpChatID && effectiveDumpChatID != 0 {
		chID := getChannelID(effectiveDumpChatID)
		var hash int64
		if uploadBotSession != nil {
			if inChan, err := uploadBotSession.ResolveChannel(ctx, effectiveDumpChatID); err == nil && inChan != nil {
				hash = inChan.AccessHash
			}
		} else {
			hash = telegram.ResolveChannelAccessHash(ctx, clientToUse, effectiveDumpChatID)
			if hash == 0 {
				hash = telegram.GetChannelAccessHash(chID)
			}
		}
		if hash != 0 {
			uploadPeer = &tg.InputPeerChannel{
				ChannelID:  chID,
				AccessHash: hash,
			}
			actualDestChatID = effectiveDumpChatID
			log.Printf("[AZML-Go Uploader] Uploading to Dump channel %d", effectiveDumpChatID)
		}
	} else if targetUploadChatID == opt.UserID && opt.UserID != 0 {
		uHash := telegram.GetUserAccessHash(opt.UserID)
		if uHash != 0 {
			uploadPeer = &tg.InputPeerUser{
				UserID:     opt.UserID,
				AccessHash: uHash,
			}
			actualDestChatID = opt.UserID
			log.Printf("[AZML-Go Uploader] Uploading directly to User PM %d", opt.UserID)
		}
	} else {
		// fromChatID (the group chat where command was given)
		if uploadBotSession != nil && fromChatID < 0 {
			chID := getChannelID(fromChatID)
			if inChan, err := uploadBotSession.ResolveChannel(ctx, fromChatID); err == nil && inChan != nil {
				uploadPeer = &tg.InputPeerChannel{
					ChannelID:  chID,
					AccessHash: inChan.AccessHash,
				}
			} else {
				uploadPeer = peer
			}
		} else {
			uploadPeer = peer
		}
		actualDestChatID = fromChatID
		log.Printf("[AZML-Go Uploader] Uploading to group chat %d", fromChatID)
	}

	req := &tg.MessagesSendMediaRequest{
		Peer:     uploadPeer,
		Media:    inputMedia,
		Message:  plainCaption,
		Entities: captionEntities,
		RandomID: time.Now().UnixNano(),
	}

	var res tg.UpdatesClass
	var sendErr error
	for attempt := 0; attempt < 10; attempt++ {
		res, sendErr = clientToUse.MessagesSendMedia(ctx, req)
		if sendErr == nil {
			break
		}
		// Fallback to command peer if sending to preferred peer failed (only for Main Bot)
		if uploadBotSession == nil && uploadPeer != peer && attempt == 0 {
			log.Printf("[AZML-Go Uploader] Upload to preferred peer failed (%v), falling back to command peer", sendErr)
			uploadPeer = peer
			actualDestChatID = fromChatID
			req.Peer = peer
			continue
		}
		errStr := sendErr.Error()
		if strings.Contains(errStr, "engine was closed") || strings.Contains(errStr, "engine forcibly closed") ||
			strings.Contains(errStr, "connection") || strings.Contains(errStr, "reset") ||
			strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline") ||
			strings.Contains(errStr, "broken pipe") || strings.Contains(errStr, "write tcp") {
			log.Printf("[AZML-Go Uploader] Warning: MessagesSendMedia failed (attempt %d/10): %v. Retrying in 2s...", attempt+1, sendErr)
			time.Sleep(2 * time.Second)
			continue
		}
		break
	}
	if sendErr != nil {
		log.Printf("[AZML-Go Uploader] MessagesSendMedia FAILED for %s (%d bytes) to peer %T: %v", opt.FileName, fileSize, uploadPeer, sendErr)
		return nil, fmt.Errorf("failed to send uploaded media: %v", sendErr)
	}
	log.Printf("[AZML-Go Uploader] Successfully sent uploaded media for %s to peer %T!", opt.FileName, uploadPeer)

	msgID := telegram.ExtractMessageID(res)
	if msgID > 0 {
		botToken := ""
		if cfg != nil {
			botToken = cfg.BotToken
		}
		if botToken == "" {
			botToken = os.Getenv("BOT_TOKEN")
		}

		sentChats := make(map[int64]bool)
		sentChats[actualDestChatID] = true

		// 0. Send to User Personal Dump channel (if user explicitly configured /setdump or -dump)
		if opt.DumpChatID != 0 && actualDestChatID != opt.DumpChatID && !sentChats[opt.DumpChatID] {
			sentChats[opt.DumpChatID] = true
			if !copyMessageViaBotAPI(botToken, opt.DumpChatID, actualDestChatID, msgID) {
				chID := getChannelID(opt.DumpChatID)
				dumpPeer := &tg.InputPeerChannel{
					ChannelID:  chID,
					AccessHash: telegram.GetChannelAccessHash(chID),
				}
				_, dErr := u.botClient.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
					FromPeer:   uploadPeer,
					ToPeer:     dumpPeer,
					ID:         []int{msgID},
					RandomID:   []int64{time.Now().UnixNano() + 1},
					DropAuthor: true,
				})
				if dErr != nil {
					log.Printf("[AZML-Go Uploader] Forward to user personal dump failed: %v", dErr)
				}
			}
		}

		// 1. Send to User PM - ensure user gets the file in PM cleanly via Main Bot
		if opt.UserID != 0 && actualDestChatID != opt.UserID && !sentChats[opt.UserID] {
			sentChats[opt.UserID] = true
			if !copyMessageViaBotAPI(botToken, opt.UserID, actualDestChatID, msgID) {
				userPeer := &tg.InputPeerUser{
					UserID:     opt.UserID,
					AccessHash: telegram.GetUserAccessHash(opt.UserID),
				}
				_, pErr := u.botClient.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
					FromPeer:   uploadPeer,
					ToPeer:     userPeer,
					ID:         []int{msgID},
					RandomID:   []int64{time.Now().UnixNano() + 3},
					DropAuthor: true,
				})
				if pErr != nil {
					log.Printf("[AZML-Go Uploader] Forward to user PM (%d) failed: %v", opt.UserID, pErr)
				}
			}
		}

		// 2. Send to Leech Log ID via Main Bot (Main Bot is already admin in Leech Log)
		if effectiveLogChatID != 0 && actualDestChatID != effectiveLogChatID && !sentChats[effectiveLogChatID] {
			sentChats[effectiveLogChatID] = true
			if !copyMessageViaBotAPI(botToken, effectiveLogChatID, actualDestChatID, msgID) {
				chID := getChannelID(effectiveLogChatID)
				logPeer := &tg.InputPeerChannel{
					ChannelID:  chID,
					AccessHash: telegram.GetChannelAccessHash(chID),
				}
				_, fErr := u.botClient.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
					FromPeer:   uploadPeer,
					ToPeer:     logPeer,
					ID:         []int{msgID},
					RandomID:   []int64{time.Now().UnixNano() + 2},
					DropAuthor: true,
				})
				if fErr != nil {
					log.Printf("[AZML-Go Uploader] Forward to leech log failed: %v", fErr)
				}
			}
		}

		// 3. Clean up intermediate upload in group chat if it was only used for worker bot transfer
		if isGroupChat && actualDestChatID == fromChatID {
			// User requested: "jismein leech kar raha usmein file na aaye only pm and leech log id mein"
			go func(chatID int64, mID int) {
				time.Sleep(1500 * time.Millisecond)
				if !deleteMessageViaBotAPI(botToken, chatID, mID) {
					switch p := peer.(type) {
					case *tg.InputPeerChannel:
						_, _ = clientToUse.ChannelsDeleteMessages(context.Background(), &tg.ChannelsDeleteMessagesRequest{
							Channel: &tg.InputChannel{
								ChannelID:  p.ChannelID,
								AccessHash: p.AccessHash,
							},
							ID: []int{mID},
						})
					default:
						_, _ = clientToUse.MessagesDeleteMessages(context.Background(), &tg.MessagesDeleteMessagesRequest{
							Revoke: true,
							ID:     []int{mID},
						})
					}
				}
			}(fromChatID, msgID)
		}

		// 4. Send to Dump Chat ID if configured and not already there
		if opt.DumpChatID != 0 && !sentChats[opt.DumpChatID] {
			dumpChatID := opt.DumpChatID
			sentChats[dumpChatID] = true
			if !copyMessageViaBotAPI(botToken, dumpChatID, actualDestChatID, msgID) {
				chID := getChannelID(dumpChatID)
				dumpPeer := &tg.InputPeerChannel{
					ChannelID:  chID,
					AccessHash: telegram.GetChannelAccessHash(chID),
				}
				_, fErr := clientToUse.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
					FromPeer:   uploadPeer,
					ToPeer:     dumpPeer,
					ID:         []int{msgID},
					RandomID:   []int64{time.Now().UnixNano() + 1},
					DropAuthor: true,
				})
				if fErr != nil {
					log.Printf("[AZML-Go Uploader] Forward to dump failed: %v", fErr)
				}
			}
		}
	}

	return &res, nil
}

type uploadProgressTracker struct {
	uploadStart time.Time
	lastReport  time.Time
	onProgress  UploadProgressFunc
	mu          sync.Mutex
}

func (t *uploadProgressTracker) Report(uploaded, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.lastReport) >= 1500*time.Millisecond || uploaded >= total {
		elapsed := time.Since(t.uploadStart).Seconds()
		speed := float64(0)
		if elapsed > 0 {
			speed = float64(uploaded) / elapsed
		}
		eta := "-"
		if speed > 0 && total > uploaded {
			remSec := int64(float64(total-uploaded) / speed)
			eta = ext.GetReadableTime(remSec)
		}
		if t.onProgress != nil {
			t.onProgress(uploaded, total, speed, eta)
		}
		t.lastReport = time.Now()
	}
}

func (t *uploadProgressTracker) Chunk(ctx context.Context, state uploader.ProgressState) error {
	t.Report(state.Uploaded, state.Total)
	return nil
}

func isVideoExt(ext string) bool {
	switch ext {
	case ".mp4", ".mkv", ".webm", ".avi", ".mov", ".flv", ".wmv", ".m4v", ".ts":
		return true
	}
	return false
}

func isAudioExt(ext string) bool {
	switch ext {
	case ".mp3", ".m4a", ".flac", ".wav", ".ogg", ".opus", ".aac", ".wma":
		return true
	}
	return false
}

func getChannelID(id int64) int64 {
	idStr := fmt.Sprintf("%d", id)
	if strings.HasPrefix(idStr, "-100") {
		idStr = strings.TrimPrefix(idStr, "-100")
		var cleanID int64
		_, _ = fmt.Sscanf(idStr, "%d", &cleanID)
		return cleanID
	}
	if id < 0 {
		return -id
	}
	return id
}

type UploadTask struct {
	gid       string
	name      string
	size      int64
	curr      int64
	speed     float64
	eta       string
	startTime time.Time
	status    types.TaskStatus
	cancelFn  context.CancelFunc
	chatID    int64
	userID    int64
	msgID     int
	mu        sync.RWMutex
}

func NewUploadTask(gid, name string, size int64, chatID, userID int64, msgID int, cancelFn context.CancelFunc) *UploadTask {
	return &UploadTask{
		gid:       gid,
		name:      name,
		size:      size,
		status:    types.StatusUploading,
		startTime: time.Now(),
		cancelFn:  cancelFn,
		chatID:    chatID,
		userID:    userID,
		msgID:     msgID,
	}
}

func (t *UploadTask) GID() string              { return t.gid }
func (t *UploadTask) Name() string             { return t.name }
func (t *UploadTask) Size() int64              { return t.size }
func (t *UploadTask) DownloadedBytes() int64   { return t.curr }
func (t *UploadTask) Speed() float64           { return t.speed }
func (t *UploadTask) Status() types.TaskStatus { return t.status }
func (t *UploadTask) Engine() string           { return "MTProto" }
func (t *UploadTask) StartTime() time.Time     { return t.startTime }
func (t *UploadTask) MessageID() int           { return t.msgID }
func (t *UploadTask) ChatID() int64            { return t.chatID }
func (t *UploadTask) UserID() int64            { return t.userID }
func (t *UploadTask) Progress() float64 {
	if t.size <= 0 {
		return 0
	}
	return (float64(t.curr) / float64(t.size)) * 100
}
func (t *UploadTask) ETA() string {
	if t.eta != "" {
		return t.eta
	}
	if t.speed <= 0 || t.size <= 0 {
		return "-"
	}
	rem := t.size - t.curr
	if rem <= 0 {
		return "0s"
	}
	return ext.GetReadableTime(int64(float64(rem) / t.speed))
}
func (t *UploadTask) Cancel() error {
	t.mu.Lock()
	cancel := t.cancelFn
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}
func (t *UploadTask) Update(curr, total int64, speed float64, eta string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.curr = curr
	if total > 0 {
		t.size = total
	}
	t.speed = speed
	t.eta = eta
}
