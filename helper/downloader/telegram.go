package downloader

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/types"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

type TelegramDownloader struct {
	rawClient *tg.Client
	dl        *downloader.Downloader
}

func NewTelegramDownloader(rawClient *tg.Client) *TelegramDownloader {
	// 512KB chunks for high speed MTProto download
	dl := downloader.NewDownloader().WithPartSize(512 * 1024)
	return &TelegramDownloader{
		rawClient: rawClient,
		dl:        dl,
	}
}

func (t *TelegramDownloader) DownloadDocument(ctx context.Context, loc tg.InputDocumentFileLocation, outPath string, onProgress func(cur, total int64)) error {
	_ = os.MkdirAll(filepath.Dir(outPath), 0755)
	_, err := t.dl.Download(t.rawClient, &loc).WithThreads(8).ToPath(ctx, outPath)
	return err
}

type TelegramDownloadTask struct {
	name      string
	size      int64
	curr      int64
	speed     float64
	startTime time.Time
	status    types.TaskStatus
	cancelFn  context.CancelFunc
	mu        sync.RWMutex
}

func (t *TelegramDownloadTask) GID() string             { return "tgdl" }
func (t *TelegramDownloadTask) Name() string            { return t.name }
func (t *TelegramDownloadTask) Size() int64             { return t.size }
func (t *TelegramDownloadTask) DownloadedBytes() int64  { return t.curr }
func (t *TelegramDownloadTask) Speed() float64          { return t.speed }
func (t *TelegramDownloadTask) Status() types.TaskStatus  { return t.status }
func (t *TelegramDownloadTask) Engine() string          { return "MTProto Downloader" }
func (t *TelegramDownloadTask) StartTime() time.Time    { return t.startTime }
func (t *TelegramDownloadTask) MessageID() int          { return 0 }
func (t *TelegramDownloadTask) ChatID() int64           { return 0 }
func (t *TelegramDownloadTask) UserID() int64           { return 0 }
func (t *TelegramDownloadTask) Progress() float64 {
	if t.size <= 0 {
		return 0
	}
	return (float64(t.curr) / float64(t.size)) * 100
}
func (t *TelegramDownloadTask) ETA() string {
	if t.speed <= 0 || t.size <= 0 {
		return "-"
	}
	rem := t.size - t.curr
	if rem <= 0 {
		return "0s"
	}
	return ext.GetReadableTime(int64(float64(rem) / t.speed))
}
func (t *TelegramDownloadTask) Cancel() error {
	if t.cancelFn != nil {
		t.cancelFn()
	}
	return nil
}

