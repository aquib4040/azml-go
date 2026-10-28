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
	client           *tg.Client
	globalFloodUntil atomic.Int64
	lastReqTime      atomic.Int64
	minIntervalNs    atomic.Int64
}

func NewTelegramChunkDownloader(client *tg.Client) *TelegramChunkDownloader {
	d := &TelegramChunkDownloader{
		client: client,
	}
	// Default minimum interval between chunk requests: 50ms (~20 MB/s line capacity).
	// Keeps throughput fast while avoiding MTProto connection queue drop.
	d.minIntervalNs.Store(50 * int64(time.Millisecond))
	return d
}

func (d *TelegramChunkDownloader) decayPacing() {
	curr := d.minIntervalNs.Load()
	base := int64(50 * time.Millisecond)
	if curr > base {
		d.minIntervalNs.Store(curr - int64(5*time.Millisecond))
	}
}

func (d *TelegramChunkDownloader) waitGlobalFlood(ctx context.Context) error {
	untilNano := d.globalFloodUntil.Load()
	if untilNano == 0 {
		return nil
	}
	waitDur := time.Until(time.Unix(0, untilNano))
	if waitDur > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDur):
		}
	}
	return nil
}

func (d *TelegramChunkDownloader) triggerGlobalFlood(dur time.Duration) {
	target := time.Now().Add(dur).UnixNano()
	for {
		curr := d.globalFloodUntil.Load()
		if curr >= target {
			break
		}
		if d.globalFloodUntil.CompareAndSwap(curr, target) {
			break
		}
	}
}

