package uploader

import (
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
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

func saveBigFilePartWithRetry(ctx context.Context, client *tg.Client, fileID int64, partIndex int, totalParts int, bytes []byte) error {
	for attempt := 0; attempt < 10; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		partCtx, partCancel := context.WithTimeout(ctx, 45*time.Second)
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

		if d, ok := tgerr.AsFloodWait(err); ok {
			waitTime := d
			if waitTime < 1*time.Second {
				waitTime = 1 * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waitTime + time.Duration(rand.Intn(400))*time.Millisecond):
			}
			continue
		}

		errStr := err.Error()
		if strings.Contains(errStr, "connection") || strings.Contains(errStr, "reset") ||
			strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline") ||
			strings.Contains(errStr, "canceled") || strings.Contains(errStr, "engine was closed") ||
			strings.Contains(errStr, "engine forcibly closed") || strings.Contains(errStr, "broken pipe") ||
			strings.Contains(errStr, "write tcp") || strings.Contains(errStr, "read tcp") {
			if attempt > 3 {
				log.Printf("[AZML-Go Uploader] Warning: part %d (attempt %d/10) transient error: %s. Retrying...", partIndex, attempt+1, errStr)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1*time.Second + time.Duration(rand.Intn(300))*time.Millisecond):
			}
			continue
		}

		return err
	}
	return fmt.Errorf("exceeded max retries (10) for part %d", partIndex)
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
	var workerSession *pool.BotSession

	if fileSize > twoGB {
		if u.userClient == nil || u.userUp == nil {
			return nil, fmt.Errorf("file size %s exceeds 2GB limit; USER_SESSION_STRING is required to upload files > 2GB (up to 4GB)", ext.GetReadableFileSize(fileSize))
		}
		clientToUse = u.userClient
	} else {
		// Multi-bot upload load balancing:
		// When SessionPool has active sessions, use a worker session to avoid rate-limiting the main bot.
		if u.sessPool != nil && u.sessPool.ActiveSessionCount() > 0 {
			cfg := config.Get()
			if wBot, err := u.sessPool.GetNextAvailable(cfg.WorkerBotTokens, cfg.TelegramAPI, cfg.TelegramHash); err == nil && wBot != nil {
				workerSession = wBot
				clientToUse = wBot.API
				log.Printf("[AZML-Go Uploader] Multi-bot worker session ...%s selected for upload", wBot.SafeSuffix())
			}
		}
		if clientToUse == nil {
			clientToUse = u.botClient
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

		numWorkers := 8
		if envVal := os.Getenv("UPLOAD_THREADS"); envVal != "" {
			if val, pErr := strconv.Atoi(envVal); pErr == nil && val > 0 {
				numWorkers = val
			}
		}
		if numWorkers > 16 {
			numWorkers = 16
		}
		if int(totalParts) < numWorkers {
			numWorkers = int(totalParts)
		}

		var wg sync.WaitGroup
		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
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
				}
			}()
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
	isVideo := isVideoExt(extName) && !opt.AsDoc
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
		}
		details, _ := ext.ProbeMedia(opt.FilePath)
		dur := 0
		w := 0
		h := 0
		if details != nil {
			dur = details.Duration
			w = details.Width
			h = details.Height
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

	fromChatID := telegram.PeerToChatID(peer)
	isGroupChat := (fromChatID != opt.UserID)

	uploadPeer := peer
	actualDestChatID := fromChatID

	effectiveDumpChatID := opt.DumpChatID
	if effectiveDumpChatID == 0 {
		effectiveDumpChatID = u.dumpChatID
	}
	cfg := config.Get()
	if effectiveDumpChatID == 0 && cfg != nil {
		effectiveDumpChatID = cfg.DumpChatID
	}
	if effectiveDumpChatID == 0 && cfg != nil {
		effectiveDumpChatID = cfg.LeechLogID
	}

	if workerSession != nil && effectiveDumpChatID != 0 {
		if inChan, err := workerSession.ResolveChannel(ctx, effectiveDumpChatID); err == nil && inChan != nil {
			uploadPeer = &tg.InputPeerChannel{
				ChannelID:  inChan.ChannelID,
				AccessHash: inChan.AccessHash,
			}
			actualDestChatID = effectiveDumpChatID
			log.Printf("[AZML-Go Uploader] Multi-bot worker uploading to dump channel %d (dest chat %d)", effectiveDumpChatID, fromChatID)
		} else {
			log.Printf("[AZML-Go Uploader] Worker could not resolve dump channel %d: %v. Falling back to main bot.", effectiveDumpChatID, err)
			clientToUse = u.botClient
			workerSession = nil
		}
	} else if isGroupChat {
		// Prefer uploading directly to LeechLogID if its access hash is known
		if opt.LogChatID != 0 {
			chID := getChannelID(opt.LogChatID)
			hash := telegram.GetChannelAccessHash(chID)
			if hash != 0 {
				uploadPeer = &tg.InputPeerChannel{
					ChannelID:  chID,
					AccessHash: hash,
				}
				actualDestChatID = opt.LogChatID
			}
		}
		// If LeechLogID has no cached access hash, prefer uploading directly to User PM
		if uploadPeer == peer && opt.UserID != 0 {
			uHash := telegram.GetUserAccessHash(opt.UserID)
			if uHash != 0 {
				uploadPeer = &tg.InputPeerUser{
					UserID:     opt.UserID,
					AccessHash: uHash,
				}
				actualDestChatID = opt.UserID
			}
		}
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
		// Fallback to command peer if sending to preferred peer failed
		if uploadPeer != peer && attempt == 0 {
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

		// 0. Ensure message is delivered to original chat (group/channel/PM) where leech command was sent
		if !sentChats[fromChatID] {
			sentChats[fromChatID] = true
			if !copyMessageViaBotAPI(botToken, fromChatID, actualDestChatID, msgID) {
				_, fErr := u.botClient.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
					FromPeer:   uploadPeer,
					ToPeer:     peer,
					ID:         []int{msgID},
					RandomID:   []int64{time.Now().UnixNano() + 1},
					DropAuthor: true,
				})
				if fErr != nil {
					log.Printf("[AZML-Go Uploader] Delivery to original chat (%d) failed: %v", fromChatID, fErr)
				}
			}
		}

		// 1. Send to Leech Log ID (if configured and not already sent there)
		if opt.LogChatID != 0 && !sentChats[opt.LogChatID] {
			logChatID := opt.LogChatID
			sentChats[logChatID] = true
			if !copyMessageViaBotAPI(botToken, logChatID, actualDestChatID, msgID) {
				chID := getChannelID(logChatID)
				logPeer := &tg.InputPeerChannel{
					ChannelID:  chID,
					AccessHash: telegram.GetChannelAccessHash(chID),
				}
				_, fErr := clientToUse.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
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

		// 2. Send to Bot PM (User PM) - always ensure user gets the file in PM
		if opt.UserID != 0 && !sentChats[opt.UserID] {
			sentChats[opt.UserID] = true
			if !copyMessageViaBotAPI(botToken, opt.UserID, actualDestChatID, msgID) {
				userPeer := &tg.InputPeerUser{
					UserID:     opt.UserID,
					AccessHash: telegram.GetUserAccessHash(opt.UserID),
				}
				_, pErr := clientToUse.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
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

		// 3. Send to Dump Chat ID if configured
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

		// 4. If uploaded to group chat as fallback, delete it from group chat so GC never keeps the file
		if isGroupChat && actualDestChatID == fromChatID {
			switch p := peer.(type) {
			case *tg.InputPeerChannel:
				accessHash := p.AccessHash
				if accessHash == 0 {
					accessHash = telegram.GetChannelAccessHash(p.ChannelID)
				}
				_, _ = clientToUse.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
					Channel: &tg.InputChannel{
						ChannelID:  p.ChannelID,
						AccessHash: accessHash,
					},
					ID: []int{msgID},
				})
			default:
				_, _ = clientToUse.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
					Revoke: true,
					ID:     []int{msgID},
				})
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
