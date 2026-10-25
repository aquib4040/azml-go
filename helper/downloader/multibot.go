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
	"azml-go/helper/pool"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// MultiBotDownloader downloads files from a dump channel using a pool of authenticated MTProto worker bots.
// Each chunk is load-balanced across worker sessions with per-session anti-flood pacing and seamless failover.
type MultiBotDownloader struct {
	pool         *pool.SessionPool
	workerTokens []string
	apiID        int
	apiHash      string
}

func NewMultiBotDownloader(sessPool *pool.SessionPool, workerTokens []string, apiID int, apiHash string) *MultiBotDownloader {
	return &MultiBotDownloader{
		pool:         sessPool,
		workerTokens: workerTokens,
		apiID:        apiID,
		apiHash:      apiHash,
	}
}

// DownloadToFile downloads a Telegram document from the dump channel to disk using the multi-bot pool.
func (m *MultiBotDownloader) DownloadToFile(
	ctx context.Context,
	dumpChatID int64,
	dumpMsgID int64,
	fileSize int64,
	destPath string,
	onProgress DownloadProgressFunc,
) error {
	_ = os.MkdirAll(filepath.Dir(destPath), 0777)

	primaryBot, err := m.pool.GetNextAvailable(m.workerTokens, m.apiID, m.apiHash)
	if err != nil {
		return fmt.Errorf("no worker bot session available in pool: %w", err)
	}

	primaryBot.StartDownload()
	defer primaryBot.EndDownload()

	docSize, _, _, resolveErr := primaryBot.ResolveDocument(ctx, dumpChatID, dumpMsgID, false)
	if resolveErr != nil {
		docSize, _, _, resolveErr = primaryBot.ResolveDocument(ctx, dumpChatID, dumpMsgID, true)
		if resolveErr != nil {
			return fmt.Errorf("failed to resolve document from dump channel %d (msg %d): %w", dumpChatID, dumpMsgID, resolveErr)
		}
	}
	if fileSize <= 0 {
		fileSize = docSize
	}
	if fileSize <= 0 {
		return fmt.Errorf("invalid file size %d for multi-bot download", fileSize)
	}

	const partSize int64 = 1048576 // 1 MB chunk size (Telegram MTProto maximum)

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

	activeBots := m.pool.ActiveSessionCount()
	if activeBots <= 0 {
		activeBots = 1
	}

	// Compute concurrency based on active bots in pool
	numWorkers := activeBots * 2
	if numWorkers < 4 {
		numWorkers = 4
	}
	if numWorkers > 16 {
		numWorkers = 16
	}
	if envVal := os.Getenv("DOWNLOAD_THREADS"); envVal != "" {
		if val, pErr := strconv.Atoi(envVal); pErr == nil && val > 0 {
			numWorkers = val
		}
	}
	if totalParts < numWorkers {
		numWorkers = totalParts
	}

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

	reportProgress := func(curr int64) {
		if onProgress == nil {
			return
		}
		trackerMu.Lock()
		defer trackerMu.Unlock()

		now := time.Now()
		elapsed := now.Sub(lastReportTime).Seconds()
		if elapsed >= 0.5 || curr == fileSize {
			delta := curr - lastReportBytes
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
			lastReportBytes = curr
			lastReportTime = now
		}
	}

	log.Printf("[MultiBot DL] 🚀 Starting download: %s (%s), %d parts, %d workers across %d MTProto bots",
		filepath.Base(destPath), ext.GetReadableFileSize(fileSize), totalParts, numWorkers, activeBots)

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
				requestLimit := int(partSize)

				data, partErr := m.fetchChunkWithRetry(ctx, dumpChatID, dumpMsgID, offset, requestLimit, partIndex, totalParts)
				if partErr != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("download failed at part %d/%d (offset %d): %w", partIndex+1, totalParts, offset, partErr)
					})
					return
				}

				if int64(len(data)) > actualBytes {
					data = data[:actualBytes]
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

func (m *MultiBotDownloader) fetchChunkWithRetry(
	ctx context.Context,
	dumpChatID int64,
	dumpMsgID int64,
	offset int64,
	limit int,
	partIndex int,
	totalParts int,
) ([]byte, error) {
	const maxAttempts = 15
	failedTokens := make(map[string]bool)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		currentBot, bErr := m.pool.AcquireLeastLoadedBot(m.workerTokens, m.apiID, m.apiHash, failedTokens)
		if bErr != nil || currentBot == nil {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(200+rand.Intn(200)) * time.Millisecond):
			}
			continue
		}

		data, err := m.fetchChunkWithBot(ctx, currentBot, dumpChatID, dumpMsgID, offset, limit)
		m.pool.ReleaseBotLoad(currentBot)

		if err == nil && len(data) > 0 {
			return data, nil
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		failedTokens[currentBot.Token] = true

		if d, ok := tgerr.AsFloodWait(err); ok {
			waitSec := int64(d.Seconds())
			if waitSec < 2 {
				waitSec = 2
			}
			currentBot.SetCooldown(waitSec)
			log.Printf("[MultiBot DL] ⚡ Bot ...%s hit FloodWait %ds on part %d/%d. Failover to next bot...",
				currentBot.SafeSuffix(), waitSec, partIndex+1, totalParts)
		} else if err != nil && strings.Contains(err.Error(), "FLOOD_WAIT") {
			currentBot.SetCooldown(3)
			log.Printf("[MultiBot DL] ⚡ Bot ...%s hit FloodWait on part %d/%d. Failover to next bot...",
				currentBot.SafeSuffix(), partIndex+1, totalParts)
		} else if err != nil && (strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline")) {
			currentBot.SetCooldown(10)
			log.Printf("[MultiBot DL] ⏱️ Bot ...%s part %d/%d timed out. Failover to next bot...",
				currentBot.SafeSuffix(), partIndex+1, totalParts)
		} else if err != nil && (strings.Contains(err.Error(), "FILE_REFERENCE_EXPIRED") || strings.Contains(err.Error(), "FILE_REFERENCE_INVALID")) {
			log.Printf("[MultiBot DL] 🔄 Renewing file reference for dump msg %d on bot ...%s", dumpMsgID, currentBot.SafeSuffix())
			_, _, _, _ = currentBot.ResolveDocument(ctx, dumpChatID, dumpMsgID, true)
		}

		if len(failedTokens) >= m.pool.ActiveSessionCount() {
			failedTokens = make(map[string]bool)
		}
	}

	return nil, fmt.Errorf("part %d/%d failed after %d attempts across worker bots", partIndex+1, totalParts, maxAttempts)
}

