package uploader

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/types"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

type TelegramUploader struct {
	botClient  *tg.Client
	botUp      *uploader.Uploader
	userClient *tg.Client
	userUp     *uploader.Uploader
}

func NewTelegramUploader(botClient *tg.Client, userClient *tg.Client) *TelegramUploader {
	// 512KB chunks with 16 parallel workers for blazing-fast upload speed
	botUp := uploader.NewUploader(botClient).WithPartSize(512 * 1024).WithThreads(16)
	var userUp *uploader.Uploader
	if userClient != nil {
		userUp = uploader.NewUploader(userClient).WithPartSize(512 * 1024).WithThreads(16)
	}
	return &TelegramUploader{
		botClient:  botClient,
		botUp:      botUp,
		userClient: userClient,
		userUp:     userUp,
	}
}

type UploadProgressFunc func(curr, total int64, speed float64, eta string)

type UploadOptions struct {
	FilePath     string
	FileName     string
	Caption      string
	ThumbPath    string
	CoverPath    string
	AsDoc        bool
	DumpChatID   int64
	LogChatID    int64
	IsPremium    bool
	OnProgress   UploadProgressFunc
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
	var uploaderToUse *uploader.Uploader

	if fileSize > twoGB {
		if u.userClient == nil || u.userUp == nil {
			return nil, fmt.Errorf("file size %s exceeds 2GB limit; USER_SESSION_STRING is required to upload files > 2GB (up to 4GB)", ext.GetReadableFileSize(fileSize))
		}
		log.Printf("[AZML-Go User Client] Uploading large file (%s > 2GB) using User Session with 16 parallel threads...", ext.GetReadableFileSize(fileSize))
		clientToUse = u.userClient
		uploaderToUse = u.userUp
	} else {
		log.Printf("[AZML-Go Bot Client] Uploading file (%s <= 2GB) using Bot Client with 16 parallel threads...", ext.GetReadableFileSize(fileSize))
		clientToUse = u.botClient
		uploaderToUse = u.botUp
	}

	uploadStart := time.Now()
	var lastReport time.Time

	// Stream upload directly from disk with progress wrapper
	fileHandle, err := os.Open(opt.FilePath)
	if err != nil {
		return nil, err
	}
	defer fileHandle.Close()

	progressReader := &progressReaderWrapper{
		reader: fileHandle,
		total:  fileSize,
		onProgress: func(curr int64) {
			if opt.OnProgress != nil && time.Since(lastReport) >= 1*time.Second {
				elapsed := time.Since(uploadStart).Seconds()
				speed := float64(0)
				if elapsed > 0 {
					speed = float64(curr) / elapsed
				}
				eta := "-"
				if speed > 0 {
					remSec := int64(float64(fileSize-curr) / speed)
					eta = fmt.Sprintf("%ds", remSec)
				}
				opt.OnProgress(curr, fileSize, speed, eta)
				lastReport = time.Now()
			}
		},
	}

	uploadName := opt.FileName
	if uploadName == "" {
		uploadName = filepath.Base(opt.FilePath)
	}

	uploadPayload := uploader.NewUpload(uploadName, progressReader, fileSize)
	uploadedFile, err := uploaderToUse.Upload(ctx, uploadPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file parts: %v", err)
	}

	var thumbFile tg.InputFileClass
	var coverPhoto tg.InputPhotoClass

	// Upload Legacy Thumbnail (320x320)
	if opt.ThumbPath != "" {
		if thumbHandle, tErr := uploader.NewUploader(clientToUse).FromPath(ctx, opt.ThumbPath); tErr == nil {
			thumbFile = thumbHandle
		}
	}

	// Upload Modern MTProto HD VideoCover (thumb=cover)
	if isVideoExt(strings.ToLower(filepath.Ext(opt.FilePath))) && !opt.AsDoc {
		targetCoverPath := opt.CoverPath
		if targetCoverPath == "" {
			targetCoverPath = opt.ThumbPath
		}
		if targetCoverPath != "" {
			if coverHandle, cErr := uploader.NewUploader(clientToUse).FromPath(ctx, targetCoverPath); cErr == nil {
				uploadMediaReq := &tg.MessagesUploadMediaRequest{
					Peer: peer,
					Media: &tg.InputMediaUploadedPhoto{
						File: coverHandle,
					},
				}
				if mediaRes, mErr := clientToUse.MessagesUploadMedia(ctx, uploadMediaReq); mErr == nil {
					if mediaPhoto, ok := mediaRes.(*tg.MessageMediaPhoto); ok {
						if photo, ok := mediaPhoto.Photo.(*tg.Photo); ok {
							coverPhoto = photo.AsInput()
						}
					}
				}
			}
		}
	}

	var attributes []tg.DocumentAttributeClass
	extName := strings.ToLower(filepath.Ext(opt.FilePath))
	isVideo := isVideoExt(extName) && !opt.AsDoc
	isAudio := isAudioExt(extName) && !opt.AsDoc

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
		Thumb:      thumbFile,
		MimeType:   mimeType,
		Attributes: attributes,
	}
	if coverPhoto != nil {
		inputMedia.SetVideoCover(coverPhoto)
	}

	req := &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    inputMedia,
		Message:  opt.Caption,
		RandomID: time.Now().UnixNano(),
	}

	res, err := clientToUse.MessagesSendMedia(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to send uploaded media: %v", err)
	}

	// Mirror to Dump chat if configured
	if opt.DumpChatID != 0 {
		dumpPeer := &tg.InputPeerChannel{
			ChannelID: getChannelID(opt.DumpChatID),
		}
		_, _ = clientToUse.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
			Peer:     dumpPeer,
			Media:    inputMedia,
			Message:  opt.Caption,
			RandomID: time.Now().UnixNano() + 1,
		})
	}

	// Mirror to Leech Log channel if configured
	if opt.LogChatID != 0 {
		logPeer := &tg.InputPeerChannel{
			ChannelID: getChannelID(opt.LogChatID),
		}
		_, _ = clientToUse.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
			Peer:     logPeer,
			Media:    inputMedia,
			Message:  fmt.Sprintf("📂 <b>Leech Log:</b>\n%s", opt.Caption),
			RandomID: time.Now().UnixNano() + 2,
		})
	}

	return &res, nil
}

type progressReaderWrapper struct {
	reader     *os.File
	total      int64
	curr       int64
	onProgress func(int64)
	mu         sync.Mutex
}

func (p *progressReaderWrapper) Read(b []byte) (int, error) {
	n, err := p.reader.Read(b)
	if n > 0 {
		p.mu.Lock()
		p.curr += int64(n)
		curr := p.curr
		p.mu.Unlock()
		if p.onProgress != nil {
			p.onProgress(curr)
		}
	}
	return n, err
}

func (p *progressReaderWrapper) Seek(offset int64, whence int) (int64, error) {
	return p.reader.Seek(offset, whence)
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
	// Standard Telegram channel ID prefix is -100
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

func (t *UploadTask) GID() string             { return t.gid }
func (t *UploadTask) Name() string            { return t.name }
func (t *UploadTask) Size() int64             { return t.size }
func (t *UploadTask) DownloadedBytes() int64  { return t.curr }
func (t *UploadTask) Speed() float64          { return t.speed }
func (t *UploadTask) Status() types.TaskStatus  { return t.status }
func (t *UploadTask) Engine() string          { return "MTProto" }
func (t *UploadTask) StartTime() time.Time    { return t.startTime }
func (t *UploadTask) MessageID() int          { return t.msgID }
func (t *UploadTask) ChatID() int64           { return t.chatID }
func (t *UploadTask) UserID() int64           { return t.userID }
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

