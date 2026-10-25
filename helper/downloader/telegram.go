package downloader

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"azml-go/helper/ext"
	"azml-go/types"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type DownloadProgressFunc func(curr, total int64, speed float64, eta string)

type TelegramChunkDownloader struct {
	client *tg.Client
}

func NewTelegramChunkDownloader(client *tg.Client) *TelegramChunkDownloader {
	return &TelegramChunkDownloader{
		client: client,
	}
}

// DownloadToFile downloads any Telegram document or media to disk using concurrent chunks,
// per-part retries, adaptive FloodWait backoff, and real-time speed tracking without restarting on errors.
func (d *TelegramChunkDownloader) DownloadToFile(
	ctx context.Context,
	loc tg.InputFileLocationClass,
	fileSize int64,
	destPath string,
	onProgress DownloadProgressFunc,
	refreshRef func() ([]byte, error),
) error {
	_ = os.MkdirAll(filepath.Dir(destPath), 0777)

	if fileSize <= 0 {
		return fmt.Errorf("invalid file size %d for chunk download", fileSize)
	}

	// Optimal MTProto chunk size: 512KB for standard, 1024KB (1MB) for files >= 50MB
	partSize := int64(512 * 1024)
	if fileSize >= 50*1024*1024 {
		partSize = 1024 * 1024
	}

	totalParts := int((fileSize + partSize - 1) / partSize)
	if totalParts <= 0 {
		totalParts = 1
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return fmt.Errorf("failed to open destination file %s: %w", destPath, err)
	}
	defer out.Close()

	_ = out.Truncate(fileSize)

	numWorkers := 8
	if envVal := os.Getenv("DOWNLOAD_THREADS"); envVal != "" {
		if val, pErr := strconv.Atoi(envVal); pErr == nil && val > 0 {
			numWorkers = val
		}
	}
	if numWorkers > 16 {
		numWorkers = 16
	}
	if totalParts < numWorkers {
		numWorkers = totalParts
	}

	tasks := make(chan int, totalParts)
	for i := 0; i < totalParts; i++ {
		tasks <- i
	}
	close(tasks)

	var downloadedBytes atomic.Int64
	var errOnce sync.Once
	var firstErr error

	startTime := time.Now()
	var lastBytes int64
	var lastTime = startTime
	var trackerMu sync.Mutex

	reportProgress := func(curr int64) {
		if onProgress == nil {
			return
		}
		trackerMu.Lock()
		defer trackerMu.Unlock()

		now := time.Now()
		elapsed := now.Sub(lastTime).Seconds()
		if elapsed >= 0.5 || curr == fileSize {
			delta := curr - lastBytes
			speed := 0.0
			if elapsed > 0 {
				speed = float64(delta) / elapsed
			}
			eta := "-"
			rem := fileSize - curr
			if speed > 0 && rem > 0 {
				etaSec := int64(float64(rem) / speed)
				eta = ext.GetReadableTime(etaSec)
			}
			onProgress(curr, fileSize, speed, eta)
			lastBytes = curr
			lastTime = now
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for partIndex := range tasks {
				if firstErr != nil || ctx.Err() != nil {
					return
				}

				offset := int64(partIndex) * partSize
				limit := int(partSize)
				if offset+int64(limit) > fileSize {
					limit = int(fileSize - offset)
				}

				data, partErr := d.getPartWithRetry(ctx, loc, offset, limit, partIndex, totalParts, refreshRef)
				if partErr != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("download failed at part %d/%d (offset %d): %w", partIndex+1, totalParts, offset, partErr)
					})
					return
				}

				if _, wErr := out.WriteAt(data, offset); wErr != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("failed to write part %d to disk: %w", partIndex+1, wErr)
					})
					return
				}

				done := downloadedBytes.Add(int64(len(data)))
				reportProgress(done)
			}
		}(w)
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	_ = out.Sync()
	reportProgress(fileSize)
	return nil
}

func (d *TelegramChunkDownloader) getPartWithRetry(
	ctx context.Context,
	loc tg.InputFileLocationClass,
	offset int64,
	limit int,
	partIndex int,
	totalParts int,
	refreshRef func() ([]byte, error),
) ([]byte, error) {
	const maxAttempts = 15

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		partCtx, partCancel := context.WithTimeout(ctx, 45*time.Second)
		res, err := d.client.UploadGetFile(partCtx, &tg.UploadGetFileRequest{
			Precise:      true,
			CDNSupported: false,
			Location:     loc,
			Offset:       offset,
			Limit:        limit,
		})
		partCancel()

		if err == nil {
			switch f := res.(type) {
			case *tg.UploadFile:
				return f.Bytes, nil
			default:
				return nil, fmt.Errorf("unexpected file response %T", res)
			}
		}

		// 1. Check FloodWait
		if waitSec, ok := tgerr.AsFloodWait(err); ok {
			waitTime := waitSec
			if waitTime < 1*time.Second {
				waitTime = 1 * time.Second
			}
			jitter := time.Duration(rand.Intn(400)) * time.Millisecond
			log.Printf("[TG Chunk DL] Part %d/%d got FloodWait %v. Worker pausing and retrying (attempt %d/%d)...",
				partIndex+1, totalParts, waitTime, attempt, maxAttempts)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitTime + jitter):
			}
			continue
		}

		// 2. Check File Reference Expired
		if strings.Contains(err.Error(), "FILE_REFERENCE_EXPIRED") && refreshRef != nil {
			log.Printf("[TG Chunk DL] File reference expired at part %d/%d. Refreshing reference...", partIndex+1, totalParts)
			if newRef, rErr := refreshRef(); rErr == nil && len(newRef) > 0 {
				if docLoc, ok := loc.(*tg.InputDocumentFileLocation); ok {
					docLoc.FileReference = newRef
				}
				continue
			}
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// 3. Transient error or timeout: Exponential backoff with jitter
		backoff := time.Duration(attempt) * 500 * time.Millisecond
		if backoff > 4*time.Second {
			backoff = 4 * time.Second
		}
		jitter := time.Duration(rand.Intn(300)) * time.Millisecond
		log.Printf("[TG Chunk DL] Part %d/%d transient error (%v). Retrying in %v (attempt %d/%d)...",
			partIndex+1, totalParts, err, backoff+jitter, attempt, maxAttempts)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff + jitter):
		}
	}

	return nil, fmt.Errorf("failed after %d attempts", maxAttempts)
}

type TelegramDownloader struct {
	rawClient *tg.Client
	dl        *downloader.Downloader
}

func NewTelegramDownloader(rawClient *tg.Client) *TelegramDownloader {
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

func (t *TelegramDownloadTask) GID() string              { return "tgdl" }
func (t *TelegramDownloadTask) Name() string             { return t.name }
func (t *TelegramDownloadTask) Size() int64              { return t.size }
func (t *TelegramDownloadTask) DownloadedBytes() int64   { return t.curr }
func (t *TelegramDownloadTask) Speed() float64           { return t.speed }
func (t *TelegramDownloadTask) Status() types.TaskStatus { return t.status }
func (t *TelegramDownloadTask) Engine() string           { return "MTProto Downloader" }
func (t *TelegramDownloadTask) StartTime() time.Time     { return t.startTime }
func (t *TelegramDownloadTask) MessageID() int           { return 0 }
func (t *TelegramDownloadTask) ChatID() int64            { return 0 }
func (t *TelegramDownloadTask) UserID() int64            { return 0 }
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