func (m *MultiBotDownloader) fetchChunkWithBot(
	parentCtx context.Context,
	bot *pool.BotSession,
	dumpChatID int64,
	dumpMsgID int64,
	offset int64,
	limit int,
) ([]byte, error) {
	if bot.IsCooling() {
		return nil, fmt.Errorf("bot is cooling down")
	}

	ctx, cancel := context.WithTimeout(parentCtx, 25*time.Second)
	defer cancel()

	// Anti-flood lock and 120ms pacing per bot session
	bot.LockChunk()
	defer bot.UnlockChunk()

	if bot.IsCooling() {
		return nil, fmt.Errorf("bot entered cooldown")
	}

	_, _, location, err := bot.ResolveDocument(ctx, dumpChatID, dumpMsgID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve document: %w", err)
	}

	res, err := bot.API.UploadGetFile(ctx, &tg.UploadGetFileRequest{
		Precise:      false,
		CDNSupported: false,
		Location:     location,
		Offset:       offset,
		Limit:        limit,
	})
	if err != nil {
		return nil, err
	}

	if f, ok := res.(*tg.UploadFile); ok {
		if len(f.Bytes) == 0 {
			return nil, fmt.Errorf("received 0 bytes from server")
		}
		return f.Bytes, nil
	}

	return nil, fmt.Errorf("unexpected file response %T", res)
}