func (d *TelegramChunkDownloader) paceRequest(ctx context.Context) error {
	interval := d.minIntervalNs.Load()
	if interval <= 0 {
		return nil
	}

	for {
		now := time.Now().UnixNano()
		last := d.lastReqTime.Load()
		target := last + interval
		if target > now {
			diff := time.Duration(target - now)
			if diff > 0 && diff < 3*time.Second {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(diff):
				}
			}
			now = time.Now().UnixNano()
		}

		if d.lastReqTime.CompareAndSwap(last, now) {
			break
		}
	}
	return nil
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

	// MTProto optimal chunk size: 1MB (1048576) — maximum allowed by Telegram API.
	// Must be divisible by 4096 and a power of 2 up to 1MB.
	const partSize int64 = 1048576 // 1 MB

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

	numWorkers := 5
	if envVal := os.Getenv("DOWNLOAD_THREADS"); envVal != "" {
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
	if totalParts < numWorkers {
		numWorkers = totalParts
	}

	// Pre-compute the actual byte count for each part (last part may be smaller)
	partActualSize := make([]int64, totalParts)
	for i := 0; i < totalParts; i++ {
		off := int64(i) * partSize
		remaining := fileSize - off
		if remaining > partSize {
			partActualSize[i] = partSize
		} else {
			partActualSize[i] = remaining
		}
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
	var lastReportBytes int64
	var lastReportTime = startTime
	var trackerMu sync.Mutex
	var smoothSpeed float64

	reportProgress := func(curr int64) {
		if onProgress == nil {
			return
		}
		trackerMu.Lock()
		defer trackerMu.Unlock()

		now := time.Now()
		elapsed := now.Sub(lastReportTime).Seconds()
		if elapsed >= 0.8 || curr == fileSize {
			delta := curr - lastReportBytes
			instantSpeed := 0.0
			if elapsed > 0 {
				instantSpeed = float64(delta) / elapsed
			}
			if smoothSpeed <= 0 {
				smoothSpeed = instantSpeed
			} else if instantSpeed > 0 {
				smoothSpeed = smoothSpeed*0.7 + instantSpeed*0.3
			}
			speed := smoothSpeed
			eta := "-"
			rem := fileSize - curr
			if speed > 0 && rem > 0 {
				etaSec := int64(float64(rem) / speed)
				eta = ext.GetReadableTime(etaSec)
			}
			onProgress(curr, fileSize, speed, eta)
			lastReportBytes = curr
			lastReportTime = now
		}
	}

	log.Printf("[TG Chunk DL] Starting download: %s (%s), %d parts, %d workers",
		filepath.Base(destPath), ext.GetReadableFileSize(fileSize), totalParts, numWorkers)

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
				actualBytes := partActualSize[partIndex]

				// IMPORTANT: Always request full partSize as limit.
				// Telegram API requires limit to be a valid power-of-2 multiple of 4096 (up to 1MB).
				// For the last chunk, the server returns only the remaining bytes even if we request more.
				// Sending the actual remaining size (e.g. 73123) causes LIMIT_INVALID error.
				requestLimit := int(partSize)

				data, partErr := d.getPartWithRetry(ctx, loc, offset, requestLimit, partIndex, totalParts, refreshRef)
				if partErr != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("download failed at part %d/%d (offset %d): %w", partIndex+1, totalParts, offset, partErr)
					})
					return
				}

				// Trim to actual expected size for the last part (server may return exactly what we need,
				// but if it returns more padding, we must not write past fileSize)
				if int64(len(data)) > actualBytes {
					data = data[:actualBytes]
				}

				if int64(len(data)) < actualBytes {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("incomplete chunk at part %d/%d (offset %d): got %d bytes, expected %d", partIndex+1, totalParts, offset, len(data), actualBytes)
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
	if fi, sErr := out.Stat(); sErr == nil {
		if fi.Size() != fileSize {
			return fmt.Errorf("downloaded file size mismatch: got %d bytes, expected %d bytes", fi.Size(), fileSize)
		}
	}
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
	const maxTransientRetries = 15
	transientAttempts := 0

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Wait if any worker recently received a FloodWait
		if err := d.waitGlobalFlood(ctx); err != nil {
			return nil, err
		}

		// Pace chunk requests to keep throughput smooth without tripping FloodWait
		if err := d.paceRequest(ctx); err != nil {
			return nil, err
		}

		partCtx, partCancel := context.WithTimeout(ctx, 60*time.Second)
		res, err := d.client.UploadGetFile(partCtx, &tg.UploadGetFileRequest{
			Precise:      false,
			CDNSupported: false,
			Location:     loc,
			Offset:       offset,
			Limit:        limit,
		})
		partCancel()

		if err == nil {
			switch f := res.(type) {
			case *tg.UploadFile:
				if len(f.Bytes) == 0 {
					transientAttempts++
					if transientAttempts > maxTransientRetries {
						return nil, fmt.Errorf("received empty chunk after %d retries", maxTransientRetries)
					}
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(500 * time.Millisecond):
					}
					continue
				}
				d.decayPacing()
				return f.Bytes, nil
			default:
				return nil, fmt.Errorf("unexpected file response %T", res)
			}
		}

		// 1. FloodWait — pause ALL workers globally, adaptively increase pacing interval
		if waitDur, ok := tgerr.AsFloodWait(err); ok {
			if waitDur < 1*time.Second {
				waitDur = 1 * time.Second
			}
			jitter := time.Duration(rand.Intn(300)) * time.Millisecond
			totalWait := waitDur + jitter
			log.Printf("[TG Chunk DL] Part %d/%d FloodWait %v. Pausing all workers for %v...",
				partIndex+1, totalParts, waitDur, totalWait)

			d.triggerGlobalFlood(totalWait)

			// Adaptively back off pacing to match Telegram's current session quota
			currInterval := d.minIntervalNs.Load()
			if currInterval < int64(450*time.Millisecond) {
				d.minIntervalNs.Store(currInterval + int64(40*time.Millisecond))
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(totalWait):
			}
			continue
		}

		// 2. File reference expired — refresh and retry
		if strings.Contains(err.Error(), "FILE_REFERENCE_EXPIRED") && refreshRef != nil {
			log.Printf("[TG Chunk DL] File reference expired at part %d/%d, refreshing...", partIndex+1, totalParts)
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

		// 3. Transient error — exponential backoff with jitter
		transientAttempts++
		if transientAttempts > maxTransientRetries {
			return nil, fmt.Errorf("part %d/%d failed after %d transient attempts: %w", partIndex+1, totalParts, maxTransientRetries, err)
		}

		backoff := time.Duration(transientAttempts) * 500 * time.Millisecond
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
		jitter := time.Duration(rand.Intn(400)) * time.Millisecond
		log.Printf("[TG Chunk DL] Part %d/%d error: %v. Retrying in %v (transient attempt %d/%d)",
			partIndex+1, totalParts, err, backoff+jitter, transientAttempts, maxTransientRetries)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff + jitter):
		}
	}
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
