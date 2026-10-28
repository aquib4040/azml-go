package downloader

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"runtime/debug"
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

var globalActiveDownloads atomic.Int64

func computeTargetWorkers(activeDownloads int64, availableBots int, numTasks int) int {
	if availableBots <= 0 {
		availableBots = 1
	}
	if activeDownloads <= 0 {
		activeDownloads = 1
	}

	// Environment variable manual override
	if envVal := os.Getenv("DOWNLOAD_THREADS"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			if val > numTasks {
				val = numTasks
			}
			return val
		}
	}

	// Fair allocation:
	// 1 active download: 6 workers (saturates multi-bot pool without spam).
	// 2 to 4 active downloads: 5 workers each.
	// 5+ active downloads: 4 workers each.
	var target int
	if activeDownloads <= 1 {
		target = 6
	} else if activeDownloads <= 4 {
		target = 5
	} else {
		target = 4
	}

	// When availableBots is constrained (e.g. fewer than 8 bots available due to uploads),
	// clamp target so total download workers do not excessively overwhelm the available pool.
	if availableBots > 0 && availableBots < 8 && int(activeDownloads) > 0 {
		maxPerDL := availableBots / int(activeDownloads)
		if maxPerDL < 2 {
			maxPerDL = 2
		}
		if target > maxPerDL {
			target = maxPerDL
		}
	}

	if target > numTasks {
		target = numTasks
	}
	if target < 2 {
		target = 2
	}
	return target
}

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
	defer func() { _ = out.Close() }()

	_ = out.Truncate(fileSize)

	availableBots := m.pool.AvailableDownloadBotsCount()
	activeDownloads := globalActiveDownloads.Add(1)
	defer globalActiveDownloads.Add(-1)

	numWorkers := computeTargetWorkers(activeDownloads, availableBots, totalParts)
	bufferLimit := numWorkers * 2

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

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	tasks := make(chan int, bufferLimit)

	// Feeder goroutine: feeds parts to workers bounded strictly by bufferLimit
	go func() {
		defer close(tasks)
		for i := 0; i < totalParts; i++ {
			select {
			case <-workerCtx.Done():
				return
			case tasks <- i:
			}
		}
	}()

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

	log.Printf("[MultiBot DL] 🚀 Starting download: %s (%s), %d parts, %d workers (Active DLs: %d, Avail Bots: %d)",
		filepath.Base(destPath), ext.GetReadableFileSize(fileSize), totalParts, numWorkers, activeDownloads, availableBots)

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[MultiBot DL] Worker %d panic recovered: %v\n%s", workerID, r, debug.Stack())
				}
			}()

			for partIndex := range tasks {
				if firstErr != nil || workerCtx.Err() != nil {
					return
				}

				offset := int64(partIndex) * partSize
				actualBytes := partActualSize[partIndex]
				requestLimit := int(partSize)

				data, partErr := m.fetchChunkWithRetry(workerCtx, dumpChatID, dumpMsgID, offset, requestLimit, partIndex, totalParts)
				if partErr != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("download failed at part %d/%d (offset %d): %w", partIndex+1, totalParts, offset, partErr)
						workerCancel()
					})
					return
				}

				if int64(len(data)) < actualBytes {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("download incomplete at part %d/%d (offset %d): expected %d bytes, got %d", partIndex+1, totalParts, offset, actualBytes, len(data))
						workerCancel()
					})
					return
				}
				if int64(len(data)) > actualBytes {
					data = data[:actualBytes]
				}

				if _, wErr := out.WriteAt(data, offset); wErr != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("failed to write part %d to disk: %w", partIndex+1, wErr)
						workerCancel()
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
			return fmt.Errorf("downloaded file size mismatch: expected %d bytes, got %d bytes", fileSize, fi.Size())
		}
	}
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
			waitDur := time.Duration(200+rand.Intn(200)) * time.Millisecond
			if bErr != nil && strings.Contains(bErr.Error(), "cooling down") {
				// Exponential backoff with jitter when all bots are cooling down
				// Prevents thundering herd when a bot exits cooldown.
				waitDur = time.Duration(1200+rand.Intn(1000)) * time.Millisecond
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitDur):
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
			currentBot.SetCooldown(8)
			log.Printf("[MultiBot DL] ⏱️ Bot ...%s part %d/%d timed out (8s cooldown). Failover to next bot...",
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

	// If this bot hits FloodWait:
	if err != nil {
		if d, ok := tgerr.AsFloodWait(err); ok {
			waitSec := int64(d.Seconds())
			if waitSec < 2 {
				waitSec = 2
			}
			bot.SetCooldown(waitSec + 2)
			// If multi-bot pool has multiple active sessions, do not stall this download worker with a sleep.
			// Return immediately so fetchChunkWithRetry can instantly grab an idle bot from the pool.
			if m.pool != nil && m.pool.ActiveSessionCount() > 1 {
				return nil, err
			}
			time.Sleep(d + 80*time.Millisecond)
			res, err = bot.API.UploadGetFile(ctx, &tg.UploadGetFileRequest{
				Precise:      false,
				CDNSupported: false,
				Location:     location,
				Offset:       offset,
				Limit:        limit,
			})
		}
	}

	// Immediate file reference refresh on expiration
	if err != nil && (strings.Contains(err.Error(), "FILE_REFERENCE_EXPIRED") || strings.Contains(err.Error(), "FILE_REFERENCE_INVALID")) {
		log.Printf("[MultiBot DL] 🔄 Renewing file reference for dump msg %d on bot ...%s", dumpMsgID, bot.SafeSuffix())
		_, _, newLoc, rErr := bot.ResolveDocument(ctx, dumpChatID, dumpMsgID, true)
		if rErr == nil && newLoc != nil {
			location = newLoc
			res, err = bot.API.UploadGetFile(ctx, &tg.UploadGetFileRequest{
				Precise:      false,
				CDNSupported: false,
				Location:     location,
				Offset:       offset,
				Limit:        limit,
			})
		}
	}

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
